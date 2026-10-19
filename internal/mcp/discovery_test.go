package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestDiscoverFixedSequenceJSONAndSSE(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer fake-r2-secret" {
			t.Error("missing credential")
		}
		if r.Method == "DELETE" {
			methods = append(methods, "DELETE")
			if r.Header.Get("Mcp-Session-Id") != "owned" {
				t.Error("wrong session")
			}
			w.WriteHeader(204)
			return
		}
		if r.Method != "POST" {
			t.Errorf("forbidden method %s", r.Method)
			w.WriteHeader(400)
			return
		}
		var q struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		methods = append(methods, q.Method)
		w.Header().Set("Content-Type", "application/json")
		switch q.Method {
		case "initialize":
			if q.Params["protocolVersion"] != "2025-11-25" {
				t.Error("wrong protocol")
			}
			w.Header().Set("Mcp-Session-Id", "owned")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`, q.ID)
		case "notifications/initialized":
			if q.ID != 0 {
				t.Error("notification id")
			}
			w.WriteHeader(202)
		case "tools/list":
			if r.Header.Get("MCP-Protocol-Version") != "2025-11-25" {
				t.Error("protocol header")
			}
			if q.Params["cursor"] == nil {
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"one","inputSchema":{"type":"object"}}],"nextCursor":"page2"}}`, q.ID)
			} else {
				if q.Params["cursor"] != "page2" {
					t.Error("wrong cursor")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"tools\":[{\"name\":\"two\",\"inputSchema\":{\"type\":\"object\"}}]}}\n\n", q.ID)
			}
		default:
			t.Errorf("forbidden method %s", q.Method)
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	result, err := Discover(context.Background(), srv.URL+"/mcp", "fake-r2-secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 2 || result.ProtocolVersion != "2025-11-25" || result.Requests != 5 || result.ResponseBytes <= 0 {
		t.Fatalf("wrong result: %#v", result)
	}
	if strings.Join(methods, ",") != "initialize,notifications/initialized,tools/list,tools/list,DELETE" {
		t.Fatal(methods)
	}
}

func TestToolDefinitionDigestPreservesLargeIntegersAndKeyOrder(t *testing.T) {
	first, err := parseTool(json.RawMessage(`{"name":"precise","inputSchema":{"type":"object","minimum":9007199254740992}}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseTool(json.RawMessage(`{"name":"precise","inputSchema":{"type":"object","minimum":9007199254740993}}`))
	if err != nil {
		t.Fatal(err)
	}
	if first.DefinitionDigest == second.DefinitionDigest {
		t.Fatal("distinct large integers share a definition digest")
	}
	reordered, err := parseTool(json.RawMessage(`{"inputSchema":{"minimum":9007199254740993,"type":"object"},"name":"precise"}`))
	if err != nil || reordered.DefinitionDigest != second.DefinitionDigest {
		t.Fatalf("key order changed definition identity: %v", err)
	}
}

func TestToolDefinitionPreservesUnboundedJSONNumbers(t *testing.T) {
	for _, field := range []string{"inputSchema", "outputSchema", "annotations"} {
		t.Run(field, func(t *testing.T) {
			definition := `{"name":"precise","inputSchema":{"type":"object"},"outputSchema":{"type":"object"},"annotations":{}}`
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(definition), &fields); err != nil {
				t.Fatal(err)
			}
			want := `{"type":"object","maximum":1e400,"minimum":9007199254740993}`
			fields[field] = json.RawMessage(want)
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			tool, err := parseTool(raw)
			if err != nil {
				t.Fatalf("valid JSON number rejected: %v", err)
			}
			got := map[string]json.RawMessage{"inputSchema": tool.InputSchema, "outputSchema": tool.OutputSchema, "annotations": tool.Annotations}[field]
			if string(got) != want {
				t.Fatalf("number altered: %s", got)
			}
			fields[field] = json.RawMessage(`{"type":"object","maximum":1e401,"minimum":9007199254740993}`)
			changedRaw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := parseTool(changedRaw)
			if err != nil || changed.DefinitionDigest == tool.DefinitionDigest {
				t.Fatalf("large exponent discarded from digest: %v", err)
			}
		})
	}
}
