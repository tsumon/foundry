package store

import (
	"errors"
	"foundry/internal/domain"
)

// ApproveMissionExecution commits the execute seal and transition together with
// current availability checks for every execution role's exact frozen version.
// Engine validates filesystem/image inputs first; its earlier account preflight
// is not authoritative because account mutations use an independent mutex.
func (s *Store) ApproveMissionExecution(id string, expected int64, digest string) (domain.Mission, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Mission{}, err
	}
	defer tx.Rollback()
	m, err := updateMissionTx(tx, id, expected, "plan.approved", func(m *domain.Mission) error {
		if m.State != "awaiting_approval" || m.Plan == nil || digest == "" {
			return errors.New("mission is not awaiting execution approval")
		}
		if err := domain.ValidatePlanAdjustments(*m, *m.Plan); err != nil {
			return err
		}
		for _, a := range m.Plan.Adjustments {
			if a.Unresolved {
				return errors.New("unresolved failure blocks execution approval")
			}
		}
		proposal := ""
		for i := len(m.Approvals) - 1; i >= 0; i-- {
			if m.Approvals[i].Kind == "plan_proposal" {
				proposal = m.Approvals[i].Digest
				break
			}
		}
		if proposal != digest {
			return errors.New("plan approval inputs changed")
		}
		for _, role := range []domain.Role{domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
			if err := guardModelCallTx(tx, *m, role); err != nil {
				return err
			}
		}
		m.State = "running"
		m.Stage = "build"
		m.Epoch++
		m.Approvals = append(m.Approvals, domain.Approval{Kind: "execute", Digest: digest, Revision: expected, PlanRevision: m.Plan.Revision, CreatedAt: timestamp()})
		return nil
	})
	if err != nil {
		return domain.Mission{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Mission{}, err
	}
	return m, nil
}

func executionApprovals(m domain.Mission) []domain.Approval {
	var approvals []domain.Approval
	for _, a := range m.Approvals {
		if a.Kind == "execute" {
			approvals = append(approvals, a)
		}
	}
	return approvals
}
