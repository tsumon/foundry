package model

import (
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"net/http"
	"strconv"
	"strings"
)

func providerFailure(code, certainty string) *ProviderError {
	return &ProviderError{Code: code, Certainty: certainty}
}

// Metadata is deliberately bounded. Unknown provider code/param text and all
// message/detail text are discarded instead of reaching logs, evidence or UI.
func safeIdentifier(s string) string {
	if s == "" || len(s) > 128 {
		return ""
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.:/", c)) {
			return ""
		}
	}
	return s
}

func safeProviderCode(code string) string {
	return domain.SafeInferenceProviderCode(code)
}
func safeProviderParam(param string) string {
	return domain.SafeInferenceProviderParam(param)
}

func sanitizedResolverError(err error) error {
	var p *ProviderError
	if !errors.As(err, &p) || p == nil {
		return providerFailure("auth_invalid", "not_sent")
	}
	code := p.Error()
	if code == "model_provider_error" {
		code = "auth_invalid"
	}
	certainty := p.Certainty
	if certainty != "not_sent" && certainty != "rejected" && certainty != "unknown" {
		certainty = "not_sent"
	}
	result := providerFailure(code, certainty)
	result.RequestID = safeIdentifier(p.RequestID)
	result.ProviderCode = safeProviderCode(p.ProviderCode)
	result.ProviderParam = safeProviderParam(p.ProviderParam)
	if p.HTTPStatus >= 100 && p.HTTPStatus <= 599 {
		result.HTTPStatus = p.HTTPStatus
	}
	if p.RetryAfterSeconds > 0 && p.RetryAfterSeconds <= 86400 {
		result.RetryAfterSeconds = p.RetryAfterSeconds
	}
	switch p.BodyShape {
	case "non_json", "json_other", "error_object", "detail":
		result.BodyShape = p.BodyShape
	}
	return result
}

func classifyProviderError(status int, header http.Header, body []byte) *ProviderError {
	p := providerFailure("request_rejected", "rejected")
	p.HTTPStatus = status
	p.RequestID = safeIdentifier(header.Get("X-Request-ID"))
	if p.RequestID == "" {
		p.RequestID = safeIdentifier(header.Get("Request-ID"))
	}
	if seconds, err := strconv.Atoi(header.Get("Retry-After")); err == nil && seconds > 0 && seconds <= 86400 {
		p.RetryAfterSeconds = seconds
	}
	switch {
	case status == 401:
		p.Code = "auth_invalid"
	case status == 403:
		p.Code = "entitlement_denied"
	case status == 429:
		p.Code = "rate_limited"
	case status >= 500:
		p.Code = "server_error"
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil || envelope == nil {
		p.BodyShape = "non_json"
		return p
	}
	p.BodyShape = "json_other"
	if raw, ok := envelope["error"]; ok {
		var e struct {
			Code  string `json:"code"`
			Type  string `json:"type"`
			Param string `json:"param"`
		}
		if json.Unmarshal(raw, &e) == nil && len(raw) > 0 && raw[0] == '{' {
			p.BodyShape = "error_object"
			p.ProviderCode = safeProviderCode(e.Code)
			if p.ProviderCode == "" {
				p.ProviderCode = safeProviderCode(e.Type)
			}
			p.ProviderParam = safeProviderParam(e.Param)
		}
	} else if _, ok := envelope["detail"]; ok {
		p.BodyShape = "detail"
	}
	switch p.ProviderCode {
	case "invalid_api_key", "authentication_error", "subscription_sharing_invalid_user":
		p.Code = "auth_invalid"
	case "permission_error", "subscription_sharing_user_not_eligible", "subscription_sharing_route_not_supported", "chatpass_v2_scope_not_authorized", "chatpass_v2_invalid_authorization_context":
		p.Code = "entitlement_denied"
	case "insufficient_quota", "insufficient_balance", "exceeded_current_quota_error", "usage_limit_reached", "subscription_sharing_usage_limit_exceeded":
		// For SIWC this is the selected route/app allowance, not the whole plan.
		p.Code = "quota_exhausted"
	case "rate_limit_exceeded", "rate_limit_error":
		p.Code = "rate_limited"
	case "overloaded_error", "subscription_sharing_usage_unavailable", "subscription_sharing_user_unavailable":
		p.Code = "server_error"
	case "invalid_request_error", "model_not_found", "subscription_sharing_unsupported_capability":
		p.Code = "request_rejected"
	}
	return p
}
