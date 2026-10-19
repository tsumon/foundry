package engine

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/store"
	"path/filepath"
	"testing"
)

func TestReplanArchivesFailedRunAcrossReopen(t *testing.T) {
	for _, failCommands := range []bool{true, false} {
		name := "review"
		outputs := []string{research, plan, edit, `{"passed":false,"summary":"unsafe implementation","findings":[]}`, research, plan}
		if failCommands {
			name = "verification"
			outputs = []string{research, plan, edit, edit, edit, research, plan}
		}
		t.Run(name, func(t *testing.T) {
			e, s, _, runner, m := setup(t, outputs...)
			runner.fail = failCommands
			if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			m = wait(t, s, m.ID, "awaiting_approval")
			if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			previous := wait(t, s, m.ID, "blocked")
			if _, err := e.Plan(context.Background(), m.ID, previous.Revision); err != nil {
				t.Fatal(err)
			}
			m = wait(t, s, m.ID, "awaiting_approval")
			e.Close()
			s.Close()
			reopened, err := store.Open(filepath.Join(e.options.DataDir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			m, err = reopened.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(m)
			var wire struct {
				Archives []struct {
					Epoch           int64                 `json:"epoch"`
					PlanRevision    int64                 `json:"plan_revision"`
					CandidateDigest string                `json:"candidate_digest"`
					Evidence        []domain.Verification `json:"evidence"`
					Reviews         []domain.Review       `json:"reviews"`
					Changes         []domain.Change       `json:"changes"`
				} `json:"run_archives"`
			}
			if err := json.Unmarshal(b, &wire); err != nil {
				t.Fatal(err)
			}
			if len(wire.Archives) != 1 {
				t.Fatalf("prior run erased: archives=%d", len(wire.Archives))
			}
			a := wire.Archives[0]
			if a.Epoch != previous.Epoch || a.PlanRevision != previous.Plan.Revision || a.CandidateDigest != previous.CandidateDigest || len(a.Changes) != len(previous.Changes) {
				t.Fatal("archive lost run identity/changes")
			}
			got, _ := json.Marshal(a.Evidence)
			want, _ := json.Marshal(previous.Evidence)
			if string(got) != string(want) {
				t.Fatal("command evidence changed")
			}
			got, _ = json.Marshal(a.Reviews)
			want, _ = json.Marshal(previous.Reviews)
			if string(got) != string(want) {
				t.Fatal("review explanation changed")
			}
			if len(m.Evidence) != 0 || len(m.Reviews) != 0 || len(m.Changes) != 0 {
				t.Fatal("old active run not cleared")
			}
			m.State = "awaiting_delivery"
			if domain.CanDeliver(m, m.CandidateDigest) == nil {
				t.Fatal("archive authorized new delivery")
			}
		})
	}
}
