package store

import (
	"encoding/json"
	"foundry/internal/domain"
	"strings"
	"testing"
)

func TestResearchFlowLocalOperationRequiresExactModeAndExecuteApproval(t *testing.T) {
	t.Run("mismatched-pair", func(t *testing.T) {
		s := openTestStore(t)
		m, op, _ := operationFixture(t, s)
		m.ID = "bad-v2"
		m.WorkspaceMode = domain.WorkspaceModeControlledMCPReadV2
		var err error
		if _, err = s.CreateMission(m); err == nil {
			t.Fatal("mismatched mode created")
		}
		// Existing malformed records remain inspectable, but cannot authorize writes.
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`INSERT INTO missions VALUES(?,?,?,?,?)`, m.ID, m.Revision, m.State, m.UpdatedAt, raw); err != nil {
			t.Fatal(err)
		}
		op.MissionID = m.ID
		op.ExpectedRevision = m.Revision
		if _, err = s.BeginLocalOperation(op); err == nil {
			t.Fatal("mismatched v2 local write authorized")
		}
	})
	t.Run("context-only", func(t *testing.T) {
		s, m, g := researchFixture(t)
		var err error
		before := domain.GenerationRef{ID: generationID, Digest: strings.Repeat("a", 64)}
		m, err = s.UpdateMission(m.ID, m.Revision, "fixture", func(m *domain.Mission) error {
			m.CandidateGeneration = &before
			m.CandidateDigest = before.Digest
			m.Plan = &domain.Plan{Revision: 1}
			m.State = "running"
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		op := domain.LocalOperation{ID: operationID, MissionID: m.ID, Kind: "apply", ExpectedRevision: m.Revision, Epoch: m.Epoch, PlanRevision: 1, Before: before, BaseDigest: strings.Repeat("b", 64), IntentDigest: strings.Repeat("c", 64), ApprovalDigest: g.AuthorityDigest}
		if _, err = s.BeginLocalOperation(op); err == nil {
			t.Fatal("context-only local write authorized")
		}
	})
}
