package store

import (
	"encoding/json"
	"foundry/internal/domain"
	"testing"
)

func codingMission() domain.Mission {
	ref := &domain.GenerationRef{ID: "base", Digest: "digest"}
	return domain.Mission{ID: "coding", CodingVersion: 1, WorkspaceMode: domain.WorkspaceModeGenerationsV1, Epoch: 1, Plan: &domain.Plan{Revision: 3}, CandidateGeneration: ref, CodingAttempts: []domain.CodingAttempt{{ID: "attempt", Epoch: 1, PlanRevision: 3, Phase: "worker", Role: domain.Worker, StepID: "one", Status: "running", Before: ref, StartedAt: "2026-10-21T00:00:00Z"}}}
}
func TestCodingTraceRejectsMutationAndDuplicateWithoutPersisting(t *testing.T) {
	for _, kind := range []string{"duplicate", "delete", "identity", "version", "terminal", "foreign_after", "foreign_before", "stale", "overflow"} {
		t.Run(kind, func(t *testing.T) {
			s := openTestStore(t)
			input := codingMission()
			if kind == "terminal" {
				a := &input.CodingAttempts[0]
				a.Status = "succeeded"
				a.After = a.Before
				a.FinishedAt = "2026-10-21T00:01:00Z"
			}
			m, err := s.CreateMission(input)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(m)
			_, err = s.UpdateMission(m.ID, m.Revision, "fixture", func(m *domain.Mission) error {
				a := &m.CodingAttempts[0]
				switch kind {
				case "duplicate":
					m.CodingAttempts = append(m.CodingAttempts, *a)
				case "delete":
					m.CodingAttempts = nil
				case "identity":
					a.StepID = "other"
				case "version":
					m.CodingVersion = 0
					m.CodingAttempts = nil
				case "terminal":
					a.Status = "failed"
					a.FailureCode = "edit_denied"
				case "foreign_after":
					a.Status = "succeeded"
					a.FinishedAt = "2026-10-21T00:01:00Z"
					a.After = &domain.GenerationRef{ID: "foreign", Digest: "foreign"}
				case "foreign_before":
					a.Status = "interrupted"
					a.FailureCode = "interrupted"
					a.FinishedAt = "2026-10-21T00:01:00Z"
					next := *a
					next.ID = "next"
					next.Status = "running"
					next.FailureCode = ""
					next.FinishedAt = ""
					next.Before = &domain.GenerationRef{ID: "foreign", Digest: "foreign"}
					m.CodingAttempts = append(m.CodingAttempts, next)
				case "stale":
					m.Epoch++
					a.Status = "succeeded"
					a.After = a.Before
					a.FinishedAt = "2026-10-21T00:01:00Z"
				case "overflow":
					for len(m.CodingAttempts) <= 256 {
						m.CodingAttempts = append(m.CodingAttempts, *a)
					}
				}
				return nil
			})
			if err == nil {
				t.Fatal("invalid coding mutation accepted", kind)
			}
			got, _ := s.GetMission(m.ID)
			after, _ := json.Marshal(got)
			if string(raw) != string(after) {
				t.Fatal("rejected mutation changed persisted state")
			}
		})
	}
}
func TestCodingUnknownAndCorruptTraceReadOnly(t *testing.T) {
	for _, raw := range []string{`{"id":"future","coding_version":9,"workspace_mode":"generations_v1"}`, `{"id":"future","coding_version":1,"workspace_mode":"generations_v1","coding_attempts":[{"future":"unknown"}]}`} {
		t.Run(raw, func(t *testing.T) {
			s := openTestStore(t)
			_, err := s.db.Exec(`INSERT INTO missions(id,revision,state,updated_at,data) VALUES('future',1,'running','now',?)`, raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.GetMission("future"); err != nil {
				t.Fatal("must remain inspectable", err)
			}
			if _, err = s.UpdateMission("future", 1, "rewrite", func(m *domain.Mission) error { m.Title = "changed"; return nil }); err == nil {
				t.Fatal("rewrote unknown trace")
			}
			var got string
			_ = s.db.QueryRow(`SELECT data FROM missions WHERE id='future'`).Scan(&got)
			if got != raw {
				t.Fatal("raw data changed")
			}
		})
	}
}
