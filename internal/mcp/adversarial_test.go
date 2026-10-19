package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Each fixture mutates one protocol boundary; accepting any would publish an
// incomplete or untrusted-as-valid new catalog.
func TestDiscoverRejectsAdversarialResponses(t *testing.T) {
	cases := []struct {
		name, initialize, list, contentType string
		want                                string
	}{
		{name: "wrong version", initialize: `{"protocolVersion":"2026-07-28","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}`, want: "unsupported_protocol"},
		{name: "missing tools", initialize: `{"protocolVersion":"2025-11-25","capabilities":{},"serverInfo":{"name":"fixture","version":"1"}}`, want: "protocol_error"},
		{name: "duplicate tools", list: `{"tools":[{"name":"x","inputSchema":{"type":"object"}},{"name":"x","inputSchema":{"type":"object"}}]}`, want: "protocol_error"},
		{name: "array schema", list: `{"tools":[{"name":"x","inputSchema":[]}]}`, want: "invalid_response"},
		{name: "null schema", list: `{"tools":[{"name":"x","inputSchema":null}]}`, want: "invalid_response"},
		{name: "schema type", list: `{"tools":[{"name":"x","inputSchema":{"type":"string"}}]}`, want: "invalid_response"},
		{name: "cursor cycle", list: `{"tools":[],"nextCursor":"again"}`, want: "protocol_error"},
		{name: "long cursor", list: `{"tools":[],"nextCursor":"` + strings.Repeat("x", 4097) + `"}`, want: "budget_exceeded"},
		{name: "missing tools array", list: `{}`, want: "invalid_response"},
		{name: "duplicate JSON keys", list: `{"tools":[],"tools":[]}`, want: "invalid_response"},
		{name: "schema bytes", list: `{"tools":[{"name":"x","inputSchema":{"type":"object","description":"` + strings.Repeat("x", 65536) + `"}}]}`, want: "budget_exceeded"},
		{name: "deep schema", list: `{"tools":[{"name":"x","inputSchema":{"type":"object","x":` + strings.Repeat(`[`, 34) + `0` + strings.Repeat(`]`, 34) + `}}]}`, want: "budget_exceeded"},
		{name: "large response", list: `{"tools":[],"extra":"` + strings.Repeat("x", 1<<20) + `"}`, want: "budget_exceeded"},
		{name: "wrong id", list: `RAW:{"jsonrpc":"2.0","id":999,"result":{"tools":[]}}`, want: "protocol_error"},
		{name: "result and error", list: `RAW:{"jsonrpc":"2.0","id":2,"result":{"tools":[]},"error":{"code":1}}`, want: "invalid_response"},
		{name: "server request", list: `RAW:{"jsonrpc":"2.0","id":2,"method":"sampling/createMessage","params":{}}`, want: "protocol_error"},
		{name: "error text", list: `RAW:{"jsonrpc":"2.0","id":2,"error":{"code":1,"message":"fake-r2-secret<script>"}}`, want: "protocol_error"},
		{name: "html", list: `<html>fake-r2-secret</html>`, contentType: "text/html", want: "invalid_response"},
		{name: "malformed SSE", list: "event: message\ndata: nope\n\n", contentType: "text/event-stream", want: "invalid_response"},
		{name: "SSE endpoint switch", list: "event: endpoint\ndata: https://other.example\n\n", contentType: "text/event-stream", want: "protocol_error"},
		{name: "SSE server request", list: "data: {\"jsonrpc\":\"2.0\",\"id\":99,\"method\":\"elicitation/create\"}\n\n", contentType: "text/event-stream", want: "protocol_error"},
		{name: "too many notifications", list: strings.Repeat("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n", 65), contentType: "text/event-stream", want: "budget_exceeded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "POST" {
					t.Errorf("unexpected %s", r.Method)
					w.WriteHeader(400)
					return
				}
				var q struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&q)
				w.Header().Set("Content-Type", "application/json")
				switch q.Method {
				case "initialize":
					result := tc.initialize
					if result == "" {
						result = `{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}`
					}
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, q.ID, result)
				case "notifications/initialized":
					w.WriteHeader(202)
				case "tools/list":
					if tc.contentType != "" {
						w.Header().Set("Content-Type", tc.contentType)
						fmt.Fprint(w, tc.list)
					} else if strings.HasPrefix(tc.list, "RAW:") {
						fmt.Fprint(w, strings.TrimPrefix(tc.list, "RAW:"))
					} else {
						fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, q.ID, tc.list)
					}
				default:
					t.Errorf("unexpected method %s", q.Method)
				}
			}))
			defer srv.Close()
			result, err := Discover(context.Background(), srv.URL, "fake-r2-secret")
			var e *Error
			if !errors.As(err, &e) || e.Code != tc.want || e.Unknown {
				t.Fatalf("result=%#v err=%#v", result, err)
			}
			if strings.Contains(err.Error(), "fake-r2-secret") {
				t.Fatal("leaked remote text")
			}
			if requests.Load() > 4 {
				t.Fatal("retried after invalid response")
			}
		})
	}
}

func TestDiscoverNoRedirectProxyOrAutomaticRetry(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		t.Error("credential redirect or proxy reached")
	}))
	defer target.Close()
	t.Setenv("HTTP_PROXY", target.URL)
	t.Setenv("HTTPS_PROXY", target.URL)
	t.Setenv("ALL_PROXY", target.URL)
	t.Setenv("NO_PROXY", "")
	var sourceHits atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sourceHits.Add(1); http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	_, err := Discover(context.Background(), source.URL, "fake-secret")
	if err == nil || sourceHits.Load() != 1 || targetHits.Load() != 0 {
		t.Fatalf("err=%v source=%d target=%d", err, sourceHits.Load(), targetHits.Load())
	}
	client, tr, err := newHTTPClient(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.CloseIdleConnections()
	if tr.Proxy != nil || tr.TLSClientConfig.InsecureSkipVerify || client.CheckRedirect == nil {
		t.Fatal("unsafe transport")
	}
}

func TestDiscoverDisconnectAndCancelAreUnknown(t *testing.T) {
	for _, cancelAfterRequest := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelAfterRequest), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if cancelAfterRequest {
					cancel()
					return
				}
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
			}))
			defer srv.Close()
			_, err := Discover(ctx, srv.URL, "")
			var e *Error
			if !errors.As(err, &e) || !e.Unknown || requests.Load() != 1 {
				t.Fatalf("err=%#v hits=%d", err, requests.Load())
			}
		})
	}
}

func TestDiscoveryDeadlineAndNoRequestWhenAlreadyCanceled(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, srv.URL, ""); err == nil || requests.Load() != 0 {
		t.Fatal("canceled dispatch")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Discover(ctx, srv.URL, "")
	if err == nil || time.Since(start) > time.Second || requests.Load() != 1 {
		t.Fatal("deadline not propagated")
	}
}

func TestEndpointAndDNSAddressRejection(t *testing.T) {
	for _, endpoint := range []string{"http://8.8.8.8/mcp", "http://localhost/mcp", "https://user:secret@example.com/mcp", "https://example.com/mcp?q=x", "https://example.com/mcp#x", "http://10.0.0.1/mcp", "https://169.254.169.254/mcp", "https://127.0.0.1:0/mcp", "https://[fe80::1%25en0]/mcp", "https://100.100.100.200/mcp"} {
		if ValidateEndpoint(endpoint) == nil {
			t.Errorf("allowed %s", endpoint)
		}
	}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.0.1", "169.254.169.254", "224.0.0.1", "0.0.0.0", "100.64.0.1", "::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1", "64:ff9b::7f00:1"} {
		if publicAddress(netip.MustParseAddr(address)) {
			t.Errorf("public %s", address)
		}
	}
	u, _ := url.Parse("https://fixture.invalid/mcp")
	for _, addresses := range [][]net.IP{{net.ParseIP("127.0.0.1")}, {net.ParseIP("8.8.8.8"), net.ParseIP("10.0.0.1")}} {
		dial := pinnedDial(u, func(context.Context, string, string) ([]net.IP, error) { return addresses, nil })
		conn, err := dial(context.Background(), "tcp", "fixture.invalid:443")
		if conn != nil {
			conn.Close()
		}
		if err == nil {
			t.Fatal("dial allowed forbidden DNS")
		}
	}
}

func TestSessionCleanupFailureDoesNotDiscardCatalog(t *testing.T) {
	var deletes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deletes.Add(1)
			w.WriteHeader(500)
			return
		}
		var q struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		w.Header().Set("Content-Type", "application/json")
		if q.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "own")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`, q.ID)
		} else if q.Method == "notifications/initialized" {
			w.WriteHeader(202)
		} else if q.Method == "tools/list" {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[]}}`, q.ID)
		} else {
			t.Error(q.Method)
		}
	}))
	defer srv.Close()
	result, err := Discover(context.Background(), srv.URL, "")
	if err != nil || result.Tools == nil || len(result.Tools) != 0 || deletes.Load() != 1 {
		t.Fatalf("%#v %v deletes=%d", result, err, deletes.Load())
	}
}

func TestSSEPrimerAndCleanupBodyAccounting(t *testing.T) {
	var bytesWritten atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(500)
			n, _ := fmt.Fprint(w, "cleanup failure")
			bytesWritten.Add(int64(n))
			return
		}
		var q struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		w.Header().Set("Content-Type", "application/json")
		var body string
		if q.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "own")
			body = fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`, q.ID)
		} else if q.Method == "notifications/initialized" {
			w.WriteHeader(202)
		} else if q.Method == "tools/list" {
			w.Header().Set("Content-Type", "text/event-stream")
			body = fmt.Sprintf("id: primer\ndata:\n\n: comment\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"tools\":[]}}\n\n", q.ID)
		} else {
			t.Error(q.Method)
		}
		n, _ := fmt.Fprint(w, body)
		bytesWritten.Add(int64(n))
	}))
	defer srv.Close()
	result, err := Discover(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.ResponseBytes != bytesWritten.Load() {
		t.Fatalf("bytes=%d want=%d", result.ResponseBytes, bytesWritten.Load())
	}
}
