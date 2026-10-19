package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"foundry/internal/capability"
	"foundry/internal/domain"
	"foundry/internal/mcptest"
	"foundry/internal/model"
	"foundry/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func injectedResearchFixture(t *testing.T, server *localMCPServer) (*Engine, domain.Mission) {
	t.Helper()
	old, s, _, runner, base := setup(t)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	c, err := s.SaveMCPConnection(domain.MCPConnection{ID: "injected-owned", Name: "test-only owned listener", Endpoint: httpServer.URL, Protocol: domain.MCPProtocol, Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	op, err := capability.New(s, nil).Discover(context.Background(), c.ID, c.Revision, "injected-discovery", true)
	if err != nil || op.Status != "succeeded" {
		t.Fatal("discovery", err, op.Status)
	}
	snap, err := s.GetMCPExecutionSnapshot(c.ID, op.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	tool := snap.Tools[0]
	descriptor := mcptest.Descriptor{Binding: domain.MCPTaskBindings{ConnectionID: c.ID, ConnectionRevision: c.Revision, Endpoint: c.Endpoint, Protocol: c.Protocol, DiscoveryOperationID: op.ID, SnapshotID: snap.ID, SnapshotDigest: snap.Digest, Tools: []domain.MCPToolBinding{{Name: tool.Name, DefinitionDigest: tool.DefinitionDigest, InputSchemaDigest: mcpHash(tool.InputSchema), Policy: json.RawMessage(mcpFixturePolicy)}}, Budget: domain.FixedMCPBudget()}, Alive: func() bool { return true }}
	e := New(s, &scripted{outputs: []string{`{"kind":"mcp","tool_id":"lookup","arguments":{"query":"alpha"},"reason":"Synthetic fixture"}`}}, Options{DataDir: old.options.DataDir, Runner: runner, LocalMCPTest: &descriptor})
	t.Cleanup(func() { e.Close() })
	r := controlledRequest(t, s, base)
	r.ToolMode = domain.ToolModeControlledMCPReadV2
	r.WorkspaceMode = domain.WorkspaceModeControlledMCPReadV2
	r.MCPSelection = selectionOf(descriptor.Binding)
	m, err := e.Create(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return e, m
}

func TestResearchFlowValidToolErrorContinuesWithoutFactCitation(t *testing.T) {
	server := &localMCPServer{sse: true, result: `{"content":[{"type":"text","text":"Synthetic lookup unavailable"}],"isError":true}`}
	e, m := injectedResearchFixture(t, server)
	e.gateway = &scripted{outputs: []string{`{"kind":"mcp","tool_id":"lookup","arguments":{"query":"alpha"},"reason":"Synthetic fixture"}`, `{"kind":"final","research":{"summary":"Lookup returned an error, not factual evidence","findings":[],"unknowns":["lookup failed"]}}`, plan}}
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	p, _ := e.s.ListMCPProposals(m.ID)
	if _, err := e.DecideResearchMCP(context.Background(), m.ID, m.Revision, p[0].ID, p[0].Digest, "tool-error", true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	calls, err := e.ResearchMCPInvocations(m.ID)
	if err != nil || len(calls) != 1 || calls[0].Status != "failed" || calls[0].ErrorCode != "tool_error" || calls[0].ResultDigest == "" || m.State != "awaiting_approval" {
		t.Fatalf("error observation not continued %+v %s %s %v", calls, m.State, m.Error, err)
	}
	_, count := server.counts()
	if count != 1 {
		t.Fatal("remote call duplicated")
	}
}

func TestResearchFlowToolErrorCannotSupportFactualCitation(t *testing.T) {
	server := &localMCPServer{result: `{"content":[{"type":"text","text":"lookup failed"}],"isError":true}`}
	e, m := injectedResearchFixture(t, server)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	// This reply intentionally cites the newly supplied tool-error observation.
	e.gateway = &researchGateway{s: e.s}
	p, _ := e.s.ListMCPProposals(m.ID)
	if _, err := e.DecideResearchMCP(context.Background(), m.ID, m.Revision, p[0].ID, p[0].Digest, "error-citation", true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	if m.State != "blocked" || m.Research != nil || m.Plan != nil {
		t.Fatal("error accepted as factual lookup")
	}
}

func TestResearchFlowDispatchBoundaryAndLateResultRecheckOwnedFixture(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "pre-call", true: "late-result"}[late], func(t *testing.T) {
			server := &localMCPServer{}
			e, m := injectedResearchFixture(t, server)
			if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
				t.Fatal(err)
			}
			m = researchIdle(t, e, m.ID)
			var alive atomic.Bool
			alive.Store(true)
			e.options.LocalMCPTest.Alive = alive.Load
			if late {
				server.onCall = func(w http.ResponseWriter, r *http.Request) {
					alive.Store(false)
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"late"}],"isError":false}}`)
				}
			} else {
				server.onList = func() { alive.Store(false) }
			}
			proposals, _ := e.s.ListMCPProposals(m.ID)
			if _, err := e.DecideResearchMCP(context.Background(), m.ID, m.Revision, proposals[0].ID, proposals[0].Digest, "boundary", true); err != nil {
				t.Fatal(err)
			}
			m = researchIdle(t, e, m.ID)
			_, count := server.counts()
			want := 0
			if late {
				want = 1
			}
			if count != want {
				t.Fatal("dispatch boundary violated", count, want)
			}
			calls, err := e.s.ListMCPInvocations(m.ID)
			if err != nil || len(calls) != 1 || calls[0].Result != nil || m.State != "blocked" {
				t.Fatal("late result accepted", err)
			}
		})
	}
}

func TestResearchFlowLocalHTTPModelAndMCP(t *testing.T) {
	e, _, m := researchFixture(t)
	// Use a real loopback HTTP model adapter; its only responses are the fixed
	// local Researcher/Planner protocol. No provider credentials or external API.
	gateway := &researchGateway{s: e.s}
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 2 {
			http.Error(w, "bad", 400)
			return
		}
		role := domain.Researcher
		if gateway.calls == 2 {
			role = domain.Planner
		}
		result, err := gateway.Complete(r.Context(), model.Request{Role: role, Prompt: request.Messages[1].Content})
		if err != nil {
			http.Error(w, "bad", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": result.Text}}}})
	}))
	defer modelServer.Close()
	// Create a fresh mission with an explicitly selected local HTTP profile.
	profile := m.Profiles[domain.Researcher]
	profile.BaseURL = modelServer.URL
	if err := e.s.PutProfile(profile); err != nil {
		t.Fatal(err)
	}
	r := CreateRequest{Title: m.Title, Goal: m.Goal, RepoPath: m.RepoPath, Spec: m.Spec, ToolMode: m.ToolMode, WorkspaceMode: m.WorkspaceMode, CapabilitySelection: &domain.CapabilitySelection{RepositoryID: m.TaskBindings.Git.RepositoryID, RepositoryRevision: m.TaskBindings.Git.RepositoryRevision, ObservationID: m.TaskBindings.Git.ObservationID}, MCPSelection: selectionOf(*m.MCPBindings)}
	var err error
	m, err = e.Create(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	e.gateway = model.NewClient(nil)
	if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	p, _ := e.s.ListMCPProposals(m.ID)
	if len(p) != 1 {
		t.Fatalf("HTTP model proposal failed %s %s", m.State, m.Error)
	}
	if _, err = e.DecideResearchMCP(context.Background(), m.ID, m.Revision, p[0].ID, p[0].Digest, "http-model", true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	if m.State != "awaiting_approval" || gateway.calls != 3 {
		t.Fatalf("HTTP model flow %s %s", m.State, m.Error)
	}
}

func TestResearchFlowKilledAtActualToolsCallNeverReplays(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := &localMCPServer{onCall: func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}}
	e, m := injectedResearchFixture(t, server)
	defer close(release)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	command := exec.Command(os.Args[0], "-test.run=^TestResearchFlowDispatchChild$", "-test.count=1")
	command.Env = append(os.Environ(), "FOUNDRY_RESEARCH_CHILD="+e.options.DataDir, "FOUNDRY_RESEARCH_MISSION="+m.ID)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			command.Process.Kill()
			command.Wait()
		}
	})
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		command.Process.Kill()
		command.Wait()
		t.Fatalf("child did not call: %s", output.String())
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("child not killed")
	}
	e = reopenResearch(t, e)
	before, _ := server.counts()
	if err := e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls, err := e.s.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 1 || calls[0].Status != "outcome_unknown" || calls[0].Phase != "call_started" || calls[0].Result != nil {
		t.Fatalf("crash ledger %+v %v", calls, err)
	}
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, count := server.counts()
	if before != after || count != 1 {
		t.Fatal("recovery sent network", before, after, count)
	}
}

func TestResearchFlowDispatchChild(t *testing.T) {
	dir := os.Getenv("FOUNDRY_RESEARCH_CHILD")
	if dir == "" {
		t.Skip("child-process entry point")
	}
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m, err := s.GetMission(os.Getenv("FOUNDRY_RESEARCH_MISSION"))
	if err != nil {
		t.Fatal(err)
	}
	// Test-only descriptor injection: production startup never obtains ownership
	// from persisted endpoints. Parent explicitly owns this exact HTTP listener.
	descriptor := mcptest.Descriptor{Binding: *m.MCPBindings, Alive: func() bool { return true }}
	e := New(s, nil, Options{DataDir: dir, LocalMCPTest: &descriptor, Runner: &panicMCPRunner{}})
	defer e.Close()
	p, err := s.ListMCPProposals(m.ID)
	if err != nil || len(p) != 1 {
		t.Fatal(err)
	}
	if _, err = e.DecideResearchMCP(context.Background(), m.ID, m.Revision, p[0].ID, p[0].Digest, "killed-request", true); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	op := e.active
	e.mu.Unlock()
	if op == nil {
		t.Fatal("missing owner")
	}
	<-op.finished
	t.Fatal(fmt.Sprintf("expected process kill at tools/call, got completion"))
}
