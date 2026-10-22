package sandbox

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestC4StartStagesPersistWithoutPrivateDetails(t *testing.T) {
	for _, stage := range []string{"start_precheck", "start_attach", "start_postcheck"} {
		t.Run(stage, func(t *testing.T) {
			h, f, tr := newC4DockerHarness(t)
			i := map[string]int{"start_precheck": 3, "start_attach": 4, "start_postcheck": 5}[stage]
			reason := "protocol_failed"
			if stage == "start_attach" {
				f.steps[i].err = errors.New("PRIVATE token=secret")
				reason = "process_failed"
			} else {
				f.steps[i].out = "PRIVATE invalid"
			}
			f.steps = append(f.steps[:i+1], f.steps[8:]...)
			o, err := runC4DockerHarness(h, f, tr)
			if err == nil || o.Result == nil || o.Result.Class != "outcome_unknown" || o.Started != nil || o.Passed() || o.CleanupState() != "confirmed" {
				t.Fatal("incorrect start failure state", o, err)
			}
			if !strings.Contains(o.Result.Output, "phase="+stage+" reason="+reason) || strings.Contains(o.Result.Output, "PRIVATE") {
				t.Fatal("missing sanitized start diagnostic", o.Result.Output)
			}
			if h.mission.C4Operations[0].Result.Output != o.Result.Output {
				t.Fatal("diagnostic not durable")
			}
		})
	}
}

func TestC4CleanupPrechecksPersistAndNeverRemove(t *testing.T) {
	for _, i := range []int{8, 9} {
		t.Run(string(rune('0'+i)), func(t *testing.T) {
			h, f, tr := newC4DockerHarness(t)
			f.steps[i].out = "PRIVATE malformed"
			f.steps = f.steps[:i+1]
			o, err := runC4DockerHarness(h, f, tr)
			if err == nil || o.Passed() || o.CleanupState() != "failed" || len(o.Cleanup) != 1 || f.calls != i+1 {
				t.Fatal("cleanup blocked but not recorded", o, err)
			}
			raw, _ := json.Marshal(o.Cleanup[0])
			var fields map[string]any
			json.Unmarshal(raw, &fields)
			phase := "cleanup_precheck"
			if i == 9 {
				phase = "cleanup_remove_precheck"
			}
			if fields["phase"] != phase || fields["reason"] != "protocol_failed" || strings.Contains(string(raw), "PRIVATE") {
				t.Fatal("missing bounded cleanup diagnostic", string(raw))
			}
			if h.mission.C4Operations[0].CleanupState() != "failed" {
				t.Fatal("not durable")
			}
		})
	}
}

func TestC4CleanupBlockedPersistenceFailureStops(t *testing.T) {
	h, f, tr := newC4DockerHarness(t)
	f.steps[8].out = "invalid"
	f.steps = f.steps[:9]
	h.failPersist = "cleanup"
	o, err := runC4DockerHarness(h, f, tr)
	if !errors.Is(err, ErrC4Persistence) || len(o.Cleanup) != 0 || f.calls != 9 {
		t.Fatal("failed blocked-cleanup persistence not propagated", o, err)
	}
}
