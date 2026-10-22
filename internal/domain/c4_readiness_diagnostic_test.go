package domain

import "testing"

// Catches arbitrary diagnostic text entering the append-only authority journal.
func TestC4ReadinessDiagnosticValidation(t *testing.T) {
	for _, code := range []string{"readiness_gate_failed", "version_trust_failed", "version_process_failed", "info_command_failed", "info_output_limit", "image_protocol_failed", "image_timeout", "image_canceled", "failed", "image_secret", "PRIVATE", "image_protocol_failed\nPRIVATE"} {
		m := readyC4Fixture()
		r := m.C4Controls[0].Result
		r.Status, r.FailureCode, r.ValidUntil, r.Fingerprint = "failed", code, "", ""
		want := code != "image_secret" && code != "PRIVATE" && code != "image_protocol_failed\nPRIVATE"
		if got := ValidateC4Controls(m) == nil; got != want {
			t.Errorf("code %q valid=%v want %v", code, got, want)
		}
		if want {
			n := cloneC4(m)
			n.C4Controls[0].Result.FailureCode = "timeout"
			if ValidateC4ControlTransition(m, n) == nil {
				t.Fatal("historical result rewritten")
			}
			if code != "failed" {
				m.C4Controls[0].Result.Status = "outcome_unknown"
				if ValidateC4Controls(m) == nil {
					t.Fatal("unknown outcome assigned a definite failure stage")
				}
			}
		}
	}
}
