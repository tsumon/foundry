package domain

import "errors"

// C4VMIdentity is immutable installed-bundle identity, not permission to boot.
type C4VMIdentity struct {
	SchemaVersion  int    `json:"schema_version"`
	ManifestDigest string `json:"manifest_digest"`
	HostPlatform   string `json:"host_platform"`
	GuestPlatform  string `json:"guest_platform"`
	RuntimeDigest  string `json:"runtime_digest"`
	KernelDigest   string `json:"kernel_digest"`
	RootFSDigest   string `json:"rootfs_digest"`
	VerifierDigest string `json:"verifier_digest"`
	Protocol       string `json:"protocol"`
	PolicyVersion  string `json:"policy_version"`
}

func (v C4VMIdentity) Validate() error {
	if v.SchemaVersion != 1 || v.Protocol != "foundry-vm-v1" || v.PolicyVersion != "go-vm-v1" || !(v.HostPlatform == "darwin/arm64" && v.GuestPlatform == "linux/arm64" || v.HostPlatform == "windows/amd64" && v.GuestPlatform == "linux/amd64") {
		return errors.New("unsupported VM identity")
	}
	for _, digest := range []string{v.ManifestDigest, v.RuntimeDigest, v.KernelDigest, v.RootFSDigest, v.VerifierDigest} {
		if !c4SHA.MatchString(digest) {
			return errors.New("invalid VM digest")
		}
	}
	return nil
}

type C4VMPolicy struct {
	Version          string   `json:"version"`
	GuestMemoryBytes int64    `json:"guest_memory_bytes"`
	LifetimeSeconds  int      `json:"lifetime_seconds"`
	HostShares       bool     `json:"host_shares"`
	Test             C4Policy `json:"test"`
}

func FixedC4VMPolicy() C4VMPolicy {
	return C4VMPolicy{Version: "go-vm-v1", GuestMemoryBytes: 2 << 30, LifetimeSeconds: 180, Test: FixedC4Policy()}
}

// ExecutionIdentity fills historical verification image_id compatibility keys
// with a backend-qualified bundle digest; it never invents a Docker image.
func (p C4Profile) ExecutionIdentity() string {
	if p.Mode == "embedded_vm" {
		if p.EmbeddedVM == nil {
			return ""
		}
		return "embedded-vm:sha256:" + p.EmbeddedVM.ManifestDigest
	}
	return p.ImageID
}

// C4SourceMatches is exhaustive: unknown source kinds never inherit authority.
func C4SourceMatches(mode, source string) bool {
	switch mode {
	case "docker":
		return source == "docker_c4_control"
	case "synthetic":
		return source == "synthetic_c4_control"
	case "embedded_vm":
		return source == "embedded_vm_c4_control"
	default:
		return false
	}
}
