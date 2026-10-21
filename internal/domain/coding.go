package domain

import (
	"errors"
	"reflect"
	"time"
)

const CodingTraceLimit = 256

type CodingAttempt struct {
	ID             string         `json:"id"`
	Epoch          int64          `json:"epoch"`
	PlanRevision   int64          `json:"plan_revision"`
	Phase          string         `json:"phase"`
	Role           Role           `json:"role,omitempty"`
	StepID         string         `json:"step_id,omitempty"`
	RepairRound    int            `json:"repair_round,omitempty"`
	Status         string         `json:"status"`
	InvocationID   string         `json:"invocation_id,omitempty"`
	Before         *GenerationRef `json:"before,omitempty"`
	After          *GenerationRef `json:"after,omitempty"`
	VerificationID string         `json:"verification_id,omitempty"`
	FailureCode    string         `json:"failure_code,omitempty"`
	StartedAt      string         `json:"started_at"`
	FinishedAt     string         `json:"finished_at,omitempty"`
}

func ValidateCodingVersion(version int, workspaceMode, toolMode string) error {
	if version == 0 {
		return nil
	}
	base, _, err := BaseWorkspaceMode(workspaceMode)
	if version != 1 || err != nil || base != WorkspaceModeGenerationsV1 || toolMode != "" {
		return errors.New("unsupported coding version or nonordinary workspace")
	}
	return nil
}

// ValidateCodingTrace is read-only. Unsupported or corrupt records must not be
// repaired by decoding and reserializing a projection this binary understands.
func ValidateCodingTrace(m Mission) error {
	if err := ValidateCodingVersion(m.CodingVersion, m.WorkspaceMode, m.ToolMode); err != nil {
		return err
	}
	if m.CodingTraceInvalid || len(m.CodingAttempts) > CodingTraceLimit || (m.CodingVersion == 0 && len(m.CodingAttempts) > 0) {
		return errors.New("invalid coding trace")
	}
	seen := map[string]bool{}
	running := false
	for _, a := range m.CodingAttempts {
		if a.ID == "" || seen[a.ID] || a.Epoch < 0 || a.PlanRevision <= 0 || a.Epoch > m.Epoch || a.Before == nil || a.Before.ID == "" || a.Before.Digest == "" {
			return errors.New("invalid coding attempt identity")
		}
		seen[a.ID] = true
		started, err := time.Parse(time.RFC3339Nano, a.StartedAt)
		if err != nil {
			return errors.New("invalid coding attempt start")
		}
		if a.After != nil && (a.After.ID == "" || a.After.Digest == "") {
			return errors.New("invalid coding output generation")
		}
		switch a.Phase {
		case "worker":
			if a.Role != Worker || a.StepID == "" || a.RepairRound != 0 {
				return errors.New("invalid worker attempt")
			}
		case "debugger":
			if a.Role != Debugger || a.RepairRound < 1 || a.RepairRound > 2 || a.StepID != "" {
				return errors.New("invalid debugger attempt")
			}
		case "verification":
			if a.Role != "" || a.StepID != "" || a.RepairRound != 0 {
				return errors.New("invalid verification attempt")
			}
		case "qa", "auditor":
			if string(a.Role) != a.Phase || a.StepID != "" || a.RepairRound != 0 {
				return errors.New("invalid review attempt")
			}
		default:
			return errors.New("unsupported coding phase")
		}
		switch a.Status {
		case "running":
			if running || a.FinishedAt != "" || a.FailureCode != "" || a.After != nil || a.VerificationID != "" {
				return errors.New("invalid running attempt")
			}
			running = true
		case "succeeded", "failed", "interrupted", "outcome_unknown":
			if finished, err := time.Parse(time.RFC3339Nano, a.FinishedAt); err != nil || finished.Before(started) {
				return errors.New("invalid coding attempt finish")
			}
			if a.Status == "succeeded" {
				if a.FailureCode != "" || a.After == nil || (a.Phase == "verification" && a.VerificationID == "") {
					return errors.New("invalid successful attempt")
				}
			} else {
				switch a.FailureCode {
				case "verification_failed", "invalid_model_output", "edit_denied", "review_rejected", "infrastructure_failed", "persistence_failed", "interrupted", "outcome_unknown":
				default:
					return errors.New("invalid coding failure code")
				}
				if (a.Status == "interrupted") != (a.FailureCode == "interrupted") || (a.Status == "outcome_unknown") != (a.FailureCode == "outcome_unknown") {
					return errors.New("inconsistent coding outcome")
				}
			}
		default:
			return errors.New("unsupported coding status")
		}
	}
	return nil
}

// Only an append or one terminal transition is allowed; identity and input
// bindings never change. New records always start running in the current epoch.
func ValidateCodingTransition(before, after Mission) error {
	if err := ValidateCodingTrace(before); err != nil {
		return err
	}
	if err := ValidateCodingTrace(after); err != nil {
		return err
	}
	if before.CodingVersion != after.CodingVersion || len(after.CodingAttempts) < len(before.CodingAttempts) {
		return errors.New("coding version and trace are immutable")
	}
	for i, a := range before.CodingAttempts {
		b := after.CodingAttempts[i]
		if a.Status != "running" {
			if !reflect.DeepEqual(a, b) {
				return errors.New("terminal coding attempt is immutable")
			}
			continue
		}
		identity := b
		identity.Status = a.Status
		identity.InvocationID = a.InvocationID
		identity.After = a.After
		identity.VerificationID = a.VerificationID
		identity.FailureCode = a.FailureCode
		identity.FinishedAt = a.FinishedAt
		if !reflect.DeepEqual(a, identity) || (a.InvocationID != "" && a.InvocationID != b.InvocationID) {
			return errors.New("coding attempt identity is immutable")
		}
		if b.Status != "running" && b.Status != "interrupted" && b.Status != "outcome_unknown" && a.Epoch != after.Epoch {
			return errors.New("stale coding attempt outcome")
		}
		if b.After != nil && !reflect.DeepEqual(b.After, after.CandidateGeneration) {
			return errors.New("coding outcome must bind selected candidate")
		}
	}
	for _, a := range after.CodingAttempts[len(before.CodingAttempts):] {
		if !reflect.DeepEqual(a.Before, after.CandidateGeneration) {
			return errors.New("coding input must bind selected candidate")
		}
		if a.Status != "running" || a.Epoch != after.Epoch || after.Plan == nil || a.PlanRevision != after.Plan.Revision {
			return errors.New("new coding attempt must start in current run")
		}
	}
	return nil
}
