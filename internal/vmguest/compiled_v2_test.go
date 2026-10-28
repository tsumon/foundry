package vmguest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestV2CompiledInventoryExactAndFrozen(t *testing.T) {
	for _, bad := range []string{"", "extra", "declaration", "symlink", "missing", "directory"} {
		t.Run(bad, func(t *testing.T) {
			root := t.TempDir()
			_ = os.Mkdir(filepath.Join(root, "tests"), 0700)
			_ = os.Mkdir(filepath.Join(root, "node_modules"), 0700)
			_ = os.WriteFile(filepath.Join(root, "tests/a.js"), []byte("owned"), 0600)
			_ = os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0600)
			switch bad {
			case "extra":
				_ = os.WriteFile(filepath.Join(root, "extra.js"), nil, 0600)
			case "declaration":
				_ = os.WriteFile(filepath.Join(root, "a.d.js"), nil, 0600)
			case "symlink":
				_ = os.Symlink("a.js", filepath.Join(root, "tests/link.js"))
			case "missing":
				_ = os.Remove(filepath.Join(root, "tests/a.js"))
			case "directory":
				_ = os.Mkdir(filepath.Join(root, "unexpected"), 0700)
			}
			digest, e := compiledInventoryV2(root, []string{"a.d.ts", "tests/a.ts"})
			if (e == nil) != (bad == "") {
				t.Fatal("inventory result", bad, digest, e)
			}
			if bad == "" && len(digest) != 64 {
				t.Fatal("missing digest")
			}
		})
	}
}
func TestV2StaticBoundaryIdentityFDEnvironmentAndOrder(t *testing.T) {
	good := childObservationV2{uid: 65534, euid: 65534, gid: 65534, egid: 65534, ppid: 1, nnp: 1, groups: []int{}, env: append([]string{}, envV2...), cwd: "/workspace", stdinNull: true, stdoutPipe: true, stderrPipe: true, reportPipe: true, contractReadonly: true, contractSealed: true, privateDescriptors: true}
	if validateChildObservationV2(testNodeV2, good) != nil {
		t.Fatal("good static boundary")
	}
	for _, name := range []string{"uid", "nnp", "groups", "env", "cwd", "stdin", "report", "contract", "seal", "private"} {
		o := good
		switch name {
		case "uid":
			o.euid = 0
		case "nnp":
			o.nnp = 0
		case "groups":
			o.groups = []int{1}
		case "env":
			o.env = append(append([]string{}, envV2...), "NODE_OPTIONS=bad")
		case "cwd":
			o.cwd = "/tmp"
		case "stdin":
			o.stdinNull = false
		case "report":
			o.reportPipe = false
		case "contract":
			o.contractReadonly = false
		case "seal":
			o.contractSealed = false
		case "private":
			o.privateDescriptors = false
		}
		if validateChildObservationV2(testNodeV2, o) == nil {
			t.Fatal("unsafe child accepted", name)
		}
	}
	good.reportPipe = false
	if validateChildObservationV2(compileTypeScriptV2, good) != nil {
		t.Fatal("closed FD3 compile")
	}
	good.reportPipe = true
	if validateChildObservationV2(compileTypeScriptV2, good) == nil {
		t.Fatal("compile inherited report")
	}
}
