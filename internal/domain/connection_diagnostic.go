package domain

// ConnectionDiagnostic is bounded, output-only evidence attached to a new
// terminal connection receipt. It is never part of approval material.
type ConnectionDiagnostic struct {
	Version          int    `json:"version"`
	Stage            string `json:"stage"`
	Code             string `json:"code"`
	HTTPStatus       int    `json:"http_status,omitempty"`
	ProviderCode     string `json:"provider_code,omitempty"`
	CredentialCommit string `json:"credential_commit"`
	Cleanup          string `json:"cleanup"`
}

func (d ConnectionDiagnostic) Valid() bool {
	if d.Version != 1 || !connectionDiagnosticStage(d.Stage) || !connectionDiagnosticCode(d.Code) ||
		(d.HTTPStatus != 0 && (d.HTTPStatus < 400 || d.HTTPStatus > 599)) ||
		!connectionDiagnosticProviderCode(d.ProviderCode) ||
		(d.ProviderCode != "" && d.HTTPStatus == 0) {
		return false
	}
	switch d.CredentialCommit {
	case "not_committed", "committed", "unknown":
	default:
		return false
	}
	switch d.Cleanup {
	case "not_required", "completed", "pending", "unknown":
	default:
		return false
	}
	// A successful commit may be followed by a later failure and any observed
	// cleanup outcome. Conversely, pending cleanup means a partial write may
	// exist, so the credential outcome cannot truthfully be "not committed".
	return d.CredentialCommit != "not_committed" || d.Cleanup != "pending"
}

func connectionDiagnosticStage(stage string) bool {
	switch stage {
	case "callback_validation", "browser_authorization", "authorization_intent", "token_exchange", "grant_validation", "oidc_discovery", "identity_validation", "credential_commit", "session":
		return true
	default:
		return false
	}
}

func connectionDiagnosticCode(code string) bool {
	switch code {
	case "auth_invalid", "entitlement_denied", "quota_exhausted", "rate_limited", "request_rejected", "server_error", "transport_unknown", "response_incomplete", "canceled", "expired", "storage_error":
		return true
	default:
		return false
	}
}

func connectionDiagnosticProviderCode(code string) bool {
	switch code {
	case "", "invalid_client", "invalid_grant", "subscription_sharing_user_not_eligible", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
		return true
	default:
		return false
	}
}

func CloneConnectionDiagnostic(d *ConnectionDiagnostic) *ConnectionDiagnostic {
	if d == nil {
		return nil
	}
	copy := *d
	return &copy
}
