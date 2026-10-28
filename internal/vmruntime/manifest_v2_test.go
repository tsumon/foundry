package vmruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func fixtureBundleV2(t *testing.T) (string, string) {
	t.Helper()
	if host := runtime.GOOS + "/" + runtime.GOARCH; host != "darwin/arm64" && host != "windows/amd64" {
		t.Skip("supported installed VM hosts only")
	}
	root, instances := fixtureBundle(t)
	raw, _ := os.ReadFile(filepath.Join(root, "manifest.json"))
	var old Manifest
	json.Unmarshal(raw, &old)
	os.Remove(filepath.Join(root, old.Verifier))
	m := ManifestV2{SchemaVersion: 2, HostPlatform: old.HostPlatform, GuestPlatform: old.GuestPlatform, Protocol: "foundry-vm-v2", PolicyVersion: "multilang-vm-v2", Runtime: old.Runtime, Kernel: old.Kernel, RootFS: old.RootFS, Verifier: "foundry-vm-guest-v2", RuntimeInventory: RuntimeInventoryPathV2}
	supervisor := []byte("inert-supervisor-v2")
	d := inventoryFixtureV2(t)
	d.GuestPlatform = m.GuestPlatform
	d.Supervisor.Size = int64(len(supervisor))
	d.Supervisor.SHA256 = hashV2Test(supervisor)
	descriptor, e := d.CanonicalBytes()
	if e != nil {
		t.Fatal(e)
	}
	m.RuntimeInventoryDigest, _ = d.Digest()
	for _, a := range old.Assets {
		if a.Path != old.Verifier {
			m.Assets = append(m.Assets, a)
		}
	}
	for p, b := range map[string][]byte{m.Verifier: supervisor, m.RuntimeInventory: descriptor} {
		if e := os.WriteFile(filepath.Join(root, p), b, 0600); e != nil {
			t.Fatal(e)
		}
		m.Assets = append(m.Assets, Asset{p, hashV2Test(b), int64(len(b))})
	}
	b, _ := json.Marshal(m)
	if e := os.WriteFile(filepath.Join(root, "manifest.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	return root, instances
}
func TestLoadRetainV2ExactBytesAndNoEffects(t *testing.T) {
	root, instances := fixtureBundleV2(t)
	r, e := LoadV2(root, instances)
	if e != nil {
		t.Fatal(e)
	}
	if !r.Trusted() || r.Identity().SchemaVersion != 2 {
		t.Fatal("identity")
	}
	entries, _ := os.ReadDir(instances)
	if len(entries) != 0 {
		t.Fatal("load effects")
	}
	if _, e := Load(root, instances); e == nil {
		t.Fatal("v1 negotiated v2")
	}
	old, oldInstances := fixtureBundle(t)
	if _, e := LoadV2(old, oldInstances); e == nil {
		t.Fatal("v2 negotiated v1")
	}
	archive := filepath.Join(archiveTestParent(t), "archive")
	retained, e := Retain(root, archive, r.Identity())
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"manifest.json", RuntimeInventoryPathV2} {
		a, _ := os.ReadFile(filepath.Join(root, p))
		b, _ := os.ReadFile(filepath.Join(retained, p))
		if !bytes.Equal(a, b) {
			t.Fatal("retention changed bytes")
		}
	}
	runtimes, e := LoadRetained(archive, instances)
	if e != nil || len(runtimes) != 1 || runtimes[0].Identity() != r.Identity() {
		t.Fatalf("load retained: %v", e)
	}
	os.WriteFile(filepath.Join(retained, RuntimeInventoryPathV2), []byte("tampered"), 0600)
	if _, e := LoadRetained(archive, instances); e == nil {
		t.Fatal("late descriptor tamper")
	}
	if _, e := Retain(root, archive, r.Identity()); e == nil {
		t.Fatal("existing target tamper")
	}
}
func TestLoadV2RejectsDescriptorAndManifestDrift(t *testing.T) {
	for _, kind := range []string{"platform", "supervisor", "digest", "missing", "unknown", "duplicate", "alias", "null", "unlisted", "bounds"} {
		t.Run(kind, func(t *testing.T) {
			root, instances := fixtureBundleV2(t)
			p := filepath.Join(root, "manifest.json")
			raw, _ := os.ReadFile(p)
			var m ManifestV2
			json.Unmarshal(raw, &m)
			switch kind {
			case "platform", "supervisor":
				descriptor, err := os.ReadFile(filepath.Join(root, m.RuntimeInventory))
				if err != nil {
					t.Fatal(err)
				}
				d, err := ParseRuntimeInventoryV2(descriptor)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "platform" {
					d.GuestPlatform = "linux/amd64"
					if m.GuestPlatform == d.GuestPlatform {
						d.GuestPlatform = "linux/arm64"
					}
				} else {
					d.Supervisor.SHA256 = hashV2Test([]byte("other"))
				}
				b, _ := d.CanonicalBytes()
				os.WriteFile(filepath.Join(root, m.RuntimeInventory), b, 0600)
				m.RuntimeInventoryDigest, _ = d.Digest()
				for i := range m.Assets {
					if m.Assets[i].Path == m.RuntimeInventory {
						m.Assets[i] = Asset{m.RuntimeInventory, hashV2Test(b), int64(len(b))}
					}
				}
				raw, _ = json.Marshal(m)
			case "digest":
				m.RuntimeInventoryDigest = hashV2Test([]byte("other"))
				raw, _ = json.Marshal(m)
			case "missing":
				raw = bytes.Replace(raw, []byte(`"runtime_inventory":"runtime-inventory-v2.json",`), nil, 1)
			case "unknown":
				raw = append([]byte(`{"x":1,`), raw[1:]...)
			case "duplicate":
				raw = bytes.Replace(raw, []byte(`"schema_version":2`), []byte(`"schema_version":2,"schema_version":2`), 1)
			case "alias":
				raw = bytes.Replace(raw, []byte(`"schema_version"`), []byte(`"Schema_version"`), 1)
			case "null":
				raw = bytes.Replace(raw, []byte(`"schema_version":2`), []byte(`"schema_version":null`), 1)
			case "unlisted":
				os.WriteFile(filepath.Join(root, "extra"), []byte("x"), 0600)
			case "bounds":
				raw = bytes.Repeat([]byte(" "), (1<<20)+1)
			}
			os.WriteFile(p, raw, 0600)
			if _, e := LoadV2(root, instances); e == nil {
				t.Fatal("drift accepted")
			}
		})
	}
}

func TestRetainedMixedSchemasAndUnknownCatalogFailClosed(t *testing.T) {
	v1, i1 := fixtureBundle(t)
	v2, i2 := fixtureBundleV2(t)
	r1, e := Load(v1, i1)
	if e != nil {
		t.Fatal(e)
	}
	r2, e := LoadV2(v2, i2)
	if e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(archiveTestParent(t), "archive")
	for _, r := range []*Runtime{r1, r2} {
		if _, e := Retain(r.bundleRoot, archive, r.Identity()); e != nil {
			t.Fatal(e)
		}
	}
	got, e := LoadRetained(archive, i2)
	if e != nil || len(got) != 2 {
		t.Fatal("mixed catalog", e)
	}
	for _, r := range got {
		if r.Identity() != r1.Identity() && r.Identity() != r2.Identity() {
			t.Fatal("identity changed")
		}
	}
	bad := r2.Identity()
	bad.SchemaVersion = 1
	if _, e := Retain(v2, archive, bad); e == nil {
		t.Fatal("expected schema ignored")
	}
	path := filepath.Join(archive, r2.Identity().ManifestDigest, "manifest.json")
	raw, _ := os.ReadFile(path)
	for _, b := range [][]byte{bytes.Replace(raw, []byte(`"schema_version":2`), []byte(`"schema_version":3`), 1), bytes.Replace(raw, []byte(`"schema_version":2`), []byte(`"schema_version":2,"schema_version":1`), 1), bytes.Replace(raw, []byte(`"schema_version"`), []byte(`"Schema_version"`), 1), bytes.Replace(raw, []byte(`"schema_version":2`), []byte(`"schema_version":null`), 1)} {
		if e := os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := LoadRetained(archive, i2); e == nil {
			t.Fatal("invalid catalog negotiated")
		}
	}
}
