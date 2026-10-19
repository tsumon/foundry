package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"foundry/internal/domain"
	"foundry/internal/mcp"
)

const exactSnapshotSelect = `SELECT s.id,s.connection_id,s.connection_revision,s.operation_id,s.data,o.id,o.connection_id,o.status,o.snapshot_id,o.intent
FROM mcp_tool_snapshots s JOIN discovery_operations o ON o.id=s.operation_id`

func (s *Store) readMCPToolSnapshot(query string, args ...any) (domain.MCPToolSnapshot, error) {
	var snapshot domain.MCPToolSnapshot
	var sqlID, sqlConnectionID, operationID, opID, opConnectionID, status, opSnapshotID string
	var sqlRevision int64
	var data, intent []byte
	err := s.db.QueryRow(query, args...).Scan(&sqlID, &sqlConnectionID, &sqlRevision, &operationID, &data, &opID, &opConnectionID, &status, &opSnapshotID, &intent)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, ErrNotFound
	}
	if err != nil {
		return snapshot, err
	}
	var authorized struct {
		ID                 string `json:"id"`
		ConnectionID       string `json:"connection_id"`
		ConnectionRevision int64  `json:"connection_revision"`
	}
	if json.Unmarshal(data, &snapshot) != nil || json.Unmarshal(intent, &authorized) != nil || sqlID != snapshot.ID || sqlConnectionID != snapshot.ConnectionID || sqlRevision != snapshot.ConnectionRevision || operationID != opID || status != "succeeded" || opSnapshotID != sqlID || opConnectionID != sqlConnectionID || authorized.ID != opID || authorized.ConnectionID != sqlConnectionID || authorized.ConnectionRevision != sqlRevision {
		return domain.MCPToolSnapshot{}, errors.New("invalid MCP snapshot authority")
	}
	if snapshot.ExecutionVersion != 0 {
		if err := mcp.VerifyExecutionSnapshot(snapshot); err != nil {
			return domain.MCPToolSnapshot{}, err
		}
	}
	return snapshot, nil
}

// GetMCPToolSnapshot performs an exact historical display read. It does not
// claim that a legacy catalog is eligible to execute.
func (s *Store) GetMCPToolSnapshot(id string) (domain.MCPToolSnapshot, error) {
	if !catalogID(id) {
		return domain.MCPToolSnapshot{}, ErrNotFound
	}
	return s.readMCPToolSnapshot(exactSnapshotSelect+` WHERE s.id=?`, id)
}

// GetMCPExecutionSnapshot binds the requested snapshot to trusted SQL owner and
// successful operation columns before verifying its complete execution bytes.
func (s *Store) GetMCPExecutionSnapshot(connectionID, snapshotID string) (domain.MCPToolSnapshot, error) {
	if !catalogID(connectionID) || !catalogID(snapshotID) {
		return domain.MCPToolSnapshot{}, ErrNotFound
	}
	snapshot, err := s.readMCPToolSnapshot(exactSnapshotSelect+` WHERE s.id=? AND s.connection_id=?`, snapshotID, connectionID)
	if err != nil {
		return domain.MCPToolSnapshot{}, err
	}
	if err := mcp.VerifyExecutionSnapshot(snapshot); err != nil {
		return domain.MCPToolSnapshot{}, err
	}
	return snapshot, nil
}
