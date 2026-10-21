package engine

import (
	"errors"
	"foundry/internal/domain"
	"foundry/internal/workspace"
	"path/filepath"
	"reflect"
)

func archiveCodingRun(m domain.Mission) domain.RunArchive {
	revision := int64(0)
	if m.Plan != nil {
		revision = m.Plan.Revision
	}
	a := domain.RunArchive{Epoch: m.Epoch, PlanRevision: revision, Revision: m.Revision, ArchivedAt: now(), State: m.State, Error: m.Error, ImageID: m.Spec.ImageID, CandidateDigest: m.CandidateDigest, CandidateGeneration: m.CandidateGeneration, Plan: m.Plan, Research: m.Research, Changes: m.Changes, Evidence: m.Evidence, Reviews: m.Reviews, Delivery: m.Delivery}
	if m.CodingVersion == 1 && m.Plan != nil {
		for _, attempt := range m.CodingAttempts {
			if attempt.PlanRevision == m.Plan.Revision {
				a.CodingAttempts = append(a.CodingAttempts, attempt)
				a.Epoch = attempt.Epoch
			}
		}
	}
	return a
}

func (e *Engine) freezeReplan(m domain.Mission) (*domain.RunArchive, *domain.FailureBundle, error) {
	return e.freezeReplanWith(m, e.s.GetLocalOperation)
}

func (e *Engine) freezeReplanWith(m domain.Mission, readOperation func(string) (domain.LocalOperation, error)) (*domain.RunArchive, *domain.FailureBundle, error) {
	if m.State == "draft" {
		return nil, nil, nil
	}
	a := archiveCodingRun(m)
	if m.CodingVersion == 0 {
		return &a, nil, nil
	}
	if err := e.checkReplanMaterial(m); err != nil {
		return nil, nil, err
	}
	if len(a.CodingAttempts) == 0 {
		// An explicitly retried interrupted Planner does not invent execution facts.
		if len(a.Evidence) > 0 || len(a.Reviews) > 0 || len(a.Changes) > 0 {
			return nil, nil, errors.New("execution material has no controller trace")
		}
		return &a, m.ReplanContext, nil
	}
	last := a.CodingAttempts[len(a.CodingAttempts)-1]
	if last.Status == "interrupted" && last.After == nil && !reflect.DeepEqual(last.Before, a.CandidateGeneration) {
		if a.CandidateGeneration == nil {
			return nil, nil, errors.New("missing recovered candidate")
		}
		op, err := readOperation(a.CandidateGeneration.ID)
		if err != nil {
			return nil, nil, err
		}
		a.RecoveredSelection = &op
		if err := domain.ValidateRecoveredSelection(m, a); err != nil {
			return nil, nil, err
		}
		// The selected generation is not sufficient evidence on its own. Check
		// frozen authority and the complete on-disk recorded edit chain as well.
		if err := e.checkCodingReviewMaterial(m); err != nil {
			return nil, nil, err
		}
		approval, err := authority(m, filepath.Join(e.dir(m.ID), "base"), true)
		if err != nil || approval != op.ApprovalDigest {
			return nil, nil, errors.New("recovered selection approval mismatch")
		}
		if err := e.checkRecoveredSelection(m, a); err != nil {
			return nil, nil, err
		}
	}
	b, err := domain.BuildFailureBundle(m, a)
	return &a, b, err
}

func (e *Engine) checkReplanMaterial(m domain.Mission) error {
	if err := domain.ValidateReplanContext(m); err != nil {
		return err
	}
	if m.ReplanContext != nil {
		if err := workspace.VerifyGeneration(e.dir(m.ID), m.ReplanContext.CandidateGeneration); err != nil {
			return err
		}
		for _, a := range m.RunArchives {
			if a.Epoch == m.ReplanContext.Epoch && a.RecoveredSelection != nil {
				if err := e.checkRecoveredSelection(m, a); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (e *Engine) checkRecoveredSelection(m domain.Mission, a domain.RunArchive) error {
	if err := domain.ValidateRecoveredSelection(m, a); err != nil {
		return err
	}
	op := a.RecoveredSelection
	base, err := workspace.Digest(filepath.Join(e.dir(m.ID), "base"))
	if err != nil || base != op.BaseDigest {
		return errors.New("recovered selection base changed")
	}
	if err := workspace.VerifyGeneration(e.dir(m.ID), op.Before); err != nil {
		return err
	}
	return workspace.VerifyGenerationParent(e.dir(m.ID), *op.Result.Generation, op.Before)
}
