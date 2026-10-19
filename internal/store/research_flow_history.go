package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"

	"foundry/internal/domain"
)

// All names are private constants. Trusted SQL identity is checked separately
// from canonical immutable JSON; no malformed row yields partial history.
func readResearchRecord[T any](q rowQuerier, table, idColumn, id, missionID, extraColumns string, extraValues ...*string) (T, error) {
	var result T
	var owner, trustedID, session, digest string
	var version int
	var epoch int64
	var raw []byte
	columns := ""
	if extraColumns != "" {
		columns = "," + extraColumns
	}
	args := []any{&trustedID, &owner, &version, &epoch, &session, &raw, &digest}
	for _, v := range extraValues {
		args = append(args, v)
	}
	err := q.QueryRow(`SELECT `+idColumn+`,mission_id,version,epoch,session_id,data,digest`+columns+` FROM `+table+` WHERE `+idColumn+`=? AND mission_id=?`, id, missionID).Scan(args...)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if version != 2 {
		return result, ErrMCPFutureVersion
	}
	if mcpHash(raw) != digest || decodeStoredJSON(raw, &result) != nil {
		return result, errors.New("corrupt research record")
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if !bytes.Equal(canonical, raw) {
		return result, errors.New("noncanonical research record")
	}
	var h struct {
		MissionID string `json:"mission_id"`
		Version   int    `json:"version"`
		Epoch     int64  `json:"epoch"`
		SessionID string `json:"session_id"`
		CreatedAt string `json:"created_at"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &h) != nil || json.Unmarshal(raw, &fields) != nil || h.MissionID != owner || h.Version != version || h.Epoch != epoch || h.SessionID != session || !safeLedgerText(owner, 256) || !safeLedgerText(session, 256) || epoch < 1 || !validCatalogTime(h.CreatedAt) {
		return result, errors.New("research JSON differs from SQL owner")
	}
	var embeddedID string
	if json.Unmarshal(fields[idColumn], &embeddedID) != nil || embeddedID != trustedID || !safeLedgerText(embeddedID, 256) {
		return result, errors.New("research JSON differs from SQL identity")
	}
	return result, nil
}

func readResearchSource(q rowQuerier, missionID, id string) (domain.MCPProposalSource, error) {
	source, err := readMCPRecord[domain.MCPProposalSource](q, "mcp_proposal_sources", "model_invocation_id", id, missionID)
	if err != nil {
		return source, err
	}
	m, err := readMissionRow(q, missionID)
	if err != nil {
		return source, err
	}
	v, err := readInvocation(q, id)
	if err != nil {
		return source, err
	}
	g, err := readGrant(q, source.ReadGrantID)
	if err != nil {
		return source, err
	}
	if source.Purpose != researchSourcePurpose(m) || !isMCPMission(m) || v.MissionID != missionID || v.Epoch != source.Epoch || v.SessionID != source.SessionID || v.Kind != "model" || v.Role != domain.Researcher || v.GrantID != source.ReadGrantID || v.BindingDigest != source.BindingDigest || v.AuthorityDigest != source.AuthorityDigest || g.MissionID != missionID || g.Epoch != v.Epoch || g.SessionID != v.SessionID || g.Role != domain.Researcher || g.RequiredState != "planning" || g.BindingDigest != v.BindingDigest || g.AuthorityDigest != v.AuthorityDigest {
		return source, errors.New("corrupt research source ownership")
	}
	return source, nil
}

func readResearchApplication(q rowQuerier, missionID, id string) (domain.ResearchTurnApplication, error) {
	var kind, target string
	a, err := readResearchRecord[domain.ResearchTurnApplication](q, "research_turn_applications", "source_invocation_id", id, missionID, "kind,target_id", &kind, &target)
	if err != nil {
		return a, err
	}
	if a.Kind != kind || a.TargetID != target {
		return a, errors.New("research application target differs from SQL")
	}
	source, err := readResearchSource(q, missionID, id)
	if err != nil {
		return a, err
	}
	v, err := readInvocation(q, id)
	if err != nil {
		return a, err
	}
	if source.Purpose != "research_turn_v2" || a.Epoch != source.Epoch || a.SessionID != source.SessionID || v.Status != "succeeded" || v.Result == nil || !sameJSON(a.SourceResult, v.Result) {
		return a, errors.New("corrupt research application source")
	}
	switch a.Kind {
	case "final":
		if a.Research == nil || a.TargetID != "" || !safeLedgerText(a.Research.Summary, 16000) {
			return a, errors.New("corrupt final research")
		}
	case "repository":
		if a.Research != nil {
			return a, errors.New("unexpected research in repository application")
		}
		target, err := readInvocation(q, a.TargetID)
		if err != nil {
			return a, err
		}
		if target.Kind != "repository" || target.Role != domain.Researcher || target.MissionID != missionID || target.Epoch != a.Epoch || target.SessionID != a.SessionID || target.GrantID != source.ReadGrantID || target.BindingDigest != source.BindingDigest || target.AuthorityDigest != source.AuthorityDigest {
			return a, errors.New("corrupt repository application target")
		}
	case "mcp":
		if a.Research != nil {
			return a, errors.New("unexpected research in MCP application")
		}
		target, err := readMCPRecord[domain.MCPProposal](q, "mcp_call_proposals", "id", a.TargetID, missionID)
		if err != nil {
			return a, err
		}
		if target.SourceInvocationID != id || target.Epoch != a.Epoch || target.SessionID != a.SessionID || !sameJSON(target.SourceResult, a.SourceResult) || target.ReadGrantID != source.ReadGrantID || target.BindingDigest != source.BindingDigest || target.AuthorityDigest != source.AuthorityDigest || target.Digest != proposalDigest(target) {
			return a, errors.New("corrupt MCP application target")
		}
	default:
		return a, errors.New("invalid research application kind")
	}
	return a, nil
}

func validateResearchObservation(q rowQuerier, missionID string, epoch int64, session string, o domain.ResearchObservationRef) error {
	if !safeLedgerText(o.InvocationID, 256) {
		return ErrConflict
	}
	var applicationTarget string
	switch o.Kind {
	case "repository":
		v, err := readInvocation(q, o.InvocationID)
		if err != nil {
			return err
		}
		if v.MissionID != missionID || v.Epoch != epoch || v.SessionID != session || v.Kind != "repository" || v.Status != "succeeded" || v.Result == nil || !sameJSON(v.Result, o.Result) {
			return ErrConflict
		}
		applicationTarget = v.ID
	case "mcp":
		v, err := readMCPInvocation(q, missionID, o.InvocationID)
		if err != nil {
			return err
		}
		if v.Epoch != epoch || v.SessionID != session || (v.Status != "succeeded" && !(v.Status == "failed" && v.ErrorCode == "tool_error")) || v.Result == nil || !sameJSON(v.Result, o.Result) {
			return ErrConflict
		}
		p, err := readMCPRecord[domain.MCPProposal](q, "mcp_call_proposals", "id", v.ProposalID, missionID)
		if err != nil {
			return err
		}
		g, err := readMCPRecord[domain.MCPCallGrant](q, "mcp_call_grants", "id", v.GrantID, missionID)
		if err != nil {
			return err
		}
		if p.Epoch != v.Epoch || p.SessionID != v.SessionID || p.BindingDigest != v.BindingDigest || p.AuthorityDigest != v.AuthorityDigest || g.ProposalID != p.ID || g.ProposalDigest != p.Digest || g.Epoch != v.Epoch || g.SessionID != v.SessionID || g.BindingDigest != v.BindingDigest || g.AuthorityDigest != v.AuthorityDigest {
			return errors.New("corrupt research MCP observation authority")
		}
		applicationTarget = v.ProposalID
	default:
		return ErrConflict
	}
	var sourceID string
	err := q.QueryRow(`SELECT source_invocation_id FROM research_turn_applications WHERE mission_id=? AND epoch=? AND session_id=? AND kind=? AND target_id=?`, missionID, epoch, session, o.Kind, applicationTarget).Scan(&sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	_, err = readResearchApplication(q, missionID, sourceID)
	return err
}

func readResearchConsumption(q rowQuerier, missionID, id string) (domain.ResearchConsumption, error) {
	var kind, observationID string
	c, err := readResearchRecord[domain.ResearchConsumption](q, "research_consumptions", "consumer_invocation_id", id, missionID, "observation_kind,observation_invocation_id", &kind, &observationID)
	if err != nil {
		return c, err
	}
	if c.Observation.Kind != kind || c.Observation.InvocationID != observationID {
		return c, errors.New("research consumption differs from SQL observation")
	}
	if err := validateResearchObservation(q, missionID, c.Epoch, c.SessionID, c.Observation); err != nil {
		return c, err
	}
	source, err := readResearchSource(q, missionID, id)
	if err != nil {
		return c, err
	}
	if source.Purpose != "research_turn_v2" || source.Epoch != c.Epoch || source.SessionID != c.SessionID {
		return c, errors.New("corrupt research consumption owner")
	}
	return c, nil
}

func readResearchPlanner(q rowQuerier, missionID, id string) (domain.ResearchPlannerCheckpoint, error) {
	var plannerID string
	c, err := readResearchRecord[domain.ResearchPlannerCheckpoint](q, "research_planner_checkpoints", "source_invocation_id", id, missionID, "planner_invocation_id", &plannerID)
	if err != nil {
		return c, err
	}
	if c.PlannerInvocationID != plannerID {
		return c, errors.New("planner differs from SQL identity")
	}
	a, err := readResearchApplication(q, missionID, id)
	if err != nil {
		return c, err
	}
	v, err := readInvocation(q, plannerID)
	if err != nil {
		return c, err
	}
	if a.Kind != "final" || a.Epoch != c.Epoch || a.SessionID != c.SessionID || v.MissionID != missionID || v.Epoch != c.Epoch || v.SessionID != c.SessionID || v.Kind != "model" || v.Role != domain.Planner {
		return c, errors.New("corrupt research planner linkage")
	}
	return c, nil
}

func (s *Store) GetResearchTurnApplication(missionID, sourceID string) (domain.ResearchTurnApplication, error) {
	return readResearchApplication(s.db, missionID, sourceID)
}
func (s *Store) GetResearchConsumption(missionID, consumerID string) (domain.ResearchConsumption, error) {
	return readResearchConsumption(s.db, missionID, consumerID)
}
func (s *Store) GetResearchPlannerCheckpoint(missionID, sourceID string) (domain.ResearchPlannerCheckpoint, error) {
	return readResearchPlanner(s.db, missionID, sourceID)
}

func listResearchRecords[T any](s *Store, missionID, table, idColumn string, read func(rowQuerier, string, string) (T, error)) ([]T, error) {
	if _, err := s.GetMission(missionID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT `+idColumn+` FROM `+table+` WHERE mission_id=? ORDER BY rowid`, missionID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]T, 0, len(ids))
	for _, id := range ids {
		item, err := read(s.db, missionID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Store) ListResearchTurnApplications(missionID string) ([]domain.ResearchTurnApplication, error) {
	return listResearchRecords(s, missionID, "research_turn_applications", "source_invocation_id", readResearchApplication)
}
func (s *Store) ListResearchConsumptions(missionID string) ([]domain.ResearchConsumption, error) {
	return listResearchRecords(s, missionID, "research_consumptions", "consumer_invocation_id", readResearchConsumption)
}
func (s *Store) ListResearchPlannerCheckpoints(missionID string) ([]domain.ResearchPlannerCheckpoint, error) {
	return listResearchRecords(s, missionID, "research_planner_checkpoints", "source_invocation_id", readResearchPlanner)
}

func validateResearchClaim(q rowQuerier, v domain.Invocation) error {
	if v.Kind == "repository" {
		var source string
		err := q.QueryRow(`SELECT source_invocation_id FROM research_turn_applications WHERE mission_id=? AND kind='repository' AND target_id=?`, v.MissionID, v.ID).Scan(&source)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		_, err = readResearchApplication(q, v.MissionID, source)
		return err
	}
	if v.Role == domain.Planner {
		_, err := researchPlannerByInvocation(q, v.MissionID, v.ID)
		return err
	}
	if v.Role == domain.Researcher {
		var earlier int
		if err := q.QueryRow(`SELECT count(*) FROM invocations WHERE mission_id=? AND epoch=? AND session_id=? AND kind='model' AND role='researcher' AND ordinal<?`, v.MissionID, v.Epoch, v.SessionID, v.Ordinal).Scan(&earlier); err != nil {
			return err
		}
		if earlier > 0 {
			_, err := readResearchConsumption(q, v.MissionID, v.ID)
			return err
		}
	}
	return nil
}
