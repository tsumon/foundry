package store

import (
	"foundry/internal/domain"
	"testing"
)

func TestRunArchivesAreAppendOnly(t *testing.T) {
	for _, kind := range []string{"edit", "remove", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			s := openTestStore(t)
			m, err := s.CreateMission(domain.Mission{ID: "history", RunArchives: []domain.RunArchive{{Epoch: 2, PlanRevision: 4, Evidence: []domain.Verification{{ID: "prior", Results: []domain.CommandResult{{Output: "failure"}}}}}}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.UpdateMission(m.ID, m.Revision, "fixture.mutate", func(m *domain.Mission) error {
				switch kind {
				case "edit":
					m.RunArchives[0].Evidence[0].Results[0].Output = "pass"
				case "remove":
					m.RunArchives = nil
				case "duplicate":
					m.RunArchives = append(m.RunArchives, m.RunArchives[0])
				}
				return nil
			})
			if err == nil {
				t.Fatal("archive mutation accepted")
			}
			events, err := s.Events(0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 {
				t.Fatal("failed archive mutation emitted event")
			}
			got, err := s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.RunArchives[0].Evidence[0].Results[0].Output != "failure" {
				t.Fatal("history changed")
			}
		})
	}
}
