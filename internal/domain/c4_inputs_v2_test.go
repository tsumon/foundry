package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func inputFile(path, role string) C4InputFileV2 {
	return C4InputFileV2{Path: path, Mode: 0644, Bytes: 1, SHA256: strings.Repeat("a", 64), Role: role}
}
func inputFixture(index int) C4InputContractV2 {
	c := C4InputContractV2{SchemaVersion: 2, ProfileID: c4V2Tuples[index].id,
		Dependencies:    C4DependencyBindingV2{SchemaVersion: 1, Ecosystem: "node", BundleDigest: strings.Repeat("b", 64), ManifestDigest: strings.Repeat("c", 64)},
		AdmissionDigest: strings.Repeat("d", 64), SourceFiles: []C4SourceDigestV2{}, Protected: []C4InputFileV2{},
		ProtectedPrefixes: []string{".foundry-build", ".venv", "node_modules", "site-packages", "tests"}, TestFiles: []string{"tests/a.test.js"}, SourceRoots: []string{"src"}, HarnessVersion: "foundry-test-v2"}
	names := []struct{ path, role string }{{"package-lock.json", "lock"}, {"package.json", "manifest"}}
	if index == 2 {
		c.Dependencies.Ecosystem = "python"
		c.TestFiles = []string{"tests/test_a.py"}
		names = []struct{ path, role string }{{"foundry.python.lock.json", "lock"}}
	}
	if index == 1 {
		c.TestFiles = []string{"tests/a.test.ts"}
		names = append(names, struct{ path, role string }{"tsconfig.json", "config"})
		c.TypeScript = &C4TypeScriptContractV2{ConfigPath: "tsconfig.json", ConfigDigest: strings.Repeat("a", 64), InputFiles: []string{"src/a.ts", "src/types.d.ts", "tests/a.test.ts"}, OutputRoot: "/foundry-build/compiled"}
	}
	for _, n := range names {
		f := inputFile(n.path, n.role)
		c.Protected = append(c.Protected, f)
		c.SourceFiles = append(c.SourceFiles, C4SourceDigestV2{Path: f.Path, Bytes: f.Bytes, SHA256: f.SHA256})
	}
	c.Protected = append(c.Protected, inputFile(c.TestFiles[0], "test"), inputFile("tests/helper.txt", "helper"))
	sort.Slice(c.Protected, func(i, j int) bool { return c.Protected[i].Path < c.Protected[j].Path })
	return c
}
func sealedInput(t *testing.T, index int) C4InputContractV2 {
	t.Helper()
	c, e := inputFixture(index).WithDigests()
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestC4InputV2DigestsAndDeepCopy(t *testing.T) {
	for i := range c4V2Tuples {
		original := inputFixture(i)
		c, e := original.WithDigests()
		if e != nil {
			t.Fatal(e)
		}
		if e = c.Validate(); e != nil {
			t.Fatal(e)
		}
		raw, e := c.CanonicalJSON()
		if e != nil {
			t.Fatal(e)
		}
		if !json.Valid(raw) || strings.Contains(string(raw), "null") {
			t.Fatal(string(raw))
		}
		protected, _ := json.Marshal(struct {
			Protected []C4InputFileV2 `json:"protected"`
			Prefixes  []string        `json:"protected_prefixes"`
		}{c.Protected, c.ProtectedPrefixes})
		h := sha256.Sum256(append([]byte("foundry:c4:v2:protected-inputs\n"), protected...))
		if c.ProtectedInputsDigest != hex.EncodeToString(h[:]) {
			t.Fatal("protected digest does not bind inventory and prefixes")
		}
		// Preserve canonical struct field order, independently replacing the two serialized values.
		unsigned := strings.ReplaceAll(string(raw), `"protected_inputs_digest":"`+c.ProtectedInputsDigest+`"`, `"protected_inputs_digest":""`)
		unsigned = strings.ReplaceAll(unsigned, `"execution_contract_digest":"`+c.ExecutionContractDigest+`"`, `"execution_contract_digest":""`)
		h = sha256.Sum256([]byte("foundry:c4:v2:execution-contract\n" + unsigned))
		if c.ExecutionContractDigest != hex.EncodeToString(h[:]) {
			t.Fatal("wrong execution digest")
		}
		original.Protected[0].SHA256 = "changed"
		original.SourceFiles[0].SHA256 = "changed"
		original.TestFiles[0] = "changed"
		original.SourceRoots[0] = "changed"
		original.ProtectedPrefixes[0] = "changed"
		if original.TypeScript != nil {
			original.TypeScript.InputFiles[0] = "changed"
			original.TypeScript.ConfigDigest = "changed"
		}
		if e = c.Validate(); e != nil {
			t.Fatal("sealed storage aliases input:", e)
		}
		before, _ := c.CanonicalJSON()
		c2, e := c.WithDigests()
		if e != nil {
			t.Fatal(e)
		}
		after, _ := c2.CanonicalJSON()
		if string(before) != string(after) {
			t.Fatal("reseal unstable")
		}
	}
}
func TestC4InputV2RejectsInvalidContract(t *testing.T) {
	changes := map[string]func(*C4InputContractV2){
		"schema": func(c *C4InputContractV2) { c.SchemaVersion = 1 }, "profile": func(c *C4InputContractV2) { c.ProfileID = "c4_go_model_v1" },
		"null source": func(c *C4InputContractV2) { c.SourceFiles = nil }, "null protected": func(c *C4InputContractV2) { c.Protected = nil }, "null prefixes": func(c *C4InputContractV2) { c.ProtectedPrefixes = nil }, "null tests": func(c *C4InputContractV2) { c.TestFiles = nil }, "null roots": func(c *C4InputContractV2) { c.SourceRoots = nil },
		"empty tests": func(c *C4InputContractV2) { c.TestFiles = []string{} }, "empty roots": func(c *C4InputContractV2) { c.SourceRoots = []string{} }, "multiple roots": func(c *C4InputContractV2) { c.SourceRoots = []string{".", "src"} }, "nonclosed root": func(c *C4InputContractV2) { c.SourceRoots = []string{"lib"} }, "harness": func(c *C4InputContractV2) { c.HarnessVersion = "caller" },
		"admission": func(c *C4InputContractV2) { c.AdmissionDigest = strings.Repeat("A", 64) }, "unsorted protected": func(c *C4InputContractV2) { c.Protected[0], c.Protected[1] = c.Protected[1], c.Protected[0] },
		"mode": func(c *C4InputContractV2) { c.Protected[0].Mode = 0444 }, "negative bytes": func(c *C4InputContractV2) { c.Protected[0].Bytes = -1 }, "large bytes": func(c *C4InputContractV2) { c.Protected[0].Bytes = 10<<20 + 1 }, "overflow": func(c *C4InputContractV2) { c.Protected[0].Bytes = 1<<63 - 1 }, "bad hash": func(c *C4InputContractV2) { c.Protected[0].SHA256 = "bad" }, "role": func(c *C4InputContractV2) { c.Protected[0].Role = "source" },
		"source mismatch": func(c *C4InputContractV2) { c.SourceFiles[0].SHA256 = strings.Repeat("e", 64) }, "source size mismatch": func(c *C4InputContractV2) { c.SourceFiles[0].Bytes++ }, "missing source": func(c *C4InputContractV2) { c.SourceFiles = c.SourceFiles[1:] }, "extra source": func(c *C4InputContractV2) {
			c.SourceFiles = append(c.SourceFiles, C4SourceDigestV2{Path: "z.json", SHA256: strings.Repeat("a", 64)})
		},
		"unprotected test": func(c *C4InputContractV2) { c.TestFiles = []string{"tests/missing.test.js"} }, "missing selected prefix": func(c *C4InputContractV2) { c.ProtectedPrefixes = c.ProtectedPrefixes[:4] }, "missing reserved prefix": func(c *C4InputContractV2) { c.ProtectedPrefixes = c.ProtectedPrefixes[1:] }, "slash prefix": func(c *C4InputContractV2) { c.ProtectedPrefixes[4] = "tests/" },
		"cross list case alias": func(c *C4InputContractV2) {
			c.ProtectedPrefixes = append(c.ProtectedPrefixes, "z")
			c.TypeScript = nil
			c.Protected = append(c.Protected, inputFile("Z/helper", "helper"))
		},
		"TS on JS": func(c *C4InputContractV2) { c.TypeScript = &C4TypeScriptContractV2{} }, "ecosystem": func(c *C4InputContractV2) { c.Dependencies.Ecosystem = "python" }, "binding schema": func(c *C4InputContractV2) { c.Dependencies.SchemaVersion = 2 }, "binding hash": func(c *C4InputContractV2) { c.Dependencies.ManifestDigest = "" }, "binding count": func(c *C4InputContractV2) { c.Dependencies.FileCount = 10001 }, "binding bytes": func(c *C4InputContractV2) { c.Dependencies.TotalBytes = 64<<20 + 1 },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			c := inputFixture(0)
			change(&c)
			before, _ := json.Marshal(c)
			if _, e := c.WithDigests(); e == nil {
				t.Fatal("invalid input accepted")
			}
			after, _ := json.Marshal(c)
			if string(before) != string(after) {
				t.Fatal("validation normalized caller input")
			}
		})
	}
	for _, field := range []string{"protected", "execution"} {
		c := sealedInput(t, 0)
		if field == "protected" {
			c.ProtectedInputsDigest = strings.Repeat("e", 64)
		} else {
			c.ExecutionContractDigest = strings.Repeat("e", 64)
		}
		if c.Validate() == nil {
			t.Fatal("stale digest accepted")
		}
		if _, e := c.CanonicalJSON(); e == nil {
			t.Fatal("invalid canonical output")
		}
	}
}
func TestC4InputV2PortablePaths(t *testing.T) {
	for _, p := range []string{"", "/absolute", "../escape", "x/../a", "x/./a", "x//a", "x\\a", "x:a", "x/a.", "x/a ", "CON.js", "x/com1.txt", "x/a?b", "x/~x", "x/é", "x/\x00a", strings.Repeat("a", 1025), strings.Repeat("x/", 32) + "a"} {
		t.Run(fmt.Sprintf("%q", p), func(t *testing.T) {
			c := inputFixture(0)
			c.Protected = append(c.Protected, inputFile(p, "helper"))
			sort.Slice(c.Protected, func(i, j int) bool { return c.Protected[i].Path < c.Protected[j].Path })
			if _, e := c.WithDigests(); e == nil {
				t.Fatal("unsafe path accepted", p)
			}
		})
	}
	for _, paths := range [][]string{{"z/a", "z/a"}, {"Z/a", "z/b"}, {"z", "z/a"}, {"z/a", "z"}} {
		c := inputFixture(0)
		for _, p := range paths {
			c.Protected = append(c.Protected, inputFile(p, "helper"))
		}
		sort.Slice(c.Protected, func(i, j int) bool { return c.Protected[i].Path < c.Protected[j].Path })
		if _, e := c.WithDigests(); e == nil {
			t.Fatal("alias accepted", paths)
		}
	}
	for _, p := range []string{"node_modules/x", ".venv/x", "site-packages/x", ".foundry-build/x", "NODE_MODULES/x"} {
		c := inputFixture(0)
		c.Protected = append(c.Protected, inputFile(p, "helper"))
		sort.Slice(c.Protected, func(i, j int) bool { return c.Protected[i].Path < c.Protected[j].Path })
		if _, e := c.WithDigests(); e == nil {
			t.Fatal("reserved path accepted")
		}
	}
}
func TestC4InputV2TypeScript(t *testing.T) {
	for name, change := range map[string]func(*C4InputContractV2){"missing": func(c *C4InputContractV2) { c.TypeScript = nil }, "config path": func(c *C4InputContractV2) { c.TypeScript.ConfigPath = "other.json" }, "config digest": func(c *C4InputContractV2) { c.TypeScript.ConfigDigest = strings.Repeat("f", 64) }, "output": func(c *C4InputContractV2) { c.TypeScript.OutputRoot = "/tmp" }, "null inputs": func(c *C4InputContractV2) { c.TypeScript.InputFiles = nil }, "unsorted": func(c *C4InputContractV2) { c.TypeScript.InputFiles = []string{"tests/a.test.ts", "src/a.ts"} }, "missing test": func(c *C4InputContractV2) { c.TypeScript.InputFiles = []string{"src/a.ts"} }, "JS input": func(c *C4InputContractV2) { c.TypeScript.InputFiles = []string{"a.js", "tests/a.test.ts"} }} {
		t.Run(name, func(t *testing.T) {
			c := inputFixture(1)
			change(&c)
			if _, e := c.WithDigests(); e == nil {
				t.Fatal("bad TS contract accepted")
			}
		})
	}
	for i := range c4V2Tuples {
		c := inputFixture(i)
		c.TestFiles = []string{"elsewhere/a.test.js"}
		if _, e := c.WithDigests(); e == nil {
			t.Fatal("arbitrary test root accepted")
		}
	}
}
func candidateInventory(c C4InputContractV2) []C4InputFileV2 {
	files := append([]C4InputFileV2{}, c.Protected...)
	if c.TypeScript != nil {
		for _, p := range c.TypeScript.InputFiles {
			found := false
			for _, f := range files {
				found = found || f.Path == p
			}
			if !found {
				files = append(files, inputFile(p, ""))
			}
		}
	} else {
		files = append(files, inputFile("src/code.txt", ""))
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}
func TestC4InputV2CandidateProtection(t *testing.T) {
	for i := range c4V2Tuples {
		c := sealedInput(t, i)
		files := candidateInventory(c)
		for j := range files {
			files[j].Role = "untrusted"
			if strings.HasPrefix(files[j].Path, "src/") {
				files[j].SHA256 = strings.Repeat("e", 64)
				files[j].Bytes = 3
				files[j].Mode = 0755
			}
		}
		if e := c.ValidateSourceInventory(files); e != nil {
			t.Fatal("editable source/ignored inventory role rejected", e)
		}
		for _, change := range []string{"missing", "hash", "size", "mode", "new test", "new helper", "config", "reserved", "invalid source hash", "alias"} {
			t.Run(c.ProfileID+"/"+change, func(t *testing.T) {
				bad := append([]C4InputFileV2{}, files...)
				switch change {
				case "missing":
					bad = bad[1:]
				case "hash":
					bad[0].SHA256 = strings.Repeat("f", 64)
				case "size":
					bad[0].Bytes++
				case "mode":
					bad[0].Mode = 0755
				case "new test":
					bad = append(bad, inputFile("tests/new.test.js", ""))
				case "new helper":
					bad = append(bad, inputFile("tests/fixture/new.txt", ""))
				case "config":
					for j := range bad {
						if bad[j].Role == "untrusted" && !strings.HasPrefix(bad[j].Path, "src/") {
							bad[j].SHA256 = strings.Repeat("f", 64)
							break
						}
					}
				case "reserved":
					bad = append(bad, inputFile("node_modules/a.js", ""))
				case "invalid source hash":
					for j := range bad {
						if strings.HasPrefix(bad[j].Path, "src/") {
							bad[j].SHA256 = "invalid"
						}
					}
				case "alias":
					bad = append(bad, inputFile("SRC/other.txt", ""))
				}
				sort.Slice(bad, func(i, j int) bool { return bad[i].Path < bad[j].Path })
				if c.ValidateSourceInventory(bad) == nil {
					t.Fatal("candidate drift accepted")
				}
			})
		}
	}
	c := sealedInput(t, 0)
	c.ProtectedPrefixes = append(c.ProtectedPrefixes, "tests-more")
	c.Protected = append(c.Protected, inputFile("tests-more/data.txt", "helper"))
	sort.Slice(c.Protected, func(i, j int) bool { return c.Protected[i].Path < c.Protected[j].Path })
	c, e := c.WithDigests()
	if e != nil {
		t.Fatal(e)
	}
	files := candidateInventory(c)
	files = append(files, inputFile("tests-new/editable.txt", ""))
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if e = c.ValidateSourceInventory(files); e != nil {
		t.Fatal("prefix without slash matched sibling", e)
	}
}
func TestC4InputV2ProfileBinding(t *testing.T) {
	c := sealedInput(t, 0)
	p := v2Profile(t, 0, false)
	p.V2.DependencyBundleDigest = c.Dependencies.BundleDigest
	p.V2.ProtectedInputsDigest = c.ProtectedInputsDigest
	p.V2.ExecutionContractDigest = c.ExecutionContractDigest
	if e := c.ValidateProfileBinding(p); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*C4Profile){"ID": func(p *C4Profile) { *p = v2Profile(t, 2, false) }, "dependency": func(p *C4Profile) { p.V2.DependencyBundleDigest = strings.Repeat("f", 64) }, "protected": func(p *C4Profile) { p.V2.ProtectedInputsDigest = strings.Repeat("f", 64) }, "execution": func(p *C4Profile) { p.V2.ExecutionContractDigest = strings.Repeat("f", 64) }, "invalid": func(p *C4Profile) { p.Mode = "docker" }} {
		t.Run(name, func(t *testing.T) {
			bad := p
			v := *p.V2
			bad.V2 = &v
			change(&bad)
			if c.ValidateProfileBinding(bad) == nil {
				t.Fatal("mixed binding accepted")
			}
		})
	}
}
func TestC4InputV2BindingShape(t *testing.T) {
	raw, _ := json.Marshal(C4DependencyBindingV2{})
	want := `{"schema_version":0,"ecosystem":"","bundle_digest":"","manifest_digest":"","file_count":0,"total_bytes":0}`
	if string(raw) != want {
		t.Fatal(string(raw))
	}
	c := sealedInput(t, 0)
	copy, e := c.WithDigests()
	if e != nil || !reflect.DeepEqual(c, copy) {
		t.Fatal(e)
	}
}

func TestC4InputV2Bounds(t *testing.T) {
	c := inputFixture(0)
	// The upper inventory count and byte limits are accepted exactly.
	for len(c.Protected) < 500 {
		c.Protected = append(c.Protected, inputFile(fmt.Sprintf("z/%03d", len(c.Protected)), "helper"))
	}
	c.Protected[len(c.Protected)-1].Bytes = (10 << 20) - 499
	sealed, e := c.WithDigests()
	if e != nil {
		t.Fatal("exact protected limit rejected:", e)
	}
	if e = sealed.ValidateSourceInventory(sealed.Protected); e != nil {
		t.Fatal("exact source limit rejected:", e)
	}
	c.Protected[len(c.Protected)-1].Bytes++
	if _, e = c.WithDigests(); e == nil {
		t.Fatal("protected bytes bound missing")
	}
	c.Protected[len(c.Protected)-1].Bytes = 1
	c.Protected = append(c.Protected, inputFile("z/extra", "helper"))
	if _, e = c.WithDigests(); e == nil {
		t.Fatal("protected count bound missing")
	}
	if e = sealed.ValidateSourceInventory(c.Protected); e == nil {
		t.Fatal("source count bound missing")
	}
	inventory := candidateInventory(sealedInput(t, 0))
	inventory[2].Bytes = 10 << 20
	if e = sealedInput(t, 0).ValidateSourceInventory(inventory); e == nil {
		t.Fatal("cumulative source byte bound missing")
	}
	if e = sealedInput(t, 0).ValidateSourceInventory(nil); e == nil {
		t.Fatal("nil inventory accepted")
	}
	// All lists are bounded before JSON hashing. Each is independently exercised.
	for _, field := range []string{"prefixes", "tests", "TS inputs"} {
		c := inputFixture(1)
		list := make([]string, 501)
		for i := range list {
			list[i] = fmt.Sprintf("z/%03d.ts", i)
		}
		switch field {
		case "prefixes":
			c.ProtectedPrefixes = list
		case "tests":
			c.TestFiles = list
		case "TS inputs":
			c.TypeScript.InputFiles = list
		}
		if _, e = c.WithDigests(); e == nil {
			t.Fatal("unbounded list", field)
		}
	}
	// Long, individually valid paths exercise the independent 1 MiB JSON cap.
	c = inputFixture(1)
	c.TypeScript.InputFiles = []string{}
	for len(c.Protected) < 500 {
		p := fmt.Sprintf("z/%03d/", len(c.Protected)) + strings.Repeat("a", 1000) + ".ts"
		c.Protected = append(c.Protected, inputFile(p, "helper"))
		c.TypeScript.InputFiles = append(c.TypeScript.InputFiles, p)
	}
	c.TypeScript.InputFiles = append(c.TypeScript.InputFiles, "tests/a.test.ts")
	sort.Strings(c.TypeScript.InputFiles)
	if _, e = c.WithDigests(); e == nil {
		t.Fatal("canonical JSON size bound missing")
	}
	for _, p := range []string{strings.Repeat("x/", 31) + "a", strings.Repeat("a", 1024)} {
		c = inputFixture(0)
		c.Protected = append(c.Protected, inputFile(p, "helper"))
		sort.Slice(c.Protected, func(i, j int) bool { return c.Protected[i].Path < c.Protected[j].Path })
		if _, e = c.WithDigests(); e != nil {
			t.Fatal("exact path/depth bound rejected:", e)
		}
	}
}

func TestC4InputV2TSInventoryClosed(t *testing.T) {
	c := sealedInput(t, 1)
	files := candidateInventory(c)
	for _, p := range []string{"src/new.ts", "src/new.d.ts"} {
		bad := append([]C4InputFileV2{}, files...)
		bad = append(bad, inputFile(p, ""))
		sort.Slice(bad, func(i, j int) bool { return bad[i].Path < bad[j].Path })
		if c.ValidateSourceInventory(bad) == nil {
			t.Fatal("new TS compiler input accepted", p)
		}
	}
	for j, f := range files {
		if f.Path == "src/types.d.ts" {
			bad := append([]C4InputFileV2{}, files[:j]...)
			bad = append(bad, files[j+1:]...)
			if c.ValidateSourceInventory(bad) == nil {
				t.Fatal("missing ambient input accepted")
			}
		}
	}
}

func TestC4V2PolicyBindsSeparateInputMounts(t *testing.T) {
	for _, tuple := range c4V2Tuples {
		p, e := FixedC4VMPolicyV2(tuple.id)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(p.Test)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		depsPath := "/workspace/node_modules"
		if tuple.language == "python" {
			depsPath = "/foundry-deps/python"
		}
		for key, want := range map[string]C4Tmpfs{"source_mount": {Path: "/workspace", SizeBytes: 16 << 20, Inodes: 16384, Nosuid: true, Nodev: true}, "dependency_mount": {Path: depsPath, SizeBytes: 80 << 20, Inodes: 32768, Nosuid: true, Nodev: true}} {
			var got C4Tmpfs
			if e = json.Unmarshal(fields[key], &got); e != nil || !reflect.DeepEqual(got, want) {
				t.Fatal("missing or unsafe input mount", tuple.id, key, got, e)
			}
		}
	}
}

func TestC4InputV2ExplicitHelpersAndAdditionalConfig(t *testing.T) {
	c := inputFixture(0)
	c.ProtectedPrefixes = append(c.ProtectedPrefixes, "test_support")
	c.Protected = append(c.Protected, inputFile("test_support/fixture.json", "helper"), inputFile("tool.config.json", "config"))
	sort.Strings(c.ProtectedPrefixes)
	sort.Slice(c.Protected, func(i, j int) bool { return c.Protected[i].Path < c.Protected[j].Path })
	c, e := c.WithDigests()
	if e != nil {
		t.Fatal(e)
	}
	files := candidateInventory(c)
	for _, path := range []string{"tests/helper.txt", "test_support/fixture.json", "tool.config.json"} {
		for _, mutation := range []string{"remove", "change"} {
			bad := append([]C4InputFileV2{}, files...)
			for j := range bad {
				if bad[j].Path == path {
					if mutation == "remove" {
						bad = append(bad[:j], bad[j+1:]...)
					} else {
						bad[j].SHA256 = strings.Repeat("f", 64)
					}
					break
				}
			}
			if c.ValidateSourceInventory(bad) == nil {
				t.Fatal("protected input drift accepted", path, mutation)
			}
		}
	}
	files = append(files, inputFile("test_support/new.txt", ""))
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if c.ValidateSourceInventory(files) == nil {
		t.Fatal("new helper under declared prefix accepted")
	}
}

func TestC4InputV2AllValidLayouts(t *testing.T) {
	for _, ext := range []string{"js", "mjs", "cjs"} {
		c := inputFixture(0)
		c.SourceRoots = []string{"."}
		c.ProtectedPrefixes[4] = "test"
		c.TestFiles = []string{"test/a.test." + ext}
		for j := range c.Protected {
			if c.Protected[j].Role == "test" {
				c.Protected[j].Path = c.TestFiles[0]
				c.Protected[j].Mode = 0755
			}
			if c.Protected[j].Role == "helper" {
				c.Protected[j].Path = "test/helper.txt"
				c.Protected[j].Bytes = 0
			}
		}
		sort.Slice(c.Protected, func(i, j int) bool { return c.Protected[i].Path < c.Protected[j].Path })
		sealed, e := c.WithDigests()
		if e != nil {
			t.Fatal("supported JS layout rejected", ext, e)
		}
		if e = sealed.ValidateSourceInventory(candidateInventory(sealed)); e != nil {
			t.Fatal(e)
		}
	}
	for i := range c4V2Tuples {
		c := inputFixture(i)
		for _, badName := range []string{"tests/a.txt", "tests/a.test.ts", "tests/test_a.py", "test/a.test.js"} {
			if badName == c.TestFiles[0] {
				continue
			}
			bad := inputFixture(i)
			bad.TestFiles = []string{badName}
			for j := range bad.Protected {
				if bad.Protected[j].Role == "test" {
					bad.Protected[j].Path = badName
				}
			}
			sort.Slice(bad.Protected, func(i, j int) bool { return bad.Protected[i].Path < bad.Protected[j].Path })
			if _, e := bad.WithDigests(); e == nil {
				t.Fatal("unsupported test declaration accepted", bad.ProfileID, badName)
			}
		}
	}
}

func TestC4InputV2ExecutionDigestBindsInspectedMaterial(t *testing.T) {
	for name, change := range map[string]func(*C4InputContractV2){
		"admission":        func(c *C4InputContractV2) { c.AdmissionDigest = strings.Repeat("e", 64) },
		"bundle":           func(c *C4InputContractV2) { c.Dependencies.BundleDigest = strings.Repeat("e", 64) },
		"manifest":         func(c *C4InputContractV2) { c.Dependencies.ManifestDigest = strings.Repeat("e", 64) },
		"file count":       func(c *C4InputContractV2) { c.Dependencies.FileCount = 1 },
		"dependency bytes": func(c *C4InputContractV2) { c.Dependencies.TotalBytes = 1 },
		"source root":      func(c *C4InputContractV2) { c.SourceRoots = []string{"."} },
		"metadata": func(c *C4InputContractV2) {
			c.SourceFiles[0].SHA256 = strings.Repeat("e", 64)
			c.Protected[0].SHA256 = strings.Repeat("e", 64)
		},
		"prefix": func(c *C4InputContractV2) { c.ProtectedPrefixes = append(c.ProtectedPrefixes, "z") },
	} {
		t.Run(name, func(t *testing.T) {
			c := sealedInput(t, 0)
			old := c.ExecutionContractDigest
			change(&c)
			if c.Validate() == nil {
				t.Fatal("stale execution digest accepted")
			}
			sealed, e := c.WithDigests()
			if e != nil {
				t.Fatal(e)
			}
			if sealed.ExecutionContractDigest == old {
				t.Fatal("unbound inspected material")
			}
		})
	}
}
