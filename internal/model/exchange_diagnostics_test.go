package model

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Catches missing correlation/handshake receipts, credential/header leakage,
// protocol relaxation and an accidental replay of a consumed invocation.
func TestSIWCExchangeTraceOneShotAndRedacted(t *testing.T) {
	var sends atomic.Int32
	var clientID atomic.Value
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		clientID.Store(r.Header.Get("X-Client-Request-Id"))
		w.Header()["Content-Type"] = nil
		w.Header().Set("X-Request-ID", "fake-test-secret")
		w.Header().Set("X-Private", "PRIVATE upstream detail")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		fmt.Fprint(w, completedInference)
	}))
	defer srv.Close()
	lease := &leaseResolver{}
	router := NewRouter(nil, lease, RouterOptions{CodingAdmission: func(context.Context, Request) error { return nil }})
	tr := router.http.Transport.(*http.Transport)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, ServerName: srv.Certificate().DNSNames[0]}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "api.openai.com:443" {
			return nil, fmt.Errorf("unexpected endpoint")
		}
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	s := routedSelection("https://api.openai.com/v1", "openai_siwc_responses")
	s.Route.Environment = "live"
	q := routedRequest(s)
	q.InvocationID, q.Prompt = "PRIVATE invocation", "PRIVATE prompt"
	result, err := router.Complete(context.Background(), q)
	p := requireProviderError(t, err, "response_incomplete", "unknown")
	if result.Text != "" || result.UsageKnown || p.ResponseDiagnostic.BytesRead != 0 || p.RequestID != "" {
		t.Fatal("missing media accepted, read or credential echo retained")
	}
	raw, _ := json.Marshal(p)
	var receipt map[string]any
	if json.Unmarshal(raw, &receipt) != nil {
		t.Fatal("invalid JSON")
	}
	d, ok := receipt["exchange_diagnostic"].(map[string]any)
	if !ok {
		t.Fatalf("missing exchange diagnostic: %s", raw)
	}
	receivedID, _ := clientID.Load().(string)
	if !regexp.MustCompile(`^fdy_[a-f0-9]{32}$`).MatchString(receivedID) || d["client_request_id"] != receivedID {
		t.Fatal("missing or uncorrelated random request ID")
	}
	fingerprint := sha256.Sum256(srv.Certificate().Raw)
	if d["version"] != float64(1) || d["tls_verified"] != true || d["peer_certificate_sha256"] != hex.EncodeToString(fingerprint[:]) || d["peer_class"] != "loopback" || d["proxy_mode"] != "disabled" || d["request_writes"] != float64(1) || d["final_status"] != float64(200) {
		t.Fatalf("missing TLS/send observations: %s", raw)
	}
	for _, name := range []string{"connect", "tls", "headers"} {
		phase, ok := d[name].(map[string]any)
		if !ok || phase["state"] != "completed" || phase["attempts"] != float64(1) {
			t.Fatalf("missing phase %s: %s", name, raw)
		}
	}
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "fake-test-secret") || strings.Contains(string(raw), "127.0.0.1") || strings.Contains(string(raw), "example.com") {
		t.Fatal("private raw trace persisted")
	}
	_, err = router.Complete(context.Background(), q)
	requireProviderError(t, err, "request_rejected", "not_sent")
	if sends.Load() != 1 || lease.calls != 1 {
		t.Fatal("unexpected replay or credential read")
	}
}

func TestSIWCExchangeTraceSuccessRejectionRedirectAndDisconnect(t *testing.T) {
	for _, mode := range []string{"success", "quota", "redirect", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			var sends, redirects atomic.Int32
			var clientID atomic.Value
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects.Add(1) }))
			defer target.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				clientID.Store(r.Header.Get("X-Client-Request-Id"))
				switch mode {
				case "success":
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, completedInference)
				case "quota":
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(429)
					fmt.Fprint(w, `{"error":{"code":"insufficient_quota","message":"PRIVATE"}}`)
				case "redirect":
					w.Header().Set("Location", target.URL+"/PRIVATE")
					w.WriteHeader(307)
				case "disconnect":
					c, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					c.Close()
				}
			}))
			defer srv.Close()
			lease := &leaseResolver{}
			router := NewRouter(nil, lease, RouterOptions{SimulationOrigins: []string{srv.URL}, CodingAdmission: func(context.Context, Request) error { return nil }})
			q := routedRequest(routedSelection(srv.URL+"/v1", "openai_siwc_responses"))
			q.InvocationID = "one"
			result, err := router.Complete(context.Background(), q)
			d := result.ExchangeDiagnostic
			if mode == "success" {
				if err != nil || result.Text != "FOUNDRY_INFERENCE_OK" {
					t.Fatalf("valid stream failed: %v", err)
				}
			} else {
				code, certainty := "transport_unknown", "unknown"
				if mode == "quota" {
					code, certainty = "quota_exhausted", "rejected"
				}
				p := requireProviderError(t, err, code, certainty)
				d = p.ExchangeDiagnostic
			}
			receivedID, _ := clientID.Load().(string)
			if !d.Valid() || d.ClientRequestID != receivedID || d.RequestWrites != 1 || d.TLSVerified || d.TLS.State != "" {
				t.Fatalf("lost trace: %+v", d)
			}
			raw, _ := json.Marshal(d)
			if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "127.0.0.1") {
				t.Fatal("private trace leaked")
			}
			_, err = router.Complete(context.Background(), q)
			requireProviderError(t, err, "request_rejected", "not_sent")
			if sends.Load() != 1 || lease.calls != 1 || redirects.Load() != 0 {
				t.Fatal("request repeated or redirect followed")
			}
		})
	}
}

func TestSIWCExchangeTraceTLSFailureIsNotRetried(t *testing.T) {
	var sends, dials atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1) }))
	defer srv.Close()
	r := NewRouter(nil, &leaseResolver{}, RouterOptions{CodingAdmission: func(context.Context, Request) error { return nil }})
	tr := r.http.Transport.(*http.Transport)
	// Deliberately leave the loopback fixture certificate untrusted; validation
	// must fail before sending any HTTP request. No external address is dialled.
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1)
		if addr != "api.openai.com:443" {
			return nil, errors.New("denied")
		}
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	s := routedSelection("https://api.openai.com/v1", "openai_siwc_responses")
	s.Route.Environment = "live"
	q := routedRequest(s)
	q.InvocationID = "one"
	_, err := r.Complete(context.Background(), q)
	p := requireProviderError(t, err, "transport_unknown", "unknown")
	d := p.ExchangeDiagnostic
	if !d.Valid() || d.TLS.State != "failed" || d.TLSVerified || d.RequestWrites != 0 || d.FinalStatus != 0 || sends.Load() != 0 || dials.Load() != 1 {
		t.Fatalf("TLS failure observation/retry changed: %+v", d)
	}
}

func TestExchangeObserverFreezesConcurrentCallbacksAndRedactsErrors(t *testing.T) {
	o := newExchangeObserver(&http.Transport{})
	tr := o.trace()
	tr.DNSStart(httptrace.DNSStartInfo{Host: "PRIVATE hostname"})
	tr.DNSDone(httptrace.DNSDoneInfo{Err: errors.New("PRIVATE secret"), Addrs: []net.IPAddr{{IP: net.ParseIP("10.2.3.4")}}})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); tr.GotFirstResponseByte() }()
	}
	wg.Wait()
	d := o.finish(nil)
	if !d.Valid() || d.DNS.State != "failed" {
		t.Fatal(d)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("PRIVATE")})
			tr.ConnectStart("PRIVATE", "PRIVATE")
			tr.ConnectDone("PRIVATE", "PRIVATE", nil)
		}()
	}
	wg.Wait()
	if after := o.d; after != d {
		t.Fatal("late callbacks rewrote frozen trace")
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "10.2.3.4") {
		t.Fatal("raw DNS/error data persisted")
	}
}

func TestExchangeTracePreservesAggregateInformationalHeaderLimit(t *testing.T) {
	var sends atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		w.Header().Set("X-Padding", strings.Repeat("x", 512))
		for i := 0; i < 3; i++ {
			w.WriteHeader(http.StatusEarlyHints)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, completedInference)
	}))
	defer srv.Close()
	router := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}})
	// The same cumulative policy as the production default, with a small bound
	// so a regression does not require allocating multi-megabyte header fixtures.
	router.http.Transport.(*http.Transport).MaxResponseHeaderBytes = 1024
	result, err := router.Complete(context.Background(), routedRequest(routedSelection(srv.URL+"/v1", "openai_siwc_responses")))
	if err == nil || result.Text != "" {
		t.Fatal("observation bypassed aggregate 1xx/final header limit")
	}
	p := requireProviderError(t, err, "transport_unknown", "unknown")
	if !p.ExchangeDiagnostic.Valid() || sends.Load() != 1 {
		t.Fatal("lost bounded failure or retried")
	}
}

func TestExchangeTraceRetainsCorrelationForNonstandardStatus(t *testing.T) {
	for _, code := range []string{"600", "000"} {
		t.Run(code, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				fmt.Fprintf(conn, "HTTP/1.1 %s unexpected\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}", code)
			}))
			defer srv.Close()
			r := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}})
			result, err := r.Complete(context.Background(), routedRequest(routedSelection(srv.URL+"/v1", "openai_siwc_responses")))
			wantCode, wantCertainty := "server_error", "rejected"
			wantStatus := 600
			if code == "000" {
				wantCode, wantCertainty, wantStatus = "response_incomplete", "unknown", 0
			}
			p := requireProviderError(t, err, wantCode, wantCertainty)
			if result.Text != "" || !p.ExchangeDiagnostic.Valid() || p.ExchangeDiagnostic.ClientRequestID == "" || p.ExchangeDiagnostic.Headers.State != "completed" || p.ExchangeDiagnostic.FinalStatus != wantStatus || calls.Load() != 1 {
				t.Fatalf("nonstandard status dropped trace or relaxed outcome: %+v", p)
			}
		})
	}
}
