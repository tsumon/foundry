package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"foundry/internal/domain"
	"foundry/internal/mcp"
)

func (s *Store) RegisterMCPProposalSource(missionID, modelInvocationID string, expected int64) (domain.Mission, domain.MCPProposalSource, error) {
	zeroM, zeroS := domain.Mission{}, domain.MCPProposalSource{}
	tx, err := s.db.Begin()
	if err != nil {
		return zeroM, zeroS, err
	}
	defer tx.Rollback()
	m, err := readMissionRow(tx, missionID)
	if err != nil {
		return zeroM, zeroS, err
	}
	if old, err := readMCPRecord[domain.MCPProposalSource](tx, "mcp_proposal_sources", "model_invocation_id", modelInvocationID, missionID); err == nil {
		return m, old, tx.Commit()
	} else if !errors.Is(err, ErrNotFound) {
		return zeroM, zeroS, err
	}
	v, err := readInvocation(tx, modelInvocationID)
	if err != nil {
		return zeroM, zeroS, err
	}
	if m.Revision != expected || v.MissionID != missionID || v.Status != "authorized_pending" || mcpCheckpointBlocked(m.Stage) {
		return zeroM, zeroS, ErrConflict
	}
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return zeroM, zeroS, ErrConflict
	}
	source := domain.MCPProposalSource{Version: 1, MissionID: missionID, ModelInvocationID: modelInvocationID, Purpose: researchSourcePurpose(m), Epoch: v.Epoch, SessionID: v.SessionID, ReadGrantID: v.GrantID, BindingDigest: v.BindingDigest, AuthorityDigest: v.AuthorityDigest, CreatedAt: timestamp()}
	if _, err := validateMCPSource(tx, m, source, false); err != nil {
		return zeroM, zeroS, err
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return zeroM, zeroS, err
	}
	if _, err := tx.Exec(`INSERT INTO mcp_proposal_sources(model_invocation_id,mission_id,version,epoch,session_id,data,digest) VALUES(?,?,?,?,?,?,?)`, source.ModelInvocationID, source.MissionID, 1, source.Epoch, source.SessionID, raw, mcpHash(raw)); err != nil {
		return zeroM, zeroS, constraintConflict(err)
	}
	m, err = updateMissionTx(tx, missionID, expected, "mcp.source_registered", func(m *domain.Mission) error { m.Stage = "mcp_research_turn"; return nil })
	if err != nil {
		return zeroM, zeroS, err
	}
	return m, source, tx.Commit()
}

func (s *Store) CreateMCPProposal(p domain.MCPProposal, expected int64) (domain.Mission, domain.MCPProposal, error) {
	zeroM, zeroP := domain.Mission{}, domain.MCPProposal{}
	if !safeLedgerText(p.ID, 256) || !safeLedgerText(p.MissionID, 256) || !safeLedgerText(p.SourceInvocationID, 256) {
		return zeroM, zeroP, errors.New("invalid MCP proposal identity")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zeroM, zeroP, err
	}
	defer tx.Rollback()
	m, err := readMissionRow(tx, p.MissionID)
	if err != nil {
		return zeroM, zeroP, err
	}
	if existing, err := readMCPRecord[domain.MCPProposal](tx, "mcp_call_proposals", "id", p.ID, p.MissionID); err == nil {
		if existing.SourceInvocationID != p.SourceInvocationID || !sameJSON(existing.SourceResult, p.SourceResult) || existing.ToolID != p.ToolID || !sameJSON(existing.Arguments, p.Arguments) {
			return zeroM, zeroP, ErrConflict
		}
		if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
			a, err := readResearchApplication(tx, m.ID, p.SourceInvocationID)
			if err != nil {
				return zeroM, zeroP, err
			}
			if a.Kind != "mcp" || a.TargetID != p.ID {
				return zeroM, zeroP, ErrConflict
			}
		}
		return m, existing, tx.Commit()
	} else if !errors.Is(err, ErrNotFound) {
		return zeroM, zeroP, err
	}
	if m.Revision != expected || m.State != "planning" || (mcpCheckpointBlocked(m.Stage) && m.Stage != "awaiting_mcp_materialization") {
		return zeroM, zeroP, ErrConflict
	}
	source, err := readMCPRecord[domain.MCPProposalSource](tx, "mcp_proposal_sources", "model_invocation_id", p.SourceInvocationID, m.ID)
	if err != nil {
		return zeroM, zeroP, err
	}
	v, err := validateMCPSource(tx, m, source, true)
	if err != nil {
		return zeroM, zeroP, err
	}
	if !sameJSON(v.Result, p.SourceResult) {
		return zeroM, zeroP, ErrConflict
	}
	tool, bound, err := currentMCPTool(tx, m, p.ToolID)
	if err != nil {
		return zeroM, zeroP, err
	}
	canonical, err := mcp.ValidateArguments(tool, bound.Policy, p.Arguments)
	if err != nil {
		return zeroM, zeroP, err
	}
	p.Version = 1
	p.Epoch = source.Epoch
	p.SessionID = source.SessionID
	p.ReadGrantID = source.ReadGrantID
	p.BindingDigest = source.BindingDigest
	p.AuthorityDigest = source.AuthorityDigest
	p.Arguments = canonical
	p.CreatedAt = timestamp()
	p.Digest = proposalDigest(p)
	if err := validateMCPProposal(tx, m, p); err != nil {
		return zeroM, zeroP, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return zeroM, zeroP, err
	}
	if _, err = tx.Exec(`INSERT INTO mcp_call_proposals(id,mission_id,version,epoch,session_id,source_invocation_id,data,digest) VALUES(?,?,?,?,?,?,?,?)`, p.ID, p.MissionID, 1, p.Epoch, p.SessionID, p.SourceInvocationID, raw, mcpHash(raw)); err != nil {
		return zeroM, zeroP, constraintConflict(err)
	}
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		a := domain.ResearchTurnApplication{MissionID: m.ID, Epoch: p.Epoch, SessionID: p.SessionID, SourceInvocationID: p.SourceInvocationID, SourceResult: p.SourceResult, Kind: "mcp", TargetID: p.ID}
		if err := validateNewResearchApplication(tx, m, a); err != nil {
			return zeroM, zeroP, err
		}
		if _, err := insertResearchApplication(tx, a); err != nil {
			return zeroM, zeroP, err
		}
	}
	m, err = updateMissionTx(tx, m.ID, expected, "mcp.proposal_created", func(m *domain.Mission) error { m.Stage = "awaiting_tool_approval"; return nil })
	if err != nil {
		return zeroM, zeroP, err
	}
	return m, p, tx.Commit()
}

func (s *Store) DecideMCPProposal(missionID, proposalID, digest, requestID string, expected int64, approve bool) (domain.Mission, domain.MCPCallGrant, domain.MCPInvocation, error) {
	zeroM, zeroG, zeroV := domain.Mission{}, domain.MCPCallGrant{}, domain.MCPInvocation{}
	if !safeLedgerText(requestID, 256) || !lowerHex(digest, 64) {
		return zeroM, zeroG, zeroV, ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zeroM, zeroG, zeroV, err
	}
	defer tx.Rollback()
	var oldProposal, oldDigest, grantID, invocationID string
	var oldExpected int64
	var oldApprove bool
	var version int
	err = tx.QueryRow(`SELECT version,proposal_id,proposal_digest,expected_revision,approve,grant_id,invocation_id FROM mcp_call_decisions WHERE mission_id=? AND request_id=?`, missionID, requestID).Scan(&version, &oldProposal, &oldDigest, &oldExpected, &oldApprove, &grantID, &invocationID)
	if err == nil {
		if version != 1 {
			return zeroM, zeroG, zeroV, ErrMCPFutureVersion
		}
		if proposalID != oldProposal || digest != oldDigest || expected != oldExpected || approve != oldApprove {
			return zeroM, zeroG, zeroV, ErrConflict
		}
		m, err := readMissionRow(tx, missionID)
		if err != nil {
			return zeroM, zeroG, zeroV, err
		}
		if !approve {
			if grantID != "" || invocationID != "" {
				return zeroM, zeroG, zeroV, errors.New("corrupt MCP rejection")
			}
			return m, zeroG, zeroV, tx.Commit()
		}
		grant, err := readMCPRecord[domain.MCPCallGrant](tx, "mcp_call_grants", "id", grantID, missionID)
		if err != nil {
			return zeroM, zeroG, zeroV, err
		}
		v, err := readMCPInvocation(tx, missionID, invocationID)
		if err != nil {
			return zeroM, zeroG, zeroV, err
		}
		if grant.ProposalID != proposalID || grant.ProposalDigest != digest || grant.RequestID != requestID || grant.ExpectedRevision != expected || v.GrantID != grant.ID || v.ProposalID != proposalID {
			return zeroM, zeroG, zeroV, errors.New("corrupt MCP decision linkage")
		}
		return m, grant, v, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zeroM, zeroG, zeroV, err
	}
	m, err := readMissionRow(tx, missionID)
	if err != nil {
		return zeroM, zeroG, zeroV, err
	}
	if m.Revision != expected || m.State != "planning" || m.Stage != "awaiting_tool_approval" {
		return zeroM, zeroG, zeroV, ErrConflict
	}
	p, err := readMCPRecord[domain.MCPProposal](tx, "mcp_call_proposals", "id", proposalID, missionID)
	if err != nil {
		return zeroM, zeroG, zeroV, err
	}
	if p.Digest != digest {
		return zeroM, zeroG, zeroV, ErrConflict
	}
	if err := validateMCPProposal(tx, m, p); err != nil {
		return zeroM, zeroG, zeroV, err
	}
	grant, v := zeroG, zeroV
	if approve {
		if err := mcpToolQuota(tx, missionID); err != nil {
			return zeroM, zeroG, zeroV, err
		}
		identity := mcpHash([]byte(missionID + "\x00" + requestID))
		grantID = "mcp-grant-" + identity
		invocationID = "mcp-call-" + identity
		grant = domain.MCPCallGrant{Version: 1, ID: grantID, MissionID: missionID, ProposalID: p.ID, ProposalDigest: p.Digest, RequestID: requestID, ExpectedRevision: expected, Epoch: p.Epoch, SessionID: p.SessionID, BindingDigest: p.BindingDigest, AuthorityDigest: p.AuthorityDigest, Binding: *m.MCPBindings, Profiles: m.Profiles, CreatedAt: timestamp()}
		raw, err := json.Marshal(grant)
		if err != nil {
			return zeroM, zeroG, zeroV, err
		}
		if _, err := tx.Exec(`INSERT INTO mcp_call_grants(id,mission_id,version,epoch,session_id,proposal_id,data,digest) VALUES(?,?,?,?,?,?,?,?)`, grant.ID, missionID, 1, grant.Epoch, grant.SessionID, p.ID, raw, mcpHash(raw)); err != nil {
			return zeroM, zeroG, zeroV, constraintConflict(err)
		}
		v = domain.MCPInvocation{Version: 1, ID: invocationID, MissionID: missionID, Epoch: p.Epoch, SessionID: p.SessionID, ProposalID: p.ID, GrantID: grant.ID, BindingDigest: p.BindingDigest, AuthorityDigest: p.AuthorityDigest, ReservedResultBytes: 64 << 10, Status: "authorized_pending", Phase: "preflight", CreatedAt: grant.CreatedAt}
		if err := tx.QueryRow(`SELECT COALESCE(MAX(ordinal),0)+1 FROM mcp_invocations WHERE mission_id=?`, missionID).Scan(&v.Ordinal); err != nil {
			return zeroM, zeroG, zeroV, err
		}
		raw, err = json.Marshal(v)
		if err != nil {
			return zeroM, zeroG, zeroV, err
		}
		if _, err = tx.Exec(`INSERT INTO mcp_invocations(id,mission_id,version,epoch,session_id,ordinal,proposal_id,grant_id,intent,digest,reserved_result_bytes,status,phase) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ID, missionID, 1, v.Epoch, v.SessionID, v.Ordinal, v.ProposalID, v.GrantID, raw, mcpHash(raw), v.ReservedResultBytes, v.Status, v.Phase); err != nil {
			return zeroM, zeroG, zeroV, constraintConflict(err)
		}
		if err := mcpTransition(tx, v, "", v.Status); err != nil {
			return zeroM, zeroG, zeroV, err
		}
	}
	if _, err = tx.Exec(`INSERT INTO mcp_call_decisions(mission_id,request_id,version,proposal_id,proposal_digest,expected_revision,approve,grant_id,invocation_id) VALUES(?,?,?,?,?,?,?,?,?)`, missionID, requestID, 1, proposalID, digest, expected, approve, grantID, invocationID); err != nil {
		return zeroM, zeroG, zeroV, constraintConflict(err)
	}
	m, err = updateMissionTx(tx, missionID, expected, "mcp.proposal_decided", func(m *domain.Mission) error {
		if approve {
			m.Stage = "mcp_authorized_pending"
		} else {
			m.State = "blocked"
			m.Stage = "mcp_rejected"
			m.Error = "MCP proposal rejected"
		}
		return nil
	})
	if err != nil {
		return zeroM, zeroG, zeroV, err
	}
	return m, grant, v, tx.Commit()
}

func (s *Store) GetMCPProposalSource(missionID, id string) (domain.MCPProposalSource, error) {
	return readResearchSource(s.db, missionID, id)
}
func (s *Store) GetMCPProposal(missionID, id string) (domain.MCPProposal, error) {
	return readMCPRecord[domain.MCPProposal](s.db, "mcp_call_proposals", "id", id, missionID)
}
func (s *Store) GetMCPCallGrant(missionID, id string) (domain.MCPCallGrant, error) {
	return readMCPRecord[domain.MCPCallGrant](s.db, "mcp_call_grants", "id", id, missionID)
}

func listMCPRecords[T any](s *Store, missionID, table, idColumn string) ([]T, error) {
	rows, err := s.db.Query(`SELECT `+idColumn+` FROM `+table+` WHERE mission_id=? ORDER BY `+idColumn, missionID)
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
		v, err := readMCPRecord[T](s.db, table, idColumn, id, missionID)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}
func (s *Store) ListMCPProposalSources(missionID string) ([]domain.MCPProposalSource, error) {
	return listResearchRecords(s, missionID, "mcp_proposal_sources", "model_invocation_id", readResearchSource)
}
func (s *Store) ListMCPProposals(missionID string) ([]domain.MCPProposal, error) {
	return listMCPRecords[domain.MCPProposal](s, missionID, "mcp_call_proposals", "id")
}
func (s *Store) ListMCPCallGrants(missionID string) ([]domain.MCPCallGrant, error) {
	return listMCPRecords[domain.MCPCallGrant](s, missionID, "mcp_call_grants", "id")
}
