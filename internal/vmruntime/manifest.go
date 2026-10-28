package vmruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/platformfs"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Asset struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Manifest struct {
	SchemaVersion int     `json:"schema_version"`
	HostPlatform  string  `json:"host_platform"`
	GuestPlatform string  `json:"guest_platform"`
	Protocol      string  `json:"protocol"`
	PolicyVersion string  `json:"policy_version"`
	Runtime       string  `json:"runtime"`
	Kernel        string  `json:"kernel"`
	RootFS        string  `json:"rootfs"`
	Verifier      string  `json:"verifier"`
	Assets        []Asset `json:"assets"`
}

func canonicalDirectory(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == filepath.VolumeName(p)+string(filepath.Separator) {
		return errors.New("absolute non-root directory required")
	}
	resolved, e := filepath.EvalSymlinks(p)
	if e != nil || resolved != p {
		return errors.New("symlink-free existing directory required")
	}
	if e := platformfs.ValidateDirectory(p); e != nil {
		return errors.New("unsafe directory")
	}
	s, e := os.Lstat(p)
	if e != nil || !s.IsDir() {
		return errors.New("directory required")
	}
	return nil
}
func openAsset(root *os.Root, p string) (*platformfs.File, error) {
	if !safeRelative(p) {
		return nil, errors.New("invalid asset path")
	}
	parts := strings.Split(p, "/")
	for i := 1; i <= len(parts); i++ {
		s, e := root.Lstat(strings.Join(parts[:i], "/"))
		if e != nil || s.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe asset ancestor")
		}
	}
	f, e := platformfs.OpenRootRegular(root, p)
	if e != nil {
		return nil, errors.New("cannot open asset")
	}
	s, e := f.Stat()
	if e != nil || !s.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("asset must be regular")
	}
	return f, nil
}
func verifyAsset(root *os.Root, a Asset, dst io.Writer) error {
	f, e := openAsset(root, a.Path)
	if e != nil {
		return e
	}
	defer f.Close()
	s, e := f.Stat()
	if e != nil || s.Size() != a.Size {
		return errors.New("asset size mismatch")
	}
	h := sha256.New()
	w := io.Writer(h)
	if dst != nil {
		w = io.MultiWriter(h, dst)
	}
	n, e := io.Copy(w, io.LimitReader(f, a.Size+1))
	if e != nil || n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return errors.New("asset integrity mismatch")
	}
	return nil
}
func loadManifest(bundle string) (Manifest, domain.C4VMIdentity, error) {
	var m Manifest
	var id domain.C4VMIdentity
	if e := canonicalDirectory(bundle); e != nil {
		return m, id, e
	}
	root, e := os.OpenRoot(bundle)
	if e != nil {
		return m, id, errors.New("cannot open bundle")
	}
	defer root.Close()
	f, e := openAsset(root, "manifest.json")
	if e != nil {
		return m, id, e
	}
	b, e := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	f.Close()
	if e != nil || len(b) > 1024*1024 {
		return m, id, errors.New("manifest exceeds bound")
	}
	if e := strictJSON(b, &m); e != nil {
		return m, id, e
	}
	host := runtime.GOOS + "/" + runtime.GOARCH
	guest, exe := "", ""
	switch host {
	case "darwin/arm64":
		guest = "linux/arm64"
		exe = "runtime/foundry-vm-helper"
	case "windows/amd64":
		guest = "linux/amd64"
		exe = "runtime/qemu-system-x86_64.exe"
	default:
		return m, id, errors.New("unsupported VM host")
	}
	if m.SchemaVersion != 1 || m.HostPlatform != host || m.GuestPlatform != guest || m.Protocol != "foundry-vm-v1" || m.PolicyVersion != "go-vm-v1" || m.Runtime != exe || m.Kernel != "kernel" || m.RootFS != "initramfs.cpio.gz" || m.Verifier != "foundry-vm-guest" || len(m.Assets) < 4 || len(m.Assets) > 4096 {
		return m, id, errors.New("invalid bundle contract")
	}
	assets, e := verifyBundleAssets(root, m.Assets, []string{m.Runtime, m.Kernel, m.RootFS, m.Verifier})
	if e != nil {
		return m, id, e
	}
	sum := sha256.Sum256(b)
	id = domain.C4VMIdentity{SchemaVersion: 1, ManifestDigest: hex.EncodeToString(sum[:]), HostPlatform: host, GuestPlatform: guest, RuntimeDigest: assets[m.Runtime].SHA256, KernelDigest: assets[m.Kernel].SHA256, RootFSDigest: assets[m.RootFS].SHA256, VerifierDigest: assets[m.Verifier].SHA256, Protocol: m.Protocol, PolicyVersion: m.PolicyVersion}
	return m, id, nil
}

// Shared by both strict manifest loaders; no schema normalization is serialized.
func verifyBundleAssets(root *os.Root, inventory []Asset, required []string) (map[string]Asset, error) {
	assets := map[string]Asset{}
	folded := map[string]bool{}
	var total int64
	for _, a := range inventory {
		key := strings.ToLower(a.Path)
		total += a.Size
		if !safeRelative(a.Path) || a.Path == "manifest.json" || folded[key] || !hexDigest(a.SHA256) || a.Size < 0 || a.Size > 2*1024*1024*1024 || total > 4*1024*1024*1024 {
			return nil, errors.New("invalid asset inventory")
		}
		folded[key] = true
		assets[a.Path] = a
		if e := verifyAsset(root, a, nil); e != nil {
			return nil, e
		}
	}
	count := 0
	e := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		count++
		if count > 8192 {
			return errors.New("bundle entry bound exceeded")
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("bundle symlink refused")
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("special bundle entry refused")
		}
		if p != "manifest.json" {
			if _, ok := assets[p]; !ok {
				return errors.New("unlisted bundle asset")
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	for _, p := range required {
		if _, ok := assets[p]; !ok {
			return nil, errors.New("required asset missing")
		}
	}
	return assets, nil
}
