package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"foundry/internal/domain"
)

func persistedExecutionSnapshot(t *testing.T, c domain.MCPConnection, id string) *domain.MCPToolSnapshot {
	t.Helper()
	raw := json.RawMessage(`{"inputSchema":{"type":"object"},"name":"lookup","x-extension":{"n":9007199254740993}}`)
	definitionHash := sha256.Sum256(raw)
	tool := domain.MCPTool{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`), RawDefinition: raw, DefinitionDigest: hex.EncodeToString(definitionHash[:])}
	snapshot := &domain.MCPToolSnapshot{ID: id, ConnectionID: c.ID, ConnectionRevision: c.Revision, Protocol: domain.MCPProtocol, ServerName: "fixture", ServerVersion: "1", Tools: []domain.MCPTool{tool}, ObservedAt: "2026-10-18T00:00:00Z", ExecutionVersion: 1}
	tools, err := json.Marshal(snapshot.Tools)
	if err != nil {
		t.Fatal(err)
	}
	snapshotHash := sha256.Sum256(tools)
	snapshot.Digest = hex.EncodeToString(snapshotHash[:])
	return snapshot
}

func persistSnapshot(t *testing.T, s *Store, c domain.MCPConnection, operationID string, snapshot *domain.MCPToolSnapshot) {
	t.Helper()
	if _, _, err := s.BeginDiscovery(discoveryIntent(c, operationID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimDiscovery(operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishDiscovery(operationID, "succeeded", snapshot, 3, 42, ""); err != nil {
		t.Fatal(err)
	}
}

func TestExactExecutionSnapshotRemainsReadableAfterNewerDiscovery(t *testing.T) {
	s := openTestStore(t)
	c := capabilityConnection(t, s)
	old := persistedExecutionSnapshot(t, c, "old-snapshot")
	persistSnapshot(t, s, c, "old-operation", old)
	newer := persistedExecutionSnapshot(t, c, "new-snapshot")
	persistSnapshot(t, s, c, "new-operation", newer)

	got, err := s.GetMCPExecutionSnapshot(c.ID, old.ID)
	if err != nil || got.ID != old.ID || string(got.Tools[0].RawDefinition) != string(old.Tools[0].RawDefinition) {
		t.Fatalf("exact historical snapshot replaced: %+v %v", got, err)
	}
	legacyView, err := s.GetMCPToolSnapshot(old.ID)
	if err != nil || legacyView.ID != old.ID {
		t.Fatalf("exact display read failed: %+v %v", legacyView, err)
	}
}

func TestLegacySnapshotIsDisplayOnly(t *testing.T) {
	s := openTestStore(t)
	c := capabilityConnection(t, s)
	legacy := discoverySnapshot(c, "legacy-snapshot")
	persistSnapshot(t, s, c, "legacy-operation", legacy)
	if got, err := s.GetMCPToolSnapshot(legacy.ID); err != nil || got.ID != legacy.ID {
		t.Fatalf("legacy display lost: %+v %v", got, err)
	}
	if _, err := s.GetMCPExecutionSnapshot(c.ID, legacy.ID); err == nil {
		t.Fatal("legacy catalog granted execution authority")
	}
	if _, err := s.GetMCPExecutionSnapshot("other-connection", legacy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ownership mismatch leaked snapshot: %v", err)
	}
}

func TestExactSnapshotReaderRejectsUntrustedSQLLinkage(t *testing.T) {
	for name, tamper := range map[string]func(*testing.T, *Store, domain.MCPConnection, *domain.MCPToolSnapshot){
		"embedded identity": func(t *testing.T, s *Store, _ domain.MCPConnection, snapshot *domain.MCPToolSnapshot) {
			if _, err := s.db.Exec(`DROP TRIGGER snapshot_no_update`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE mcp_tool_snapshots SET data=json_set(data,'$.id','forged') WHERE id=?`, snapshot.ID); err != nil {
				t.Fatal(err)
			}
		},
		"operation status": func(t *testing.T, s *Store, _ domain.MCPConnection, snapshot *domain.MCPToolSnapshot) {
			if _, err := s.db.Exec(`DROP TRIGGER discovery_transition`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE discovery_operations SET status='failed' WHERE snapshot_id=?`, snapshot.ID); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t)
			c := capabilityConnection(t, s)
			snapshot := persistedExecutionSnapshot(t, c, "trusted-snapshot")
			persistSnapshot(t, s, c, "trusted-operation", snapshot)
			tamper(t, s, c, snapshot)
			if _, err := s.GetMCPToolSnapshot(snapshot.ID); err == nil {
				t.Fatal("untrusted SQL linkage accepted")
			}
		})
	}
}
