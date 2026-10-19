package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRouterTypedErrorsDoNotLeakOrRetry(t *testing.T) {
	cases := []struct {
		name                            string
		status                          int
		body, code, providerCode, shape string
	}{
		{"identity", 401, `{"detail":"fake-test-secret auth failure"}`, "auth_invalid", "", "detail"},
		{"permission", 403, `{"detail":"quota exhausted is not a machine code"}`, "entitlement_denied", "", "detail"},
		{"rate", 429, `{"error":{"code":"rate_limit_exceeded"}}`, "rate_limited", "rate_limit_exceeded", "error_object"},
		{"temporary", 503, `{"detail":"upstream failure"}`, "server_error", "", "detail"},
		{"usage limit", 429, `{"error":{"code":"subscription_sharing_usage_limit_exceeded"}}`, "quota_exhausted", "subscription_sharing_usage_limit_exceeded", "error_object"},
		{"usage unavailable", 503, `{"error":{"code":"subscription_sharing_usage_unavailable"}}`, "server_error", "subscription_sharing_usage_unavailable", "error_object"},
		{"kimi quota", 403, `{"error":{"type":"exceeded_current_quota_error"}}`, "quota_exhausted", "exceeded_current_quota_error", "error_object"},
		{"kimi concurrency", 403, `{"error":{"type":"permission_error","message":"concurrent request limit"}}`, "entitlement_denied", "permission_error", "error_object"},
		{"untrusted code", 400, `{"error":{"code":"fake-test-secret","param":"fake-test-secret","message":"fake-test-secret"}}`, "request_rejected", "", "error_object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("X-Request-ID", "req_123")
				w.Header().Set("Retry-After", "12")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			s := routedSelection(srv.URL+"/v1", "kimi_code_chat")
			result, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(context.Background(), routedRequest(s))
			p := requireProviderError(t, err, tc.code, "rejected")
			if p.HTTPStatus != tc.status || p.ProviderCode != tc.providerCode || p.BodyShape != tc.shape || p.RequestID != "req_123" || p.RetryAfterSeconds != 12 || result.CredentialGeneration != 7 {
				t.Fatalf("metadata %#v result %#v", p, result)
			}
			encoded, _ := json.Marshal(p)
			if strings.Contains(string(encoded), "fake-test-secret") || strings.Contains(err.Error(), "fake-test-secret") {
				t.Fatal("leaked provider detail")
			}
			if calls != 1 {
				t.Fatal("retried rejected call")
			}
		})
	}
}

func TestRouterDoesNotFollowRedirect(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
	defer target.Close()
	calls := 0
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	s := routedSelection(source.URL+"/v1", "openai_api_chat")
	_, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{source.URL, target.URL}}).Complete(context.Background(), routedRequest(s))
	requireProviderError(t, err, "transport_unknown", "unknown")
	if calls != 1 || targetCalls != 0 {
		t.Fatal("redirect/retry occurred")
	}
}

func TestRouterSIWCRequiresCompleteTerminalEnvelope(t *testing.T) {
	cases := []struct{ name, stream, code, certainty, providerCode string }{
		{"delta EOF", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", "response_incomplete", "unknown", ""},
		{"incomplete", "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\"}}\n\n", "response_incomplete", "unknown", ""},
		{"failed limit", "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"subscription_sharing_usage_limit_exceeded\",\"message\":\"fake-test-secret\"}}}\n\n", "quota_exhausted", "rejected", "subscription_sharing_usage_limit_exceeded"},
		{"failed unavailable", "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"subscription_sharing_usage_unavailable\"}}}\n\n", "server_error", "rejected", "subscription_sharing_usage_unavailable"},
		{"explicit error", "data: {\"type\":\"error\",\"code\":\"subscription_sharing_unsupported_capability\",\"param\":\"tools\",\"message\":\"fake-test-secret\"}\n\n", "request_rejected", "rejected", "subscription_sharing_unsupported_capability"},
		{"wrong event", "event: response.completed\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", "response_incomplete", "unknown", ""},
		{"bad JSON", "data: nope\n\n", "response_incomplete", "unknown", ""},
		{"tool only", "data: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"name\":\"exec\"}]}}\n\n", "response_incomplete", "unknown", ""},
		{"oversize", ":" + strings.Repeat("x", maxResponseBytes+1) + "\n\n", "response_incomplete", "unknown", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Request-ID", "req_stream")
				fmt.Fprint(w, tc.stream)
			}))
			defer srv.Close()
			s := routedSelection(srv.URL+"/v1", "openai_siwc_responses")
			result, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(context.Background(), routedRequest(s))
			p := requireProviderError(t, err, tc.code, tc.certainty)
			if p.ProviderCode != tc.providerCode || p.RequestID != "req_stream" {
				t.Fatalf("bad metadata %#v", p)
			}
			if tc.certainty == "rejected" && p.HTTPStatus != 200 {
				t.Errorf("lost actual HTTP status: %d", p.HTTPStatus)
			}
			if result.Text != "" || calls != 1 {
				t.Fatal("partial success or retry")
			}
		})
	}
}

func TestRouterCancelledStreamIsUnknownNoReplay(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-release
	}))
	defer srv.Close()
	defer close(release)
	s := routedSelection(srv.URL+"/v1", "openai_siwc_responses")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-started; cancel() }()
	_, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(ctx, routedRequest(s))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request not started")
	}
	p, ok := err.(*ProviderError)
	if !ok || p.Certainty != "unknown" || (p.Code != "response_incomplete" && p.Code != "transport_unknown") {
		t.Fatalf("cancellation not classified unknown: %#v", err)
	}
}

func TestRouterPreservesSanitizedBrokerDenial(t *testing.T) {
	resolver := &leaseResolver{err: &ProviderError{Code: "transport_unknown", Certainty: "unknown", RequestID: "bad secret\n", ProviderCode: "fake-test-secret", HTTPStatus: 503, BodyShape: "fake-test-secret"}}
	s := routedSelection("http://127.0.0.1:1234/v1", "openai_siwc_responses")
	_, err := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: []string{"http://127.0.0.1:1234"}}).Complete(context.Background(), routedRequest(s))
	p := requireProviderError(t, err, "transport_unknown", "unknown")
	if p.RequestID != "" || p.ProviderCode != "" || p.BodyShape != "" {
		t.Fatal("unsanitized broker metadata")
	}
}
