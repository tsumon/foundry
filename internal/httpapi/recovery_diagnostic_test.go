package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/testutil"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type diagnosticBoundary struct{ calls []string }

func (b *diagnosticBoundary) Resolve(context.Context, string) (string, error) {
	b.calls = append(b.calls, "resolve")
	return "", errors.New("forbidden")
}
func (b *diagnosticBoundary) Run(context.Context, string, string, string, string) (domain.CommandResult, error) {
	b.calls = append(b.calls, "run")
	return domain.CommandResult{}, errors.New("forbidden")
}
func (b *diagnosticBoundary) Reconcile(context.Context) error {
	b.calls = append(b.calls, "reconcile")
	return errors.New("forbidden")
}
func (b *diagnosticBoundary) Complete(context.Context, model.Request) (model.Result, error) {
	b.calls = append(b.calls, "model")
	return model.Result{}, errors.New("forbidden")
}

func diagnosticHTTPMission(t *testing.T, f *fixture) (domain.Mission, string, *diagnosticBoundary) {
	t.Helper()
	b := &diagnosticBoundary{}
	f.handler.(*server).engine = engine.New(f.store, b, engine.Options{DataDir: f.dataDir, Runner: b})
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-qb", "main"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture"}} {
		cmd := testutil.Git(t, args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	if err := f.store.PutProfile(domain.Profile{ID: "test", Name: "Test", Protocol: "chat_completions", BaseURL: "http://127.0.0.1", Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	e := f.handler.(*server).engine
	v, err := e.InspectRepository(context.Background(), domain.RepositoryInspectionRequest{RepoPath: repo, Consent: true, Spec: domain.RepositoryImportSpec{Scope: []string{"file.txt"}, Acceptance: []string{"test -f file.txt"}, Image: "local"}})
	if err != nil || v.Status != "ready" {
		t.Fatal(v, err)
	}
	m, err := e.Create(context.Background(), engine.CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: v.ID, Digest: v.Digest}, CodingVersion: 1, WorkspaceMode: "generations_v1", Title: "import", Goal: "change", RepoPath: repo, Spec: domain.Spec{Scope: v.Spec.Scope, Acceptance: v.Spec.Acceptance, Image: v.Spec.Image, ProfileID: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	return m, repo, b
}

// All durable columns, including budgets, events, approvals and operation state,
// must be unchanged. This is a logical SQLite snapshot, not a WAL-byte assertion.
func diagnosticDurableSnapshot(t *testing.T, f *fixture) string {
	t.Helper()
	db, err := sql.Open("sqlite3", f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	all := map[string][][]any{}
	for _, table := range []string{"missions", "events", "local_operations", "repository_import_operations", "invocations"} {
		rows, err := db.Query("SELECT * FROM " + table + " ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			all[table] = append(all[table], values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	raw, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Catches a missing route, false-success on missing selected files, mutation,
// recovery dispatch, and bypassed session/origin/host/global-read-only gates.
func TestRecoveryDiagnosticHTTPReadOnly(t *testing.T) {
	f := newFixture(t)
	m, repo, boundary := diagnosticHTTPMission(t, f)
	route := "/api/missions/" + m.ID + "/recovery-diagnostic"
	if rr := f.request("GET", route, "", false); rr.Code != 401 {
		t.Fatal(rr.Code)
	}
	f.authenticate()
	if err := os.Rename(repo, repo+"-gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Rename(repo+"-gone", repo) })
	if err := os.Rename(filepath.Join(f.dataDir, "missions", m.ID, "generations", m.CandidateGeneration.ID), filepath.Join(f.dataDir, "missing-candidate")); err != nil {
		t.Fatal(err)
	}
	f.handler.(*server).recoveryReadOnly = true
	before := diagnosticDurableSnapshot(t, f)
	rr := f.request("GET", route, "", true)
	if rr.Code != 200 {
		t.Fatalf("diagnostic: %d %s", rr.Code, rr.Body.String())
	}
	var d struct {
		SchemaVersion int `json:"schema_version"`
		Checks        []struct{ ID, Status, Code string }
		NextActions   []string `json:"next_actions"`
	}
	var envelope struct {
		Diagnostic json.RawMessage `json:"diagnostic"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(envelope.Diagnostic, &d); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range d.Checks {
		if c.ID == "candidate" {
			found = true
			if c.Status != "blocked" {
				t.Fatal(c)
			}
		}
	}
	if !found || d.SchemaVersion != 1 {
		t.Fatal(rr.Body.String())
	}
	if after := diagnosticDurableSnapshot(t, f); before != after {
		t.Fatal("diagnosis changed durable state")
	}
	if len(boundary.calls) != 0 {
		t.Fatal("forbidden dependency dispatch", boundary.calls)
	}
	if rr := f.request("POST", route, `{}`, true); rr.Code != 503 {
		t.Fatal(rr.Code)
	}
	for _, kind := range []string{"origin", "host"} {
		req := httptest.NewRequest("GET", testOrigin+route, nil)
		req.AddCookie(f.cookie)
		if kind == "origin" {
			req.Header.Set("Origin", "https://foreign.invalid")
		} else {
			req.Host = "foreign.invalid"
		}
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, req)
		if rr.Code != 403 {
			t.Fatal(kind, rr.Code)
		}
	}
	if strings.Contains(rr.Body.String(), repo) {
		t.Fatal("diagnosis leaked source path")
	}
	if rr := f.request("GET", "/api/missions/absent/recovery-diagnostic", "", true); rr.Code != 404 {
		t.Fatal(rr.Code)
	}
	f.handler.(*server).recoveryReadOnly = false
	if rr := f.request("POST", route, `{}`, true); rr.Code != 404 {
		t.Fatal(rr.Code)
	}
}

func TestRecoveryDiagnosticUnreadableMissionIsSafe(t *testing.T) {
	f := newFixture(t)
	m, _, b := diagnosticHTTPMission(t, f)
	f.authenticate()
	db, err := sql.Open("sqlite3", f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`UPDATE missions SET data=json_set(data,'$.revision','PRIVATE_PLAN_ERROR') WHERE id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	before := diagnosticDurableSnapshot(t, f)
	rr := f.request("GET", "/api/missions/"+m.ID+"/recovery-diagnostic", "", true)
	if rr.Code != 500 || !strings.Contains(rr.Body.String(), "recovery_diagnostic_unavailable") || strings.Contains(rr.Body.String(), "PRIVATE_PLAN_ERROR") {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if diagnosticDurableSnapshot(t, f) != before || len(b.calls) != 0 {
		t.Fatal("unreadable mission caused mutation or dispatch")
	}
}
