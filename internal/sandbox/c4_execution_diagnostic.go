package sandbox

import (
	"context"
	"errors"
	"foundry/internal/domain"
)

type c4ImageCheckError struct{ cause error }

func (c4ImageCheckError) Error() string   { return "C4 image check failed" }
func (e c4ImageCheckError) Unwrap() error { return e.cause }

type c4StageError struct {
	phase string
	cause error
}

func (c4StageError) Error() string   { return "C4 stage failed" }
func (e c4StageError) Unwrap() error { return e.cause }

func c4FailureReason(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, errC4DockerTrust):
		return "trust_failed"
	case errors.Is(err, errC4DockerProcess):
		return "process_failed"
	case errors.Is(err, errC4DockerCommand):
		return "command_failed"
	case errors.Is(err, errC4DockerOutputLimit):
		return "output_limit"
	default:
		return "protocol_failed"
	}
}

func c4StartFailure(err error) domain.C4Result {
	phase := "start_ack"
	var stage c4StageError
	if errors.As(err, &stage) {
		switch stage.phase {
		case "start_precheck", "start_attach", "start_postcheck":
			phase = stage.phase
		}
	}
	return domain.C4Result{Class: "outcome_unknown", Output: "C4 phase=" + phase + " reason=" + c4FailureReason(err) + "; execution outcome unknown."}
}

func c4CleanupFailure(o domain.C4Operation, id, phase string, err error) *domain.C4Cleanup {
	var stage c4StageError
	if phase == "cleanup_remove" && errors.As(err, &stage) && stage.phase == "cleanup_remove_precheck" {
		phase = stage.phase
	}
	return &domain.C4Cleanup{State: "failed", ContainerID: id, Owner: o.Owner, Phase: phase, Reason: c4FailureReason(err)}
}

// Only fixed stage/reason text and a syntactically valid, explicitly unverified
// observed ID enter durable output. This is diagnostic data, never Created,
// ContainerID, cleanup permission or a test result. Raw CLI output is excluded.
func c4PrestartFailure(stage string, err error, observedID string) domain.C4Result {
	var image c4ImageCheckError
	if errors.As(err, &image) {
		stage = "image"
	}
	if stage != "create" && stage != "image" && stage != "initial_inspect" {
		stage = "prestart"
	}
	reason := c4FailureReason(err)
	output := "C4 phase=" + stage + " reason=" + reason + "; execution outcome unknown."
	if stage == "initial_inspect" && c4ContainerID.MatchString(observedID) {
		output += " Observed container ID=" + observedID + " (unverified; not cleanup authority)."
	}
	return domain.C4Result{Class: "outcome_unknown", Output: output}
}
