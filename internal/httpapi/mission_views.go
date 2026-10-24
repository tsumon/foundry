package httpapi

import (
	"foundry/internal/domain"
	"foundry/internal/engine"
	"net/http"
)

type displayProfile struct {
	RouteID           string `json:"route_id,omitempty"`
	AccountID         string `json:"account_id,omitempty"`
	AccountRevision   int64  `json:"account_revision,omitempty"`
	CredentialVersion int64  `json:"credential_version,omitempty"`
	ID                string `json:"id"`
	Name              string `json:"name"`
	Protocol          string `json:"protocol"`
	Model             string `json:"model"`
}

func displayModelProfile(p domain.Profile) displayProfile {
	return displayProfile{ID: p.ID, Name: p.Name, Protocol: p.Protocol, Model: p.Model, RouteID: p.RouteID, AccountID: p.AccountID, AccountRevision: p.AccountRevision, CredentialVersion: p.CredentialVersion}
}

type missionMCPSelection struct {
	ConnectionID       string                  `json:"connection_id"`
	ConnectionRevision int64                   `json:"connection_revision"`
	Endpoint           string                  `json:"endpoint"`
	Protocol           string                  `json:"protocol"`
	SnapshotID         string                  `json:"snapshot_id"`
	SnapshotDigest     string                  `json:"snapshot_digest"`
	Tools              []domain.MCPToolBinding `json:"tools"`
	Budget             domain.MCPBudget        `json:"budget"`
	TestOnly           bool                    `json:"test_only"`
}

// Strip internal authority before embedding the compatible mission fields.
// Profiles are display-only; explicit settings remain the profile-edit surface.
type SafeMissionView struct {
	domain.Mission
	ModelContinuations        []modelContinuationView           `json:"model_continuations,omitempty"`
	ModelContinuationStatus   string                            `json:"model_continuation_status,omitempty"`
	EffectiveModelSnapshot    *safeMissionModelSnapshot         `json:"effective_model_snapshot,omitempty"`
	C4ProfileBinding          *c4ProfileBinding                 `json:"c4_profile_binding,omitempty"`
	ModelSnapshot             *safeMissionModelSnapshot         `json:"model_snapshot,omitempty"`
	ModelSnapshotStatus       string                            `json:"model_snapshot_status,omitempty"`
	ModelPolicySnapshot       *domain.MissionRolePolicySnapshot `json:"model_policy_snapshot,omitempty"`
	ModelPolicySnapshotStatus string                            `json:"model_policy_snapshot_status,omitempty"`
	Profiles                  map[domain.Role]displayProfile    `json:"profiles"`
	MCPSelection              *missionMCPSelection              `json:"mcp_selection,omitempty"`
	ResearchStatus            *engine.ResearchStatus            `json:"research_status,omitempty"`
	ModelRuntime              *modelRuntimeView                 `json:"model_runtime,omitempty"`
}

// Response-only display binding; never persisted, accepted as consent, or used
// by action gates. The detached profile ties its canonical digest to this view.
type c4ProfileBinding struct {
	Revision int64            `json:"revision"`
	Profile  domain.C4Profile `json:"profile"`
	Digest   string           `json:"digest"`
}

func missionC4ProfileBinding(m domain.Mission) *c4ProfileBinding {
	if m.Revision < 1 || m.Spec.C4 == nil || m.Spec.C4.Mode != "embedded_vm" || m.Spec.C4.Validate() != nil {
		return nil
	}
	p := *m.Spec.C4
	identity := *p.EmbeddedVM
	p.EmbeddedVM = &identity
	return &c4ProfileBinding{Revision: m.Revision, Profile: p, Digest: p.ProfileDigest()}
}

func (s *server) missionView(m domain.Mission) (any, error) {
	binding := missionC4ProfileBinding(m)
	v2 := domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode)
	_, accountBound, _ := domain.BaseWorkspaceMode(m.WorkspaceMode)
	accountData := accountBound || m.ModelSnapshot != nil || m.ModelSnapshotInvalid || len(m.ModelContinuations) > 0 || m.ModelContinuationsInvalid
	policyData := m.ModelPolicySnapshot != nil || m.ModelPolicySnapshotInvalid
	for _, p := range m.Profiles {
		accountData = accountData || p.AccountID != "" || p.AccountRevision != 0 || p.CredentialVersion != 0
	}
	if !v2 && m.MCPBindings == nil && !accountData && !policyData {
		if binding != nil {
			return struct {
				domain.Mission
				C4ProfileBinding *c4ProfileBinding `json:"c4_profile_binding"`
			}{m, binding}, nil
		}
		return m, nil
	}
	view := SafeMissionView{Mission: m, C4ProfileBinding: binding, Profiles: map[domain.Role]displayProfile{}}
	view.Mission.MCPBindings = nil
	view.Mission.ModelSnapshot = nil
	view.Mission.ModelContinuations = nil
	if len(m.ModelContinuations) > 0 || m.ModelContinuationsInvalid {
		view.ModelContinuations = modelContinuationViews(m)
		view.ModelContinuationStatus = "valid"
		effective, err := domain.EffectiveModelSnapshot(m)
		if err != nil {
			view.ModelContinuationStatus = "invalid_or_unsupported"
		} else {
			view.EffectiveModelSnapshot = modelSnapshotView(effective)
		}
	}
	view.Mission.ModelPolicySnapshot = nil
	view.ModelSnapshot = modelSnapshotView(m.ModelSnapshot)
	if domain.IsInferenceSnapshot(m) {
		runtime := s.modelRuntimeView()
		view.ModelRuntime = &runtime
	}
	if accountData {
		view.ModelSnapshotStatus = "valid"
		if domain.ValidateMissionModelSnapshot(m) != nil {
			view.ModelSnapshotStatus = "invalid_or_unsupported"
		}
	}
	if policyData {
		view.ModelPolicySnapshotStatus = "valid"
		if domain.ValidateMissionRolePolicySnapshot(m) != nil {
			view.ModelPolicySnapshotStatus = "invalid_or_unsupported"
		} else {
			view.ModelPolicySnapshot = m.ModelPolicySnapshot
		}
	}
	for role, p := range m.Profiles {
		view.Profiles[role] = displayModelProfile(p)
	}
	if b := m.MCPBindings; b != nil {
		view.MCPSelection = &missionMCPSelection{b.ConnectionID, b.ConnectionRevision, b.Endpoint, b.Protocol, b.SnapshotID, b.SnapshotDigest, b.Tools, b.Budget, v2}
	}
	if !v2 || view.ModelSnapshotStatus == "invalid_or_unsupported" || view.ModelPolicySnapshotStatus == "invalid_or_unsupported" {
		return view, nil
	}
	status, err := s.engine.ResearchMCPStatus(m.ID, m.Revision)
	if err != nil {
		return nil, err
	}
	view.ResearchStatus = &status
	return view, nil
}

func (s *server) writeMission(w http.ResponseWriter, m domain.Mission, extra map[string]any) {
	view, err := s.missionView(m)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if extra == nil {
		extra = map[string]any{}
	}
	extra["mission"] = view
	writeJSON(w, http.StatusOK, extra)
}
