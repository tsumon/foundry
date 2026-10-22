package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Catches losing the failed phase/reason, raw transport disclosure, or running
// another query after failure. Only the process boundary is simulated.
func TestC4ReadinessStageDiagnostics(t *testing.T) {
	for stage, name := range []string{"version", "info", "image"} {
		for _, reason := range []string{"trust_failed", "process_failed", "command_failed", "output_limit", "protocol_failed", "timeout", "canceled"} {
			t.Run(name+"/"+reason, func(t *testing.T) {
				p, c, steps := c4ReadinessFixture()
				tr := &c4CountingTrust{}
				steps[stage].stderr = "PRIVATE /secret/path token=untrusted\n"
				switch reason {
				case "trust_failed":
					tr.failAt = stage + 1
				case "process_failed":
					steps[stage].err = errors.New("PRIVATE process failure")
				case "command_failed":
					steps[stage].exit = 1
				case "output_limit":
					steps[stage].out = strings.Repeat("PRIVATE", c4DockerInspectLimit)
				case "protocol_failed":
					steps[stage].out = `{"PRIVATE":"not valid metadata"}`
				case "timeout":
					steps[stage].err = context.DeadlineExceeded
				case "canceled":
					steps[stage].err = context.Canceled
				}
				f := &c4ScriptProcess{t: t, steps: steps}
				r, err := checkC4DockerReadiness(context.Background(), p, c, f, tr)
				b, _ := json.Marshal(r)
				wantCalls := stage + 1
				if reason == "trust_failed" {
					wantCalls = stage
				}
				if err == nil || r.Fingerprint != "" || f.calls != wantCalls || r.FailureCode != name+"_"+reason {
					t.Fatalf("lost failure phase or continued: result=%s err=%v calls=%d", b, err, f.calls)
				}
				if strings.Contains(string(b)+err.Error(), "PRIVATE") || strings.Contains(string(b)+err.Error(), "/secret") {
					t.Fatal("untrusted diagnostic escaped")
				}
				if reason == "timeout" && !errors.Is(err, context.DeadlineExceeded) || reason == "canceled" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation semantics lost")
				}
			})
		}
	}
}

func TestC4ReadinessGateDiagnosticHasNoEffects(t *testing.T) {
	p, c, _ := c4ReadinessFixture()
	c.Enabled = false
	f, tr := &c4ScriptProcess{t: t}, &c4CountingTrust{}
	r, err := checkC4DockerReadiness(context.Background(), p, c, f, tr)
	b, _ := json.Marshal(r)
	if err == nil || r.FailureCode != "readiness_gate_failed" || f.calls != 0 || tr.calls != 0 {
		t.Fatal("gate diagnostic missing or has effects", string(b), err)
	}
}
