package httpapi

import (
	"encoding/json"
	"fmt"
	"foundry/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCapabilitiesAuthenticatedReadAndNoCall(t *testing.T) {
	f := newFixture(t)
	if r := f.request("GET", "/api/capabilities", "", false); r.Code != 401 {
		t.Fatal(r.Code)
	}
	f.authenticate()
	r := f.request("GET", "/api/capabilities", "", true)
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	var c domain.CapabilitySnapshot
	if err := json.Unmarshal(r.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.Connections == nil || c.Snapshots == nil || c.ToolExecution {
		t.Fatalf("bad snapshot %#v", c)
	}
	if r = f.request(http.MethodPost, "/api/capabilities/mcp/x/call", `{}`, true); r.Code != 404 {
		t.Fatal(r.Code)
	}
}

func TestMCPHTTPContractReadNoNetworkReplayAndCAS(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	var hits atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != "POST" {
			t.Errorf("unexpected %s", r.Method)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fake-http-secret" {
			t.Error("credential not forwarded")
		}
		var q struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		w.Header().Set("Content-Type", "application/json")
		switch q.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`, q.ID)
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[]}}`, q.ID)
		default:
			t.Error(q.Method)
		}
	}))
	defer remote.Close()
	body := fmt.Sprintf(`{"expected_revision":0,"name":"fixture","endpoint":%q,"enabled":true,"api_key":"fake-http-secret"}`, remote.URL)
	r := f.request("POST", "/api/capabilities/mcp", body, true)
	if r.Code != 200 || strings.Contains(r.Body.String(), "fake-http-secret") {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	var saved struct {
		Connection mcpConnectionView `json:"connection"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	c := saved.Connection
	if c.ID == "" || c.Revision != 1 || !c.HasCredential || strings.Contains(r.Body.String(), "credential_ref") {
		t.Fatal(c)
	}
	r = f.request("GET", "/api/capabilities", "", true)
	if r.Code != 200 || hits.Load() != 0 || strings.Contains(r.Body.String(), "fake-http-secret") {
		t.Fatal("settings read dispatched or leaked")
	}
	path := "/api/capabilities/mcp/" + c.ID + "/discover"
	for _, body := range []string{`{"expected_revision":1,"request_id":"request","consent":false}`, `{"expected_revision":1,"request_id":"request","consent":true,"tool":"x"}`} {
		r = f.request("POST", path, body, true)
		if r.Code != 400 || hits.Load() != 0 {
			t.Fatalf("%d %s", r.Code, r.Body.String())
		}
	}
	r = f.request("POST", path, `{"expected_revision":1,"request_id":"request","consent":true}`, true)
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	var result struct {
		Operation domain.DiscoveryOperation `json:"operation"`
		Snapshot  *domain.MCPToolSnapshot   `json:"snapshot"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Operation.Status != "succeeded" || result.Snapshot == nil || result.Snapshot.ID != result.Operation.SnapshotID || hits.Load() != 3 {
		t.Fatalf("%#v hits=%d", result, hits.Load())
	}
	r = f.request("POST", path, `{"expected_revision":1,"request_id":"request","consent":true}`, true)
	if r.Code != 200 || hits.Load() != 3 {
		t.Fatal("duplicate dispatch")
	}
	r = f.request("GET", "/api/capabilities/discoveries/request", "", true)
	if r.Code != 200 || hits.Load() != 3 {
		t.Fatal("read dispatch")
	}
	r = f.request("POST", path, `{"expected_revision":2,"request_id":"request","consent":true}`, true)
	if r.Code != 409 || hits.Load() != 3 {
		t.Fatal("wrong intent accepted")
	}
	r = f.request("POST", "/api/capabilities/mcp", fmt.Sprintf(`{"id":%q,"expected_revision":0,"name":"stale","endpoint":%q,"enabled":true}`, c.ID, c.Endpoint), true)
	if r.Code != 409 {
		t.Fatal(r.Code)
	}
	r = f.request("GET", "/api/capabilities/discoveries/missing", "", true)
	if r.Code != 404 {
		t.Fatal(r.Code)
	}
	r = f.request("GET", "/api/health", "", true)
	var health struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &health)
	if !health.Capabilities["mcp_discovery"] || health.Capabilities["tool_execution"] || health.Capabilities["skill_execution"] || health.Capabilities["git_write"] {
		t.Fatal(health)
	}
}

func TestCapabilityHTTPHostOriginAndJSONBoundary(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	for _, tc := range []struct {
		method, path, host, origin, contentType string
		want                                    int
	}{
		{"GET", "/api/capabilities", "evil.example", "", "", 403},
		{"GET", "/api/capabilities", "127.0.0.1:4317", "https://evil.example", "", 403},
		{"POST", "/api/capabilities/mcp", "127.0.0.1:4317", "https://evil.example", "application/json", 403},
		{"POST", "/api/capabilities/mcp", "127.0.0.1:4317", testOrigin, "text/plain", 403},
		{"POST", "/api/capabilities/mcp/x/call", "127.0.0.1:4317", testOrigin, "application/json", 404},
		{"POST", "/api/capabilities/call", "127.0.0.1:4317", testOrigin, "application/json", 404},
	} {
		req := httptest.NewRequest(tc.method, testOrigin+tc.path, strings.NewReader(`{}`))
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Content-Type", tc.contentType)
		req.AddCookie(f.cookie)
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, req)
		if rr.Code != tc.want {
			t.Fatalf("%s %s: %d", tc.method, tc.path, rr.Code)
		}
	}
	for _, path := range []string{"/api/capabilities", "/api/capabilities/discoveries/request"} {
		if r := f.request("GET", path, "", false); r.Code != 401 {
			t.Fatal(r.Code)
		}
	}
	r := f.request("POST", "/api/capabilities/mcp", `{"expected_revision":0,"name":"fixture","endpoint":"https://secret@example.com","enabled":true}`, true)
	if r.Code != 400 || strings.Contains(r.Body.String(), "secret@example.com") {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
}
