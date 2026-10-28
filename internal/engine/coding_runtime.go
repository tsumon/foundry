package engine

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/vmruntime"
	"reflect"
	"runtime"
	"time"
)

// NewCodingVMCapability accepts only sealed, installed runtime provenance.
// Construction and metadata never probe hardware or boot a guest.
func NewCodingVMCapability(g *model.Router, vm *vmruntime.Runtime, retained ...*vmruntime.Runtime) (*C4ControlledCapability, error) {
	if g == nil || vm == nil || !vm.Trusted() || vm.Identity().Validate() != nil || vm.Identity().SchemaVersion != 1 {
		return nil, ErrC4Unavailable
	}
	id, err := opaque()
	if err != nil {
		return nil, err
	}
	registry := map[domain.C4VMIdentity]*vmruntime.Runtime{vm.Identity(): vm}
	for _, old := range retained {
		if old == nil || !old.Trusted() || old.Identity().Validate() != nil {
			return nil, ErrC4Unavailable
		}
		if _, exists := registry[old.Identity()]; !exists {
			registry[old.Identity()] = old
		}
	}
	return &C4ControlledCapability{modelBacked: true, vm: vm, retainedVMs: registry, source: "embedded_vm_c4_control", models: g, config: C4Activation{SchemaVersion: 1, Enabled: true, RuntimeID: id, ExpiresAt: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)}}, nil
}

func (c *C4ControlledCapability) vmProfileMatches(p domain.C4Profile) bool {
	return c.vmForProfile(p) != nil
}

func (c *C4ControlledCapability) vmForProfile(p domain.C4Profile) *vmruntime.Runtime {
	if c == nil || p.Validate() != nil || p.Mode != "embedded_vm" || p.EmbeddedVM == nil {
		return nil
	}
	if r := c.retainedVMs[*p.EmbeddedVM]; r != nil && r.Trusted() && r.Identity() == *p.EmbeddedVM {
		if p.SchemaVersion == 2 {
			target, err := r.ProbeTargetV2(p.ID)
			if err != nil || target.RuntimeInventoryDigest != p.V2.RuntimeInventoryDigest {
				return nil
			}
		}
		return r
	}
	return nil
}

func (c *C4ControlledCapability) sourceFor(p *domain.C4Profile) string {
	if c == nil {
		return ""
	}
	if p == nil {
		return c.source
	}
	// Explicit Docker selection on a new model-backed task is separate
	// authority; no execution failure ever changes this frozen selection.
	if c.modelBacked && c.source == "embedded_vm_c4_control" && p.Mode == "docker" && runtime.GOOS != "windows" {
		return "docker_c4_control"
	}
	return c.source
}

// CodingVMProfile returns a detached copy of startup-owned metadata only.
func (e *Engine) CodingVMProfile() (domain.C4Profile, bool) {
	c := e.options.C4Controlled
	if c == nil || c.source != "embedded_vm_c4_control" || c.vm == nil || !c.vm.Trusted() {
		return domain.C4Profile{}, false
	}
	v := c.vm.Identity()
	return domain.C4Profile{SchemaVersion: 1, ControlVersion: 1, ID: "c4_go_model_v1", Mode: "embedded_vm", Platform: v.GuestPlatform, Verifier: "/opt/foundry/verify-go-v1", EmbeddedVM: &v}, true
}

// NewCodingDockerCapability configures a separate model-backed runtime. It does
// not inspect Docker, resolve credentials, read source or execute any command.
// The old fixture-only constructors and profile remain independent.
func NewCodingDockerCapability(g *model.Router) (*C4ControlledCapability, error) {
	if g == nil || runtime.GOOS == "windows" {
		return nil, ErrC4Unavailable
	}
	id, err := opaque()
	if err != nil {
		return nil, err
	}
	return &C4ControlledCapability{modelBacked: true, config: C4Activation{SchemaVersion: 1, Enabled: true, RuntimeID: id, ExpiresAt: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)}, source: "docker_c4_control", models: g}, nil
}

func isModelC4(m domain.Mission) bool { return m.Spec.C4 != nil && m.Spec.C4.IsModelBacked() }

// Historical structure must accept valid pre-catalog snapshots unchanged.
// New-task selection policy belongs to admission, not startup recovery.
func modelC4Profiles(profiles map[domain.Role]domain.Profile, snapshot *domain.MissionModelSnapshot) error {
	if snapshot == nil || snapshot.SchemaVersion != 2 || len(profiles) != 7 {
		return ErrC4Unavailable
	}
	for _, role := range domain.RolePolicyRoles() {
		s, ok := snapshot.Roles[role]
		if !ok || profiles[role] != s.Profile || s.Account == nil || s.Route == nil || (s.Account.Provider != "openai" && !(s.Account.Provider == "custom" && s.Route.Adapter == "compatible_api_responses")) || s.Profile.AccountID == "" || s.Route.ExtraUsage != "off" || domain.ValidateModelRoute(*s.Route, *s.Account) != nil {
			return ErrC4Unavailable
		}
	}
	return nil
}

func newModelC4Profiles(profiles map[domain.Role]domain.Profile, snapshot *domain.MissionModelSnapshot) error {
	if err := modelC4Profiles(profiles, snapshot); err != nil {
		return err
	}
	for _, role := range domain.RolePolicyRoles() {
		s := snapshot.Roles[role]
		if s.Account.AuthMethod == "openai_siwc" && s.Route.Environment == "live" && (s.Catalog == nil || s.Profile.CatalogOperationID == "" || s.Catalog.OperationID != s.Profile.CatalogOperationID) {
			return ErrC4Unavailable
		}
	}
	return nil
}

func c4SelectedProfiles(p *domain.C4Profile, profiles map[domain.Role]domain.Profile, snapshot *domain.MissionModelSnapshot) error {
	if p != nil && p.IsModelBacked() {
		return modelC4Profiles(profiles, snapshot)
	}
	return c4Profiles(profiles, snapshot)
}

// The ephemeral activation is pinned to this runtime, then bound to the frozen
// mission. A restart cannot revive an earlier readiness receipt.
func (e *Engine) c4Activation(m domain.Mission) C4Activation {
	c := e.options.C4Controlled.config
	if e.options.C4Controlled.modelBacked && m.Spec.C4 != nil && m.RepositoryImport != nil {
		c.Profile = *m.Spec.C4
		c.SourceRoot = m.RepositoryImport.Root
		c.BaseCommit = m.BaseCommit
		c.BaseDigest = m.RepositoryImport.BaseDigest
	}
	return c
}

func (e *Engine) CodingWorkbenchEnabled() bool {
	return e.options.C4Controlled != nil && e.options.C4Controlled.modelBacked
}
func (e *Engine) CodingWorkbenchAvailable() bool {
	if !e.CodingWorkbenchEnabled() {
		return false
	}
	deadline, err := time.Parse(time.RFC3339Nano, e.options.C4Controlled.config.ExpiresAt)
	return err == nil && e.c4Time().Before(deadline) && e.c4Root != "" && e.gateway != nil
}

// AdmitCodingModelCall is consulted before secret resolution and immediately
// before sending. Request identity is insufficient: compare every field with
// the sealed actual request and current epoch, role and approval authority.
func (e *Engine) AdmitCodingModelCall(ctx context.Context, q model.Request) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || ctx.Err() != nil || !e.CodingWorkbenchAvailable() || q.InvocationID == "" {
		return ErrC4Unavailable
	}
	v, err := e.s.GetInvocation(q.InvocationID)
	if err != nil || v.Status != "dispatching" || v.Kind != "model" || v.Name != "model.complete" || v.Role != q.Role {
		return ErrC4Unavailable
	}
	op := &operation{id: v.MissionID, epoch: v.Epoch, ctx: ctx}
	m, binding, auth, err := e.controlledAuthority(op, q.Role)
	if err != nil || !isModelC4(m) || e.c4ControlledGate(m, true) != nil || binding != v.BindingDigest || auth != v.AuthorityDigest {
		return ErrC4Unavailable
	}
	if _, err = e.currentC4Readiness(m); err != nil {
		return err
	}
	if err = e.s.CheckModelCall(m.ID, q.Role); err != nil {
		return err
	}
	raw, err := e.evidenceStore().Read(v.Input)
	if err != nil {
		return err
	}
	var input controlledInput
	if json.Unmarshal(raw, &input) != nil {
		return errors.New("invalid sealed input")
	}
	if err = e.validateV2ContextSummary(m, input.Prompt); err != nil {
		return err
	}
	expected := model.Request{InvocationID: v.ID, SnapshotSchema: input.SnapshotSchema, Selection: input.Selection, Profile: input.Profile, Role: input.Role, System: input.System, Prompt: input.Prompt, MaxOutputTokens: input.MaxOutputTokens}
	snapshot, err := domain.EffectiveModelSnapshot(m)
	if err != nil || !reflect.DeepEqual(q, expected) || input.Selection == nil || !reflect.DeepEqual(*input.Selection, snapshot.Roles[q.Role]) {
		return ErrC4Unavailable
	}
	return nil
}
