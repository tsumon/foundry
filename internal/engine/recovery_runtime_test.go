package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/platformfs"
	"foundry/internal/sandbox"
	"foundry/internal/store"
	"foundry/internal/vmruntime"
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
	var providerCalls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		http.Error(w, "unexpected provider call", http.StatusServiceUnavailable)
	}))
	defer provider.Close()
	e, _, q, driver, lease := codingImportWithStore(t, provider.URL+"/v1", s)
	oldVM, oldInstances, oldIdentity, oldSource := newInertVMRuntimeBundle(t)
	newVM, newInstances, newIdentity, newSource := newInertVMRuntimeBundle(t, "updated bundle")
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err = platformfs.PrivateTemp(parent, "archive-parent-")
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(parent, "archive")
	oldPath, err := vmruntime.Retain(oldSource, archive, oldIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vmruntime.Retain(newSource, archive, newIdentity); err != nil {
		t.Fatal(err)
	}
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
	evidenceBefore := runtimeRecoveryFiles(t, e.options.DataDir)
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
	// An explicitly retained exact identity restores metadata compatibility, not
	// execution approval. The new default remains distinct from the old mission.
	// Simulate a real restart: neither original bundle location exists, both
	// identities are read back from disk, and the durable store is reopened.
	for _, source := range []string{oldSource, newSource} {
		if err := os.Rename(source, source+"-uninstalled"); err != nil {
			t.Fatal(err)
		}
		// Both paths belong solely to this inert test fixture.
		t.Cleanup(func() { _ = os.Rename(source+"-uninstalled", source) })
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	loaded, err := vmruntime.LoadRetained(archive, newInstances)
	if err != nil || len(loaded) != 2 {
		t.Fatal("persisted identities did not coexist", err)
	}
	var archivedOld, archivedNew *vmruntime.Runtime
	for _, runtime := range loaded {
		switch runtime.Identity() {
		case oldIdentity:
			archivedOld = runtime
		case newIdentity:
			archivedNew = runtime
		}
	}
	if archivedOld == nil || archivedNew == nil {
		t.Fatal("lost full archived identity")
	}
	retainedCap, err := NewCodingVMCapability(e.gateway.(*model.Router), archivedNew, loaded...)
	if err != nil {
		t.Fatal(err)
	}
	options := e.options
	options.C4Controlled = retainedCap
	e = New(s, e.gateway, options)
	if err := e.CheckC4ControlledRecovery(); err != nil {
		t.Fatal("exact retained runtime rejected", err)
	}
	if err := e.Recover(context.Background()); err != nil {
		t.Fatal("persisted old runtime recovery failed", err)
	}
	currentProfile, ok := e.CodingVMProfile()
	if !ok || *currentProfile.EmbeddedVM != newVM.Identity() {
		t.Fatal("retention changed new-task default")
	}
	if selected := retainedCap.vmForProfile(*m.Spec.C4); selected != archivedOld || selected == oldVM {
		t.Fatal("old task did not resolve exact runtime")
	}
	if _, err := e.currentC4Readiness(m); err == nil {
		t.Fatal("startup retained stale readiness authority")
	}
	if err = db.QueryRow("SELECT data FROM missions WHERE id=?", m.ID).Scan(&after); err != nil || !bytes.Equal(raw, after) {
		t.Fatal("retention rewrote original task", err)
	}
	if lease.reads != 0 || driver.creates != 0 {
		t.Fatal("retention caused external execution")
	}
	if got, err := s.GetMission(m.ID); err != nil || got.Budget.UsedCalls != 1 || got.Budget.MaxCalls != 7 || !got.Budget.UsageUnknown || !got.C4Operations[0].Passed() {
		t.Fatal("recovery changed budget or original baseline", err)
	}
	if !reflect.DeepEqual(evidenceBefore, runtimeRecoveryFiles(t, e.options.DataDir)) {
		t.Fatal("recovery changed snapshot or evidence bytes")
	}
	// Missing exact old identity must not select the newer retained default.
	missing := filepath.Join(parent, "missing-old")
	if err := os.Rename(oldPath, missing); err != nil {
		t.Fatal(err)
	}
	remaining, err := vmruntime.LoadRetained(archive, newInstances)
	if err != nil || len(remaining) != 1 {
		t.Fatal("missing-old fixture invalid", err)
	}
	missingCap, err := NewCodingVMCapability(e.gateway.(*model.Router), remaining[0], remaining...)
	if err != nil {
		t.Fatal(err)
	}
	e.options.C4Controlled = missingCap
	if err := e.CheckC4ControlledRecovery(); !errors.Is(err, ErrC4RuntimeMismatch) {
		t.Fatal("missing old archive lost mismatch diagnosis", err)
	}
	if err := e.Recover(context.Background()); !errors.Is(err, ErrC4Unavailable) {
		t.Fatal("missing old archive did not deny recovery", err)
	}
	if err := os.Rename(missing, oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldPath, "kernel"), []byte("tampered inert archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if loaded, err := vmruntime.LoadRetained(archive, newInstances); err == nil || len(loaded) != 0 {
		t.Fatal("tampered old archive returned startup authority")
	}
	if err := e.Recover(context.Background()); !errors.Is(err, ErrC4Unavailable) {
		t.Fatal("tampered old archive admitted recovery", err)
	}
	if err = db.QueryRow("SELECT data FROM missions WHERE id=?", m.ID).Scan(&after); err != nil || !bytes.Equal(raw, after) {
		t.Fatal("archive denial changed original snapshots, baseline, journal or budget", err)
	}
	if !reflect.DeepEqual(evidenceBefore, runtimeRecoveryFiles(t, e.options.DataDir)) {
		t.Fatal("archive denial changed evidence files")
	}
	if providerCalls.Load() != 0 || lease.reads != 0 || driver.creates != 0 || driver.starts != 0 || driver.inspects != 0 || driver.removes != 0 {
		t.Fatal("archive recovery performed external activity")
	}
	for _, root := range []string{oldInstances, newInstances} {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatal("archive recovery allocated VM", err)
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

func runtimeRecoveryFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("snapshot/evidence fixture is empty")
	}
	return files
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
