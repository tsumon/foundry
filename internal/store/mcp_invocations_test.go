package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"github.com/mattn/go-sqlite3"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func resetMCPPlanning(t *testing.T, s *Store, id string) domain.Mission {
	t.Helper()
	m, err := s.GetMission(id)
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.UpdateMission(id, m.Revision, "test.new_action", func(m *domain.Mission) error { m.State = "planning"; m.Stage = "plan"; m.Error = ""; return nil })
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// These test transactions deliberately bypass the Go stage fence so the
// reciprocal SQL guards, including rollback of prior writes, are the boundary.
func competingMCPReservation(tx *sql.Tx, m domain.Mission, p domain.MCPProposal, id string) error {
	g := domain.MCPCallGrant{Version: 1, ID: "grant-" + id, MissionID: m.ID, ProposalID: p.ID, ProposalDigest: p.Digest, RequestID: id, ExpectedRevision: m.Revision, Epoch: p.Epoch, SessionID: p.SessionID, BindingDigest: p.BindingDigest, AuthorityDigest: p.AuthorityDigest, Binding: *m.MCPBindings, Profiles: m.Profiles, CreatedAt: timestamp()}
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO mcp_call_grants(id,mission_id,version,epoch,session_id,proposal_id,data,digest) VALUES(?,?,1,?,?,?,?,?)`, g.ID, m.ID, g.Epoch, g.SessionID, p.ID, raw, mcpHash(raw)); err != nil {
		return err
	}
	v := domain.MCPInvocation{Version: 1, ID: id, MissionID: m.ID, Epoch: p.Epoch, SessionID: p.SessionID, Ordinal: 1, ProposalID: p.ID, GrantID: g.ID, BindingDigest: p.BindingDigest, AuthorityDigest: p.AuthorityDigest, ReservedResultBytes: 64 << 10, Status: "authorized_pending", Phase: "preflight", CreatedAt: g.CreatedAt}
	raw, err = json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO mcp_invocations(id,mission_id,version,epoch,session_id,ordinal,proposal_id,grant_id,intent,digest,reserved_result_bytes,status,phase) VALUES(?,?,1,?,?,?,?,?,?,?,?,?,?)`, v.ID, m.ID, v.Epoch, v.SessionID, v.Ordinal, p.ID, g.ID, raw, mcpHash(raw), v.ReservedResultBytes, v.Status, v.Phase); err != nil {
		return err
	}
	return mcpTransition(tx, v, "", v.Status)
}

func competingGenericReservation(tx *sql.Tx, m domain.Mission, g domain.ReadGrant, id string) error {
	v := modelInvocation(id, m, g, g.BindingDigest)
	v.Role = domain.Planner
	v.GrantID = ""
	v.SessionID = g.SessionID
	v.CreatedAt = timestamp()
	v.Status = "authorized_pending"
	if err := tx.QueryRow(`SELECT COALESCE(MAX(ordinal),0)+1 FROM invocations WHERE mission_id=?`, m.ID).Scan(&v.Ordinal); err != nil {
		return err
	}
	raw, err := json.Marshal(intentFromInvocation(v))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO invocations(id,mission_id,epoch,session_id,ordinal,kind,role,name,binding_digest,authority_digest,grant_id,intent,status,reserved_result_bytes,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,0,?)`, v.ID, m.ID, v.Epoch, v.SessionID, v.Ordinal, v.Kind, v.Role, v.Name, v.BindingDigest, v.AuthorityDigest, v.GrantID, raw, v.Status, v.CreatedAt)
	return err
}

func competingReservation(s *Store, m domain.Mission, g domain.ReadGrant, p domain.MCPProposal, kind, id string, acquired, release chan struct{}) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if acquired != nil {
		close(acquired)
		<-release
	}
	if err := insertEvent(tx, m.ID, "test.competing_"+id, m.Revision, timestamp()); err != nil {
		return err
	}
	if kind == "mcp" {
		err = competingMCPReservation(tx, m, p, id)
	} else {
		err = competingGenericReservation(tx, m, g, id)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func TestMCPConcurrentCrossLedgerReservationsBothDirections(t *testing.T) {
	for _, first := range []string{"generic", "mcp"} {
		t.Run(first, func(t *testing.T) {
			s := openTestStore(t)
			m, g := mcpStoreFixture(t, s, "reserve-race-"+first)
			m, p := mcpProposalFixture(t, s, m, g, "reserve-race-"+first)
			second := "generic"
			if first == "generic" {
				second = "mcp"
			}
			acquired, release, contending := make(chan struct{}), make(chan struct{}), make(chan struct{})
			winner, loser := make(chan error, 1), make(chan error, 1)
			go func() { winner <- competingReservation(s, m, g, p, first, "winner", acquired, release) }()
			<-acquired
			waiters := s.db.Stats().WaitCount
			go func() { close(contending); loser <- competingReservation(s, m, g, p, second, "loser", nil, nil) }()
			<-contending
			deadline := time.After(2 * time.Second)
			for s.db.Stats().WaitCount == waiters {
				select {
				case <-deadline:
					close(release)
					t.Fatal("second transaction did not concurrently wait for the held connection")
				default:
					runtime.Gosched()
				}
			}
			close(release)
			if err := <-winner; err != nil {
				t.Fatal(err)
			}
			wantGuard := "active MCP invocation"
			if second == "mcp" {
				wantGuard = "active generic invocation"
			}
			if err := <-loser; err == nil || !strings.Contains(err.Error(), wantGuard) {
				t.Fatalf("losing reservation did not reach reciprocal guard: %v", err)
			}
			var active, markers, grants int
			if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM invocations WHERE mission_id=? AND status IN ('authorized_pending','dispatching'))+(SELECT count(*) FROM mcp_invocations WHERE mission_id=? AND status IN ('authorized_pending','dispatching'))`, m.ID, m.ID).Scan(&active); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT count(*) FROM events WHERE mission_id=? AND kind LIKE 'test.competing_%'`, m.ID).Scan(&markers); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT count(*) FROM mcp_call_grants WHERE mission_id=?`, m.ID).Scan(&grants); err != nil {
				t.Fatal(err)
			}
			wantGrants := 0
			if first == "mcp" {
				wantGrants = 1
			}
			if active != 1 || markers != 1 || grants != wantGrants {
				t.Fatalf("partial racing transaction: active=%d markers=%d grants=%d", active, markers, grants)
			}
			after, _ := s.GetMission(m.ID)
			if after.Revision != m.Revision {
				t.Fatal("SQL race changed mission revision")
			}
		})
	}
}

func TestMCPConcurrentCrossLedgerClaimVersusReservationBothDirections(t *testing.T) {
	for _, claimant := range []string{"generic", "mcp"} {
		t.Run(claimant, func(t *testing.T) {
			s := openTestStore(t)
			m, g := mcpStoreFixture(t, s, "claim-race-"+claimant)
			m, p := mcpProposalFixture(t, s, m, g, "claim-race-"+claimant)
			var id string
			var err error
			contender := "generic"
			if claimant == "mcp" {
				var v domain.MCPInvocation
				m, _, v, err = s.DecideMCPProposal(m.ID, p.ID, p.Digest, "claim-race", m.Revision, true)
				id = v.ID
			} else {
				m = resetMCPPlanning(t, s, m.ID)
				v := modelInvocation("generic-claim-race", m, g, g.BindingDigest)
				v.Role = domain.Planner
				v.GrantID = ""
				m, v, err = s.BeginInvocation(v, m.Revision)
				id = v.ID
				contender = "mcp"
			}
			if err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			claimed, rejected := make(chan error, 1), make(chan error, 1)
			go func() {
				<-start
				var err error
				if claimant == "mcp" {
					_, err = s.ClaimMCPInvocation(m.ID, id, m.Epoch)
				} else {
					_, err = s.ClaimInvocation(id, m.Epoch)
				}
				claimed <- err
			}()
			go func() { <-start; rejected <- competingReservation(s, m, g, p, contender, "claim-loser", nil, nil) }()
			close(start)
			if err := <-claimed; err != nil {
				t.Fatal(err)
			}
			wantGuard := "active MCP invocation"
			if contender == "mcp" {
				wantGuard = "active generic invocation"
			}
			if err := <-rejected; err == nil || !strings.Contains(err.Error(), wantGuard) {
				t.Fatalf("claim contender did not reach reciprocal guard: %v", err)
			}
			var active, markers, grants int
			if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM invocations WHERE mission_id=? AND status='dispatching')+(SELECT count(*) FROM mcp_invocations WHERE mission_id=? AND status='dispatching')`, m.ID, m.ID).Scan(&active); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT count(*) FROM events WHERE mission_id=? AND kind='test.competing_claim-loser'`, m.ID).Scan(&markers); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT count(*) FROM mcp_call_grants WHERE mission_id=?`, m.ID).Scan(&grants); err != nil {
				t.Fatal(err)
			}
			wantGrants := 0
			if claimant == "mcp" {
				wantGrants = 1
			}
			if active != 1 || markers != 0 || grants != wantGrants {
				t.Fatalf("claim race left partial records: active=%d markers=%d grants=%d", active, markers, grants)
			}
			after, _ := s.GetMission(m.ID)
			if after.Revision != m.Revision {
				t.Fatal("failed contender changed mission revision")
			}
		})
	}
}

func TestMCPSourceClaimPropagatesCorruptionAndReadFailure(t *testing.T) {
	for _, failure := range []string{"checksum", "json", "select"} {
		t.Run(failure, func(t *testing.T) {
			s := openTestStore(t)
			m, g := mcpStoreFixture(t, s, "source-error-"+failure)
			v := modelInvocation("source-error-"+failure, m, g, g.BindingDigest)
			v.SessionID = g.SessionID
			m, v, err := s.BeginInvocation(v, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			m, _, err = s.RegisterMCPProposalSource(m.ID, v.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "select":
				// Version checks still work, but selecting the payload digest fails.
				_, err = s.db.Exec(`ALTER TABLE mcp_proposal_sources RENAME COLUMN digest TO unavailable_digest`)
			default:
				if _, err = s.db.Exec(`DROP TRIGGER mcp_proposal_sources_no_update`); err != nil {
					t.Fatal(err)
				}
				if failure == "checksum" {
					_, err = s.db.Exec(`UPDATE mcp_proposal_sources SET digest='tampered' WHERE model_invocation_id=?`, v.ID)
				} else {
					raw := []byte(`{"not":"a source"}`)
					_, err = s.db.Exec(`UPDATE mcp_proposal_sources SET data=?,digest=? WHERE model_invocation_id=?`, raw, mcpHash(raw), v.ID)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.ClaimInvocation(v.ID, v.Epoch)
			if err == nil || errors.Is(err, ErrConflict) {
				t.Fatalf("source failure hidden as permission conflict: %v", err)
			}
			if failure == "select" {
				var databaseError sqlite3.Error
				if !errors.As(err, &databaseError) {
					t.Fatalf("SQL identity lost: %v", err)
				}
			}
			stored, _ := s.GetInvocation(v.ID)
			after, _ := s.GetMission(m.ID)
			if stored.Status != "authorized_pending" || after.Revision != m.Revision {
				t.Fatal("failed claim mutated state")
			}
		})
	}
}

func TestMCPTransitionInsertRejectsMalformedContinuity(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "history-insert")
	m, p := mcpProposalFixture(t, s, m, g, "history-insert")
	_, _, v, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][4]string{{"", "authorized_pending", "preflight", timestamp()}, {"authorized_pending", "succeeded", "call_started", timestamp()}, {"authorized_pending", "dispatching", "wrong", timestamp()}, {"authorized_pending", "dispatching", "preflight", "bad-time"}} {
		if _, err := s.db.Exec(`INSERT INTO mcp_invocation_transitions(invocation_id,mission_id,version,from_status,to_status,phase,created_at) VALUES(?,?,1,?,?,?,?)`, v.ID, m.ID, row[0], row[1], row[2], row[3]); err == nil {
			t.Fatalf("accepted malformed transition %v", row)
		}
	}
}

func failRepository(t *testing.T, s *Store, m domain.Mission, g domain.ReadGrant, id string) domain.Mission {
	t.Helper()
	v := repositoryInvocation(id, m, g, g.BindingDigest)
	v.SessionID = g.SessionID
	_, v, err := s.BeginInvocation(v, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.ClaimInvocation(v.ID, v.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishInvocation(v.ID, v.Epoch, "failed", nil, "read_failed", domain.CallUsage{}); err != nil {
		t.Fatal(err)
	}
	m, err = s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMCPAndRepositoryLifetimeQuotaBothDirections(t *testing.T) {
	for _, last := range []string{"mcp", "repository"} {
		t.Run(last, func(t *testing.T) {
			s := openTestStore(t)
			m, g := mcpStoreFixture(t, s, "quota-"+last)
			var savedP domain.MCPProposal
			var savedV domain.MCPInvocation
			var savedRevision int64
			for i := 0; i < 3; i++ {
				suffix := fmt.Sprintf("%s-%d", last, i)
				if i == 1 {
					m = failRepository(t, s, m, g, "repo-"+suffix)
					continue
				}
				var p domain.MCPProposal
				m, p = mcpProposalFixture(t, s, m, g, suffix)
				savedRevision = m.Revision
				var err error
				m, _, savedV, err = s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request-"+suffix, m.Revision, true)
				if err != nil {
					t.Fatal(err)
				}
				savedP = p
				v, err := s.ClaimMCPInvocation(m.ID, savedV.ID, m.Epoch)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.FinishMCPInvocation(m.ID, v.ID, v.Epoch, domain.MCPCallOutcome{Status: "failed", ErrorCode: "preflight_failed"}); err != nil {
					t.Fatal(err)
				}
				m = resetMCPPlanning(t, s, m.ID)
			}
			if last == "mcp" {
				var p domain.MCPProposal
				m, p = mcpProposalFixture(t, s, m, g, "fourth")
				if _, _, _, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "fourth", m.Revision, true); !errors.Is(err, ErrMCPBudget) {
					t.Fatalf("fourth approval accepted: %v", err)
				}
			} else {
				v := repositoryInvocation("fourth-repo", m, g, g.BindingDigest)
				v.SessionID = g.SessionID
				if _, _, err := s.BeginInvocation(v, m.Revision); !errors.Is(err, ErrMCPBudget) {
					t.Fatalf("fourth repository accepted: %v", err)
				}
			}
			var count int
			if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM mcp_invocations WHERE mission_id=?)+(SELECT count(*) FROM invocations WHERE mission_id=? AND kind='repository')`, m.ID, m.ID).Scan(&count); err != nil || count != 3 {
				t.Fatalf("reservation count=%d err=%v", count, err)
			}
			before, _ := s.GetMission(m.ID)
			after, _, replayed, err := s.DecideMCPProposal(m.ID, savedP.ID, savedP.Digest, "request-"+last+"-2", savedRevision, true)
			if err != nil || replayed.ID != savedV.ID || after.Revision != before.Revision {
				t.Fatalf("quota blocked stable replay: %v", err)
			}
		})
	}
}

func TestMCPApprovalConcurrentReplayAndEventRollback(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "concurrent")
	m, p := mcpProposalFixture(t, s, m, g, "concurrent")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_mcp_event BEFORE INSERT ON events WHEN NEW.kind='mcp.proposal_decided' BEGIN SELECT RAISE(ABORT,'test event failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, true); err == nil {
		t.Fatal("event failure ignored")
	}
	for _, table := range []string{"mcp_call_grants", "mcp_invocations", "mcp_call_decisions"} {
		var count int
		s.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE mission_id=?`, m.ID).Scan(&count)
		if count != 0 {
			t.Fatalf("partial transaction %s", table)
		}
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_mcp_event`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, v, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, true)
			ids <- v.ID
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("concurrent duplicate reservation")
		}
	}
	var claims int
	var lock sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ClaimMCPInvocation(m.ID, first, m.Epoch); err == nil {
				lock.Lock()
				claims++
				lock.Unlock()
			}
		}()
	}
	wg.Wait()
	if claims != 1 {
		t.Fatalf("claims=%d", claims)
	}
}

func TestMCPCurrentConnectionAndCredentialCheckedAtEveryBoundary(t *testing.T) {
	for _, boundary := range []string{"approve", "claim", "call_started"} {
		t.Run(boundary, func(t *testing.T) {
			s := openTestStore(t)
			m, g := mcpStoreFixture(t, s, boundary)
			m, p := mcpProposalFixture(t, s, m, g, boundary)
			var v domain.MCPInvocation
			var err error
			if boundary != "approve" {
				m, _, v, err = s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, true)
				if err != nil {
					t.Fatal(err)
				}
			}
			if boundary == "call_started" {
				v, err = s.ClaimMCPInvocation(m.ID, v.ID, v.Epoch)
				if err != nil {
					t.Fatal(err)
				}
			}
			c, err := s.GetMCPConnection(m.MCPBindings.ConnectionID)
			if err != nil {
				t.Fatal(err)
			}
			c.CredentialRef = "new-opaque-ref"
			c.CredentialRevision++
			if _, err := s.SaveMCPConnection(c, c.Revision); err != nil {
				t.Fatal(err)
			}
			switch boundary {
			case "approve":
				_, _, _, err = s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, true)
			case "claim":
				_, err = s.ClaimMCPInvocation(m.ID, v.ID, v.Epoch)
			default:
				_, err = s.MarkMCPCallStarted(m.ID, v.ID, v.Epoch)
			}
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("stale authority accepted: %v", err)
			}
		})
	}
}

func TestMCPToolErrorEvidenceAndStaleResultNeverAdvanceMission(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "late")
	m, p := mcpProposalFixture(t, s, m, g, "late")
	m, _, v, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimMCPInvocation(m.ID, v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MarkMCPCallStarted(m.ID, v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	m, err = s.UpdateMission(m.ID, m.Revision, "cancel", func(m *domain.Mission) error { m.Epoch++; m.State = "paused"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	evidence := controlledEnvelope("mcp_result", v.ID, m.ID, g.BindingDigest, `{"isError":true}`)
	outcome := domain.MCPCallOutcome{Status: "failed", ErrorCode: "tool_error", Result: &evidence, Requests: 4, RequestBytes: 123, ResponseBytes: 456, DurationMillis: 10}
	terminal, err := s.FinishMCPInvocation(m.ID, v.ID, v.Epoch, outcome)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetMission(m.ID)
	if after.Revision != m.Revision || after.State != "paused" || terminal.Result == nil || terminal.RequestBytes != 123 {
		t.Fatal("late result advanced mission or lost history")
	}
	if _, err := s.FinishMCPInvocation(m.ID, v.ID, v.Epoch, outcome); err != nil {
		t.Fatal(err)
	}
}

func TestMCPRejectionReplayReservesNothing(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "reject")
	m, p := mcpProposalFixture(t, s, m, g, "reject")
	expected := m.Revision
	rejected, grant, v, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "reject", expected, false)
	if err != nil || rejected.State != "blocked" || grant.ID != "" || v.ID != "" {
		t.Fatalf("bad rejection: %v", err)
	}
	again, _, _, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "reject", expected, false)
	if err != nil || again.Revision != rejected.Revision {
		t.Fatal("rejection replay changed state")
	}
	calls, err := s.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 0 {
		t.Fatal("rejection reserved")
	}
}

func TestMCPPreflightCannotForgeDispatchMarkerAtTerminalTransition(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "phase-forge")
	m, p := mcpProposalFixture(t, s, m, g, "phase-forge")
	m, _, v, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimMCPInvocation(m.ID, v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE mcp_invocations SET status='outcome_unknown',phase='call_started',error_code='unknown',finished_at=? WHERE id=?`, timestamp(), v.ID); err == nil {
		t.Fatal("terminal transition forged durable dispatch marker")
	}
}

func TestMCPReadersRejectTrustedColumnSubstitution(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "column")
	m, p := mcpProposalFixture(t, s, m, g, "column")
	if _, err := s.db.Exec(`DROP TRIGGER mcp_call_proposals_no_update`); err != nil {
		t.Fatal(err)
	}
	// A second valid source makes the foreign key valid; the frozen proposal's
	// embedded source must still agree with its SQL-owned source column.
	m = resetMCPPlanning(t, s, m.ID)
	v := modelInvocation("other-source", m, g, g.BindingDigest)
	v.SessionID = g.SessionID
	m, v, err := s.BeginInvocation(v, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RegisterMCPProposalSource(m.ID, v.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE mcp_call_proposals SET source_invocation_id=? WHERE id=?`, v.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMCPProposal(m.ID, p.ID); err == nil {
		t.Fatal("SQL source substitution accepted")
	}
}

func TestMCPReciprocalActiveGuardsAndAtomicTriggerFailure(t *testing.T) {
	for _, first := range []string{"generic", "mcp"} {
		t.Run(first, func(t *testing.T) {
			s := openTestStore(t)
			m, g := mcpStoreFixture(t, s, "active-"+first)
			m, p := mcpProposalFixture(t, s, m, g, "active-"+first)
			if first == "generic" {
				m = resetMCPPlanning(t, s, m.ID)
				v := modelInvocation("active-generic", m, g, g.BindingDigest)
				v.Role = domain.Planner
				v.GrantID = ""
				m, _, err := s.BeginInvocation(v, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				m, err = s.UpdateMission(m.ID, m.Revision, "test.waiting", func(m *domain.Mission) error { m.Stage = "awaiting_tool_approval"; return nil })
				if err != nil {
					t.Fatal(err)
				}
				if _, _, _, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "denied", m.Revision, true); err == nil {
					t.Fatal("MCP overlapped generic model")
				}
				for _, table := range []string{"mcp_call_grants", "mcp_call_decisions", "mcp_invocations"} {
					var count int
					if err := s.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE mission_id=?`, m.ID).Scan(&count); err != nil || count != 0 {
						t.Fatalf("trigger left %s partial row", table)
					}
				}
			} else {
				_, _, _, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "approved", m.Revision, true)
				if err != nil {
					t.Fatal(err)
				}
				// Bypass Go phase checks to exercise reciprocal SQL enforcement directly.
				_, err = s.db.Exec(`INSERT INTO invocations(id,mission_id,epoch,session_id,ordinal,kind,role,name,binding_digest,authority_digest,grant_id,intent,status,reserved_result_bytes,created_at) SELECT 'sql-generic',mission_id,epoch,session_id,ordinal+1,kind,role,name,binding_digest,authority_digest,grant_id,json_set(intent,'$.id','sql-generic','$.ordinal',ordinal+1),'authorized_pending',reserved_result_bytes,created_at FROM invocations WHERE id=?`, p.SourceInvocationID)
				if err == nil {
					t.Fatal("generic SQL reservation overlapped MCP")
				}
			}
		})
	}
}

func TestMCPFinishDoesNotRelabelStorageFailureAsStaleResult(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "finish-storage")
	m, p := mcpProposalFixture(t, s, m, g, "finish-storage")
	m, _, v, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimMCPInvocation(m.ID, v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE mcp_connections RENAME TO unavailable_connections`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishMCPInvocation(m.ID, v.ID, v.Epoch, domain.MCPCallOutcome{Status: "failed", ErrorCode: "preflight_failed"}); err == nil {
		t.Fatal("infrastructure failure silently finalized stale result")
	}
	got, err := s.GetMCPInvocation(m.ID, v.ID)
	if err != nil || got.Status != "dispatching" {
		t.Fatal("failed finalization changed ledger")
	}
}

func TestMCPTransitionRejectsDifferentSQLOwner(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "transition-owner")
	m, p := mcpProposalFixture(t, s, m, g, "transition-owner")
	_, _, v, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := controlledStoreMission(t, s, "other-transition-owner")
	if _, err := s.db.Exec(`INSERT INTO mcp_invocation_transitions(invocation_id,mission_id,version,from_status,to_status,phase,created_at) VALUES(?,?,1,'','authorized_pending','preflight',?)`, v.ID, other.ID, timestamp()); err == nil {
		t.Fatal("MCP transition detached from SQL owner")
	}
}
