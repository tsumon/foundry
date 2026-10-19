package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCallAllowsResponseHeadersAfterPreflightTimeout(t *testing.T) {
	definition := `{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
	tool := callTool(t, definition)
	var calls atomic.Int32
	srv := newCallFixture(t, definition, func(w http.ResponseWriter, request callFixtureRequest) {
		calls.Add(1)
		time.Sleep(5200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"delayed"}]}}`, request.ID)
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	result, err := Call(ctx, callConnection(srv.URL), tool, callPolicy(), json.RawMessage(`{"query":"alpha"}`), "", func() error { return nil })
	if err != nil || calls.Load() != 1 || !result.Dispatched || result.Stage != CallStageResponseReceived {
		t.Fatalf("result=%#v err=%#v calls=%d", result, err, calls.Load())
	}
}

func TestCallAndDiscoveryKeepDistinctHeaderTimeouts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	_, callTransport, err := newCallHTTPClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer callTransport.CloseIdleConnections()
	_, discoveryTransport, err := newHTTPClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer discoveryTransport.CloseIdleConnections()
	if callTransport.ResponseHeaderTimeout != 30*time.Second || discoveryTransport.ResponseHeaderTimeout != 5*time.Second {
		t.Fatalf("call=%s discovery=%s", callTransport.ResponseHeaderTimeout, discoveryTransport.ResponseHeaderTimeout)
	}
}
