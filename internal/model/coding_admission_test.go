package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Missing, changed and reused claims must never resolve a credential or send.
func TestCodingRouterClaimIsOneShotAndCheckedAgainBeforeSend(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"one","object":"chat.completion","model":"actual","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	resolver := &leaseResolver{}
	checks := 0
	admission := func(ctx context.Context, q Request) error {
		checks++
		if q.InvocationID != "claimed" || q.Prompt != "sealed" {
			return errors.New("denied")
		}
		return nil
	}
	router := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: []string{server.URL}, CodingAdmission: admission})
	q := routedRequest(routedSelection(server.URL+"/v1", "openai_api_chat"))
	for _, id := range []string{"", "unclaimed"} {
		q.InvocationID = id
		if _, err := router.Complete(context.Background(), q); err == nil {
			t.Fatal("unclaimed request sent")
		}
	}
	if resolver.calls != 0 || calls != 0 {
		t.Fatal("denial had effects")
	}
	q.InvocationID, q.Prompt = "claimed", "sealed"
	if _, err := router.Complete(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if checks < 2 || calls != 1 || resolver.calls != 1 {
		t.Fatal("missing admission checks")
	}
	if _, err := router.Complete(context.Background(), q); err == nil {
		t.Fatal("claim replayed")
	}
	if calls != 1 || resolver.calls != 1 {
		t.Fatal("duplicate had effects")
	}
}

func TestCodingRouterLivePreflightDoesNotAuthorizeCatalog(t *testing.T) {
	resolver := &leaseResolver{}
	r := NewRouter(nil, resolver, RouterOptions{CodingAdmission: func(context.Context, Request) error { return errors.New("not claimed") }})
	s := routedSelection("https://api.openai.com/v1", "openai_api_responses")
	s.Route.Environment = "live"
	if err := r.PreflightModelSelection(context.Background(), 2, s); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Complete(context.Background(), routedRequest(s)); err == nil {
		t.Fatal("unclaimed live request")
	}
	if _, err := r.ProbeSelection(context.Background(), 2, s); err == nil {
		t.Fatal("coding admission enabled catalog")
	}
	if resolver.calls != 0 {
		t.Fatal("preflight read credential")
	}
}

// Live declarations still use only an owned TLS listener in this test. The
// transport never performs public DNS or reaches a real provider.
func TestCodingRouterLiveAdaptersUseExactSelectedProtocol(t *testing.T) {
	for _, adapter := range []string{"openai_api_responses", "openai_siwc_responses"} {
		t.Run(adapter, func(t *testing.T) {
			sends := 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends++
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || r.URL.Path != "/v1/responses" || r.Method != "POST" || body["store"] != false || r.Header.Get("Authorization") != "Bearer fake-test-secret" {
					t.Error("wrong selected protocol")
				}
				if adapter == "openai_siwc_responses" {
					if _, ok := body["max_output_tokens"]; ok {
						t.Error("unsupported SIWC output limit")
					}
					if body["stream"] != true {
						t.Error("SIWC requires stream")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, completedInference)
				} else {
					if body["max_output_tokens"] != float64(100) {
						t.Error("API output bound missing")
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"object":"response","status":"completed","model":"owned","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}]}`)
				}
			}))
			defer srv.Close()
			lease := &leaseResolver{}
			router := NewRouter(nil, lease, RouterOptions{CodingAdmission: func(ctx context.Context, q Request) error {
				if q.InvocationID != "exact" || q.Selection.Route.Adapter != adapter {
					return errors.New("denied")
				}
				return nil
			}})
			transport := srv.Client().Transport.(*http.Transport).Clone()
			transport.TLSClientConfig.ServerName = srv.Certificate().DNSNames[0]
			transport.Proxy = nil
			transport.DisableKeepAlives = true
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr != "api.openai.com:443" {
					return nil, fmt.Errorf("unexpected endpoint %s", addr)
				}
				return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
			}
			router.http.Transport = transport
			selection := routedSelection("https://api.openai.com/v1", adapter)
			selection.Route.Environment = "live"
			q := routedRequest(selection)
			q.InvocationID = "exact"
			result, err := router.Complete(context.Background(), q)
			if err != nil || result.Text == "" || sends != 1 || lease.calls != 1 {
				t.Fatal(result, err, sends, lease.calls)
			}
			if _, err = router.ProbeSelection(context.Background(), 2, selection); err == nil || sends != 1 || lease.calls != 1 {
				t.Fatal("task gate authorized catalog")
			}
		})
	}
}

type codingRevokeResolver struct {
	revoked *bool
	calls   int
}

func (r *codingRevokeResolver) ResolveModelCredential(context.Context, domain.RoleModelSelection) (CredentialLease, error) {
	r.calls++
	*r.revoked = true
	return CredentialLease{Secret: "owned", Generation: 1}, nil
}

func TestCodingRouterRevocationDuringCredentialLeaseNeverSends(t *testing.T) {
	sends := 0
	revoked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends++ }))
	defer srv.Close()
	resolver := &codingRevokeResolver{revoked: &revoked}
	router := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: []string{srv.URL}, CodingAdmission: func(context.Context, Request) error {
		if revoked {
			return errors.New("revoked")
		}
		return nil
	}})
	q := routedRequest(routedSelection(srv.URL+"/v1", "openai_api_responses"))
	q.InvocationID = "one"
	_, err := router.Complete(context.Background(), q)
	requireProviderError(t, err, "request_rejected", "not_sent")
	if sends != 0 || resolver.calls != 1 {
		t.Fatal("revoked request sent")
	}
}
