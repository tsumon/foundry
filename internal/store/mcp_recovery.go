package store

import (
	"errors"

	"foundry/internal/domain"
	"github.com/mattn/go-sqlite3"
)

// RecoverMCPInvocations is local-only. Structural source envelopes are checked
// here; Engine must independently verify sealed bytes before accepting the
// retained materialization/observation checkpoint. Nothing is dispatched.
func (s *Store) RecoverMCPInvocations(missionID string) error {
	m, err := readMissionRow(s.db, missionID)
	if err != nil {
		return err
	}
	if err = domain.ValidateMissionModelSnapshot(m); err != nil {
		return err
	}
	if !isMCPMission(m) {
		return nil
	}
	// A future row disables the whole mission's reconciliation without touching
	// that row or interpreting a newer schema as today's execution authority.
	if err := supportedMCPLedger(s.db, missionID); err != nil {
		if errors.Is(err, ErrMCPFutureVersion) {
			return nil
		}
		return err
	}
	sources, err := s.ListMCPProposalSources(missionID)
	if err != nil {
		return s.blockMCPCorruption(missionID, err)
	}
	proposals, err := s.ListMCPProposals(missionID)
	if err != nil {
		return s.blockMCPCorruption(missionID, err)
	}
	grants, err := s.ListMCPCallGrants(missionID)
	if err != nil {
		return s.blockMCPCorruption(missionID, err)
	}
	calls, err := s.ListMCPInvocations(missionID)
	if err != nil {
		return s.blockMCPCorruption(missionID, err)
	}
	bySource := map[string]domain.MCPProposalSource{}
	byProposal := map[string]domain.MCPProposal{}
	byGrant := map[string]domain.MCPCallGrant{}
	for _, source := range sources {
		v, err := s.GetInvocation(source.ModelInvocationID)
		if err != nil {
			return s.blockMCPCorruption(missionID, err)
		}
		if source.Purpose != researchSourcePurpose(m) || v.MissionID != missionID || v.Epoch != source.Epoch || v.SessionID != source.SessionID || v.Kind != "model" || v.Role != domain.Researcher || v.BindingDigest != source.BindingDigest || v.AuthorityDigest != source.AuthorityDigest || v.GrantID != source.ReadGrantID {
			return s.blockMCPCorruption(missionID, errors.New("source linkage"))
		}
		grant, err := s.GetReadGrant(source.ReadGrantID)
		if err != nil {
			return s.blockMCPCorruption(missionID, err)
		}
		if grant.MissionID != missionID || grant.Epoch != source.Epoch || grant.SessionID != source.SessionID || grant.BindingDigest != source.BindingDigest || grant.AuthorityDigest != source.AuthorityDigest {
			return s.blockMCPCorruption(missionID, errors.New("source read grant"))
		}
		bySource[source.ModelInvocationID] = source
	}
	for _, p := range proposals {
		source, ok := bySource[p.SourceInvocationID]
		v, err := s.GetInvocation(p.SourceInvocationID)
		if !ok || err != nil || v.Status != "succeeded" || v.Result == nil || !sameJSON(v.Result, p.SourceResult) || p.Epoch != source.Epoch || p.SessionID != source.SessionID || p.BindingDigest != source.BindingDigest || p.AuthorityDigest != source.AuthorityDigest || p.ReadGrantID != source.ReadGrantID || p.Digest != proposalDigest(p) {
			return s.blockMCPCorruption(missionID, errors.New("proposal source linkage"))
		}
		byProposal[p.ID] = p
	}
	for _, grant := range grants {
		p, ok := byProposal[grant.ProposalID]
		if !ok || grant.ProposalDigest != p.Digest || grant.Epoch != p.Epoch || grant.SessionID != p.SessionID || grant.BindingDigest != p.BindingDigest || grant.AuthorityDigest != p.AuthorityDigest {
			return s.blockMCPCorruption(missionID, errors.New("grant linkage"))
		}
		byGrant[grant.ID] = grant
	}
	for _, v := range calls {
		g, ok := byGrant[v.GrantID]
		if !ok || g.ProposalID != v.ProposalID || g.Epoch != v.Epoch || g.SessionID != v.SessionID || g.BindingDigest != v.BindingDigest || g.AuthorityDigest != v.AuthorityDigest {
			return s.blockMCPCorruption(missionID, errors.New("invocation linkage"))
		}
	}
	if err := s.checkMCPDecisionHistory(missionID, byProposal, byGrant, calls); err != nil {
		return s.blockMCPCorruption(missionID, err)
	}
	if err := s.checkMCPTransitionHistory(missionID, calls); err != nil {
		return s.blockMCPCorruption(missionID, err)
	}
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		if err := s.checkResearchHistory(missionID); err != nil {
			return s.blockMCPCorruption(missionID, err)
		}
	}
	unfinishedGeneric, err := s.HasUnfinishedInvocations(missionID)
	if err != nil {
		return err
	}
	active := unfinishedGeneric
	for _, v := range calls {
		if v.Status == "authorized_pending" || v.Status == "dispatching" {
			active = true
		}
	}
	if active {
		if m.State != "blocked" || m.Stage != "mcp_interrupted" {
			m, err = s.UpdateMission(missionID, m.Revision, "mcp.interrupted", func(m *domain.Mission) error {
				m.Epoch++
				m.State = "blocked"
				m.Stage = "mcp_interrupted"
				m.Error = "interrupted MCP planning; no calls were replayed"
				return nil
			})
			if err != nil {
				return err
			}
		}
		if err := s.RecoverInvocations(missionID); err != nil {
			return err
		}
		for _, v := range calls {
			if v.Status == "authorized_pending" || v.Status == "dispatching" {
				if err := s.recoverMCPCall(v); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// Read every generic row too, so malformed historical results cannot be
	// silently treated as an empty source ledger.
	if err := s.RecoverInvocations(missionID); err != nil {
		return err
	}
	m, err = readMissionRow(s.db, missionID)
	if err != nil {
		return err
	}
	if m.State != "planning" {
		return nil
	}
	// V2 has applications for all three tagged branches and the Planner, so it
	// must not enter ABv1's MCP-only source-materialization checkpoint. Engine
	// verifies sealed bytes and explicitly reconciles any missing application.
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return nil
	}
	if m.Stage == "awaiting_tool_approval" || m.Stage == "mcp_observation_ready" {
		return nil
	}
	var latest domain.Invocation
	for _, source := range sources {
		if source.Epoch != m.Epoch {
			continue
		}
		v, err := s.GetInvocation(source.ModelInvocationID)
		if err != nil {
			return s.blockMCPCorruption(missionID, err)
		}
		if v.Ordinal > latest.Ordinal {
			latest = v
		}
	}
	if latest.ID == "" || latest.Status != "succeeded" {
		return nil
	}
	for _, p := range proposals {
		if p.SourceInvocationID == latest.ID {
			return nil
		}
	}
	source := bySource[latest.ID]
	if _, err := validateMCPSource(s.db, m, source, true); err != nil {
		return s.blockMCPCorruption(missionID, err)
	}
	if m.Stage != "awaiting_mcp_materialization" {
		_, err = s.UpdateMission(missionID, m.Revision, "mcp.awaiting_materialization", func(m *domain.Mission) error { m.Stage = "awaiting_mcp_materialization"; return nil })
	}
	return err
}

func (s *Store) checkMCPDecisionHistory(missionID string, proposals map[string]domain.MCPProposal, grants map[string]domain.MCPCallGrant, calls []domain.MCPInvocation) error {
	byCall := map[string]domain.MCPInvocation{}
	for _, v := range calls {
		byCall[v.ID] = v
	}
	rows, err := s.db.Query(`SELECT request_id,proposal_id,proposal_digest,expected_revision,approve,grant_id,invocation_id FROM mcp_call_decisions WHERE mission_id=?`, missionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var requestID, proposalID, digest, grantID, invocationID string
		var expected int64
		var approve bool
		if err := rows.Scan(&requestID, &proposalID, &digest, &expected, &approve, &grantID, &invocationID); err != nil {
			return err
		}
		p, ok := proposals[proposalID]
		if !ok || p.Digest != digest || !safeLedgerText(requestID, 256) || expected < 1 {
			return errors.New("invalid MCP decision")
		}
		if approve {
			g, gok := grants[grantID]
			v, vok := byCall[invocationID]
			if !gok || !vok || g.RequestID != requestID || g.ExpectedRevision != expected || g.ProposalID != proposalID || v.GrantID != grantID || v.ProposalID != proposalID {
				return errors.New("invalid approved MCP decision")
			}
			seen[grantID] = true
		} else if grantID != "" || invocationID != "" {
			return errors.New("invalid MCP rejection")
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(seen) != len(grants) || len(grants) != len(calls) {
		return errors.New("orphan MCP authorization")
	}
	return nil
}

func (s *Store) blockMCPCorruption(missionID string, cause error) error {
	if errors.Is(cause, ErrMCPFutureVersion) {
		return nil
	}
	var databaseError sqlite3.Error
	if errors.As(cause, &databaseError) {
		return cause
	}
	m, err := readMissionRow(s.db, missionID)
	if err != nil {
		return err
	}
	if m.State == "blocked" && m.Stage == "mcp_corrupt" {
		return nil
	}
	_, err = s.UpdateMission(missionID, m.Revision, "mcp.corrupt", func(m *domain.Mission) error {
		m.Epoch++
		m.State = "blocked"
		m.Stage = "mcp_corrupt"
		m.Error = "MCP ledger is corrupt; execution remains disabled"
		return nil
	})
	return err
}

func (s *Store) recoverMCPCall(v domain.MCPInvocation) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	status := "interrupted_before_dispatch"
	if v.Status == "dispatching" {
		status = "interrupted_before_call"
		if v.Phase == "call_started" {
			status = "outcome_unknown"
		}
	}
	result, err := tx.Exec(`UPDATE mcp_invocations SET status=?,error_code=?,finished_at=? WHERE id=? AND mission_id=? AND status=? AND phase=?`, status, status+"_after_restart", timestamp(), v.ID, v.MissionID, v.Status, v.Phase)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	if err := mcpTransition(tx, v, v.Status, status); err != nil {
		return err
	}
	m, err := readMissionRow(tx, v.MissionID)
	if err != nil {
		return err
	}
	if err := insertEvent(tx, v.MissionID, "mcp.recovered", m.Revision, timestamp()); err != nil {
		return err
	}
	return tx.Commit()
}
