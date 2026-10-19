package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPBindingDigestFreezesRemoteAuthorityWithoutChangingLegacy(t *testing.T) {
	repo := TaskBindings{Git: GitSourceBinding{RepositoryID: "repo", RepositoryRevision: 1, ObservationID: "obs", Root: "/tmp/repo", BaseCommit: strings.Repeat("a", 40)}, PolicyVersion: "repo_read_v1", Tools: []string{"repo.list", "repo.read", "repo.search"}, Budget: FixedReadBudget()}
	remote := MCPTaskBindings{ConnectionID: "connection", ConnectionRevision: 1, Endpoint: "http://127.0.0.1:1234/mcp", Protocol: MCPProtocol, DiscoveryOperationID: "discovery", SnapshotID: "snapshot", SnapshotDigest: strings.Repeat("b", 64), Tools: []MCPToolBinding{{Name: "lookup", DefinitionDigest: strings.Repeat("c", 64), InputSchemaDigest: strings.Repeat("d", 64), Policy: json.RawMessage(`{"policy_version":"mcp_read_v1","reviewed":true,"schema":{"type":"object","properties":{},"additionalProperties":false}}`)}}, Budget: FixedMCPBudget()}
	legacy, err := TaskBindingDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	// Hand-serialized R3 JSON (skills canonicalized to []) hashed independently.
	if legacy != "f46d745aaae6462475ffd47130797ce2512323fbb6790985dbd5888094cc38bf" {
		t.Fatalf("legacy binding digest changed: %s", legacy)
	}
	before, _ := json.Marshal(repo)
	first, err := MCPBindingDigest(repo, remote)
	if err != nil {
		t.Fatal(err)
	}
	remote.CredentialRef = "opaque"
	remote.CredentialRevision = 1
	second, err := MCPBindingDigest(repo, remote)
	if err != nil || first == second {
		t.Fatalf("credential identity omitted: %v", err)
	}
	after, _ := json.Marshal(repo)
	unchanged, _ := TaskBindingDigest(repo)
	if string(before) != string(after) || legacy != unchanged {
		t.Fatal("legacy authority changed")
	}
	remote.Budget.MaxCalls = 4
	if _, err := MCPBindingDigest(repo, remote); err == nil {
		t.Fatal("mutable budget accepted")
	}
	raw, _ := json.Marshal(Mission{ID: "legacy"})
	if strings.Contains(string(raw), "mcp_bindings") {
		t.Fatal("legacy JSON gained MCP field")
	}
}

func TestMCPBindingRejectsUnreviewedPolicyAndUnsafeEndpoint(t *testing.T) {
	repo := TaskBindings{Git: GitSourceBinding{RepositoryID: "repo", RepositoryRevision: 1, ObservationID: "obs", Root: "/tmp/repo", BaseCommit: strings.Repeat("a", 40)}, PolicyVersion: "repo_read_v1", Tools: []string{"repo.list", "repo.read", "repo.search"}, Budget: FixedReadBudget()}
	binding := MCPTaskBindings{ConnectionID: "local", ConnectionRevision: 1, Endpoint: "http://127.0.0.1:1234", Protocol: MCPProtocol, DiscoveryOperationID: "discovery", SnapshotID: "snapshot", SnapshotDigest: strings.Repeat("a", 64), Tools: []MCPToolBinding{{Name: "lookup", DefinitionDigest: strings.Repeat("b", 64), InputSchemaDigest: strings.Repeat("c", 64), Policy: json.RawMessage(`{"policy_version":"mcp_read_v1","reviewed":false,"schema":{"type":"object","properties":{},"additionalProperties":false}}`)}}, Budget: FixedMCPBudget()}
	if _, err := MCPBindingDigest(repo, binding); err == nil {
		t.Fatal("unreviewed policy gained frozen authority")
	}
}
