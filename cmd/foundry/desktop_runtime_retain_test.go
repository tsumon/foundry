package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"foundry/internal/platformfs"
	"foundry/internal/store"
	"foundry/internal/vmruntime"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRetainDesktopRuntimeExplicitLocalOnly(t *testing.T) {
	for _, args := range [][]string{nil, {"--desktop"}, {"--coding-workbench"}} {
		if handled, err := retainDesktopRuntime(args); handled || err != nil {
			t.Fatal("normal startup intercepted", err)
		}
	}
	for _, args := range [][]string{{"--retain-vm-bundle"}, {"--retain-vm-bundle", "source", "--runtime-archive", "root", "--expected-vm-identity", "missing", "--desktop"}} {
		if handled, err := retainDesktopRuntime(args); !handled || err == nil {
			t.Fatal("invalid import accepted")
		}
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("supported VM host only")
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := platformfs.PrivateTemp(parent, "retain-")
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "bundle")
	if err := os.Mkdir(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	instances := filepath.Join(root, "instances")
	if err := os.Mkdir(instances, 0700); err != nil {
		t.Fatal(err)
	}
	exe, guest := "runtime/foundry-vm-helper", "linux/arm64"
	if runtime.GOOS == "windows" {
		exe, guest = "runtime/qemu-system-x86_64.exe", "linux/amd64"
	}
	m := vmruntime.Manifest{SchemaVersion: 1, HostPlatform: runtime.GOOS + "/" + runtime.GOARCH, GuestPlatform: guest, Protocol: "foundry-vm-v1", PolicyVersion: "go-vm-v1", Runtime: exe, Kernel: "kernel", RootFS: "initramfs.cpio.gz", Verifier: "foundry-vm-guest"}
	for _, path := range []string{m.Runtime, m.Kernel, m.RootFS, m.Verifier} {
		b := []byte("inert, never executable: " + path)
		sum := sha256.Sum256(b)
		m.Assets = append(m.Assets, vmruntime.Asset{Path: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(b))})
		if err := os.MkdirAll(filepath.Dir(filepath.Join(bundle, path)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bundle, path), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	vm, err := vmruntime.Load(bundle, instances)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(vm.Identity())
	expected := filepath.Join(root, "expected.json")
	if err := os.WriteFile(expected, b, 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "archive")
	if handled, err := retainDesktopRuntime([]string{"--retain-vm-bundle", bundle, "--runtime-archive", archive, "--expected-vm-identity", expected}); !handled || err != nil {
		t.Fatal("import rejected", err)
	}
	retained, err := vmruntime.LoadRetained(archive, instances)
	if err != nil || len(retained) != 1 || retained[0].Identity() != vm.Identity() {
		t.Fatal("wrong archive", err)
	}
	entries, _ := os.ReadDir(instances)
	if len(entries) != 0 {
		t.Fatal("import executed VM")
	}
	data := filepath.Join(root, "coding")
	if err := platformfs.PrivateDirectory(data); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(data, "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t.Setenv("FOUNDRY_VM_BUNDLE", bundle)
	secrets := &codingForbiddenSecrets{}
	models, e, err := newCodingRuntime(s, data, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	defer models.siwc.Close()
	defer models.acceptance.Close()
	p, ok := e.CodingVMProfile()
	if !ok || *p.EmbeddedVM != vm.Identity() {
		t.Fatal("installed default changed")
	}
	loaded, err := vmruntime.LoadRetained(filepath.Join(data, "vm-bundles"), filepath.Join(data, "vm-instances"))
	if err != nil || len(loaded) != 1 || loaded[0].Identity() != vm.Identity() {
		t.Fatal("coding startup did not retain verified runtime", err)
	}
	if secrets.calls != 0 {
		t.Fatal("startup touched credentials")
	}
	if err := os.WriteFile(filepath.Join(data, "vm-bundles", vm.Identity().ManifestDigest, "kernel"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := newCodingRuntime(s, data, secrets); err == nil {
		t.Fatal("damaged retained runtime silently fell back")
	}
}
