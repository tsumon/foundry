package store

import (
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"testing"
)

func TestMCPDecisionReadRejectsCorruptV2Application(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(map[bool]string{false: "reject", true: "approve"}[approve], func(t *testing.T) {
			s, m, g := researchFixture(t)
			m, source, err := s.BeginResearchTurn(researchInput("source", m, g), nil, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			m, source = researchFinish(t, s, source)
			m, p, err := s.CreateMCPProposal(domain.MCPProposal{ID: "proposal", MissionID: m.ID, SourceInvocationID: source.ID, SourceResult: *source.Result, ToolID: "lookup", Arguments: json.RawMessage(`{}`)}, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err = s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, approve); err != nil {
				t.Fatal(err)
			}
			if _, err = s.GetMCPDecision(m.ID, "request"); err != nil {
				t.Fatal(err)
			}
			if decisions, err := s.ListMCPDecisions(m.ID); err != nil || len(decisions) != 1 {
				t.Fatalf("decision history: %+v %v", decisions, err)
			}
			if _, err = s.db.Exec(`DROP TRIGGER research_turn_applications_no_update`); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE research_turn_applications SET digest='corrupt'`); err != nil {
				t.Fatal(err)
			}
			if _, err = s.GetMCPDecision(m.ID, "request"); err == nil {
				t.Fatal("corrupt v2 application accepted by safe decision read")
			}
			if decisions, err := s.ListMCPDecisions(m.ID); err == nil || decisions != nil {
				t.Fatalf("corrupt application returned partial history: %+v %v", decisions, err)
			}
		})
	}
}

func TestMCPDecisionReadOwnsRejectedAndApprovedHistory(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(map[bool]string{false: "reject", true: "approve"}[approve], func(t *testing.T) {
			s := openTestStore(t)
			m, g := mcpStoreFixture(t, s, "decision-read")
			if decisions, err := s.ListMCPDecisions(m.ID); err != nil || decisions == nil || len(decisions) != 0 {
				t.Fatalf("empty decision history: %+v %v", decisions, err)
			}
			m, p := mcpProposalFixture(t, s, m, g, "decision-read")
			_, _, call, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, approve)
			if err != nil {
				t.Fatal(err)
			}
			d, err := s.GetMCPDecision(m.ID, "request")
			if err != nil || d.Approved != approve || d.ProposalID != p.ID || d.InvocationID != call.ID {
				t.Fatalf("decision %+v %v", d, err)
			}
			if _, err = s.GetMCPDecision("unrelated", "request"); !errors.Is(err, ErrNotFound) {
				t.Fatal("owner leak", err)
			}
			decisions, err := s.ListMCPDecisions(m.ID)
			if err != nil || len(decisions) != 1 || decisions[0] != d {
				t.Fatalf("list differs from verified receipt: %+v %v", decisions, err)
			}
			if decisions, err := s.ListMCPDecisions("unrelated"); !errors.Is(err, ErrNotFound) || decisions != nil {
				t.Fatal("history owner leak", decisions, err)
			}
			if _, err = s.db.Exec(`DROP TRIGGER mcp_call_decisions_no_update`); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE mcp_call_decisions SET proposal_digest=? WHERE mission_id=?`, "bad", m.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.GetMCPDecision(m.ID, "request"); err == nil {
				t.Fatal("corrupt decision read")
			}
			if decisions, err := s.ListMCPDecisions(m.ID); err == nil || decisions != nil {
				t.Fatalf("corrupt decision returned partial history: %+v %v", decisions, err)
			}
		})
	}
}

func TestMCPDecisionListRejectsFutureAndSQLFailure(t *testing.T) {
	for _, fault := range []struct{ name, sql string }{
		{"future", `DROP TRIGGER mcp_call_decisions_no_update; UPDATE mcp_call_decisions SET version=2`},
		{"infrastructure", `DROP TABLE mcp_call_decisions`},
	} {
		t.Run(fault.name, func(t *testing.T) {
			s := openTestStore(t)
			m, g := mcpStoreFixture(t, s, "decision-list")
			m, p := mcpProposalFixture(t, s, m, g, "decision-list")
			if _, _, _, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, false); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(fault.sql); err != nil {
				t.Fatal(err)
			}
			decisions, err := s.ListMCPDecisions(m.ID)
			if err == nil || decisions != nil || fault.name == "future" && !errors.Is(err, ErrMCPFutureVersion) {
				t.Fatalf("unsafe history on %s: %+v %v", fault.name, decisions, err)
			}
		})
	}
}
