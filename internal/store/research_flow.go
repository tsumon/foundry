package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"foundry/internal/domain"
)

func researchSourcePurpose(m domain.Mission) string {
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return "research_turn_v2"
	}
	return "research_turn"
}

func insertResearchSource(tx *sql.Tx, m domain.Mission, v domain.Invocation) error {
	source := domain.MCPProposalSource{Version: 1, MissionID: m.ID, ModelInvocationID: v.ID, Purpose: researchSourcePurpose(m), Epoch: v.Epoch, SessionID: v.SessionID, ReadGrantID: v.GrantID, BindingDigest: v.BindingDigest, AuthorityDigest: v.AuthorityDigest, CreatedAt: timestamp()}
	if _, err := validateMCPSource(tx, m, source, false); err != nil {
		return err
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO mcp_proposal_sources(model_invocation_id,mission_id,version,epoch,session_id,data,digest) VALUES(?,?,?,?,?,?,?)`, v.ID, m.ID, 1, v.Epoch, v.SessionID, raw, mcpHash(raw))
	return constraintConflict(err)
}

// BeginResearchTurn reserves the paid call, source checkpoint and (except for
// the first turn) the newly consumed observation as one atomic transition.
// The controller verifies sealed input/result bytes before calling Store.
func (s *Store) BeginResearchTurn(v domain.Invocation, observation *domain.ResearchObservationRef, expected int64) (domain.Mission, domain.Invocation, error) {
	zeroM, zeroV := domain.Mission{}, domain.Invocation{}
	if err := validateNewInvocation(v); err != nil {
		return zeroM, zeroV, err
	}
	if v.Kind != "model" || v.Role != domain.Researcher {
		return zeroM, zeroV, ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zeroM, zeroV, err
	}
	defer tx.Rollback()
	m, err := readMissionRow(tx, v.MissionID)
	if err != nil {
		return zeroM, zeroV, err
	}
	if err := researchMission(tx, m); err != nil {
		return zeroM, zeroV, err
	}
	if old, err := readInvocation(tx, v.ID); err == nil {
		if !sameInvocationRequest(old, v) {
			return zeroM, zeroV, ErrConflict
		}
		if _, err := readResearchSource(tx, m.ID, v.ID); err != nil {
			return zeroM, zeroV, err
		}
		consumption, err := readResearchConsumption(tx, m.ID, v.ID)
		if observation == nil {
			if !errors.Is(err, ErrNotFound) {
				if err == nil {
					err = ErrConflict
				}
				return zeroM, zeroV, err
			}
		} else if err != nil || !sameJSON(consumption.Observation, *observation) {
			if err == nil {
				err = ErrConflict
			}
			return zeroM, zeroV, err
		}
		return m, old, tx.Commit()
	} else if !errors.Is(err, ErrNotFound) {
		return zeroM, zeroV, err
	}
	if m.State != "planning" || m.Epoch != v.Epoch || m.Revision != expected {
		return zeroM, zeroV, ErrConflict
	}
	var turns int
	if err := tx.QueryRow(`SELECT count(*) FROM mcp_proposal_sources WHERE mission_id=? AND epoch=? AND session_id=?`, m.ID, v.Epoch, v.SessionID).Scan(&turns); err != nil {
		return zeroM, zeroV, err
	}
	if (turns == 0) != (observation == nil) {
		return zeroM, zeroV, ErrConflict
	}
	if observation != nil {
		if err := validateResearchObservation(tx, m.ID, v.Epoch, v.SessionID, *observation); err != nil {
			return zeroM, zeroV, err
		}
	}
	var finalized bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM research_turn_applications WHERE mission_id=? AND epoch=? AND session_id=? AND kind='final')`, m.ID, v.Epoch, v.SessionID).Scan(&finalized); err != nil {
		return zeroM, zeroV, err
	}
	if finalized {
		return zeroM, zeroV, ErrConflict
	}
	m, v, err = beginInvocationTx(tx, v, expected, true)
	if err != nil {
		return zeroM, zeroV, err
	}
	if err := insertResearchSource(tx, m, v); err != nil {
		return zeroM, zeroV, err
	}
	if observation != nil {
		c := domain.ResearchConsumption{Version: 2, MissionID: m.ID, Epoch: v.Epoch, SessionID: v.SessionID, Observation: *observation, ConsumerInvocationID: v.ID, CreatedAt: timestamp()}
		raw, err := json.Marshal(c)
		if err != nil {
			return zeroM, zeroV, err
		}
		if _, err := tx.Exec(`INSERT INTO research_consumptions(consumer_invocation_id,mission_id,version,epoch,session_id,observation_kind,observation_invocation_id,data,digest) VALUES(?,?,?,?,?,?,?,?,?)`, v.ID, m.ID, 2, v.Epoch, v.SessionID, observation.Kind, observation.InvocationID, raw, mcpHash(raw)); err != nil {
			return zeroM, zeroV, constraintConflict(err)
		}
	}
	m, err = updateMissionTx(tx, m.ID, m.Revision, "research.turn_reserved", func(m *domain.Mission) error { m.Stage = "research_turn"; return nil })
	if err != nil {
		return zeroM, zeroV, err
	}
	return m, v, tx.Commit()
}

func researchMission(q rowQuerier, m domain.Mission) error {
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return err
	}
	if !domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) || !isMCPMission(m) {
		return ErrConflict
	}
	return supportedMCPLedger(q, m.ID)
}

// ApplyResearchTurn atomically reserves a repository target or installs the
// validated final brief. MCP applications are recorded by CreateMCPProposal.
func (s *Store) ApplyResearchTurn(a domain.ResearchTurnApplication, repository *domain.Invocation, expected int64) (domain.Mission, domain.ResearchTurnApplication, error) {
	zeroM, zeroA := domain.Mission{}, domain.ResearchTurnApplication{}
	if a.Version != 0 || a.CreatedAt != "" || (a.Kind != "repository" && a.Kind != "final") {
		return zeroM, zeroA, ErrConflict
	}
	if a.Kind == "repository" && (repository == nil || repository.ID != a.TargetID || repository.Kind != "repository" || repository.MissionID != a.MissionID || repository.Epoch != a.Epoch || repository.SessionID != a.SessionID) || a.Kind == "final" && repository != nil {
		return zeroM, zeroA, ErrConflict
	}
	if repository != nil {
		if err := validateNewInvocation(*repository); err != nil {
			return zeroM, zeroA, err
		}
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
	if old, err := readResearchApplication(tx, m.ID, a.SourceInvocationID); err == nil {
		check := a
		check.Version, check.CreatedAt = old.Version, old.CreatedAt
		if !sameJSON(old, check) {
			return zeroM, zeroA, ErrConflict
		}
		if repository != nil {
			v, err := readInvocation(tx, a.TargetID)
			if err != nil {
				return zeroM, zeroA, err
			}
			if !sameInvocationRequest(v, *repository) {
				return zeroM, zeroA, ErrConflict
			}
		}
		return m, old, tx.Commit()
	} else if !errors.Is(err, ErrNotFound) {
		return zeroM, zeroA, err
	}
	if m.Revision != expected || m.State != "planning" || m.Epoch != a.Epoch || mcpCheckpointBlocked(m.Stage) {
		return zeroM, zeroA, ErrConflict
	}
	if err := validateNewResearchApplication(tx, m, a); err != nil {
		return zeroM, zeroA, err
	}
	if repository != nil {
		source, err := readInvocation(tx, a.SourceInvocationID)
		if err != nil {
			return zeroM, zeroA, err
		}
		if repository.GrantID != source.GrantID || repository.BindingDigest != source.BindingDigest || repository.AuthorityDigest != source.AuthorityDigest {
			return zeroM, zeroA, ErrConflict
		}
		m, _, err = beginInvocationTx(tx, *repository, m.Revision, true)
		if err != nil {
			return zeroM, zeroA, err
		}
	}
	a, err = insertResearchApplication(tx, a)
	if err != nil {
		return zeroM, zeroA, err
	}
	m, err = updateMissionTx(tx, m.ID, m.Revision, "research.turn_applied", func(m *domain.Mission) error {
		if a.Kind == "final" {
			m.Research = a.Research
			m.Stage = "planner_ready"
		} else {
			m.Stage = "research_repository"
		}
		return nil
	})
	if err != nil {
		return zeroM, zeroA, err
	}
	return m, a, tx.Commit()
}

func validateNewResearchApplication(q rowQuerier, m domain.Mission, a domain.ResearchTurnApplication) error {
	source, err := readResearchSource(q, m.ID, a.SourceInvocationID)
	if err != nil {
		return err
	}
	v, err := validateMCPSource(q, m, source, true)
	if err != nil {
		return err
	}
	if a.Epoch != v.Epoch || a.SessionID != v.SessionID || !sameJSON(a.SourceResult, v.Result) {
		return ErrConflict
	}
	if a.Kind == "final" {
		if a.Research == nil || a.TargetID != "" || !safeLedgerText(a.Research.Summary, 16000) {
			return ErrConflict
		}
	} else {
		if (a.Kind != "repository" && a.Kind != "mcp") || a.Research != nil || !safeLedgerText(a.TargetID, 256) {
			return ErrConflict
		}
		var turns int
		if err := q.QueryRow(`SELECT count(*) FROM invocations WHERE mission_id=? AND epoch=? AND session_id=? AND role='researcher' AND kind='model'`, m.ID, v.Epoch, v.SessionID).Scan(&turns); err != nil {
			return err
		}
		if turns >= m.TaskBindings.Budget.MaxTurns {
			return errors.New("last Researcher turn must be final")
		}
	}
	return nil
}

func insertResearchApplication(tx *sql.Tx, a domain.ResearchTurnApplication) (domain.ResearchTurnApplication, error) {
	a.Version, a.CreatedAt = 2, timestamp()
	raw, err := json.Marshal(a)
	if err != nil {
		return domain.ResearchTurnApplication{}, err
	}
	_, err = tx.Exec(`INSERT INTO research_turn_applications(source_invocation_id,mission_id,version,epoch,session_id,kind,target_id,data,digest) VALUES(?,?,?,?,?,?,?,?,?)`, a.SourceInvocationID, a.MissionID, 2, a.Epoch, a.SessionID, a.Kind, a.TargetID, raw, mcpHash(raw))
	return a, constraintConflict(err)
}

func (s *Store) BeginResearchPlanner(v domain.Invocation, finalSourceID string, expected int64) (domain.Mission, domain.Invocation, error) {
	zeroM, zeroV := domain.Mission{}, domain.Invocation{}
	if err := validateNewInvocation(v); err != nil {
		return zeroM, zeroV, err
	}
	if v.Kind != "model" || v.Role != domain.Planner || v.GrantID != "" {
		return zeroM, zeroV, ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zeroM, zeroV, err
	}
	defer tx.Rollback()
	m, err := readMissionRow(tx, v.MissionID)
	if err != nil {
		return zeroM, zeroV, err
	}
	if err := researchMission(tx, m); err != nil {
		return zeroM, zeroV, err
	}
	if old, err := readResearchPlanner(tx, m.ID, finalSourceID); err == nil {
		stored, err := readInvocation(tx, old.PlannerInvocationID)
		if err != nil {
			return zeroM, zeroV, err
		}
		if !sameInvocationRequest(stored, v) {
			return zeroM, zeroV, ErrConflict
		}
		return m, stored, tx.Commit()
	} else if !errors.Is(err, ErrNotFound) {
		return zeroM, zeroV, err
	}
	a, err := readResearchApplication(tx, m.ID, finalSourceID)
	if err != nil {
		return zeroM, zeroV, err
	}
	if a.Kind != "final" || a.Epoch != v.Epoch || a.SessionID != v.SessionID || m.Stage != "planner_ready" || m.State != "planning" || !sameJSON(m.Research, a.Research) {
		return zeroM, zeroV, ErrConflict
	}
	m, v, err = beginInvocationTx(tx, v, expected, true)
	if err != nil {
		return zeroM, zeroV, err
	}
	c := domain.ResearchPlannerCheckpoint{Version: 2, MissionID: m.ID, Epoch: v.Epoch, SessionID: v.SessionID, SourceInvocationID: finalSourceID, PlannerInvocationID: v.ID, CreatedAt: timestamp()}
	raw, err := json.Marshal(c)
	if err != nil {
		return zeroM, zeroV, err
	}
	if _, err := tx.Exec(`INSERT INTO research_planner_checkpoints(source_invocation_id,planner_invocation_id,mission_id,version,epoch,session_id,data,digest) VALUES(?,?,?,?,?,?,?,?)`, finalSourceID, v.ID, m.ID, 2, v.Epoch, v.SessionID, raw, mcpHash(raw)); err != nil {
		return zeroM, zeroV, constraintConflict(err)
	}
	return m, v, tx.Commit()
}
