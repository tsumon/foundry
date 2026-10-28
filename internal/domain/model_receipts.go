package domain

import "strings"

// SanitizeModelCallReceipt removes unknown metadata rather than treating any
// provider text as a trusted machine code. Success means completed, not approved.
func SanitizeModelCallReceipt(r ModelCallReceipt) ModelCallReceipt {
	if !r.ExchangeDiagnostic.Valid() {
		r.ExchangeDiagnostic = HTTPExchangeDiagnostic{}
	}
	if !r.ResponseDiagnostic.Valid() {
		r.ResponseDiagnostic = ResponseDiagnostic{}
	}
	identifier := func(s string) string {
		if len(s) > 128 {
			return ""
		}
		for _, c := range s {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.:/", c)) {
				return ""
			}
		}
		return s
	}
	r.RequestID = identifier(r.RequestID)
	r.ResolvedModel = identifier(r.ResolvedModel)
	if r.CredentialGeneration < 0 {
		r.CredentialGeneration = 0
	}
	if r.HTTPStatus < 100 || r.HTTPStatus > 599 {
		r.HTTPStatus = 0
	}
	if r.RetryAfterSeconds < 0 || r.RetryAfterSeconds > 86400 {
		r.RetryAfterSeconds = 0
	}
	switch r.ProviderCode {
	case "invalid_api_key", "authentication_error", "permission_error", "insufficient_quota", "rate_limit_exceeded", "rate_limit_error", "overloaded_error", "invalid_request_error", "model_not_found", "insufficient_balance", "exceeded_current_quota_error", "usage_limit_reached", "subscription_sharing_user_not_eligible", "subscription_sharing_usage_limit_exceeded", "subscription_sharing_usage_unavailable", "subscription_sharing_unsupported_capability", "subscription_sharing_route_not_supported", "subscription_sharing_invalid_user", "chatpass_v2_scope_not_authorized", "chatpass_v2_invalid_authorization_context", "subscription_sharing_user_unavailable":
	default:
		r.ProviderCode = ""
	}
	switch r.ProviderParam {
	case "model", "input", "tools", "tool_choice", "temperature", "max_output_tokens", "max_tokens", "service_tier", "stream", "store", "instructions":
	default:
		r.ProviderParam = ""
	}
	switch r.BodyShape {
	case "non_json", "json_other", "error_object", "detail":
	default:
		r.BodyShape = ""
	}
	switch r.Certainty {
	case "completed", "not_sent", "rejected", "unknown":
	default:
		r.Certainty = "unknown"
	}
	return r
}
