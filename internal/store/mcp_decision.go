package store

import (
	"database/sql"
	"errors"
	"foundry/internal/domain"
)

// MCPDecision is a safe identity projection, not a grant or execution permit.
type MCPDecision struct {
	MissionID        string `json:"mission_id"`
	RequestID        string `json:"request_id"`
	ProposalID       string `json:"proposal_id"`
	ProposalDigest   string `json:"proposal_digest"`
	ExpectedRevision int64  `json:"expected_revision"`
	Approved         bool   `json:"approved"`
	InvocationID     string `json:"invocation_id,omitempty"`
	GrantID          string `json:"grant_id,omitempty"`
}

func (s *Store) GetMCPDecision(missionID, requestID string) (MCPDecision, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return MCPDecision{}, err
	}
	defer tx.Rollback()
	d, err := readMCPDecision(tx, missionID, requestID)
	if err != nil {
		return d, err
	}
	return d, tx.Commit()
}

// ListMCPDecisions returns all sessions in original decision revision order.
// The single read transaction and shared receipt checks prevent partial or
// unverified history, including rejections which have no invocation.
func (s *Store) ListMCPDecisions(missionID string) ([]MCPDecision, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = readMissionRow(tx, missionID); err != nil {
		return nil, err
	}
	if err = supportedMCPLedger(tx, missionID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT request_id FROM mcp_call_decisions WHERE mission_id=? ORDER BY expected_revision,request_id`, missionID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	out := []MCPDecision{}
	for _, id := range ids {
		d, err := readMCPDecision(tx, missionID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func readMCPDecision(tx *sql.Tx, missionID, requestID string) (MCPDecision, error) {
	d := MCPDecision{MissionID: missionID, RequestID: requestID}
	var err error
	_, err = readMissionRow(tx, missionID)
	if err != nil {
		return d, err
	}
	if err = supportedMCPLedger(tx, missionID); err != nil {
		return d, err
	}
	var version int
	err = tx.QueryRow(`SELECT version,proposal_id,proposal_digest,expected_revision,approve,grant_id,invocation_id FROM mcp_call_decisions WHERE mission_id=? AND request_id=?`, missionID, requestID).Scan(&version, &d.ProposalID, &d.ProposalDigest, &d.ExpectedRevision, &d.Approved, &d.GrantID, &d.InvocationID)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if version != 1 {
		return d, ErrMCPFutureVersion
	}
	p, err := readMCPRecord[domain.MCPProposal](tx, "mcp_call_proposals", "id", d.ProposalID, missionID)
	if err != nil {
		return d, err
	}
	if p.Digest != d.ProposalDigest || !safeLedgerText(requestID, 256) || d.ExpectedRevision < 1 {
		return d, errors.New("corrupt MCP decision")
	}
	source, err := readResearchSource(tx, missionID, p.SourceInvocationID)
	if err != nil {
		return d, err
	}
	vsource, err := readInvocation(tx, p.SourceInvocationID)
	if err != nil {
		return d, err
	}
	if source.Epoch != p.Epoch || source.SessionID != p.SessionID || source.BindingDigest != p.BindingDigest || source.AuthorityDigest != p.AuthorityDigest || vsource.Status != "succeeded" || vsource.Result == nil || *vsource.Result != p.SourceResult {
		return d, errors.New("corrupt decision source")
	}
	if source.Purpose == "research_turn_v2" {
		a, err := readResearchApplication(tx, missionID, source.ModelInvocationID)
		if err != nil {
			return d, err
		}
		if a.Kind != "mcp" || a.TargetID != p.ID {
			return d, errors.New("corrupt decision application")
		}
	}
	if !d.Approved {
		if d.GrantID != "" || d.InvocationID != "" {
			return d, errors.New("corrupt MCP rejection")
		}
	} else {
		g, err := readMCPRecord[domain.MCPCallGrant](tx, "mcp_call_grants", "id", d.GrantID, missionID)
		if err != nil {
			return d, err
		}
		v, err := readMCPInvocation(tx, missionID, d.InvocationID)
		if err != nil {
			return d, err
		}
		if g.ProposalID != p.ID || g.ProposalDigest != p.Digest || g.RequestID != requestID || g.ExpectedRevision != d.ExpectedRevision || v.GrantID != g.ID || v.ProposalID != p.ID {
			return d, errors.New("corrupt MCP decision linkage")
		}
	}
	return d, nil
}
