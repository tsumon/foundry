package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func TestWorkspaceModeImmutableIncludingOldWriter(t *testing.T) {
	for _, mode := range []string{"", domain.WorkspaceModeGenerationsV1, "future"} {
		t.Run("mode_"+mode, func(t *testing.T) {
			s := openTestStore(t)
			m, e := s.CreateMission(domain.Mission{ID: "mode", WorkspaceMode: mode})
			if mode == "future" {
				if e == nil {
					t.Fatal("future creation accepted")
				}
				// Simulate an existing newer writer, not a supported creation request.
				_, e = s.db.Exec(`INSERT INTO missions VALUES('mode',1,'draft','then',?)`, `{"id":"mode","revision":1,"state":"draft","workspace_mode":"future"}`)
				m, _ = s.GetMission("mode")
			}
			if e != nil {
				t.Fatal(e)
			}
			_, e = s.UpdateMission(m.ID, m.Revision, "fixture.mode", func(m *domain.Mission) error { m.WorkspaceMode = "changed"; return nil })
			if e == nil {
				t.Fatal("Go mode mutation accepted")
			}
			if mode != "" {
				if _, e = s.db.Exec(`UPDATE missions SET data=json_remove(data,'$.workspace_mode') WHERE id=?`, m.ID); e == nil {
					t.Fatal("old writer stripped mode")
				}
			}
			got, e := s.GetMission(m.ID)
			if e != nil || got.WorkspaceMode != mode {
				t.Fatalf("mode=%+v %v", got, e)
			}
		})
	}
}

func TestLegacyMissionUpgradeBackupAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, e := sql.Open("sqlite3", path)
	if e != nil {
		t.Fatal(e)
	}
	old := `{"id":"old","revision":9,"state":"awaiting_approval","plan":{"revision":7,"summary":"legacy","steps":[]},"approvals":[{"kind":"proposal","digest":"legacy","revision":9,"created_at":"then"}]}`
	_, e = db.Exec(`CREATE TABLE missions(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,state TEXT NOT NULL,updated_at TEXT NOT NULL,data BLOB NOT NULL); INSERT INTO missions VALUES('old',9,'awaiting_approval','then',?)`, old)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.GetMission("old")
	if e != nil || got.WorkspaceMode != "" || got.CandidateGeneration != nil || got.Plan.Revision != 7 {
		t.Fatalf("legacy=%+v %v", got, e)
	}
	raw, e := json.Marshal(got)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"workspace_mode", "candidate_generation", "plan_revision", "operation_id", "patch_sha256", "report_sha256"} {
		if strings.Contains(string(raw), `"`+name+`"`) {
			t.Fatalf("new field materialized: %s", raw)
		}
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	// VACUUM INTO is a consistent SQLite snapshot, including committed WAL rows.
	if _, e = s.db.Exec(`VACUUM INTO ?`, backup); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	recovered, e := Open(backup)
	if e != nil {
		t.Fatal(e)
	}
	defer recovered.Close()
	copy, e := recovered.GetMission("old")
	if e != nil || copy.Revision != 9 || copy.Plan.Summary != "legacy" || copy.WorkspaceMode != "" {
		t.Fatalf("backup=%+v %v", copy, e)
	}
	history, e := recovered.ListPlanRevisions("old")
	if e != nil || history == nil || len(history) != 0 {
		t.Fatalf("history=%+v %v", history, e)
	}
}

func TestReliabilityInitializationFailureIsAtomic(t *testing.T) {
	s := openTestStore(t)
	if _, e := s.CreateMission(domain.Mission{ID: "old"}); e != nil {
		t.Fatal(e)
	}
	// Simulate a name collision on a later migration statement.
	_, e := s.db.Exec(`DROP TABLE local_operations; DROP TABLE plan_revisions; CREATE VIEW plan_revisions AS SELECT 1 AS mission_id`)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.initializeReliability(); e == nil {
		t.Fatal("incompatible schema accepted")
	}
	var count int
	if e = s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='local_operations' AND type='table'`).Scan(&count); e != nil || count != 0 {
		t.Fatalf("partial schema survived: %d %v", count, e)
	}
	if _, e = s.GetMission("old"); e != nil {
		t.Fatal(e)
	}
}
