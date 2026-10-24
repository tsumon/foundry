package model

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSIWCFailureDiagnosticsDoNotChangeSuccessBoundary(t *testing.T) {
	completed := `{"type":"response.completed","response":{"object":"response","status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"safe"}]}]}}`
	frame := func(s string) string { return "data: " + s + "\n\n" }
	for _, tc := range []struct{ name, body, stage, reason, terminal string }{
		{"EOF", frame(`{"type":"response.output_text.delta","delta":"PRIVATE"}`), "stream_read", "missing_terminal", ""},
		{"unterminated", "data: " + completed, "stream_read", "unterminated_frame", ""},
		{"JSON", frame(`{"PRIVATE":`), "sse_decode", "invalid_json", ""},
		{"type missing", frame(`{"PRIVATE":"PRIVATE"}`), "sse_decode", "missing_event_type", ""},
		{"type mismatch", "event: PRIVATE\n" + frame(completed), "sse_decode", "event_type_mismatch", ""},
		{"incomplete", frame(`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"PRIVATE"}}}`), "provider_terminal", "provider_incomplete", "response.incomplete"},
		{"failed", frame(`{"type":"response.failed","response":{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"PRIVATE"}}}`), "provider_terminal", "provider_failed", "response.failed"},
		{"error", frame(`{"type":"error","code":"subscription_sharing_unsupported_capability","message":"PRIVATE"}`), "provider_terminal", "provider_error", "error"},
		{"envelope JSON", frame(`{"type":"response.completed","response":42}`), "completed_response", "invalid_envelope_json", "response.completed"},
		{"envelope status", frame(`{"type":"response.completed","response":{"object":"response","status":"incomplete"}}`), "completed_response", "invalid_envelope", "response.completed"},
		{"tool", frame(`{"type":"response.completed","response":{"object":"response","status":"completed","output":[{"type":"function_call","name":"PRIVATE"}]}}`), "completed_response", "invalid_output", "response.completed"},
		{"content", strings.Replace(frame(completed), "output_text", "refusal", 1), "completed_response", "invalid_content", "response.completed"},
		{"empty", strings.Replace(frame(completed), `"safe"`, `" "`, 1), "completed_response", "empty_output", "response.completed"},
		{"body limit", strings.Repeat(":x\n", maxResponseBytes/3+2), "stream_read", "body_limit", ""},
		{"line limit", ":" + strings.Repeat("x", maxResponseBytes+1), "stream_read", "line_limit", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := decodeSIWCStream(strings.NewReader(tc.body), http.Header{})
			var p *ProviderError
			if !errors.As(err, &p) || result.Text != "" || result.UsageKnown {
				t.Fatalf("partial success: %+v %v", result, err)
			}
			wantCode, wantCertainty := "response_incomplete", "unknown"
			if tc.name == "failed" {
				wantCode, wantCertainty = "quota_exhausted", "rejected"
			}
			if tc.name == "error" {
				wantCode, wantCertainty = "request_rejected", "rejected"
			}
			if p.Code != wantCode || p.Certainty != wantCertainty {
				t.Fatalf("outcome changed: %+v", p)
			}
			raw, _ := json.Marshal(p)
			var wire struct {
				Diagnostic struct {
					Version       int
					Stage, Reason string
					Terminal      string `json:"terminal_event"`
					Bytes         int64  `json:"bytes_read"`
					Events        int    `json:"events_seen"`
				} `json:"response_diagnostic"`
			}
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			d := wire.Diagnostic
			wantEvents := 1
			if tc.name == "unterminated" || tc.name == "body limit" || tc.name == "line limit" {
				wantEvents = 0
			}
			if d.Events != wantEvents || !p.ResponseDiagnostic.Valid() {
				t.Fatalf("invalid event count/diagnostic: %s", raw)
			}
			if d.Version != 1 || d.Stage != tc.stage || d.Reason != tc.reason || d.Terminal != tc.terminal || d.Bytes <= 0 || d.Bytes > maxResponseBytes+1 {
				t.Fatalf("missing/wrong bounded diagnostic: %s", raw)
			}
			if strings.Contains(string(raw), "PRIVATE") {
				t.Fatal("raw provider data leaked")
			}
		})
	}
	result, err := decodeSIWCStream(strings.NewReader(frame(completed)), http.Header{})
	if err != nil || result.Text != "safe" {
		t.Fatalf("valid completion rejected: %+v %v", result, err)
	}
}

type diagnosticBrokenReader struct{}

func (diagnosticBrokenReader) Read([]byte) (int, error) { return 0, errors.New("PRIVATE reader error") }

func TestSIWCDiagnosticReaderFailureAndMediaType(t *testing.T) {
	for _, tc := range []struct {
		name, media, reason string
		reader              io.Reader
	}{
		{"read", "text/event-stream", "read_error", io.MultiReader(strings.NewReader("data: {\"type\":\"response.created\"}\n\n"), diagnosticBrokenReader{})},
		{"media", "application/json; secret=PRIVATE", "unexpected_media_type", strings.NewReader("PRIVATE")},
		{"invalid media", "PRIVATE;=", "invalid_media_type", strings.NewReader("PRIVATE")},
		{"missing media", "", "missing_media_type", strings.NewReader("PRIVATE")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{tc.media}}, Body: io.NopCloser(tc.reader)}
			_, err := (&Router{}).decodeResponse(response, "openai_siwc_responses")
			p := requireProviderError(t, err, "response_incomplete", "unknown")
			if !p.ResponseDiagnostic.Valid() {
				t.Fatalf("diagnostic cannot be persisted: %+v", p.ResponseDiagnostic)
			}
			raw, _ := json.Marshal(p)
			if !strings.Contains(string(raw), `"reason":"`+tc.reason+`"`) || strings.Contains(string(raw), "PRIVATE") || p.HTTPStatus != 200 {
				t.Fatalf("bad diagnostic: %s", raw)
			}
		})
	}
}

func TestSIWCCredentialGuardKeepsActualStreamCounters(t *testing.T) {
	body := "data: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"PRIVATE credential\"}]}]}}\n\n"
	req, _ := http.NewRequest("POST", "http://127.0.0.1/responses", nil)
	req.Header.Set("Authorization", "Bearer PRIVATE credential")
	r, err := (&Router{}).decodeResponse(&http.Response{StatusCode: 200, Request: req, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, "openai_siwc_responses")
	p := requireProviderError(t, err, "response_incomplete", "unknown")
	d := p.ResponseDiagnostic
	if d.Reason != "credential_echo" || d.EventsSeen != 1 || d.BytesRead != int64(len(body)) || !d.Valid() || r.Text != "" {
		t.Fatalf("guard lost real observations: %+v", d)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("credential leaked")
	}
}
