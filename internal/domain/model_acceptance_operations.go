package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const AcceptancePrompt = "Write a pure function that adds two integers. Synthetic acceptance data only."
const AcceptanceSystem = "Return a short coding example. No tools, files, repository context, or external actions."

type ModelAcceptanceProposal struct {
	Phase                   string `json:"phase"`
	ConnectionPurpose       string `json:"connection_purpose,omitempty"`
	RouteID                 string `json:"route_id"`
	ExpectedAccountRevision int64  `json:"expected_account_revision"`
	Model                   string `json:"model,omitempty"`
	PrerequisiteID          string `json:"prerequisite_id,omitempty"`
}
type ModelAcceptanceMaterial struct {
	Phase             string              `json:"phase"`
	ConnectionPurpose string              `json:"connection_purpose,omitempty"`
	RequestedScopes   []string            `json:"requested_scopes,omitempty"`
	Account           ModelAccount        `json:"account"`
	Route             ModelRoute          `json:"route"`
	CredentialVersion int64               `json:"credential_version"`
	Authorization     *ModelAuthorization `json:"authorization,omitempty"`
	PrerequisiteID    string              `json:"prerequisite_id,omitempty"`
	ConnectionID      string              `json:"connection_id,omitempty"`
	Model             string              `json:"model,omitempty"`
	PromptDigest      string              `json:"prompt_digest,omitempty"`
	DataSummary       string              `json:"data_summary"`
	CreatedAt         string              `json:"created_at"`
	ExpiresAt         string              `json:"expires_at"`
	DeadlineSeconds   int                 `json:"deadline_seconds"`
	MaxAttempts       int                 `json:"max_attempts"`
}

// Identity policy is narrower than the route's subscription billing metadata.
// Empty purpose/scopes preserve historical simulation material and digests.
func (m ModelAcceptanceMaterial) ValidConnectionPurpose() bool {
	if m.ConnectionPurpose == "" {
		return len(m.RequestedScopes) == 0
	}
	return m.ConnectionPurpose == "identity_only" && m.Phase == "connection" &&
		m.Model == "" && m.PrerequisiteID == "" && m.ConnectionID == "" && m.PromptDigest == "" &&
		m.Account.Provider == "openai" && m.Account.AuthMethod == "openai_siwc" &&
		m.Route.AccountID == m.Account.ID && m.Route.Adapter == "openai_siwc_responses" &&
		m.Route.Environment == "simulation" && m.Route.ExtraUsage == "off" &&
		reflect.DeepEqual(m.RequestedScopes, []string{"openid", "profile", "email"})
}

type ModelAcceptanceCatalogEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}
type ModelAcceptanceOperation struct {
	ID                    string                        `json:"id"`
	AccountID             string                        `json:"account_id"`
	Revision              int64                         `json:"revision"`
	Phase                 string                        `json:"phase"`
	State                 string                        `json:"state"`
	Material              ModelAcceptanceMaterial       `json:"material"`
	MaterialDigest        string                        `json:"material_digest"`
	Attempts              int                           `json:"attempts"`
	ClaimSequence         int64                         `json:"claim_sequence"`
	Source                string                        `json:"source"`
	Certainty             string                        `json:"certainty"`
	Code                  string                        `json:"code,omitempty"`
	StartedAt             string                        `json:"started_at,omitempty"`
	FinishedAt            string                        `json:"finished_at,omitempty"`
	ResultAccountRevision int64                         `json:"result_account_revision,omitempty"`
	ResultSnapshotDigest  string                        `json:"result_snapshot_digest,omitempty"`
	Catalog               []ModelAcceptanceCatalogEntry `json:"catalog"`
	InputTokens           int64                         `json:"input_tokens,omitempty"`
	OutputTokens          int64                         `json:"output_tokens,omitempty"`
	UsageKnown            bool                          `json:"usage_known"`
	Quota                 string                        `json:"quota"`
	RemoteBilling         string                        `json:"remote_billing"`
	LiveAccess            string                        `json:"live_access"`
}

func AcceptanceDigest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (m ModelAcceptanceMaterial) Selection() RoleModelSelection {
	p := Profile{ID: "acceptance-only", AccountID: m.Account.ID, AccountRevision: m.Account.Revision, CredentialVersion: m.CredentialVersion, RouteID: m.Route.ID, BaseURL: m.Account.BaseURL, Protocol: m.Route.Protocol, Model: m.Model}
	if p.Model == "" {
		p.Model = "catalog-discovery"
	}
	s := RoleModelSelection{Profile: p, Account: &m.Account, Route: &m.Route}
	if m.Authorization != nil {
		s.Authorization = &m.Authorization.Binding
	}
	return s
}
