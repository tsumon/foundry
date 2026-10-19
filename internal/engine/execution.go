package engine

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/workspace"
	"path/filepath"
)

func (e *Engine) candidate(m domain.Mission) (string, error) {
	path, err := e.selectedCandidate(m)
	if err != nil {
		return "", err
	}
	digest, err := workspace.Digest(path)
	if err != nil {
		return "", err
	}
	if digest != m.CandidateDigest {
		return "", errors.New("candidate changed outside controller")
	}
	if err = e.checkSnapshot(m); err != nil {
		return "", err
	}
	seal, err := authority(m, filepath.Join(e.dir(m.ID), "base"), true)
	if err != nil {
		return "", err
	}
	if !hasSeal(m, "execute", seal) {
		return "", errors.New("execution approval inputs changed")
	}
	return digest, nil
}
func (e *Engine) apply(op *operation, out editOutput, files []string) error {
	m, err := e.s.GetMission(op.id)
	if err != nil {
		return err
	}
	if domain.UsesGenerations(m.WorkspaceMode) {
		return e.applyGeneration(op, out, files)
	}
	_, err = e.update(op, "candidate.edited", func(m *domain.Mission) error {
		if _, err := e.candidate(*m); err != nil {
			return err
		}
		edits := []workspace.Edit{}
		allowed := map[string]bool{}
		for _, p := range files {
			allowed[p] = true
		}
		for _, edit := range out.Edits {
			if edit.Content == nil || !allowed[edit.Path] {
				return errors.New("edit missing content or outside exact step files")
			}
			edits = append(edits, workspace.Edit{Path: edit.Path, Content: *edit.Content})
		}
		changes, err := workspace.Apply(filepath.Join(e.dir(m.ID), "candidate"), edits, m.Spec.Scope, m.Spec.Protected)
		if err != nil {
			return err
		}
		digest, err := workspace.Digest(filepath.Join(e.dir(m.ID), "candidate"))
		if err != nil {
			return err
		}
		m.Changes = append(m.Changes, changes...)
		m.CandidateDigest = digest
		return nil
	})
	return err
}
func ordered(steps []domain.Step) []domain.Step {
	out := []domain.Step{}
	done := map[string]bool{}
	for len(out) < len(steps) {
		progress := false
		for _, s := range steps {
			if done[s.ID] {
				continue
			}
			ready := true
			for _, dep := range s.DependsOn {
				ready = ready && done[dep]
			}
			if ready {
				out = append(out, s)
				done[s.ID] = true
				progress = true
			}
		}
		if !progress {
			return nil
		}
	}
	return out
}
func (e *Engine) execution(op *operation) error {
	m, err := e.s.GetMission(op.id)
	if err != nil {
		return err
	}
	steps := ordered(m.Plan.Steps)
	if len(steps) != len(m.Plan.Steps) {
		return errors.New("invalid plan dependency graph")
	}
	allFiles := []string{}
	for _, step := range steps {
		allFiles = append(allFiles, step.Files...)
		var out editOutput
		if err := e.call(op, domain.Worker, step, &out); err != nil {
			return err
		}
		if err := e.apply(op, out, step.Files); err != nil {
			return err
		}
		if _, err = e.update(op, "step.completed", func(m *domain.Mission) error {
			for i := range m.Plan.Steps {
				if m.Plan.Steps[i].ID == step.ID {
					m.Plan.Steps[i].Status = "done"
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for round := 0; round <= 2; round++ {
		passed, err := e.verify(op)
		if err != nil {
			return err
		}
		if passed {
			break
		}
		if round == 2 {
			return errors.New("acceptance failed after maximum two repair rounds")
		}
		var out editOutput
		if err := e.call(op, domain.Debugger, map[string]int{"repair_round": round + 1}, &out); err != nil {
			return err
		}
		if err := e.apply(op, out, allFiles); err != nil {
			return err
		}
	}
	for _, role := range []domain.Role{domain.QA, domain.Auditor} {
		var out reviewOutput
		if err := e.call(op, role, nil, &out); err != nil {
			return err
		}
		m, err = e.s.GetMission(op.id)
		if err != nil {
			return err
		}
		if err := validateReview(out, m); err != nil {
			return e.denyModelValidation(op, "invalid_review", err)
		}
		_, err = e.update(op, "review.completed", func(m *domain.Mission) error {
			digest, err := e.candidate(*m)
			if err != nil {
				return err
			}
			m.Reviews = append(m.Reviews, domain.Review{Role: role, Passed: *out.Passed, Summary: out.Summary, Findings: out.Findings.domainFindings(), CandidateDigest: digest})
			return nil
		})
		if err != nil {
			return err
		}
		if !*out.Passed {
			return errors.New("independent review blocked delivery")
		}
	}
	_, err = e.update(op, "delivery.ready", func(m *domain.Mission) error {
		digest, err := e.candidate(*m)
		if err != nil {
			return err
		}
		m.State = "awaiting_delivery"
		m.Stage = "deliver"
		return domain.CanDeliver(*m, digest)
	})
	return err
}
func (e *Engine) verify(op *operation) (bool, error) {
	m, err := e.update(op, "verification.started", func(m *domain.Mission) error {
		if _, err := e.candidate(*m); err != nil {
			return err
		}
		m.State = "verifying"
		m.Stage = "verify"
		return nil
	})
	if err != nil {
		return false, err
	}
	id, err := opaque()
	if err != nil {
		return false, err
	}
	v := domain.Verification{ID: id, CandidateDigest: m.CandidateDigest, ImageID: m.Spec.ImageID, CreatedAt: now(), AcceptanceRevision: m.Plan.Revision, Passed: true, Results: []domain.CommandResult{}}
	var infrastructureError error
	for _, command := range m.Spec.Acceptance {
		candidate, err := e.selectedCandidate(m)
		if err != nil {
			return false, err
		}
		result, err := e.runner.Run(op.ctx, m.ID, candidate, m.Spec.ImageID, command)
		if op.ctx.Err() != nil {
			return false, op.ctx.Err()
		}
		if result.Command != command {
			return false, errors.New("runner returned mismatched command")
		}
		if err != nil {
			v.Passed = false
			result.ExitCode = -1
			infrastructureError = errors.New("sandbox infrastructure failed; resolve isolation/cleanup before replanning")
		}
		if result.ExitCode != 0 || result.TimedOut {
			v.Passed = false
		}
		v.Results = append(v.Results, result)
		if infrastructureError != nil {
			break
		}
	}
	_, err = e.update(op, "verification.completed", func(m *domain.Mission) error {
		digest, err := e.candidate(*m)
		if err != nil {
			return err
		}
		if digest != v.CandidateDigest {
			return errors.New("candidate changed during verification")
		}
		m.Evidence = append(m.Evidence, v)
		return nil
	})
	if err == nil {
		err = infrastructureError
	}
	return v.Passed, err
}
func (e *Engine) Deliver(ctx context.Context, id string, revision int64) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.checked(id, revision)
	if err != nil {
		return m, err
	}
	if e.active != nil {
		return m, errors.New("another operation is active")
	}
	if domain.UsesGenerations(m.WorkspaceMode) && m.State == "delivered" {
		if _, err = e.selectedCandidate(m); err != nil {
			return m, err
		}
		return m, e.verifyDelivery(m)
	}
	digest, err := e.candidate(m)
	if err != nil {
		return m, err
	}
	if err = domain.CanDeliver(m, digest); err != nil {
		return m, err
	}
	if domain.UsesGenerations(m.WorkspaceMode) {
		return e.deliverGeneration(ctx, m)
	}
	return e.s.UpdateMission(id, revision, "delivery.exported", func(m *domain.Mission) error {
		patch, report, err := workspace.Export(ctx, e.dir(id), marshalReport(*m))
		if err != nil {
			return err
		}
		m.State = "delivered"
		m.Stage = "deliver"
		m.Delivery = &domain.Delivery{PatchPath: patch, ReportPath: report, CandidateDigest: digest, CreatedAt: now()}
		m.Approvals = append(m.Approvals, domain.Approval{Kind: "deliver", Digest: digest, Revision: revision, CreatedAt: now()})
		return nil
	})
}
func (e *Engine) Chat(ctx context.Context, id string, revision int64, message string) (domain.Mission, error) {
	e.mu.Lock()
	m, err := e.checked(id, revision)
	if err != nil {
		e.mu.Unlock()
		return m, err
	}
	if e.active != nil {
		e.mu.Unlock()
		return m, errors.New("chat disabled during active operation")
	}
	if err = e.preflightModelRoles(ctx, m, domain.Main); err != nil {
		e.mu.Unlock()
		return m, err
	}
	if m.State == "draft" || len(message) == 0 || len(message) > 8000 {
		e.mu.Unlock()
		return m, errors.New("authorized context and bounded message required")
	}
	authorized := false
	for _, a := range m.Approvals {
		authorized = authorized || a.Kind == "context"
	}
	if !authorized {
		e.mu.Unlock()
		return m, errors.New("context share not approved")
	}
	var output struct {
		Answer string `json:"answer"`
	}
	e.start(m, func(op *operation) error {
		if err := e.call(op, domain.Main, map[string]string{"question": message}, &output); err != nil {
			return err
		}
		if output.Answer == "" {
			return e.denyModelValidation(op, "empty_main_response", errors.New("empty main response"))
		}
		_, err := e.update(op, "chat.completed", func(m *domain.Mission) error {
			m.Messages = append(m.Messages, domain.Message{Role: "user", Content: message, CreatedAt: now()}, domain.Message{Role: "main", Content: output.Answer, CreatedAt: now()})
			return nil
		})
		return err
	})
	op := e.active
	e.mu.Unlock()
	<-op.done
	return e.s.GetMission(id)
}
