package store

import (
	"database/sql"
	"errors"
	"foundry/internal/domain"
	"strings"
)

// RecoveryMission bounds decoding and checks the trusted row identity. It is
// intentionally read-only even for malformed/unsupported evidence.
func (s *Store) RecoveryMission(id string) (domain.Mission, error) {
	var raw []byte
	var revision int64
	err := s.db.QueryRow(`SELECT revision,CASE WHEN length(data)<=67108864 THEN data ELSE NULL END FROM missions WHERE id=?`, id).Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return domain.Mission{}, err
	}
	m, err := decodeMission(raw)
	if err == nil && (m.ID != id || m.Revision != revision) {
		err = errors.New("mission identity mismatch")
	}
	return m, err
}

// LatestLocalOperation reads one bounded record without scanning history. It
// never changes unfinished rows. Bound the SQL transfer, not only JSON decoding.
func (s *Store) LatestLocalOperation(missionID string) (domain.LocalOperation, error) {
	var id string
	var tooLarge bool
	err := s.db.QueryRow(`SELECT id, length(intent)>16777216 OR length(COALESCE(result,''))>33554432 FROM local_operations WHERE mission_id=? ORDER BY rowid DESC LIMIT 1`, missionID).Scan(&id, &tooLarge)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return domain.LocalOperation{}, err
	}
	if tooLarge {
		return domain.LocalOperation{}, errors.New("local operation exceeds diagnostic bounds")
	}
	op, err := s.RecoveryLocalOperation(id)
	if err == nil && op.MissionID != missionID {
		err = errors.New("local operation mission mismatch")
	}
	return op, err
}

// RecoveryLocalOperation uses the ordinary decoder and pure result validator,
// but never transfers raw operation errors or unbounded JSON to diagnosis.
func (s *Store) RecoveryLocalOperation(id string) (domain.LocalOperation, error) {
	op, err := scanOperation(s.db.QueryRow(`SELECT CASE WHEN length(intent)<=16777216 AND json_extract(intent,'$.mission_id')=mission_id THEN intent ELSE NULL END,status,CASE WHEN length(COALESCE(result,''))<=33554432 THEN result ELSE x'00' END,'',updated_at FROM local_operations WHERE id=?`, id))
	if err != nil {
		return op, err
	}
	bad := errors.New("invalid diagnostic local operation")
	if op.ID != id || !lowerHex(op.ID, 32) || !lowerHex(op.Before.ID, 32) || !lowerHex(op.Before.Digest, 64) || !lowerHex(op.BaseDigest, 64) || !lowerHex(op.IntentDigest, 64) || !lowerHex(op.ApprovalDigest, 64) || op.ExpectedRevision < 1 || op.Epoch < 0 || op.PlanRevision < 0 {
		return op, bad
	}
	if op.Kind != "reset" && op.Kind != "apply" && op.Kind != "export" {
		return op, bad
	}
	switch op.Status {
	case "pending":
		if op.Result != nil {
			return op, bad
		}
	case "prepared", "committed":
		if op.Result == nil {
			return op, bad
		}
	case "interrupted":
	default:
		return op, bad
	}
	if op.Result != nil {
		if err := validateOperationResult(op, *op.Result); err != nil {
			return op, err
		}
	}
	return op, nil
}

// RecoveryInvocations inspects at most 100 bounded ledger envelopes. Oversized
// input is rejected instead of being truncated into apparently valid evidence.
func (s *Store) RecoveryInvocations(id string) ([]domain.Invocation, error) {
	query := strings.Replace(invocationSelect, ",intent,", ",CASE WHEN length(intent)<=262144 THEN intent ELSE NULL END,", 1)
	query = strings.Replace(query, ",result,", ",CASE WHEN length(COALESCE(result,''))<=262144 THEN result ELSE x'00' END,", 1)
	query = strings.Replace(query, ",model_receipt FROM", ",CASE WHEN length(COALESCE(model_receipt,''))<=65536 THEN model_receipt ELSE x'00' END FROM", 1)
	rows, err := s.db.Query(query+` WHERE mission_id=? ORDER BY ordinal DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Invocation{}
	for rows.Next() {
		v, err := scanInvocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
