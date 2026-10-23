package vmacceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCancelledAdmissionHasNoEffects(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("native admission hosts")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	bundle := filepath.Join(root, "bundle")
	if e = os.Mkdir(bundle, 0700); e != nil {
		t.Fatal(e)
	}
	data := filepath.Join(root, "private")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	summary, e := Run(ctx, Config{NativeVMAcceptance: true, Bundle: bundle, DataDir: data})
	if e == nil || summary.Passed {
		t.Fatal("cancelled acceptance passed")
	}
	if _, e = os.Stat(data); !os.IsNotExist(e) {
		t.Fatal("cancelled acceptance had effects")
	}
}

func TestRunWithoutExplicitPermissionHasNoEffects(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "new-private-data")
	s, err := Run(context.Background(), Config{Bundle: root, DataDir: data})
	if err == nil || s.Passed || s.Stage != "admission" || s.FailureCode != "explicit_native_vm_acceptance_required" {
		t.Fatal(s, err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatal("created data without explicit permission")
	}
	raw, err := json.Marshal(s)
	if err != nil || len(raw) > 8192 {
		t.Fatal("invalid bounded summary")
	}
}

func TestMalformedBundleProducesRetainedSanitizedFailureWithoutNativeLaunch(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("native admission hosts")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	bundle := filepath.Join(root, "bundle")
	if e = os.Mkdir(bundle, 0700); e != nil {
		t.Fatal(e)
	}
	data := filepath.Join(root, "private")
	summary, e := Run(context.Background(), Config{NativeVMAcceptance: true, Bundle: bundle, DataDir: data})
	if e == nil || summary.Passed || summary.Stage != "bundle" || summary.FailureCode != "bundle_failed" {
		t.Fatal(summary, e)
	}
	raw, e := os.ReadFile(filepath.Join(data, "summary.json"))
	if e != nil {
		t.Fatal(e)
	}
	var persisted Summary
	if e = json.Unmarshal(raw, &persisted); e != nil || persisted.Passed || persisted.FailureCode != summary.FailureCode {
		t.Fatal("failure report missing", e)
	}
	entries, e := os.ReadDir(filepath.Join(data, "instances"))
	if e != nil || len(entries) != 0 {
		t.Fatal("invalid bundle launched instance", e)
	}
}

func TestReserveDataRootIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	if err := reserveDataRoot(path); err != nil {
		t.Fatal(err)
	}
	if err := reserveDataRoot(path); err == nil {
		t.Fatal("existing root claimed")
	}
}

func TestSourceDigestIncludesCommittedMetadata(t *testing.T) {
	root := filepath.Join(t.TempDir(), "source")
	if err := createRepository(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	before, err := sourceTreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := sourceTreeDigest(root)
	if err != nil || before == after {
		t.Fatal("git mutation was invisible", err)
	}
}
