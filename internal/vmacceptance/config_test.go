package vmacceptance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdmissionRequiresExplicitFreshCanonicalPaths(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(parent, "bundle")
	if err := os.Mkdir(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	good := Config{NativeVMAcceptance: true, Bundle: bundle, DataDir: filepath.Join(parent, "new-private")}
	if err := good.validate("darwin/arm64"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"no-approval", "relative-bundle", "relative-data", "existing-data", "same-as-bundle", "unsupported-host"} {
		t.Run(kind, func(t *testing.T) {
			c := good
			host := "darwin/arm64"
			switch kind {
			case "no-approval":
				c.NativeVMAcceptance = false
			case "relative-bundle":
				c.Bundle = "bundle"
			case "relative-data":
				c.DataDir = "new-private"
			case "existing-data":
				c.DataDir = parent
			case "same-as-bundle":
				c.DataDir = bundle
			case "unsupported-host":
				host = "linux/amd64"
			}
			if c.validate(host) == nil {
				t.Fatal("unsafe native admission accepted")
			}
		})
	}
	if _, err := os.Stat(good.DataDir); !os.IsNotExist(err) {
		t.Fatal("admission created private directory", err)
	}
}

func TestAdmissionRejectsSymlinkParentWithoutEffects(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Skip("symlinks unavailable")
	}
	c := Config{NativeVMAcceptance: true, Bundle: parent, DataDir: filepath.Join(alias, "fresh")}
	if c.validate("darwin/arm64") == nil {
		t.Fatal("symlink parent accepted")
	}
	if _, err := os.Stat(filepath.Join(parent, "fresh")); !os.IsNotExist(err) {
		t.Fatal("admission had effects", err)
	}
}
