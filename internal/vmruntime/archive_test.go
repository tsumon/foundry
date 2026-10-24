package vmruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func TestArchiveRetainsExactRuntimeWithoutExecution(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("supported hosts only")
	}
	source, instances := fixtureBundle(t)
	original, err := Load(source, instances)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	archive := filepath.Join(parent, "archive")
	retained, err := Retain(source, archive, original.Identity())
	if err != nil {
		t.Fatal(err)
	}
	if retained != filepath.Join(archive, original.Identity().ManifestDigest) {
		t.Fatal("not content addressed")
	}
	if again, err := Retain(source, archive, original.Identity()); err != nil || again != retained {
		t.Fatal("not idempotent", err)
	}
	loaded, err := LoadRetained(archive, instances)
	if err != nil || len(loaded) != 1 || loaded[0].Identity() != original.Identity() {
		t.Fatal("archive identity lost", err)
	}
	if _, err := Load(source, instances); err != nil {
		t.Fatal("source changed", err)
	}
	entries, err := os.ReadDir(instances)
	if err != nil || len(entries) != 0 {
		t.Fatal("archive executed or allocated instance", err)
	}
	if err := os.WriteFile(filepath.Join(retained, "kernel"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRetained(archive, instances); err == nil {
		t.Fatal("tampered archive accepted")
	}
	if _, err := Retain(source, archive, original.Identity()); err == nil {
		t.Fatal("tampered archive overwritten")
	}
}

func TestArchiveRejectsWrongIdentityAndUntrustedPaths(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("supported hosts only")
	}
	source, instances := fixtureBundle(t)
	r, err := Load(source, instances)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	for _, bad := range []domain.C4VMIdentity{{}, func() domain.C4VMIdentity { v := r.Identity(); v.KernelDigest = strings.Repeat("a", 64); return v }()} {
		archive := filepath.Join(parent, "must-not-exist")
		if _, err := Retain(source, archive, bad); err == nil {
			t.Fatal("wrong expected identity accepted")
		}
		if _, err := os.Lstat(archive); !os.IsNotExist(err) {
			t.Fatal("mismatch created archive")
		}
	}
	if _, err := Retain(source, filepath.Join(source, "nested"), r.Identity()); err == nil {
		t.Fatal("archive inside source accepted")
	}
	archive := filepath.Join(parent, "archive")
	if _, err := Retain(source, archive, r.Identity()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(archive, ".staging-incomplete"), 0700); err != nil {
		t.Fatal(err)
	}
	if loaded, err := LoadRetained(archive, instances); err != nil || len(loaded) != 1 {
		t.Fatal("incomplete staging became authority", err)
	}
	if err := os.Mkdir(filepath.Join(archive, strings.Repeat("a", 64)), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRetained(archive, instances); err == nil {
		t.Fatal("incomplete published archive accepted")
	}
}

func TestArchiveNeverFollowsAliasOrPublicDirectory(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Unix permission and symlink fixture")
	}
	source, instances := fixtureBundle(t)
	r, err := Load(source, instances)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	public := filepath.Join(parent, "public")
	if err := os.Mkdir(public, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(public, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Retain(source, public, r.Identity()); err == nil {
		t.Fatal("nonprivate archive accepted")
	}
	private := filepath.Join(parent, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(private, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Retain(source, alias, r.Identity()); err == nil {
		t.Fatal("archive symlink accepted")
	}
	entries, _ := os.ReadDir(private)
	if len(entries) != 0 {
		t.Fatal("unsafe archive caused writes")
	}
}
