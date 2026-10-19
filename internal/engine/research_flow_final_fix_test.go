package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/store"
)

type recoveryRecordingRunner struct {
	calls int
	err   error
}

func (*recoveryRecordingRunner) Resolve(context.Context, string) (string, error) {
	panic("recovery resolved an image")
}
func (*recoveryRecordingRunner) Run(context.Context, string, string, string, string) (domain.CommandResult, error) {
	panic("recovery started a sandbox")
}
func (r *recoveryRecordingRunner) Reconcile(context.Context) error {
	r.calls++
	return r.err
}

// researchFixture also creates a legacy draft; remove that fixture-only row so
// ordinary recovery cannot hide a missing v2 cleanup call.
func onlyResearchMission(t *testing.T, e *Engine, id string) {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(e.options.DataDir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`DELETE FROM missions WHERE id<>?`, id); err != nil {
		t.Fatal(err)
	}
}

func TestResearchFlowRecoveryExecutionCleanupRepeatedStartup(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[cleanupFails], func(t *testing.T) {
			e, _, m := researchFixture(t)
			onlyResearchMission(t, e, m.ID)
			m, err := e.s.UpdateMission(m.ID, m.Revision, "fixture.approved_execution", func(m *domain.Mission) error {
				m.State = "verifying"
				m.Approvals = append(m.Approvals, domain.Approval{Kind: "execute", Revision: m.Revision, CreatedAt: now()})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			r := &recoveryRecordingRunner{}
			if cleanupFails {
				r.err = errors.New("owned cleanup not confirmed")
			}
			e.options.Runner = r
			for attempt := 1; attempt <= 2; attempt++ {
				e = reopenResearch(t, e)
				err = e.Recover(context.Background())
				if !errors.Is(err, r.err) || (cleanupFails && err == nil) {
					t.Fatalf("startup %d cleanup error: %v", attempt, err)
				}
				if r.calls != attempt {
					t.Fatalf("startup %d cleanup calls=%d", attempt, r.calls)
				}
				got, err := e.s.GetMission(m.ID)
				if err != nil || got.State != "blocked" || got.Budget.UsedCalls != 0 {
					t.Fatalf("recovery did not fence without replay: %+v %v", got, err)
				}
			}
		})
	}
}

func TestResearchFlowRecoveryPureResearchDoesNotCleanup(t *testing.T) {
	e, _, m := researchFixture(t)
	onlyResearchMission(t, e, m.ID)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	r := &recoveryRecordingRunner{}
	e.options.Runner = r
	e = reopenResearch(t, e)
	if err := e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.calls != 0 {
		t.Fatal("pure research acquired sandbox cleanup")
	}
	got, _ := e.s.GetMission(m.ID)
	if got.Stage != "awaiting_tool_approval" || got.Budget.UsedCalls != 1 {
		t.Fatal("pure research replayed or lost waiting proposal")
	}
}

func TestResearchFlowRejectedOutputAllowsExplicitReplan(t *testing.T) {
	e, s, m := researchFixture(t)
	e.gateway = &scripted{outputs: []string{"not a tagged research result"}}
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	if m.State != "blocked" || m.Budget.UsedCalls != 1 {
		t.Fatalf("malformed output did not block and charge: %+v", m)
	}
	old, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(old) != 1 || old[0].Status != "succeeded" {
		t.Fatalf("missing sealed rejected source: %+v %v", old, err)
	}
	e = reopenResearch(t, e)
	s = e.s
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMission(m.ID)
	if m.State != "blocked" || m.Stage == "mcp_corrupt" || m.Budget.UsedCalls != 1 {
		t.Fatalf("rejected output mistaken for corruption: %s/%s", m.State, m.Stage)
	}
	e.gateway = &researchGateway{s: s}
	if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal("explicit replan", err)
	}
	m = researchIdle(t, e, m.ID)
	card, err := e.ResearchMCPDecision(m.ID)
	if err != nil || card == nil || !card.CanDecide {
		t.Fatalf("old rejected output poisoned new decision: %+v %v", card, err)
	}
	if _, err = e.DecideResearchMCP(context.Background(), m.ID, m.Revision, card.ProposalID, card.ProposalDigest, "replanned-decision", true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	if m.State != "awaiting_approval" || m.Plan == nil || m.Budget.UsedCalls != 4 {
		t.Fatalf("replan did not reach Planner with old charge: %s %s calls=%d", m.State, m.Error, m.Budget.UsedCalls)
	}
	if _, err = e.InvocationDetail(m.ID, old[0].ID); err != nil {
		t.Fatal("old reservation not inspectable", err)
	}
	if _, err = e.ResearchMCPHistory(m.ID); err != nil {
		t.Fatal("history unreadable", err)
	}
	e = reopenResearch(t, e)
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ = e.s.GetMission(m.ID)
	if m.State != "awaiting_approval" || m.Stage == "mcp_corrupt" || m.Budget.UsedCalls != 4 {
		t.Fatalf("restart poisoned rejected history: %s/%s", m.State, m.Stage)
	}
}

func TestResearchFlowRejectedSourceIntegrityStillFailsClosed(t *testing.T) {
	for _, kind := range []string{"digest", "envelope", "owner", "future", "forged-application"} {
		t.Run(kind, func(t *testing.T) {
			e, _, m := researchFixture(t)
			m, source := prepareResearchCheckpoint(t, e, m, "rejected model output")
			// Integrity-only reads accept intact unaccepted text; materialization
			// must still refuse to turn it into an application.
			if _, err := e.ResearchMCPHistory(m.ID); err != nil {
				t.Fatal("intact unapplied response is not corrupt", err)
			}
			if _, _, err := e.applyResearchSource(m, source.ID); err == nil {
				t.Fatal("malformed current source materialized")
			}
			db := fixtureSQL(t, e)
			var err error
			switch kind {
			case "digest":
				err = os.WriteFile(filepath.Join(e.options.DataDir, "evidence", "blobs", source.Result.Digest[:2], source.Result.Digest), []byte("tampered model output"), 0600)
			case "envelope":
				_, err = db.Exec(`DROP TRIGGER invocation_transition`)
				if err == nil {
					_, err = db.Exec(`UPDATE invocations SET result=json_set(result,'$.mission_id','wrong-owner') WHERE id=?`, source.ID)
				}
			case "owner", "future":
				_, err = db.Exec(`DROP TRIGGER mcp_proposal_sources_no_update`)
				if err == nil && kind == "owner" {
					_, err = db.Exec(`UPDATE mcp_proposal_sources SET session_id='wrong-session' WHERE model_invocation_id=?`, source.ID)
				} else if err == nil {
					_, err = db.Exec(`UPDATE mcp_proposal_sources SET version=99 WHERE model_invocation_id=?`, source.ID)
				}
			case "forged-application":
				// The Store owns structural linkage, not parsing. A structurally
				// valid forged final must still fail the Engine's semantic audit.
				m, _, err = e.s.ApplyResearchTurn(domain.ResearchTurnApplication{MissionID: m.ID, Epoch: source.Epoch, SessionID: source.SessionID, SourceInvocationID: source.ID, SourceResult: *source.Result, Kind: "final", Research: &domain.Research{Summary: "forged final", Findings: []domain.Finding{}, Unknowns: []string{}}}, nil, m.Revision)
			}
			if err != nil {
				t.Fatal("tamper fixture", err)
			}
			if _, err = e.ResearchMCPHistory(m.ID); err == nil {
				t.Fatal("corrupt history accepted")
			}
			if kind == "future" && !errors.Is(err, store.ErrMCPFutureVersion) {
				t.Fatal("future version not fenced", err)
			}
			before, _ := json.Marshal(m)
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := e.s.GetMission(m.ID)
			if kind == "future" {
				after, _ := json.Marshal(got)
				if string(before) != string(after) {
					t.Fatal("future history rewritten")
				}
			} else if got.State != "blocked" || got.Stage != "mcp_corrupt" {
				t.Fatalf("corruption not isolated: %s/%s", got.State, got.Stage)
			}
		})
	}
}

func TestResearchFlowRecoveryFutureExecutionHistoryDoesNotCleanup(t *testing.T) {
	for _, kind := range []string{"workspace", "ledger"} {
		t.Run(kind, func(t *testing.T) {
			e, _, m := researchFixture(t)
			onlyResearchMission(t, e, m.ID)
			m, _ = prepareResearchCheckpoint(t, e, m, `{"kind":"final","research":{"summary":"No claims","findings":[],"unknowns":[]}}`)
			m, err := e.s.UpdateMission(m.ID, m.Revision, "fixture.execution_history", func(m *domain.Mission) error {
				m.State = "verifying"
				m.Approvals = append(m.Approvals, domain.Approval{Kind: "execute", Revision: m.Revision, CreatedAt: now()})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			db := fixtureSQL(t, e)
			if kind == "workspace" {
				_, err = db.Exec(`DROP TRIGGER immutable_workspace_mode`)
				if err == nil {
					_, err = db.Exec(`UPDATE missions SET data=json_set(data,'$.workspace_mode','future-generations') WHERE id=?`, m.ID)
				}
			} else {
				_, err = db.Exec(`DROP TRIGGER mcp_proposal_sources_no_update`)
				if err == nil {
					_, err = db.Exec(`UPDATE mcp_proposal_sources SET version=99 WHERE mission_id=?`, m.ID)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			m, _ = e.s.GetMission(m.ID)
			before, _ := json.Marshal(m)
			r := &recoveryRecordingRunner{}
			e.options.Runner = r
			e = reopenResearch(t, e)
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := e.s.GetMission(m.ID)
			after, _ := json.Marshal(got)
			if r.calls != 0 || string(before) != string(after) {
				t.Fatal("future format acquired cleanup or was rewritten")
			}
		})
	}
}

func TestResearchFlowPauseWaitingWhileOtherMissionActive(t *testing.T) {
	e, s, a := researchFixture(t)
	if _, err := e.PlanControlled(context.Background(), a.ID, a.Revision, true); err != nil {
		t.Fatal(err)
	}
	a = researchIdle(t, e, a.ID)
	r := CreateRequest{Title: "other research", Goal: a.Goal, RepoPath: a.RepoPath, Spec: a.Spec, WorkspaceMode: a.WorkspaceMode, ToolMode: a.ToolMode,
		CapabilitySelection: &domain.CapabilitySelection{RepositoryID: "source", RepositoryRevision: 1, ObservationID: "observed"}}
	selection, err := e.LocalMCPTestSelection()
	if err != nil {
		t.Fatal(err)
	}
	r.MCPSelection = selection.Selection
	b, err := e.Create(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	g := &scripted{outputs: []string{`{"kind":"final","research":{"summary":"B research","findings":[],"unknowns":[]}}`, plan}, entered: make(chan struct{}, 2), release: make(chan struct{})}
	e.gateway = g
	defer close(g.release)
	if _, err = e.PlanControlled(context.Background(), b.ID, b.Revision, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("B never entered model")
	}
	e.mu.Lock()
	owner := e.active
	e.mu.Unlock()
	paused, err := e.Pause(context.Background(), a.ID, a.Revision)
	if err != nil {
		t.Fatal("waiting A could not pause while B owns slot", err)
	}
	if paused.State != "paused" || paused.Epoch != a.Epoch+1 || paused.Revision != a.Revision+1 {
		t.Fatalf("A not independently fenced: %+v", paused)
	}
	e.mu.Lock()
	unchanged := e.active == owner && owner.id == b.ID && owner.ctx.Err() == nil
	e.mu.Unlock()
	if !unchanged {
		t.Fatal("pausing A changed/cancelled B")
	}
	for _, id := range []string{a.ID, b.ID} {
		calls, err := s.ListMCPInvocations(id)
		if err != nil || len(calls) != 0 {
			t.Fatal("pause dispatched MCP", err)
		}
		models, err := s.ListInvocations(id, 0, 100)
		if err != nil || len(models) != 1 {
			t.Fatal("pause dispatched another model", err)
		}
	}
}
