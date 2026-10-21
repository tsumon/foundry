package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/sandbox"
	"foundry/internal/store"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestC4EmptySyntheticRuntimeNeverReconcilesLegacy(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f := &c4Forbidden{}
	e := New(s, f, Options{DataDir: t.TempDir(), Runner: f, C4: NewC4SyntheticCapability(sandbox.NewC4SyntheticDriver(&c4Driver{}), &scripted{}, "/synthetic/docker", "unix:///synthetic/docker.sock")})
	if err := e.Recover(context.Background()); err != nil || f.calls != 0 {
		t.Fatal("synthetic startup reached legacy runner", err, f.calls)
	}
}

// Removing store-only recovery would either replay a driver/legacy effect or
// preserve reusable approval after restart. Production must fail preflight first.
func TestC4RecoveryStopsWithoutReplay(t *testing.T) {
	e, s, m, d, f, _ := c4Fixture(t)
	m, err := e.PrepareC4Baseline(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	o := m.C4Operations[0]
	m, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	o = m.C4Operations[0]
	o, err = e.c4Persist(m.ID)(context.Background(), o, sandbox.C4Change{Stage: "dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMission(m.ID)
	if err = CheckRecoveryModelSnapshots(s); err == nil {
		t.Fatal("production preflight accepted C4")
	}
	production := New(s, f, Options{DataDir: e.options.DataDir, Runner: f})
	if err = production.Recover(context.Background()); err == nil {
		t.Fatal("production recovery accepted C4")
	}
	after, _ := s.GetMission(m.ID)
	if after.Revision != m.Revision {
		t.Fatal("production mutated C4")
	}
	restarted := New(s, f, Options{DataDir: e.options.DataDir, Runner: f, C4: e.options.C4})
	if err = restarted.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMission(m.ID)
	if m.C4Operations[0].Result == nil || m.C4Operations[0].Result.Class != "outcome_unknown" || m.State != "blocked" {
		t.Fatal(m)
	}
	if _, err = restarted.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest); err == nil {
		t.Fatal("replayed restart")
	}
	diagnostic, err := restarted.DiagnoseRecovery(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(diagnostic)
	if !strings.Contains(string(raw), "c4") || !strings.Contains(string(raw), "outcome_unknown") {
		t.Fatal(string(raw))
	}
	if d.creates != 0 || f.calls != 0 {
		t.Fatal("recovery effect", d.creates, f.calls)
	}
}

func TestC4LateEpochCASStopsAllFurtherEffects(t *testing.T) {
	e, s, m, d, f, _ := c4Fixture(t)
	m, err := e.PrepareC4Baseline(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	o := m.C4Operations[0]
	m, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	d.afterCreate = func() {
		current, _ := s.GetMission(m.ID)
		if _, err := s.UpdateMission(m.ID, current.Revision, "test.epoch", func(m *domain.Mission) error { m.Epoch++; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = e.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest); err == nil {
		t.Fatal("stale epoch persisted")
	}
	m, _ = s.GetMission(m.ID)
	if d.creates != 1 || d.starts != 0 || d.removes != 0 || m.C4Operations[0].Created != nil || f.calls != 0 {
		t.Fatal("effects after stale authority", m.C4Operations, d.starts, d.removes)
	}
	// Exact operation CAS cannot accept an altered caller snapshot even if all
	// immutable material fields remain identical.
	expected := m.C4Operations[0]
	approval := *expected.Approval
	approval.At = now()
	expected.Approval = &approval
	if _, err = e.c4Persist(m.ID)(context.Background(), expected, sandbox.C4Change{Stage: "result", Result: &domain.C4Result{Class: "outcome_unknown"}}); err == nil {
		t.Fatal("partial operation CAS accepted")
	}
}

func TestC4PauseDoesNotReconcileOrReplay(t *testing.T) {
	e, s, m, d, f, _ := c4Fixture(t)
	d.waiting = make(chan struct{})
	m, err := e.PrepareC4Baseline(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	o := m.C4Operations[0]
	m, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
		done <- err
	}()
	select {
	case <-d.waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("driver did not reach wait")
	}
	current, _ := s.GetMission(m.ID)
	if _, err = e.CleanupC4Operation(context.Background(), m.ID, current.Revision, o.ID, o.MaterialDigest, strings.Repeat("b", 64)); err == nil {
		t.Fatal("cleanup during active operation")
	}
	if err = e.Recover(context.Background()); err == nil {
		t.Fatal("recovery during active operation")
	}
	if _, err = e.Pause(context.Background(), m.ID, current.Revision); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pause failed to stop")
	}
	if f.calls != 0 || d.creates != 1 || d.starts != 1 || d.removes != 0 {
		t.Fatal("pause escaped/replayed", f.calls, d.creates, d.starts, d.removes)
	}
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, _ = s.GetMission(m.ID)
	if current.C4Operations[0].Result == nil || current.C4Operations[0].Result.Class != "outcome_unknown" {
		t.Fatal(current.C4Operations)
	}
}

func TestC4TerminalPersistenceFailureRetainsIntent(t *testing.T) {
	e, s, m, d, f, _ := c4Fixture(t)
	fault, err := sql.Open("sqlite3", d.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fault.Close()
	d.beforeResult = func() {
		if _, err := fault.Exec(`CREATE TRIGGER fail_c4 BEFORE UPDATE ON missions BEGIN SELECT RAISE(ABORT,'synthetic persistence failure'); END`); err != nil {
			t.Fatal(err)
		}
	}
	m, err = e.PrepareC4Baseline(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	o := m.C4Operations[0]
	m, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest); err == nil {
		t.Fatal("terminal write failure ignored")
	}
	m, _ = s.GetMission(m.ID)
	if m.C4Operations[0].Started == nil || m.C4Operations[0].Result != nil || d.removes != 0 {
		t.Fatal("effects after uncertain write", m.C4Operations, d.removes)
	}
	if _, err = fault.Exec(`DROP TRIGGER fail_c4`); err != nil {
		t.Fatal(err)
	}
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMission(m.ID)
	if m.C4Operations[0].Result == nil || m.C4Operations[0].Result.Class != "outcome_unknown" {
		t.Fatal("missing unknown result", m.C4Operations)
	}
	m, err = e.CleanupC4Operation(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if m.C4Operations[0].Passed() || m.C4Operations[0].CleanupState() != "confirmed" || d.creates != 1 || d.removes != 1 || f.calls != 0 {
		t.Fatal("cleanup promoted/replayed", m.C4Operations, d.creates, d.removes, f.calls)
	}
}
