package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

var ErrInvalidModelSnapshot = errors.New("invalid_mission_model_snapshot")

type RoleModelSelection struct {
	Catalog       *ModelCatalogProvenance    `json:"catalog,omitempty"`
	Profile       Profile                    `json:"profile"`
	Account       *ModelAccount              `json:"account,omitempty"`
	Route         *ModelRoute                `json:"route,omitempty"`
	Authorization *ModelAuthorizationBinding `json:"authorization,omitempty"`
}

// Catalog evidence is provenance, never permission to invoke a model. The
// generation and expiry pin the selected authorization rather than refreshing
// it behind an existing task's frozen identity.
type ModelCatalogProvenance struct {
	OperationID             string `json:"operation_id"`
	ReceiptDigest           string `json:"receipt_digest"`
	AuthorizationGeneration int64  `json:"authorization_generation"`
	ExpiresAt               string `json:"expires_at"`
}
type MissionModelSnapshot struct {
	SchemaVersion int                         `json:"schema_version"`
	Roles         map[Role]RoleModelSelection `json:"roles"`
	Digest        string                      `json:"digest"`
}

func BaseWorkspaceMode(mode string) (base string, accountBound bool, err error) {
	switch mode {
	case "", WorkspaceModeGenerationsV1, WorkspaceModeControlledReadV1, WorkspaceModeControlledMCPReadV1, WorkspaceModeControlledMCPReadV2:
		return mode, false, nil
	case "account_snapshot_v1", "account_snapshot_v2":
		return "", true, nil
	case "generations_account_snapshot_v1", "generations_account_snapshot_v2":
		return WorkspaceModeGenerationsV1, true, nil
	case "generations_controlled_read_account_snapshot_v1", "generations_controlled_read_account_snapshot_v2":
		return WorkspaceModeControlledReadV1, true, nil
	case "generations_controlled_mcp_read_account_snapshot_v1", "generations_controlled_mcp_read_account_snapshot_v2":
		return WorkspaceModeControlledMCPReadV2, true, nil
	default:
		return "", false, errors.New("unsupported workspace mode; execution disabled")
	}
}
func InferenceWorkspaceMode(base string) (string, error) {
	switch base {
	case "":
		return "account_snapshot_v2", nil
	case WorkspaceModeGenerationsV1:
		return "generations_account_snapshot_v2", nil
	case WorkspaceModeControlledReadV1:
		return "generations_controlled_read_account_snapshot_v2", nil
	case WorkspaceModeControlledMCPReadV2:
		return "generations_controlled_mcp_read_account_snapshot_v2", nil
	default:
		return "", ErrInvalidModelSnapshot
	}
}

func IsInferenceSnapshot(m Mission) bool {
	return m.ModelSnapshot != nil && m.ModelSnapshot.SchemaVersion == 2 && ValidateMissionModelSnapshot(m) == nil
}
func AccountWorkspaceMode(base string) (string, error) {
	switch base {
	case "":
		return "account_snapshot_v1", nil
	case WorkspaceModeGenerationsV1:
		return "generations_account_snapshot_v1", nil
	case WorkspaceModeControlledReadV1:
		return "generations_controlled_read_account_snapshot_v1", nil
	case WorkspaceModeControlledMCPReadV2:
		return "generations_controlled_mcp_read_account_snapshot_v1", nil
	default:
		return "", ErrInvalidModelSnapshot
	}
}
func ModelSnapshotDigest(snapshot MissionModelSnapshot) (string, error) {
	// Exclude Digest entirely, not merely a caller-controlled empty string.
	raw, err := json.Marshal(struct {
		SchemaVersion int                         `json:"schema_version"`
		Roles         map[Role]RoleModelSelection `json:"roles"`
	}{snapshot.SchemaVersion, snapshot.Roles})
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}

func ValidateMissionModelSnapshot(m Mission) error {
	if err := ValidateMissionRolePolicySnapshot(m); err != nil {
		return ErrInvalidModelSnapshot
	}
	if m.ModelSnapshotInvalid {
		return ErrInvalidModelSnapshot
	}
	base, bound, err := BaseWorkspaceMode(m.WorkspaceMode)
	if err != nil {
		return err
	}
	wantTool := ""
	switch base {
	case WorkspaceModeControlledReadV1:
		wantTool = ToolModeControlledReadV1
	case WorkspaceModeControlledMCPReadV1:
		wantTool = ToolModeControlledMCPReadV1
	case WorkspaceModeControlledMCPReadV2:
		wantTool = ToolModeControlledMCPReadV2
	}
	if m.ToolMode != wantTool {
		return ErrInvalidModelSnapshot
	}
	if !bound {
		if m.ModelSnapshot != nil {
			return ErrInvalidModelSnapshot
		}
		for _, p := range m.Profiles {
			if p.AccountID != "" || p.AccountRevision != 0 || p.CredentialVersion != 0 || p.RouteID != "" {
				return ErrInvalidModelSnapshot
			}
		}
		return nil
	}
	s := m.ModelSnapshot
	wantSchema := 1
	if mode, _ := InferenceWorkspaceMode(base); mode == m.WorkspaceMode {
		wantSchema = 2
	}
	if s == nil || s.SchemaVersion != wantSchema || len(s.Roles) != 7 || len(m.Profiles) != 7 {
		return ErrInvalidModelSnapshot
	}
	accountFound := false
	for _, role := range []Role{Main, Researcher, Planner, Worker, Debugger, QA, Auditor} {
		selection, ok := s.Roles[role]
		p, present := m.Profiles[role]
		if !ok || !present || selection.Profile != p || p.ID == "" || p.Model == "" || (p.Protocol != "chat_completions" && p.Protocol != "responses" && !(wantSchema == 2 && p.AccountID != "" && p.Protocol == "messages")) {
			return ErrInvalidModelSnapshot
		}
		if _, _, err := ParseProviderBaseURL(p.BaseURL); err != nil {
			return ErrInvalidModelSnapshot
		}
		if p.CatalogOperationID != "" || selection.Catalog != nil {
			c := selection.Catalog
			if wantSchema != 2 || c == nil || c.OperationID != p.CatalogOperationID || ValidateModelCredentialOperation(c.OperationID) != nil || len(c.ReceiptDigest) != 64 || c.AuthorizationGeneration < 1 || c.ExpiresAt == "" || selection.Account == nil || selection.Account.AuthMethod != "openai_siwc" || selection.Route == nil || selection.Route.Environment != "live" || selection.Authorization == nil {
				return ErrInvalidModelSnapshot
			}
		}
		if wantSchema == 1 && (p.RouteID != "" || selection.Route != nil || selection.Authorization != nil) {
			return ErrInvalidModelSnapshot
		}
		if p.AccountID == "" {
			if selection.Account != nil || p.AccountRevision != 0 || p.CredentialVersion != 0 || p.RouteID != "" || selection.Route != nil || selection.Authorization != nil {
				return ErrInvalidModelSnapshot
			}
			continue
		}
		accountFound = true
		a := selection.Account
		if a == nil || ValidateModelAccount(*a) != nil || a.ID != p.AccountID || a.Revision != p.AccountRevision || a.Revision < 1 || p.CredentialVersion < 0 || a.CurrentCredentialVersion != p.CredentialVersion || a.BaseURL != p.BaseURL || p.HasKey {
			return ErrInvalidModelSnapshot
		}
		if wantSchema == 1 && a.AuthMethod != "api_key" {
			return ErrInvalidModelSnapshot
		}
		if wantSchema == 2 {
			r := selection.Route
			if r == nil || p.RouteID != r.ID || r.Protocol != p.Protocol || ValidateModelRoute(*r, *a) != nil {
				return ErrInvalidModelSnapshot
			}
			binding := selection.Authorization
			if a.AuthMethod == "api_key" && binding != nil {
				return ErrInvalidModelSnapshot
			}
			if a.AuthMethod == "openai_siwc" {
				if p.CredentialVersion != 0 {
					return ErrInvalidModelSnapshot
				}
				if binding != nil && (binding.ID == "" || binding.AccountID != a.ID || binding.Issuer == "" || binding.Subject == "" || binding.ClientID == "" || binding.SpendClass != r.SpendClass || len(binding.Scopes) == 0) {
					return ErrInvalidModelSnapshot
				}
			}
		}
	}
	if !accountFound {
		return ErrInvalidModelSnapshot
	}
	digest, err := ModelSnapshotDigest(*s)
	if err != nil || digest != s.Digest {
		return ErrInvalidModelSnapshot
	}
	return nil
}
