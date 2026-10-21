package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

type diagnosticNoDispatch struct{ calls []string }

func (b *diagnosticNoDispatch) Resolve(context.Context, string) (string, error) {
	b.calls = append(b.calls, "resolve")
	return "", errors.New("forbidden")
}
func (b *diagnosticNoDispatch) Run(context.Context, string, string, string, string) (domain.CommandResult, error) {
	b.calls = append(b.calls, "run")
	return domain.CommandResult{}, errors.New("forbidden")
}
func (b *diagnosticNoDispatch) Reconcile(context.Context) error {
	b.calls = append(b.calls, "reconcile")
	return errors.New("forbidden")
}
func (b *diagnosticNoDispatch) Complete(context.Context, model.Request) (model.Result, error) {
	b.calls = append(b.calls, "model")
	return model.Result{}, errors.New("forbidden")
}

func recoveryDiagnosticFixture(t *testing.T) (*Engine, *store.Store, domain.Mission, *diagnosticNoDispatch) {
	t.Helper()
	_, _, repo, _ := importFixture(t)
	data := t.TempDir()
	s, err := store.Open(filepath.Join(data, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	b := &diagnosticNoDispatch{}
	e := New(s, b, Options{DataDir: data, Runner: b})
	m, err := e.Create(context.Background(), createImportRequest(t, e, repo))
	if err != nil {
		t.Fatal(err)
	}
	return e, s, m, b
}

func diagnosticCheck(t *testing.T, d domain.RecoveryDiagnostic, id, want string) domain.RecoveryCheck {
	t.Helper()
	for _, c := range d.Checks {
		if c.ID == id {
			if string(c.Status) != want {
				t.Fatalf("%s: want %s, got %+v", id, want, c)
			}
			return c
		}
	}
	t.Fatalf("check %s absent: %+v", id, d)
	return domain.RecoveryCheck{}
}

func diagnosePure(t *testing.T, e *Engine, m domain.Mission) domain.RecoveryDiagnostic {
	t.Helper()
	before, err := e.s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := e.s.Events(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := e.s.ListLocalOperations(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	imports, err := e.s.ListRepositoryImportOperations()
	if err != nil {
		t.Fatal(err)
	}
	disk := importSourceDigest(t, e.dir(m.ID))
	d, err := e.DiagnoseRecovery(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := e.s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	endEvents, _ := e.s.Events(0, 1000)
	endOps, _ := e.s.ListLocalOperations(m.ID)
	endImports, _ := e.s.ListRepositoryImportOperations()
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(events, endEvents) || !reflect.DeepEqual(ops, endOps) || !reflect.DeepEqual(imports, endImports) || disk != importSourceDigest(t, e.dir(m.ID)) {
		t.Fatal("diagnosis mutated durable records or managed files")
	}
	if d.Stale || d.Revision != before.Revision || d.Epoch != before.Epoch || len(d.Checks) != 10 {
		t.Fatalf("unexpected diagnostic identity %+v", d)
	}
	return d
}

// Catches source re-access, false-success absence, and cross-dimension coupling.
func TestRecoveryDiagnosticFrozenSourceAndCorruption(t *testing.T) {
	for _, kind := range []string{"intact", "candidate_missing", "candidate_corrupt", "base_missing", "base_corrupt", "missing_selection", "stripped_import", "malformed_import", "unsupported_import", "unsupported_coding", "unsupported_workspace", "corrupt_trace"} {
		t.Run(kind, func(t *testing.T) {
			e, s, m, b := recoveryDiagnosticFixture(t)
			if err := os.Rename(m.RepoPath, m.RepoPath+"-gone"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Rename(m.RepoPath+"-gone", m.RepoPath) })
			db := failureDB(t, e)
			switch kind {
			case "candidate_missing":
				if err := os.Rename(filepath.Join(e.dir(m.ID), "generations", m.CandidateGeneration.ID), filepath.Join(e.dir(m.ID), "unselected")); err != nil {
					t.Fatal(err)
				}
			case "candidate_corrupt":
				if err := os.WriteFile(filepath.Join(e.dir(m.ID), "generations", m.CandidateGeneration.ID, "tree/file.txt"), []byte("changed"), 0644); err != nil {
					t.Fatal(err)
				}
			case "base_missing":
				if err := os.Rename(filepath.Join(e.dir(m.ID), "base"), filepath.Join(e.dir(m.ID), "unavailable-base")); err != nil {
					t.Fatal(err)
				}
			case "base_corrupt":
				if err := os.WriteFile(filepath.Join(e.dir(m.ID), "base/file.txt"), []byte("changed"), 0644); err != nil {
					t.Fatal(err)
				}
			default:
				statement := ""
				switch kind {
				case "missing_selection":
					statement = `UPDATE missions SET data=json_remove(data,'$.candidate_generation') WHERE id=?`
				case "stripped_import":
					statement = `UPDATE missions SET data=json_remove(data,'$.import_version','$.repository_import') WHERE id=?`
				case "malformed_import":
					statement = `UPDATE missions SET data=json_set(data,'$.repository_import.unknown',1) WHERE id=?`
				case "unsupported_import":
					statement = `UPDATE missions SET data=json_set(data,'$.import_version',19) WHERE id=?`
				case "unsupported_coding":
					statement = `UPDATE missions SET data=json_set(data,'$.coding_version',19) WHERE id=?`
				case "unsupported_workspace":
					if _, err := db.Exec(`DROP TRIGGER immutable_workspace_mode`); err != nil {
						t.Fatal(err)
					}
					statement = `UPDATE missions SET data=json_set(data,'$.workspace_mode','future') WHERE id=?`
				case "corrupt_trace":
					statement = `UPDATE missions SET data=json_set(data,'$.coding_attempts',json('[{"status":"succeeded"}]')) WHERE id=?`
				}
				if statement != "" {
					if _, err := db.Exec(statement, m.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			d := diagnosePure(t, e, m)
			switch kind {
			case "intact":
				for _, id := range []string{"candidate_record", "candidate", "base_authority", "import_provenance"} {
					diagnosticCheck(t, d, id, "verified")
				}
				for _, id := range []string{"coding_trace", "verification", "reviews", "export", "local_operation"} {
					diagnosticCheck(t, d, id, "not_applicable")
				}
			case "candidate_missing", "candidate_corrupt", "missing_selection":
				diagnosticCheck(t, d, "candidate", "blocked")
				diagnosticCheck(t, d, "base_authority", "verified")
			case "base_missing", "base_corrupt":
				diagnosticCheck(t, d, "base_authority", "blocked")
				diagnosticCheck(t, d, "import_provenance", "blocked")
				diagnosticCheck(t, d, "candidate", "verified")
			case "stripped_import", "malformed_import":
				diagnosticCheck(t, d, "import_provenance", "blocked")
			case "unsupported_import":
				diagnosticCheck(t, d, "import_provenance", "unsupported")
			case "unsupported_coding", "unsupported_workspace":
				diagnosticCheck(t, d, "candidate", "unsupported")
			case "corrupt_trace":
				diagnosticCheck(t, d, "coding_trace", "blocked")
			}
			if len(b.calls) != 0 {
				t.Fatal("dependency dispatched", b.calls)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(filepath.Join(e.options.DataDir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			restart := New(reopened, b, e.options)
			again := diagnosePure(t, restart, m)
			if !reflect.DeepEqual(d.Checks, again.Checks) {
				t.Fatal("reopen changed diagnosis")
			}
		})
	}
}

func TestRecoveryDiagnosticPreparedCandidateDoesNotSelect(t *testing.T) {
	e, s, m, b := recoveryDiagnosticFixture(t)
	op, err := e.localIntent(m, "reset", nil)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.BeginLocalOperation(op)
	if err != nil {
		t.Fatal(err)
	}
	ref, _, err := workspace.PrepareGeneration(e.dir(m.ID), op.ID, filepath.Join(e.dir(m.ID), "base"), m.CandidateGeneration, nil, m.Spec.Scope, m.Spec.Protected)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareLocalOperation(op.ID, domain.LocalOperationResult{Generation: &ref}); err != nil {
		t.Fatal(err)
	}
	d := diagnosePure(t, e, m)
	diagnosticCheck(t, d, "candidate", "verified")
	diagnosticCheck(t, d, "local_operation", "unknown")
	after, _ := s.GetMission(m.ID)
	if *after.CandidateGeneration != *m.CandidateGeneration || len(b.calls) != 0 {
		t.Fatal("prepared result was adopted")
	}
}

func TestRecoveryDiagnosticInterruptedSelectionAndFailedReplan(t *testing.T) {
	for _, kind := range []string{"selection", "failed"} {
		t.Run(kind, func(t *testing.T) {
			var e *Engine
			var m domain.Mission
			if kind == "selection" {
				e, _, _, m = recoveredCodingSelection(t)
			} else {
				var s *store.Store
				e, s, _, _, m = codingSetup(t, plan, edit, codingReject)
				if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
					t.Fatal(err)
				}
				finishCoding(e)
				m, _ = s.GetMission(m.ID)
				if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
					t.Fatal(err)
				}
				finishCoding(e)
				m, _ = s.GetMission(m.ID)
			}
			b := &diagnosticNoDispatch{}
			e.runner, e.gateway = b, b
			d := diagnosePure(t, e, m)
			diagnosticCheck(t, d, "candidate", "verified")
			if kind == "selection" {
				diagnosticCheck(t, d, "coding_trace", "unknown")
			}
			if !slices.Contains(d.NextActions, domain.RecoveryReplan) {
				t.Fatalf("explicit replan decision absent: %+v", d)
			}
			if len(b.calls) != 0 {
				t.Fatal(b.calls)
			}
		})
	}
}

func TestRecoveryDiagnosticExportAndReviewBindings(t *testing.T) {
	for _, kind := range []string{"intact", "prepared_export", "delivered", "corrupt_patch", "corrupt_report", "corrupt_review", "corrupt_verification", "corrupt_trace_binding", "missing_execution_approval"} {
		t.Run(kind, func(t *testing.T) {
			e, s, _, _, m := codingSetup(t, plan, edit, codingPass, codingPass)
			if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			if m.State != "awaiting_delivery" {
				t.Fatal(m.State, m.Error)
			}
			if kind == "missing_execution_approval" {
				last := len(m.Approvals) - 1
				if m.Approvals[last].Kind != "execute" {
					t.Fatal("fixture lacks execution approval")
				}
				if _, err := failureDB(t, e).Exec(`UPDATE missions SET data=json_remove(data,?) WHERE id=?`, fmt.Sprintf("$.approvals[%d]", last), m.ID); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "prepared_export" {
				op, err := e.exportIntent(m)
				if err != nil {
					t.Fatal(err)
				}
				op, err = s.BeginLocalOperation(op)
				if err != nil {
					t.Fatal(err)
				}
				candidate, err := e.selectedCandidate(m)
				if err != nil {
					t.Fatal(err)
				}
				p, err := workspace.PrepareExport(context.Background(), e.dir(m.ID), op.ID, candidate, op.ExportReport)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.PrepareLocalOperation(op.ID, domain.LocalOperationResult{Export: &domain.PreparedExport{PatchPath: p.PatchPath, ReportPath: p.ReportPath, PatchSHA256: p.PatchSHA256, ReportSHA256: p.ReportSHA256, ReportInput: op.ExportReport}}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "delivered" || kind == "corrupt_patch" || kind == "corrupt_report" {
				var err error
				m, err = e.Deliver(context.Background(), m.ID, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				if kind != "delivered" {
					path := m.Delivery.PatchPath
					if kind == "corrupt_report" {
						path = m.Delivery.ReportPath
					}
					if err = os.WriteFile(path, []byte("RAW_PRIVATE_ERROR"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			if kind == "corrupt_review" || kind == "corrupt_verification" || kind == "corrupt_trace_binding" {
				path := "$.reviews[0].binding_digest"
				if kind == "corrupt_verification" {
					path = "$.evidence[0].candidate_digest"
				}
				if kind == "corrupt_trace_binding" {
					path = "$.coding_attempts[0].after.digest"
				}
				if _, err := failureDB(t, e).Exec(`UPDATE missions SET data=json_set(data,?,'changed') WHERE id=?`, path, m.ID); err != nil {
					t.Fatal(err)
				}
			}
			b := &diagnosticNoDispatch{}
			e.runner, e.gateway = b, b
			d := diagnosePure(t, e, m)
			switch kind {
			case "intact", "prepared_export":
				diagnosticCheck(t, d, "verification", "verified")
				diagnosticCheck(t, d, "reviews", "verified")
				if !slices.Contains(d.NextActions, domain.RecoveryDeliveryDecision) {
					t.Fatal(d.NextActions)
				}
				if kind == "prepared_export" {
					diagnosticCheck(t, d, "local_operation", "unknown")
					diagnosticCheck(t, d, "export", "not_applicable")
				}
			case "delivered":
				diagnosticCheck(t, d, "export", "verified")
			case "corrupt_patch", "corrupt_report":
				diagnosticCheck(t, d, "export", "blocked")
			case "corrupt_review":
				diagnosticCheck(t, d, "reviews", "blocked")
			case "corrupt_verification":
				diagnosticCheck(t, d, "verification", "blocked")
			case "corrupt_trace_binding":
				diagnosticCheck(t, d, "coding_trace", "blocked")
			case "missing_execution_approval":
				diagnosticCheck(t, d, "base_authority", "blocked")
			}
			raw, _ := json.Marshal(d)
			if len(b.calls) != 0 || strings.Contains(string(raw), "RAW_PRIVATE_ERROR") {
				t.Fatal("dispatch or unsafe projection", b.calls, string(raw))
			}
		})
	}
}

// Real concurrent CAS updates must make observations stale without holding the
// engine lock across disk reads, and stale results must expose no decision links.
func TestRecoveryDiagnosticConcurrentRevisionAndEpoch(t *testing.T) {
	e, s, m, _ := recoveryDiagnosticFixture(t)
	var wg sync.WaitGroup
	stop, started := make(chan struct{}), make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		first := true
		for {
			select {
			case <-stop:
				return
			default:
			}
			current, err := s.GetMission(m.ID)
			if err != nil {
				return
			}
			_, err = s.UpdateMission(m.ID, current.Revision, "fixture.concurrent", func(x *domain.Mission) error { x.Epoch++; return nil })
			if err != nil {
				return
			}
			if first {
				close(started)
				first = false
			}
		}
	}()
	<-started
	found := false
	for i := 0; i < 20; i++ {
		d, err := e.DiagnoseRecovery(context.Background(), m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Stale {
			found = true
			if len(d.NextActions) != 0 {
				t.Fatal("stale actions", d.NextActions)
			}
			break
		}
	}
	close(stop)
	wg.Wait()
	if !found {
		t.Fatal("concurrent mutation was not fenced")
	}
}

func TestRecoveryDiagnosticHistoricalAndUnknownInvocation(t *testing.T) {
	e, _, _, _, m := setup(t)
	d := diagnosePure(t, e, m)
	diagnosticCheck(t, d, "import_provenance", "not_applicable")
	diagnosticCheck(t, d, "coding_trace", "not_applicable")
	e, s, _, _, m := controlledSetup(t)
	m, _ = unfinishedMainFixture(t, e, s, m, "paused", true)
	before, _ := s.ListInvocations(m.ID, 0, 100)
	rows, readErr := s.RecoveryInvocations(m.ID)
	c := diagnoseInvocationRecords(rows, readErr)
	after, _ := s.ListInvocations(m.ID, 0, 100)
	if c.Status != domain.RecoveryUnknown || !reflect.DeepEqual(before, after) {
		t.Fatal(c)
	}
	d = diagnosePure(t, e, m)
	diagnosticCheck(t, d, "candidate", "unsupported")
}

func TestRecoveryDiagnosticImportInitialGenerationIsNotCurrentSelection(t *testing.T) {
	e, _, m, _ := recoveryDiagnosticFixture(t)
	initial := *m.CandidateGeneration
	var err error
	m, err = e.resetGeneration(m)
	if err != nil {
		t.Fatal(err)
	}
	if *m.CandidateGeneration == initial {
		t.Fatal("fixture did not select later generation")
	}
	d := diagnosePure(t, e, m)
	diagnosticCheck(t, d, "import_provenance", "verified")
	diagnosticCheck(t, d, "candidate", "verified")
}

// Catches accidentally treating any syntactically decoded committed row as
// evidence even when its result is gone, or reading through a mission symlink.
func TestRecoveryDiagnosticRejectsCorruptOperationAndManagedRoot(t *testing.T) {
	for _, kind := range []string{"result", "selection", "root"} {
		t.Run(kind, func(t *testing.T) {
			e, _, m, _ := recoveryDiagnosticFixture(t)
			var err error
			m, err = e.resetGeneration(m)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "root" {
				outside := filepath.Join(t.TempDir(), "moved")
				if err := os.Rename(e.dir(m.ID), outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, e.dir(m.ID)); err != nil {
					t.Fatal(err)
				}
			} else {
				db := failureDB(t, e)
				if _, err := db.Exec(`DROP TRIGGER operation_transition`); err != nil {
					t.Fatal(err)
				}
				statement := `UPDATE local_operations SET result=NULL WHERE id=?`
				if kind == "selection" {
					statement = `UPDATE local_operations SET status='prepared' WHERE id=?`
				}
				if _, err := db.Exec(statement, m.CandidateGeneration.ID); err != nil {
					t.Fatal(err)
				}
			}
			d := diagnosePure(t, e, m)
			switch kind {
			case "result":
				diagnosticCheck(t, d, "local_operation", "blocked")
			case "selection":
				diagnosticCheck(t, d, "candidate_record", "blocked")
			case "root":
				diagnosticCheck(t, d, "base_authority", "blocked")
			}
		})
	}
}
