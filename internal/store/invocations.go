package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"foundry/internal/domain"
)

// ErrRepositoryBudget distinguishes a deterministic policy denial from storage
// failures; callers must not relabel infrastructure failures as model denials.
var ErrRepositoryBudget = errors.New("repository call budget exhausted")

type invocationIntent struct {
	ID                  string                  `json:"id"`
	MissionID           string                  `json:"mission_id"`
	Epoch               int64                   `json:"epoch"`
	SessionID           string                  `json:"session_id"`
	Ordinal             int64                   `json:"ordinal"`
	Kind                string                  `json:"kind"`
	Role                domain.Role             `json:"role"`
	Name                string                  `json:"name"`
	BindingDigest       string                  `json:"binding_digest"`
	AuthorityDigest     string                  `json:"authority_digest"`
	GrantID             string                  `json:"grant_id,omitempty"`
	Input               domain.EvidenceEnvelope `json:"input"`
	ReservedResultBytes int64                   `json:"reserved_result_bytes"`
	CreatedAt           string                  `json:"created_at"`
}

func intentFromInvocation(v domain.Invocation) invocationIntent {
	return invocationIntent{
		ID: v.ID, MissionID: v.MissionID, Epoch: v.Epoch, SessionID: v.SessionID,
		Ordinal: v.Ordinal, Kind: v.Kind, Role: v.Role, Name: v.Name,
		BindingDigest: v.BindingDigest, AuthorityDigest: v.AuthorityDigest,
		GrantID: v.GrantID, Input: v.Input, ReservedResultBytes: v.ReservedResultBytes,
		CreatedAt: v.CreatedAt,
	}
}

func (i invocationIntent) invocation() domain.Invocation {
	return domain.Invocation{
		ID: i.ID, MissionID: i.MissionID, Epoch: i.Epoch, SessionID: i.SessionID,
		Ordinal: i.Ordinal, Kind: i.Kind, Role: i.Role, Name: i.Name,
		BindingDigest: i.BindingDigest, AuthorityDigest: i.AuthorityDigest,
		GrantID: i.GrantID, Input: i.Input, ReservedResultBytes: i.ReservedResultBytes,
		CreatedAt: i.CreatedAt,
	}
}

const invocationSelect = `SELECT id,mission_id,epoch,session_id,ordinal,kind,role,name,binding_digest,authority_digest,grant_id,intent,status,result,error_code,reserved_result_bytes,created_at,dispatched_at,finished_at,usage_known,input_tokens,output_tokens,model_receipt FROM invocations`

type rowScanner interface{ Scan(...any) error }

func scanInvocation(row rowScanner) (domain.Invocation, error) {
	var trusted invocationIntent
	var role string
	var intentRaw, resultRaw []byte
	var status, errorCode, dispatchedAt, finishedAt string
	var usageKnown int
	var inputTokens, outputTokens int64
	var receiptRaw []byte
	err := row.Scan(
		&trusted.ID, &trusted.MissionID, &trusted.Epoch, &trusted.SessionID, &trusted.Ordinal,
		&trusted.Kind, &role, &trusted.Name, &trusted.BindingDigest, &trusted.AuthorityDigest,
		&trusted.GrantID, &intentRaw, &status, &resultRaw, &errorCode,
		&trusted.ReservedResultBytes, &trusted.CreatedAt, &dispatchedAt, &finishedAt,
		&usageKnown, &inputTokens, &outputTokens, &receiptRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Invocation{}, ErrNotFound
	}
	if err != nil {
		return domain.Invocation{}, err
	}
	trusted.Role = domain.Role(role)
	var persisted invocationIntent
	if err := json.Unmarshal(intentRaw, &persisted); err != nil {
		return domain.Invocation{}, fmt.Errorf("decode immutable intent: %w", err)
	}
	trusted.Input = persisted.Input
	want, _ := json.Marshal(trusted)
	got, _ := json.Marshal(persisted)
	if !bytes.Equal(want, got) {
		return domain.Invocation{}, errors.New("immutable intent disagrees with trusted invocation columns")
	}
	v := persisted.invocation()
	v.Status = status
	v.ErrorCode = errorCode
	v.DispatchedAt = dispatchedAt
	v.FinishedAt = finishedAt
	v.UsageKnown = usageKnown == 1
	v.InputTokens = inputTokens
	v.OutputTokens = outputTokens
	if len(receiptRaw) > 0 {
		v.ModelReceipt = &domain.ModelCallReceipt{}
		if err := decodeStoredJSON(receiptRaw, v.ModelReceipt); err != nil {
			return domain.Invocation{}, err
		}
	}
	if len(resultRaw) > 0 {
		v.Result = &domain.EvidenceEnvelope{}
		if err := json.Unmarshal(resultRaw, v.Result); err != nil {
			return domain.Invocation{}, fmt.Errorf("decode invocation result: %w", err)
		}
	}
	if err := validateStoredInvocation(v); err != nil {
		return domain.Invocation{}, err
	}
	return v, nil
}

func (s *Store) CreateReadGrant(g domain.ReadGrant, expected int64) (domain.Mission, domain.ReadGrant, error) {
	zeroMission, zeroGrant := domain.Mission{}, domain.ReadGrant{}
	tx, err := s.db.Begin()
	if err != nil {
		return zeroMission, zeroGrant, err
	}
	defer tx.Rollback()
	if existing, readErr := readGrant(tx, g.ID); readErr == nil {
		replay := g
		replay.CreatedAt = existing.CreatedAt
		if !sameJSON(existing, replay) {
			return zeroMission, zeroGrant, ErrConflict
		}
		m, err := readMissionRow(tx, existing.MissionID)
		if err != nil {
			return zeroMission, zeroGrant, err
		}
		if err = tx.Commit(); err != nil {
			return zeroMission, zeroGrant, err
		}
		return m, existing, nil
	} else if !errors.Is(readErr, ErrNotFound) {
		return zeroMission, zeroGrant, readErr
	}
	if !safeLedgerText(g.ID, 256) || !safeLedgerText(g.MissionID, 256) || g.Epoch < 1 || !safeLedgerText(g.SessionID, 256) || g.Role != domain.Researcher || g.RequiredState != "planning" || !lowerHex(g.BindingDigest, 64) || !lowerHex(g.AuthorityDigest, 64) || g.CreatedAt != "" {
		return zeroMission, zeroGrant, errors.New("invalid read grant")
	}
	m, err := readMissionRow(tx, g.MissionID)
	if err != nil {
		return zeroMission, zeroGrant, err
	}
	if m.Revision != expected {
		return zeroMission, zeroGrant, ErrConflict
	}
	if _, err := controlledMissionAuthority(m, g.BindingDigest); err != nil || m.Epoch != g.Epoch || m.State != g.RequiredState {
		if err != nil {
			return zeroMission, zeroGrant, err
		}
		return zeroMission, zeroGrant, ErrConflict
	}
	if isMCPMission(m) {
		if err := supportedMCPLedger(tx, m.ID); err != nil {
			return zeroMission, zeroGrant, err
		}
		if err := mcpMissionAuthority(m, g.BindingDigest, g.AuthorityDigest); err != nil {
			return zeroMission, zeroGrant, err
		}
	}
	g.CreatedAt = timestamp()
	raw, err := json.Marshal(g)
	if err != nil {
		return zeroMission, zeroGrant, err
	}
	m, err = updateMissionTx(tx, g.MissionID, expected, "read_grant.created", func(*domain.Mission) error { return nil })
	if err != nil {
		return zeroMission, zeroGrant, err
	}
	_, err = tx.Exec(`INSERT INTO read_grants(id,mission_id,epoch,session_id,role,required_state,binding_digest,authority_digest,data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, g.ID, g.MissionID, g.Epoch, g.SessionID, g.Role, g.RequiredState, g.BindingDigest, g.AuthorityDigest, raw, g.CreatedAt)
	if err != nil {
		return zeroMission, zeroGrant, constraintConflict(err)
	}
	if err = tx.Commit(); err != nil {
		_ = tx.Rollback()
		stored, readErr := s.GetReadGrant(g.ID)
		if readErr == nil && sameJSON(stored, g) {
			mission, missionErr := s.GetMission(g.MissionID)
			return mission, stored, missionErr
		}
		return zeroMission, zeroGrant, fmt.Errorf("commit read grant: %w", err)
	}
	return m, g, nil
}

func (s *Store) BeginInvocation(v domain.Invocation, expected int64) (domain.Mission, domain.Invocation, error) {
	zeroMission, zeroInvocation := domain.Mission{}, domain.Invocation{}
	if err := validateNewInvocation(v); err != nil {
		return zeroMission, zeroInvocation, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zeroMission, zeroInvocation, err
	}
	defer tx.Rollback()
	m, saved, err := beginInvocationTx(tx, v, expected, false)
	if err != nil {
		return zeroMission, zeroInvocation, err
	}
	if err = tx.Commit(); err != nil {
		_ = tx.Rollback()
		stored, readErr := s.GetInvocation(saved.ID)
		if readErr == nil && sameInvocationStoredIntent(stored, saved) {
			mission, missionErr := s.GetMission(saved.MissionID)
			return mission, stored, missionErr
		}
		return zeroMission, zeroInvocation, fmt.Errorf("commit invocation reservation: %w", err)
	}
	return m, saved, nil
}

// Shared reservation, quota and intent logic. researchTransition is only set
// by a checkpoint transaction that also persists its owning v2 flow record.
func beginInvocationTx(tx *sql.Tx, v domain.Invocation, expected int64, researchTransition bool) (domain.Mission, domain.Invocation, error) {
	zeroMission, zeroInvocation := domain.Mission{}, domain.Invocation{}
	if err := validateNewInvocation(v); err != nil {
		return zeroMission, zeroInvocation, err
	}
	if existing, readErr := readInvocation(tx, v.ID); readErr == nil {
		if !sameInvocationRequest(existing, v) {
			return zeroMission, zeroInvocation, ErrConflict
		}
		m, err := readMissionRow(tx, existing.MissionID)
		if err != nil {
			return zeroMission, zeroInvocation, err
		}
		return m, existing, nil
	} else if !errors.Is(readErr, ErrNotFound) {
		return zeroMission, zeroInvocation, readErr
	}
	m, err := readMissionRow(tx, v.MissionID)
	if err != nil {
		return zeroMission, zeroInvocation, err
	}
	if m.Revision != expected || m.Epoch != v.Epoch {
		return zeroMission, zeroInvocation, ErrConflict
	}
	if v.Kind == "model" {
		if err := guardModelCallTx(tx, m, v.Role); err != nil {
			return zeroMission, zeroInvocation, err
		}
	}
	bindings, err := invocationMissionAuthority(m, v)
	if err != nil {
		return zeroMission, zeroInvocation, err
	}
	if isMCPMission(m) {
		if err := supportedMCPLedger(tx, m.ID); err != nil {
			return zeroMission, zeroInvocation, err
		}
		if mcpCheckpointBlocked(m.Stage) && !(researchTransition && domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) && m.Stage == "mcp_observation_ready") {
			return zeroMission, zeroInvocation, ErrConflict
		}
		if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) && !researchTransition && (v.Role == domain.Researcher || v.Role == domain.Planner) {
			return zeroMission, zeroInvocation, ErrConflict
		}
		if err := mcpRoleAuthority(m, v.BindingDigest, v.AuthorityDigest, v.Role); err != nil {
			return zeroMission, zeroInvocation, err
		}
	}
	if !activeRoleState(v.Role, m.State) {
		return zeroMission, zeroInvocation, ErrConflict
	}
	if err := validateInvocationKind(v, bindings); err != nil {
		return zeroMission, zeroInvocation, err
	}
	if v.GrantID != "" {
		if _, err := invocationGrant(tx, v, m.State); err != nil {
			return zeroMission, zeroInvocation, err
		}
	} else if needsInvocationGrant(m, v) {
		return zeroMission, zeroInvocation, ErrConflict
	}
	if err := validateEnvelopeRef(v.Input, v, "model_input", bindings.Budget.MaxArgumentBytes); err != nil {
		if v.Kind == "model" {
			err = validateEnvelopeRef(v.Input, v, "model_input", 2<<20)
		}
		if err != nil {
			return zeroMission, zeroInvocation, err
		}
	}
	if v.Kind == "model" {
		if m.Budget.MaxCalls < 1 || m.Budget.UsedCalls >= m.Budget.MaxCalls {
			return zeroMission, zeroInvocation, errors.New("model call budget exhausted")
		}
		if v.Role == domain.Researcher && m.ToolMode != "" {
			var turns int
			if err := tx.QueryRow(`SELECT count(*) FROM invocations WHERE mission_id=? AND epoch=? AND session_id=? AND role=? AND kind='model'`, v.MissionID, v.Epoch, v.SessionID, v.Role).Scan(&turns); err != nil {
				return zeroMission, zeroInvocation, err
			}
			if turns >= bindings.Budget.MaxTurns {
				return zeroMission, zeroInvocation, errors.New("Researcher turn budget exhausted")
			}
		}
	} else {
		var calls int
		var reserved int64
		if isMCPMission(m) {
			if err := mcpToolQuota(tx, v.MissionID); err != nil {
				return zeroMission, zeroInvocation, err
			}
		}
		if err := tx.QueryRow(`SELECT count(*),COALESCE(sum(reserved_result_bytes),0) FROM invocations WHERE mission_id=? AND kind='repository'`, v.MissionID).Scan(&calls, &reserved); err != nil {
			return zeroMission, zeroInvocation, err
		}
		if calls >= bindings.Budget.MaxCalls || reserved+int64(bindings.Budget.MaxResultBytes) > bindings.Budget.MaxTotalResultBytes {
			return zeroMission, zeroInvocation, ErrRepositoryBudget
		}
		v.ReservedResultBytes = int64(bindings.Budget.MaxResultBytes)
	}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(ordinal),0)+1 FROM invocations WHERE mission_id=?`, v.MissionID).Scan(&v.Ordinal); err != nil {
		return zeroMission, zeroInvocation, err
	}
	v.Status = "authorized_pending"
	v.CreatedAt = timestamp()
	intentRaw, err := json.Marshal(intentFromInvocation(v))
	if err != nil {
		return zeroMission, zeroInvocation, err
	}
	m, err = updateMissionTx(tx, v.MissionID, expected, "invocation.authorized", func(m *domain.Mission) error {
		if v.Kind == "model" {
			m.Budget.UsedCalls++
			m.Budget.UsageUnknown = true
		}
		return nil
	})
	if err != nil {
		return zeroMission, zeroInvocation, err
	}
	_, err = tx.Exec(`INSERT INTO invocations(id,mission_id,epoch,session_id,ordinal,kind,role,name,binding_digest,authority_digest,grant_id,intent,status,reserved_result_bytes,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ID, v.MissionID, v.Epoch, v.SessionID, v.Ordinal, v.Kind, v.Role, v.Name, v.BindingDigest, v.AuthorityDigest, v.GrantID, intentRaw, v.Status, v.ReservedResultBytes, v.CreatedAt)
	if err != nil {
		return zeroMission, zeroInvocation, constraintConflict(err)
	}
	if _, err = tx.Exec(`INSERT INTO invocation_transitions(invocation_id,mission_id,from_status,to_status,created_at) VALUES(?,?, '',?,?)`, v.ID, v.MissionID, v.Status, v.CreatedAt); err != nil {
		return zeroMission, zeroInvocation, err
	}
	return m, v, nil
}

func (s *Store) ClaimInvocation(id string, epoch int64) (domain.Invocation, error) {
	zero := domain.Invocation{}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	v, err := readInvocation(tx, id)
	if err != nil {
		return zero, err
	}
	if v.Status != "authorized_pending" || epoch != v.Epoch {
		return zero, ErrConflict
	}
	m, err := readMissionRow(tx, v.MissionID)
	if err != nil {
		return zero, err
	}
	if v.Kind == "model" {
		if err := guardModelCallTx(tx, m, v.Role); err != nil {
			return zero, err
		}
	}
	bindings, err := invocationMissionAuthority(m, v)
	if err != nil {
		return zero, err
	}
	if m.Epoch != epoch || !activeRoleState(v.Role, m.State) {
		return zero, ErrConflict
	}
	if isMCPMission(m) {
		if err := supportedMCPLedger(tx, m.ID); err != nil {
			return zero, err
		}
		if mcpCheckpointBlocked(m.Stage) {
			return zero, ErrConflict
		}
		if err := mcpRoleAuthority(m, v.BindingDigest, v.AuthorityDigest, v.Role); err != nil {
			return zero, err
		}
		if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
			if err := validateResearchClaim(tx, v); err != nil {
				return zero, err
			}
		}
		if v.Kind == "model" && v.Role == domain.Researcher {
			source, err := readMCPRecord[domain.MCPProposalSource](tx, "mcp_proposal_sources", "model_invocation_id", v.ID, m.ID)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return zero, ErrConflict
				}
				return zero, err
			}
			if _, err := validateMCPSource(tx, m, source, false); err != nil {
				return zero, err
			}
		}
	}
	if err := validateInvocationKind(v, bindings); err != nil {
		return zero, err
	}
	if v.GrantID != "" {
		if _, err := invocationGrant(tx, v, m.State); err != nil {
			return zero, err
		}
	} else if needsInvocationGrant(m, v) {
		return zero, ErrConflict
	}
	v.DispatchedAt = timestamp()
	result, err := tx.Exec(`UPDATE invocations SET status='dispatching',dispatched_at=? WHERE id=? AND status='authorized_pending'`, v.DispatchedAt, id)
	if err != nil {
		return zero, constraintConflict(err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return zero, err
		}
		return zero, ErrConflict
	}
	if _, err = tx.Exec(`INSERT INTO invocation_transitions(invocation_id,mission_id,from_status,to_status,created_at) VALUES(?,?,'authorized_pending','dispatching',?)`, id, v.MissionID, v.DispatchedAt); err != nil {
		return zero, err
	}
	if err = insertEvent(tx, v.MissionID, "invocation.dispatching", m.Revision, v.DispatchedAt); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	v.Status = "dispatching"
	return v, nil
}

func (s *Store) FinishInvocation(id string, epoch int64, status string, resultEnvelope *domain.EvidenceEnvelope, safeError string, usage domain.CallUsage) (domain.Invocation, error) {
	zero := domain.Invocation{}
	if status != "succeeded" && status != "failed" && status != "outcome_unknown" {
		return zero, errors.New("invalid invocation terminal status")
	}
	if status == "succeeded" {
		if resultEnvelope == nil || safeError != "" {
			return zero, errors.New("successful invocation requires evidence and no error")
		}
	} else if resultEnvelope != nil || !validSafeCode(safeError) {
		return zero, errors.New("failed invocation requires only a safe error code")
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || (!usage.UsageKnown && (usage.InputTokens != 0 || usage.OutputTokens != 0)) {
		return zero, errors.New("invalid call usage")
	}
	if err := validateModelReceipt(usage.ModelReceipt, status); err != nil {
		return zero, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	v, err := readInvocation(tx, id)
	if err != nil {
		return zero, err
	}
	if v.Status != "dispatching" || v.Epoch != epoch {
		if terminalInvocationMatches(v, epoch, status, resultEnvelope, safeError, usage) {
			return v, tx.Commit()
		}
		return zero, ErrConflict
	}
	m, err := readMissionRow(tx, v.MissionID)
	if err != nil {
		return zero, err
	}
	bindings, err := invocationMissionAuthority(m, v)
	if err != nil {
		return zero, err
	}
	if domain.IsInferenceSnapshot(m) && v.Kind == "model" && usage.ModelReceipt == nil {
		return zero, errors.New("schema2 model outcome requires provider receipt")
	}
	staleUnknown := m.Epoch != epoch && status == "outcome_unknown" && safeError == "stale_dispatch" && resultEnvelope == nil && !usage.UsageKnown && usage.InputTokens == 0 && usage.OutputTokens == 0
	if !staleUnknown && (m.Epoch != epoch || !activeRoleState(v.Role, m.State)) {
		return zero, ErrConflict
	}
	if v.GrantID != "" && !staleUnknown {
		if _, err := invocationGrant(tx, v, m.State); err != nil {
			return zero, err
		}
	}
	if v.Kind == "repository" && usage != (domain.CallUsage{}) {
		return zero, errors.New("repository invocation cannot report model usage")
	}
	if resultEnvelope != nil {
		kind := "model_output"
		limit := 2 << 20
		if v.Kind == "repository" {
			kind = "tool_observation"
			limit = bindings.Budget.MaxResultBytes
		}
		if err := validateEnvelopeRef(*resultEnvelope, v, kind, limit); err != nil {
			return zero, err
		}
	}
	var otherUnknown int
	if v.Kind == "model" {
		if err := tx.QueryRow(`SELECT count(*) FROM invocations WHERE mission_id=? AND kind='model' AND id<>? AND usage_known=0`, v.MissionID, v.ID).Scan(&otherUnknown); err != nil {
			return zero, err
		}
	}
	m, err = updateMissionTx(tx, v.MissionID, m.Revision, "invocation.finished", func(m *domain.Mission) error {
		if v.Kind == "model" {
			if usage.UsageKnown {
				m.Budget.InputTokens += usage.InputTokens
				m.Budget.OutputTokens += usage.OutputTokens
			}
			m.Budget.UsageUnknown = !usage.UsageKnown || otherUnknown > 0
		}
		return nil
	})
	if err != nil {
		return zero, err
	}
	var resultRaw any
	if resultEnvelope != nil {
		resultRaw, err = json.Marshal(resultEnvelope)
		if err != nil {
			return zero, err
		}
	}
	finishedAt := timestamp()
	var receiptRaw any
	if usage.ModelReceipt != nil {
		receiptRaw, err = json.Marshal(usage.ModelReceipt)
		if err != nil {
			return zero, err
		}
	}
	usageKnown := 0
	if usage.UsageKnown {
		usageKnown = 1
	}
	updated, err := tx.Exec(`UPDATE invocations SET status=?,result=?,error_code=?,finished_at=?,usage_known=?,input_tokens=?,output_tokens=?,model_receipt=? WHERE id=? AND status='dispatching'`, status, resultRaw, safeError, finishedAt, usageKnown, usage.InputTokens, usage.OutputTokens, receiptRaw, id)
	if err != nil {
		return zero, constraintConflict(err)
	}
	if changed, err := updated.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return zero, err
		}
		return zero, ErrConflict
	}
	if _, err = tx.Exec(`INSERT INTO invocation_transitions(invocation_id,mission_id,from_status,to_status,created_at) VALUES(?,?,'dispatching',?,?)`, id, v.MissionID, status, finishedAt); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		_ = tx.Rollback()
		stored, readErr := s.GetInvocation(id)
		if readErr == nil && terminalInvocationMatches(stored, epoch, status, resultEnvelope, safeError, usage) {
			return stored, nil
		}
		return zero, fmt.Errorf("commit invocation finish: %w", err)
	}
	v.Status, v.Result, v.ErrorCode, v.FinishedAt = status, resultEnvelope, safeError, finishedAt
	v.UsageKnown, v.InputTokens, v.OutputTokens = usage.UsageKnown, usage.InputTokens, usage.OutputTokens
	v.ModelReceipt = usage.ModelReceipt
	return v, nil
}

func (s *Store) GetInvocation(id string) (domain.Invocation, error) {
	v, err := readInvocation(s.db, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return domain.Invocation{}, fmt.Errorf("corrupt invocation ledger %q: %w", id, err)
	}
	return v, err
}

func (s *Store) ListInvocations(missionID string, before int64, limit int) ([]domain.Invocation, error) {
	if _, err := s.GetMission(missionID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(invocationSelect+` WHERE mission_id=? AND (?<=0 OR ordinal<?) ORDER BY ordinal DESC LIMIT ?`, missionID, before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Invocation, 0)
	for rows.Next() {
		v, scanErr := scanInvocation(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("corrupt invocation ledger for mission %q: %w", missionID, scanErr)
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (s *Store) ListReadGrants(missionID string) ([]domain.ReadGrant, error) {
	if _, err := s.GetMission(missionID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT data FROM read_grants WHERE mission_id=? ORDER BY epoch,created_at,id`, missionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.ReadGrant, 0)
	for rows.Next() {
		var raw []byte
		var g domain.ReadGrant
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &g); err != nil {
			return nil, fmt.Errorf("corrupt read grant: %w", err)
		}
		result = append(result, g)
	}
	return result, rows.Err()
}

// HasUnfinishedInvocations uses SQL-owned facts even if an intent is corrupt.
// Recovery callers must fence the owning epoch before decoding any ledger rows.
func (s *Store) HasUnfinishedInvocations(missionID string) (bool, error) {
	var unfinished bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM invocations WHERE mission_id=? AND status IN ('authorized_pending','dispatching'))`, missionID).Scan(&unfinished)
	return unfinished, err
}

// RecoverInvocations only touches the explicitly selected mission. The engine
// selects supported formats; future-format ledgers must remain untouched.
func (s *Store) RecoverInvocations(missionID string) error {
	m, err := s.GetMission(missionID)
	if err != nil {
		return err
	}
	if err = domain.ValidateMissionModelSnapshot(m); err != nil {
		return err
	}
	rows, err := s.db.Query(`SELECT id,mission_id FROM invocations WHERE mission_id=? ORDER BY ordinal`, missionID)
	if err != nil {
		return err
	}
	type target struct{ id, missionID string }
	targets := make([]target, 0)
	for rows.Next() {
		var target target
		if err := rows.Scan(&target.id, &target.missionID); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, target := range targets {
		if err := s.recoverInvocation(target.id, target.missionID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) recoverInvocation(id, trustedMissionID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	v, err := readInvocation(tx, id)
	if err != nil {
		m, missionErr := readMissionRow(tx, trustedMissionID)
		if missionErr != nil {
			return missionErr
		}
		_, missionErr = updateMissionTx(tx, trustedMissionID, m.Revision, "invocation.corrupt", func(m *domain.Mission) error {
			m.State = "blocked"
			m.Error = "invocation ledger is corrupt; execution remains disabled"
			return nil
		})
		if missionErr != nil {
			return missionErr
		}
		return tx.Commit()
	}
	if v.MissionID != trustedMissionID {
		return errors.New("trusted invocation mission identity mismatch")
	}
	historicalMission, err := readMissionRow(tx, trustedMissionID)
	if err != nil {
		return err
	}
	if domain.IsInferenceSnapshot(historicalMission) && historicalMission.ToolMode == "" && v.Kind == "model" {
		if _, err = domain.HistoricalModelSnapshot(historicalMission, v.BindingDigest); err != nil {
			return err
		}
	}
	if v.Status != "authorized_pending" && v.Status != "dispatching" {
		return nil
	}
	to, code := "interrupted_before_dispatch", "interrupted_before_dispatch"
	if v.Status == "dispatching" {
		to, code = "outcome_unknown", "outcome_unknown_after_restart"
	}
	finishedAt := timestamp()
	m, err := readMissionRow(tx, v.MissionID)
	if err != nil {
		return err
	}
	var receiptRaw any
	if domain.IsInferenceSnapshot(m) && v.Kind == "model" {
		certainty := "not_sent"
		if v.Status == "dispatching" {
			certainty = "unknown"
		}
		receiptRaw, err = json.Marshal(domain.ModelCallReceipt{Certainty: certainty})
		if err != nil {
			return err
		}
	}
	updated, err := tx.Exec(`UPDATE invocations SET status=?,error_code=?,finished_at=?,model_receipt=? WHERE id=? AND status=?`, to, code, finishedAt, receiptRaw, id, v.Status)
	if err != nil {
		return err
	}
	if changed, err := updated.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return err
		}
		return ErrConflict
	}
	if _, err = tx.Exec(`INSERT INTO invocation_transitions(invocation_id,mission_id,from_status,to_status,created_at) VALUES(?,?,?,?,?)`, id, v.MissionID, v.Status, to, finishedAt); err != nil {
		return err
	}
	if err = insertEvent(tx, v.MissionID, "invocation.recovered", m.Revision, finishedAt); err != nil {
		return err
	}
	return tx.Commit()
}

type rowQuerier interface{ QueryRow(string, ...any) *sql.Row }

func readInvocation(q rowQuerier, id string) (domain.Invocation, error) {
	return scanInvocation(q.QueryRow(invocationSelect+` WHERE id=?`, id))
}

func readMissionRow(q rowQuerier, id string) (domain.Mission, error) {
	var raw []byte
	err := q.QueryRow(`SELECT data FROM missions WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Mission{}, ErrNotFound
	}
	if err != nil {
		return domain.Mission{}, err
	}
	return decodeMission(raw)
}

func (s *Store) GetReadGrant(id string) (domain.ReadGrant, error) {
	return readGrant(s.db, id)
}

func readGrant(q rowQuerier, id string) (domain.ReadGrant, error) {
	var raw []byte
	var trusted domain.ReadGrant
	var role string
	err := q.QueryRow(`SELECT id,mission_id,epoch,session_id,role,required_state,binding_digest,authority_digest,data,created_at FROM read_grants WHERE id=?`, id).Scan(&trusted.ID, &trusted.MissionID, &trusted.Epoch, &trusted.SessionID, &role, &trusted.RequiredState, &trusted.BindingDigest, &trusted.AuthorityDigest, &raw, &trusted.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReadGrant{}, ErrNotFound
	}
	if err != nil {
		return domain.ReadGrant{}, err
	}
	var g domain.ReadGrant
	if err := json.Unmarshal(raw, &g); err != nil {
		return domain.ReadGrant{}, fmt.Errorf("decode read grant: %w", err)
	}
	trusted.Role = domain.Role(role)
	if !sameJSON(trusted, g) {
		return domain.ReadGrant{}, errors.New("read grant data disagrees with trusted columns")
	}
	return g, nil
}

func controlledMissionAuthority(m domain.Mission, digest string) (domain.TaskBindings, error) {
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return domain.TaskBindings{}, err
	}
	if isMCPMission(m) {
		actual, err := domain.MCPBindingDigest(*m.TaskBindings, *m.MCPBindings)
		if err != nil {
			return domain.TaskBindings{}, err
		}
		if actual != digest {
			return domain.TaskBindings{}, ErrConflict
		}
		return *m.TaskBindings, nil
	}
	base, _, err := domain.BaseWorkspaceMode(m.WorkspaceMode)
	if err != nil || base != domain.WorkspaceModeControlledReadV1 || m.ToolMode != domain.ToolModeControlledReadV1 || m.TaskBindings == nil {
		return domain.TaskBindings{}, errors.New("mission is not fenced for controlled read")
	}
	actual, err := domain.TaskBindingDigest(*m.TaskBindings)
	if err != nil {
		return domain.TaskBindings{}, err
	}
	if actual != digest {
		return domain.TaskBindings{}, ErrConflict
	}
	return *m.TaskBindings, nil
}

func invocationGrant(q rowQuerier, v domain.Invocation, state string) (domain.ReadGrant, error) {
	g, err := readGrant(q, v.GrantID)
	if err != nil {
		return domain.ReadGrant{}, err
	}
	if g.MissionID != v.MissionID || g.Epoch != v.Epoch || g.SessionID != v.SessionID || g.Role != v.Role || g.RequiredState != state || g.BindingDigest != v.BindingDigest || g.AuthorityDigest != v.AuthorityDigest {
		return domain.ReadGrant{}, ErrConflict
	}
	return g, nil
}

func validateNewInvocation(v domain.Invocation) error {
	if v.ModelReceipt != nil {
		return errors.New("caller supplied outcome receipt")
	}
	if !safeLedgerText(v.ID, 256) || !safeLedgerText(v.MissionID, 256) || v.Epoch < 1 || !safeLedgerText(v.SessionID, 256) || !lowerHex(v.BindingDigest, 64) || !lowerHex(v.AuthorityDigest, 64) {
		return errors.New("invalid invocation identity")
	}
	if v.Ordinal != 0 || v.ReservedResultBytes != 0 || v.CreatedAt != "" || v.DispatchedAt != "" || v.FinishedAt != "" || v.Result != nil || v.ErrorCode != "" || v.UsageKnown || v.InputTokens != 0 || v.OutputTokens != 0 || (v.Status != "" && v.Status != "authorized_pending") {
		return errors.New("caller supplied invocation runtime state")
	}
	return nil
}

func validateStoredInvocation(v domain.Invocation) error {
	if err := validateModelReceipt(v.ModelReceipt, v.Status); err != nil {
		return err
	}
	if v.Kind != "model" && v.ModelReceipt != nil {
		return errors.New("non-model receipt")
	}
	if !safeLedgerText(v.ID, 256) || !safeLedgerText(v.MissionID, 256) || v.Epoch < 1 || !safeLedgerText(v.SessionID, 256) || v.Ordinal < 1 || !lowerHex(v.BindingDigest, 64) || !lowerHex(v.AuthorityDigest, 64) || !validCatalogTime(v.CreatedAt) || v.ReservedResultBytes < 0 || (!v.UsageKnown && (v.InputTokens != 0 || v.OutputTokens != 0)) {
		return errors.New("invalid stored invocation identity or counters")
	}
	if err := validateEnvelopeRef(v.Input, v, "model_input", 2<<20); err != nil {
		return err
	}
	if v.Kind == "model" {
		if v.Name != "model.complete" || !supportedInvocationRole(v.Role) || v.ReservedResultBytes != 0 {
			return errors.New("invalid stored model invocation")
		}
	} else if v.Kind == "repository" {
		if v.Role != domain.Researcher || (v.Name != "repo.list" && v.Name != "repo.read" && v.Name != "repo.search") || v.ReservedResultBytes != 64<<10 || v.Input.Bytes > 8<<10 {
			return errors.New("invalid stored repository invocation")
		}
	} else {
		return errors.New("invalid stored invocation kind")
	}
	switch v.Status {
	case "authorized_pending":
		if v.Result != nil || v.ErrorCode != "" || v.DispatchedAt != "" || v.FinishedAt != "" || v.UsageKnown {
			return errors.New("invalid pending invocation state")
		}
	case "dispatching":
		if v.Result != nil || v.ErrorCode != "" || !validCatalogTime(v.DispatchedAt) || v.FinishedAt != "" || v.UsageKnown {
			return errors.New("invalid dispatching invocation state")
		}
	case "succeeded":
		if v.Result == nil || v.ErrorCode != "" || !validCatalogTime(v.DispatchedAt) || !validCatalogTime(v.FinishedAt) {
			return errors.New("invalid successful invocation state")
		}
		kind, limit := "model_output", 2<<20
		if v.Kind == "repository" {
			kind, limit = "tool_observation", int(v.ReservedResultBytes)
		}
		if err := validateEnvelopeRef(*v.Result, v, kind, limit); err != nil {
			return err
		}
	case "failed", "outcome_unknown":
		if v.Result != nil || !validSafeCode(v.ErrorCode) || !validCatalogTime(v.DispatchedAt) || !validCatalogTime(v.FinishedAt) {
			return errors.New("invalid failed invocation state")
		}
	case "interrupted_before_dispatch":
		if v.Result != nil || !validSafeCode(v.ErrorCode) || v.DispatchedAt != "" || !validCatalogTime(v.FinishedAt) || v.UsageKnown {
			return errors.New("invalid interrupted invocation state")
		}
	default:
		return errors.New("unknown invocation status")
	}
	return nil
}

func validateInvocationKind(v domain.Invocation, bindings domain.TaskBindings) error {
	if v.Kind == "model" {
		if v.Name != "model.complete" || !supportedInvocationRole(v.Role) {
			return errors.New("unsupported model invocation")
		}
		return nil
	}
	if v.Kind != "repository" || v.Role != domain.Researcher {
		return errors.New("unsupported invocation kind or role")
	}
	for _, name := range bindings.Tools {
		if v.Name == name {
			return nil
		}
	}
	return errors.New("repository tool is not bound")
}

func validateEnvelopeRef(envelope domain.EvidenceEnvelope, v domain.Invocation, kind string, maxBytes int) error {
	if envelope.SchemaVersion != 1 || envelope.Kind != kind || envelope.InvocationID != v.ID || envelope.MissionID != v.MissionID || envelope.Epoch != v.Epoch || !safeLedgerText(envelope.Producer, 256) || !validCatalogTime(envelope.CapturedAt) || !lowerHex(envelope.Digest, 64) || envelope.Bytes < 0 || envelope.Bytes > int64(maxBytes) || (envelope.ContentType != "application/json" && envelope.ContentType != "text/plain" && envelope.ContentType != "text/plain; charset=utf-8") || envelope.BindingDigest != v.BindingDigest {
		return errors.New("invalid invocation evidence envelope")
	}
	return nil
}

func activeRoleState(role domain.Role, state string) bool {
	switch role {
	case domain.Researcher, domain.Planner:
		return state == "planning"
	case domain.Worker:
		return state == "running"
	case domain.Debugger:
		return state == "running" || state == "verifying"
	case domain.QA, domain.Auditor:
		return state == "verifying"
	case domain.Main:
		return state == "draft" || state == "planning" || state == "awaiting_approval" || state == "running" || state == "verifying" || state == "awaiting_delivery" || state == "paused" || state == "blocked" || state == "delivered"
	default:
		return false
	}
}

func supportedInvocationRole(role domain.Role) bool {
	switch role {
	case domain.Main, domain.Researcher, domain.Planner, domain.Worker, domain.Debugger, domain.QA, domain.Auditor:
		return true
	default:
		return false
	}
}

func sameInvocationRequest(existing, replay domain.Invocation) bool {
	replay.Ordinal = existing.Ordinal
	replay.ReservedResultBytes = existing.ReservedResultBytes
	replay.CreatedAt = existing.CreatedAt
	return sameInvocationStoredIntent(existing, replay)
}

func sameInvocationStoredIntent(a, b domain.Invocation) bool {
	return sameJSON(intentFromInvocation(a), intentFromInvocation(b))
}

func terminalInvocationMatches(v domain.Invocation, epoch int64, status string, result *domain.EvidenceEnvelope, safeError string, usage domain.CallUsage) bool {
	return v.Epoch == epoch && v.Status == status && sameJSON(v.Result, result) && v.ErrorCode == safeError && v.UsageKnown == usage.UsageKnown && v.InputTokens == usage.InputTokens && v.OutputTokens == usage.OutputTokens && sameJSON(v.ModelReceipt, usage.ModelReceipt)
}

func validateModelReceipt(r *domain.ModelCallReceipt, status string) error {
	if r == nil {
		return nil
	}
	if *r != domain.SanitizeModelCallReceipt(*r) {
		return errors.New("invalid model receipt metadata")
	}
	if r.ResponseDiagnostic.Version != 0 {
		want := "unknown"
		if r.ResponseDiagnostic.Reason == "provider_failed" || r.ResponseDiagnostic.Reason == "provider_error" {
			want = "rejected"
		}
		// A concurrent cancellation may conservatively demote rejection to unknown.
		if r.HTTPStatus != 200 || r.Certainty != want && !(want == "rejected" && r.Certainty == "unknown") {
			return errors.New("response diagnostic disagrees with outcome")
		}
	}
	if status == "succeeded" && r.Certainty == "completed" || status == "failed" && (r.Certainty == "not_sent" || r.Certainty == "rejected") || status == "outcome_unknown" && r.Certainty == "unknown" || status == "interrupted_before_dispatch" && r.Certainty == "not_sent" {
		return nil
	}
	return errors.New("model receipt certainty disagrees with outcome")
}

func safeLedgerText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max && !strings.ContainsRune(value, 0) && !strings.ContainsFunc(value, unicode.IsControl)
}

func validSafeCode(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

func constraintConflict(err error) error {
	if err == nil {
		return nil
	}
	if isConstraint(err) {
		return ErrConflict
	}
	return err
}
