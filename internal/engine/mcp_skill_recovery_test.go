package engine

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/skills"
	"github.com/mattn/go-sqlite3"
)

func TestMCPRecoveryClassifiesFrozenSkillReadFailuresWithoutChangingR3(t *testing.T) {
	for _, kind := range []string{"sql-infrastructure", "missing-revision", "invalid-revision"} {
		t.Run(kind, func(t *testing.T) {
			server := &localMCPServer{}
			httpServer := httptest.NewServer(server)
			defer httpServer.Close()
			skill, err := skills.Parse([]domain.SkillFile{{Path: "SKILL.md", Content: "---\nname: fixture\ndescription: Local frozen context\n---\nRead-only fixture context"}}, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			skill.ID = "frozen-skill"
			e, s, m, source := mcpEngineFixtureWithSkills(t, httpServer.URL, mcpFixtureSource, []domain.SkillRevision{skill})
			m, _, err = e.MaterializeMCPProposal(context.Background(), m.ID, m.Revision, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			db := fixtureSQL(t, e)
			query := `ALTER TABLE skill_revisions RENAME TO unavailable_skill_revisions`
			if kind == "missing-revision" {
				query = `DROP TRIGGER skill_revision_no_delete; DELETE FROM skill_revisions WHERE id='frozen-skill'`
			}
			if kind == "invalid-revision" {
				query = `DROP TRIGGER skill_revision_no_update; UPDATE skill_revisions SET data=json_set(data,'$.files[0].content','tampered context') WHERE id='frozen-skill'`
			}
			if _, err = db.Exec(query); err != nil {
				t.Fatal(err)
			}
			// R3 keeps its established safe error contract even for SQL failures.
			_, legacyErr := e.boundSkills(m.TaskBindings, "")
			wantLegacy := "bound Skill exact version unavailable"
			if kind == "invalid-revision" {
				wantLegacy = "bound Skill integrity changed"
			}
			if legacyErr == nil || legacyErr.Error() != wantLegacy {
				t.Fatalf("R3 error contract changed: %v", legacyErr)
			}
			// Structural reconciliation still works, and mission writes remain
			// available: only the subsequent Engine Skill authority read fails.
			if err = s.RecoverMCPInvocations(m.ID); err != nil {
				t.Fatal(err)
			}
			beforeRequests, _ := server.counts()
			err = e.Recover(context.Background())
			got, getErr := s.GetMission(m.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if kind == "sql-infrastructure" {
				var databaseError sqlite3.Error
				if !errors.As(err, &databaseError) {
					t.Fatalf("SQL failure lost its type or was mislabeled corruption: %v; state=%s stage=%s", err, got.State, got.Stage)
				}
				if got.Revision != m.Revision || got.Epoch != m.Epoch || got.Stage != m.Stage || got.State != m.State {
					t.Fatal("infrastructure failure rewrote waiting checkpoint", got)
				}
			} else {
				if err != nil {
					t.Fatal("genuine Skill corruption was not isolated", err)
				}
				if got.State != "blocked" || got.Stage != "mcp_corrupt" || got.Epoch != m.Epoch+1 {
					t.Fatal("genuine Skill corruption retained authority", got)
				}
			}
			afterRequests, _ := server.counts()
			if afterRequests != beforeRequests {
				t.Fatal("recovery dispatched network")
			}
		})
	}
}
