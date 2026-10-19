package domain

import (
	"encoding/json"
	"testing"
)

func TestAccountWorkspaceModesAreStrictAndBehaviorPreserving(t *testing.T) {
	cases := []struct {
		wrapper, base, tool   string
		generations, research bool
	}{
		{"account_snapshot_v1", "", "", false, false},
		{"generations_account_snapshot_v1", "generations_v1", "", true, false},
		{"generations_controlled_read_account_snapshot_v1", "generations_controlled_read_v1", "controlled_read_v1", true, false},
		{"generations_controlled_mcp_read_account_snapshot_v1", "generations_controlled_mcp_read_v2", "controlled_mcp_read_v2", true, true},
	}
	for _, tc := range cases {
		base, bound, err := BaseWorkspaceMode(tc.wrapper)
		if err != nil || base != tc.base || !bound {
			t.Fatalf("projection=%s %v %v", base, bound, err)
		}
		wrapper, err := AccountWorkspaceMode(tc.base)
		if err != nil || wrapper != tc.wrapper {
			t.Fatalf("wrapper=%s %v", wrapper, err)
		}
		if UsesGenerations(tc.wrapper) != tc.generations || IsResearchFlowV2(tc.wrapper, tc.tool) != tc.research {
			t.Fatal("behavior changed", tc.wrapper)
		}
	}
	for _, mode := range []string{"future", "account_snapshot_v3", "generations_controlled_mcp_read_v1_account_snapshot_v1"} {
		if _, _, err := BaseWorkspaceMode(mode); err == nil || UsesGenerations(mode) {
			t.Fatal("unknown interpreted", mode)
		}
	}
	if _, err := AccountWorkspaceMode(WorkspaceModeControlledMCPReadV1); err == nil {
		t.Fatal("ABv1 acquired unsupported wrapper")
	}
}

func validModelSnapshotMission(t *testing.T) Mission {
	t.Helper()
	a := ModelAccount{ID: "a", Revision: 3, Name: "Synthetic", Provider: "custom", BaseURL: "https://example.com", AuthMethod: "api_key", BillingMode: "unknown", Enabled: true, CurrentCredentialVersion: 1}
	p := Profile{ID: "p", Model: "fake", Protocol: "responses", BaseURL: a.BaseURL, AccountID: a.ID, AccountRevision: 3, CredentialVersion: 1}
	m := Mission{WorkspaceMode: "account_snapshot_v1", Profiles: map[Role]Profile{}, ModelSnapshot: &MissionModelSnapshot{SchemaVersion: 1, Roles: map[Role]RoleModelSelection{}}}
	for _, role := range []Role{Main, Researcher, Planner, Worker, Debugger, QA, Auditor} {
		copy := a
		m.Profiles[role] = p
		m.ModelSnapshot.Roles[role] = RoleModelSelection{Profile: p, Account: &copy}
	}
	m.ModelSnapshot.Digest, _ = ModelSnapshotDigest(*m.ModelSnapshot)
	return m
}
func TestModelSnapshotRejectsStructuralMismatchAndPreservesLegacyJSON(t *testing.T) {
	m := validModelSnapshotMission(t)
	if err := ValidateMissionModelSnapshot(m); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*Mission){
		"missing":      func(m *Mission) { m.ModelSnapshot = nil },
		"schema":       func(m *Mission) { m.ModelSnapshot.SchemaVersion = 2 },
		"missing role": func(m *Mission) { delete(m.ModelSnapshot.Roles, Worker) },
		"extra role":   func(m *Mission) { m.ModelSnapshot.Roles[Role("release")] = m.ModelSnapshot.Roles[Worker] },
		"digest":       func(m *Mission) { m.ModelSnapshot.Digest = "wrong" },
		"profile":      func(m *Mission) { p := m.Profiles[Worker]; p.Model = "other"; m.Profiles[Worker] = p },
		"identity":     func(m *Mission) { m.ModelSnapshot.Roles[Worker].Account.ID = "other" },
		"revision":     func(m *Mission) { m.ModelSnapshot.Roles[Worker].Account.Revision = 4 },
		"version":      func(m *Mission) { m.ModelSnapshot.Roles[Worker].Account.CurrentCredentialVersion = 2 },
		"tool":         func(m *Mission) { m.ToolMode = ToolModeControlledReadV1 },
		"unwrapped":    func(m *Mission) { m.WorkspaceMode = "" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			m := validModelSnapshotMission(t)
			mutate(&m)
			if err := ValidateMissionModelSnapshot(m); err == nil {
				t.Fatal("accepted invalid snapshot")
			}
		})
	}
	raw, err := json.Marshal(Profile{ID: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"id":"old","name":"","kind":"","protocol":"","base_url":"","model":"","has_key":false}` {
		t.Fatalf("legacy profile bytes changed: %s", raw)
	}
}

func TestModelSnapshotValidationRejectsLegacyModePairMismatch(t *testing.T) {
	for _, pair := range [][2]string{{"", ToolModeControlledReadV1}, {WorkspaceModeGenerationsV1, ToolModeControlledMCPReadV2}, {WorkspaceModeControlledReadV1, ""}, {WorkspaceModeControlledMCPReadV1, ToolModeControlledMCPReadV2}, {WorkspaceModeControlledMCPReadV2, ""}} {
		if err := ValidateMissionModelSnapshot(Mission{WorkspaceMode: pair[0], ToolMode: pair[1]}); err == nil {
			t.Errorf("accepted mismatched legacy mode pair %q", pair)
		}
	}
}
