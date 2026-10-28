package engine

import (
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/vmruntime"
	"time"
)

// CodingProfileTemplateV2 describes installed eligibility; it lacks project input
// digests and cannot pass C4Profile.Validate or authorize readiness/execution.
type CodingProfileLimitsV2 struct {
	MaxCalls           int   `json:"max_calls"`
	MaxRepairs         int   `json:"max_repairs"`
	MaxSourceFiles     int   `json:"max_source_files"`
	MaxSourceBytes     int64 `json:"max_source_bytes"`
	MaxDependencyFiles int   `json:"max_dependency_files"`
	MaxDependencyBytes int64 `json:"max_dependency_bytes"`
}
type CodingProfileTemplateV2 struct {
	SchemaVersion          int                   `json:"schema_version"`
	ProfileID              string                `json:"profile_id"`
	Language               string                `json:"language"`
	RunnerID               string                `json:"runner_id"`
	Verifier               string                `json:"verifier"`
	Mode                   string                `json:"mode"`
	EmbeddedVM             *domain.C4VMIdentity  `json:"embedded_vm,omitempty"`
	RuntimeInventoryDigest string                `json:"runtime_inventory_digest,omitempty"`
	Available              bool                  `json:"available"`
	Reason                 string                `json:"reason"`
	Limits                 CodingProfileLimitsV2 `json:"limits"`
}

func NewCodingVMCapabilityWithV2(g *model.Router, v1, v2 *vmruntime.Runtime, retained ...*vmruntime.Runtime) (*C4ControlledCapability, error) {
	var c *C4ControlledCapability
	var err error
	if v1 != nil {
		c, err = NewCodingVMCapability(g, v1, retained...)
	} else {
		if g == nil {
			return nil, ErrC4Unavailable
		}
		id, e := opaque()
		if e != nil {
			return nil, e
		}
		c = &C4ControlledCapability{modelBacked: true, source: "embedded_vm_c4_control", models: g, config: C4Activation{SchemaVersion: 1, Enabled: true, RuntimeID: id, ExpiresAt: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)}, retainedVMs: map[domain.C4VMIdentity]*vmruntime.Runtime{}}
		for _, r := range retained {
			if r == nil || !r.Trusted() || r.Identity().Validate() != nil {
				return nil, ErrC4Unavailable
			}
			c.retainedVMs[r.Identity()] = r
		}
	}
	if err != nil {
		return nil, err
	}
	if v2 != nil {
		if !v2.Trusted() || v2.Identity().SchemaVersion != 2 {
			return nil, ErrC4Unavailable
		}
		if _, err = v2.ProbeTargetV2("c4_node_model_v2"); err != nil {
			return nil, err
		}
		c.currentV2 = v2
		c.retainedVMs[v2.Identity()] = v2
	}
	return c, nil
}
func (e *Engine) CodingProfilesV2() []CodingProfileTemplateV2 {
	out := []CodingProfileTemplateV2{}
	c := e.options.C4Controlled
	installed := c != nil && c.currentV2 != nil && c.currentV2.Trusted() && c.currentV2.ValidateInstalledV2() == nil
	for _, tuple := range [][4]string{{"c4_node_model_v2", "javascript", "node-test-v2", "/opt/foundry/verify-node-v2"}, {"c4_typescript_model_v2", "typescript", "typescript-node-test-v2", "/opt/foundry/verify-typescript-v2"}, {"c4_python_model_v2", "python", "python-unittest-v2", "/opt/foundry/verify-python-v2"}} {
		row := CodingProfileTemplateV2{SchemaVersion: 2, ProfileID: tuple[0], Language: tuple[1], RunnerID: tuple[2], Verifier: tuple[3], Mode: "embedded_vm", Reason: "embedded_vm_v2_bundle_unavailable", Limits: CodingProfileLimitsV2{16, 2, 500, 10 << 20, 10000, 64 << 20}}
		if installed {
			if target, err := c.currentV2.ProbeTargetV2(row.ProfileID); err == nil {
				id := target.Identity
				row.EmbeddedVM = &id
				row.RuntimeInventoryDigest = target.RuntimeInventoryDigest
				row.Available = e.CodingWorkbenchAvailable()
				row.Reason = ""
				if !row.Available {
					row.Reason = "coding_runtime_unavailable_or_expired"
				}
			}
		}
		out = append(out, row)
	}
	return out
}
func (e *Engine) currentV2Matches(id string, identity domain.C4VMIdentity, inventory string) bool {
	for _, row := range e.CodingProfilesV2() {
		if row.ProfileID == id && row.Available && row.EmbeddedVM != nil && *row.EmbeddedVM == identity && row.RuntimeInventoryDigest == inventory {
			return true
		}
	}
	return false
}
