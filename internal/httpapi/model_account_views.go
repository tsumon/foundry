package httpapi

import "foundry/internal/domain"

type safeModelAccount struct {
	ID                       string `json:"id"`
	Revision                 int64  `json:"revision"`
	Name                     string `json:"name"`
	Provider                 string `json:"provider"`
	BaseURL                  string `json:"base_url"`
	AuthMethod               string `json:"auth_method"`
	BillingMode              string `json:"billing_mode"`
	Enabled                  bool   `json:"enabled"`
	CurrentCredentialVersion int64  `json:"current_credential_version"`
	CreatedAt                string `json:"created_at"`
	UpdatedAt                string `json:"updated_at"`
	IdentityStatus           string `json:"identity_status"`
	UsageStatus              string `json:"usage_status"`
	InferenceEnabled         bool   `json:"inference_enabled"`
}

func accountView(a domain.ModelAccount) safeModelAccount {
	return safeModelAccount{ID: a.ID, Revision: a.Revision, Name: a.Name, Provider: a.Provider, BaseURL: a.BaseURL, AuthMethod: a.AuthMethod, BillingMode: a.BillingMode, Enabled: a.Enabled, CurrentCredentialVersion: a.CurrentCredentialVersion, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, IdentityStatus: "unverified", UsageStatus: "unknown", InferenceEnabled: false}
}

// Credential DTOs deliberately enumerate safe fields rather than embedding
// storage records, so future storage fields cannot silently become public.
type safeModelCredential struct {
	AccountID      string `json:"account_id"`
	Version        int64  `json:"version"`
	OperationID    string `json:"operation_id"`
	State          string `json:"state"`
	CleanupPending bool   `json:"cleanup_pending"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

func credentialView(v domain.ModelCredentialVersion) safeModelCredential {
	return safeModelCredential{v.AccountID, v.Version, v.OperationID, v.State, v.CleanupPending, v.CreatedAt, v.UpdatedAt}
}

type safeAccountProfile struct {
	domain.Profile
	Account *safeModelAccount `json:"account,omitempty"`
}

func (s *server) profileView(p domain.Profile) (safeAccountProfile, error) {
	view := safeAccountProfile{Profile: p}
	if p.AccountID != "" {
		a, err := s.store.GetModelAccount(p.AccountID)
		if err != nil {
			return view, err
		}
		safe := accountView(a)
		view.Account = &safe
	}
	return view, nil
}

type safeRoleModelSelection struct {
	Profile       displayProfile                 `json:"profile"`
	Account       *displayModelAccount           `json:"account,omitempty"`
	Route         *safeModelRoute                `json:"route,omitempty"`
	Authorization *safeModelAuthorizationBinding `json:"authorization,omitempty"`
}

type safeModelRoute struct {
	ID          string `json:"id"`
	AccountID   string `json:"account_id"`
	Adapter     string `json:"adapter"`
	Protocol    string `json:"protocol"`
	BaseURL     string `json:"base_url"`
	Environment string `json:"environment"`
	SpendClass  string `json:"spend_class"`
	ExtraUsage  string `json:"extra_usage"`
	CreatedAt   string `json:"created_at"`
}

type safeModelAuthorizationBinding struct {
	ID         string   `json:"id"`
	AccountID  string   `json:"account_id"`
	Issuer     string   `json:"issuer"`
	Subject    string   `json:"subject"`
	ClientID   string   `json:"client_id"`
	SpendClass string   `json:"spend_class"`
	Scopes     []string `json:"scopes"`
}
type displayModelAccount struct {
	ID               string `json:"id"`
	Revision         int64  `json:"revision"`
	Name             string `json:"name"`
	Provider         string `json:"provider"`
	BillingMode      string `json:"billing_mode"`
	Enabled          bool   `json:"enabled"`
	IdentityStatus   string `json:"identity_status"`
	UsageStatus      string `json:"usage_status"`
	InferenceEnabled bool   `json:"inference_enabled"`
}
type safeMissionModelSnapshot struct {
	SchemaVersion int                                    `json:"schema_version"`
	Roles         map[domain.Role]safeRoleModelSelection `json:"roles"`
	Digest        string                                 `json:"digest"`
}

func modelSnapshotView(snapshot *domain.MissionModelSnapshot) *safeMissionModelSnapshot {
	if snapshot == nil {
		return nil
	}
	view := &safeMissionModelSnapshot{SchemaVersion: snapshot.SchemaVersion, Roles: map[domain.Role]safeRoleModelSelection{}, Digest: snapshot.Digest}
	for role, selection := range snapshot.Roles {
		v := safeRoleModelSelection{Profile: displayModelProfile(selection.Profile)}
		if a := selection.Account; a != nil {
			v.Account = &displayModelAccount{ID: a.ID, Revision: a.Revision, Name: a.Name, Provider: a.Provider, BillingMode: a.BillingMode, Enabled: a.Enabled, IdentityStatus: "unverified", UsageStatus: "unknown", InferenceEnabled: false}
		}
		if route := selection.Route; route != nil {
			v.Route = &safeModelRoute{ID: route.ID, AccountID: route.AccountID, Adapter: route.Adapter, Protocol: route.Protocol, BaseURL: route.BaseURL, Environment: route.Environment, SpendClass: route.SpendClass, ExtraUsage: route.ExtraUsage, CreatedAt: route.CreatedAt}
		}
		if authorization := selection.Authorization; authorization != nil {
			v.Authorization = &safeModelAuthorizationBinding{ID: authorization.ID, AccountID: authorization.AccountID, Issuer: authorization.Issuer, Subject: authorization.Subject, ClientID: authorization.ClientID, SpendClass: authorization.SpendClass, Scopes: append([]string(nil), authorization.Scopes...)}
		}
		view.Roles[role] = v
	}
	return view
}
