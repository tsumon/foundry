package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"foundry/internal/domain"
)

func (s *Store) BeginLocalOperation(op domain.LocalOperation) (domain.LocalOperation, error) {
	zero := domain.LocalOperation{}
	if !lowerHex(op.ID, 32) || !lowerHex(op.Before.ID, 32) || !lowerHex(op.Before.Digest, 64) || !lowerHex(op.BaseDigest, 64) || !lowerHex(op.IntentDigest, 64) || !lowerHex(op.ApprovalDigest, 64) || op.ExpectedRevision < 1 || op.Epoch < 0 || op.PlanRevision < 0 {
		return zero, errors.New("invalid local operation identity or intent")
	}
	if op.Kind != "apply" && op.Kind != "reset" && op.Kind != "export" {
		return zero, errors.New("invalid local operation kind")
	}
	if op.Result != nil || (op.Status != "" && op.Status != "pending") || op.Error != "" {
		return zero, errors.New("new operation must be pending without result")
	}
	if (op.Kind == "export" && strings.TrimSpace(op.ExportReport) == "") || (op.Kind != "export" && op.ExportReport != "") {
		return zero, errors.New("export report input must be fixed before dispatch")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	if _, err = operationMission(tx, op); err != nil {
		return zero, err
	}
	op.Status = "pending"
	op.CreatedAt = timestamp()
	op.UpdatedAt = op.CreatedAt
	intent, err := json.Marshal(op)
	if err != nil {
		return zero, err
	}
	_, err = tx.Exec(`INSERT INTO local_operations(id,mission_id,intent,status,updated_at) VALUES(?,?,?,'pending',?)`, op.ID, op.MissionID, intent, op.UpdatedAt)
	if err != nil {
		if isConstraint(err) {
			return zero, ErrConflict
		}
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return op, nil
}

func (s *Store) PrepareLocalOperation(id string, result domain.LocalOperationResult) (domain.LocalOperation, error) {
	zero := domain.LocalOperation{}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	op, err := readOperation(tx, id)
	if err != nil {
		return zero, err
	}
	if op.Status != "pending" && !(op.Status == "interrupted" && op.Kind == "export") {
		return zero, ErrConflict
	}
	if op.Result != nil && !sameJSON(*op.Result, result) {
		return zero, errors.New("prepared export result is immutable")
	}
	if err = validateOperationResult(op, result); err != nil {
		return zero, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return zero, err
	}
	op.Status = "prepared"
	op.Result = &result
	op.Error = ""
	op.UpdatedAt = timestamp()
	if _, err = tx.Exec(`UPDATE local_operations SET status='prepared',result=?,error='',updated_at=? WHERE id=?`, raw, op.UpdatedAt, id); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return op, nil
}

func (s *Store) CommitLocalOperation(id, event string, mutate func(*domain.Mission) error) (domain.Mission, error) {
	zero := domain.Mission{}
	if mutate == nil || strings.TrimSpace(event) == "" {
		return zero, errors.New("mutation and event are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	op, err := readOperation(tx, id)
	if err != nil {
		return zero, err
	}
	if op.Status != "prepared" || op.Result == nil {
		return zero, ErrConflict
	}
	if err = validateOperationResult(op, *op.Result); err != nil {
		return zero, err
	}
	if _, err = operationMission(tx, op); err != nil {
		return zero, err
	}
	m, err := updateMissionTx(tx, op.MissionID, op.ExpectedRevision, event, func(m *domain.Mission) error {
		if err := mutate(m); err != nil {
			return err
		}
		return validateOperationSelection(op, *m)
	})
	if err != nil {
		return zero, err
	}
	if _, err = tx.Exec(`UPDATE local_operations SET status='committed',updated_at=? WHERE id=?`, m.UpdatedAt, id); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		// A failed COMMIT may have succeeded on disk. Query the authoritative
		// status before reporting failure; never redispatch a filesystem action.
		_ = tx.Rollback()
		stored, readErr := s.GetLocalOperation(id)
		if readErr == nil && stored.Status == "committed" {
			return s.GetMission(op.MissionID)
		}
		return zero, fmt.Errorf("commit local operation: %w", err)
	}
	return m, nil
}

func (s *Store) InterruptLocalOperation(id, reason string) (domain.LocalOperation, error) {
	zero := domain.LocalOperation{}
	if strings.TrimSpace(reason) == "" {
		return zero, errors.New("interruption reason is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	op, err := readOperation(tx, id)
	if err != nil {
		return zero, err
	}
	if op.Status != "pending" && op.Status != "prepared" {
		return zero, ErrConflict
	}
	op.Status = "interrupted"
	op.Error = reason
	op.UpdatedAt = timestamp()
	if _, err = tx.Exec(`UPDATE local_operations SET status='interrupted',error=?,updated_at=? WHERE id=?`, reason, op.UpdatedAt, id); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return op, nil
}

func (s *Store) GetLocalOperation(id string) (domain.LocalOperation, error) {
	return readOperation(s.db, id)
}

func (s *Store) ListLocalOperations(missionID string) ([]domain.LocalOperation, error) {
	if _, err := s.GetMission(missionID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT intent,status,result,error,updated_at FROM local_operations WHERE mission_id=? ORDER BY rowid`, missionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.LocalOperation{}
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, op)
	}
	return result, rows.Err()
}

type operationQuerier interface{ QueryRow(string, ...any) *sql.Row }
type operationScanner interface{ Scan(...any) error }

func readOperation(q operationQuerier, id string) (domain.LocalOperation, error) {
	return scanOperation(q.QueryRow(`SELECT intent,status,result,error,updated_at FROM local_operations WHERE id=?`, id))
}

// Use the caller's transaction: engine snapshot checks also run inside mission
// mutations, where opening a second connection would deadlock the single writer.
func validateRecoveredSelections(q operationQuerier, m domain.Mission) error {
	for _, a := range m.RunArchives {
		if a.RecoveredSelection == nil {
			continue
		}
		if err := domain.ValidateRecoveredSelection(m, a); err != nil {
			return err
		}
		op, err := readOperation(q, a.RecoveredSelection.ID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(&op, a.RecoveredSelection) {
			return errors.New("recovered local operation changed")
		}
	}
	return nil
}

func scanOperation(row operationScanner) (domain.LocalOperation, error) {
	var op domain.LocalOperation
	var intent, result []byte
	var status, reason, updated string
	err := row.Scan(&intent, &status, &result, &reason, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return op, ErrNotFound
	}
	if err != nil {
		return op, err
	}
	if err = json.Unmarshal(intent, &op); err != nil {
		return op, err
	}
	op.Status = status
	op.Error = reason
	op.UpdatedAt = updated
	if len(result) > 0 {
		op.Result = &domain.LocalOperationResult{}
		if err = json.Unmarshal(result, op.Result); err != nil {
			return op, err
		}
	}
	return op, nil
}

func operationMission(tx *sql.Tx, op domain.LocalOperation) (domain.Mission, error) {
	var raw []byte
	var revision int64
	err := tx.QueryRow(`SELECT revision,data FROM missions WHERE id=?`, op.MissionID).Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Mission{}, ErrNotFound
	}
	if err != nil {
		return domain.Mission{}, err
	}
	m, err := decodeMission(raw)
	if err != nil {
		return m, err
	}
	if !domain.UsesGenerations(m.WorkspaceMode) {
		return m, errors.New("local operation requires generations_v1")
	}
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return m, err
	}
	base, _, err := domain.BaseWorkspaceMode(m.WorkspaceMode)
	if err != nil {
		return m, err
	}
	if base == domain.WorkspaceModeControlledMCPReadV2 {
		if !domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) || !isMCPMission(m) {
			return m, ErrConflict
		}
		if err = supportedMCPLedger(tx, m.ID); err != nil {
			return m, err
		}
		binding, err := domain.MCPBindingDigest(*m.TaskBindings, *m.MCPBindings)
		if err != nil {
			return m, err
		}
		if op.Kind == "reset" {
			found := false
			for i := len(m.Approvals) - 1; i >= 0; i-- {
				if m.Approvals[i].Kind == "snapshot" {
					found = m.Approvals[i].Digest == op.ApprovalDigest
					break
				}
			}
			if !found {
				return m, ErrConflict
			}
		} else if err = mcpRoleAuthority(m, binding, op.ApprovalDigest, domain.Worker); err != nil {
			return m, err
		}
	}
	planRevision := int64(0)
	if m.Plan != nil {
		planRevision = m.Plan.Revision
	}
	if revision != op.ExpectedRevision || m.Revision != op.ExpectedRevision || m.Epoch != op.Epoch || planRevision != op.PlanRevision || m.CandidateGeneration == nil || *m.CandidateGeneration != op.Before || m.CandidateDigest != op.Before.Digest {
		return m, ErrConflict
	}
	return m, nil
}

func validateOperationResult(op domain.LocalOperation, r domain.LocalOperationResult) error {
	if op.Kind == "apply" || op.Kind == "reset" {
		if r.Generation == nil || r.Export != nil || r.Generation.ID != op.ID || !lowerHex(r.Generation.Digest, 64) {
			return errors.New("generation result does not match operation")
		}
		if op.Kind == "reset" && len(r.Changes) > 0 {
			return errors.New("reset result must clear changes")
		}
		return nil
	}
	if op.Kind != "export" || r.Generation != nil || len(r.Changes) != 0 || r.Export == nil {
		return errors.New("export result does not match operation")
	}
	e := r.Export
	if strings.TrimSpace(e.PatchPath) == "" || strings.TrimSpace(e.ReportPath) == "" || e.PatchPath == e.ReportPath || !lowerHex(e.PatchSHA256, 64) || !lowerHex(e.ReportSHA256, 64) || e.ReportInput != op.ExportReport || e.ReportSHA256 != sha256Text(op.ExportReport) {
		return errors.New("export result differs from persisted input")
	}
	return nil
}

func validateOperationSelection(op domain.LocalOperation, m domain.Mission) error {
	r := op.Result
	if op.Kind == "export" {
		e := r.Export
		d := m.Delivery
		if m.CandidateGeneration == nil || *m.CandidateGeneration != op.Before || m.CandidateDigest != op.Before.Digest || d == nil || d.OperationID != op.ID || d.CandidateDigest != op.Before.Digest || d.PatchPath != e.PatchPath || d.ReportPath != e.ReportPath || d.PatchSHA256 != e.PatchSHA256 || d.ReportSHA256 != e.ReportSHA256 {
			return errors.New("delivery does not match prepared export")
		}
	} else if m.CandidateGeneration == nil || *m.CandidateGeneration != *r.Generation || m.CandidateDigest != r.Generation.Digest || !slices.Equal(m.Changes, r.Changes) {
		return errors.New("candidate selection does not match prepared generation")
	}
	return nil
}

func lowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func sha256Text(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func sameJSON(a, b any) bool {
	ar, ae := json.Marshal(a)
	br, be := json.Marshal(b)
	return ae == nil && be == nil && reflect.DeepEqual(ar, br)
}
