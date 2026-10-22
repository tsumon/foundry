package engine

import (
	"errors"
	"foundry/internal/domain"
	"foundry/internal/sandbox"
)

func (e *Engine) verifyC4Candidate(run *operation) (bool, error) {
	m, err := e.update(run, "verification.started", func(m *domain.Mission) error {
		if _, err := e.candidate(*m); err != nil {
			return err
		}
		// No repeated dispatch of unchanged bytes, including a newly named generation.
		for _, o := range m.C4Operations {
			if o.Kind == "candidate" && o.Epoch == m.Epoch && o.PlanRevision == c4PlanRevision(*m) && o.CandidateDigest == m.CandidateDigest {
				return errors.New("C4 candidate already verified; changed candidate required")
			}
		}
		m.State = "verifying"
		m.Stage = "verify"
		return nil
	})
	if err != nil {
		return false, err
	}
	o, err := e.newC4Operation(m, "candidate")
	if err != nil {
		return false, err
	}
	m, err = e.update(run, "c4.prepared", func(current *domain.Mission) error {
		if err := e.c4Current(*current, o); err != nil {
			return err
		}
		current.C4Operations = append(current.C4Operations, o)
		return nil
	})
	if err != nil {
		return false, err
	}
	m, err = e.update(run, "c4.approved", func(current *domain.Mission) error {
		if err := e.c4Current(*current, o); err != nil {
			return err
		}
		for i := len(current.Approvals) - 1; i >= 0; i-- {
			a := current.Approvals[i]
			if a.Kind == "execute" && a.PlanRevision == o.PlanRevision {
				index := len(current.C4Operations) - 1
				current.C4Operations[index].Approval = &domain.C4Approval{Digest: a.Digest, Revision: a.Revision, At: a.CreatedAt}
				current.C4Operations[index].State = "approved"
				o = current.C4Operations[index]
				return nil
			}
		}
		return ErrC4Unavailable
	})
	if err != nil {
		return false, err
	}
	candidate, err := e.selectedCandidate(m)
	if err != nil {
		return false, err
	}
	candidate, err = e.c4Path(candidate)
	if err != nil {
		return false, err
	}
	finished, executionErr := e.executeC4(run.ctx, m, o, candidate)
	if errors.Is(executionErr, sandbox.ErrC4Persistence) {
		return false, codingError("persistence_failed", executionErr)
	}
	if finished.Result == nil {
		return false, codingError("infrastructure_failed", executionErr)
	}
	r := finished.Result
	v := c4Verification(m, finished)
	_, err = e.update(run, "verification.completed", func(current *domain.Mission) error {
		if err := e.c4Current(*current, finished); err != nil {
			return err
		}
		current.Evidence = append(current.Evidence, v)
		return nil
	})
	if err != nil {
		return false, err
	}
	if finished.Passed() {
		return true, nil
	}
	if r.Class == "test_failed" && finished.CleanupState() == "confirmed" && errors.Is(executionErr, sandbox.ErrC4Execution) {
		return false, nil
	}
	return false, codingError("infrastructure_failed", errors.New("C4 infrastructure/outcome/cleanup requires explicit diagnosis; no repair or replay"))
}

func c4Verification(m domain.Mission, o domain.C4Operation) domain.Verification {
	r := o.Result
	code := -1
	if r.ExitCode != nil {
		code = *r.ExitCode
	}
	source := "synthetic_stub"
	if m.Spec.C4.ControlVersion == 1 && m.Spec.C4.Mode == "docker" {
		source = "unknown"
		for _, c := range m.C4Controls {
			if c.ID == o.ReadinessID && c.MaterialDigest == o.ReadinessDigest && c.SourceKind == "docker_c4_control" && c.Result != nil && c.Result.Status == "ready" && o.Dispatch != nil && o.Started != nil && o.CleanupState() == "confirmed" && (r.Class == "passed" || r.Class == "test_failed") && r.ExitCode != nil && r.Tests > 0 {
				source = "isolated_execution"
			}
		}
	}
	return domain.Verification{ID: o.ID, CandidateDigest: o.CandidateDigest, ImageID: m.Spec.C4.ImageID, CreatedAt: r.At, AcceptanceRevision: o.PlanRevision, Passed: o.Passed(), SourceVersion: 1, SourceKind: source, Results: []domain.CommandResult{{Command: m.Spec.C4.Verifier, ExitCode: code, Output: r.Output, TimedOut: r.Class == "timeout"}}}
}
