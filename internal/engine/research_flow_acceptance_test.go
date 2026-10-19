package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/model"
)

// Only the model and sandbox are synthetic. Store, Git snapshots, evidence,
// controller, MCP discovery/transport and all approval transitions are real.
type mixedAcceptanceGateway struct {
	afterMCP chan struct{}
	resume   chan struct{}
}

func (g *mixedAcceptanceGateway) Complete(ctx context.Context, r model.Request) (model.Result, error) {
	if r.Role != domain.Researcher {
		switch r.Role {
		case domain.Planner:
			return model.Result{Text: plan}, nil
		case domain.Worker:
			return model.Result{Text: edit}, nil
		case domain.QA, domain.Auditor:
			return model.Result{Text: review}, nil
		default:
			return model.Result{}, fmt.Errorf("unexpected synthetic role %s", r.Role)
		}
	}
	var input struct {
		Extra struct {
			Observations []struct {
				Kind         string `json:"kind"`
				InvocationID string `json:"invocation_id"`
				Digest       string `json:"digest"`
			} `json:"observations"`
		} `json:"extra"`
	}
	if err := json.Unmarshal([]byte(r.Prompt), &input); err != nil {
		return model.Result{}, err
	}
	switch len(input.Extra.Observations) {
	case 0:
		return model.Result{Text: `{"kind":"repository","tool":"repo.read","arguments":{"path":"src/main.txt","start_line":1,"max_lines":80}}`}, nil
	case 1:
		return model.Result{Text: `{"kind":"mcp","tool_id":"reference.lookup","arguments":{"topic":"mcp-workflow"},"reason":"SYNTHETIC mixed-source approval acceptance"}`}, nil
	case 2:
		close(g.afterMCP)
		select {
		case <-g.resume:
		case <-ctx.Done():
			return model.Result{}, ctx.Err()
		}
		return model.Result{Text: `{"kind":"repository","tool":"repo.search","arguments":{"query":"before","path":"src/main.txt"}}`}, nil
	case 3:
		for _, o := range input.Extra.Observations {
			if o.Kind == "mcp" {
				raw, err := json.Marshal(domain.Research{Summary: "SYNTHETIC mixed-source acceptance", Findings: []domain.Finding{
					{Severity: "info", Summary: "Committed source contains before", Paths: []string{"src/main.txt"}},
					{Severity: "info", Summary: "Synthetic reference inspected, not repository evidence", Paths: []string{}, Sources: []domain.ResearchSourceRef{{Kind: "mcp", InvocationID: o.InvocationID, Digest: o.Digest}}},
				}, Unknowns: []string{"Fake model and runner do not validate cloud inference or Docker"}})
				return model.Result{Text: `{"kind":"final","research":` + string(raw) + `}`}, err
			}
		}
	}
	return model.Result{}, fmt.Errorf("unexpected mixed-source observations")
}

func acceptanceGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo
	raw, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, raw)
	}
	return string(raw)
}

// Catches lost shared quota/session/citations or accidental execution authority
// when repository and MCP observations occur in the same delivered mission.
func TestResearchFlowAcceptanceMixedSourcesThroughDelivery(t *testing.T) {
	e, s, m := researchFixture(t)
	g := &mixedAcceptanceGateway{afterMCP: make(chan struct{}), resume: make(chan struct{})}
	e.gateway = g
	if err := os.WriteFile(filepath.Join(m.RepoPath, "untracked.txt"), []byte("untouched\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitState := func() []string {
		return []string{acceptanceGit(t, m.RepoPath, "rev-parse", "HEAD"), acceptanceGit(t, m.RepoPath, "rev-parse", "HEAD^{tree}"), acceptanceGit(t, m.RepoPath, "status", "--porcelain=v1", "--untracked-files=all")}
	}
	before := gitState()
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	card, err := e.ResearchMCPDecision(m.ID)
	if err != nil || card == nil || card.UsedCalls != 1 || card.RemainingCalls != 2 {
		t.Fatalf("mixed quota at approval: %+v %v", card, err)
	}
	calls, err := s.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 0 || m.Stage != "awaiting_tool_approval" {
		t.Fatal("MCP dispatched without approval", calls, m.Stage, err)
	}
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("context consent authorized execution")
	}
	if _, err := e.DecideResearchMCP(context.Background(), m.ID, m.Revision, card.ProposalID, card.ProposalDigest, "mixed-acceptance", true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-g.afterMCP:
	case <-time.After(5 * time.Second):
		t.Fatal("approved MCP did not reach continuation")
	}
	m, err = s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	// This checkpoint has a real MCP grant/result, but no execution approval.
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("single-call grant authorized plan execution")
	}
	op := &operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}
	for _, role := range []domain.Role{domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
		e.mu.Lock()
		_, _, _, err := e.controlledAuthority(op, role)
		e.mu.Unlock()
		if err == nil {
			t.Fatalf("MCP grant authorized %s", role)
		}
	}
	close(g.resume)
	m = researchIdle(t, e, m.ID)
	if m.State != "awaiting_approval" || m.Plan == nil || m.Research == nil || m.Budget.UsedCalls != 5 || len(m.Evidence) != 0 || len(m.Changes) != 0 || m.Delivery != nil {
		t.Fatalf("planning separation: state=%s error=%s budget=%+v", m.State, m.Error, m.Budget)
	}
	calls, err = s.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 1 || calls[0].Status != "succeeded" || calls[0].Result == nil {
		t.Fatalf("MCP result: %+v %v", calls, err)
	}
	invocations, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	repositoryCalls, researchTurns := 0, 0
	reserved := calls[0].ReservedResultBytes
	for _, v := range invocations {
		if v.Kind == "repository" {
			repositoryCalls++
			reserved += v.ReservedResultBytes
			if v.Status != "succeeded" {
				t.Fatal("repository observation failed", v)
			}
		}
		if v.Kind == "model" && v.Role == domain.Researcher {
			researchTurns++
		}
	}
	if repositoryCalls != 2 || researchTurns != 4 || reserved != 192<<10 {
		t.Fatalf("shared quota: repo=%d turns=%d bytes=%d", repositoryCalls, researchTurns, reserved)
	}
	consumed, err := s.ListResearchConsumptions(m.ID)
	if err != nil || len(consumed) != 3 {
		t.Fatalf("consumptions %+v %v", consumed, err)
	}
	for _, c := range consumed {
		if c.SessionID != calls[0].SessionID || c.MissionID != m.ID {
			t.Fatal("observation escaped original research session", c)
		}
	}
	ref := m.Research.Findings[1].Sources[0]
	if ref.InvocationID != calls[0].ID || ref.Digest != calls[0].Result.Digest || ref.Kind != "mcp" {
		t.Fatal("mixed-source citation lost identity", ref)
	}
	if _, err := e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("research result authorized delivery")
	}
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	if m.State != "awaiting_delivery" || len(m.Changes) != 1 || len(m.Evidence) != 1 || m.Delivery != nil || m.Budget.UsedCalls != 8 {
		t.Fatalf("stub-runner verification: state=%s error=%s", m.State, m.Error)
	}
	m, err = e.Deliver(context.Background(), m.ID, m.Revision)
	if err != nil || m.State != "delivered" || m.Delivery == nil {
		t.Fatalf("delivery: %s %v", m.State, err)
	}
	acceptanceGit(t, m.RepoPath, "apply", "--check", m.Delivery.PatchPath)
	if after := gitState(); !reflect.DeepEqual(before, after) {
		t.Fatalf("source HEAD/tree/status changed: before=%q after=%q", before, after)
	}
	for path, want := range map[string]string{"src/main.txt": "before\n", "untracked.txt": "untouched\n"} {
		raw, err := os.ReadFile(filepath.Join(m.RepoPath, path))
		if err != nil || string(raw) != want {
			t.Fatalf("source changed %s: %q %v", path, raw, err)
		}
	}
}
