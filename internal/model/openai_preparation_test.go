package model

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIPreparationEndpointPolicy(t *testing.T) {
	tests := []struct {
		base string
		want string
	}{
		{"https://api.openai.com/v1", "exact_official"},
		{"http://127.0.0.1:43123/v1", "owned_simulation_required"},
		{"https://api.openai.com/v1/", "invalid"},
		{"https://api.openai.com/v1/extra", "invalid"},
		{"https://api.openai.com/v1?x=1", "invalid"},
		{"https://api.openai.com/v1#x", "invalid"},
		{"https://user@api.openai.com/v1", "invalid"},
		{"http://localhost:43123/v1", "invalid"},
		{"http://127.0.0.1:043123/v1", "invalid"},
		{"http://127.0.0.1:43123/v1%2fextra", "invalid"},
		{"http://127.0.0.1:43123/v1/%2e%2e/v1", "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.base, func(t *testing.T) {
			if got := OpenAIPreparationEndpointPolicy(tt.base); got != tt.want {
				t.Fatalf("policy=%q want %q", got, tt.want)
			}
		})
	}
}

// Removing the exact /v1 gate would allow route metadata to lease a credential
// for a different resource on an otherwise pinned origin.
func TestOpenAIEndpointAliasesCannotLeaseCredentials(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()

	bad := []string{
		srv.URL + "/v1/extra",
		srv.URL + "/v1/",
		srv.URL + "/v1%2fextra",
		srv.URL + "/v1/%2e%2e/v1",
		srv.URL + "/v1?x=1",
		srv.URL + "/v1#x",
		strings.Replace(srv.URL, "http://", "http://user@", 1) + "/v1",
		strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/v1",
		strings.Replace(srv.URL, "http://127.0.0.1:", "http://127.0.0.1:0", 1) + "/v1",
	}
	for _, base := range bad {
		t.Run(base, func(t *testing.T) {
			resolver := &leaseResolver{}
			router := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: []string{srv.URL}})
			s := routedSelection(base, "openai_siwc_responses")
			_, completeErr := router.Complete(context.Background(), routedRequest(s))
			requireProviderError(t, completeErr, "request_rejected", "not_sent")
			_, probeErr := router.ProbeSelection(context.Background(), 2, s)
			requireProviderError(t, probeErr, "request_rejected", "not_sent")
			if resolver.calls != 0 || hits != 0 {
				t.Fatalf("alias leased or dispatched: reads=%d hits=%d", resolver.calls, hits)
			}
		})
	}
}

func TestOpenAICanonicalSimulationUsesRealHTTPPath(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path=%q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"status\":\"completed\",\"model\":\"synthetic\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\n")
	}))
	defer srv.Close()
	resolver := &leaseResolver{}
	s := routedSelection(srv.URL+"/v1", "openai_siwc_responses")
	result, err := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(context.Background(), routedRequest(s))
	if err != nil || result.Text != "done" || resolver.calls != 1 || hits != 1 {
		t.Fatalf("result=%+v err=%v reads=%d hits=%d", result, err, resolver.calls, hits)
	}
}

// A future endpoint mapping typo must fail before the credential boundary,
// even when the base origin itself is valid and pinned.
func TestOpenAIUnsupportedMethodPathCannotLeaseCredentials(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	for _, tc := range []struct{ method, endpoint string }{
		{http.MethodPost, "/models"},
		{http.MethodGet, "/responses"},
		{http.MethodPost, "/chat/completions"},
		{http.MethodGet, "/../models"},
	} {
		resolver := &leaseResolver{}
		router := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: []string{srv.URL}})
		s := routedSelection(srv.URL+"/v1", "openai_siwc_responses")
		_, _, err := router.send(context.Background(), s, tc.method, tc.endpoint, nil)
		requireProviderError(t, err, "request_rejected", "not_sent")
		if resolver.calls != 0 || hits != 0 {
			t.Fatalf("unsupported pair leased or dispatched: %s %s reads=%d hits=%d", tc.method, tc.endpoint, resolver.calls, hits)
		}
	}
}
