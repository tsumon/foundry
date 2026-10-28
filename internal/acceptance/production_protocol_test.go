package acceptance

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"foundry/internal/model"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

// The old TLS fixtures replaced the production transport and added a custom
// dialer/TLS config, which accidentally disabled HTTP/2. Exercise the original
// constructor without those substitutions. Trust is process-local and synthetic;
// certificate verification must remain enabled, including in this regression.
func TestProductionTransportPinsHTTP1(t *testing.T) {
	if os.Getenv("FOUNDRY_TEST_PROTOCOL_CHILD") == "1" {
		raw, err := base64.StdEncoding.DecodeString(os.Getenv("FOUNDRY_TEST_PROTOCOL_CERT"))
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			t.Fatal(err)
		}
		pool := x509.NewCertPool()
		pool.AddCert(cert)
		x509.SetFallbackRoots(pool)
		endpoint := os.Getenv("FOUNDRY_TEST_PROTOCOL_URL")
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "https" || u.Hostname() != "127.0.0.1" || u.Port() == "" {
			t.Fatal("loopback only")
		}
		_, db, _, route, _, resolver, _ := catalogController(t)
		catalog := NewProductionCatalog(db, ProductionCatalogOptions{Route: route, Resolver: resolver})
		inference := NewProductionInference(db, ProductionInferenceOptions{Route: route, Resolver: resolver})
		for name, client := range map[string]*http.Client{"catalog": catalog.catalog.http, "inference": inference.inference.http} {
			t.Run(name, func(t *testing.T) {
				method := http.MethodPost
				var body io.Reader = strings.NewReader("synthetic protocol probe")
				if name == "catalog" {
					method, body = http.MethodGet, nil
				}
				req, err := http.NewRequest(method, endpoint, body)
				if err != nil {
					t.Fatal(err)
				}
				req.GetBody = nil
				req, observe := model.ObserveInferenceRequest(req, client.Transport)
				response, err := client.Do(req)
				diagnostic := observe(response)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				// HTTP/1.1 can be selected without ALPN; it must never select h2.
				if response.Proto != "HTTP/1.1" || (diagnostic.ALPN != "http1_1" && diagnostic.ALPN != "none") {
					t.Errorf("one-shot protocol policy drift: received %s, ALPN %s", response.Proto, diagnostic.ALPN)
				}
				if !diagnostic.TLSVerified || diagnostic.RequestWrites != 1 || diagnostic.ReusedConnection || diagnostic.ProxyMode != "disabled" {
					t.Fatalf("unsafe observation: %+v", diagnostic)
				}
				if response.Header.Get("Content-Type") != "text/event-stream" {
					t.Fatal("response media lost")
				}
				_, err = io.Copy(io.Discard, response.Body)
				if err != nil {
					t.Fatal(err)
				}
			})
		}
		return
	}
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, inferenceCompletion)
	}))
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	server.StartTLS()
	defer server.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProductionTransportPinsHTTP1$", "-test.v")
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "GODEBUG=") || strings.HasPrefix(item, "FOUNDRY_TEST_PROTOCOL_") {
			continue
		}
		cmd.Env = append(cmd.Env, item)
	}
	cmd.Env = append(cmd.Env, "GODEBUG=x509usefallbackroots=1,http2client=1", "FOUNDRY_TEST_PROTOCOL_CHILD=1", "FOUNDRY_TEST_PROTOCOL_URL="+server.URL, "FOUNDRY_TEST_PROTOCOL_CERT="+base64.StdEncoding.EncodeToString(server.Certificate().Raw))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("constructor wire regression: %v\n%s", err, output)
	}
	if requests.Load() != 2 {
		t.Fatalf("unexpected request count %d", requests.Load())
	}
}
