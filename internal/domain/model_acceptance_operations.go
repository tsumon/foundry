package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

const AcceptancePrompt = "Write a pure function that adds two integers. Synthetic acceptance data only."
const AcceptanceSystem = "Return a short coding example. No tools, files, repository context, or external actions."
const OpenAISubscriptionRequestedResource = "https://api.openai.com/v1"

// ProductionAdmission freezes provenance, not authority. Only a current,
// constructor-owned controller may admit this generation to the adapter.
type ProductionAdmission struct {
	Generation string `json:"generation"`
	Profile    string `json:"profile"`
}

func (p ProductionAdmission) Valid() bool {
	b, err := hex.DecodeString(p.Generation)
	return err == nil && len(b) == 16 && hex.EncodeToString(b) == p.Generation && (p.Profile == "openai_siwc_subscription_v1" || p.Profile == "openai_siwc_catalog_v1" || p.Profile == "openai_siwc_inference_v1")
}

type ModelAcceptanceProposal struct {
	Phase                   string `json:"phase"`
	ConnectionPurpose       string `json:"connection_purpose,omitempty"`
	RouteID                 string `json:"route_id"`
	ExpectedAccountRevision int64  `json:"expected_account_revision"`
	Model                   string `json:"model,omitempty"`
	PrerequisiteID          string `json:"prerequisite_id,omitempty"`
}
type ModelAcceptanceMaterial struct {
	InferencePrompt     string               `json:"inference_prompt,omitempty"`
	ResponseByteLimit   int                  `json:"response_byte_limit,omitempty"`
	CatalogDigest       string               `json:"catalog_digest,omitempty"`
	RequestMethod       string               `json:"request_method,omitempty"`
	RequestURL          string               `json:"request_url,omitempty"`
	ProductionAdmission *ProductionAdmission `json:"production_admission,omitempty"`
	Phase               string               `json:"phase"`
	ConnectionPurpose   string               `json:"connection_purpose,omitempty"`
	ConnectionMode      string               `json:"connection_mode,omitempty"`
	LoginTimeoutSeconds int                  `json:"login_timeout_seconds,omitempty"`
	RequestedScopes     []string             `json:"requested_scopes,omitempty"`
	RequestedResource   string               `json:"requested_resource,omitempty"`
	Account             ModelAccount         `json:"account"`
	Route               ModelRoute           `json:"route"`
	CredentialVersion   int64                `json:"credential_version"`
	Authorization       *ModelAuthorization  `json:"authorization,omitempty"`
	PrerequisiteID      string               `json:"prerequisite_id,omitempty"`
	ConnectionID        string               `json:"connection_id,omitempty"`
	Model               string               `json:"model,omitempty"`
	PromptDigest        string               `json:"prompt_digest,omitempty"`
	DataSummary         string               `json:"data_summary"`
	CreatedAt           string               `json:"created_at"`
	ExpiresAt           string               `json:"expires_at"`
	DeadlineSeconds     int                  `json:"deadline_seconds"`
	MaxAttempts         int                  `json:"max_attempts"`
}

// Explicit connection policies are narrower than the route's subscription
// billing metadata. Empty permission fields preserve historical material.
func (m ModelAcceptanceMaterial) ValidConnectionPurpose() bool {
	if m.InferencePrompt != "" || m.ResponseByteLimit != 0 || m.CatalogDigest != "" {
		return false
	}
	if m.ProductionAdmission != nil && m.ProductionAdmission.Profile == "openai_siwc_catalog_v1" {
		return false
	}
	if m.RequestMethod != "" || m.RequestURL != "" {
		return false
	}
	production := m.ProductionAdmission != nil
	if production && (!m.ProductionAdmission.Valid() || m.ProductionAdmission.Profile != "openai_siwc_subscription_v1" || m.ConnectionPurpose != "subscription_only" || m.ConnectionMode != "browser_handoff" ||
		!m.Account.Enabled || m.Account.BillingMode != "subscription" || m.Account.BaseURL != OpenAISubscriptionRequestedResource ||
		m.Route.Environment != "live" || m.Route.BaseURL != OpenAISubscriptionRequestedResource || m.Route.Protocol != "responses" || m.Route.SpendClass != "subscription_only") {
		return false
	}
	if m.ConnectionMode == "" {
		if m.LoginTimeoutSeconds != 0 {
			return false
		}
	} else if m.ConnectionMode != "browser_handoff" || m.ConnectionPurpose != "subscription_only" || m.LoginTimeoutSeconds != 600 || m.DeadlineSeconds != 30 || m.MaxAttempts != 1 {
		return false
	}
	if m.ConnectionPurpose == "" {
		return len(m.RequestedScopes) == 0 && m.RequestedResource == ""
	}
	if m.Phase != "connection" || m.Model != "" || m.PrerequisiteID != "" || m.ConnectionID != "" || m.PromptDigest != "" ||
		m.Account.Provider != "openai" || m.Account.AuthMethod != "openai_siwc" ||
		m.Route.AccountID != m.Account.ID || m.Route.Adapter != "openai_siwc_responses" ||
		!production && m.Route.Environment != "simulation" || m.Route.ExtraUsage != "off" {
		return false
	}
	switch m.ConnectionPurpose {
	case "identity_only":
		return m.RequestedResource == "" && reflect.DeepEqual(m.RequestedScopes, []string{"openid", "profile", "email"})
	case "subscription_only":
		return m.RequestedResource == OpenAISubscriptionRequestedResource && reflect.DeepEqual(m.RequestedScopes, []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"})
	default:
		return false
	}
}

const OpenAICatalogURL = "https://api.openai.com/v1/models"

func (m ModelAcceptanceMaterial) ValidAcceptancePurpose() bool {
	return m.ValidProductionInference() || m.ValidProductionCatalog() || m.ValidConnectionPurpose()
}

func (m ModelAcceptanceMaterial) ValidProductionCatalog() bool {
	if m.InferencePrompt != "" || m.ResponseByteLimit != 0 || m.CatalogDigest != "" {
		return false
	}
	return m.ProductionAdmission != nil && m.ProductionAdmission.Valid() && m.ProductionAdmission.Profile == "openai_siwc_catalog_v1" &&
		m.Phase == "catalog" && m.RequestMethod == "GET" && m.RequestURL == OpenAICatalogURL && m.ConnectionPurpose == "" && m.ConnectionMode == "" && m.LoginTimeoutSeconds == 0 && len(m.RequestedScopes) == 0 && m.RequestedResource == "" && m.Model == "" && m.PromptDigest == "" &&
		m.Account.Enabled && m.Account.Provider == "openai" && m.Account.AuthMethod == "openai_siwc" && m.Account.BillingMode == "subscription" && m.Account.BaseURL == OpenAISubscriptionRequestedResource &&
		m.Route.AccountID == m.Account.ID && m.Route.Environment == "live" && m.Route.Adapter == "openai_siwc_responses" && m.Route.Protocol == "responses" && m.Route.BaseURL == OpenAISubscriptionRequestedResource && m.Route.SpendClass == "subscription_only" && m.Route.ExtraUsage == "off" &&
		m.CredentialVersion == 0 && m.Authorization != nil && m.Authorization.State == "available" && m.Authorization.Generation > 0 && m.Authorization.Binding.AccountID == m.Account.ID && m.Authorization.Binding.Issuer == "https://auth.openai.com" && ValidateModelAuthorizationBinding(m.Authorization.Binding) == nil && ModelAuthorizationAllowsInference(m.Authorization.Binding) && m.DeadlineSeconds == 30 && m.MaxAttempts == 1
}

func ValidModelCatalogName(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= 200 && !strings.ContainsAny(s, "<>") && strings.IndexFunc(s, unicode.IsControl) < 0
}

func ValidModelCatalogIdentifier(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.:/", c)) {
			return false
		}
	}
	return true
}

type ModelAcceptanceCatalogEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}
type ModelAcceptanceOperation struct {
	InferenceEvidence
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
	ConnectionDiagnostic  *ConnectionDiagnostic         `json:"connection_diagnostic,omitempty"`
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
