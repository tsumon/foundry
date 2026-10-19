package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"testing"
)

func mcpStoreFixture(t *testing.T, s *Store, id string) (domain.Mission, domain.ReadGrant) {
	t.Helper()
	c, err := s.SaveMCPConnection(domain.MCPConnection{ID: "mcp-" + id, Name: "local", Endpoint: "http://127.0.0.1:1234/mcp", Protocol: domain.MCPProtocol, Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := persistedExecutionSnapshot(t, c, "snapshot-"+id)
	persistSnapshot(t, s, c, "discovery-"+id, snapshot)
	base, _ := controlledStoreMission(t, s, "base-"+id)
	schema := sha256.Sum256(snapshot.Tools[0].InputSchema)
	remote := domain.MCPTaskBindings{ConnectionID: c.ID, ConnectionRevision: c.Revision, Endpoint: c.Endpoint, Protocol: c.Protocol, DiscoveryOperationID: "discovery-" + id, SnapshotID: snapshot.ID, SnapshotDigest: snapshot.Digest, Tools: []domain.MCPToolBinding{{Name: "lookup", DefinitionDigest: snapshot.Tools[0].DefinitionDigest, InputSchemaDigest: hex.EncodeToString(schema[:]), Policy: json.RawMessage(`{"policy_version":"mcp_read_v1","reviewed":true,"schema":{"type":"object","properties":{},"additionalProperties":false}}`)}}, Budget: domain.FixedMCPBudget()}
	base.ID = id
	base.WorkspaceMode = domain.WorkspaceModeControlledMCPReadV1
	base.ToolMode = domain.ToolModeControlledMCPReadV1
	base.MCPBindings = &remote
	digest, err := domain.MCPBindingDigest(*base.TaskBindings, remote)
	if err != nil {
		t.Fatal(err)
	}
	base.Approvals = []domain.Approval{{Kind: "context", Digest: fmt.Sprintf("%064x", 11)}}
	m, err := s.CreateMission(base)
	if err != nil {
		t.Fatal(err)
	}
	g := domain.ReadGrant{ID: "grant-" + id, MissionID: id, Epoch: m.Epoch, SessionID: "session", Role: domain.Researcher, RequiredState: "planning", BindingDigest: digest, AuthorityDigest: base.Approvals[0].Digest}
	m, g, err = s.CreateReadGrant(g, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return m, g
}

func mcpProposalFixture(t *testing.T, s *Store, m domain.Mission, g domain.ReadGrant, suffix string) (domain.Mission, domain.MCPProposal) {
	t.Helper()
	v := modelInvocation("source-"+suffix, m, g, g.BindingDigest)
	v.SessionID = g.SessionID
	var err error
	m, v, err = s.BeginInvocation(v, m.Revision)
	if err != nil {
		t.Fatal("begin source: ", err)
	}
	if _, err = s.ClaimInvocation(v.ID, v.Epoch); !errors.Is(err, ErrConflict) {
		t.Fatalf("unregistered source dispatched: %v", err)
	}
	m, _, err = s.RegisterMCPProposalSource(m.ID, v.ID, m.Revision)
	if err != nil {
		t.Fatal("register source: ", err)
	}
	v, err = s.ClaimInvocation(v.ID, v.Epoch)
	if err != nil {
		t.Fatal("claim source: ", err)
	}
	body := `{"tool_id":"lookup","arguments":{}}`
	envelope := controlledEnvelope("model_output", v.ID, m.ID, g.BindingDigest, body)
	v, err = s.FinishInvocation(v.ID, v.Epoch, "succeeded", &envelope, "", domain.CallUsage{})
	if err != nil {
		t.Fatal("finish source: ", err)
	}
	m, err = s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := domain.MCPProposal{ID: "proposal-" + suffix, MissionID: m.ID, SourceInvocationID: v.ID, SourceResult: envelope, ToolID: "lookup", Arguments: json.RawMessage(`{}`)}
	m, p, err = s.CreateMCPProposal(p, m.Revision)
	if err != nil {
		t.Fatal("create proposal: ", err)
	}
	if m.State != "planning" || m.Stage != "awaiting_tool_approval" {
		t.Fatalf("bad checkpoint %+v", m)
	}
	return m, p
}

func TestMCPProposalApprovalReplayAndFrozenArguments(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "approval")
	m, p := mcpProposalFixture(t, s, m, g, "approval")
	expected := m.Revision
	m, grant, v, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", expected, true)
	if err != nil {
		t.Fatal(err)
	}
	again, g2, v2, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", expected, true)
	if err != nil || again.Revision != m.Revision || grant.ID != g2.ID || v.ID != v2.ID {
		t.Fatalf("replay mutated decision: %v", err)
	}
	if _, _, _, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", expected, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("decision replaced: %v", err)
	}
	if _, _, _, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", expected+1, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("revision intent replaced: %v", err)
	}
	if _, err := s.GetMCPInvocation("other", v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross mission read: %v", err)
	}
	v, err = s.ClaimMCPInvocation(m.ID, v.ID, v.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimMCPInvocation(m.ID, v.ID, v.Epoch); !errors.Is(err, ErrConflict) {
		t.Fatal("double claim")
	}
	if _, err = s.MarkMCPCallStarted(m.ID, v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishMCPInvocation(m.ID, v.ID, v.Epoch, domain.MCPCallOutcome{Status: "failed", ErrorCode: "tool_error"}); err != nil {
		t.Fatal(err)
	}
}

func TestMCPBindingsImmutableAndSourceCannotMaterializeTwice(t *testing.T) {
	s := openTestStore(t)
	m, g := mcpStoreFixture(t, s, "immutable")
	if _, err := s.UpdateMission(m.ID, m.Revision, "tamper", func(m *domain.Mission) error { m.MCPBindings.Endpoint = "http://127.0.0.1:2"; return nil }); err == nil {
		t.Fatal("MCP binding mutable")
	}
	if _, err := s.db.Exec(`UPDATE missions SET data=json_set(data,'$.mcp_bindings.endpoint','http://127.0.0.1:2') WHERE id=?`, m.ID); err == nil {
		t.Fatal("SQL binding mutable")
	}
	m, p := mcpProposalFixture(t, s, m, g, "immutable")
	p.ID = "replacement"
	p.Digest = ""
	p.CreatedAt = ""
	if _, _, err := s.CreateMCPProposal(p, m.Revision); err == nil {
		t.Fatal("source materialized twice")
	}
}
