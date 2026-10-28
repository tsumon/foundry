package domain

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// ModelRoute is immutable, non-secret metadata. Simulation routes still need
// a constructor-pinned origin check at the transport before any secret access.
type ModelRoute struct {
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

// ModelAuthorizationBinding freezes an identity, never a token generation.
type ModelAuthorizationBinding struct {
	ID         string   `json:"id"`
	AccountID  string   `json:"account_id"`
	Issuer     string   `json:"issuer"`
	Subject    string   `json:"subject"`
	ClientID   string   `json:"client_id"`
	SpendClass string   `json:"spend_class"`
	Scopes     []string `json:"scopes"`
}

var ErrInvalidModelRoute = errors.New("invalid_model_route")

func ValidateModelRoute(r ModelRoute, a ModelAccount) error {
	if r.ID == "" || len(r.ID) > 256 || r.AccountID == "" || r.AccountID != a.ID || r.BaseURL != a.BaseURL || ValidateModelAccount(a) != nil || (r.ExtraUsage != "off" && r.ExtraUsage != "unknown") {
		return ErrInvalidModelRoute
	}
	provider, auth, protocol, spend := "", "api_key", "chat_completions", "explicit_api"
	var endpoints []string
	switch r.Adapter {
	case "compatible_api_responses":
		provider, protocol = "custom", "responses"
	case "openai_api_chat", "openai_api_responses", "openai_siwc_responses":
		provider, endpoints = "openai", []string{"https://api.openai.com/v1"}
		if r.Adapter != "openai_api_chat" {
			protocol = "responses"
		}
		if r.Adapter == "openai_siwc_responses" {
			auth, spend = "openai_siwc", "subscription_only"
		}
	case "anthropic_api_messages":
		provider, protocol, endpoints = "anthropic", "messages", []string{"https://api.anthropic.com/v1"}
	case "zhipu_api_chat":
		provider, endpoints = "zhipu", []string{"https://open.bigmodel.cn/api/paas/v4"}
	case "kimi_api_chat":
		provider, endpoints = "kimi", []string{"https://api.moonshot.cn/v1", "https://api.moonshot.ai/v1"}
	case "kimi_code_chat":
		provider, spend, endpoints = "kimi", "interactive_package", []string{"https://api.kimi.com/coding/v1"}
	default:
		return ErrInvalidModelRoute
	}
	if a.Provider != provider || a.AuthMethod != auth || r.Protocol != protocol || r.SpendClass != spend {
		return ErrInvalidModelRoute
	}
	if spend == "explicit_api" {
		if a.BillingMode != "api_credits" && a.BillingMode != "metered_api" {
			return ErrInvalidModelRoute
		}
	} else if a.BillingMode != "subscription" {
		return ErrInvalidModelRoute
	}
	if r.Environment == "live" {
		if r.Adapter == "compatible_api_responses" && compatibleAPIEndpoint(r.BaseURL) {
			return nil
		}
		for _, endpoint := range endpoints {
			if r.BaseURL == endpoint {
				return nil
			}
		}
		return ErrInvalidModelRoute
	}
	if r.Environment != "simulation" {
		return ErrInvalidModelRoute
	}
	u, err := url.Parse(r.BaseURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || strings.ContainsAny(u.Path, "\\") {
		return ErrInvalidModelRoute
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || u.Host != "127.0.0.1:"+strconv.Itoa(port) {
		return ErrInvalidModelRoute
	}
	return nil
}

// A compatible route is an explicitly frozen receiver, not an exception to
// official-provider endpoint checks. No credentials, query or ambiguous paths.
func compatibleAPIEndpoint(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || strings.ContainsAny(base, "\\\r\n\t ") || strings.HasSuffix(u.Path, "/") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(u.Path, "/"), "/") {
		if segment == "." || segment == ".." || segment == "" && u.Path != "" {
			return false
		}
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != u.Port() {
			return false
		}
	}
	return u.Scheme == "https" || u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.Port() != ""
}
