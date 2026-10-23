package vmruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func fixtureBundle(t *testing.T) (string, string) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	instances, _ := filepath.EvalSymlinks(t.TempDir())
	host := runtime.GOOS + "/" + runtime.GOARCH
	guest := "linux/arm64"
	exe := "runtime/foundry-vm-helper"
	if host == "windows/amd64" {
		guest = "linux/amd64"
		exe = "runtime/qemu-system-x86_64.exe"
	}
	m := Manifest{SchemaVersion: 1, HostPlatform: host, GuestPlatform: guest, Protocol: "foundry-vm-v1", PolicyVersion: "go-vm-v1", Runtime: exe, Kernel: "kernel", RootFS: "initramfs.cpio.gz", Verifier: "foundry-vm-guest"}
	for _, p := range []string{exe, m.Kernel, m.RootFS, m.Verifier, "LICENSE"} {
		b := []byte("fixture-" + p)
		h := sha256.Sum256(b)
		m.Assets = append(m.Assets, Asset{Path: p, SHA256: hex.EncodeToString(h[:]), Size: int64(len(b))})
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0700)
		os.WriteFile(filepath.Join(root, p), b, 0600)
	}
	b, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(root, "manifest.json"), b, 0600)
	return root, instances
}
func TestLoadIsEffectFreeAndRejectsTampering(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("supported hosts only")
	}
	root, instances := fixtureBundle(t)
	r, e := Load(root, instances)
	if e != nil {
		t.Fatal(e)
	}
	if !r.Trusted() || r.Identity().ManifestDigest == "" {
		t.Fatal("missing immutable identity")
	}
	entries, _ := os.ReadDir(instances)
	if len(entries) != 0 {
		t.Fatal("Load created instance effects")
	}
	for _, kind := range []string{"changed", "unlisted", "symlink", "missing", "traversal", "platform", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			root, instances := fixtureBundle(t)
			p := filepath.Join(root, "kernel")
			switch kind {
			case "changed":
				os.WriteFile(p, []byte("changed"), 0600)
			case "unlisted":
				os.WriteFile(filepath.Join(root, "evil"), []byte("x"), 0600)
			case "symlink":
				os.Remove(p)
				os.Symlink("LICENSE", p)
			case "missing":
				os.Remove(p)
			default:
				b, _ := os.ReadFile(filepath.Join(root, "manifest.json"))
				var m Manifest
				json.Unmarshal(b, &m)
				if kind == "traversal" {
					m.Assets[0].Path = "../escape"
				}
				if kind == "platform" {
					m.GuestPlatform = "linux/386"
				}
				if kind == "duplicate" {
					m.Assets = append(m.Assets, m.Assets[0])
				}
				b, _ = json.Marshal(m)
				os.WriteFile(filepath.Join(root, "manifest.json"), b, 0600)
			}
			if _, e := Load(root, instances); e == nil {
				t.Fatal("unsafe bundle accepted")
			}
		})
	}
}
