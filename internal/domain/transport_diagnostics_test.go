package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTransportDiagnosticIsClosedAndHistoricalReceiptsStayUnchanged(t *testing.T) {
	const legacy = `{"version":1,"stage":"http_media","reason":"missing_media_type","media_type":"missing","events_seen":0,"bytes_read":0}`
	const transport = `{"version":1,"content_type":"absent","protocol":"http1_1","body_length":"positive"}`
	decode := func(extra string) ModelCallReceipt {
		t.Helper()
		var d ResponseDiagnostic
		raw := legacy
		if extra != "" {
			raw = strings.TrimSuffix(raw, "}") + `,"transport":` + extra + `}`
		}
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatal(err)
		}
		return ModelCallReceipt{Certainty: "unknown", ResponseDiagnostic: d}
	}
	old := decode("")
	if SanitizeModelCallReceipt(old) != old {
		t.Fatal("historical receipt changed")
	}
	raw, _ := json.Marshal(old)
	if strings.Contains(string(raw), "transport") {
		t.Fatal("invented historical transport evidence")
	}
	current := SanitizeModelCallReceipt(decode(transport))
	raw, _ = json.Marshal(current)
	if !strings.Contains(string(raw), `"transport":`+transport) {
		t.Fatalf("transport evidence lost: %s", raw)
	}
	for _, bad := range []string{
		strings.Replace(transport, `"version":1`, `"version":99`, 1),
		strings.Replace(transport, `"version":1`, `"version":0`, 1),
		strings.Replace(transport, `"absent"`, `"PRIVATE"`, 1),
		strings.Replace(transport, `"http1_1"`, `"PRIVATE"`, 1),
		strings.Replace(transport, `"positive"`, `"PRIVATE"`, 1),
		strings.Replace(transport, `"absent"`, `"single"`, 1),
	} {
		raw, _ := json.Marshal(SanitizeModelCallReceipt(decode(bad)))
		if strings.Contains(string(raw), "response_diagnostic") || strings.Contains(string(raw), "PRIVATE") {
			t.Fatalf("unsafe/contradictory observation accepted: %s", raw)
		}
	}
}
