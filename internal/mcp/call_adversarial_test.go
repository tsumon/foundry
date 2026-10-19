package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCallDistinguishesKnownAndUnknownPostDispatchOutcomes(t *testing.T) {
	definition := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
	tool := callTool(t, definition)
	for _, tc := range []struct {
		name, code string
		unknown    bool
		handler    func(http.ResponseWriter, callFixtureRequest)
	}{
		{name: "jsonrpc refusal", code: "remote_error", handler: func(w http.ResponseWriter, request callFixtureRequest) {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32602,"message":"fake-call-secret remote text"}}`, request.ID)
		}},
		{name: "invalid content", code: "invalid_result", handler: func(w http.ResponseWriter, request callFixtureRequest) {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"image","data":"AA==","mimeType":"image/png"}]}}`, request.ID)
		}},
		{name: "disconnect", code: "transport_error", unknown: true, handler: func(w http.ResponseWriter, request callFixtureRequest) {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newCallFixture(t, definition, tc.handler)
			defer srv.Close()
			result, err := Call(context.Background(), callConnection(srv.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "fake-call-secret", func() error { return nil })
			var protocolError *Error
			if !errors.As(err, &protocolError) || protocolError.Code != tc.code || protocolError.Unknown != tc.unknown || !protocolError.CallMayHaveBeenSent || !result.Dispatched {
				t.Fatalf("result=%#v err=%#v", result, err)
			}
			wantStage := CallStageResponseReceived
			if tc.unknown {
				wantStage = CallStageCallStarted
			}
			if result.Stage != wantStage || strings.Contains(err.Error(), "fake-call-secret") || strings.Contains(err.Error(), "remote text") {
				t.Fatalf("stage=%s err=%q", result.Stage, err)
			}
		})
	}
}

func TestCallReportsValidRemoteToolErrorAsEvidence(t *testing.T) {
	definition := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false},"outputSchema":{"type":"object","properties":{"answer":{"type":"integer"}},"required":["answer"],"additionalProperties":false}}`
	tool := callTool(t, definition)
	srv := newCallFixture(t, definition, func(w http.ResponseWriter, request callFixtureRequest) {
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"not found"}],"isError":true}}`, request.ID)
	})
	defer srv.Close()
	result, err := Call(context.Background(), callConnection(srv.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "", func() error { return nil })
	if err != nil || !result.RemoteToolError || result.Stage != CallStageResponseReceived || !strings.Contains(string(result.Result), "not found") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCallRejectsDriftTaskRequirementSessionMismatchAndCredentialReflection(t *testing.T) {
	base := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
	for _, tc := range []struct {
		name      string
		frozen    string
		live      string
		mutate    func(http.ResponseWriter, *http.Request, callFixtureRequest) bool
		wantCalls int32
		wantCode  string
	}{
		{name: "definition drift", frozen: base, live: `{"name":"lookup","description":"changed","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`, wantCode: "tool_changed"},
		{name: "task required", frozen: `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false},"execution":{"taskSupport":"required"}}`, live: base, wantCode: "task_unsupported"},
		{name: "null task support", frozen: `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false},"execution":{"taskSupport":null}}`, live: base, wantCode: "task_unsupported"},
		{name: "session mismatch", frozen: base, live: base, wantCode: "protocol_error", mutate: func(w http.ResponseWriter, r *http.Request, request callFixtureRequest) bool {
			if request.Method == "tools/list" {
				w.Header().Set("Mcp-Session-Id", "different-session")
			}
			return false
		}},
		{name: "credential reflection", frozen: base, live: base, wantCalls: 1, wantCode: "invalid_result", mutate: func(w http.ResponseWriter, r *http.Request, request callFixtureRequest) bool {
			if request.Method == "tools/call" {
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"fake-call-secret"}]}}`, request.ID)
				return true
			}
			return false
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := callTool(t, tc.frozen)
			var calls, callbacks atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				var request callFixtureRequest
				_ = json.NewDecoder(r.Body).Decode(&request)
				if request.Method == "tools/call" {
					calls.Add(1)
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.mutate != nil && tc.mutate(w, r, request) {
					return
				}
				switch request.Method {
				case "initialize":
					w.Header().Set("Mcp-Session-Id", "fixture-session")
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`, request.ID)
				case "notifications/initialized":
					w.WriteHeader(http.StatusAccepted)
				case "tools/list":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[%s]}}`, request.ID, tc.live)
				case "tools/call":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[]}}`, request.ID)
				}
			}))
			defer srv.Close()
			result, err := Call(context.Background(), callConnection(srv.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "fake-call-secret", func() error { callbacks.Add(1); return nil })
			var protocolError *Error
			if !errors.As(err, &protocolError) || protocolError.Code != tc.wantCode || calls.Load() != tc.wantCalls {
				t.Fatalf("result=%#v err=%#v calls=%d callbacks=%d", result, err, calls.Load(), callbacks.Load())
			}
			if tc.wantCalls == 0 && (result.Dispatched || callbacks.Load() != 0 || protocolError.CallMayHaveBeenSent) {
				t.Fatalf("preflight denial crossed dispatch: result=%#v err=%#v", result, err)
			}
		})
	}
}

func TestCallPreflightTransportFailureIsNotToolOutcomeUnknown(t *testing.T) {
	definition := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
	tool := callTool(t, definition)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()
	result, err := Call(context.Background(), callConnection(srv.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "", func() error { t.Fatal("callback on failed preflight"); return nil })
	var protocolError *Error
	if !errors.As(err, &protocolError) || protocolError.Unknown || protocolError.CallMayHaveBeenSent || result.Dispatched || hits.Load() != 1 {
		t.Fatalf("result=%#v err=%#v hits=%d", result, err, hits.Load())
	}
}

func TestCallCancellationAfterDispatchIsUnknownAndNotRetried(t *testing.T) {
	definition := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
	tool := callTool(t, definition)
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	srv := newCallFixture(t, definition, func(w http.ResponseWriter, request callFixtureRequest) {
		calls.Add(1)
		cancel()
		<-time.After(20 * time.Millisecond)
	})
	defer srv.Close()
	result, err := Call(ctx, callConnection(srv.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "", func() error { return nil })
	var protocolError *Error
	if !errors.As(err, &protocolError) || protocolError.Code != "canceled" || !protocolError.Unknown || !protocolError.CallMayHaveBeenSent || !result.Dispatched || calls.Load() != 1 {
		t.Fatalf("result=%#v err=%#v calls=%d", result, err, calls.Load())
	}
}

func TestCallRejectsServerCallbackWithoutExecutingIt(t *testing.T) {
	definition := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
	tool := callTool(t, definition)
	var calls atomic.Int32
	srv := newCallFixture(t, definition, func(w http.ResponseWriter, request callFixtureRequest) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":99,\"method\":\"sampling/createMessage\",\"params\":{}}\n\n")
	})
	defer srv.Close()
	result, err := Call(context.Background(), callConnection(srv.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "", func() error { return nil })
	var protocolError *Error
	if !errors.As(err, &protocolError) || protocolError.Code != "protocol_error" || protocolError.Unknown || !protocolError.CallMayHaveBeenSent || calls.Load() != 1 || result.Stage != CallStageResponseReceived {
		t.Fatalf("result=%#v err=%#v calls=%d", result, err, calls.Load())
	}
}

func TestCallTenPageSessionFitsFourteenRequestBudget(t *testing.T) {
	definition := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
	tool := callTool(t, definition)
	var pages, calls, deletes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request callFixtureRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "ten-pages")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`, request.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			page := pages.Add(1)
			if page < 10 {
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[],"nextCursor":"page-%d"}}`, request.ID, page+1)
			} else {
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[%s]}}`, request.ID, definition)
			}
		case "tools/call":
			calls.Add(1)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[]}}`, request.ID)
		default:
			t.Errorf("unexpected method %s", request.Method)
		}
	}))
	defer srv.Close()
	result, err := Call(context.Background(), callConnection(srv.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "", func() error { return nil })
	if err != nil || pages.Load() != 10 || calls.Load() != 1 || deletes.Load() != 1 || result.Requests != 14 {
		t.Fatalf("result=%#v err=%v pages=%d calls=%d deletes=%d", result, err, pages.Load(), calls.Load(), deletes.Load())
	}
}

func TestCallMissingCallbackStillSendsNoToolCall(t *testing.T) {
	definition := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
	tool := callTool(t, definition)
	var calls atomic.Int32
	srv := newCallFixture(t, definition, func(w http.ResponseWriter, request callFixtureRequest) { calls.Add(1) })
	defer srv.Close()
	result, err := Call(context.Background(), callConnection(srv.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "", nil)
	var protocolError *Error
	if !errors.As(err, &protocolError) || protocolError.Code != "dispatch_checkpoint_failed" || calls.Load() != 0 || result.Dispatched || protocolError.CallMayHaveBeenSent {
		t.Fatalf("result=%#v err=%#v calls=%d", result, err, calls.Load())
	}
}

func TestCallDoesNotFollowRedirectOrUseProxy(t *testing.T) {
	definition := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
	tool := callTool(t, definition)
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		t.Error("redirect or proxy target reached")
	}))
	defer target.Close()
	t.Setenv("HTTP_PROXY", target.URL)
	t.Setenv("HTTPS_PROXY", target.URL)
	t.Setenv("ALL_PROXY", target.URL)
	t.Setenv("NO_PROXY", "")
	var sourceHits atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceHits.Add(1)
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	result, err := Call(context.Background(), callConnection(source.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "fake-call-secret", func() error { t.Fatal("callback after redirect"); return nil })
	if err == nil || sourceHits.Load() != 1 || targetHits.Load() != 0 || result.Dispatched {
		t.Fatalf("result=%#v err=%v source=%d target=%d", result, err, sourceHits.Load(), targetHits.Load())
	}
}

func TestCallEnforcesWireNotificationAndJSONDepthBudgets(t *testing.T) {
	definition := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
	tool := callTool(t, definition)
	for _, tc := range []struct {
		name    string
		respond func(http.ResponseWriter, callFixtureRequest)
	}{
		{name: "wire response", respond: func(w http.ResponseWriter, request callFixtureRequest) {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"%s"}]}}`, request.ID, strings.Repeat("x", (1<<20)+1))
		}},
		{name: "notifications", respond: func(w http.ResponseWriter, request callFixtureRequest) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, strings.Repeat("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n", 65))
		}},
		{name: "json depth", respond: func(w http.ResponseWriter, request callFixtureRequest) {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[],"future":%s0%s}}`, request.ID, strings.Repeat("[", 34), strings.Repeat("]", 34))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newCallFixture(t, definition, tc.respond)
			defer srv.Close()
			result, err := Call(context.Background(), callConnection(srv.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "", func() error { return nil })
			var protocolError *Error
			if !errors.As(err, &protocolError) || protocolError.Code != "budget_exceeded" || protocolError.Unknown || !protocolError.CallMayHaveBeenSent || result.Stage != CallStageResponseReceived {
				t.Fatalf("result=%#v err=%#v", result, err)
			}
		})
	}
}
