package store

import (
	"context"
	"encoding/json"
	"fmt"
	"foundry/internal/domain"
	"path/filepath"
	"strings"
	"testing"
)

func readyImportInspection(id string) domain.RepositoryInspection {
	v := storedInspection(id)
	v.Status = "ready"
	v.Stage = "complete"
	v.Code = ""
	v.Root = "/fixture"
	v.Commit = strings.Repeat("d", 40)
	n := 1
	size := int64(0)
	v.FileCount = &n
	v.ContextFileCount = &n
	v.TotalBytes = &size
	v.ContextBytes = &size
	v.ContextManifest = []domain.FileMeta{{Path: "src/file.txt", SHA256: strings.Repeat("e", 64)}}
	v.Digest = domain.RepositoryInspectionDigest(v)
	return v
}

func TestRepositoryImportOperationStrictEvidenceAndHistory(t *testing.T) {
	for _, kind := range []string{"id", "revision", "column_status", "unknown", "duplicate", "missing", "oversized", "bad_stage"} {
		t.Run(kind, func(t *testing.T) {
			s := openTestStore(t)
			v := readyImportInspection("inspection")
			if _, err := s.SaveRepositoryInspection(v, ""); err != nil {
				t.Fatal(err)
			}
			o, err := s.BeginRepositoryImport(context.Background(), domain.RepositoryImportOperation{ID: "op", InspectionID: v.ID, MissionID: "mission", IntentDigest: strings.Repeat("a", 64)}, v, "")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(o)
			var fields map[string]any
			json.Unmarshal(raw, &fields)
			switch kind {
			case "id":
				fields["id"] = "foreign"
			case "revision":
				fields["revision"] = 2
			case "unknown":
				fields["unknown"] = true
			case "missing":
				delete(fields, "intent_digest")
			case "oversized":
				fields["stage"] = strings.Repeat("x", 1<<20)
			case "bad_stage":
				fields["stage"] = "complete"
			}
			raw, _ = json.Marshal(fields)
			if kind == "duplicate" {
				raw = append([]byte(`{"id":"op",`), raw[1:]...)
			}
			if kind == "column_status" {
				_, err = s.db.Exec(`UPDATE repository_import_operations SET status='committed' WHERE id='op'`)
			} else {
				_, err = s.db.Exec(`UPDATE repository_import_operations SET data=? WHERE id='op'`, raw)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.GetRepositoryImportOperation("op"); err == nil {
				t.Fatal("corrupt operation accepted")
			}
			if _, err = s.ListRepositoryImportOperations(); err == nil {
				t.Fatal("corrupt history accepted")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 102; i++ {
		id := fmt.Sprintf("entry-%03d", i)
		v := readyImportInspection(id)
		if _, err = s.SaveRepositoryInspection(v, ""); err != nil {
			t.Fatal(err)
		}
		if _, err = s.BeginRepositoryImport(context.Background(), domain.RepositoryImportOperation{ID: id, InspectionID: id, MissionID: id, IntentDigest: strings.Repeat("a", 64)}, v, ""); err != nil {
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
	rows, err := s.ListRepositoryImportOperations()
	if err != nil || len(rows) != 100 || rows[0].ID != "entry-101" || rows[99].ID != "entry-002" {
		t.Fatal(rows, err)
	}
	if old, err := s.GetRepositoryImportOperation("entry-000"); err != nil || old.Status != "pending" {
		t.Fatal(old, err)
	}
}

func TestRepositoryImportOperationAtomicCommitAndImmutableBinding(t *testing.T) {
	s := openTestStore(t)
	v := storedInspection("inspection")
	v.Status = "ready"
	v.Stage = "complete"
	v.Code = ""
	v.Root = "/fixture"
	v.Commit = strings.Repeat("d", 40)
	n := 1
	size := int64(0)
	v.FileCount = &n
	v.ContextFileCount = &n
	v.TotalBytes = &size
	v.ContextBytes = &size
	v.ContextManifest = []domain.FileMeta{{Path: "src/file.txt", SHA256: strings.Repeat("e", 64)}}
	v.Digest = domain.RepositoryInspectionDigest(v)
	if _, err := s.SaveRepositoryInspection(v, ""); err != nil {
		t.Fatal(err)
	}
	o, err := s.BeginRepositoryImport(context.Background(), domain.RepositoryImportOperation{ID: "op", InspectionID: v.ID, MissionID: "mission", IntentDigest: strings.Repeat("a", 64)}, v, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginRepositoryImport(context.Background(), domain.RepositoryImportOperation{ID: "other", InspectionID: v.ID, MissionID: "other", IntentDigest: o.IntentDigest}, v, ""); err == nil {
		t.Fatal("selection consumed twice")
	}
	b := domain.RepositoryImportBinding{SchemaVersion: 1, InspectionID: v.ID, InspectionDigest: v.Digest, ImportOperationID: o.ID, Root: v.Root, Commit: v.Commit, BaseDigest: strings.Repeat("b", 64), ImportSpecDigest: domain.RepositoryImportSpecDigest(v.Spec)}
	ref := domain.GenerationRef{ID: strings.Repeat("c", 32), Digest: b.BaseDigest}
	o, err = s.PrepareRepositoryImport(o, b, ref)
	if err != nil {
		t.Fatal(err)
	}
	m := domain.Mission{ID: o.MissionID, ImportVersion: 1, RepositoryImport: &b, CodingVersion: 1, WorkspaceMode: "generations_v1", State: "draft", RepoPath: v.Root, BaseCommit: v.Commit, CandidateGeneration: &ref, CandidateDigest: ref.Digest}
	if _, err = s.CreateMission(m); err == nil {
		t.Fatal("import bypassed atomic commit")
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_import BEFORE UPDATE ON repository_import_operations WHEN NEW.status='committed' BEGIN SELECT RAISE(ABORT,'fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitRepositoryImport(m, o, ""); err == nil {
		t.Fatal("commit fault ignored")
	}
	if _, err = s.GetMission(m.ID); err != ErrNotFound {
		t.Fatal("non-atomic mission", err)
	}
	persisted, err := s.GetRepositoryImportOperation(o.ID)
	if err != nil || persisted.Status != "prepared" {
		t.Fatal(persisted, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_import`); err != nil {
		t.Fatal(err)
	}
	m, err = s.CommitRepositoryImport(m, o, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ValidateMissionRepositoryImport(m); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateMission(m.ID, m.Revision, "tamper", func(m *domain.Mission) error { m.ImportVersion = 0; m.RepositoryImport = nil; return nil }); err == nil {
		t.Fatal("binding removed")
	}
	stripped := m
	stripped.ImportVersion = 0
	stripped.RepositoryImport = nil
	if err = s.ValidateMissionRepositoryImport(stripped); err == nil {
		t.Fatal("raw downgrade accepted")
	}
	foreign := m
	foreign.ID = "foreign"
	if err = s.ValidateMissionRepositoryImport(foreign); err == nil {
		t.Fatal("cross-task binding accepted")
	}
}
