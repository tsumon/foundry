package store

import (
	"errors"
	"foundry/internal/domain"
	"path/filepath"
	"testing"
	"time"
)

func TestMCPRecoveryPreservesWaitingAndClassifiesDispatchMarker(t *testing.T) {
	for _, phase := range []string{"waiting", "pending", "preflight", "call_started"} {
		t.Run(phase, func(t *testing.T) {
			s := openTestStore(t)
			m, g := mcpStoreFixture(t, s, phase)
			m, p := mcpProposalFixture(t, s, m, g, phase)
			epoch := m.Epoch
			var v domain.MCPInvocation
			var err error
			if phase != "waiting" {
				m, _, v, err = s.DecideMCPProposal(m.ID, p.ID, p.Digest, "approve", m.Revision, true)
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == "preflight" || phase == "call_started" {
				v, err = s.ClaimMCPInvocation(m.ID, v.ID, v.Epoch)
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == "call_started" {
				v, err = s.MarkMCPCallStarted(m.ID, v.ID, v.Epoch)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := s.RecoverMCPInvocations(m.ID); err != nil {
				t.Fatal(err)
			}
			recovered, err := s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "waiting" {
				if recovered.Epoch != epoch || recovered.Stage != "awaiting_tool_approval" || recovered.State != "planning" {
					t.Fatalf("waiting proposal discarded: %+v", recovered)
				}
				return
			}
			got, err := s.GetMCPInvocation(m.ID, v.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"pending": "interrupted_before_dispatch", "preflight": "interrupted_before_call", "call_started": "outcome_unknown"}[phase]
			if got.Status != want || got.ReservedResultBytes != 64<<10 || recovered.State != "blocked" {
				t.Fatalf("unsafe recovery: %+v %+v", got, recovered)
			}
			revision := recovered.Revision
			if err := s.RecoverMCPInvocations(m.ID); err != nil {
				t.Fatal(err)
			}
			again, _ := s.GetMission(m.ID)
			if again.Revision != revision {
				t.Fatal("recovery replay changed mission")
			}
		})
	}
}

func TestMCPRecoveryFutureVersionRemainsUntouchedAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m, g := mcpStoreFixture(t, s, "future")
	m, p := mcpProposalFixture(t, s, m, g, "future")
	if _, err := s.db.Exec(`DROP TRIGGER mcp_call_proposals_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE mcp_call_proposals SET version=2 WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.GetMCPProposal(m.ID, p.ID); !errors.Is(err, ErrMCPFutureVersion) {
		t.Fatalf("future version enabled: %v", err)
	}
	if err := s.RecoverMCPInvocations(m.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMission(m.ID)
	if got.Revision != m.Revision || got.Epoch != m.Epoch {
		t.Fatal("future version mutated")
	}
	got = resetMCPPlanning(t, s, m.ID)
	v := modelInvocation("future-must-stay-disabled", got, g, g.BindingDigest)
	v.SessionID = g.SessionID
	if _, _, err := s.BeginInvocation(v, got.Revision); !errors.Is(err, ErrMCPFutureVersion) {
		t.Fatalf("future ledger permitted a new call: %v", err)
	}
}

func TestMCPRecoveryInfrastructureErrorIsNotLedgerCorruption(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "recovery-storage")
	m, p := mcpProposalFixture(t, s, m, g, "recovery-storage")
	// Break the generic source reader while retaining each MCP table, so the
	// local reconciliation reaches a real SQL failure rather than corrupt JSON.
	if _, err := s.db.Exec(`ALTER TABLE invocations RENAME TO unavailable_invocations`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverMCPInvocations(m.ID); err == nil {
		t.Fatal("SQL failure mislabeled as isolated corruption")
	}
	after, _ := s.GetMission(m.ID)
	if after.Revision != m.Revision {
		t.Fatalf("SQL failure mutated mission for proposal %s", p.ID)
	}
}

func TestMCPRecoveryRetainsSucceededSourceForExactMaterialization(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "materialize")
	v := modelInvocation("source-materialize", m, g, g.BindingDigest)
	v.SessionID = g.SessionID
	m, v, err := s.BeginInvocation(v, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m, _, err = s.RegisterMCPProposalSource(m.ID, v.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.ClaimInvocation(v.ID, v.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	envelope := controlledEnvelope("model_output", v.ID, m.ID, g.BindingDigest, `{"tool_id":"lookup","arguments":{}}`)
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "succeeded", &envelope, "", domain.CallUsage{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverMCPInvocations(m.ID); err != nil {
		t.Fatal(err)
	}
	recovered, _ := s.GetMission(m.ID)
	if recovered.Epoch != m.Epoch || recovered.Stage != "awaiting_mcp_materialization" || recovered.State != "planning" {
		t.Fatalf("source lost: %+v", recovered)
	}
	next := modelInvocation("cannot-recall", recovered, g, g.BindingDigest)
	next.SessionID = g.SessionID
	if _, _, err := s.BeginInvocation(next, recovered.Revision); err == nil {
		t.Fatal("model recalled while materialization pending")
	}
}

func TestMCPRecoveryIsolatesTrustedOwnerAndListsFailClosed(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "corrupt")
	m, p := mcpProposalFixture(t, s, m, g, "corrupt")
	healthy, hg := mcpStoreFixture(t, s, "healthy")
	healthy, _ = mcpProposalFixture(t, s, healthy, hg, "healthy")
	if _, err := s.db.Exec(`DROP TRIGGER mcp_call_proposals_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE mcp_call_proposals SET data=json_set(data,'$.mission_id','healthy') WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ListMCPProposals(m.ID); err == nil || rows != nil {
		t.Fatal("corruption returned partial ledger")
	}
	if err := s.RecoverMCPInvocations(m.ID); err != nil {
		t.Fatal(err)
	}
	bad, _ := s.GetMission(m.ID)
	good, _ := s.GetMission(healthy.ID)
	if bad.State != "blocked" || good.Revision != healthy.Revision || good.State != "planning" {
		t.Fatalf("corruption escaped owner: bad=%+v good=%+v", bad, good)
	}
}

func TestMCPRecoveryRejectsMalformedTransitionHistory(t *testing.T) {
	for _, corruption := range []string{"bad_status", "bad_phase", "bad_time", "reverse_time", "missing", "disconnected", "wrong_terminal", "foreign_invocation", "future"} {
		t.Run(corruption, func(t *testing.T) {
			s := openTestStore(t)
			m, g := mcpStoreFixture(t, s, "history-"+corruption)
			m, p := mcpProposalFixture(t, s, m, g, "history-"+corruption)
			m, _, v, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "approve", m.Revision, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ClaimMCPInvocation(m.ID, v.ID, v.Epoch); err != nil {
				t.Fatal(err)
			}
			healthy, hg := mcpStoreFixture(t, s, "healthy-"+corruption)
			healthy, _ = mcpProposalFixture(t, s, healthy, hg, "healthy-"+corruption)
			if _, err := s.db.Exec(`DROP TRIGGER mcp_invocation_transitions_no_update`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`DROP TRIGGER mcp_invocation_transitions_no_delete`); err != nil {
				t.Fatal(err)
			}
			statement := map[string]string{
				"bad_status":         `UPDATE mcp_invocation_transitions SET to_status='invented' WHERE invocation_id=?`,
				"bad_phase":          `UPDATE mcp_invocation_transitions SET phase='invented' WHERE invocation_id=?`,
				"bad_time":           `UPDATE mcp_invocation_transitions SET created_at='not-a-time' WHERE invocation_id=?`,
				"reverse_time":       `UPDATE mcp_invocation_transitions SET created_at='2000-01-01T00:00:00Z' WHERE invocation_id=? AND from_status='authorized_pending'`,
				"missing":            `DELETE FROM mcp_invocation_transitions WHERE invocation_id=?`,
				"disconnected":       `DELETE FROM mcp_invocation_transitions WHERE invocation_id=? AND from_status=''`,
				"wrong_terminal":     `UPDATE mcp_invocation_transitions SET to_status='interrupted_before_dispatch' WHERE invocation_id=? AND from_status='authorized_pending'`,
				"foreign_invocation": `UPDATE mcp_invocation_transitions SET mission_id='healthy-foreign_invocation' WHERE invocation_id=?`,
				"future":             `UPDATE mcp_invocation_transitions SET version=2 WHERE invocation_id=?`,
			}[corruption]
			if _, err := s.db.Exec(statement, v.ID); err != nil {
				t.Fatal(err)
			}
			owner := m.ID
			if corruption == "foreign_invocation" {
				owner = healthy.ID
			}
			var before int
			if err := s.db.QueryRow(`SELECT count(*) FROM mcp_invocation_transitions`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := s.RecoverMCPInvocations(owner); err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetMission(owner)
			call, _ := s.GetMCPInvocation(m.ID, v.ID)
			if corruption == "future" {
				if got.Revision != m.Revision || got.Epoch != m.Epoch {
					t.Fatal("future transition was rewritten")
				}
			} else if got.State != "blocked" || got.Stage != "mcp_corrupt" {
				t.Fatalf("malformed transition history accepted: %+v", got)
			}
			if call.Status != "dispatching" {
				t.Fatal("corrupt history was rewritten during recovery")
			}
			otherID, otherRevision := healthy.ID, healthy.Revision
			if corruption == "foreign_invocation" {
				otherID, otherRevision = m.ID, m.Revision
			}
			other, _ := s.GetMission(otherID)
			if other.Revision != otherRevision {
				t.Fatal("history corruption changed another SQL owner")
			}
			var after int
			s.db.QueryRow(`SELECT count(*) FROM mcp_invocation_transitions`).Scan(&after)
			if after != before {
				t.Fatal("corrupt transition rows were changed")
			}
		})
	}
}

func TestCheckMCPLedgerVersionIncludesDecisionAndTransitionHistory(t *testing.T) {
	for _, table := range []string{"mcp_call_decisions", "mcp_invocation_transitions"} {
		t.Run(table, func(t *testing.T) {
			s := openTestStore(t)
			m, g := mcpStoreFixture(t, s, "future-gate")
			m, p := mcpProposalFixture(t, s, m, g, "future-gate")
			m, _, v, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "approve", m.Revision, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CheckMCPLedgerVersion(m.ID); err != nil {
				t.Fatalf("supported ledger rejected: %v", err)
			}
			if _, err := s.db.Exec(`DROP TRIGGER ` + table + `_no_update`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE `+table+` SET version=2 WHERE mission_id=?`, m.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.CheckMCPLedgerVersion(m.ID); !errors.Is(err, ErrMCPFutureVersion) {
				t.Fatalf("future history not exposed to recovery caller: %v", err)
			}
			if err := s.RecoverMCPInvocations(m.ID); err != nil {
				t.Fatal(err)
			}
			after, err := s.GetMission(m.ID)
			if err != nil || after.Revision != m.Revision || after.Epoch != m.Epoch || after.Stage != m.Stage || after.State != m.State {
				t.Fatalf("future ledger mutated mission: %+v, %v", after, err)
			}
			call, err := s.GetMCPInvocation(m.ID, v.ID)
			if err != nil || call.Status != v.Status || call.Phase != v.Phase {
				t.Fatalf("future ledger mutated call: %+v, %v", call, err)
			}
			if err := s.CheckMCPLedgerVersion(m.ID); !errors.Is(err, ErrMCPFutureVersion) {
				t.Fatalf("future row rewritten: %v", err)
			}
		})
	}
}

func TestMCPTransitionTimestampsCompareParsedFractions(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "fractional-time")
	m, p := mcpProposalFixture(t, s, m, g, "fractional-time")
	_, _, v, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "approve", m.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.ClaimMCPInvocation(m.ID, v.ID, v.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER mcp_invocation_transitions_no_update`); err != nil {
		t.Fatal(err)
	}
	// .1Z sorts lexically after .11Z, but it is the earlier instant. Keep the
	// fixture after the invocation timestamps and exercise the read-only check.
	base := time.Now().UTC().Add(time.Second).Format("2006-01-02T15:04:05")
	if _, err := s.db.Exec(`UPDATE mcp_invocation_transitions SET created_at=CASE WHEN from_status='' THEN ? ELSE ? END WHERE invocation_id=?`, base+".1Z", base+".11Z", v.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.checkMCPTransitionHistory(m.ID, []domain.MCPInvocation{v}); err != nil {
		t.Fatalf("valid chronological fractions rejected: %v", err)
	}
}
