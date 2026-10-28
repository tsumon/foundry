package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// C4ProfileV2 binds installed runtime and inspected immutable inputs. It does
// not carry commands, execution authority, or mutable repository state.
type C4ProfileV2 struct {
	Language                string `json:"language"`
	RunnerID                string `json:"runner_id"`
	RuntimeInventoryDigest  string `json:"runtime_inventory_digest"`
	DependencyBundleDigest  string `json:"dependency_bundle_digest"`
	ProtectedInputsDigest   string `json:"protected_inputs_digest"`
	ExecutionContractDigest string `json:"execution_contract_digest"`
}

// c4LanguageProfileV2 is a closed lookup, never a caller-extensible registry.
func c4LanguageProfileV2(id string) (language, runner, verifier string, err error) {
	switch id {
	case "c4_node_model_v2":
		return "javascript", "node-test-v2", "/opt/foundry/verify-node-v2", nil
	case "c4_typescript_model_v2":
		return "typescript", "typescript-node-test-v2", "/opt/foundry/verify-typescript-v2", nil
	case "c4_python_model_v2":
		return "python", "python-unittest-v2", "/opt/foundry/verify-python-v2", nil
	default:
		return "", "", "", errors.New("unsupported v2 language profile")
	}
}

func (p C4Profile) validateV2() error {
	language, runner, verifier, err := c4LanguageProfileV2(p.ID)
	if err != nil {
		return err
	}
	if p.ControlVersion != 1 || p.Mode != "embedded_vm" || p.ImageID != "" || p.DockerPath != "" || p.Endpoint != "" || p.Verifier != verifier || p.EmbeddedVM == nil || p.EmbeddedVM.SchemaVersion != 2 || p.EmbeddedVM.Validate() != nil || p.Platform != p.EmbeddedVM.GuestPlatform || p.V2 == nil || p.V2.Language != language || p.V2.RunnerID != runner {
		return errors.New("invalid or unsupported v2 VM profile")
	}
	for _, digest := range []string{p.V2.RuntimeInventoryDigest, p.V2.DependencyBundleDigest, p.V2.ProtectedInputsDigest, p.V2.ExecutionContractDigest} {
		if !c4SHA.MatchString(digest) {
			return errors.New("invalid v2 profile digest")
		}
	}
	return nil
}

// C4TestPolicyV2 describes common fixed isolation and evidence limits. Runtime
// inventory binds image-owned commands and environment separately.
type C4TestPolicyV2 struct {
	CPUs                   int       `json:"cpus"`
	MemoryBytes            int64     `json:"memory_bytes"`
	PidsLimit              int       `json:"pids_limit"`
	TimeoutSeconds         int       `json:"timeout_seconds"`
	OutputLimitBytes       int       `json:"output_limit_bytes"`
	UID                    int       `json:"uid"`
	GID                    int       `json:"gid"`
	ReadOnlyRoot           bool      `json:"read_only_root"`
	ReadOnlySource         bool      `json:"read_only_source"`
	ReadOnlyDependencies   bool      `json:"read_only_dependencies"`
	NoNewPrivileges        bool      `json:"no_new_privileges"`
	Network                string    `json:"network"`
	Pull                   string    `json:"pull"`
	Logging                string    `json:"logging"`
	DisableHealthcheck     bool      `json:"disable_healthcheck"`
	DropCapabilities       []string  `json:"drop_capabilities"`
	Ports                  []string  `json:"ports"`
	Tmpfs                  []C4Tmpfs `json:"tmpfs"`
	SourceMount            C4Tmpfs   `json:"source_mount"`
	DependencyMount        C4Tmpfs   `json:"dependency_mount"`
	RunnerID               string    `json:"runner_id"`
	MaxReportBytes         int       `json:"max_report_bytes"`
	MaxCases               int       `json:"max_cases"`
	MaxSourceFiles         int       `json:"max_source_files"`
	MaxSourceBytes         int64     `json:"max_source_bytes"`
	MaxDependencyFiles     int       `json:"max_dependency_files"`
	MaxDependencyBytes     int64     `json:"max_dependency_bytes"`
	MaxDependencyFileBytes int64     `json:"max_dependency_file_bytes"`
}

type C4VMPolicyV2 struct {
	Version          string         `json:"version"`
	GuestMemoryBytes int64          `json:"guest_memory_bytes"`
	LifetimeSeconds  int            `json:"lifetime_seconds"`
	HostShares       bool           `json:"host_shares"`
	Test             C4TestPolicyV2 `json:"test"`
}

// FixedC4VMPolicyV2 returns fresh slice storage and rejects unknown profiles.
func FixedC4VMPolicyV2(profileID string) (C4VMPolicyV2, error) {
	_, runner, _, err := c4LanguageProfileV2(profileID)
	if err != nil {
		return C4VMPolicyV2{}, err
	}
	dependencyPath := "/workspace/node_modules"
	if profileID == "c4_python_model_v2" {
		dependencyPath = "/foundry-deps/python"
	}
	return C4VMPolicyV2{Version: "multilang-vm-v2", GuestMemoryBytes: 2 << 30, LifetimeSeconds: 180,
		Test: C4TestPolicyV2{CPUs: 1, MemoryBytes: 1 << 30, PidsLimit: 128, TimeoutSeconds: 120, OutputLimitBytes: 64 << 10, UID: 65534, GID: 65534,
			ReadOnlyRoot: true, ReadOnlySource: true, ReadOnlyDependencies: true, NoNewPrivileges: true,
			Network: "none", Pull: "never", Logging: "none", DisableHealthcheck: true, DropCapabilities: []string{"ALL"}, Ports: []string{},
			Tmpfs:           []C4Tmpfs{{Path: "/tmp", SizeBytes: 64 << 20, Inodes: 16384, Nosuid: true, Nodev: true}, {Path: "/foundry-build", SizeBytes: 256 << 20, Inodes: 16384, Exec: true, Nosuid: true, Nodev: true}},
			SourceMount:     C4Tmpfs{Path: "/workspace", SizeBytes: 16 << 20, Inodes: 16384, Nosuid: true, Nodev: true},
			DependencyMount: C4Tmpfs{Path: dependencyPath, SizeBytes: 80 << 20, Inodes: 32768, Nosuid: true, Nodev: true},
			RunnerID:        runner, MaxReportBytes: 2 << 20, MaxCases: 4096, MaxSourceFiles: 500, MaxSourceBytes: 10 << 20,
			MaxDependencyFiles: 10000, MaxDependencyBytes: 64 << 20, MaxDependencyFileBytes: 8 << 20,
		},
	}, nil
}

func (p C4Profile) profileDigestV2() string {
	// Like the v1 digest, this hashes a declaration; Validate gates its authority.
	// Unknown IDs receive a zero policy, never a fallback execution policy.
	policy, _ := FixedC4VMPolicyV2(p.ID)
	raw, _ := json.Marshal(struct {
		Profile C4Profile    `json:"profile"`
		Policy  C4VMPolicyV2 `json:"policy"`
	}{p, policy})
	h := sha256.Sum256(append([]byte("foundry:c4:v2:vm-profile\n"), raw...))
	return hex.EncodeToString(h[:])
}

// IsModelBacked recognizes only validated, closed model profile declarations.
func (p C4Profile) IsModelBacked() bool {
	if p.Validate() != nil {
		return false
	}
	return p.ID == "c4_go_model_v1" || p.SchemaVersion == 2
}

// AcceptanceCommands returns the exact validated verifier in fresh storage.
func (p C4Profile) AcceptanceCommands() ([]string, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return []string{p.Verifier}, nil
}
