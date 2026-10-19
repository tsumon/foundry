package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"foundry/internal/domain"
)

func callTool(t *testing.T, definition string) domain.MCPTool {
	t.Helper()
	tool, err := parseTool(json.RawMessage(definition))
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func callPolicy() json.RawMessage {
	return json.RawMessage(`{"policy_version":"mcp_read_v1","reviewed":true,"schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`)
}

func callConnection(endpoint string) domain.MCPConnection {
	return domain.MCPConnection{ID: "local", Revision: 1, Endpoint: endpoint, Protocol: domain.MCPProtocol, Enabled: true, CredentialRevision: 1}
}

func TestCallUsesOneSessionAndDispatchesExactlyOnceJSONAndSSE(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprintf("sse=%t", sse), func(t *testing.T) {
			definition := `{"name":"lookup","description":"read","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false},"outputSchema":{"type":"object","properties":{"answer":{"type":"integer"}},"required":["answer"],"additionalProperties":false},"execution":{"taskSupport":"optional"}}`
			tool := callTool(t, definition)
			var mu sync.Mutex
			var methods []string
			var calls, forbiddenFetch atomic.Int32
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/linked" {
					forbiddenFetch.Add(1)
					w.WriteHeader(http.StatusTeapot)
					return
				}
				if r.Header.Get("Authorization") != "Bearer fake-call-secret" {
					t.Error("missing credential")
				}
				if r.Method == http.MethodDelete {
					mu.Lock()
					methods = append(methods, "DELETE")
					mu.Unlock()
					if r.Header.Get("Mcp-Session-Id") != "same-session" {
						t.Error("cleanup left the initialized session")
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.Method != http.MethodPost {
					t.Errorf("unexpected method %s", r.Method)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				var request struct {
					ID     int             `json:"id"`
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				methods = append(methods, request.Method)
				mu.Unlock()
				if request.Method != "initialize" {
					if r.Header.Get("Mcp-Session-Id") != "same-session" || r.Header.Get("MCP-Protocol-Version") != domain.MCPProtocol {
						t.Error("request did not use initialized session and protocol")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				switch request.Method {
				case "initialize":
					w.Header().Set("Mcp-Session-Id", "same-session")
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`, request.ID)
				case "notifications/initialized":
					if request.ID != 0 {
						t.Error("notification carried an id")
					}
					w.WriteHeader(http.StatusAccepted)
				case "tools/list":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[%s]}}`, request.ID, definition)
				case "tools/call":
					calls.Add(1)
					var params struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					}
					if json.Unmarshal(request.Params, &params) != nil || params.Name != "lookup" || string(params.Arguments) != `{"query":"alpha"}` {
						t.Errorf("wrong call params: %s", request.Params)
					}
					payload := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"ok"},{"type":"resource_link","uri":%q,"name":"linked"}],"structuredContent":{"answer":9007199254740993},"isError":false}}`, request.ID, srv.URL+"/linked")
					if sse {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\ndata: %s\n\n", payload)
					} else {
						fmt.Fprint(w, payload)
					}
				default:
					t.Errorf("unexpected method %s", request.Method)
				}
			}))
			defer srv.Close()

			var callbacks atomic.Int32
			result, err := Call(context.Background(), callConnection(srv.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "fake-call-secret", func() error {
				callbacks.Add(1)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if callbacks.Load() != 1 || calls.Load() != 1 || forbiddenFetch.Load() != 0 {
				t.Fatalf("callbacks=%d calls=%d resource_fetches=%d", callbacks.Load(), calls.Load(), forbiddenFetch.Load())
			}
			if result.Stage != CallStageResponseReceived || !result.Dispatched || result.RemoteToolError || result.Requests != 5 || result.RequestBytes == 0 || result.ResponseBytes == 0 || result.Duration <= 0 {
				t.Fatalf("wrong metrics/outcome: %#v", result)
			}
			if !strings.Contains(string(result.Result), `"answer":9007199254740993`) || strings.Contains(string(result.Result), "fake-call-secret") {
				t.Fatalf("wrong normalized result: %s", result.Result)
			}
			mu.Lock()
			gotMethods := strings.Join(methods, ",")
			mu.Unlock()
			if gotMethods != "initialize,notifications/initialized,tools/list,tools/call,DELETE" {
				t.Fatal(gotMethods)
			}
		})
	}
}

func TestCallCallbackFailureSendsNoToolCall(t *testing.T) {
	definition := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
	tool := callTool(t, definition)
	var calls atomic.Int32
	srv := newCallFixture(t, definition, func(w http.ResponseWriter, request callFixtureRequest) {
		calls.Add(1)
		t.Error("tools/call reached fixture after callback failure")
	})
	defer srv.Close()
	injected := errors.New("persistence unavailable")
	result, err := Call(context.Background(), callConnection(srv.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "", func() error { return injected })
	var protocolError *Error
	if !errors.As(err, &protocolError) || protocolError.Code != "dispatch_checkpoint_failed" || protocolError.Unknown || protocolError.CallMayHaveBeenSent || result.Dispatched || result.Stage != CallStagePreflight || calls.Load() != 0 {
		t.Fatalf("result=%#v err=%#v calls=%d", result, err, calls.Load())
	}
}

func TestCallRejectsInvalidArgumentsAndNonLoopbackBeforeNetwork(t *testing.T) {
	definition := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
	tool := callTool(t, definition)
	for name, testCase := range map[string]struct {
		connection domain.MCPConnection
		arguments  json.RawMessage
	}{
		"arguments": {callConnection("http://127.0.0.1:1"), json.RawMessage(`{"query":1}`)},
		"hostname":  {callConnection("http://localhost:1/mcp"), json.RawMessage(`{"query":"ok"}`)},
		"public":    {callConnection("http://192.0.2.1/mcp"), json.RawMessage(`{"query":"ok"}`)},
		"https":     {callConnection("https://127.0.0.1:1/mcp"), json.RawMessage(`{"query":"ok"}`)},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := Call(context.Background(), testCase.connection, tool, callPolicy(), testCase.arguments, "", func() error { t.Fatal("callback on local rejection"); return nil })
			var protocolError *Error
			if !errors.As(err, &protocolError) || result.Requests != 0 || result.Dispatched || protocolError.CallMayHaveBeenSent || result.Duration <= 0 {
				t.Fatalf("result=%#v err=%#v", result, err)
			}
		})
	}
}

func TestCallRejectsUnsupportedOutputSchemaBeforeNetwork(t *testing.T) {
	tool := callTool(t, `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false},"outputSchema":{"type":"object","properties":{"value":{"$ref":"https://example.invalid/schema"}}}}`)
	result, err := Call(context.Background(), callConnection("http://127.0.0.1:1"), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "", func() error {
		t.Fatal("callback for execution-ineligible output schema")
		return nil
	})
	var protocolError *Error
	if !errors.As(err, &protocolError) || protocolError.Code != "invalid_tool" || result.Requests != 0 || result.Dispatched {
		t.Fatalf("result=%#v err=%#v", result, err)
	}
}

type callFixtureRequest struct {
	ID     int
	Method string
	Params json.RawMessage
}

func newCallFixture(t *testing.T, definition string, onCall func(http.ResponseWriter, callFixtureRequest)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request callFixtureRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "fixture-session")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`, request.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[%s]}}`, request.ID, definition)
		case "tools/call":
			onCall(w, request)
		default:
			t.Errorf("unexpected method %s", request.Method)
		}
	}))
}
