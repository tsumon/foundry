package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func TestMCPSourceCorruptionDuringCallCannotPublishObservation(t *testing.T) {
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
	server.onCall = func(w http.ResponseWriter, r *http.Request) {
		if err := os.WriteFile(filepath.Join(e.options.DataDir, "evidence", "blobs", source.Result.Digest[:2], source.Result.Digest), []byte("corrupted during call"), 0600); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":12,"result":{"content":[]}}`))
	}
	result, err := e.DispatchMCPInvocation(context.Background(), m.ID, m.Revision, v.ID)
	if err == nil || result.Status != "outcome_unknown" || result.ErrorCode != "dispatch_interrupted" || result.Result != nil {
		t.Fatal("changed source accepted", result, err)
	}
	got, _ := s.GetMission(m.ID)
	if got.Stage == "mcp_observation_ready" {
		t.Fatal("changed source advanced")
	}
}

func TestMCPRecoveryPropagatesSQLFailureAfterLedgerReconciliation(t *testing.T) {
	server := &localMCPServer{}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	e, s, m, source := mcpEngineFixture(t, httpServer.URL, mcpFixtureSource)
	m, _, err := e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	db := fixtureSQL(t, e)
	// Store's structural waiting-checkpoint audit does not read snapshots;
	// Engine's sealed-source argument/schema audit does. Mission writes still work.
	if _, err = db.Exec(`ALTER TABLE mcp_tool_snapshots RENAME TO unavailable_snapshots`); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverMCPInvocations(m.ID); err != nil {
		t.Fatal("fixture must pass structural audit", err)
	}
	if err = e.Recover(context.Background()); err == nil {
		t.Fatal("SQLite failure mislabeled isolated corruption")
	}
	got, _ := s.GetMission(m.ID)
	if got.Revision != m.Revision || got.Epoch != m.Epoch || got.Stage != m.Stage {
		t.Fatal("SQL failure rewrote mission", got)
	}
}

func TestMCPControllerRechecksFrozenAuthorityBeforeToolBytes(t *testing.T) {
	for _, kind := range []string{"base", "context", "connection", "source"} {
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
			server.onList = func() {
				switch kind {
				case "base":
					if err := os.WriteFile(filepath.Join(e.dir(m.ID), "base/src/main.txt"), []byte("tamper\n"), 0600); err != nil {
						t.Error(err)
					}
				case "context":
					current, err := s.GetMission(m.ID)
					if err != nil {
						t.Error(err)
						return
					}
					if _, err = s.UpdateMission(m.ID, current.Revision, "fixture.context", func(m *domain.Mission) error { m.Goal = "changed after claim"; return nil }); err != nil {
						t.Error(err)
					}
				case "connection":
					c, err := s.GetMCPConnection(m.MCPBindings.ConnectionID)
					if err != nil {
						t.Error(err)
						return
					}
					c.Enabled = false
					if _, err = s.SaveMCPConnection(c, c.Revision); err != nil {
						t.Error(err)
					}
				case "source":
					if err := os.WriteFile(filepath.Join(e.options.DataDir, "evidence", "blobs", source.Result.Digest[:2], source.Result.Digest), []byte("tamper"), 0600); err != nil {
						t.Error(err)
					}
				}
			}
			result, err := e.DispatchMCPInvocation(context.Background(), m.ID, m.Revision, v.ID)
			if err == nil || result.Phase != "preflight" || result.Status != "failed" || result.Result != nil {
				t.Fatal(result, err)
			}
			_, calls := server.counts()
			if calls != 0 {
				t.Fatal("authority drift sent tool bytes")
			}
		})
	}
}

func TestMCPControllerNeverSubstitutesValidButDifferentSourceArguments(t *testing.T) {
	server := &localMCPServer{}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	e, s, m, source := mcpEngineFixture(t, httpServer.URL, mcpFixtureSource)
	// Store cannot read sealed files. A well-formed internally supplied proposal
	// with different arguments must still be rejected by the Engine boundary.
	m, p, err := s.CreateMCPProposal(domain.MCPProposal{ID: "substitute", MissionID: m.ID, SourceInvocationID: source.ID, SourceResult: *source.Result, ToolID: "lookup", Arguments: json.RawMessage(`{"query":"different"}`)}, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := server.counts()
	if _, _, _, err = e.DecideMCPProposal(context.Background(), m.ID, m.Revision, p.ID, p.Digest, "decision", true); err == nil {
		t.Fatal("caller arguments replaced source")
	}
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMission(m.ID)
	after, _ := server.counts()
	if got.State != "blocked" || got.Stage != "mcp_corrupt" || after != before {
		t.Fatal(got)
	}
}

func TestMCPRecoveryCorruptionIsolatesTrustedOwnerAndFutureRows(t *testing.T) {
	for _, kind := range []string{"ledger", "future-decision", "future-transition"} {
		t.Run(kind, func(t *testing.T) {
			server := &localMCPServer{}
			httpServer := httptest.NewServer(server)
			defer httpServer.Close()
			e, s, m, source := mcpEngineFixture(t, httpServer.URL, mcpFixtureSource)
			m, p, err := e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if kind != "ledger" {
				m, _, _, err = e.DecideMCPProposal(context.Background(), m.ID, m.Revision, p.ID, p.Digest, "decision", true)
				if err != nil {
					t.Fatal(err)
				}
			}
			other := m
			other.ID = "unrelated-future"
			other.WorkspaceMode = "future_workspace"
			other.State = "draft"
			other.Revision = 0
			other = seedPersistedMission(t, e, other)
			beforeOther, _ := json.Marshal(other)
			beforeMission, _ := json.Marshal(m)
			db := fixtureSQL(t, e)
			query := `DROP TRIGGER mcp_call_proposals_no_update; UPDATE mcp_call_proposals SET data=json_set(data,'$.mission_id','unrelated-future') WHERE mission_id='mcp-mission'`
			if kind == "future-decision" {
				query = `DROP TRIGGER mcp_call_decisions_no_update; UPDATE mcp_call_decisions SET version=2 WHERE mission_id='mcp-mission'`
			}
			if kind == "future-transition" {
				query = `DROP TRIGGER mcp_invocation_transitions_no_update; UPDATE mcp_invocation_transitions SET version=2 WHERE mission_id='mcp-mission'`
			}
			if _, err = db.Exec(query); err != nil {
				t.Fatal(err)
			}
			if kind != "ledger" {
				if err = os.WriteFile(filepath.Join(e.dir(m.ID), "base/src/main.txt"), []byte("also corrupt but future must stay untouched"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			if kind != "ledger" {
				if _, err = e.PauseMCP(context.Background(), m.ID, m.Revision); err == nil {
					t.Fatal("future ledger paused and rewritten")
				}
			}
			got, _ := s.GetMission(m.ID)
			other, _ = s.GetMission(other.ID)
			afterOther, _ := json.Marshal(other)
			if string(beforeOther) != string(afterOther) {
				t.Fatal("unrelated mission rewritten")
			}
			if kind == "ledger" {
				if got.State != "blocked" || got.Epoch != m.Epoch+1 {
					t.Fatal("corrupt trusted owner not fenced", got)
				}
			} else {
				afterMission, _ := json.Marshal(got)
				if string(beforeMission) != string(afterMission) {
					t.Fatal("future mission rewritten")
				}
			}
		})
	}
}

func TestMCPResultValidationAndCredentialLossDoNotPromote(t *testing.T) {
	for _, kind := range []string{"tool-error", "oversize", "reflection", "missing-credential"} {
		t.Run(kind, func(t *testing.T) {
			server := &localMCPServer{}
			httpServer := httptest.NewServer(server)
			defer httpServer.Close()
			switch kind {
			case "tool-error":
				server.result = `{"isError":true,"content":[{"type":"text","text":"safe tool refusal"}]}`
			case "oversize":
				server.result = `{"content":[{"type":"text","text":"` + strings.Repeat("x", 64<<10) + `"}]}`
			case "reflection":
				server.result = `{"content":[{"type":"text","text":"fake-secret"}]}`
			}
			e, s, m, source := mcpEngineFixture(t, httpServer.URL, mcpFixtureSource, "fake-secret")
			m, p, err := e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			m, _, v, err := e.DecideMCPProposal(context.Background(), m.ID, m.Revision, p.ID, p.Digest, "decision", true)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "missing-credential" {
				e.options.MCPSecrets = nil
			}
			before, _ := server.counts()
			result, err := e.DispatchMCPInvocation(context.Background(), m.ID, m.Revision, v.ID)
			if err == nil || result.Status != "failed" {
				t.Fatal(result, err)
			}
			got, _ := s.GetMission(m.ID)
			if kind == "tool-error" {
				if result.Result == nil || result.ErrorCode != "tool_error" || got.Stage != "mcp_observation_ready" {
					t.Fatal(result, got)
				}
			} else {
				if result.Result != nil || got.Stage == "mcp_observation_ready" {
					t.Fatal(result, got)
				}
			}
			after, _ := server.counts()
			if kind == "missing-credential" && after != before {
				t.Fatal("missing credential made HTTP request")
			}
		})
	}
}
