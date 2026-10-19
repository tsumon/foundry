package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/store"
)

func TestMCPLocalRecoveryRetainsOnlyVerifiedWaitingEpochs(t *testing.T) {
	for _, kind := range []string{"waiting", "source-gap", "incomplete-model", "pending", "preflight", "call-started", "corrupt-source"} {
		t.Run(kind, func(t *testing.T) {
			server := &localMCPServer{}
			httpServer := httptest.NewServer(server)
			defer httpServer.Close()
			output := mcpFixtureSource
			if kind == "incomplete-model" {
				output = ""
			}
			e, s, m, source := mcpEngineFixture(t, httpServer.URL, output)
			var v domain.MCPInvocation
			if kind != "source-gap" && kind != "incomplete-model" {
				var p domain.MCPProposal
				var err error
				m, p, err = e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, source.ID)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "pending" || kind == "preflight" || kind == "call-started" {
					m, _, v, err = e.DecideMCPProposal(context.Background(), m.ID, m.Revision, p.ID, p.Digest, "decision", true)
					if err != nil {
						t.Fatal(err)
					}
					if kind != "pending" {
						v, err = s.ClaimMCPInvocation(m.ID, v.ID, v.Epoch)
						if err != nil {
							t.Fatal(err)
						}
					}
					if kind == "call-started" {
						v, err = s.MarkMCPCallStarted(m.ID, v.ID, v.Epoch)
						if err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if kind == "corrupt-source" {
				if err := os.WriteFile(filepath.Join(e.options.DataDir, "evidence", "blobs", source.Result.Digest[:2], source.Result.Digest), []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			e.options.EnableLocalMCP = false
			// Reopen the actual database, not just a second in-memory controller.
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(filepath.Join(e.options.DataDir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			e = New(reopened, nil, Options{DataDir: e.options.DataDir, Runner: &panicMCPRunner{}})
			s = reopened
			t.Cleanup(func() { e.Close(); reopened.Close() })
			before, _ := server.counts()
			if err := e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "waiting":
				if got.Stage != "awaiting_tool_approval" || got.Epoch != m.Epoch {
					t.Fatal(got)
				}
			case "source-gap":
				if got.Stage != "awaiting_mcp_materialization" || got.Epoch != m.Epoch {
					t.Fatal(got)
				}
				e.options.EnableLocalMCP = true
				got, p, err := e.MaterializeMCPProposal(context.Background(), got.ID, got.Revision, source.ID)
				if err != nil {
					t.Fatal(err)
				}
				again, p2, err := e.MaterializeMCPProposal(context.Background(), got.ID, got.Revision, source.ID)
				if err != nil || p.ID != p2.ID || again.Revision != got.Revision {
					t.Fatal("replay materialized twice", err)
				}
			default:
				if got.State != "blocked" || got.Epoch != m.Epoch+1 {
					t.Fatal("not fenced", got)
				}
			}
			if v.ID != "" {
				call, err := s.GetMCPInvocation(m.ID, v.ID)
				want := map[string]string{"pending": "interrupted_before_dispatch", "preflight": "interrupted_before_call", "call-started": "outcome_unknown"}[kind]
				if err != nil || call.Status != want {
					t.Fatal(call, err)
				}
			}
			after, _ := server.counts()
			if after != before {
				t.Fatal("recovery made HTTP requests")
			}
			stable, _ := s.GetMission(m.ID)
			raw, _ := json.Marshal(stable)
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			stable, _ = s.GetMission(m.ID)
			afterRaw, _ := json.Marshal(stable)
			if string(raw) != string(afterRaw) {
				t.Fatal("recovery not idempotent")
			}
		})
	}
}

func TestMCPPauseFencesActiveTransportAndReleasesOwnership(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := &localMCPServer{onCall: func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":12,"result":{"content":[{"type":"text","text":"late"}]}}`))
	}}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	defer close(release)
	e, s, m, source := mcpEngineFixture(t, httpServer.URL, mcpFixtureSource)
	m, p, err := e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, _, v, err := e.DecideMCPProposal(context.Background(), m.ID, m.Revision, p.ID, p.Digest, "decision", true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := e.DispatchMCPInvocation(context.Background(), m.ID, m.Revision, v.ID); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no tools/call")
	}
	current, _ := s.GetMission(m.ID)
	paused, err := e.PauseMCP(context.Background(), m.ID, current.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("canceled invocation succeeded")
	}
	call, err := s.GetMCPInvocation(m.ID, v.ID)
	if err != nil || call.Status != "outcome_unknown" || call.Result != nil || paused.State != "paused" || paused.Epoch != m.Epoch+1 || e.active != nil {
		t.Fatal(paused, call, err)
	}
}

func TestMCPAllActiveAPIsDefaultOffBeforeSecrets(t *testing.T) {
	server := &localMCPServer{}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	e, s, m, source := mcpEngineFixture(t, httpServer.URL, mcpFixtureSource, "fake-key")
	m, p, err := e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, _, v, err := e.DecideMCPProposal(context.Background(), m.ID, m.Revision, p.ID, p.Digest, "decision", true)
	if err != nil {
		t.Fatal(err)
	}
	e.options.EnableLocalMCP = false
	secrets := e.options.MCPSecrets.(*mcpSecrets)
	beforeGets := secrets.gets
	beforeRequests, _ := server.counts()
	if _, _, err = e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, source.ID); err == nil {
		t.Fatal("materialize enabled")
	}
	if _, _, _, err = e.DecideMCPProposal(context.Background(), m.ID, m.Revision, p.ID, p.Digest, "other", true); err == nil {
		t.Fatal("decide enabled")
	}
	if _, err = e.DispatchMCPInvocation(context.Background(), m.ID, m.Revision, v.ID); err == nil {
		t.Fatal("dispatch enabled")
	}
	if _, err = e.PauseMCP(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("pause enabled")
	}
	got, _ := s.GetMission(m.ID)
	afterRequests, _ := server.counts()
	if got.Revision != m.Revision || secrets.gets != beforeGets || afterRequests != beforeRequests {
		t.Fatal("disabled side effects")
	}
}

func TestMCPSourceCredentialReflectionAndArgumentBounds(t *testing.T) {
	server := &localMCPServer{}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	for _, kind := range []string{"literal", "escaped", "primitive", "oversize", "missing"} {
		t.Run(kind, func(t *testing.T) {
			secret := "fake-secret"
			output := `{"tool_id":"lookup","arguments":{"query":"fake-secret"}}`
			if kind == "escaped" {
				output = `{"tool_id":"lookup","arguments":{"query":"fake-\u0073ecret"}}`
			}
			if kind == "primitive" {
				secret = "true"
				output = `{"tool_id":"lookup","arguments":{"query":"true"}}`
			}
			if kind == "oversize" {
				output = `{"tool_id":"lookup","arguments":{"query":"` + strings.Repeat("x", 8192) + `"}}`
			}
			if kind == "missing" {
				output = mcpFixtureSource
				secret = ""
			}
			e, s, m, v := mcpEngineFixture(t, httpServer.URL, output, secret)
			before, _ := server.counts()
			if _, _, err := e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, v.ID); err == nil {
				t.Fatal("invalid source persisted")
			}
			rows, err := s.ListMCPProposals(m.ID)
			after, _ := server.counts()
			if err != nil || len(rows) != 0 || after != before {
				t.Fatal(rows, err)
			}
		})
	}
}

func TestMCPResponsePersistenceFailuresConsumeUnknownAttempt(t *testing.T) {
	for _, kind := range []string{"seal", "database"} {
		t.Run(kind, func(t *testing.T) {
			server := &localMCPServer{}
			httpServer := httptest.NewServer(server)
			defer httpServer.Close()
			e, s, m, source := mcpEngineFixture(t, httpServer.URL, mcpFixtureSource)
			m, p, err := e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			m, _, v, err := e.DecideMCPProposal(context.Background(), m.ID, m.Revision, p.ID, p.Digest, "decision", true)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "database" {
				db := fixtureSQL(t, e)
				if _, err = db.Exec(`CREATE TRIGGER fixture_result_fail BEFORE UPDATE OF status ON mcp_invocations WHEN NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'fixture storage failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "seal" {
				server.onCall = func(w http.ResponseWriter, r *http.Request) {
					root := filepath.Join(e.options.DataDir, "evidence", "staging")
					if err := os.Rename(root, root+"-unavailable"); err != nil {
						t.Error(err)
					}
					if err := os.WriteFile(root, []byte("not directory"), 0600); err != nil {
						t.Error(err)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":12,"result":{"content":[{"type":"text","text":"observed"}]}}`))
				}
			}
			result, err := e.DispatchMCPInvocation(context.Background(), m.ID, m.Revision, v.ID)
			if err == nil || result.Status != "outcome_unknown" || result.Result != nil {
				t.Fatal(result, err)
			}
			wantCode := map[string]string{"seal": "result_seal_failed", "database": "result_commit_failed"}[kind]
			if result.ErrorCode != wantCode {
				t.Fatal("wrong persistence failure boundary", result.ErrorCode, wantCode)
			}
			got, _ := s.GetMission(m.ID)
			_, calls := server.counts()
			if got.Stage == "mcp_observation_ready" || calls != 1 {
				t.Fatal(got, calls)
			}
			if _, err = e.DispatchMCPInvocation(context.Background(), got.ID, got.Revision, v.ID); err == nil {
				t.Fatal("unknown invocation replayed")
			}
		})
	}
}

func TestMCPActualChildProcessKilledAfterToolsCallNeverReplays(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := &localMCPServer{onCall: func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	defer close(release)
	e, s, m, source := mcpEngineFixture(t, httpServer.URL, mcpFixtureSource)
	m, p, err := e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, _, v, err := e.DecideMCPProposal(context.Background(), m.ID, m.Revision, p.ID, p.Digest, "decision", true)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestMCPDispatchChildProcess$", "-test.count=1")
	command.Env = append(os.Environ(), "FOUNDRY_MCP_TEST_CHILD="+e.options.DataDir, "FOUNDRY_MCP_TEST_INV="+v.ID)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("child did not dispatch: %s", output.String())
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = command.Wait(); err == nil {
		t.Fatal("child exited without kill")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(filepath.Join(e.options.DataDir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered := New(reopened, nil, Options{DataDir: e.options.DataDir, Runner: &panicMCPRunner{}})
	defer recovered.Close()
	before, _ := server.counts()
	if err = recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	call, err := reopened.GetMCPInvocation(m.ID, v.ID)
	if err != nil || call.Status != "outcome_unknown" || call.Phase != "call_started" || call.Result != nil {
		t.Fatal(call, err)
	}
	got, _ := reopened.GetMission(m.ID)
	if got.State != "blocked" || got.Epoch != m.Epoch+1 {
		t.Fatal(got)
	}
	if err = recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, calls := server.counts()
	if after != before || calls != 1 {
		t.Fatal("killed call was replayed", before, after, calls)
	}
}

func TestMCPDispatchChildProcess(t *testing.T) {
	dir := os.Getenv("FOUNDRY_MCP_TEST_CHILD")
	if dir == "" {
		t.Skip("child-process entry point")
	}
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := New(s, nil, Options{DataDir: dir, EnableLocalMCP: true, Runner: &panicMCPRunner{}})
	defer e.Close()
	m, err := s.GetMission("mcp-mission")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.DispatchMCPInvocation(context.Background(), m.ID, m.Revision, os.Getenv("FOUNDRY_MCP_TEST_INV")); err != nil {
		t.Fatal(err)
	}
}
