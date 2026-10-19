package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"foundry/internal/domain"
)

const mcpInvocationSelect = `SELECT id,mission_id,version,epoch,session_id,ordinal,proposal_id,grant_id,intent,digest,reserved_result_bytes,status,phase,result,error_code,requests,request_bytes,response_bytes,duration_millis,dispatched_at,finished_at FROM mcp_invocations`

func scanMCPInvocation(row rowScanner) (domain.MCPInvocation, error) {
	var v, trusted domain.MCPInvocation
	var raw, result []byte
	var digest string
	err := row.Scan(&trusted.ID, &trusted.MissionID, &trusted.Version, &trusted.Epoch, &trusted.SessionID, &trusted.Ordinal, &trusted.ProposalID, &trusted.GrantID, &raw, &digest, &trusted.ReservedResultBytes, &trusted.Status, &trusted.Phase, &result, &trusted.ErrorCode, &trusted.Requests, &trusted.RequestBytes, &trusted.ResponseBytes, &trusted.DurationMillis, &trusted.DispatchedAt, &trusted.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if trusted.Version != 1 {
		return v, ErrMCPFutureVersion
	}
	if mcpHash(raw) != digest || decodeStoredJSON(raw, &v) != nil {
		return v, errors.New("corrupt MCP invocation intent")
	}
	if v.ID != trusted.ID || v.MissionID != trusted.MissionID || v.Version != trusted.Version || v.Epoch != trusted.Epoch || v.SessionID != trusted.SessionID || v.Ordinal != trusted.Ordinal || v.ProposalID != trusted.ProposalID || v.GrantID != trusted.GrantID || v.ReservedResultBytes != trusted.ReservedResultBytes || v.Status != "authorized_pending" || v.Phase != "preflight" || v.Result != nil || v.ErrorCode != "" || v.Requests != 0 || v.RequestBytes != 0 || v.ResponseBytes != 0 || v.DurationMillis != 0 || v.DispatchedAt != "" || v.FinishedAt != "" {
		return v, errors.New("MCP intent differs from trusted columns")
	}
	trusted.BindingDigest = v.BindingDigest
	trusted.AuthorityDigest = v.AuthorityDigest
	trusted.CreatedAt = v.CreatedAt
	v = trusted
	if len(result) > 0 {
		v.Result = &domain.EvidenceEnvelope{}
		if decodeStoredJSON(result, v.Result) != nil {
			return v, errors.New("corrupt MCP result")
		}
	}
	if !safeLedgerText(v.ID, 256) || !safeLedgerText(v.MissionID, 256) || v.Epoch < 1 || v.Ordinal < 1 || !safeLedgerText(v.SessionID, 256) || !lowerHex(v.BindingDigest, 64) || !lowerHex(v.AuthorityDigest, 64) || !validCatalogTime(v.CreatedAt) || v.ReservedResultBytes != 64<<10 {
		return v, errors.New("invalid MCP invocation identity")
	}
	if err := validateMCPOutcome(v, domain.MCPCallOutcome{Status: v.Status, Result: v.Result, ErrorCode: v.ErrorCode, Requests: v.Requests, RequestBytes: v.RequestBytes, ResponseBytes: v.ResponseBytes, DurationMillis: v.DurationMillis}, true); err != nil {
		return v, err
	}
	return v, nil
}

func readMCPInvocation(q rowQuerier, missionID, id string) (domain.MCPInvocation, error) {
	return scanMCPInvocation(q.QueryRow(mcpInvocationSelect+` WHERE mission_id=? AND id=?`, missionID, id))
}
func (s *Store) GetMCPInvocation(missionID, id string) (domain.MCPInvocation, error) {
	return readMCPInvocation(s.db, missionID, id)
}
func (s *Store) ListMCPInvocations(missionID string) ([]domain.MCPInvocation, error) {
	rows, err := s.db.Query(mcpInvocationSelect+` WHERE mission_id=? ORDER BY ordinal`, missionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.MCPInvocation, 0)
	for rows.Next() {
		v, err := scanMCPInvocation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func mcpTransition(tx *sql.Tx, v domain.MCPInvocation, from, to string) error {
	_, err := tx.Exec(`INSERT INTO mcp_invocation_transitions(invocation_id,mission_id,version,from_status,to_status,phase,created_at) VALUES(?,?,1,?,?,?,?)`, v.ID, v.MissionID, from, to, v.Phase, timestamp())
	return err
}

func validateMCPInvocationAuthority(q rowQuerier, m domain.Mission, v domain.MCPInvocation) error {
	if m.Epoch != v.Epoch || m.State != "planning" || m.Stage != "mcp_authorized_pending" {
		return ErrConflict
	}
	if err := mcpMissionAuthority(m, v.BindingDigest, v.AuthorityDigest); err != nil {
		return err
	}
	p, err := readMCPRecord[domain.MCPProposal](q, "mcp_call_proposals", "id", v.ProposalID, m.ID)
	if err != nil {
		return err
	}
	if err := validateMCPProposal(q, m, p); err != nil {
		return err
	}
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		application, err := readResearchApplication(q, m.ID, p.SourceInvocationID)
		if err != nil {
			return err
		}
		if application.Kind != "mcp" || application.TargetID != p.ID {
			return errors.New("MCP proposal differs from research application")
		}
	}
	g, err := readMCPRecord[domain.MCPCallGrant](q, "mcp_call_grants", "id", v.GrantID, m.ID)
	if err != nil {
		return err
	}
	if g.Epoch != v.Epoch || g.SessionID != v.SessionID || g.ProposalID != p.ID || g.ProposalDigest != p.Digest || g.BindingDigest != v.BindingDigest || g.AuthorityDigest != v.AuthorityDigest || p.Epoch != v.Epoch || p.SessionID != v.SessionID || !sameJSON(g.Binding, m.MCPBindings) || !sameJSON(g.Profiles, m.Profiles) {
		return ErrConflict
	}
	return nil
}

func (s *Store) ClaimMCPInvocation(missionID, id string, epoch int64) (domain.MCPInvocation, error) {
	return s.advanceMCPDispatch(missionID, id, epoch, false)
}
func (s *Store) MarkMCPCallStarted(missionID, id string, epoch int64) (domain.MCPInvocation, error) {
	return s.advanceMCPDispatch(missionID, id, epoch, true)
}
func (s *Store) advanceMCPDispatch(missionID, id string, epoch int64, callStarted bool) (domain.MCPInvocation, error) {
	zero := domain.MCPInvocation{}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	v, err := readMCPInvocation(tx, missionID, id)
	if err != nil {
		return zero, err
	}
	want := "authorized_pending"
	if callStarted {
		want = "dispatching"
	}
	if v.Epoch != epoch || v.Status != want || v.Phase != "preflight" {
		return zero, ErrConflict
	}
	m, err := readMissionRow(tx, missionID)
	if err != nil {
		return zero, err
	}
	if err := validateMCPInvocationAuthority(tx, m, v); err != nil {
		return zero, err
	}
	if callStarted {
		v.Phase = "call_started"
	} else {
		v.DispatchedAt = timestamp()
	}
	v.Status = "dispatching"
	result, err := tx.Exec(`UPDATE mcp_invocations SET status=?,phase=?,dispatched_at=? WHERE id=? AND mission_id=? AND status=? AND phase='preflight'`, v.Status, v.Phase, v.DispatchedAt, id, missionID, want)
	if err != nil {
		return zero, constraintConflict(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return zero, err
	}
	if n != 1 {
		return zero, ErrConflict
	}
	if err := mcpTransition(tx, v, want, v.Status); err != nil {
		return zero, err
	}
	if err := insertEvent(tx, missionID, "mcp."+v.Phase, m.Revision, timestamp()); err != nil {
		return zero, err
	}
	return v, tx.Commit()
}

func validateMCPOutcome(v domain.MCPInvocation, o domain.MCPCallOutcome, stored bool) error {
	if o.Requests < 0 || o.Requests > 14 || o.RequestBytes < 0 || o.RequestBytes > 14*(1<<20) || o.ResponseBytes < 0 || o.ResponseBytes > 4<<20 || o.DurationMillis < 0 {
		return errors.New("invalid MCP counters")
	}
	if v.Phase != "preflight" && v.Phase != "call_started" {
		return errors.New("invalid MCP phase")
	}
	switch o.Status {
	case "authorized_pending", "dispatching":
		if !stored || o.Result != nil || o.ErrorCode != "" || o.Requests != 0 || o.RequestBytes != 0 || o.ResponseBytes != 0 || o.DurationMillis != 0 || v.FinishedAt != "" {
			return errors.New("invalid active MCP state")
		}
		if (o.Status == "authorized_pending" && (v.DispatchedAt != "" || v.Phase != "preflight")) || (o.Status == "dispatching" && !validCatalogTime(v.DispatchedAt)) {
			return errors.New("invalid MCP dispatch time")
		}
		return nil
	case "succeeded":
		if v.Phase != "call_started" || o.Result == nil || o.ErrorCode != "" {
			return errors.New("invalid MCP success")
		}
	case "failed":
		if !validSafeCode(o.ErrorCode) || (o.Result != nil && (o.ErrorCode != "tool_error" || v.Phase != "call_started")) {
			return errors.New("invalid MCP failure")
		}
	case "outcome_unknown":
		if v.Phase != "call_started" || o.Result != nil || !validSafeCode(o.ErrorCode) {
			return errors.New("invalid unknown MCP outcome")
		}
	case "interrupted_before_dispatch", "interrupted_before_call":
		if !stored || v.Phase != "preflight" || o.Result != nil || !validSafeCode(o.ErrorCode) {
			return errors.New("invalid interrupted MCP state")
		}
	default:
		return errors.New("invalid MCP status")
	}
	if stored && (!validCatalogTime(v.FinishedAt) || (o.Status != "interrupted_before_dispatch" && !validCatalogTime(v.DispatchedAt))) {
		return errors.New("invalid MCP terminal time")
	}
	if o.Result != nil {
		generic := domain.Invocation{ID: v.ID, MissionID: v.MissionID, Epoch: v.Epoch, BindingDigest: v.BindingDigest}
		if err := validateEnvelopeRef(*o.Result, generic, "mcp_result", 64<<10); err != nil {
			return err
		}
		if o.Result.Truncated || o.Result.ContentType != "application/json" {
			return errors.New("MCP result must be complete JSON")
		}
	}
	return nil
}

func (s *Store) FinishMCPInvocation(missionID, id string, epoch int64, outcome domain.MCPCallOutcome) (domain.MCPInvocation, error) {
	zero := domain.MCPInvocation{}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	v, err := readMCPInvocation(tx, missionID, id)
	if err != nil {
		return zero, err
	}
	if epoch != v.Epoch {
		return zero, ErrConflict
	}
	if err := validateMCPOutcome(v, outcome, false); err != nil {
		return zero, err
	}
	oldOutcome := domain.MCPCallOutcome{Status: v.Status, Result: v.Result, ErrorCode: v.ErrorCode, Requests: v.Requests, RequestBytes: v.RequestBytes, ResponseBytes: v.ResponseBytes, DurationMillis: v.DurationMillis}
	if v.Status != "dispatching" {
		if sameJSON(oldOutcome, outcome) {
			return v, tx.Commit()
		}
		return zero, ErrConflict
	}
	m, err := readMissionRow(tx, missionID)
	if err != nil {
		return zero, err
	}
	// Late results are historical only. They never replace the current task
	// checkpoint, revision, budget or evidence authorization.
	authorityErr := validateMCPInvocationAuthority(tx, m, v)
	if authorityErr != nil && !errors.Is(authorityErr, ErrConflict) {
		return zero, authorityErr
	}
	current := authorityErr == nil
	var raw any
	if outcome.Result != nil {
		raw, err = json.Marshal(outcome.Result)
		if err != nil {
			return zero, err
		}
	}
	finished := timestamp()
	result, err := tx.Exec(`UPDATE mcp_invocations SET status=?,result=?,error_code=?,requests=?,request_bytes=?,response_bytes=?,duration_millis=?,finished_at=? WHERE id=? AND mission_id=? AND status='dispatching'`, outcome.Status, raw, outcome.ErrorCode, outcome.Requests, outcome.RequestBytes, outcome.ResponseBytes, outcome.DurationMillis, finished, id, missionID)
	if err != nil {
		return zero, constraintConflict(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return zero, err
	}
	if n != 1 {
		return zero, ErrConflict
	}
	v.Status = outcome.Status
	v.Result = outcome.Result
	v.ErrorCode = outcome.ErrorCode
	v.Requests = outcome.Requests
	v.RequestBytes = outcome.RequestBytes
	v.ResponseBytes = outcome.ResponseBytes
	v.DurationMillis = outcome.DurationMillis
	v.FinishedAt = finished
	if err := mcpTransition(tx, v, "dispatching", v.Status); err != nil {
		return zero, err
	}
	if current {
		_, err = updateMissionTx(tx, missionID, m.Revision, "mcp.finished", func(m *domain.Mission) error {
			if outcome.Status == "succeeded" || (outcome.Status == "failed" && outcome.Result != nil) {
				m.Stage = "mcp_observation_ready"
			} else {
				m.State = "blocked"
				m.Stage = "mcp_failed"
				m.Error = outcome.ErrorCode
			}
			return nil
		})
	} else {
		err = insertEvent(tx, missionID, "mcp.stale_result", m.Revision, finished)
	}
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(); err != nil {
		return zero, err
	}
	return v, nil
}
