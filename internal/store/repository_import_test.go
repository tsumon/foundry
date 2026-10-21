package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Initial lookup and transaction recheck must both reject contradictory registry
// evidence. Removing either check must fail without creating a receipt.
func TestRepositoryInspectionRejectsCorruptRegistryEvidence(t *testing.T) {
	for _, kind := range []string{"missing_id", "foreign_id", "column_revision", "json_revision", "unknown_field", "duplicate_id", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			repo, err := s.SaveGitRepository(domain.GitRepository{ID: "repo", Name: "fixture", Path: "/must-not-be-read"}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.GetGitRepositoryForInspection(context.Background(), repo.ID); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(repo)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err = json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			columnRevision := repo.Revision
			switch kind {
			case "missing_id":
				delete(fields, "id")
			case "foreign_id":
				fields["id"] = "foreign"
			case "column_revision":
				columnRevision++
			case "json_revision":
				fields["revision"] = float64(2)
			case "unknown_field":
				fields["unknown_authority"] = true
			case "oversized":
				fields["name"] = strings.Repeat("x", 1<<20)
			}
			raw, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "duplicate_id" {
				raw = append([]byte(`{"id":"repo",`), raw[1:]...)
			}
			// Simulate damaged persisted evidence, including contradictions normally
			// prevented by SQLite CHECK constraints (missing fields already bypass them).
			if _, err = s.db.Exec(`PRAGMA ignore_check_constraints=ON`); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE git_repositories SET revision=?,data=? WHERE id=?`, columnRevision, raw, repo.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`PRAGMA ignore_check_constraints=OFF`); err != nil {
				t.Fatal(err)
			}
			if _, err = s.GetGitRepositoryForInspection(context.Background(), repo.ID); !errors.Is(err, domain.ImportInvalidRecord) {
				t.Errorf("lookup accepted corrupt %s: %v", kind, err)
			}
			v := storedInspection("receipt")
			v.RepositoryID = repo.ID
			v.RepositoryRevision = repo.Revision
			if kind == "json_revision" {
				v.RepositoryRevision = 2
			}
			v.Digest = domain.RepositoryInspectionDigest(v)
			if _, err = s.SaveRepositoryInspectionContext(context.Background(), v, repo.Path); !errors.Is(err, domain.ImportInvalidRecord) {
				t.Errorf("transaction accepted corrupt %s: %v", kind, err)
			}
			rows, err := s.ListRepositoryInspections()
			if err != nil || len(rows) != 0 {
				t.Fatalf("corrupt evidence created receipt: %v %v", rows, err)
			}
		})
	}
}

// A transaction occupies the only connection: deadline/cancel must stop waiting,
// and releasing it afterward must not allow an abandoned receipt to appear.
func TestRepositoryInspectionContextBoundsConnectionWait(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			tx, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			want := context.DeadlineExceeded
			if cancelled {
				cancel()
				want = context.Canceled
			}
			before := time.Now()
			_, err = s.SaveRepositoryInspectionContext(ctx, storedInspection("blocked"), "")
			if !errors.Is(err, want) || time.Since(before) > time.Second {
				t.Fatalf("unbounded wait: %v %v", err, time.Since(before))
			}
			if _, err = s.GetGitRepositoryForInspection(ctx, "repo"); !errors.Is(err, want) {
				t.Fatal(err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			rows, err := s.ListRepositoryInspections()
			if err != nil || len(rows) != 0 {
				t.Fatal("abandoned save continued", rows, err)
			}
		})
	}
}

func storedInspection(id string) domain.RepositoryInspection {
	v := domain.RepositoryInspection{SchemaVersion: 1, ID: id, Spec: domain.RepositoryImportSpec{Scope: []string{"src"}, Acceptance: []string{"test -f src/file.txt"}}, PolicyVersion: "committed_text_v1", Status: "blocked", Stage: "identity", Code: domain.ImportMissingHEAD, CheckedAt: "2026-10-21T00:00:00Z", WorkingTreeStatus: "not_checked"}
	v.Digest = domain.RepositoryInspectionDigest(v)
	return v
}

func TestRepositoryInspectionHistoryBoundAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 103; i++ {
		if _, err = s.SaveRepositoryInspection(storedInspection(fmt.Sprintf("inspection-%03d", i)), ""); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.ListRepositoryInspections()
	if err != nil || len(rows) != 100 || rows[0].ID != "inspection-102" || rows[99].ID != "inspection-003" {
		t.Fatal(rows, err)
	}
	if _, err = s.GetRepositoryInspection("inspection-000"); err != nil {
		t.Fatal("bounded history deleted durable record", err)
	}
}

// Overwriting receipts or persisting a stale registry revision breaks provenance.
func TestRepositoryInspectionPersistenceRechecksRegistryAndRejectsTamper(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v := storedInspection("inspection")
	if _, err = s.SaveRepositoryInspection(v, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveRepositoryInspection(v, ""); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	repo, err := s.SaveGitRepository(domain.GitRepository{ID: "repo", Name: "fixture", Path: "/tmp/repo"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	bound := storedInspection("bound")
	bound.RepositoryID = repo.ID
	bound.RepositoryRevision = repo.Revision
	bound.Digest = domain.RepositoryInspectionDigest(bound)
	repo.Name = "changed"
	if _, err = s.SaveGitRepository(repo, repo.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveRepositoryInspection(bound, repo.Path); err != domain.ImportSourceChanged {
		t.Fatal(err)
	}
	rows, err := s.ListRepositoryInspections()
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	raw, _ := json.Marshal(v)
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	fields["status"] = "ready"
	raw, _ = json.Marshal(fields)
	if _, err = s.db.Exec(`UPDATE repository_inspections SET data=? WHERE id=?`, raw, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetRepositoryInspection(v.ID); err == nil {
		t.Fatal("read tampered record")
	}
	if _, err = s.ListRepositoryInspections(); err == nil {
		t.Fatal("listed tampered record")
	}
}

func TestRepositoryInspectionRejectsUnknownPersistedFields(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v := storedInspection("inspection")
	if _, err = s.SaveRepositoryInspection(v, ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	fields["foreign_authority"] = "ignored"
	raw, _ = json.Marshal(fields)
	if _, err = s.db.Exec(`UPDATE repository_inspections SET data=? WHERE id=?`, raw, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetRepositoryInspection(v.ID); err == nil {
		t.Fatal("read unknown persisted field")
	}
	if _, err = s.ListRepositoryInspections(); err == nil {
		t.Fatal("listed unknown persisted field")
	}
}
