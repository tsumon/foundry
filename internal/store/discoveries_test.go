package store

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"foundry/internal/domain"
)

func discoveryIntent(c domain.MCPConnection, id string) domain.DiscoveryOperation {
	return domain.DiscoveryOperation{ID: id, ConnectionID: c.ID, ConnectionRevision: c.Revision, Endpoint: c.Endpoint, CredentialRevision: c.CredentialRevision, Consent: true, AuthorizedAt: "2026-10-18T00:00:00Z", Budget: domain.FixedDiscoveryBudget()}
}

// Reusing intent incorrectly or dispatching twice would break exactly-once authorization.
func TestDiscoveryReservationAndSingleClaim(t *testing.T) {
	s := openTestStore(t)
	c := capabilityConnection(t, s)
	intent := discoveryIntent(c, "request")
	first, created, err := s.BeginDiscovery(intent)
	if err != nil || !created {
		t.Fatalf("reserve: %v", err)
	}
	again, created, err := s.BeginDiscovery(intent)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("duplicate was not idempotent: %v", err)
	}
	wrong := intent
	wrong.Endpoint = "https://other.example/mcp"
	if _, _, err = s.BeginDiscovery(wrong); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong intent: %v", err)
	}
	for _, mutate := range []func(*domain.DiscoveryOperation){func(o *domain.DiscoveryOperation) { o.Consent = false }, func(o *domain.DiscoveryOperation) { o.Budget.MaxRequests++ }, func(o *domain.DiscoveryOperation) { o.CredentialRevision++ }} {
		wrong = intent
		mutate(&wrong)
		if _, _, err = s.BeginDiscovery(wrong); !errors.Is(err, ErrConflict) {
			t.Fatalf("immutable intent changed: %v", err)
		}
	}
	other := intent
	other.ID = "second"
	if _, _, err = s.BeginDiscovery(other); !errors.Is(err, ErrConflict) {
		t.Fatalf("parallel reservation: %v", err)
	}
	if _, err = s.SaveMCPConnection(c, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("active edit: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.ClaimDiscovery(first.ID); results <- err }()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("claims success=%d conflicts=%d", successes, conflicts)
	}
	if _, err = s.ClaimDiscovery(first.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("double dispatch allowed")
	}
}

func TestDiscoveryReplayKeepsFirstAuthorizationTime(t *testing.T) {
	s := openTestStore(t)
	c := capabilityConnection(t, s)
	intent := discoveryIntent(c, "same-http-request")
	first, created, err := s.BeginDiscovery(intent)
	if err != nil || !created {
		t.Fatalf("reserve: %+v %v", first, err)
	}
	intent.AuthorizedAt = "2026-10-19T00:00:00Z"
	again, created, err := s.BeginDiscovery(intent)
	if err != nil || created || again.ID != first.ID || again.AuthorizedAt != first.AuthorizedAt || again.CreatedAt != first.CreatedAt {
		t.Fatalf("same client request falsely conflicted or changed time: %+v %v %v", again, created, err)
	}
}

func discoverySnapshot(c domain.MCPConnection, id string) *domain.MCPToolSnapshot {
	return &domain.MCPToolSnapshot{ID: id, ConnectionID: c.ID, ConnectionRevision: c.Revision, Protocol: domain.MCPProtocol, ServerName: "fixture", ServerVersion: "1", Tools: []domain.MCPTool{}, Digest: strings.Repeat("a", 64), ObservedAt: "2026-10-18T00:00:00Z"}
}

// Publishing before commit or replacing on failure destroys the last successful catalog.
func TestDiscoveryAtomicSuccessAndFailedRefresh(t *testing.T) {
	s := openTestStore(t)
	c := capabilityConnection(t, s)
	intent := discoveryIntent(c, "first")
	if _, _, err := s.BeginDiscovery(intent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishDiscovery("first", "succeeded", discoverySnapshot(c, "snap"), 3, 42, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("finished without dispatch: %v", err)
	}
	if _, err := s.ClaimDiscovery("first"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_discovery_finish BEFORE UPDATE ON discovery_operations WHEN NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishDiscovery("first", "succeeded", discoverySnapshot(c, "snap"), 3, 42, ""); err == nil {
		t.Fatal("forced commit path succeeded")
	}
	all, err := s.Capabilities()
	if err != nil || len(all.Snapshots) != 0 {
		t.Fatalf("partial snapshot published: %+v %v", all, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_discovery_finish`); err != nil {
		t.Fatal(err)
	}
	done, err := s.FinishDiscovery("first", "succeeded", discoverySnapshot(c, "snap"), 3, 42, "")
	if err != nil || done.SnapshotID != "snap" || done.Requests == nil || *done.Requests != 3 {
		t.Fatalf("finish: %+v %v", done, err)
	}
	if _, err = s.FinishDiscovery("first", "failed", nil, 0, 0, "protocol_error"); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal rewrite: %v", err)
	}
	intent.ID = "refresh"
	if _, _, err = s.BeginDiscovery(intent); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimDiscovery("refresh"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishDiscovery("refresh", "failed", nil, 2, 10, "protocol_error"); err != nil {
		t.Fatal(err)
	}
	all, err = s.Capabilities()
	if err != nil || len(all.Snapshots) != 1 || all.Snapshots[0].ID != "snap" || len(all.Discoveries) != 2 || all.Discoveries[0].Status != "failed" {
		t.Fatalf("refresh lost last success: %+v %v", all, err)
	}
}

// Recovery must not replay pending work or invent actual usage after restart.
func TestDiscoveryRecoveryPreservesUnknownCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := capabilityConnection(t, s)
	if _, _, err = s.BeginDiscovery(discoveryIntent(c, "pending")); err != nil {
		t.Fatal(err)
	}
	c.ID = "conn2"
	c, err = s.SaveMCPConnection(c, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.BeginDiscovery(discoveryIntent(c, "dispatched")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimDiscovery("dispatched"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.RecoverDiscoveries(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pending", "dispatched"} {
		op, err := s.GetDiscovery(id)
		if err != nil || op.Status != "outcome_unknown" || op.Requests != nil || op.ResponseBytes != nil || op.FinishedAt == "" {
			t.Fatalf("recovery: %+v %v", op, err)
		}
		if _, err = s.ClaimDiscovery(id); !errors.Is(err, ErrConflict) {
			t.Fatalf("replayed: %v", err)
		}
	}
	if err = s.RecoverDiscoveries(); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryRejectsInvalidInputAndSQLTransitions(t *testing.T) {
	s := openTestStore(t)
	c := capabilityConnection(t, s)
	for _, mutate := range []func(*domain.DiscoveryOperation){func(o *domain.DiscoveryOperation) { o.ID = "" }, func(o *domain.DiscoveryOperation) { o.Consent = false }, func(o *domain.DiscoveryOperation) { o.Budget.MaxRequests = 100 }, func(o *domain.DiscoveryOperation) { o.Status = "succeeded" }} {
		op := discoveryIntent(c, "bad")
		mutate(&op)
		if _, _, err := s.BeginDiscovery(op); !errors.Is(err, domain.ErrInvalidCapability) {
			t.Fatalf("invalid intent accepted: %v", err)
		}
	}
	if _, _, err := s.BeginDiscovery(discoveryIntent(c, "req")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE discovery_operations SET status='succeeded' WHERE id='req'`); err == nil {
		t.Fatal("SQL skipped claim")
	}
	if _, err := s.db.Exec(`DELETE FROM discovery_operations WHERE id='req'`); err == nil {
		t.Fatal("SQL deleted authorization")
	}
	if _, err := s.ClaimDiscovery("req"); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"pending", "running", ""} {
		if _, err := s.FinishDiscovery("req", status, nil, 1, 1, "protocol_error"); !errors.Is(err, domain.ErrInvalidCapability) {
			t.Fatalf("bad terminal status: %v", err)
		}
	}
	if _, err := s.FinishDiscovery("req", "failed", nil, 1, 1, "raw server secret"); !errors.Is(err, domain.ErrInvalidCapability) {
		t.Fatalf("unsafe error persisted: %v", err)
	}
}

func TestFailedClaimLeavesReservationRecoverableWithoutRedispatch(t *testing.T) {
	s := openTestStore(t)
	c := capabilityConnection(t, s)
	intent := discoveryIntent(c, "claim-failure")
	if _, _, err := s.BeginDiscovery(intent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_claim BEFORE UPDATE ON discovery_operations WHEN NEW.status='dispatching' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimDiscovery(intent.ID); err == nil {
		t.Fatal("failed claim granted dispatch")
	}
	op, err := s.GetDiscovery(intent.ID)
	if err != nil || op.Status != "authorized_pending" || op.Requests != nil {
		t.Fatalf("failed claim changed ledger: %+v %v", op, err)
	}
	if err = s.RecoverDiscoveries(); err != nil {
		t.Fatal(err)
	}
	again, created, err := s.BeginDiscovery(intent)
	if err != nil || created || again.Status != "outcome_unknown" {
		t.Fatalf("recovery retried intent: %+v %v %v", again, created, err)
	}
	if _, err = s.ClaimDiscovery(intent.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("recovered claim: %v", err)
	}
}

func TestDiscoveryUnknownUsageAndHistoricalVersion(t *testing.T) {
	s := openTestStore(t)
	c := capabilityConnection(t, s)
	if _, _, err := s.BeginDiscovery(discoveryIntent(c, "success")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimDiscovery("success"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishDiscovery("success", "succeeded", discoverySnapshot(c, "snapshot"), 3, 32, ""); err != nil {
		t.Fatal(err)
	}
	c.Name = "Updated config"
	c, err := s.SaveMCPConnection(c, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.BeginDiscovery(discoveryIntent(c, "unknown")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimDiscovery("unknown"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishDiscovery("unknown", "outcome_unknown", nil, -1, -1, "transport_error"); err != nil {
		t.Fatal(err)
	}
	op, err := s.GetDiscovery("unknown")
	if err != nil || op.Requests != nil || op.ResponseBytes != nil {
		t.Fatalf("unknown usage fabricated: %+v %v", op, err)
	}
	all, err := s.Capabilities()
	if err != nil || len(all.Snapshots) != 1 || all.Snapshots[0].ConnectionRevision != 1 || all.Connections[0].Revision != 2 {
		t.Fatalf("historical snapshot relabeled current: %+v %v", all, err)
	}
	if _, err = s.db.Exec(`UPDATE discovery_operations SET intent='{}' WHERE id='unknown'`); err == nil {
		t.Fatal("SQL mutated authorization")
	}
}
