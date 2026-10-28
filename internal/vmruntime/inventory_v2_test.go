package vmruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func inventoryFixtureV2(t *testing.T) RuntimeInventoryV2 {
	t.Helper()
	b, e := os.ReadFile("testdata/runtime-inventory-v2.json")
	if e != nil {
		t.Fatal(e)
	}
	d, e := ParseRuntimeInventoryV2(b)
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestInventoryV2CanonicalGolden(t *testing.T) {
	b, _ := os.ReadFile("testdata/runtime-inventory-v2.json")
	want, _ := os.ReadFile("testdata/runtime-inventory-v2.sha256")
	d := inventoryFixtureV2(t)
	got, e := d.CanonicalBytes()
	if e != nil || !bytes.Equal(got, b) {
		t.Fatalf("canonical: %v", e)
	}
	h, e := d.Digest()
	if e != nil || h != strings.TrimSpace(string(want)) {
		t.Fatalf("digest %s %v", h, e)
	}
}
func TestInventoryV2RejectsMalformedAndDrift(t *testing.T) {
	raw, _ := os.ReadFile("testdata/runtime-inventory-v2.json")
	for name, change := range map[string]func([]byte) []byte{
		"space": func(b []byte) []byte { return append(b, '\n') },
		"duplicate": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema_version":2`), []byte(`"schema_version":2,"schema_version":2`), 1)
		},
		"alias": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"guest_platform"`), []byte(`"Guest_platform"`), 1)
		},
		"missing": func(b []byte) []byte { return bytes.Replace(b, []byte(`"child_contract_version":1,`), nil, 1) },
		"unknown": func(b []byte) []byte { return append([]byte(`{"unknown":1,`), b[1:]...) },
		"null":    func(b []byte) []byte { return bytes.Replace(b, []byte(`"strict":true`), []byte(`"strict":null`), 1) },
		"number":  func(b []byte) []byte { return bytes.Replace(b, []byte(`"entries":20000`), []byte(`"entries":2e4`), 1) },
		"bound":   func(b []byte) []byte { return bytes.Repeat([]byte(" "), 65537) },
		"projection": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"entries":20000`), []byte(`"entries":20001`), 1)
		},
		"runner": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`typescript-node-test-v2`), []byte(`node-test-v2`), 1)
		},
		"supervisor": func(b []byte) []byte { return bytes.Replace(b, []byte(`"/init"`), []byte(`"/other"`), 1) },
		"version":    func(b []byte) []byte { return bytes.Replace(b, []byte(`24.22.1`), []byte(`24.22.2`), 1) },
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := ParseRuntimeInventoryV2(change(bytes.Clone(raw))); e == nil {
				t.Fatal("unsafe descriptor accepted")
			}
		})
	}
}

func TestInventoryV2FileAndProjectionLimits(t *testing.T) {
	for name, change := range map[string]func(*RuntimeInventoryV2){
		"entries zero": func(d *RuntimeInventoryV2) { d.ProjectedRuntime.Entries = 0 }, "runtime bytes zero": func(d *RuntimeInventoryV2) { d.ProjectedRuntime.RegularBytes = 0 }, "runtime bytes over": func(d *RuntimeInventoryV2) { d.ProjectedRuntime.RegularBytes++ },
		"supervisor zero": func(d *RuntimeInventoryV2) { d.Supervisor.Size = 0 }, "supervisor over": func(d *RuntimeInventoryV2) { d.Supervisor.Size = (64 << 20) + 1 }, "node over": func(d *RuntimeInventoryV2) { d.Node.Executable.Size = (128 << 20) + 1 }, "python over": func(d *RuntimeInventoryV2) { d.Python.Executable.Size = (128 << 20) + 1 }, "compiler over": func(d *RuntimeInventoryV2) { d.TypeScript.Compiler.Size = (128 << 20) + 1 }, "runner over": func(d *RuntimeInventoryV2) { d.Runners[0].File.Size = (1 << 20) + 1 }, "runner ordering": func(d *RuntimeInventoryV2) { d.Runners[0], d.Runners[1] = d.Runners[1], d.Runners[0] }, "hash uppercase": func(d *RuntimeInventoryV2) { d.Supervisor.SHA256 = strings.Repeat("A", 64) }, "options": func(d *RuntimeInventoryV2) { d.Python.Options = []string{"-S", "-I", "-B"} }, "compiler contract": func(d *RuntimeInventoryV2) { d.CompilerContract.CompilerOptions.AllowJS = true }, "report schema": func(d *RuntimeInventoryV2) { d.FrameworkReport.SchemaVersion = 2 }, "child schema": func(d *RuntimeInventoryV2) { d.ChildContractVersion = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			d := inventoryFixtureV2(t)
			change(&d)
			if _, e := d.CanonicalBytes(); e == nil {
				t.Fatal("invalid descriptor encoded")
			}
			if _, e := d.Digest(); e == nil {
				t.Fatal("invalid descriptor hashed")
			}
		})
	}
	d := inventoryFixtureV2(t)
	d.Supervisor.Size = 64 << 20
	d.Node.Executable.Size = 128 << 20
	d.Python.Executable.Size = 128 << 20
	d.TypeScript.Compiler.Size = 128 << 20
	for i := range d.Runners {
		d.Runners[i].File.Size = 1 << 20
	}
	if _, e := d.CanonicalBytes(); e != nil {
		t.Fatal("exact bounds rejected", e)
	}
}

func TestInventoryV2EveryNestedObjectHasExactRequiredKeys(t *testing.T) {
	raw, e := os.ReadFile("testdata/runtime-inventory-v2.json")
	if e != nil {
		t.Fatal(e)
	}
	var value any
	if e = json.Unmarshal(raw, &value); e != nil {
		t.Fatal(e)
	}
	var visit func(any, string)
	visit = func(v any, path string) {
		switch obj := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(obj))
			for k := range obj {
				keys = append(keys, k)
			}
			reject := func(name string) {
				t.Run(path+"/"+name, func(t *testing.T) {
					b, e := json.Marshal(value)
					if e != nil {
						t.Fatal(e)
					}
					if _, e = ParseRuntimeInventoryV2(b); e == nil {
						t.Fatal("inexact object accepted")
					}
				})
			}
			for _, k := range keys {
				old := obj[k]
				delete(obj, k)
				reject("missing-" + k)
				obj[k] = nil
				reject("null-" + k)
				delete(obj, k)
				obj[strings.ToUpper(k)] = old
				reject("alias-" + k)
				delete(obj, strings.ToUpper(k))
				obj[k] = old
			}
			obj["unrecognized"] = 1
			reject("unknown")
			delete(obj, "unrecognized")
			for _, k := range keys {
				visit(obj[k], path+"/"+k)
			}
		case []any:
			for i, item := range obj {
				visit(item, fmt.Sprintf("%s/%d", path, i))
			}
		}
	}
	visit(value, "root")
}
