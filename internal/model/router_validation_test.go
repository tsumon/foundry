package model

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"foundry/internal/domain"
)

type legacyGateway struct{ calls int }

func (g *legacyGateway) Complete(_ context.Context, r Request) (Result, error) {
	g.calls++
	return Result{Text: r.Prompt}, nil
}

type networkTrap struct{ calls int }

func (t *networkTrap) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return nil, fmt.Errorf("network trap")
}

func TestRouterLegacyDelegationCannotSmuggleAccountFields(t *testing.T) {
	legacy := &legacyGateway{}
	router := NewRouter(legacy, &leaseResolver{}, RouterOptions{})
	req := Request{Profile: domain.Profile{ID: "legacy", Model: "legacy-model"}, Prompt: "legacy-output"}
	result, err := router.Complete(context.Background(), req)
	if err != nil || result.Text != "legacy-output" || legacy.calls != 1 {
		t.Fatalf("legacy changed: %#v %v", result, err)
	}
	req.Profile.RouteID = "smuggled"
	_, err = router.Complete(context.Background(), req)
	requireProviderError(t, err, "request_rejected", "not_sent")
	if legacy.calls != 1 {
		t.Fatal("invalid legacy forwarded")
	}
	if router.PreflightAccountProfile(context.Background(), domain.Profile{AccountID: "account"}) != ErrAccountInferenceDisabled {
		t.Fatal("v1 account denial lost")
	}
}

func TestRouterOAuthAndPackageDenialsHaveZeroSideEffects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*domain.RoleModelSelection)
	}{
		{"missing binding", func(s *domain.RoleModelSelection) { s.Authorization = nil }},
		{"missing direct scope", func(s *domain.RoleModelSelection) { s.Authorization.Scopes = []string{"resource.invoke"} }},
		{"missing resource scope", func(s *domain.RoleModelSelection) { s.Authorization.Scopes = []string{"chatgpt.tokens.use.direct"} }},
		{"wrong binding account", func(s *domain.RoleModelSelection) { s.Authorization.AccountID = "other" }},
		{"wrong spend", func(s *domain.RoleModelSelection) { s.Authorization.SpendClass = "explicit_api" }},
		{"unknown extra usage", func(s *domain.RoleModelSelection) { s.Route.ExtraUsage = "unknown" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := routedSelection("http://127.0.0.1:1234/v1", "openai_siwc_responses")
			tc.mutate(&s)
			resolver := &leaseResolver{}
			router := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: []string{"http://127.0.0.1:1234"}})
			trap := &networkTrap{}
			router.http.Transport = trap
			_, err := router.Complete(context.Background(), routedRequest(s))
			requireProviderError(t, err, "entitlement_denied", "not_sent")
			if resolver.calls != 0 || trap.calls != 0 {
				t.Fatal("denial read secret or network")
			}
		})
	}
}

func TestRouterRejectsIncompleteAndToolEnvelopes(t *testing.T) {
	cases := []struct{ adapter, media, body string }{
		{"openai_api_chat", "application/json", `{"object":"chat.completion","choices":[{"finish_reason":"length","message":{"role":"assistant","content":"partial"}}]}`},
		{"openai_api_chat", "application/json", `{"object":"chat.completion","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"text","tool_calls":[{"name":"exec"}]}}]}`},
		{"openai_api_chat", "application/json", `{"object":"chat.completion","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"text","function_call":{"name":"exec"}}}]}`},
		{"openai_api_chat", "application/json", `{"object":"chat.completion","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"   "}}]}`},
		{"openai_api_chat", "text/html", `{"object":"chat.completion","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"text"}}]}`},
		{"openai_api_responses", "application/json", `{"object":"response","status":"incomplete","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"partial"}]}]}`},
		{"openai_api_responses", "application/json", `{"object":"response","status":"completed","output":[{"type":"function_call","name":"exec"},{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"partial"}]}]}`},
		{"anthropic_api_messages", "application/json", `{"type":"message","role":"assistant","stop_reason":"max_tokens","content":[{"type":"text","text":"partial"}]}`},
		{"anthropic_api_messages", "application/json", `{"type":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"text"},{"type":"tool_use","name":"exec"}]}`},
		{"openai_api_chat", "application/json", strings.Repeat("x", maxResponseBytes+1)},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.media)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			s := routedSelection(srv.URL+"/v1", tc.adapter)
			result, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(context.Background(), routedRequest(s))
			requireProviderError(t, err, "response_incomplete", "unknown")
			if result.Text != "" {
				t.Fatal("untrusted partial output exposed")
			}
		})
	}
}

func TestRouterMissingMetadataStaysUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", strings.Repeat("x", 129))
		fmt.Fprint(w, `{"object":"chat.completion","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}],"usage":{"prompt_tokens":1}}`)
	}))
	defer srv.Close()
	s := routedSelection(srv.URL+"/v1", "openai_api_chat")
	result, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(context.Background(), routedRequest(s))
	if err != nil {
		t.Fatal(err)
	}
	if result.UsageKnown || result.ResolvedModel != "" || result.RequestID != "" {
		t.Fatalf("invented metadata %#v", result)
	}
}

func TestRouterDoesNotRetainSecretEchoedAsRequestID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "prefix-fake-test-secret")
		w.WriteHeader(401)
		fmt.Fprint(w, `{"detail":"secret"}`)
	}))
	defer srv.Close()
	s := routedSelection(srv.URL+"/v1", "openai_api_chat")
	_, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(context.Background(), routedRequest(s))
	p := requireProviderError(t, err, "auth_invalid", "rejected")
	if p.RequestID != "" {
		t.Fatal("credential reflected in safe metadata")
	}
}

func TestRouterDoesNotRetainSecretEchoedAsResolvedModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"chat.completion","model":"prefix-fake-test-secret","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}]}`)
	}))
	defer srv.Close()
	s := routedSelection(srv.URL+"/v1", "openai_api_chat")
	result, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(context.Background(), routedRequest(s))
	if err != nil {
		t.Fatal(err)
	}
	if result.ResolvedModel != "" {
		t.Fatal("credential reflected in model metadata")
	}
}

// Go's transport may retry requests on a reused stale connection. Dedicated
// connections prevent that implicit replay path, including explicit probes.
func TestRouterUsesFreshConnectionsToAvoidTransportReplay(t *testing.T) {
	var connections atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[]}`)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	s := routedSelection(srv.URL+"/v1", "openai_api_chat")
	router := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}})
	for range 2 {
		if _, err := router.ProbeSelection(context.Background(), 2, s); err != nil {
			t.Fatal(err)
		}
	}
	if connections.Load() != 2 {
		t.Fatal("transport reused connection and permits implicit replay")
	}
}

func TestRouterIncompleteRetainsBoundedHTTPMetadata(t *testing.T) {
	for _, media := range []string{"text/html", "application/json"} {
		t.Run(media, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", media)
				w.Header().Set("X-Request-ID", "req_bad_envelope")
				fmt.Fprint(w, `{"choices":[]}`)
			}))
			defer srv.Close()
			s := routedSelection(srv.URL+"/v1", "openai_api_chat")
			_, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(context.Background(), routedRequest(s))
			p := requireProviderError(t, err, "response_incomplete", "unknown")
			if p.HTTPStatus != 200 || p.RequestID != "req_bad_envelope" {
				t.Fatalf("missing bounded HTTP metadata: %#v", p)
			}
		})
	}
}
