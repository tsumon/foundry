package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/mcp"
	"foundry/internal/store"
	"foundry/internal/workspace"
)

const mcpFixtureDefinition = `{"inputSchema":{"additionalProperties":false,"properties":{"query":{"type":"string"}},"required":["query"],"type":"object"},"name":"lookup"}`
const mcpFixturePolicy = `{"policy_version":"mcp_read_v1","reviewed":true,"schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}`
const mcpFixtureSource = `{"tool_id":"lookup","arguments":{"query":"alpha"}}`

type localMCPServer struct {
	mu              sync.Mutex
	requests, calls int
	sse             bool
	result          string
	onCall          func(http.ResponseWriter, *http.Request)
	onList          func()
}

func (f *localMCPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()
	if r.Method == "DELETE" {
		w.WriteHeader(204)
		return
	}
	var request struct {
		ID     int             `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		w.WriteHeader(400)
		return
	}
	if request.Method != "initialize" && (r.Header.Get("Mcp-Session-Id") != "fixture-session" || r.Header.Get("MCP-Protocol-Version") != domain.MCPProtocol) {
		w.WriteHeader(400)
		return
	}
	result := ""
	switch request.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "fixture-session")
		result = `{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"local fixture","version":"1"}}`
	case "notifications/initialized":
		w.WriteHeader(202)
		return
	case "tools/list":
		if f.onList != nil {
			f.onList()
		}
		result = `{"tools":[` + mcpFixtureDefinition + `]}`
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(request.Params, &params)
		if params.Name != "lookup" || string(params.Arguments) != `{"query":"alpha"}` {
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		f.calls++
		f.mu.Unlock()
		if f.onCall != nil {
			f.onCall(w, r)
			return
		}
		result = f.result
		if result == "" {
			result = `{"content":[{"type":"text","text":"local observation"}]}`
		}
	default:
		w.WriteHeader(400)
		return
	}
	if f.sse && request.Method == "tools/call" {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":%s}\n\n", request.ID, result)
	} else {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, request.ID, result)
	}
}
func (f *localMCPServer) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests, f.calls
}

type panicMCPRunner struct{ fakeRunner }

func (*panicMCPRunner) Reconcile(context.Context) error { panic("MCP recovery invoked Docker") }

type mcpSecrets struct {
	value string
	gets  int
}

func (*mcpSecrets) Set(string, string) error     { return nil }
func (s *mcpSecrets) Get(string) (string, error) { s.gets++; return s.value, nil }
func (*mcpSecrets) Delete(string) error          { return nil }
func hashMCPFixture(raw []byte) string           { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// Only test fixtures create the fenced mode. The public Engine.Create stays closed.
func mcpEngineFixture(t *testing.T, endpoint, output string, secret ...string) (*Engine, *store.Store, domain.Mission, domain.Invocation) {
	t.Helper()
	return mcpEngineFixtureWithSkills(t, endpoint, output, nil, secret...)
}

func mcpEngineFixtureWithSkills(t *testing.T, endpoint, output string, frozenSkills []domain.SkillRevision, secret ...string) (*Engine, *store.Store, domain.Mission, domain.Invocation) {
	t.Helper()
	_, _, _, _, base := setup(t)
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	e := New(s, nil, Options{DataDir: dir, EnableLocalMCP: true, Runner: &panicMCPRunner{}})
	t.Cleanup(func() { e.Close(); s.Close() })
	connection := domain.MCPConnection{ID: "local", Name: "local fixture", Endpoint: endpoint, Protocol: domain.MCPProtocol, Enabled: true}
	if len(secret) > 0 {
		connection.CredentialRef = "fixture-secret"
		connection.CredentialRevision = 1
		e.options.MCPSecrets = &mcpSecrets{value: secret[0]}
	}
	c, err := s.SaveMCPConnection(connection, 0)
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := mcp.Discover(context.Background(), endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	op := domain.DiscoveryOperation{ID: "discovery", ConnectionID: c.ID, ConnectionRevision: c.Revision, Endpoint: c.Endpoint, CredentialRevision: c.CredentialRevision, Consent: true, AuthorizedAt: now(), Budget: domain.FixedDiscoveryBudget()}
	if _, _, err = s.BeginDiscovery(op); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimDiscovery(op.ID); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.MCPToolSnapshot{ID: "snapshot", ConnectionID: c.ID, ConnectionRevision: c.Revision, Protocol: domain.MCPProtocol, ServerName: discovery.ServerName, ServerVersion: discovery.ServerVersion, Tools: discovery.Tools, Digest: discovery.Digest, ObservedAt: discovery.ObservedAt, ExecutionVersion: 1}
	if _, err = s.FinishDiscovery(op.ID, "succeeded", &snapshot, discovery.Requests, discovery.ResponseBytes, ""); err != nil {
		t.Fatal(err)
	}
	m := base
	m.ID = "mcp-mission"
	m.Revision = 0
	m.Epoch = 1
	m.State = "planning"
	m.Stage = "research"
	m.WorkspaceMode = domain.WorkspaceModeControlledMCPReadV1
	m.ToolMode = domain.ToolModeControlledMCPReadV1
	m.TaskBindings = &domain.TaskBindings{Git: domain.GitSourceBinding{RepositoryID: "fixture-repo", RepositoryRevision: 1, ObservationID: "fixture-observation", Root: m.RepoPath, BaseCommit: m.BaseCommit}, Skills: []domain.SkillBinding{}, PolicyVersion: "repo_read_v1", Tools: []string{"repo.list", "repo.read", "repo.search"}, Budget: domain.FixedReadBudget()}
	for _, fixtureSkill := range frozenSkills {
		saved, err := s.SaveSkillRevision(fixtureSkill, 0)
		if err != nil {
			t.Fatal(err)
		}
		m.TaskBindings.Skills = append(m.TaskBindings.Skills, domain.SkillBinding{ID: saved.ID, Revision: saved.Revision, Name: saved.Name, Digest: saved.Digest, Role: domain.Researcher})
	}
	tool := snapshot.Tools[0]
	m.MCPBindings = &domain.MCPTaskBindings{ConnectionID: c.ID, ConnectionRevision: c.Revision, Endpoint: c.Endpoint, Protocol: c.Protocol, DiscoveryOperationID: op.ID, SnapshotID: snapshot.ID, SnapshotDigest: snapshot.Digest, Tools: []domain.MCPToolBinding{{Name: tool.Name, DefinitionDigest: tool.DefinitionDigest, InputSchemaDigest: hashMCPFixture(tool.InputSchema), Policy: json.RawMessage(mcpFixturePolicy)}}, Budget: domain.FixedMCPBudget()}
	m.MCPBindings.CredentialRef = c.CredentialRef
	m.MCPBindings.CredentialRevision = c.CredentialRevision
	if err = os.MkdirAll(filepath.Join(dir, "missions"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err = workspace.Snapshot(context.Background(), m.RepoPath, e.dir(m.ID), m.Spec.Scope); err != nil {
		t.Fatal(err)
	}
	auth, err := mcpAuthority(m, filepath.Join(e.dir(m.ID), "base"))
	if err != nil {
		t.Fatal(err)
	}
	m.Approvals = []domain.Approval{{Kind: "snapshot", Digest: auth, CreatedAt: now()}, {Kind: "context", Digest: auth, CreatedAt: now()}}
	m, err = s.CreateMission(m)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := domain.MCPBindingDigest(*m.TaskBindings, *m.MCPBindings)
	if err != nil {
		t.Fatal(err)
	}
	m, g, err := s.CreateReadGrant(domain.ReadGrant{ID: "read-grant", MissionID: m.ID, Epoch: m.Epoch, SessionID: "session", Role: domain.Researcher, RequiredState: "planning", BindingDigest: binding, AuthorityDigest: auth}, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	v := domain.Invocation{ID: "source", MissionID: m.ID, Epoch: m.Epoch, SessionID: g.SessionID, Role: domain.Researcher, Kind: "model", Name: "model.complete", BindingDigest: binding, AuthorityDigest: auth, GrantID: g.ID}
	v.Input, err = e.seal(v, "model_input", []byte(`{}`), "application/json", false)
	if err != nil {
		t.Fatal(err)
	}
	m, v, err = s.BeginInvocation(v, m.Revision)
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
	if output != "" {
		result, err := e.seal(v, "model_output", []byte(output), "text/plain; charset=utf-8", false)
		if err != nil {
			t.Fatal(err)
		}
		v, err = s.FinishInvocation(v.ID, v.Epoch, "succeeded", &result, "", domain.CallUsage{})
		if err != nil {
			t.Fatal(err)
		}
	}
	m, err = s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return e, s, m, v
}

func TestMCPControllerLocalJSONAndSSEObservationOnly(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprint(sse), func(t *testing.T) {
			server := &localMCPServer{sse: sse}
			httpServer := httptest.NewServer(server)
			defer httpServer.Close()
			e, s, m, source := mcpEngineFixture(t, httpServer.URL, mcpFixtureSource)
			m, p, err := e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if m.Stage != "awaiting_tool_approval" || e.active != nil {
				t.Fatal("waiting owns slot", m.Stage)
			}
			m, _, v, err := e.DecideMCPProposal(context.Background(), m.ID, m.Revision, p.ID, p.Digest, "decision", true)
			if err != nil {
				t.Fatal(err)
			}
			v, err = e.DispatchMCPInvocation(context.Background(), m.ID, m.Revision, v.ID)
			if err != nil {
				t.Fatal(err)
			}
			if v.Status != "succeeded" || v.Result == nil || v.Result.Kind != "mcp_result" {
				t.Fatal(v)
			}
			raw, err := e.evidenceStore().Read(*v.Result)
			if err != nil || hashMCPFixture(raw) != v.Result.Digest {
				t.Fatal(err)
			}
			got, _ := s.GetMission(m.ID)
			_, calls := server.counts()
			if calls != 1 || got.Stage != "mcp_observation_ready" || got.State != "planning" || got.Research != nil || got.Plan != nil || got.Delivery != nil || len(got.Evidence) != 0 || len(got.Approvals) != 2 {
				t.Fatal("observation promoted", got, calls)
			}
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			retained, _ := s.GetMission(m.ID)
			if retained.Revision != got.Revision || retained.Epoch != got.Epoch {
				t.Fatal("verified observation checkpoint changed")
			}
			blob := filepath.Join(e.options.DataDir, "evidence", "blobs", v.Result.Digest[:2], v.Result.Digest)
			if err = os.WriteFile(blob, []byte("corrupt result"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			corrupt, _ := s.GetMission(m.ID)
			if corrupt.State != "blocked" || corrupt.Stage != "mcp_corrupt" || corrupt.Epoch != got.Epoch+1 {
				t.Fatal("corrupt observation retained", corrupt)
			}
		})
	}
}

func TestMCPControllerDefaultDisabledAndUntrustedSource(t *testing.T) {
	server := &localMCPServer{}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	for _, kind := range []string{"disabled", "corrupt", "missing-context", "unknown-field", "case-alias", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			output := mcpFixtureSource
			if kind == "unknown-field" {
				output = `{"tool_id":"lookup","arguments":{"query":"alpha"},"approved":true}`
			}
			if kind == "case-alias" {
				output = `{"Tool_id":"lookup","arguments":{"query":"alpha"}}`
			}
			if kind == "duplicate" {
				output = `{"tool_id":"lookup","tool_id":"lookup","arguments":{"query":"alpha"}}`
			}
			e, s, m, v := mcpEngineFixture(t, httpServer.URL, output)
			secrets := &mcpSecrets{}
			e.options.MCPSecrets = secrets
			if kind == "disabled" {
				e.options.EnableLocalMCP = false
			}
			if kind == "corrupt" {
				path := filepath.Join(e.options.DataDir, "evidence", "blobs", v.Result.Digest[:2], v.Result.Digest)
				if err := os.WriteFile(path, []byte("corrupted"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "missing-context" {
				var err error
				m, err = s.UpdateMission(m.ID, m.Revision, "fixture", func(m *domain.Mission) error { m.Approvals = m.Approvals[:1]; return nil })
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := server.counts()
			if _, _, err := e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, v.ID); err == nil {
				t.Fatal("denial accepted")
			}
			after, _ := server.counts()
			p, _ := s.ListMCPProposals(m.ID)
			if after != before || len(p) != 0 || secrets.gets != 0 {
				t.Fatal("denial side effects")
			}
		})
	}
}
