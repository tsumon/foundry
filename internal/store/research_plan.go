package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"foundry/internal/domain"
)

// ApplyResearchPlan closes the succeeded-Planner/apply crash gap locally. The
// Engine validates the sealed output, DAG and proposal digest before this call.
// No execution approval is granted here.
func (s *Store) ApplyResearchPlan(a domain.ResearchPlanApplication, expected int64) (domain.Mission, domain.ResearchPlanApplication, error) {
	zeroM, zeroA := domain.Mission{}, domain.ResearchPlanApplication{}
	if a.Version != 0 || a.CreatedAt != "" || a.Plan.Revision < 1 || a.Proposal.Kind != "plan_proposal" || !lowerHex(a.Proposal.Digest, 64) || a.Proposal.Revision != a.Plan.Revision || a.Proposal.PlanRevision != a.Plan.Revision {
		return zeroM, zeroA, ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zeroM, zeroA, err
	}
	defer tx.Rollback()
	m, err := readMissionRow(tx, a.MissionID)
	if err != nil {
		return zeroM, zeroA, err
	}
	if err := researchMission(tx, m); err != nil {
		return zeroM, zeroA, err
	}
	if old, err := readResearchPlan(tx, m.ID, a.PlannerInvocationID); err == nil {
		check := a
		check.Version, check.CreatedAt = old.Version, old.CreatedAt
		if !sameJSON(old, check) {
			return zeroM, zeroA, ErrConflict
		}
		return m, old, tx.Commit()
	} else if !errors.Is(err, ErrNotFound) {
		return zeroM, zeroA, err
	}
	checkpoint, err := researchPlannerByInvocation(tx, m.ID, a.PlannerInvocationID)
	if err != nil {
		return zeroM, zeroA, err
	}
	v, err := readInvocation(tx, a.PlannerInvocationID)
	if err != nil {
		return zeroM, zeroA, err
	}
	if m.Revision != expected || m.State != "planning" || m.Stage != "planner_ready" || m.Epoch != a.Epoch || checkpoint.Epoch != a.Epoch || checkpoint.SessionID != a.SessionID || v.Status != "succeeded" || v.Result == nil || !sameJSON(v.Result, a.PlannerResult) {
		return zeroM, zeroA, ErrConflict
	}
	if err := mcpRoleAuthority(m, v.BindingDigest, v.AuthorityDigest, domain.Planner); err != nil {
		return zeroM, zeroA, err
	}
	if m.Plan != nil && a.Plan.Revision <= m.Plan.Revision {
		return zeroM, zeroA, ErrConflict
	}
	canonical, digest, err := canonicalPlan(a.Plan)
	if err != nil {
		return zeroM, zeroA, err
	}
	a.Version, a.CreatedAt = 2, timestamp()
	raw, err := json.Marshal(a)
	if err != nil {
		return zeroM, zeroA, err
	}
	if _, err := tx.Exec(`INSERT INTO research_plan_applications(planner_invocation_id,mission_id,version,epoch,session_id,data,digest) VALUES(?,?,?,?,?,?,?)`, v.ID, m.ID, 2, a.Epoch, a.SessionID, raw, mcpHash(raw)); err != nil {
		return zeroM, zeroA, constraintConflict(err)
	}
	record := domain.PlanRevision{MissionID: m.ID, PlanRevision: canonical.Revision, Epoch: m.Epoch, MissionRevision: m.Revision + 1, Source: "planner", Plan: canonical, PlanDigest: digest, CreatedAt: a.CreatedAt}
	if err := insertPlanRevision(tx, record); err != nil {
		return zeroM, zeroA, err
	}
	m, err = updateMissionTx(tx, m.ID, m.Revision, "research.plan_applied", func(m *domain.Mission) error {
		for i := range canonical.Steps {
			canonical.Steps[i].Status = "pending"
		}
		m.Plan = &canonical
		m.State = "awaiting_approval"
		m.Stage = "awaiting_approval"
		m.Approvals = append(m.Approvals, a.Proposal)
		return nil
	})
	if err != nil {
		return zeroM, zeroA, err
	}
	return m, a, tx.Commit()
}

func researchPlannerByInvocation(q rowQuerier, missionID, id string) (domain.ResearchPlannerCheckpoint, error) {
	var source string
	err := q.QueryRow(`SELECT source_invocation_id FROM research_planner_checkpoints WHERE mission_id=? AND planner_invocation_id=?`, missionID, id).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ResearchPlannerCheckpoint{}, ErrNotFound
	}
	if err != nil {
		return domain.ResearchPlannerCheckpoint{}, err
	}
	return readResearchPlanner(q, missionID, source)
}

func readResearchPlan(q rowQuerier, missionID, id string) (domain.ResearchPlanApplication, error) {
	a, err := readResearchRecord[domain.ResearchPlanApplication](q, "research_plan_applications", "planner_invocation_id", id, missionID, "")
	if err != nil {
		return a, err
	}
	c, err := researchPlannerByInvocation(q, missionID, id)
	if err != nil {
		return a, err
	}
	v, err := readInvocation(q, id)
	if err != nil {
		return a, err
	}
	if a.Epoch != c.Epoch || a.SessionID != c.SessionID || v.Status != "succeeded" || v.Result == nil || !sameJSON(v.Result, a.PlannerResult) || a.Plan.Revision < 1 || a.Proposal.Kind != "plan_proposal" || a.Proposal.Revision != a.Plan.Revision || a.Proposal.PlanRevision != a.Plan.Revision || !lowerHex(a.Proposal.Digest, 64) {
		return a, errors.New("corrupt research plan linkage")
	}
	canonical, digest, err := canonicalPlan(a.Plan)
	if err != nil {
		return a, err
	}
	var raw []byte
	if err := q.QueryRow(`SELECT data FROM plan_revisions WHERE mission_id=? AND plan_revision=?`, missionID, a.Plan.Revision).Scan(&raw); err != nil {
		return a, err
	}
	var record domain.PlanRevision
	if decodeStoredJSON(raw, &record) != nil || record.MissionID != missionID || record.Epoch != a.Epoch || record.PlanRevision != a.Plan.Revision || record.Source != "planner" || record.PlanDigest != digest || !sameJSON(record.Plan, canonical) {
		return a, errors.New("research plan differs from immutable revision")
	}
	return a, nil
}

func (s *Store) GetResearchPlanApplication(missionID, plannerID string) (domain.ResearchPlanApplication, error) {
	return readResearchPlan(s.db, missionID, plannerID)
}
func (s *Store) ListResearchPlanApplications(missionID string) ([]domain.ResearchPlanApplication, error) {
	return listResearchRecords(s, missionID, "research_plan_applications", "planner_invocation_id", readResearchPlan)
}
