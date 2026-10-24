package model

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A real loopback response must retain its header observation across send/decode.
// Even a complete SSE body cannot turn a missing/invalid media type into success.
func TestSIWCTransportMediaDiagnostics(t *testing.T) {
	const body = "data: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"safe\"}]}]}}\n\n"
	for _, tc := range []struct {
		name                   string
		headers                []string
		wantHeader, wantReason string
	}{
		{"absent", nil, "absent", "missing_media_type"},
		{"empty", []string{""}, "empty", "missing_media_type"},
		{"invalid", []string{"PRIVATE;="}, "single", "invalid_media_type"},
		{"wrong", []string{"application/json; secret=PRIVATE"}, "single", "unexpected_media_type"},
		{"multiple", []string{"application/json", "text/event-stream"}, "multiple", "unexpected_media_type"},
		{"valid", []string{"text/event-stream; charset=utf-8"}, "single", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.URL.Path != "/v1/responses" || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("request contract changed")
				}
				var payload map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["stream"] != true || payload["store"] != false || payload["max_output_tokens"] != nil {
					t.Error("subscription payload changed")
				}
				w.Header()["Content-Type"] = tc.headers // nil suppresses net/http's content sniffing.
				w.Header().Set("X-Request-ID", "fake-test-secret")
				w.Header().Set("X-PRIVATE", "PRIVATE")
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			resolver := &leaseResolver{}
			router := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: []string{server.URL}})
			result, err := router.Complete(context.Background(), routedRequest(routedSelection(server.URL+"/v1", "openai_siwc_responses")))
			if calls.Load() != 1 || resolver.calls != 1 {
				t.Fatal("unexpected replay or secret read")
			}
			if tc.wantReason == "" {
				if err != nil || result.Text != "safe" {
					t.Fatalf("valid SSE rejected: %+v %v", result, err)
				}
				return
			}
			p := requireProviderError(t, err, "response_incomplete", "unknown")
			if result.Text != "" || result.UsageKnown || p.ResponseDiagnostic.Reason != tc.wantReason || p.ResponseDiagnostic.BytesRead != 0 || p.HTTPStatus != 200 {
				t.Fatalf("success boundary changed: %+v %+v", result, p)
			}
			raw, _ := json.Marshal(p)
			var wire struct {
				Diagnostic struct {
					Transport struct {
						Version     int
						ContentType string `json:"content_type"`
						Protocol    string `json:"protocol"`
						BodyLength  string `json:"body_length"`
					} `json:"transport"`
				} `json:"response_diagnostic"`
			}
			if json.Unmarshal(raw, &wire) != nil {
				t.Fatal("invalid receipt JSON")
			}
			d := wire.Diagnostic.Transport
			if d.Version != 1 || d.ContentType != tc.wantHeader || d.Protocol != "http1_1" || d.BodyLength != "positive" {
				t.Fatalf("lost transport-boundary observation: %s", raw)
			}
			if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "fake-test-secret") || !p.ResponseDiagnostic.Valid() {
				t.Fatalf("unsafe diagnostic: %s", raw)
			}
		})
	}
}

type mediaReadGuard struct{ reads int }

func (r *mediaReadGuard) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }
func (*mediaReadGuard) Close() error               { return nil }

func TestSIWCMissingMediaNeverSniffsBody(t *testing.T) {
	body := &mediaReadGuard{}
	result, err := (&Router{}).decodeResponse(&http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, "openai_siwc_responses")
	requireProviderError(t, err, "response_incomplete", "unknown")
	if body.reads != 0 || result.Text != "" {
		t.Fatal("headerless body was read or accepted")
	}
}
