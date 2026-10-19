package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDiscoveryPageToolAndTotalByteLimits(t *testing.T) {
	for _, mode := range []string{"ten-pages", "eleven-pages", "501-tools", "total-bytes"} {
		t.Run(mode, func(t *testing.T) {
			var pages, requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method == "DELETE" {
					w.WriteHeader(204)
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
					w.Header().Set("Mcp-Session-Id", "own")
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`, q.ID)
				case "notifications/initialized":
					w.WriteHeader(202)
				case "tools/list":
					page := pages.Add(1)
					cursor := fmt.Sprintf("page%d", page)
					result := map[string]any{"tools": []any{}, "nextCursor": cursor}
					if mode == "ten-pages" && page == 10 {
						delete(result, "nextCursor")
					}
					if mode == "501-tools" {
						list := []any{}
						for i := 0; i < 501; i++ {
							list = append(list, map[string]any{"name": fmt.Sprintf("tool%d", i), "inputSchema": map[string]any{"type": "object"}})
						}
						result["tools"] = list
						delete(result, "nextCursor")
					}
					if mode == "total-bytes" {
						result["extra"] = strings.Repeat("x", 900000)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
				default:
					t.Error(q.Method)
				}
			}))
			defer srv.Close()
			result, err := Discover(context.Background(), srv.URL, "")
			if mode == "ten-pages" {
				if err != nil || result.Requests != 13 || pages.Load() != 10 {
					t.Fatalf("%#v %v pages=%d", result, err, pages.Load())
				}
			} else {
				var e *Error
				if !errors.As(err, &e) || e.Code != "budget_exceeded" {
					t.Fatalf("%#v %v", result, err)
				}
			}
			if requests.Load() > 13 || pages.Load() > 10 {
				t.Fatal("exceeded HTTP/page budget")
			}
		})
	}
}

func TestSSEEndBeforeResultIsUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
	}))
	defer srv.Close()
	_, err := Discover(context.Background(), srv.URL, "")
	var e *Error
	if !errors.As(err, &e) || !e.Unknown {
		t.Fatalf("unterminated response not unknown: %#v", err)
	}
}

func TestRemoteCredentialEchoCannotBecomeMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fake-secret","version":"1"}}}`)
	}))
	defer srv.Close()
	result, err := Discover(context.Background(), srv.URL, "fake-secret")
	if err == nil || result.ServerName == "fake-secret" {
		t.Fatalf("credential retained in result: %#v %v", result, err)
	}
}

func TestSSEBufferedResultCannotHideObservedByteOverrun(t *testing.T) {
	d := &discovery{result: Result{ResponseBytes: (4 << 20) - 100}}
	reader := &countedBody{reader: strings.NewReader("data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"tools\":[]}}\n\n" + strings.Repeat(" ", 100)), d: d}
	_, err := d.readSSE(reader, 1)
	var e *Error
	if !errors.As(err, &e) || e.Code != "budget_exceeded" {
		t.Fatalf("overrun accepted: %#v", err)
	}
}

func TestRejectedInitializeCannotCreateCleanupSession(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Mcp-Session-Id", "unaccepted")
		w.WriteHeader(403)
	}))
	defer srv.Close()
	if _, err := Discover(context.Background(), srv.URL, ""); err == nil || hits.Load() != 1 {
		t.Fatalf("err=%v hits=%d", err, hits.Load())
	}
}

func TestInitializedNotificationRequiresExactlyEmpty202(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`)
		} else {
			w.WriteHeader(202)
			fmt.Fprint(w, " ")
		}
	}))
	defer srv.Close()
	_, err := Discover(context.Background(), srv.URL, "")
	if err == nil || hits.Load() != 2 {
		t.Fatalf("err=%v hits=%d", err, hits.Load())
	}
}
