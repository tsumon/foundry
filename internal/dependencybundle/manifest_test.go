package dependencybundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func manifest(ecosystem string) Manifest {
	return Manifest{SchemaVersion: 1, Ecosystem: ecosystem, LockSHA256: digest([]byte("lock")), Files: []File{{Path: "lib/a", Size: 3, SHA256: digest([]byte("abc"))}}}
}

func TestCanonicalDigestsBindLockAndPayload(t *testing.T) {
	m := manifest("node")
	b, err := m.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema_version":1,"ecosystem":"node","lock_sha256":"0c030586945fe504b604ecc2e875c38ede400cd5cd73da9730302162e6b02c6f","files":[{"path":"lib/a","size":3,"sha256":"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}]}`
	if string(b) != want {
		t.Fatalf("canonical = %s", b)
	}
	binding, err := m.Binding()
	if err != nil {
		t.Fatal(err)
	}
	if binding.ManifestDigest != digest([]byte(want)) || binding.BundleDigest != digest([]byte("foundry:dependencies:v1\n"+want)) || binding.FileCount != 1 || binding.TotalBytes != 3 {
		t.Fatalf("binding = %+v", binding)
	}
	for _, change := range []func(*Manifest){func(m *Manifest) { m.Ecosystem = "python" }, func(m *Manifest) { m.LockSHA256 = digest(nil) }, func(m *Manifest) { m.Files[0].Path = "lib/b" }, func(m *Manifest) { m.Files[0].Size++ }, func(m *Manifest) { m.Files[0].SHA256 = digest(nil) }} {
		other := manifest("node")
		change(&other)
		got, e := other.Binding()
		if e != nil || got.BundleDigest == binding.BundleDigest {
			t.Fatalf("unbound mutation: %+v %v", got, e)
		}
	}
}

func TestManifestRejectsAmbiguousAndUnboundedInventory(t *testing.T) {
	cases := map[string]func(*Manifest){
		"schema": func(m *Manifest) { m.SchemaVersion = 2 }, "ecosystem": func(m *Manifest) { m.Ecosystem = "go" }, "lock": func(m *Manifest) { m.LockSHA256 = strings.Repeat("A", 64) }, "nil files": func(m *Manifest) { m.Files = nil }, "negative": func(m *Manifest) { m.Files[0].Size = -1 }, "file size": func(m *Manifest) { m.Files[0].Size = 8*1024*1024 + 1 }, "file hash": func(m *Manifest) { m.Files[0].SHA256 = "bad" },
		"duplicate": func(m *Manifest) { m.Files = append(m.Files, m.Files[0]) }, "unsorted": func(m *Manifest) { m.Files = append([]File{{Path: "z", SHA256: digest(nil)}}, m.Files...) }, "case": func(m *Manifest) { m.Files = append(m.Files, File{Path: "lib/A", SHA256: digest(nil)}) }, "parent": func(m *Manifest) {
			m.Files = []File{{Path: "a", SHA256: digest(nil)}, {Path: "a/b", SHA256: digest(nil)}}
		}, "directory case": func(m *Manifest) {
			m.Files = []File{{Path: "A/a", SHA256: digest(nil)}, {Path: "a/b", SHA256: digest(nil)}}
		},
		"total": func(m *Manifest) {
			m.Files = nil
			for i := 0; i < 9; i++ {
				m.Files = append(m.Files, File{Path: fmt.Sprintf("f%02d", i), Size: 8 * 1024 * 1024, SHA256: digest(nil)})
			}
		},
		"count": func(m *Manifest) { m.Files = make([]File, 10001) },
		"manifest": func(m *Manifest) {
			m.Files = nil
			for i := 0; i < 5000; i++ {
				m.Files = append(m.Files, File{Path: fmt.Sprintf("%04d/", i) + strings.Repeat(strings.Repeat("x", 127)+"/", 6) + strings.Repeat("z", 127), SHA256: digest(nil)})
			}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m := manifest("node")
			change(&m)
			if _, err := m.Binding(); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
	for _, p := range []string{"", "/a", "a\\b", "a:b", "a//b", ".", "a/..", "a/./b", "a\n", "a\x7f", "a/é", "a.", "a ", "CON", "con.txt", "aux", "NUL.py", "lpt9.x", "COM1", "a/PRN/x", strings.Repeat("x", 129), strings.Repeat("a/", 32) + "x", strings.Repeat("aaa/", 256) + "x"} {
		t.Run(fmt.Sprintf("path-%q", p), func(t *testing.T) {
			m := manifest("node")
			m.Files[0].Path = p
			if _, err := m.Binding(); err == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
}

func TestEmptyAndBindingJSONHaveClosedStableShape(t *testing.T) {
	for _, eco := range []string{"node", "python"} {
		m := Empty(eco)
		b, e := m.Binding()
		if e != nil || b.FileCount != 0 || b.TotalBytes != 0 || m.LockSHA256 != digest(nil) {
			t.Fatalf("empty %s: %+v %v", eco, b, e)
		}
		raw, _ := json.Marshal(b)
		var keys map[string]any
		_ = json.Unmarshal(raw, &keys)
		if len(keys) != 6 || keys["schema_version"] != float64(1) || keys["ecosystem"] != eco || keys["file_count"] != float64(0) || keys["total_bytes"] != float64(0) {
			t.Fatalf("binding shape %s", raw)
		}
	}
}

func TestManifestAcceptsExactInventoryLimits(t *testing.T) {
	m := Empty("node")
	m.Files = []File{}
	for i := 0; i < 10000; i++ {
		m.Files = append(m.Files, File{Path: fmt.Sprintf("f%05d", i), SHA256: digest(nil)})
	}
	if _, e := m.Binding(); e != nil {
		t.Fatalf("file count boundary %v", e)
	}
	m = Empty("python")
	for i := 0; i < 8; i++ {
		m.Files = append(m.Files, File{Path: fmt.Sprintf("f%d", i), Size: 8 * 1024 * 1024, SHA256: digest(nil)})
	}
	if _, e := m.Binding(); e != nil {
		t.Fatalf("payload boundary %v", e)
	}
	m = Empty("node")
	p := strings.Repeat(strings.Repeat("x", 128)+"/", 7) + strings.Repeat("z", 121)
	if len(p) != 1024 {
		t.Fatal("bad fixture")
	}
	m.Files = []File{{Path: p, SHA256: digest(nil)}}
	if _, e := m.Binding(); e != nil {
		t.Fatalf("path boundary %v", e)
	}
	m.Files[0].Path = strings.Repeat("a/", 31) + "b"
	if _, e := m.Binding(); e != nil {
		t.Fatalf("depth boundary %v", e)
	}
}
func TestManifestRejectsNonportableWindowsCharacters(t *testing.T) {
	for _, p := range []string{"a?", "a*", "a\"", "a<", "a>", "a|", "a~1"} {
		m := manifest("node")
		m.Files[0].Path = p
		if _, e := m.Binding(); e == nil {
			t.Fatalf("accepted nonportable %q", p)
		}
	}
}
