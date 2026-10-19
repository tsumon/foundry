package engine

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/mcptest"
	"foundry/internal/model"
	"foundry/internal/store"
	"strings"
	"sync"
	"testing"
	"time"
)

type researchGateway struct {
	s     *store.Store
	calls int
}

func (g *researchGateway) Complete(_ context.Context, r model.Request) (model.Result, error) {
	g.calls++
	text := plan
	if r.Role == domain.Researcher {
		var input struct {
			Extra struct {
				Observations []struct {
					InvocationID string `json:"invocation_id"`
					Digest       string `json:"digest"`
				} `json:"observations"`
			} `json:"extra"`
		}
		json.Unmarshal([]byte(r.Prompt), &input)
		text = `{"kind":"mcp","tool_id":"reference.lookup","arguments":{"topic":"mcp-workflow"},"reason":"Check synthetic workflow reference"}`
		if len(input.Extra.Observations) > 0 {
			o := input.Extra.Observations[0]
			raw, _ := json.Marshal(domain.Research{Summary: "Synthetic reference checked", Findings: []domain.Finding{{Severity: "info", Summary: "Synthetic MCP fact", Paths: []string{}, Sources: []domain.ResearchSourceRef{{Kind: "mcp", InvocationID: o.InvocationID, Digest: o.Digest}}}}, Unknowns: []string{}})
			text = `{"kind":"final","research":` + string(raw) + `}`
		}
	}
	return model.Result{Text: text}, nil
}

func researchFixture(t *testing.T) (*Engine, *store.Store, domain.Mission) {
	t.Helper()
	oldEngine, s, _, runner, old := setup(t)
	fixture, err := mcptest.Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fixture.Close() })
	d := fixture.Descriptor()
	e := New(s, &researchGateway{s: s}, Options{DataDir: oldEngine.options.DataDir, Runner: runner, LocalMCPTest: &d})
	t.Cleanup(func() { e.Close() })
	r := controlledRequest(t, s, old)
	r.WorkspaceMode = domain.WorkspaceModeControlledMCPReadV2
	r.ToolMode = domain.ToolModeControlledMCPReadV2
	view, err := e.LocalMCPTestSelection()
	if err != nil {
		t.Fatal(err)
	}
	r.MCPSelection = view.Selection
	m, err := e.Create(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return e, s, m
}

func researchIdle(t *testing.T, e *Engine, id string) domain.Mission {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		idle := e.active == nil
		e.mu.Unlock()
		if idle {
			m, err := e.s.GetMission(id)
			if err != nil {
				t.Fatal(err)
			}
			return m
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("owned segment did not suspend")
	return domain.Mission{}
}

func TestResearchFlowApprovalToPlan(t *testing.T) {
	e, s, m := researchFixture(t)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	if m.State != "planning" || m.Stage != "awaiting_tool_approval" {
		t.Fatalf("not suspended: %+v", m)
	}
	calls, err := s.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 0 {
		t.Fatal("call before approval", err)
	}
	if _, err = e.Approve(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("tool context authorized execution")
	}
	proposals, err := s.ListMCPProposals(m.ID)
	if err != nil || len(proposals) != 1 {
		t.Fatal("missing exact proposal", err)
	}
	p := proposals[0]
	rev := m.Revision
	ctx, cancel := context.WithCancel(context.Background())
	_, err = e.DecideResearchMCP(ctx, m.ID, rev, p.ID, p.Digest, "decision-one", true)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	if m.State != "awaiting_approval" || m.Research == nil || m.Plan == nil {
		t.Fatalf("no plan: %+v", m)
	}
	if m.Budget.UsedCalls != 3 || len(m.Evidence) != 0 || len(m.Changes) != 0 || m.Delivery != nil {
		t.Fatal("approval or budget bypass")
	}
	models, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range models {
		if v.Role == domain.Planner {
			raw, err := e.evidenceStore().Read(v.Input)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "SYNTHETIC LOCAL TEST:") {
				t.Fatal("raw MCP result disclosed to derived-brief recipient")
			}
		}
	}
	calls, err = s.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 1 || calls[0].Status != "succeeded" {
		t.Fatalf("calls %+v %v", calls, err)
	}
	if _, err = e.DecideResearchMCP(context.Background(), m.ID, rev, p.ID, p.Digest, "decision-one", true); err != nil {
		t.Fatal("replay", err)
	}
	if _, err = e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("unapproved delivery")
	}
}

func TestResearchFlowProtocolRejectsUnauthorizedTurns(t *testing.T) {
	for _, output := range []string{
		`{"kind":"mcp","tool_id":"shell.exec","arguments":{},"reason":"run"}`,
		`{"kind":"mcp","tool_id":"reference.lookup","arguments":{"topic":"mcp-workflow","path":"/etc/passwd"},"reason":"read"}`,
		`{"kind":"mcp","tool_id":"reference.lookup","arguments":{"topic":"mcp-workflow"},"reason":"read","connection_id":"other"}`,
		`{"kind":"mcp","kind":"final","tool_id":"reference.lookup","arguments":{},"reason":"read"}`,
		`{"kind":"repository","tool":"shell.exec","arguments":{}}`,
		`{"kind":"final","research":{"summary":"made up","findings":[{"severity":"info","summary":"fact","paths":[],"sources":[{"kind":"mcp","invocation_id":"missing","digest":"bad"}]}],"unknowns":[]}}`,
	} {
		t.Run(output[:20]+output[len(output)-10:], func(t *testing.T) {
			e, s, m := researchFixture(t)
			e.gateway = &scripted{outputs: []string{output}}
			if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
				t.Fatal(err)
			}
			m = researchIdle(t, e, m.ID)
			calls, _ := s.ListMCPInvocations(m.ID)
			if m.State != "blocked" || len(calls) != 0 || m.Plan != nil {
				t.Fatalf("unsafe output applied: %s", m.State)
			}
		})
	}
}

func TestResearchFlowRepositoryAndExecutionApprovalSeparation(t *testing.T) {
	e, s, m := researchFixture(t)
	e.gateway = &scripted{outputs: []string{`{"kind":"repository","tool":"repo.read","arguments":{"path":"src/main.txt","start_line":1,"max_lines":80}}`, `{"kind":"final","research":` + research + `}`, plan, edit, review, review}}
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	if m.State != "awaiting_approval" || m.Budget.UsedCalls != 3 {
		t.Fatalf("repository planning %s %s", m.State, m.Error)
	}
	op := &operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}
	for _, role := range []domain.Role{domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
		e.mu.Lock()
		_, _, _, err := e.controlledAuthority(op, role)
		e.mu.Unlock()
		if err == nil {
			t.Fatal("context authorized", role)
		}
	}
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	if m.State != "awaiting_delivery" || len(m.Changes) != 1 || len(m.Evidence) != 1 || m.Delivery != nil {
		t.Fatalf("approved execution %s %s", m.State, m.Error)
	}
	if _, err := e.Deliver(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ResearchMCPInvocations(m.ID); err != nil {
		t.Fatal("historical research lost after execution epoch", err)
	}
	if _, err := s.GetMission(m.ID); err != nil {
		t.Fatal(err)
	}
}

func TestResearchFlowPauseIdleAndActive(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "waiting", true: "active"}[active], func(t *testing.T) {
			e, _, m := researchFixture(t)
			var g *scripted
			if active {
				g = &scripted{outputs: []string{`{"kind":"final","research":{"summary":"stopped","findings":[],"unknowns":[]}}`}, entered: make(chan struct{}, 1), release: make(chan struct{})}
				e.gateway = g
			}
			if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
				t.Fatal(err)
			}
			if active {
				<-g.entered
				m, _ = e.s.GetMission(m.ID)
			} else {
				m = researchIdle(t, e, m.ID)
			}
			done := make(chan error, 1)
			go func() { _, err := e.Pause(context.Background(), m.ID, m.Revision); done <- err }()
			if active {
				time.Sleep(10 * time.Millisecond)
				close(g.release)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("pause deadlocked")
			}
			m = researchIdle(t, e, m.ID)
			if m.State != "paused" || m.Research != nil || m.Plan != nil {
				t.Fatal("late result passed pause")
			}
		})
	}
}

func TestResearchFlowSafeHistoryAndDisabledActions(t *testing.T) {
	e, _, m := researchFixture(t)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	card, err := e.ResearchMCPDecision(m.ID)
	if err != nil || card == nil || card.Purpose == "" {
		t.Fatal("missing verified decision", err)
	}
	raw, _ := json.Marshal(card)
	for _, secret := range []string{"credential_ref", "read_grant", "system", "prompt"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("unsafe card", secret)
		}
	}
	p, err := e.s.ListMCPProposals(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.options.LocalMCPTest = nil
	if _, err = e.DecideResearchMCP(context.Background(), m.ID, m.Revision, p[0].ID, p[0].Digest, "disabled", true); err == nil {
		t.Fatal("disabled fixture called")
	}
	if _, err = e.ResearchMCPDecision(m.ID); err != nil {
		t.Fatal("disabled history hidden", err)
	}
	calls, err := e.ResearchMCPInvocations(m.ID)
	if err != nil || len(calls) != 0 {
		t.Fatal("disabled calls history", err)
	}
}

func TestResearchFlowConcurrentDecisionReplaySchedulesOnce(t *testing.T) {
	e, s, m := researchFixture(t)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	proposals, err := s.ListMCPProposals(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := proposals[0]
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.DecideResearchMCP(context.Background(), m.ID, m.Revision, p.ID, p.Digest, "double-click", true)
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	m = researchIdle(t, e, m.ID)
	calls, err := s.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 1 || m.Budget.UsedCalls != 3 || m.State != "awaiting_approval" {
		t.Fatalf("duplicate dispatch: %d %s %v", len(calls), m.State, err)
	}
}
