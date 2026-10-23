package vmruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/platformfs"
	"golang.org/x/sys/windows"
)

// This catches the actual installed layout: the immutable app can be on D:
// while Windows' authoritative per-user data directory remains on C:. No VM,
// provider, registry mutation or drive mapping is used by this regression.
func TestWindowsLoadSeparateKnownFolderVolume(t *testing.T) {
	bundle, _ := fixtureBundle(t)
	data, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		t.Fatal(err)
	}
	instances, err := platformfs.PrivateTemp(data, "Foundry-load-regression-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(instances); err != nil {
			t.Error(err)
		}
	})
	different := !strings.EqualFold(filepath.VolumeName(bundle), filepath.VolumeName(instances))
	t.Logf("installed-layout cross_volume=%t", different)
	if os.Getenv("GITHUB_ACTIONS") == "true" && !different {
		t.Fatal("native CI must exercise the distinct install/data volumes")
	}
	// Characterize the previous defect independently of the implementation.
	if different {
		if _, err := filepath.Rel(bundle, instances); err == nil {
			t.Fatal("cross-drive Rel unexpectedly succeeded")
		}
	}
	runtime, err := Load(bundle, instances)
	if err != nil {
		t.Fatalf("installed known-folder layout rejected: %v", err)
	}
	if !runtime.Trusted() || runtime.Identity().ManifestDigest == "" {
		t.Fatal("runtime identity missing")
	}
	entries, err := os.ReadDir(instances)
	if err != nil || len(entries) != 0 {
		t.Fatal("metadata load caused instance effects")
	}
}

func TestWindowsLoadInstanceContainmentStillFailsClosed(t *testing.T) {
	bundle, separate := fixtureBundle(t)
	nested := filepath.Join(bundle, "instances")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for _, instance := range []string{bundle, nested, filepath.Join(separate, "missing"), filepath.VolumeName(separate) + `\`, `\\server\share\instances`, `C:relative`} {
		if _, err := Load(bundle, instance); err == nil {
			t.Fatal("unsafe instance location accepted")
		}
	}
	if runtime, err := Load(bundle, separate); err != nil || !runtime.Trusted() {
		t.Fatalf("separate same-volume runtime rejected: %v", err)
	}
}
