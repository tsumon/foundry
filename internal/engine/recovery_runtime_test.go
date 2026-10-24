package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/sandbox"
	"foundry/internal/store"
)

func TestCodingRecoveryRuntimeMismatchIsReadOnlyAndNotSnapshotCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e, _, q, driver, lease := codingImportWithStore(t, "http://127.0.0.1:1/v1", s)
	oldVM, oldInstances, _ := newInertVMRuntime(t)
	newVM, newInstances, _ := newInertVMRuntime(t, "updated bundle")
	oldCap, err := NewCodingVMCapability(e.gateway.(*model.Router), oldVM)
	if err != nil {
		t.Fatal(err)
	}
	e.options.C4Controlled = oldCap
	p, ok := e.CodingVMProfile()
	if !ok {
		t.Fatal("missing inert VM profile")
	}
	q.Spec.C4, q.Spec.MaxCalls = &p, 7
	m, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	m = seedInertVMBaseline(t, e, m)
	// Only this throwaway fixture is seeded. Production preflight must not write.
	m.Budget.UsedCalls, m.Budget.UsageUnknown = 1, true
	m.State = "blocked"
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE missions SET state=?,data=? WHERE id=?", m.State, raw, m.ID); err != nil {
		t.Fatal(err)
	}
	if err = CheckCodingRecoveryModelSnapshots(s); err != nil {
		t.Fatal("valid frozen snapshot rejected", err)
	}
	if err = e.CheckC4ControlledRecovery(); err != nil {
		t.Fatal("matching installed runtime rejected", err)
	}
	newCap, err := NewCodingVMCapability(e.gateway.(*model.Router), newVM)
	if err != nil {
		t.Fatal(err)
	}
	e.options.C4Controlled = newCap
	err = e.CheckC4ControlledRecovery()
	if !errors.Is(err, ErrC4Unavailable) || !errors.Is(err, ErrC4RuntimeMismatch) {
		t.Fatalf("want distinct runtime mismatch without relaxing denial, got %v", err)
	}
	if e.c4ControlledGate(m, false) == nil || e.C4ControlledStatus(m).Available {
		t.Fatal("old runtime authority accepted")
	}
	if err = e.Recover(context.Background()); !errors.Is(err, ErrC4Unavailable) {
		t.Fatal("runtime mismatch allowed startup recovery", err)
	}
	if _, err = e.PrepareC4Readiness(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("mismatched runtime admitted readiness")
	}
	var after []byte
	if err = db.QueryRow("SELECT data FROM missions WHERE id=?", m.ID).Scan(&after); err != nil || !bytes.Equal(raw, after) {
		t.Fatal("preflight rewrote immutable history or consumed budget", err)
	}
	if lease.reads != 0 || driver.creates != 0 {
		t.Fatal("preflight accessed credential or external execution")
	}
	for _, root := range []string{oldInstances, newInstances} {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatal("preflight allocated a VM", err)
		}
	}
	// Corruption must never be relabelled as a harmless bundle upgrade.
	m.ModelSnapshot.SchemaVersion = 999
	raw, _ = json.Marshal(m)
	if _, err = db.Exec("UPDATE missions SET data=? WHERE id=?", raw, m.ID); err != nil {
		t.Fatal(err)
	}
	if err = e.CheckC4ControlledRecovery(); !errors.Is(err, ErrC4Unavailable) || strings.Contains(err.Error(), "installed embedded VM") {
		t.Fatal("corrupt snapshot misclassified", err)
	}
}

// Durable synthetic journal facts, not evidence of real VM execution. The
// inert helper is never launched; preflight must preserve every fact unchanged.
func seedInertVMBaseline(t *testing.T, e *Engine, m domain.Mission) domain.Mission {
	t.Helper()
	ctx := context.Background()
	m, err := e.PrepareC4Readiness(ctx, m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	c := m.C4Controls[0]
	m, err = e.ApproveC4Control(ctx, m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	m, c, err = e.dispatchC4Control(ctx, m.ID, m.Revision, c.ID, c.MaterialDigest, "readiness")
	if err != nil {
		t.Fatal(err)
	}
	m, err = e.finishC4Control(m, c, domain.C4ControlResult{Status: "ready", Fingerprint: strings.Repeat("c", 64), ValidUntil: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	m, err = e.PrepareC4Baseline(ctx, m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	o := m.C4Operations[0]
	m, err = e.ApproveC4Baseline(ctx, m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	o = m.C4Operations[0]
	id, zero := strings.Repeat("b", 64), 0
	for _, change := range []sandbox.C4Change{
		{Stage: "dispatch"}, {Stage: "created", ContainerID: id}, {Stage: "started"},
		{Stage: "result", Result: &domain.C4Result{Class: "passed", Tests: 1, ExitCode: &zero, Output: "inert journal fixture only"}},
		{Stage: "cleanup", Cleanup: &domain.C4Cleanup{State: "confirmed", ContainerID: id, Owner: o.Owner}},
	} {
		o, err = e.c4Persist(m.ID)(ctx, o, change)
		if err != nil {
			t.Fatal(err)
		}
	}
	m, err = e.s.GetMission(m.ID)
	if err != nil || !m.C4Operations[0].Passed() {
		t.Fatal("invalid journal baseline", err)
	}
	return m
}
