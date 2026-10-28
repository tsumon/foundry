package engine

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/sandbox"
	"reflect"
)

var ErrC4Unavailable = errors.New("C4 synthetic capability unavailable or authority invalid")

// C4SyntheticCapability is exclusively an in-process disposable-fixture seam.
// It must contain local synthetic models and a synthetic driver, never a router.
// No production startup, CLI, environment, or request constructs this capability.
type C4SyntheticCapability struct {
	driver               *sandbox.C4SyntheticDriver
	models               model.Gateway
	dockerPath, endpoint string
}

func NewC4SyntheticCapability(driver *sandbox.C4SyntheticDriver, models model.Gateway, dockerPath, endpoint string) *C4SyntheticCapability {
	return &C4SyntheticCapability{driver: driver, models: models, dockerPath: dockerPath, endpoint: endpoint}
}
func (e *Engine) C4SimulationEnabled() bool {
	c := e.options.C4
	return e.options.C4Controlled == nil && e.c4Root != "" && c != nil && c.driver != nil && c.models != nil
}

// C4SimulationAvailable is a structural display hint, never action consent.
func (e *Engine) C4SimulationAvailable(m domain.Mission) bool {
	return m.Spec.C4 != nil && m.Spec.C4.ControlVersion == 0 && e.c4Gate(m) == nil
}
func isC4(m domain.Mission) bool { return m.Spec.C4 != nil || m.C4Invalid || len(m.C4Operations) > 0 }

func c4Profiles(profiles map[domain.Role]domain.Profile, snapshot *domain.MissionModelSnapshot) error {
	if snapshot != nil || len(profiles) != 7 {
		return ErrC4Unavailable
	}
	for _, role := range []domain.Role{domain.Main, domain.Planner, domain.Researcher, domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
		p, ok := profiles[role]
		if !ok || p.ID == "" || p.Model == "" || p.Kind != "synthetic" || p.BaseURL != "https://synthetic.invalid" || p.Protocol != "chat_completions" || p.HasKey || p.AccountID != "" || p.RouteID != "" || p.AccountRevision != 0 || p.CredentialVersion != 0 {
			return ErrC4Unavailable
		}
	}
	return nil
}

func c4Structure(m domain.Mission) error {
	if !isC4(m) {
		return nil
	}
	mode := m.WorkspaceMode
	if isModelC4(m) {
		mode, _, _ = domain.BaseWorkspaceMode(mode)
	}
	if domain.ValidateC4Mission(m) != nil || domain.ValidateMissionModelSnapshot(m) != nil || m.Spec.C4 == nil || (m.Spec.C4.Mode != "synthetic" && m.Spec.C4.ControlVersion != 1) || mode != "generations_v1" || m.CodingVersion != 1 || m.ImportVersion != 1 || m.RepositoryImport == nil || m.ToolMode != "" || m.TaskBindings != nil || m.MCPBindings != nil || m.ModelSnapshotInvalid || !validC4Acceptance(m.Spec.C4, m.Spec.Acceptance) {
		return ErrC4Unavailable
	}
	return c4SelectedProfiles(m.Spec.C4, m.Profiles, m.ModelSnapshot)
}
func (e *Engine) c4Gate(m domain.Mission) error {
	if e.options.C4Controlled != nil || m.Spec.C4 != nil && m.Spec.C4.ControlVersion == 1 {
		// Ongoing dispatched operations may finish after receipt/config expiry;
		// preparation and dispatch perform the independent live-time checks.
		return e.c4ControlledGate(m, false)
	}
	if !isC4(m) {
		return nil
	}
	if !e.C4SimulationEnabled() || c4Structure(m) != nil {
		return ErrC4Unavailable
	}
	c := e.options.C4
	if m.Spec.C4.DockerPath != c.dockerPath || m.Spec.C4.Endpoint != c.endpoint {
		return ErrC4Unavailable
	}
	return nil
}
func (e *Engine) c4CreateGate(r CreateRequest) error {
	if e.options.CodingWorkbench && !e.CodingWorkbenchEnabled() {
		return ErrC4Unavailable
	}
	if e.CodingWorkbenchEnabled() {
		c := e.options.C4Controlled
		if r.Spec.C4 == nil || !domain.C4SourceMatches(r.Spec.C4.Mode, c.sourceFor(r.Spec.C4)) || c.sourceFor(r.Spec.C4) == "embedded_vm_c4_control" && !c.vmProfileMatches(*r.Spec.C4) {
			return ErrC4Unavailable
		}
		if !e.CodingWorkbenchAvailable() || r.Spec.C4 == nil || !r.Spec.C4.IsModelBacked() || r.Spec.C4.Validate() != nil || r.ImportVersion != 1 || r.ImportSelection == nil || r.CodingVersion != 1 || r.WorkspaceMode != "generations_v1" || r.ToolMode != "" || r.MCPSelection != nil || r.CapabilitySelection != nil || !validC4Acceptance(r.Spec.C4, r.Spec.Acceptance) || r.Spec.MaxCalls > 16 {
			return ErrC4Unavailable
		}
		_, profiles, snapshot, _, err := e.s.ResolveMissionModelSelection(r.Spec.ProfileID)
		if err != nil {
			return err
		}
		return newModelC4Profiles(profiles, snapshot)
	}
	if e.options.C4Controlled != nil || r.Spec.C4 != nil && r.Spec.C4.ControlVersion == 1 {
		c := e.options.C4Controlled
		if c == nil || e.options.C4 != nil || ValidateC4Activation(c.config, e.c4Time()) != nil || r.Spec.C4 == nil || r.Spec.C4.ProfileDigest() != c.config.Profile.ProfileDigest() || r.ImportVersion != 1 || r.ImportSelection == nil || r.CodingVersion != 1 || r.WorkspaceMode != "generations_v1" || r.ToolMode != "" || r.MCPSelection != nil || r.CapabilitySelection != nil || !validC4Acceptance(r.Spec.C4, r.Spec.Acceptance) {
			return ErrC4Unavailable
		}
		v, err := e.s.GetRepositoryInspection(r.ImportSelection.InspectionID)
		if err != nil || domain.ValidateImportSelection(v, *r.ImportSelection) != nil || v.Root != c.config.SourceRoot || v.Commit != c.config.BaseCommit || r.RepoPath != c.config.SourceRoot {
			return ErrC4Unavailable
		}
		_, profiles, snapshot, _, err := e.s.ResolveMissionModelSelection(r.Spec.ProfileID)
		if err != nil {
			return err
		}
		return c4Profiles(profiles, snapshot)
	}
	if r.Spec.C4 == nil {
		return nil
	}
	if !e.C4SimulationEnabled() || r.Spec.C4.Validate() != nil || r.Spec.C4.Mode != "synthetic" || r.Spec.C4.DockerPath != e.options.C4.dockerPath || r.Spec.C4.Endpoint != e.options.C4.endpoint || r.ImportVersion != 1 || r.ImportSelection == nil || r.CodingVersion != 1 || r.WorkspaceMode != "generations_v1" || r.ToolMode != "" || r.MCPSelection != nil || r.CapabilitySelection != nil || !validC4Acceptance(r.Spec.C4, r.Spec.Acceptance) {
		return ErrC4Unavailable
	}
	_, profiles, snapshot, _, err := e.s.ResolveMissionModelSelection(r.Spec.ProfileID)
	if err != nil {
		return err
	}
	return c4Profiles(profiles, snapshot)
}

func (e *Engine) c4ModelGateway(m domain.Mission) (model.Gateway, error) {
	if isC4(m) {
		if err := e.c4Gate(m); err != nil {
			return nil, err
		}
		if e.options.C4Controlled != nil {
			return e.options.C4Controlled.models, nil
		}
		return e.options.C4.models, nil
	}
	if e.gateway == nil {
		return nil, errors.New("model gateway unavailable")
	}
	return e.gateway, nil
}

func (e *Engine) resolveVerificationImage(ctx context.Context, m domain.Mission) (string, error) {
	if isC4(m) {
		if err := e.c4Gate(m); err != nil {
			return "", err
		}
		if _, err := currentC4Baseline(m); err != nil {
			return "", err
		}
		// This is a pure declared-image lookup, also used by local planning.
		// Execution approval and operation dispatch check live readiness separately.
		return m.Spec.C4.ExecutionIdentity(), nil
	}
	return e.runner.Resolve(ctx, m.Spec.Image)
}

func validC4Acceptance(p *domain.C4Profile, commands []string) bool {
	if p == nil {
		return false
	}
	expected, err := p.AcceptanceCommands()
	return err == nil && reflect.DeepEqual(commands, expected)
}
