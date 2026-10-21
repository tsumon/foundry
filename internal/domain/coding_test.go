package domain

import (
	"encoding/json"
	"testing"
)

func TestCodingZeroVersionPreservesHistoricalJSON(t *testing.T) {
	raw, _ := json.Marshal(Mission{})
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for _, key := range []string{"coding_version", "coding_attempts", "CodingTraceInvalid"} {
		if _, exists := fields[key]; exists {
			t.Fatal("legacy material changed", key)
		}
	}
}
func TestCodingVersionOnlyOrdinaryGenerations(t *testing.T) {
	for _, tc := range []struct {
		v          int
		mode, tool string
		ok         bool
	}{
		{0, "", "", true}, {1, WorkspaceModeGenerationsV1, "", true}, {2, WorkspaceModeGenerationsV1, "", false}, {-1, WorkspaceModeGenerationsV1, "", false}, {1, "", "", false}, {1, WorkspaceModeControlledReadV1, ToolModeControlledReadV1, false}, {1, WorkspaceModeControlledMCPReadV2, ToolModeControlledMCPReadV2, false},
	} {
		if err := ValidateCodingVersion(tc.v, tc.mode, tc.tool); (err == nil) != tc.ok {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}

func TestCodingCorruptTerminalMaterialCannotExecute(t *testing.T) {
	for _, kind := range []string{"after", "time", "status_code"} {
		t.Run(kind, func(t *testing.T) {
			ref := &GenerationRef{ID: "g", Digest: "d"}
			a := CodingAttempt{ID: "one", Epoch: 1, PlanRevision: 1, Phase: "worker", Role: Worker, StepID: "step", Status: "succeeded", Before: ref, After: ref, StartedAt: "2026-10-21T00:01:00Z", FinishedAt: "2026-10-21T00:02:00Z"}
			switch kind {
			case "after":
				a.After = &GenerationRef{}
			case "time":
				a.FinishedAt = "2026-10-21T00:00:00Z"
			case "status_code":
				a.Status = "failed"
				a.FailureCode = "outcome_unknown"
			}
			if err := ValidateCodingTrace(Mission{CodingVersion: 1, WorkspaceMode: WorkspaceModeGenerationsV1, Epoch: 1, CodingAttempts: []CodingAttempt{a}}); err == nil {
				t.Fatal("corrupt terminal trace accepted", kind)
			}
		})
	}
}
