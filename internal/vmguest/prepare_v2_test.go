package vmguest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"foundry/internal/vmruntime"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestV2StagingRejectsAliasesSymlinksAndAllocation(t *testing.T) {
	root := t.TempDir()
	s, e := newStagerV2(filepath.Join(root, "source"), filepath.Join(root, "deps"), 65536)
	if e != nil {
		t.Fatal(e)
	}
	f := vmruntime.SourceFileV2{Path: "a/b.js", Mode: 0644, Size: 1}
	if e = s.Stage(1, 0, f, strings.NewReader("x")); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"A/c.js", "a/b.js", "../bad", "/bad", "a/b.js/file"} {
		f.Path = p
		if s.Stage(1, 1, f, strings.NewReader("x")) == nil {
			t.Fatal("unsafe path staged", p)
		}
	}
	if e = os.Symlink(root, filepath.Join(s.source, "link")); e != nil {
		t.Fatal(e)
	}
	f.Path = "link/out"
	if s.Stage(1, 1, f, strings.NewReader("x")) == nil {
		t.Fatal("followed staged symlink")
	}
	s, e = newStagerV2(filepath.Join(root, "source2"), filepath.Join(root, "deps2"), 65536)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 256; i++ {
		f.Path = strings.Repeat("a", 1) + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if e = s.Stage(1, uint32(i), f, strings.NewReader("x")); e != nil {
			t.Fatal(e)
		}
	}
	f.Path = "overflow"
	if s.Stage(1, 256, f, strings.NewReader("x")) == nil {
		t.Fatal("native page allocation budget not enforced")
	}
	if _, e = newStagerV2(filepath.Join(root, "bad"), filepath.Join(root, "bad2"), 3000); e == nil {
		t.Fatal("unknown page geometry")
	}
}
func TestV2StagingShortBodyAndFreezeModes(t *testing.T) {
	root := t.TempDir()
	s, e := newStagerV2(filepath.Join(root, "source"), filepath.Join(root, "deps"), 4096)
	if e != nil {
		t.Fatal(e)
	}
	if s.Stage(2, 0, vmruntime.SourceFileV2{Path: "pkg/a", Size: 2}, strings.NewReader("x")) == nil {
		t.Fatal("short body")
	}
	if e = s.Stage(1, 0, vmruntime.SourceFileV2{Path: "src/a", Mode: 0755, Size: 1}, strings.NewReader("x")); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Chmod(s.source, 0700); _ = os.Chmod(filepath.Join(s.source, "src"), 0700) })
	if e = freezeTreeV2(s.source); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{s.source, filepath.Join(s.source, "src"), filepath.Join(s.source, "src/a")} {
		st, e := os.Lstat(p)
		if e != nil {
			t.Fatal(e)
		}
		mode := os.FileMode(0444)
		if st.IsDir() {
			mode = 0555
		}
		if st.Mode().Perm() != mode {
			t.Fatal(st.Mode())
		}
	}
}
func TestV2MountObservationsEachNestedMount(t *testing.T) {
	good := func(p string) (mountObservationV2, error) {
		spec := mountSpecsV2()[p]
		return mountObservationV2{bytes: spec.bytes, inodes: spec.inodes, tmpfs: true, nosuid: true, nodev: true, noexec: spec.noexec, readonly: true}, nil
	}
	if checkInputMountsV2(good) != nil {
		t.Fatal("good mounts")
	}
	for _, p := range []string{"/workspace", "/foundry-deps", "/workspace/node_modules", "/foundry-build/compiled/node_modules"} {
		if checkInputMountsV2(func(s string) (mountObservationV2, error) {
			o, e := good(s)
			if s == p {
				o.readonly = false
			}
			return o, e
		}) == nil {
			t.Fatal("nested mount writable", p)
		}
	}
	for _, field := range []string{"bytes", "inodes", "type", "nosuid", "nodev", "noexec"} {
		if checkInputMountsV2(func(s string) (mountObservationV2, error) {
			o, e := good(s)
			if s == "/workspace" {
				switch field {
				case "bytes":
					o.bytes++
				case "inodes":
					o.inodes--
				case "type":
					o.tmpfs = false
				case "nosuid":
					o.nosuid = false
				case "nodev":
					o.nodev = false
				case "noexec":
					o.noexec = false
				}
			}
			return o, e
		}) == nil {
			t.Fatal("unsafe mount", field)
		}
	}
}
func TestV2RuntimeInventorySelectedBytesAndPermissions(t *testing.T) {
	raw, e := os.ReadFile("../vmruntime/testdata/runtime-inventory-v2.json")
	if e != nil {
		t.Fatal(e)
	}
	inv, e := vmruntime.ParseRuntimeInventoryV2(raw)
	if e != nil {
		t.Fatal(e)
	}
	data := []byte("owned")
	h := sha256.Sum256(data)
	sum := hex.EncodeToString(h[:])
	inv.Supervisor.Size, inv.Supervisor.SHA256 = int64(len(data)), sum
	inv.Node.Executable.Size, inv.Node.Executable.SHA256 = int64(len(data)), sum
	inv.Runners[0].File.Size, inv.Runners[0].File.SHA256 = int64(len(data)), sum
	raw, _ = inv.CanonicalBytes()
	q := probeV2(t, "c4_node_model_v2")
	q.Target.Identity.VerifierDigest = sum
	q.Target.Identity.GuestPlatform = inv.GuestPlatform
	if inv.GuestPlatform == "linux/arm64" {
		q.Target.Identity.HostPlatform = "darwin/arm64"
	}
	q.Target.RuntimeInventoryDigest, _ = inv.Digest()
	observe := func(p string) (runtimeObservationV2, error) {
		mode := os.FileMode(0444)
		if p == "/init" || p == inv.Node.Executable.Path {
			mode = 0555
		}
		return runtimeObservationV2{regular: true, mode: mode, uid: 0, gid: 0, size: int64(len(data)), sha256: sum}, nil
	}
	if verifyInventoryV2(raw, q.Target, observe) != nil {
		t.Fatal("owned runtime rejected")
	}
	for _, field := range []string{"bytes", "mode", "uid", "type"} {
		if verifyInventoryV2(raw, q.Target, func(p string) (runtimeObservationV2, error) {
			o, e := observe(p)
			if p == inv.Runners[0].File.Path {
				switch field {
				case "bytes":
					o.sha256 = strings.Repeat("f", 64)
				case "mode":
					o.mode = 0644
				case "uid":
					o.uid = 65534
				case "type":
					o.regular = false
				}
			}
			return o, e
		}) == nil {
			t.Fatal("runtime drift", field)
		}
	}
	if verifyInventoryV2(append(bytes.Clone(raw), '\n'), q.Target, observe) == nil {
		t.Fatal("noncanonical inventory")
	}
}
func TestV2MountListRejectsSharesAndUnexpectedNestedMounts(t *testing.T) {
	good := []byte("1 0 0:1 / / rw - rootfs rootfs rw\n2 1 0:2 / /workspace ro - tmpfs tmpfs ro\n")
	if checkMountListV2(good) != nil {
		t.Fatal("closed mounts")
	}
	for _, bad := range []string{"", "1 0 0:1 / /workspace rw - virtiofs host rw\n", "1 0 0:1 / /workspace/hidden ro - tmpfs tmpfs ro\n", "invalid"} {
		if checkMountListV2([]byte(bad)) == nil {
			t.Fatal("unexpected mount", bad)
		}
	}
}
func TestV2ScratchChecksExactSizeFlagsAndWritable(t *testing.T) {
	good := func(p string) (mountObservationV2, error) {
		for _, m := range scratchMounts() {
			if m.path == p {
				return mountObservationV2{bytes: m.maxBytes, inodes: 16384, tmpfs: true, nosuid: true, nodev: true, noexec: m.noexec}, nil
			}
		}
		return mountObservationV2{}, errGuestV2
	}
	if checkScratchV2(good) != nil {
		t.Fatal("good scratch")
	}
	for _, bad := range []string{"bytes", "exec", "readonly"} {
		if checkScratchV2(func(p string) (mountObservationV2, error) {
			o, e := good(p)
			if p == "/foundry-build" {
				switch bad {
				case "bytes":
					o.bytes--
				case "exec":
					o.noexec = true
				case "readonly":
					o.readonly = true
				}
			}
			return o, e
		}) == nil {
			t.Fatal("invalid scratch", bad)
		}
	}
}

// A narrow witness of Linux's relevant bind property, not a mount simulator:
// covering one ancestor nonrecursively hides its existing dependency child.
func TestV2ReadOnlyHierarchyPreservesDependencyAliases(t *testing.T) {
	for _, tc := range []struct{ ancestor, alias string }{{"/workspace", "/workspace/node_modules"}, {"/foundry-deps", "/foundry-deps/python"}, {"/foundry-build/compiled", "/foundry-build/compiled/node_modules"}} {
		t.Run(tc.alias, func(t *testing.T) {
			visible, childRO, parentRO := true, false, false
			ops := []string{}
			bind := func(p string, recursive bool) error {
				ops = append(ops, "bind:"+p)
				if p == tc.ancestor && !recursive {
					visible = false
				}
				return nil
			}
			freeze := func(p string, noexec bool) error {
				if !noexec {
					t.Fatal("lost noexec")
				}
				ops = append(ops, "freeze:"+p)
				switch p {
				case tc.alias:
					childRO = true
				case tc.ancestor:
					parentRO = true
				}
				return nil
			}
			if e := readOnlyHierarchyV2(tc.alias, true, bind, freeze); e != nil {
				t.Fatal(e)
			}
			if e := readOnlyHierarchyV2(tc.ancestor, true, bind, freeze); e != nil {
				t.Fatal(e)
			}
			if !visible || !childRO || !parentRO {
				t.Fatal("alias covered or mutable after ancestor freeze", visible, childRO, parentRO)
			}
			want := []string{"bind:" + tc.alias, "freeze:" + tc.alias, "bind:" + tc.ancestor, "freeze:" + tc.ancestor}
			if !reflect.DeepEqual(ops, want) {
				t.Fatal(ops)
			}
		})
	}
}
func TestV2HierarchyFreezeDoesNotConferChildImmutability(t *testing.T) {
	childRO := false
	bind := func(string, bool) error { return nil }
	freeze := func(p string, noexec bool) error {
		if p == "/workspace/node_modules" {
			childRO = true
		}
		return nil
	}
	if e := readOnlyHierarchyV2("/workspace", true, bind, freeze); e != nil {
		t.Fatal(e)
	}
	if childRO {
		t.Fatal("parent freeze claimed child immutability")
	}
	good := func(p string) (mountObservationV2, error) {
		s := mountSpecsV2()[p]
		return mountObservationV2{bytes: s.bytes, inodes: s.inodes, tmpfs: true, nosuid: true, nodev: true, noexec: s.noexec, readonly: p != "/workspace/node_modules" || childRO}, nil
	}
	if checkInputMountsV2(good) == nil {
		t.Fatal("writable alias passed final strict checks")
	}
	if e := readOnlyHierarchyV2("/workspace/node_modules", true, bind, freeze); e != nil {
		t.Fatal(e)
	}
	if checkInputMountsV2(good) != nil {
		t.Fatal("separately frozen alias rejected")
	}
}
func TestV2CompiledFreezeVerifiesVisibleDependencyAlias(t *testing.T) {
	good := func(p string) (mountObservationV2, error) {
		bytes, inodes := uint64(256<<20), uint64(16384)
		if p == "/foundry-build/compiled/node_modules" {
			bytes, inodes = 80<<20, 32768
		}
		return mountObservationV2{bytes: bytes, inodes: inodes, tmpfs: true, nosuid: true, nodev: true, noexec: true, readonly: true}, nil
	}
	if e := checkCompiledMountV2(good); e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"covered", "readonly", "nosuid", "nodev", "noexec"} {
		if e := checkCompiledMountV2(func(p string) (mountObservationV2, error) {
			o, e := good(p)
			if p == "/foundry-build/compiled/node_modules" {
				switch field {
				case "covered":
					o.bytes = 256 << 20
					o.inodes = 16384
				case "readonly":
					o.readonly = false
				case "nosuid":
					o.nosuid = false
				case "nodev":
					o.nodev = false
				case "noexec":
					o.noexec = false
				}
			}
			return o, e
		}); e == nil {
			t.Fatal("invalid compiled dependency mount", field)
		}
	}
}
