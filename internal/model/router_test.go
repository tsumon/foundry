package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"foundry/internal/domain"
)

type leaseResolver struct {
	calls int
	err   error
}

func (r *leaseResolver) ResolveModelCredential(_ context.Context, s domain.RoleModelSelection) (CredentialLease, error) {
	r.calls++
	return CredentialLease{Secret: "fake-test-secret", Generation: 7}, r.err
}

func routedSelection(base, adapter string) domain.RoleModelSelection {
	a := domain.ModelAccount{ID: "a", Revision: 1, Name: "test", Provider: "openai", AuthMethod: "api_key", BillingMode: "metered_api", Enabled: true, BaseURL: base, CurrentCredentialVersion: 2}
	r := domain.ModelRoute{ID: "r", AccountID: "a", Adapter: adapter, Protocol: "chat_completions", BaseURL: base, Environment: "simulation", SpendClass: "explicit_api", ExtraUsage: "off"}
	switch adapter {
	case "openai_api_responses":
		r.Protocol = "responses"
	case "anthropic_api_messages":
		a.Provider = "anthropic"
		r.Protocol = "messages"
	case "zhipu_api_chat":
		a.Provider = "zhipu"
	case "kimi_api_chat":
		a.Provider = "kimi"
	case "kimi_code_chat":
		a.Provider = "kimi"
		a.BillingMode = "subscription"
		r.SpendClass = "interactive_package"
	case "openai_siwc_responses":
		a.AuthMethod = "openai_siwc"
		a.BillingMode = "subscription"
		a.CurrentCredentialVersion = 0
		r.Protocol = "responses"
		r.SpendClass = "subscription_only"
	}
	p := domain.Profile{ID: "p", Name: "test", Kind: "direct", AccountID: "a", AccountRevision: 1, CredentialVersion: a.CurrentCredentialVersion, RouteID: "r", Protocol: r.Protocol, BaseURL: base, Model: "test-model"}
	s := domain.RoleModelSelection{Profile: p, Account: &a, Route: &r}
	if adapter == "openai_siwc_responses" {
		s.Authorization = &domain.ModelAuthorizationBinding{ID: "auth", AccountID: "a", Issuer: "https://auth.openai.com", Subject: "subject", ClientID: "issued", SpendClass: "subscription_only", Scopes: []string{"resource.invoke", "chatgpt.tokens.use.direct"}}
	}
	return s
}
func routedRequest(s domain.RoleModelSelection) Request {
	return Request{SnapshotSchema: 2, Selection: &s, Profile: s.Profile, Role: domain.Planner, System: "instructions", Prompt: "hello", MaxOutputTokens: 100}
}
func requireProviderError(t *testing.T, err error, code, certainty string) *ProviderError {
	t.Helper()
	var p *ProviderError
	if !errors.As(err, &p) || p.Code != code || p.Certainty != certainty {
		t.Fatalf("got %v (%#v), want %s/%s", err, p, code, certainty)
	}
	return p
}

// Catches gate ordering regressions and selection metadata authorizing a different route.
func TestRouterGateBeforeSecretOrNetwork(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	cases := []struct {
		name   string
		mutate func(*Request)
		pins   []string
		code   string
	}{
		{"live", func(r *Request) {
			r.Selection.Route.Environment = "live"
			r.Selection.Route.BaseURL = "https://api.openai.com/v1"
			r.Selection.Account.BaseURL = "https://api.openai.com/v1"
			r.Selection.Profile.BaseURL = "https://api.openai.com/v1"
			r.Profile = r.Selection.Profile
		}, nil, "live_provider_access_pending_confirmation"},
		{"unpinned", func(*Request) {}, nil, "request_rejected"},
		{"schema1", func(r *Request) { r.SnapshotSchema = 1 }, []string{srv.URL}, "request_rejected"},
		{"mismatched profile", func(r *Request) { r.Profile.Model = "other" }, []string{srv.URL}, "request_rejected"},
		{"disabled", func(r *Request) { r.Selection.Account.Enabled = false }, []string{srv.URL}, "entitlement_denied"},
		{"unknown adapter", func(r *Request) { r.Selection.Route.Adapter = "magic" }, []string{srv.URL}, "request_rejected"},
		{"wrong account", func(r *Request) { r.Selection.Account.ID = "other" }, []string{srv.URL}, "request_rejected"},
		{"wrong version", func(r *Request) { r.Selection.Account.CurrentCredentialVersion = 3 }, []string{srv.URL}, "request_rejected"},
		{"wrong route", func(r *Request) { r.Selection.Route.ID = "other" }, []string{srv.URL}, "request_rejected"},
		{"unknown billing", func(r *Request) { r.Selection.Account.BillingMode = "unknown" }, []string{srv.URL}, "request_rejected"},
		{"invalid max tokens", func(r *Request) { r.MaxOutputTokens = 0 }, []string{srv.URL}, "request_rejected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &leaseResolver{}
			r := routedRequest(routedSelection(srv.URL+"/v1", "openai_api_chat"))
			tc.mutate(&r)
			_, err := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: tc.pins}).Complete(context.Background(), r)
			requireProviderError(t, err, tc.code, "not_sent")
			if resolver.calls != 0 || calls != 0 {
				t.Fatal("denial touched secret/network")
			}
		})
	}
}

// Literal origins only: no localhost, aliases, paths, queries, credentials or broad loopback acceptance.
func TestRouterRejectsInvalidSimulationPins(t *testing.T) {
	for _, pin := range []string{"http://localhost:1234", "http://127.0.0.1:1234/v1", "http://127.0.0.1:1234/", "http://127.0.0.1:1234?x=y", "http://u:p@127.0.0.1:1234", "http://127.0.0.1:01234", "http://127.0.0.2:1234"} {
		s := routedSelection("http://127.0.0.1:1234/v1", "openai_api_chat")
		r := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{pin}})
		requireProviderError(t, r.PreflightModelSelection(context.Background(), 2, s), "request_rejected", "not_sent")
	}
}

func TestRouterExplicitAdapterWireContracts(t *testing.T) {
	for _, adapter := range []string{"openai_api_chat", "openai_api_responses", "anthropic_api_messages", "zhipu_api_chat", "kimi_api_chat", "kimi_code_chat", "openai_siwc_responses"} {
		t.Run(adapter, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("bad JSON")
				}
				if r.Method != "POST" || !strings.HasPrefix(r.UserAgent(), "Foundry/") {
					t.Errorf("method/identity: %s %s", r.Method, r.UserAgent())
				}
				for _, k := range []string{"tools", "tool_choice", "temperature"} {
					if _, ok := body[k]; ok {
						t.Errorf("unexpected %s", k)
					}
				}
				if string(body["model"]) != `"test-model"` {
					t.Error("model not exact")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-ID", "req_safe")
				switch adapter {
				case "anthropic_api_messages":
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "fake-test-secret" || r.Header.Get("Authorization") != "" || r.Header.Get("anthropic-version") != "2023-06-01" {
						t.Error("messages wire mismatch")
					}
					if string(body["max_tokens"]) != "100" || string(body["system"]) != `"instructions"` {
						t.Error("messages fields")
					}
					fmt.Fprint(w, `{"type":"message","role":"assistant","model":"actual","stop_reason":"end_turn","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":3,"output_tokens":4}}`)
				case "openai_api_responses":
					if r.URL.Path != "/v1/responses" || string(body["max_output_tokens"]) != "100" || string(body["store"]) != "false" {
						t.Error("responses wire")
					}
					fmt.Fprint(w, `{"object":"response","status":"completed","model":"actual","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":3,"output_tokens":4}}`)
				case "openai_siwc_responses":
					if r.URL.Path != "/v1/responses" || string(body["stream"]) != "true" || string(body["store"]) != "false" || string(body["instructions"]) != `"instructions"` || body["max_output_tokens"] != nil || !strings.HasPrefix(string(body["input"]), "[") {
						t.Error("siwc wire")
					}
					if len(body) != 5 {
						t.Errorf("SIWC body not allowlisted: %d", len(body))
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"status\":\"completed\",\"model\":\"actual\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\n\n")
				default:
					if r.URL.Path != "/v1/chat/completions" {
						t.Error("chat endpoint")
					}
					key := "max_tokens"
					if adapter == "openai_api_chat" {
						key = "max_completion_tokens"
					}
					if string(body[key]) != "100" {
						t.Error("wrong token limit")
					}
					fmt.Fprint(w, `{"object":"chat.completion","model":"actual","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`)
				}
				if adapter != "anthropic_api_messages" && r.Header.Get("Authorization") != "Bearer fake-test-secret" {
					t.Error("bearer mismatch")
				}
			}))
			defer srv.Close()
			resolver := &leaseResolver{}
			s := routedSelection(srv.URL+"/v1", adapter)
			router := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: []string{srv.URL}})
			result, err := router.Complete(context.Background(), routedRequest(s))
			if err != nil {
				t.Fatal(err)
			}
			if result.Text != "done" || result.RequestID != "req_safe" || result.ResolvedModel != "actual" || result.CredentialGeneration != 7 || !result.UsageKnown || result.InputTokens != 3 || result.OutputTokens != 4 {
				t.Fatalf("result %#v", result)
			}
			if calls != 1 || resolver.calls != 1 {
				t.Fatal("unexpected retry/fallback")
			}
		})
	}
}

func TestAPIResponsesRejectAmbiguousMediaWithoutRetry(t *testing.T) {
	for _, media := range [][]string{{"application/json", "text/html"}, {"application/json", "application/json"}, {""}} {
		t.Run(strings.Join(media, ";"), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header()["Content-Type"] = media
				fmt.Fprint(w, `{"object":"response","status":"completed","model":"actual","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":3,"output_tokens":4}}`)
			}))
			defer srv.Close()
			resolver := &leaseResolver{}
			r := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: []string{srv.URL}})
			result, err := r.Complete(context.Background(), routedRequest(routedSelection(srv.URL+"/v1", "openai_api_responses")))
			requireProviderError(t, err, "response_incomplete", "unknown")
			if result.Text != "" || result.UsageKnown || calls != 1 || resolver.calls != 1 {
				t.Fatal("invalid response accepted or replayed")
			}
		})
	}
}
