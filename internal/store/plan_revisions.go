package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"foundry/internal/domain"
)

// RevisePlan appends historical records and reseals the current proposal, but
// cannot grant execution approval. The caller validates policy and the plan DAG
// before entering this transaction; mutateSeal may only append a proposal seal.
func (s *Store) RevisePlan(id string, expectedMission, expectedPlan int64, reason string, nextPlan domain.Plan, mutateSeal func(*domain.Mission) error) (domain.Mission, domain.PlanRevision, error) {
	zero, empty := domain.Mission{}, domain.PlanRevision{}
	if mutateSeal == nil || strings.TrimSpace(reason) == "" || len(reason) > 1000 || !utf8.ValidString(reason) {
		return zero, empty, errors.New("valid reason and proposal reseal are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, empty, err
	}
	defer tx.Rollback()
	var record domain.PlanRevision
	m, err := updateMissionTx(tx, id, expectedMission, "plan.revised", func(m *domain.Mission) error {
		if m.State != "awaiting_approval" || m.Revision != expectedMission || m.Plan == nil || m.Plan.Revision != expectedPlan {
			return ErrConflict
		}
		var active int
		if err := tx.QueryRow(`SELECT count(*) FROM local_operations WHERE mission_id=? AND status IN ('pending','prepared')`, id).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return ErrConflict
		}
		if err := domain.ValidatePlanAdjustments(*m, nextPlan); err != nil {
			return err
		}
		old, oldDigest, err := canonicalPlan(*m.Plan)
		if err != nil {
			return err
		}
		next, _, err := canonicalPlan(nextPlan)
		if err != nil {
			return err
		}
		next.Revision = expectedPlan
		if sameJSON(old, next) {
			return errors.New("plan revision must change the plan")
		}
		next.Revision = expectedPlan + 1
		if next.Revision <= expectedPlan {
			return errors.New("plan revision overflow")
		}
		next, digest, err := canonicalPlan(next)
		if err != nil {
			return err
		}
		now := timestamp()
		var existing []byte
		err = tx.QueryRow(`SELECT data FROM plan_revisions WHERE mission_id=? AND plan_revision=?`, id, expectedPlan).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			prior := domain.PlanRevision{MissionID: id, PlanRevision: expectedPlan, Epoch: m.Epoch, MissionRevision: m.Revision, Source: "planner", Plan: old, PlanDigest: oldDigest, CreatedAt: now}
			if err = insertPlanRevision(tx, prior); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			var prior domain.PlanRevision
			if json.Unmarshal(existing, &prior) != nil || prior.MissionID != id || prior.PlanRevision != expectedPlan || prior.Epoch != m.Epoch || prior.PlanDigest != oldDigest || !sameJSON(prior.Plan, old) {
				return errors.New("current plan differs from immutable history")
			}
		}
		record = domain.PlanRevision{MissionID: id, PlanRevision: next.Revision, ParentPlanRevision: expectedPlan, Epoch: m.Epoch, MissionRevision: m.Revision + 1, Source: "user", Reason: reason, Plan: next, PlanDigest: digest, CreatedAt: now}
		if err = insertPlanRevision(tx, record); err != nil {
			return err
		}
		current, _, err := canonicalPlan(next)
		if err != nil {
			return err
		}
		for i := range current.Steps {
			current.Steps[i].Status = "pending"
		}
		m.Plan = &current
		frozen, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if err = mutateSeal(m); err != nil {
			return err
		}
		var before domain.Mission
		if err = json.Unmarshal(frozen, &before); err != nil {
			return err
		}
		if len(m.Approvals) != len(before.Approvals)+1 || !sameJSON(m.Approvals[:len(before.Approvals)], before.Approvals) {
			return errors.New("reseal may only append a proposal")
		}
		seal := m.Approvals[len(m.Approvals)-1]
		if seal.Kind != "plan_proposal" || !lowerHex(seal.Digest, 64) || seal.Revision != current.Revision || (seal.PlanRevision != 0 && seal.PlanRevision != current.Revision) {
			return errors.New("invalid revised proposal seal")
		}
		// Only the new seal may differ. Byte comparison also protects nested
		// slices/maps against callback mutations of frozen inputs and evidence.
		check := *m
		check.Approvals = before.Approvals
		if !sameJSON(check, before) {
			return errors.New("reseal changed frozen mission inputs")
		}
		m.Approvals[len(m.Approvals)-1].CreatedAt = now
		return nil
	})
	if err != nil {
		return zero, empty, err
	}
	if err = tx.Commit(); err != nil {
		return zero, empty, err
	}
	return m, record, nil
}

func canonicalPlan(plan domain.Plan) (domain.Plan, string, error) {
	raw, err := json.Marshal(plan)
	if err != nil {
		return domain.Plan{}, "", err
	}
	var copy domain.Plan
	if err = json.Unmarshal(raw, &copy); err != nil {
		return domain.Plan{}, "", err
	}
	for i := range copy.Steps {
		copy.Steps[i].Status = ""
	}
	raw, err = json.Marshal(copy)
	if err != nil {
		return domain.Plan{}, "", err
	}
	return copy, sha256Text(string(raw)), nil
}

func insertPlanRevision(tx *sql.Tx, revision domain.PlanRevision) error {
	raw, err := json.Marshal(revision)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO plan_revisions(mission_id,plan_revision,data) VALUES(?,?,?)`, revision.MissionID, revision.PlanRevision, raw)
	return err
}

func (s *Store) ListPlanRevisions(id string) ([]domain.PlanRevision, error) {
	if _, err := s.GetMission(id); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT data FROM plan_revisions WHERE mission_id=? ORDER BY plan_revision`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.PlanRevision{}
	for rows.Next() {
		var raw []byte
		var revision domain.PlanRevision
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &revision); err != nil {
			return nil, err
		}
		result = append(result, revision)
	}
	return result, rows.Err()
}
