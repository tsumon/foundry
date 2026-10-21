package engine

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/store"
)

// codingFailure carries a finite classification from the boundary that knows
// the outcome. Error prose is never parsed to manufacture evidence.
type codingFailure struct {
	code string
	err  error
}

func (e *codingFailure) Error() string { return e.err.Error() }
func (e *codingFailure) Unwrap() error { return e.err }
func codingError(code string, err error) error {
	if err == nil {
		return nil
	}
	var typed *codingFailure
	if errors.As(err, &typed) || errors.Is(err, context.Canceled) {
		return err
	}
	return &codingFailure{code: code, err: err}
}

func (e *Engine) beginCoding(op *operation, phase string, role domain.Role, stepID string, round int) (string, error) {
	m, err := e.s.GetMission(op.id)
	if err != nil {
		return "", codingError("persistence_failed", err)
	}
	if err = domain.ValidateCodingTrace(m); err != nil {
		return "", err
	}
	if m.CodingVersion == 0 {
		return "", nil
	}
	id, err := opaque()
	if err != nil {
		return "", codingError("infrastructure_failed", err)
	}
	_, err = e.update(op, "coding.started", func(m *domain.Mission) error {
		if len(m.CodingAttempts) >= domain.CodingTraceLimit {
			return errors.New("coding trace limit reached; no dispatch")
		}
		if _, err := e.candidate(*m); err != nil {
			return err
		}
		if m.Plan == nil {
			return errors.New("coding attempt requires approved plan")
		}
		m.CodingAttempts = append(m.CodingAttempts, domain.CodingAttempt{ID: id, Epoch: op.epoch, PlanRevision: m.Plan.Revision, Phase: phase, Role: role, StepID: stepID, RepairRound: round, Status: "running", Before: m.CandidateGeneration, StartedAt: now()})
		return nil
	})
	return id, err
}

func (e *Engine) finishCodingAttempt(op *operation, id, invocationID string, cause error) error {
	if id == "" {
		return cause
	}
	status, code := "succeeded", ""
	if cause != nil {
		status, code = "outcome_unknown", "outcome_unknown"
		var typed *codingFailure
		if errors.As(cause, &typed) {
			status, code = "failed", typed.code
			if code == "outcome_unknown" {
				status = "outcome_unknown"
			}
		}
		if errors.Is(cause, context.Canceled) {
			status, code = "interrupted", "interrupted"
		}
		if store.IsPersistenceError(cause) {
			status, code = "failed", "persistence_failed"
		}
	}
	_, err := e.update(op, "coding.finished", func(m *domain.Mission) error {
		for i := range m.CodingAttempts {
			a := &m.CodingAttempts[i]
			if a.ID != id {
				continue
			}
			if a.Status != "running" {
				return errors.New("coding attempt already terminal")
			}
			a.Status, a.FailureCode, a.FinishedAt = status, code, now()
			a.InvocationID = invocationID
			// Only the transactionally selected generation is evidence of an after.
			a.After = m.CandidateGeneration
			if (a.Phase == "verification" || a.Phase == "qa" || a.Phase == "auditor") && len(m.Evidence) > 0 {
				v := m.Evidence[len(m.Evidence)-1]
				if v.CandidateDigest == a.Before.Digest && v.AcceptanceRevision == a.PlanRevision && (a.Phase != "verification" || v.CreatedAt >= a.StartedAt) {
					a.VerificationID = v.ID
				}
			}
			return nil
		}
		return errors.New("coding attempt missing")
	})
	if err != nil {
		// A failed terminal save is itself a storage failure, never success.
		// Try recording it once; if storage still fails, leave the durable
		// running entry for restart reconciliation and stop the workflow.
		if code != "persistence_failed" && !errors.Is(err, context.Canceled) {
			return e.finishCodingAttempt(op, id, invocationID, codingError("persistence_failed", err))
		}
		return err
	}
	return cause
}

func (e *Engine) codingEdit(op *operation, role domain.Role, extra any, files []string, stepID string, round int) error {
	id, err := e.beginCoding(op, string(role), role, stepID, round)
	if err != nil {
		return err
	}
	var out editOutput
	err = e.call(op, role, extra, &out)
	if err == nil {
		err = e.apply(op, out, files)
	}
	return e.finishCodingAttempt(op, id, op.modelInvocationID, err)
}

func (e *Engine) verify(op *operation) (bool, error) {
	id, err := e.beginCoding(op, "verification", "", "", 0)
	if err != nil {
		return false, err
	}
	passed, err := e.verifyCandidate(op)
	if err == nil && !passed && id != "" {
		err = codingError("verification_failed", errors.New("candidate verification failed"))
	}
	finishErr := e.finishCodingAttempt(op, id, "", err)
	// A persisted verification failure is the sole condition permitting repair.
	if err != nil && finishErr == err {
		var typed *codingFailure
		if errors.As(err, &typed) && typed.code == "verification_failed" {
			return false, nil
		}
	}
	return passed, finishErr
}

// Interruption records no after/evidence guesses. It never replays work.
func interruptCoding(m *domain.Mission) {
	for i := range m.CodingAttempts {
		a := &m.CodingAttempts[i]
		if a.Status == "running" {
			a.Status = "interrupted"
			a.FailureCode = "interrupted"
			a.FinishedAt = now()
		}
	}
}
func unfinishedCoding(m domain.Mission) bool {
	for _, a := range m.CodingAttempts {
		if a.Status == "running" {
			return true
		}
	}
	return false
}
