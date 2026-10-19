package store

import (
	"foundry/internal/domain"
	"testing"
)

func TestMCPOutcomeRequiresDedicatedCompleteJSONResult(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "result-kind")
	m, p := mcpProposalFixture(t, s, m, g, "result-kind")
	m, _, v, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "decision", m.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.ClaimMCPInvocation(m.ID, v.ID, v.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.MarkMCPCallStarted(m.ID, v.ID, v.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	envelope := controlledEnvelope("mcp_result", v.ID, m.ID, g.BindingDigest, `{"content":[]}`)
	if _, err = s.FinishMCPInvocation(m.ID, v.ID, v.Epoch, domain.MCPCallOutcome{Status: "succeeded", Result: &envelope}); err != nil {
		t.Fatal("MCP result kind refused", err)
	}
	for _, kind := range []string{"legacy-kind", "text-content", "truncated"} {
		invalid := envelope
		switch kind {
		case "legacy-kind":
			invalid.Kind = "tool_observation"
		case "text-content":
			invalid.ContentType = "text/plain"
		case "truncated":
			invalid.Truncated = true
		}
		if err := validateMCPOutcome(v, domain.MCPCallOutcome{Status: "succeeded", Result: &invalid}, false); err == nil {
			t.Fatal("invalid MCP result accepted", kind)
		}
	}
}
