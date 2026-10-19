package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommittedExportSealBindsCandidateAndBase(t *testing.T) {
	for _, field := range []string{"candidate_id", "candidate_digest", "base_digest"} {
		t.Run(field, func(t *testing.T) {
			e, s, g, _, m := readyGeneration(t)
			m, err := e.Deliver(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			patch, err := os.ReadFile(m.Delivery.PatchPath)
			if err != nil {
				t.Fatal(err)
			}
			report, err := os.ReadFile(m.Delivery.ReportPath)
			if err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(filepath.Dir(m.Delivery.PatchPath), "manifest.json")
			raw, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var seal map[string]any
			if err = json.Unmarshal(raw, &seal); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "candidate_id":
				seal["candidate"].(map[string]any)["id"] = strings.Repeat("a", 32)
			case "candidate_digest":
				seal["candidate"].(map[string]any)["digest"] = strings.Repeat("b", 64)
			case "base_digest":
				seal["base_digest"] = strings.Repeat("c", 64)
			}
			damaged, err := json.Marshal(seal)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(manifestPath, damaged, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
				t.Error("repeat delivery accepted changed seal binding")
			}
			for _, kind := range []string{"patch", "report"} {
				if _, err = e.ReadDelivery(m.ID, kind); err == nil {
					t.Errorf("%s download accepted changed seal binding", kind)
				}
			}
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != "blocked" || !strings.Contains(got.Error, "corruption") {
				t.Error("recovery failed to block corrupted committed seal")
			}
			for path, want := range map[string][]byte{m.Delivery.PatchPath: patch, m.Delivery.ReportPath: report, manifestPath: damaged} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(want) {
					t.Errorf("artifact overwritten: %s: %v", path, err)
				}
			}
			if len(g.roles) != 5 {
				t.Error("corruption check dispatched model")
			}
		})
	}
}

func failureDB(t *testing.T, e *Engine) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(e.options.DataDir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func reopenEngine(t *testing.T, e *Engine, s *store.Store, g *scripted, r *fakeRunner) (*Engine, *store.Store) {
	t.Helper()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(filepath.Join(e.options.DataDir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	restart := New(reopened, g, Options{DataDir: e.options.DataDir, Runner: r})
	t.Cleanup(func() { restart.Close(); reopened.Close() })
	return restart, reopened
}

func TestFailedGenerationRegistrationInterruptsAndAllowsExplicitReplan(t *testing.T) {
	e, s, g, _, m := generationSetup(t, research, plan)
	old := *m.CandidateGeneration
	db := failureDB(t, e)
	if _, err := db.Exec(`CREATE TRIGGER reject_prepare BEFORE UPDATE OF status ON local_operations WHEN NEW.status='prepared' BEGIN SELECT RAISE(ABORT,'fixture prepare failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("prepare failure accepted")
	}
	got, _ := s.GetMission(m.ID)
	if *got.CandidateGeneration != old || got.Revision != m.Revision || len(g.roles) != 0 {
		t.Fatal("failed preparation changed authority")
	}
	ops, _ := s.ListLocalOperations(m.ID)
	if len(ops) != 1 || ops[0].Status != "interrupted" {
		t.Fatalf("failed operation stranded: %+v", ops)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_prepare`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	wait(t, s, m.ID, "awaiting_approval")
}

func TestFailedGenerationSelectionLeavesOldCandidateAndArchive(t *testing.T) {
	e, s, g, _, m := generationSetup(t)
	old := *m.CandidateGeneration
	db := failureDB(t, e)
	if _, err := db.Exec(`CREATE TRIGGER reject_selection BEFORE UPDATE ON missions WHEN json_extract(NEW.data,'$.state')='planning' BEGIN SELECT RAISE(ABORT,'fixture commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("selection failure accepted")
	}
	got, _ := s.GetMission(m.ID)
	if *got.CandidateGeneration != old || got.Revision != m.Revision || len(got.RunArchives) != 0 || len(g.roles) != 0 {
		t.Fatal("failed transaction changed authority")
	}
	ops, _ := s.ListLocalOperations(m.ID)
	if len(ops) != 1 || ops[0].Status != "interrupted" || ops[0].Result == nil {
		t.Fatalf("lost prepared output: %+v", ops)
	}
}

func readyGeneration(t *testing.T) (*Engine, *store.Store, *scripted, *fakeRunner, domain.Mission) {
	t.Helper()
	e, s, g, r, m := generationSetup(t, research, plan, edit, review, review)
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_approval")
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_delivery")
	return e, s, g, r, m
}

func TestExportExplicitRetryAfterSelectionFailureAndRestart(t *testing.T) {
	for _, variant := range []string{"matching", "drift", "revision"} {
		t.Run(variant, func(t *testing.T) {
			e, s, g, r, m := readyGeneration(t)
			db := failureDB(t, e)
			if _, err := db.Exec(`CREATE TRIGGER reject_delivery BEFORE UPDATE ON missions WHEN json_extract(NEW.data,'$.state')='delivered' BEGIN SELECT RAISE(ABORT,'fixture delivery failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
				t.Fatal("failed delivery committed")
			}
			ops, _ := s.ListLocalOperations(m.ID)
			var export domain.LocalOperation
			for _, op := range ops {
				if op.Kind == "export" {
					export = op
				}
			}
			if export.Status != "interrupted" || export.Result == nil {
				t.Fatal(export)
			}
			before, _ := os.ReadFile(export.Result.Export.PatchPath)
			if _, err := db.Exec(`DROP TRIGGER reject_delivery`); err != nil {
				t.Fatal(err)
			}
			restart, reopened := reopenEngine(t, e, s, g, r)
			s = reopened
			if err := restart.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			current, _ := s.GetMission(m.ID)
			if current.Revision != m.Revision || current.State != "awaiting_delivery" {
				t.Fatal("recovery consumed explicit retry CAS")
			}
			if variant == "drift" {
				if err := os.WriteFile(export.Result.Export.ReportPath, []byte("tamper"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if variant == "revision" {
				var err error
				current, err = s.UpdateMission(m.ID, m.Revision, "fixture.unrelated", func(c *domain.Mission) error {
					c.Messages = append(c.Messages, domain.Message{Role: "user", Content: "hello"})
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			delivered, err := restart.Deliver(context.Background(), m.ID, current.Revision)
			if variant == "drift" {
				if err == nil {
					t.Fatal("mismatching bundle overwritten")
				}
				b, _ := os.ReadFile(export.Result.Export.ReportPath)
				if string(b) != "tamper" {
					t.Fatal("mismatch overwritten")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if variant == "matching" && delivered.Delivery.OperationID != export.ID {
				t.Fatal("matching retry created second bundle")
			}
			if variant == "revision" && delivered.Delivery.OperationID == export.ID {
				t.Fatal("new revision reused stale intent")
			}
			after, _ := os.ReadFile(export.Result.Export.PatchPath)
			if string(before) != string(after) {
				t.Fatal("old output overwritten")
			}
			if len(g.roles) != 5 {
				t.Fatal("replayed model")
			}
		})
	}
}

func TestRecoverGenerationBoundariesNeverSelectsPrepared(t *testing.T) {
	for _, phase := range []string{"pending", "published", "prepared", "committed"} {
		t.Run(phase, func(t *testing.T) {
			e, s, g, r, m := generationSetup(t)
			old := *m.CandidateGeneration
			op, err := e.localIntent(m, "reset", nil)
			if err != nil {
				t.Fatal(err)
			}
			op, err = s.BeginLocalOperation(op)
			if err != nil {
				t.Fatal(err)
			}
			var ref domain.GenerationRef
			if phase != "pending" {
				ref, _, err = workspace.PrepareGeneration(e.dir(m.ID), op.ID, filepath.Join(e.dir(m.ID), "base"), m.CandidateGeneration, nil, m.Spec.Scope, m.Spec.Protected)
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == "prepared" || phase == "committed" {
				op, err = s.PrepareLocalOperation(op.ID, domain.LocalOperationResult{Generation: &ref, Changes: []domain.Change{}})
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == "committed" {
				m, err = s.CommitLocalOperation(op.ID, "mission.planning", func(current *domain.Mission) error { resetRun(current, m.Revision, ref); return nil })
				if err != nil {
					t.Fatal(err)
				}
			}
			restart, reopened := reopenEngine(t, e, s, g, r)
			s = reopened
			if err = restart.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetMission(m.ID)
			record, _ := s.GetLocalOperation(op.ID)
			if phase == "committed" {
				if *got.CandidateGeneration != ref || got.State != "blocked" || record.Status != "committed" {
					t.Fatal(got, record)
				}
			} else if *got.CandidateGeneration != old || record.Status != "interrupted" {
				t.Fatal("uncommitted selected", got, record)
			}
			revision := got.Revision
			if err = restart.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ = s.GetMission(m.ID)
			if got.Revision != revision {
				t.Fatal("recovery not idempotent")
			}
			if len(g.roles) != 0 {
				t.Fatal("replayed model")
			}
		})
	}
}

func TestRecoverApplyBoundariesAndEpochFence(t *testing.T) {
	for _, phase := range []string{"pending", "published", "prepared", "committed", "stale_epoch"} {
		t.Run(phase, func(t *testing.T) {
			e, s, g, r, m := readyGeneration(t)
			old := *m.CandidateGeneration
			edits := []workspace.Edit{{Path: "src/main.txt", Content: "second edit\n"}}
			op, err := e.localIntent(m, "apply", edits)
			if err != nil {
				t.Fatal(err)
			}
			op, err = s.BeginLocalOperation(op)
			if err != nil {
				t.Fatal(err)
			}
			var ref domain.GenerationRef
			var delta []domain.Change
			if phase != "pending" {
				path, err := e.selectedCandidate(m)
				if err != nil {
					t.Fatal(err)
				}
				ref, delta, err = workspace.PrepareGeneration(e.dir(m.ID), op.ID, path, m.CandidateGeneration, edits, m.Spec.Scope, m.Spec.Protected)
				if err != nil {
					t.Fatal(err)
				}
			}
			changes := append(append([]domain.Change{}, m.Changes...), delta...)
			if phase == "prepared" || phase == "committed" || phase == "stale_epoch" {
				op, err = s.PrepareLocalOperation(op.ID, domain.LocalOperationResult{Generation: &ref, Changes: changes})
				if err != nil {
					t.Fatal(err)
				}
			}
			selectResult := func(current *domain.Mission) error {
				current.CandidateGeneration = &ref
				current.CandidateDigest = ref.Digest
				current.Changes = changes
				return nil
			}
			if phase == "committed" {
				m, err = e.commitLocal(op, "candidate.edited", selectResult)
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == "stale_epoch" {
				m, err = s.UpdateMission(m.ID, m.Revision, "fixture.pause", func(current *domain.Mission) error { current.State = "paused"; current.Epoch++; return nil })
				if err != nil {
					t.Fatal(err)
				}
				if _, err = e.commitLocal(op, "candidate.edited", selectResult); err == nil {
					t.Fatal("stale epoch selected")
				}
			}
			restart, reopened := reopenEngine(t, e, s, g, r)
			s = reopened
			if err = restart.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetMission(m.ID)
			record, _ := s.GetLocalOperation(op.ID)
			if phase == "committed" {
				if *got.CandidateGeneration != ref || len(got.Changes) != 2 || record.Status != "committed" {
					t.Fatal("committed generation lost")
				}
			} else if *got.CandidateGeneration != old || record.Status != "interrupted" {
				t.Fatal("unfinished generation selected")
			}
			if len(g.roles) != 5 {
				t.Fatal("recovery replayed work")
			}
		})
	}
}

func TestRecoverExportBoundariesRequireExplicitRetry(t *testing.T) {
	for _, phase := range []string{"pending", "published", "prepared", "committed"} {
		t.Run(phase, func(t *testing.T) {
			e, s, g, r, m := readyGeneration(t)
			var op domain.LocalOperation
			var err error
			if phase == "committed" {
				m, err = e.Deliver(context.Background(), m.ID, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				op, err = s.GetLocalOperation(m.Delivery.OperationID)
			} else {
				op, err = e.exportIntent(m)
				if err != nil {
					t.Fatal(err)
				}
				op, err = s.BeginLocalOperation(op)
				if err != nil {
					t.Fatal(err)
				}
				if phase != "pending" {
					path, err := e.selectedCandidate(m)
					if err != nil {
						t.Fatal(err)
					}
					p, err := workspace.PrepareExport(context.Background(), e.dir(m.ID), op.ID, path, op.ExportReport)
					if err != nil {
						t.Fatal(err)
					}
					if phase == "prepared" {
						op, err = s.PrepareLocalOperation(op.ID, domain.LocalOperationResult{Export: &domain.PreparedExport{PatchPath: p.PatchPath, ReportPath: p.ReportPath, PatchSHA256: p.PatchSHA256, ReportSHA256: p.ReportSHA256, ReportInput: op.ExportReport}})
						if err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			restart, reopened := reopenEngine(t, e, s, g, r)
			s = reopened
			if err = restart.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetMission(m.ID)
			record, _ := s.GetLocalOperation(op.ID)
			if got.Revision != m.Revision {
				t.Fatal("recovery changed export CAS")
			}
			if phase == "committed" {
				if record.Status != "committed" || got.State != "delivered" {
					t.Fatal("lost committed delivery")
				}
			} else if record.Status != "interrupted" || got.Delivery != nil || got.State != "awaiting_delivery" {
				t.Fatal("unfinished export committed automatically")
			}
			delivered, err := restart.Deliver(context.Background(), got.ID, got.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if delivered.Delivery.OperationID != op.ID {
				t.Fatal("explicit retry did not reuse stable intent")
			}
			if len(g.roles) != 5 {
				t.Fatal("recovery replayed model")
			}
		})
	}
}

func TestRecoverCorruptGenerationBlocksWithoutReplay(t *testing.T) {
	e, s, g, _, m := generationSetup(t)
	path, err := workspace.GenerationPath(e.dir(m.ID), *m.CandidateGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path, "src/main.txt"), []byte("tamper"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "blocked")
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("corrupt selection replanned")
	}
	if len(g.roles) != 0 {
		t.Fatal("dispatched despite corruption")
	}
}

func TestRecoverCorruptBaseBlocksBeforeDispatch(t *testing.T) {
	e, s, g, _, m := generationSetup(t)
	if err := os.WriteFile(filepath.Join(e.dir(m.ID), "base/src/main.txt"), []byte("tamper"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMission(m.ID)
	if got.State != "blocked" {
		t.Fatal("corrupt frozen base not blocked")
	}
	if len(g.roles) != 0 {
		t.Fatal("corrupt base dispatched")
	}
}

func TestCorruptDraftDoesNotRequireSandboxCleanup(t *testing.T) {
	e, s, _, r, m := generationSetup(t)
	r.reconcileErr = errors.New("fixture Docker unavailable")
	if err := os.Remove(filepath.Join(e.dir(m.ID), "generations", m.CandidateGeneration.ID, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if err := e.Recover(context.Background()); err != nil {
		t.Fatal("draft corruption incorrectly requires sandbox", err)
	}
	got, _ := s.GetMission(m.ID)
	if got.State != "blocked" {
		t.Fatal("corrupt draft not blocked")
	}
}

func TestDeliveryReadsVerifiedBoundedBytes(t *testing.T) {
	e, s, _, _, m := generationSetup(t, research, plan, edit, review, review)
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_approval")
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_delivery")
	m, err := e.Deliver(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.ReadDelivery(m.ID, "patch")
	if err != nil || len(b) == 0 {
		t.Fatal(err)
	}
	if err = os.WriteFile(m.Delivery.PatchPath, []byte("drift"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ReadDelivery(m.ID, "patch"); err == nil {
		t.Fatal("drift downloaded")
	}
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "blocked")
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("corrupt committed export allowed new work")
	}
}
