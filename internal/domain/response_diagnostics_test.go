package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiagnosticSanitizerDropsUnknownOrContradictoryData(t *testing.T) {
	valid := ResponseDiagnostic{Version: 1, Stage: "provider_terminal", Reason: "provider_incomplete", MediaType: "sse", TerminalEvent: "response.incomplete", EventsSeen: 1, BytesRead: 100}
	for _, mutate := range []func(*ResponseDiagnostic){
		func(d *ResponseDiagnostic) { d.Stage = "PRIVATE" },
		func(d *ResponseDiagnostic) { d.Reason = "PRIVATE" },
		func(d *ResponseDiagnostic) { d.MediaType = "PRIVATE" },
		func(d *ResponseDiagnostic) { d.TerminalEvent = "PRIVATE" },
		func(d *ResponseDiagnostic) { d.TerminalEvent = "response.completed" },
		func(d *ResponseDiagnostic) { d.Version = 99 },
		func(d *ResponseDiagnostic) { d.BytesRead = (2 << 20) + 2 },
		func(d *ResponseDiagnostic) { d.EventsSeen = -1 },
	} {
		d := valid
		mutate(&d)
		r := SanitizeModelCallReceipt(ModelCallReceipt{Certainty: "unknown", ResponseDiagnostic: d})
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), "response_diagnostic") || strings.Contains(string(raw), "PRIVATE") {
			t.Fatalf("unsafe data: %s", raw)
		}
	}
	r := ModelCallReceipt{Certainty: "unknown", ResponseDiagnostic: valid}
	if SanitizeModelCallReceipt(r) != r {
		t.Fatal("valid diagnostic lost")
	}
	raw, _ := json.Marshal(ModelCallReceipt{Certainty: "unknown"})
	if strings.Contains(string(raw), "response_diagnostic") {
		t.Fatal("invented historical diagnostic")
	}
}
