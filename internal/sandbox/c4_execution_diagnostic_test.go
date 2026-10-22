package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestC4PrestartFailureDiagnostics(t *testing.T) {
	for _, stage := range []string{"image", "create", "initial_inspect"} {
		for _, reason := range []string{"protocol_failed", "command_failed", "trust_failed", "process_failed", "output_limit", "timeout", "canceled"} {
			t.Run(stage+"/"+reason, func(t *testing.T) {
				h, f, tr := newC4DockerHarness(t)
				i := map[string]int{"image": 0, "create": 1, "initial_inspect": 2}[stage]
				s := &f.steps[i]
				s.stderr = "PRIVATE token=private-value /private/path"
				switch reason {
				case "protocol_failed":
					s.out = "PRIVATE malformed"
				case "command_failed":
					s.exit = 1
				case "trust_failed":
					tr.failAt = i + 1
				case "process_failed":
					s.err = errors.New("PRIVATE process detail")
				case "output_limit":
					s.out = strings.Repeat("x", c4DockerInspectLimit+1)
				case "timeout":
					s.err = context.DeadlineExceeded
				case "canceled":
					s.err = context.Canceled
				}
				o, err := runC4DockerHarness(h, f, tr)
				calls := i + 1
				if reason == "trust_failed" {
					calls = i
				}
				if err == nil || o.Result == nil || o.Result.Class != "outcome_unknown" || f.calls != calls || o.Created != nil || o.Started != nil || o.ContainerID != "" || len(o.Cleanup) != 0 || o.Passed() {
					t.Fatal("failure advanced authority or lost terminal result", o, err, f.calls)
				}
				expected := "C4 phase=" + stage + " reason=" + reason + "; execution outcome unknown."
				if stage == "initial_inspect" {
					expected += " Observed container ID=" + strings.Repeat("d", 64) + " (unverified; not cleanup authority)."
				}
				if o.Result.Output != expected {
					t.Fatalf("lost diagnostic: got %q want %q", o.Result.Output, expected)
				}
				b, _ := json.Marshal(o)
				if strings.Contains(string(b), "PRIVATE") || strings.Contains(string(b), "private-value") {
					t.Fatal("raw diagnostic leaked")
				}
				if h.mission.C4Operations[0].Result.Output != expected {
					t.Fatal("diagnostic not persisted")
				}
			})
		}
	}
}

func TestC4InitialInspectionFailureResultPersistenceStops(t *testing.T) {
	h, f, tr := newC4DockerHarness(t)
	f.steps[2].out = "malformed"
	h.failPersist = "result"
	o, err := runC4DockerHarness(h, f, tr)
	if !errors.Is(err, ErrC4Persistence) || o.Result != nil || o.Created != nil || o.ContainerID != "" || f.calls != 3 {
		t.Fatal("uncertain result caused further effects", o, err)
	}
}
