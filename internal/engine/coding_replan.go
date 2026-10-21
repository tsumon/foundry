package engine

import (
	"errors"
	"foundry/internal/domain"
	"foundry/internal/workspace"
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
	}
	return nil
}
