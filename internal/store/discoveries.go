package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"

	"foundry/internal/domain"
	"foundry/internal/mcp"
)

// Only these fields form the immutable authorized intent. Runtime fields supplied
// by callers are never accepted as evidence of dispatch or completion.
type discoveryIntentRecord struct {
	ID                 string                 `json:"id"`
	ConnectionID       string                 `json:"connection_id"`
	ConnectionRevision int64                  `json:"connection_revision"`
	Endpoint           string                 `json:"endpoint"`
	CredentialRevision int64                  `json:"credential_revision"`
	Consent            bool                   `json:"consent"`
	AuthorizedAt       string                 `json:"authorized_at"`
	Budget             domain.DiscoveryBudget `json:"budget"`
}

func immutableDiscoveryIntent(op domain.DiscoveryOperation) discoveryIntentRecord {
	return discoveryIntentRecord{op.ID, op.ConnectionID, op.ConnectionRevision, op.Endpoint, op.CredentialRevision, op.Consent, op.AuthorizedAt, op.Budget}
}

const discoverySelect = `SELECT intent,status,requests,response_bytes,snapshot_id,error_code,created_at,finished_at FROM discovery_operations`

type discoveryScanner interface{ Scan(...any) error }

func scanDiscovery(row discoveryScanner) (domain.DiscoveryOperation, error) {
	var op domain.DiscoveryOperation
	var intent []byte
	var requests, responseBytes sql.NullInt64
	err := row.Scan(&intent, &op.Status, &requests, &responseBytes, &op.SnapshotID, &op.ErrorCode, &op.CreatedAt, &op.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return op, ErrNotFound
	}
	if err != nil {
		return op, err
	}
	if err = json.Unmarshal(intent, &op); err != nil {
		return op, err
	}
	if requests.Valid {
		n := int(requests.Int64)
		op.Requests = &n
	}
	if responseBytes.Valid {
		n := responseBytes.Int64
		op.ResponseBytes = &n
	}
	return op, nil
}

func (s *Store) GetDiscovery(id string) (domain.DiscoveryOperation, error) {
	return scanDiscovery(s.db.QueryRow(discoverySelect+` WHERE id=?`, id))
}

func (s *Store) BeginDiscovery(op domain.DiscoveryOperation) (domain.DiscoveryOperation, bool, error) {
	zero := domain.DiscoveryOperation{}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, false, err
	}
	defer tx.Rollback()
	existing, err := scanDiscovery(tx.QueryRow(discoverySelect+` WHERE id=?`, op.ID))
	if err == nil {
		before, _ := json.Marshal(immutableDiscoveryIntent(existing))
		replay := immutableDiscoveryIntent(op)
		// Authorization time is server metadata, not client request intent.
		// Keep the first persisted time even when an HTTP replay supplies now.
		replay.AuthorizedAt = existing.AuthorizedAt
		after, _ := json.Marshal(replay)
		if !bytes.Equal(before, after) {
			return zero, false, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return zero, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return zero, false, err
	}
	if !catalogID(op.ID) || !catalogID(op.ConnectionID) || op.ConnectionRevision < 1 || !validMCPEndpoint(op.Endpoint) || op.CredentialRevision < 0 || !op.Consent || !validCatalogTime(op.AuthorizedAt) || op.Budget != domain.FixedDiscoveryBudget() || (op.Status != "" && op.Status != "authorized_pending") || op.Requests != nil || op.ResponseBytes != nil || op.SnapshotID != "" || op.ErrorCode != "" || op.CreatedAt != "" || op.FinishedAt != "" {
		return zero, false, domain.ErrInvalidCapability
	}
	c, err := capabilityRead[domain.MCPConnection](tx, `SELECT data FROM mcp_connections WHERE id=?`, op.ConnectionID)
	if err != nil {
		return zero, false, err
	}
	if c.Revision != op.ConnectionRevision || c.Endpoint != op.Endpoint || c.CredentialRevision != op.CredentialRevision || !c.Enabled {
		return zero, false, ErrConflict
	}
	op.Status = "authorized_pending"
	op.CreatedAt = timestamp()
	intent, err := json.Marshal(immutableDiscoveryIntent(op))
	if err != nil {
		return zero, false, err
	}
	_, err = tx.Exec(`INSERT INTO discovery_operations(id,connection_id,intent,status,created_at) VALUES(?,?,?,?,?)`, op.ID, op.ConnectionID, intent, op.Status, op.CreatedAt)
	if err != nil {
		return zero, false, capabilityWriteError(err)
	}
	if err = tx.Commit(); err != nil {
		return zero, false, err
	}
	return op, true, nil
}

func (s *Store) ClaimDiscovery(id string) (domain.DiscoveryOperation, error) {
	zero := domain.DiscoveryOperation{}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	op, err := scanDiscovery(tx.QueryRow(discoverySelect+` WHERE id=?`, id))
	if err != nil {
		return zero, err
	}
	if op.Status != "authorized_pending" {
		return zero, ErrConflict
	}
	result, err := tx.Exec(`UPDATE discovery_operations SET status='dispatching' WHERE id=? AND status='authorized_pending'`, id)
	if err != nil {
		return zero, capabilityWriteError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return zero, err
	}
	if n != 1 {
		return zero, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	op.Status = "dispatching"
	return op, nil
}

func validDiscoveryError(code string) bool {
	switch code {
	case "transport_error", "protocol_error", "budget_exceeded", "canceled", "interrupted", "storage_error", "invalid_response", "unsupported_protocol":
		return true
	}
	return false
}

func validToolSnapshot(snapshot domain.MCPToolSnapshot, op domain.DiscoveryOperation) bool {
	if !catalogID(snapshot.ID) || snapshot.ConnectionID != op.ConnectionID || snapshot.ConnectionRevision != op.ConnectionRevision || snapshot.Protocol != domain.MCPProtocol || !validCapabilityDigest(snapshot.Digest) || !validCatalogTime(snapshot.ObservedAt) || snapshot.Tools == nil || len(snapshot.Tools) > op.Budget.MaxTools {
		return false
	}
	if snapshot.ExecutionVersion == 1 {
		return mcp.VerifyExecutionSnapshot(snapshot) == nil
	}
	if snapshot.ExecutionVersion != 0 {
		return false
	}
	names := map[string]bool{}
	for _, tool := range snapshot.Tools {
		if !validCapabilityText(tool.Name, 256, true) || names[tool.Name] || !validCapabilityDigest(tool.DefinitionDigest) || len(tool.InputSchema) == 0 || len(tool.InputSchema) > op.Budget.MaxSchemaBytes || !json.Valid(tool.InputSchema) || len(tool.OutputSchema) > op.Budget.MaxSchemaBytes || (len(tool.OutputSchema) > 0 && !json.Valid(tool.OutputSchema)) || (len(tool.Annotations) > 0 && !json.Valid(tool.Annotations)) {
			return false
		}
		names[tool.Name] = true
	}
	return true
}

// A negative counter (-1) means the actual count cannot be determined. This is
// persisted as NULL, not zero. Failed/unknown outcomes never publish a snapshot.
func (s *Store) FinishDiscovery(id string, status string, snapshot *domain.MCPToolSnapshot, requests int, responseBytes int64, errorCode string) (domain.DiscoveryOperation, error) {
	zero := domain.DiscoveryOperation{}
	if status != "succeeded" && status != "failed" && status != "outcome_unknown" {
		return zero, domain.ErrInvalidCapability
	}
	if requests < -1 || responseBytes < -1 || (status == "succeeded" && (snapshot == nil || errorCode != "" || requests < 0 || responseBytes < 0)) || (status != "succeeded" && (snapshot != nil || !validDiscoveryError(errorCode))) {
		return zero, domain.ErrInvalidCapability
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	op, err := scanDiscovery(tx.QueryRow(discoverySelect+` WHERE id=?`, id))
	if err != nil {
		return zero, err
	}
	if op.Status != "dispatching" {
		return zero, ErrConflict
	}
	if status == "succeeded" && (requests > op.Budget.MaxRequests || responseBytes > op.Budget.MaxTotalResponseBytes) {
		return zero, domain.ErrInvalidCapability
	}
	if snapshot != nil {
		if !validToolSnapshot(*snapshot, op) {
			return zero, domain.ErrInvalidCapability
		}
		c, err := capabilityRead[domain.MCPConnection](tx, `SELECT data FROM mcp_connections WHERE id=?`, op.ConnectionID)
		if err != nil {
			return zero, err
		}
		if c.Revision != op.ConnectionRevision {
			return zero, ErrConflict
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return zero, err
		}
		_, err = tx.Exec(`INSERT INTO mcp_tool_snapshots(id,connection_id,connection_revision,operation_id,data) VALUES(?,?,?,?,?)`, snapshot.ID, snapshot.ConnectionID, snapshot.ConnectionRevision, id, raw)
		if err != nil {
			return zero, capabilityWriteError(err)
		}
		op.SnapshotID = snapshot.ID
	}
	op.Status = status
	op.ErrorCode = errorCode
	op.FinishedAt = timestamp()
	var requestValue, responseValue any
	if requests >= 0 {
		op.Requests = &requests
		requestValue = requests
	}
	if responseBytes >= 0 {
		op.ResponseBytes = &responseBytes
		responseValue = responseBytes
	}
	result, err := tx.Exec(`UPDATE discovery_operations SET status=?,requests=?,response_bytes=?,snapshot_id=?,error_code=?,finished_at=? WHERE id=? AND status='dispatching'`, status, requestValue, responseValue, op.SnapshotID, errorCode, op.FinishedAt, id)
	if err != nil {
		return zero, capabilityWriteError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return zero, err
	}
	if n != 1 {
		return zero, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return op, nil
}

// Startup invokes recovery explicitly before accepting new discovery actions.
// Recovery never claims or retries a request, including authorized-but-unclaimed
// reservations. In-flight actual counts are unknowable after process loss.
func (s *Store) RecoverDiscoveries() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE discovery_operations SET status='outcome_unknown',requests=NULL,response_bytes=NULL,error_code='interrupted',finished_at=? WHERE status IN ('authorized_pending','dispatching')`, timestamp())
	if err != nil {
		return err
	}
	return tx.Commit()
}
