package store

import (
	"context"
	"encoding/json"
	"foundry/internal/dependencyadmission"
	"os"
	"path/filepath"
	"testing"
)

func TestDependencyAdmissionImmutableAndStrict(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	bundles := filepath.Join(root, "bundles")
	input := filepath.Join(root, "input")
	os.Mkdir(bundles, 0700)
	os.Mkdir(input, 0700)
	a, err := dependencyadmission.ImportNode(context.Background(), bundles, input, dependencyadmission.NodeSource{PackageJSON: dependencyadmission.SourceBytes{Path: "package.json", Data: []byte(`{"name":"app","version":"1.0.0"}`)}, PackageLock: dependencyadmission.SourceBytes{Path: "package-lock.json", Data: []byte(`{"lockfileVersion":3,"packages":{"":{"name":"app","version":"1.0.0"}}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SaveDependencyAdmission(a); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveDependencyAdmission(a); err != nil {
		t.Fatal("idempotent admission", err)
	}
	got, err := s.GetDependencyAdmission(a.Digest)
	if err != nil || got.Digest != a.Digest {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(a)
	raw = append(raw[:len(raw)-1], []byte(`,"private_root":"secret"}`)...)
	s.db.Exec(`UPDATE dependency_admissions SET data=? WHERE digest=?`, raw, a.Digest)
	if _, err = s.GetDependencyAdmission(a.Digest); err == nil {
		t.Fatal("accepted unknown record")
	}
	a.Digest = ""
	if s.SaveDependencyAdmission(a) == nil {
		t.Fatal("accepted unknown publication")
	}
}
