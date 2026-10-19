package httpapi

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/engine"
	"foundry/internal/store"
)

func TestMCPTaskDecisionHistorySurvivesSessionsAndDisabledReload(t *testing.T) {
	f := newMCPHTTPFixture(t)
	m := f.create()
	base := "/api/missions/" + m.ID
	if raw := f.ok("GET", base+"/mcp-invocations", nil); !strings.Contains(raw, `"decisions":[]`) || !strings.Contains(raw, `"invocations":[]`) {
		t.Fatal("empty history must expose both arrays", raw)
	}
	var revisions []int64
	for _, decision := range []string{"reject", "approve"} {
		f.ok("POST", base+"/plan", map[string]any{"revision": m.Revision, "controlled_consent": true})
		m = f.wait(m.ID, "planning", "awaiting_tool_approval")
		proposals, err := f.store.ListMCPProposals(m.ID)
		if err != nil || len(proposals) == 0 {
			t.Fatal("pending proposal", err)
		}
		p := proposals[0]
		for _, candidate := range proposals {
			if candidate.Epoch == m.Epoch {
				p = candidate
				break
			}
		}
		if p.Epoch != m.Epoch {
			t.Fatal("current epoch proposal missing")
		}
		revisions = append(revisions, m.Revision)
		f.ok("POST", base+"/mcp-proposals/"+p.ID+"/decision", map[string]any{"expected_revision": m.Revision, "proposal_digest": p.Digest, "decision": decision, "request_id": decision + "-history"})
		if decision == "reject" {
			m = f.wait(m.ID, "blocked", "mcp_rejected")
			raw := f.ok("GET", base+"/mcp-invocations", nil)
			if !strings.Contains(raw, `"request_id":"reject-history"`) || !strings.Contains(raw, `"invocations":[]`) {
				t.Fatal("rejection missing from durable history", raw)
			}
		} else {
			m = f.wait(m.ID, "awaiting_approval", "")
		}
	}
	// Reopen persisted data with no local-test authority, without any remembered
	// decision identity in the history request.
	f.engine.Close()
	f.store.Close()
	reopened, err := store.Open(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	off := engine.New(reopened, mcpTaskGateway{}, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}})
	t.Cleanup(func() { off.Close() })
	f.handler.(*server).store, f.handler.(*server).engine = reopened, off
	raw := f.ok("GET", base+"/mcp-invocations", nil)
	var history struct {
		Invocations []struct {
			ID string `json:"id"`
		} `json:"invocations"`
		Decisions []struct {
			RequestID        string `json:"request_id"`
			ExpectedRevision int64  `json:"expected_revision"`
			Approved         bool   `json:"approved"`
			GrantID          string `json:"grant_id"`
			InvocationID     string `json:"invocation_id"`
		} `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(raw), &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Decisions) != 2 || len(history.Invocations) != 1 {
		t.Fatal("incomplete persisted history", raw)
	}
	for i, requestID := range []string{"reject-history", "approve-history"} {
		d := history.Decisions[i]
		if d.RequestID != requestID || d.ExpectedRevision != revisions[i] || d.Approved != (i == 1) {
			t.Fatal("wrong historical decision", raw)
		}
		if i == 0 && (d.GrantID != "" || d.InvocationID != "") || i == 1 && (d.GrantID == "" || d.InvocationID != history.Invocations[0].ID) {
			t.Fatal("wrong decision linkage", raw)
		}
	}
	var fields struct {
		Decisions []map[string]json.RawMessage `json:"decisions"`
	}
	json.Unmarshal([]byte(raw), &fields)
	allowed := map[string]bool{"mission_id": true, "request_id": true, "proposal_id": true, "proposal_digest": true, "expected_revision": true, "approved": true, "invocation_id": true, "grant_id": true}
	for _, d := range fields.Decisions {
		for key := range d {
			if !allowed[key] {
				t.Fatalf("unsafe or invented decision field %q", key)
			}
		}
	}
}

func TestMCPTaskDecisionHistoryFailsClosed(t *testing.T) {
	for _, mutation := range []struct{ name, sql string }{
		{"corrupt", `DROP TRIGGER mcp_call_decisions_no_update; UPDATE mcp_call_decisions SET proposal_digest='corrupt'`},
		{"future", `DROP TRIGGER mcp_call_decisions_no_update; UPDATE mcp_call_decisions SET version=2`},
		{"infrastructure", `DROP TABLE mcp_call_decisions`},
		{"source_evidence", ""},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			f := newMCPHTTPFixture(t)
			m := f.create()
			base := "/api/missions/" + m.ID
			f.ok("POST", base+"/plan", map[string]any{"revision": m.Revision, "controlled_consent": true})
			m = f.wait(m.ID, "planning", "awaiting_tool_approval")
			proposals, err := f.store.ListMCPProposals(m.ID)
			if err != nil || len(proposals) != 1 {
				t.Fatal("pending proposal", err)
			}
			p := proposals[0]
			f.ok("POST", base+"/mcp-proposals/"+p.ID+"/decision", map[string]any{"expected_revision": m.Revision, "proposal_digest": p.Digest, "decision": "reject", "request_id": "reject-history"})
			if mutation.name == "source_evidence" {
				path := filepath.Join(f.dataDir, "evidence", "blobs", p.SourceResult.Digest[:2], p.SourceResult.Digest)
				if _, err = os.Stat(path); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				db, err := sql.Open("sqlite3", f.dbPath)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err = db.Exec(mutation.sql); err != nil {
					t.Fatal(err)
				}
			}
			response, raw := f.send("GET", base+"/mcp-invocations", "", true)
			if response.StatusCode < 400 || strings.Contains(raw, `"decisions"`) || strings.Contains(raw, `"invocations"`) || strings.Contains(raw, "no such table") {
				t.Fatalf("partial or unsafe history on %s: %d %s", mutation.name, response.StatusCode, raw)
			}
			assertMCPNoSecrets(t, raw)
		})
	}
}
