package domain

import (
	"encoding/json"
	"testing"
)

func TestC4CleanupDiagnosticEnumsAndLegacy(t *testing.T) {
	for _, tc := range []struct {
		phase, reason, state string
		valid                bool
	}{
		{"", "", "failed", true}, {"cleanup_precheck", "protocol_failed", "failed", true},
		{"cleanup_remove_precheck", "trust_failed", "failed", true}, {"cleanup_remove", "command_failed", "failed", true},
		{"PRIVATE", "protocol_failed", "failed", false}, {"cleanup_precheck", "PRIVATE", "failed", false},
		{"cleanup_precheck", "", "failed", false}, {"", "protocol_failed", "failed", false},
		{"cleanup_precheck", "protocol_failed", "confirmed", false},
	} {
		t.Run(tc.phase+"/"+tc.reason+"/"+tc.state, func(t *testing.T) {
			m := c4Fixture()
			o := &m.C4Operations[0]
			o.State = "finished"
			o.Approval = &C4Approval{Digest: o.MaterialDigest, Revision: 1, At: c4Time}
			o.Dispatch = &C4Fact{Revision: 2, At: c4Time}
			o.Result = &C4Result{Class: "outcome_unknown", Revision: 3, At: c4Time}
			raw, _ := json.Marshal(map[string]any{"state": tc.state, "container_id": "container-1", "owner": o.Owner, "revision": 4, "at": c4Time, "phase": tc.phase, "reason": tc.reason})
			var c C4Cleanup
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			o.Cleanup = []C4Cleanup{c}
			if (ValidateC4Mission(m) == nil) != tc.valid {
				t.Fatal("unsafe/partial diagnostic accepted or legacy rejected")
			}
		})
	}
}
