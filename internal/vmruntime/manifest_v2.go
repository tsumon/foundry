package vmruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"io"
	"os"
	"runtime"
)

type ManifestV2 struct {
	SchemaVersion          int     `json:"schema_version"`
	HostPlatform           string  `json:"host_platform"`
	GuestPlatform          string  `json:"guest_platform"`
	Protocol               string  `json:"protocol"`
	PolicyVersion          string  `json:"policy_version"`
	Runtime                string  `json:"runtime"`
	Kernel                 string  `json:"kernel"`
	RootFS                 string  `json:"rootfs"`
	Verifier               string  `json:"verifier"`
	Assets                 []Asset `json:"assets"`
	RuntimeInventory       string  `json:"runtime_inventory"`
	RuntimeInventoryDigest string  `json:"runtime_inventory_digest"`
}

// bootAssets is private launch/copy material, never a manifest on disk.
type bootAssets struct {
	HostPlatform, Runtime, Kernel, RootFS string
	Assets                                []Asset
}

func (m Manifest) boot() bootAssets {
	return bootAssets{m.HostPlatform, m.Runtime, m.Kernel, m.RootFS, m.Assets}
}
func (m ManifestV2) boot() bootAssets {
	return bootAssets{m.HostPlatform, m.Runtime, m.Kernel, m.RootFS, m.Assets}
}
func readBundleFile(root *os.Root, path string, max int) ([]byte, error) {
	f, e := openAsset(root, path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if e != nil || len(b) > max {
		return nil, errors.New("bundle metadata exceeds bound")
	}
	return b, nil
}
func loadManifestV2(bundle string) (ManifestV2, domain.C4VMIdentity, RuntimeInventoryV2, error) {
	var m ManifestV2
	var id domain.C4VMIdentity
	var d RuntimeInventoryV2
	fail := func() (ManifestV2, domain.C4VMIdentity, RuntimeInventoryV2, error) {
		return ManifestV2{}, domain.C4VMIdentity{}, RuntimeInventoryV2{}, errors.New("invalid VM v2 bundle")
	}
	if canonicalDirectory(bundle) != nil {
		return fail()
	}
	root, e := os.OpenRoot(bundle)
	if e != nil {
		return fail()
	}
	defer root.Close()
	raw, e := readBundleFile(root, "manifest.json", 1<<20)
	if e != nil || strictJSONV2(raw, &m) != nil {
		return fail()
	}
	host := runtime.GOOS + "/" + runtime.GOARCH
	guest, exe := "", ""
	switch host {
	case "darwin/arm64":
		guest, exe = "linux/arm64", "runtime/foundry-vm-helper"
	case "windows/amd64":
		guest, exe = "linux/amd64", "runtime/qemu-system-x86_64.exe"
	default:
		return fail()
	}
	if m.SchemaVersion != 2 || m.HostPlatform != host || m.GuestPlatform != guest || m.Protocol != "foundry-vm-v2" || m.PolicyVersion != "multilang-vm-v2" || m.Runtime != exe || m.Kernel != "kernel" || m.RootFS != "initramfs.cpio.gz" || m.Verifier != "foundry-vm-guest-v2" || m.RuntimeInventory != RuntimeInventoryPathV2 || !hexDigest(m.RuntimeInventoryDigest) || len(m.Assets) < 5 || len(m.Assets) > 4096 {
		return fail()
	}
	assets, e := verifyBundleAssets(root, m.Assets, []string{m.Runtime, m.Kernel, m.RootFS, m.Verifier, m.RuntimeInventory})
	if e != nil {
		return fail()
	}
	b, e := readBundleFile(root, m.RuntimeInventory, MaxRuntimeInventoryBytesV2)
	if e != nil {
		return fail()
	}
	d, e = ParseRuntimeInventoryV2(b)
	if e != nil {
		return fail()
	}
	// Verify the bytes parsed here as well as the earlier complete inventory pass.
	assetHash := sha256.Sum256(b)
	digest, e := d.Digest()
	if e != nil || hex.EncodeToString(assetHash[:]) != assets[m.RuntimeInventory].SHA256 || int64(len(b)) != assets[m.RuntimeInventory].Size || digest != m.RuntimeInventoryDigest || d.GuestPlatform != guest || d.Supervisor.SHA256 != assets[m.Verifier].SHA256 || d.Supervisor.Size != assets[m.Verifier].Size {
		return fail()
	}
	sum := sha256.Sum256(raw)
	id = domain.C4VMIdentity{SchemaVersion: 2, ManifestDigest: hex.EncodeToString(sum[:]), HostPlatform: host, GuestPlatform: guest, RuntimeDigest: assets[m.Runtime].SHA256, KernelDigest: assets[m.Kernel].SHA256, RootFSDigest: assets[m.RootFS].SHA256, VerifierDigest: assets[m.Verifier].SHA256, Protocol: m.Protocol, PolicyVersion: m.PolicyVersion}
	return m, id, d, nil
}
func loadVerifiedBoot(bundle string, schema int) (bootAssets, domain.C4VMIdentity, error) {
	switch schema {
	case 1:
		m, id, e := loadManifest(bundle)
		return m.boot(), id, e
	case 2:
		m, id, _, e := loadManifestV2(bundle)
		return m.boot(), id, e
	default:
		return bootAssets{}, domain.C4VMIdentity{}, errors.New("unknown VM bundle schema")
	}
}

// The discriminator grants no authority: the selected full loader validates the
// exact field set and every asset, including rejecting aliased schema keys.
func bundleSchema(bundle string) (int, error) {
	if canonicalDirectory(bundle) != nil {
		return 0, errors.New("invalid bundle directory")
	}
	root, e := os.OpenRoot(bundle)
	if e != nil {
		return 0, e
	}
	defer root.Close()
	b, e := readBundleFile(root, "manifest.json", 1<<20)
	if e != nil {
		return 0, e
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if uniqueValue(dec, 0) != nil {
		return 0, errors.New("invalid schema discriminator")
	}
	if _, e := dec.Token(); e != io.EOF {
		return 0, errors.New("invalid schema discriminator")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(b, &obj) != nil {
		return 0, errors.New("invalid schema discriminator")
	}
	if bytes.Equal(obj["schema_version"], []byte("1")) {
		return 1, nil
	}
	if bytes.Equal(obj["schema_version"], []byte("2")) {
		return 2, nil
	}
	return 0, errors.New("unknown VM bundle schema")
}

// LoadV2 verifies installed bytes without hardware probing, process launch,
// instance allocation, or restoring authority from receipts.
func LoadV2(bundleRoot, instanceRoot string) (*Runtime, error) {
	m, id, d, e := loadManifestV2(bundleRoot)
	if e != nil {
		return nil, e
	}
	if e = canonicalDirectory(instanceRoot); e != nil {
		return nil, e
	}
	if e = validateInstanceLocation(bundleRoot, instanceRoot); e != nil {
		return nil, e
	}
	return &Runtime{bundleRoot: bundleRoot, instanceRoot: instanceRoot, manifestV2: m, inventoryV2: d, identity: id, production: true, launch: launchNative, sessions: map[string]*Session{}}, nil
}
