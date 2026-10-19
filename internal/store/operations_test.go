package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/domain"
)

const operationID = "11111111111111111111111111111111"
const generationID = "22222222222222222222222222222222"

func operationFixture(t *testing.T, s *Store) (domain.Mission, domain.LocalOperation, domain.LocalOperationResult) {
	t.Helper()
	before := domain.GenerationRef{ID: generationID, Digest: strings.Repeat("a", 64)}
	m, err := s.CreateMission(domain.Mission{ID: "ops", WorkspaceMode: domain.WorkspaceModeGenerationsV1, CandidateGeneration: &before, CandidateDigest: before.Digest, Epoch: 2, Plan: &domain.Plan{Revision: 3}, State: "running"})
	if err != nil {
		t.Fatal(err)
	}
	op := domain.LocalOperation{ID: operationID, MissionID: m.ID, Kind: "apply", ExpectedRevision: m.Revision, Epoch: m.Epoch, PlanRevision: 3, Before: before, BaseDigest: strings.Repeat("b", 64), IntentDigest: strings.Repeat("c", 64), ApprovalDigest: strings.Repeat("d", 64)}
	result := domain.LocalOperationResult{Generation: &domain.GenerationRef{ID: operationID, Digest: strings.Repeat("e", 64)}, Changes: []domain.Change{{Path: "src/a", Before: "old", After: "new"}}}
	return m, op, result
}

func selectResult(result domain.LocalOperationResult) func(*domain.Mission) error {
	return func(m *domain.Mission) error {
		m.CandidateGeneration = result.Generation
		m.CandidateDigest = result.Generation.Digest
		m.Changes = result.Changes
		return nil
	}
}

// Catches loss of the prepared-result binding or atomic mission/event/status commit.
func TestLocalOperationLifecycle(t *testing.T) {
	s := openTestStore(t)
	m, op, result := operationFixture(t, s)
	op.CreatedAt = "untrusted"
	op.UpdatedAt = "untrusted"
	pending, err := s.BeginLocalOperation(op)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != "pending" || pending.CreatedAt == "untrusted" || pending.CreatedAt == "" {
		t.Fatalf("pending=%+v", pending)
	}
	if _, err = s.CommitLocalOperation(op.ID, "candidate.applied", selectResult(result)); err == nil {
		t.Fatal("pending commit accepted")
	}
	prepared, err := s.PrepareLocalOperation(op.ID, result)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Status != "prepared" {
		t.Fatalf("prepared=%+v", prepared)
	}
	if _, err = s.CommitLocalOperation(op.ID, "candidate.applied", func(m *domain.Mission) error { m.CandidateDigest = "wrong"; return nil }); err == nil {
		t.Fatal("unbound selection accepted")
	}
	got, err := s.CommitLocalOperation(op.ID, "candidate.applied", selectResult(result))
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != m.Revision+1 || !reflect.DeepEqual(got.CandidateGeneration, result.Generation) || !reflect.DeepEqual(got.Changes, result.Changes) {
		t.Fatalf("committed=%+v", got)
	}
	stored, err := s.GetLocalOperation(op.ID)
	if err != nil || stored.Status != "committed" {
		t.Fatalf("op=%+v %v", stored, err)
	}
	events, err := s.Events(0, 100)
	if err != nil || len(events) != 2 || events[1].Kind != "candidate.applied" {
		t.Fatalf("events=%+v %v", events, err)
	}
	for _, action := range []func() error{func() error { _, e := s.PrepareLocalOperation(op.ID, result); return e }, func() error { _, e := s.InterruptLocalOperation(op.ID, "late"); return e }, func() error { _, e := s.CommitLocalOperation(op.ID, "again", selectResult(result)); return e }} {
		if action() == nil {
			t.Fatal("terminal transition accepted")
		}
	}
}

func TestLocalOperationRejectsInvalidIntentAndResult(t *testing.T) {
	for _, field := range []string{"id", "kind", "revision", "epoch", "plan", "before", "digest", "mode", "status", "result"} {
		t.Run(field, func(t *testing.T) {
			s := openTestStore(t)
			m, op, result := operationFixture(t, s)
			switch field {
			case "id":
				op.ID = "../bad"
			case "kind":
				op.Kind = "shell"
			case "revision":
				op.ExpectedRevision++
			case "epoch":
				op.Epoch++
			case "plan":
				op.PlanRevision++
			case "before":
				op.Before.Digest = strings.Repeat("f", 64)
			case "digest":
				op.IntentDigest = "bad"
			case "status":
				op.Status = "committed"
			case "result":
				op.Result = &result
			case "mode":
				_, e := s.db.Exec(`UPDATE missions SET data=json_set(data,'$.workspace_mode','future') WHERE id=?`, m.ID)
				if e == nil {
					t.Fatal("mode SQL mutation accepted")
				}
				op.MissionID = "unknown"
				_, e = s.db.Exec(`INSERT INTO missions VALUES('unknown',1,'draft','then',?)`, `{"id":"unknown","revision":1,"state":"draft","workspace_mode":"future"}`)
				if e != nil {
					t.Fatal(e)
				}
			}
			if _, err := s.BeginLocalOperation(op); err == nil {
				t.Fatal("invalid intent accepted")
			}
		})
	}
	s := openTestStore(t)
	_, op, result := operationFixture(t, s)
	if _, err := s.BeginLocalOperation(op); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []domain.LocalOperationResult{{}, {Export: &domain.PreparedExport{}}, {Generation: &domain.GenerationRef{ID: generationID, Digest: strings.Repeat("e", 64)}}, {Generation: result.Generation, Export: &domain.PreparedExport{}}} {
		if _, err := s.PrepareLocalOperation(op.ID, bad); err == nil {
			t.Fatal("invalid result accepted")
		}
	}
	if _, err := s.PrepareLocalOperation(op.ID, result); err != nil {
		t.Fatal(err)
	}
	result.Changes[0].After = "different"
	if _, err := s.PrepareLocalOperation(op.ID, result); err == nil {
		t.Fatal("prepared intent rewritten")
	}
	if _, err := s.BeginLocalOperation(op); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate=%v", err)
	}
	if _, err := s.db.Exec(`UPDATE local_operations SET intent=json_set(intent,'$.intent_digest',?) WHERE id=?`, strings.Repeat("f", 64), op.ID); err == nil {
		t.Fatal("SQL intent rewrite accepted")
	}
}

func TestLocalOperationCommitCAS(t *testing.T) {
	for _, field := range []string{"revision", "epoch", "plan", "before"} {
		t.Run(field, func(t *testing.T) {
			s := openTestStore(t)
			m, op, result := operationFixture(t, s)
			if _, e := s.BeginLocalOperation(op); e != nil {
				t.Fatal(e)
			}
			if _, e := s.PrepareLocalOperation(op.ID, result); e != nil {
				t.Fatal(e)
			}
			// Keep the row revision unchanged to exercise each independently persisted CAS field.
			query := map[string]string{"revision": `UPDATE missions SET revision=revision+1 WHERE id=?`, "epoch": `UPDATE missions SET data=json_set(data,'$.epoch',99) WHERE id=?`, "plan": `UPDATE missions SET data=json_set(data,'$.plan.revision',99) WHERE id=?`, "before": `UPDATE missions SET data=json_set(data,'$.candidate_generation.id','33333333333333333333333333333333') WHERE id=?`}[field]
			if _, e := s.db.Exec(query, m.ID); e != nil {
				t.Fatal(e)
			}
			called := false
			_, e := s.CommitLocalOperation(op.ID, "candidate.applied", func(m *domain.Mission) error { called = true; return selectResult(result)(m) })
			if !errors.Is(e, ErrConflict) || called {
				t.Fatalf("CAS error=%v callback=%v", e, called)
			}
		})
	}
}

func TestLocalOperationInterruptAndMissing(t *testing.T) {
	s := openTestStore(t)
	_, op, _ := operationFixture(t, s)
	if _, e := s.BeginLocalOperation(op); e != nil {
		t.Fatal(e)
	}
	got, e := s.InterruptLocalOperation(op.ID, "restarted")
	if e != nil || got.Status != "interrupted" || got.Error != "restarted" {
		t.Fatalf("interrupt=%+v %v", got, e)
	}
	if _, e = s.InterruptLocalOperation(op.ID, "rewrite"); e == nil {
		t.Fatal("terminal rewrite")
	}
	ops, e := s.ListLocalOperations(op.MissionID)
	if e != nil || len(ops) != 1 {
		t.Fatalf("list=%+v %v", ops, e)
	}
	if _, e = s.GetLocalOperation(generationID); !errors.Is(e, ErrNotFound) {
		t.Fatalf("missing=%v", e)
	}
	if _, e = s.ListLocalOperations("missing"); !errors.Is(e, ErrNotFound) {
		t.Fatalf("missing list=%v", e)
	}
}

func TestLocalOperationExportReportIsPersistedAndBound(t *testing.T) {
	s := openTestStore(t)
	_, op, _ := operationFixture(t, s)
	op.Kind = "export"
	if _, e := s.BeginLocalOperation(op); e == nil {
		t.Fatal("export without persisted report accepted")
	}
	op.ExportReport = "# Stable report\n"
	if _, e := s.BeginLocalOperation(op); e != nil {
		t.Fatal(e)
	}
	result := domain.LocalOperationResult{Export: &domain.PreparedExport{PatchPath: "exports/patch", ReportPath: "exports/report", PatchSHA256: strings.Repeat("a", 64), ReportSHA256: "932a82edcde52ae55d00cb6fc1ec303662f678b0ad959506c6afed55edc894d9e", ReportInput: op.ExportReport}}
	result.Export.ReportInput = "changed"
	if _, e := s.PrepareLocalOperation(op.ID, result); e == nil {
		t.Fatal("report input changed")
	}
	result.Export.ReportInput = op.ExportReport
	// A hash mismatch is rejected even when the report text matches.
	if _, e := s.PrepareLocalOperation(op.ID, result); e == nil {
		t.Fatal("wrong report hash accepted")
	}
	sum := sha256.Sum256([]byte(op.ExportReport))
	result.Export.ReportSHA256 = hex.EncodeToString(sum[:])
	if _, e := s.PrepareLocalOperation(op.ID, result); e != nil {
		t.Fatal(e)
	}
	got, e := s.CommitLocalOperation(op.ID, "delivery.exported", func(m *domain.Mission) error {
		m.State = "delivered"
		m.Delivery = &domain.Delivery{OperationID: op.ID, CandidateDigest: op.Before.Digest, PatchPath: result.Export.PatchPath, ReportPath: result.Export.ReportPath, PatchSHA256: result.Export.PatchSHA256, ReportSHA256: result.Export.ReportSHA256}
		return nil
	})
	if e != nil || got.Delivery.OperationID != op.ID {
		t.Fatalf("export=%+v %v", got, e)
	}
}

func TestResetEmptyChangesRoundTrip(t *testing.T) {
	s := openTestStore(t)
	_, op, result := operationFixture(t, s)
	op.Kind = "reset"
	result.Changes = []domain.Change{}
	if _, e := s.BeginLocalOperation(op); e != nil {
		t.Fatal(e)
	}
	if _, e := s.PrepareLocalOperation(op.ID, result); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CommitLocalOperation(op.ID, "candidate.reset", selectResult(result)); e != nil {
		t.Fatal(e)
	}
}

func TestInterruptedExportCanOnlyReprepareExactPersistedResult(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "prepared"}[prepared], func(t *testing.T) {
			s := openTestStore(t)
			_, op, _ := operationFixture(t, s)
			op.Kind = "export"
			op.ExportReport = "stable report"
			sum := sha256.Sum256([]byte(op.ExportReport))
			result := domain.LocalOperationResult{Export: &domain.PreparedExport{PatchPath: "exports/patch", ReportPath: "exports/report", PatchSHA256: strings.Repeat("a", 64), ReportSHA256: hex.EncodeToString(sum[:]), ReportInput: op.ExportReport}}
			if _, e := s.BeginLocalOperation(op); e != nil {
				t.Fatal(e)
			}
			if prepared {
				if _, e := s.PrepareLocalOperation(op.ID, result); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := s.InterruptLocalOperation(op.ID, "restarted"); e != nil {
				t.Fatal(e)
			}
			if prepared {
				badExport := *result.Export
				badExport.PatchSHA256 = strings.Repeat("b", 64)
				if _, e := s.PrepareLocalOperation(op.ID, domain.LocalOperationResult{Export: &badExport}); e == nil {
					t.Fatal("reclaimed export changed output")
				}
			}
			got, e := s.PrepareLocalOperation(op.ID, result)
			if e != nil || got.Status != "prepared" || got.Error != "" {
				t.Fatalf("explicit reprepare=%+v %v", got, e)
			}
		})
	}
	for _, kind := range []string{"apply", "reset"} {
		t.Run(kind, func(t *testing.T) {
			s := openTestStore(t)
			_, op, result := operationFixture(t, s)
			op.Kind = kind
			if kind == "reset" {
				result.Changes = nil
			}
			if _, e := s.BeginLocalOperation(op); e != nil {
				t.Fatal(e)
			}
			if _, e := s.InterruptLocalOperation(op.ID, "restarted"); e != nil {
				t.Fatal(e)
			}
			if _, e := s.PrepareLocalOperation(op.ID, result); e == nil {
				t.Fatal("interrupted generation reclaimed")
			}
		})
	}
}

func TestLocalOperationCommitRollback(t *testing.T) {
	for _, boundary := range []string{"mutation", "event", "commit"} {
		t.Run(boundary, func(t *testing.T) {
			s := openTestStore(t)
			m, op, result := operationFixture(t, s)
			if _, e := s.BeginLocalOperation(op); e != nil {
				t.Fatal(e)
			}
			if _, e := s.PrepareLocalOperation(op.ID, result); e != nil {
				t.Fatal(e)
			}
			if boundary == "event" {
				if _, e := s.db.Exec(`CREATE TRIGGER fail_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'fixture event failure'); END`); e != nil {
					t.Fatal(e)
				}
			}
			if boundary == "commit" {
				installDeferredFailure(t, s, "events")
			}
			_, e := s.CommitLocalOperation(op.ID, "candidate.applied", func(m *domain.Mission) error {
				selectResult(result)(m)
				if boundary == "mutation" {
					return errors.New("fixture mutation failure")
				}
				return nil
			})
			if e == nil {
				t.Fatal("failure accepted")
			}
			got, e := s.GetMission(m.ID)
			if e != nil || !reflect.DeepEqual(got, m) {
				t.Fatalf("mission changed: %+v %v", got, e)
			}
			stored, e := s.GetLocalOperation(op.ID)
			if e != nil || stored.Status != "prepared" {
				t.Fatalf("operation changed: %+v %v", stored, e)
			}
			events, e := s.Events(0, 100)
			if e != nil || len(events) != 1 {
				t.Fatalf("events changed: %+v %v", events, e)
			}
		})
	}
}

func installDeferredFailure(t *testing.T, s *Store, table string) {
	t.Helper()
	_, e := s.db.Exec(`CREATE TABLE fixture_parent(id INTEGER PRIMARY KEY); CREATE TABLE fixture_child(id INTEGER REFERENCES fixture_parent(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fixture_commit AFTER INSERT ON ` + table + ` BEGIN INSERT INTO fixture_child VALUES(1); END;`)
	if e != nil {
		t.Fatal(e)
	}
}

func TestLocalOperationPersistsAcrossReopen(t *testing.T) {
	for _, boundary := range []string{"pending", "prepared", "committed", "interrupted"} {
		t.Run(boundary, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "operations.db")
			s, e := Open(path)
			if e != nil {
				t.Fatal(e)
			}
			m, op, result := operationFixture(t, s)
			if _, e = s.BeginLocalOperation(op); e != nil {
				t.Fatal(e)
			}
			if boundary == "prepared" || boundary == "committed" {
				if _, e = s.PrepareLocalOperation(op.ID, result); e != nil {
					t.Fatal(e)
				}
			}
			if boundary == "committed" {
				if m, e = s.CommitLocalOperation(op.ID, "candidate.applied", selectResult(result)); e != nil {
					t.Fatal(e)
				}
			}
			if boundary == "interrupted" {
				if _, e = s.InterruptLocalOperation(op.ID, "explicit restart reconciliation"); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			s, e = Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			stored, e := s.GetLocalOperation(op.ID)
			if e != nil || stored.Status != boundary || stored.IntentDigest != op.IntentDigest {
				t.Fatalf("operation after reopen=%+v %v", stored, e)
			}
			selected, e := s.GetMission(m.ID)
			if e != nil || !reflect.DeepEqual(selected, m) {
				t.Fatalf("selection changed after reopen=%+v %v", selected, e)
			}
		})
	}
}

func TestLocalOperationPrepareAndBeginFailureRollback(t *testing.T) {
	for _, boundary := range []string{"begin_insert", "begin_commit", "prepare_write", "prepare_commit"} {
		t.Run(boundary, func(t *testing.T) {
			s := openTestStore(t)
			m, op, result := operationFixture(t, s)
			if strings.HasPrefix(boundary, "prepare") {
				if _, e := s.BeginLocalOperation(op); e != nil {
					t.Fatal(e)
				}
			}
			switch boundary {
			case "begin_insert":
				_, e := s.db.Exec(`CREATE TRIGGER fixture_begin BEFORE INSERT ON local_operations BEGIN SELECT RAISE(ABORT,'fixture insert'); END`)
				if e != nil {
					t.Fatal(e)
				}
			case "begin_commit":
				installDeferredFailure(t, s, "local_operations")
			case "prepare_write":
				_, e := s.db.Exec(`CREATE TRIGGER fixture_prepare BEFORE UPDATE ON local_operations BEGIN SELECT RAISE(ABORT,'fixture prepare'); END`)
				if e != nil {
					t.Fatal(e)
				}
			case "prepare_commit":
				_, e := s.db.Exec(`CREATE TABLE fixture_parent(id INTEGER PRIMARY KEY); CREATE TABLE fixture_child(id INTEGER REFERENCES fixture_parent(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fixture_commit AFTER UPDATE ON local_operations BEGIN INSERT INTO fixture_child VALUES(1); END`)
				if e != nil {
					t.Fatal(e)
				}
			}
			var e error
			if strings.HasPrefix(boundary, "prepare") {
				_, e = s.PrepareLocalOperation(op.ID, result)
			} else {
				_, e = s.BeginLocalOperation(op)
			}
			if e == nil {
				t.Fatal("injected failure accepted")
			}
			stored, e := s.GetLocalOperation(op.ID)
			if strings.HasPrefix(boundary, "prepare") {
				if e != nil || stored.Status != "pending" || stored.Result != nil {
					t.Fatalf("prepare leaked=%+v %v", stored, e)
				}
			} else if !errors.Is(e, ErrNotFound) {
				t.Fatalf("begin leaked=%+v %v", stored, e)
			}
			selected, e := s.GetMission(m.ID)
			if e != nil || !reflect.DeepEqual(selected, m) {
				t.Fatal("intent failure changed selected mission")
			}
		})
	}
}
