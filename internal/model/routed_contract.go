package model

import (
	"context"
	"foundry/internal/domain"
)

// CredentialLease is runtime-only. It must never be serialized or persisted.
type CredentialLease struct {
	Secret     string `json:"-"`
	Generation int64  `json:"-"`
}

type CredentialResolver interface {
	ResolveModelCredential(context.Context, domain.RoleModelSelection) (CredentialLease, error)
}

type SelectionPreflighter interface {
	PreflightModelSelection(context.Context, int, domain.RoleModelSelection) error
}

// ProviderError carries bounded, non-secret outcome metadata. Callers must
// populate diagnostic fields from allowlisted protocol values, never raw text.
type ProviderError struct {
	Code              string `json:"code"`
	Certainty         string `json:"certainty"`
	RequestID         string `json:"request_id,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	HTTPStatus        int    `json:"http_status,omitempty"`
	ProviderCode      string `json:"provider_code,omitempty"`
	ProviderParam     string `json:"provider_param,omitempty"`
	BodyShape         string `json:"body_shape,omitempty"`
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "model_provider_error"
	}
	switch e.Code {
	case "auth_invalid", "entitlement_denied", "quota_exhausted", "rate_limited", "request_rejected", "server_error", "transport_unknown", "response_incomplete", "live_provider_access_pending_confirmation":
		return e.Code
	default:
		return "model_provider_error"
	}
}
