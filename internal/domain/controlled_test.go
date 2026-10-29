package domain

import (
	"encoding/json"
	"testing"
)

// Removing omitempty from the controlled-mode fields would rewrite legacy task JSON.
func TestControlledMissionFieldsStayOmittedFromLegacyJSON(t *testing.T) {
	raw, err := json.Marshal(Mission{ID: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" {
		t.Fatal("empty mission JSON")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"tool_mode", "task_bindings"} {
		if _, ok := fields[field]; ok {
			t.Fatalf("legacy JSON materialized %q: %s", field, raw)
		}
	}
}

// A missing limit or nondeterministic digest would let controller and store disagree on authority.
func TestControlledBindingsHaveFixedBudgetAndStableDigest(t *testing.T) {
	if WorkspaceModeControlledReadV1 != "generations_controlled_read_v1" {
		t.Fatalf("controlled workspace fence = %q", WorkspaceModeControlledReadV1)
	}
	want := ReadBudget{MaxTurns: 4, MaxCalls: 3, MaxArgumentBytes: 8 << 10, MaxResultBytes: 64 << 10, DeadlineSeconds: 5, MaxTotalResultBytes: 192 << 10}
	if got := FixedReadBudget(); got != want {
		t.Fatalf("FixedReadBudget() = %+v, want %+v", got, want)
	}
	b := TaskBindings{
		Git:           GitSourceBinding{RepositoryID: "repo", RepositoryRevision: 2, ObservationID: "obs", Root: nativeDomainRoot(), BaseCommit: "0123456789012345678901234567890123456789"},
		Skills:        []SkillBinding{{ID: "skill", Revision: 3, Digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "Review", Role: Researcher}},
		PolicyVersion: "repo_read_v1",
		Tools:         []string{"repo.list", "repo.read", "repo.search"},
		Budget:        want,
	}
	first, err := TaskBindingDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	second, err := TaskBindingDigest(b)
	if err != nil || second != first || len(first) != 64 {
		t.Fatalf("unstable digest: first=%q second=%q err=%v", first, second, err)
	}
	b.Git.BaseCommit = "1123456789012345678901234567890123456789"
	changed, err := TaskBindingDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("authority digest ignored changed base commit")
	}
}
