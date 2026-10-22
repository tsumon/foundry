package httpapi

import (
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/testutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryImportCreationAndOperationRoutes(t *testing.T) {
	f := newFixture(t)
	route := "/api/repository-imports/operations"
	if rr := f.request("GET", route, "", false); rr.Code != 401 {
		t.Fatal(rr.Code)
	}
	f.authenticate()
	if rr := f.request("GET", route, "", true); rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for _, payload := range []string{`{"import_version":1}`, `{"import_version":2}`, `{"import_selection":{"inspection_id":"x","digest":"x"}}`} {
		if rr := f.request("POST", "/api/missions", payload, true); rr.Code != 400 {
			t.Fatal(rr.Code, rr.Body.String())
		}
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("before\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-qb", "main"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture"}} {
		cmd := testutil.Git(t, args...)
		cmd.Dir = repo
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(b))
		}
	}
	if err := f.store.PutProfile(domain.Profile{ID: "test", Name: "Test", Protocol: "chat_completions", BaseURL: "http://127.0.0.1", Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	inspectionReq := domain.RepositoryInspectionRequest{RepoPath: repo, Consent: true, Spec: domain.RepositoryImportSpec{Scope: []string{"file.txt"}, Acceptance: []string{"test -f file.txt"}, Image: "local"}}
	raw, _ := json.Marshal(inspectionReq)
	rr := f.request("POST", "/api/repository-imports/inspections", string(raw), true)
	var v domain.RepositoryInspection
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &v) != nil || v.Status != "ready" {
		t.Fatal(rr.Code, rr.Body.String())
	}
	request := engine.CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: v.ID, Digest: v.Digest}, CodingVersion: 1, WorkspaceMode: "generations_v1", Title: "import", Goal: "change", RepoPath: repo, Spec: domain.Spec{Scope: v.Spec.Scope, Acceptance: v.Spec.Acceptance, Image: v.Spec.Image, ProfileID: "test"}}
	raw, _ = json.Marshal(request)
	rr = f.request("POST", "/api/missions", string(raw), true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"import_version":1`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	rr = f.request("POST", "/api/missions", string(raw), true)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	request.Title = "changed"
	raw, _ = json.Marshal(request)
	rr = f.request("POST", "/api/missions", string(raw), true)
	if rr.Code != 409 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if err := os.Rename(repo, repo+"-gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Rename(repo+"-gone", repo) })
	f.handler.(*server).recoveryReadOnly = true
	rr = f.request("GET", route, "", true)
	var ops []domain.RepositoryImportOperation
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &ops) != nil || len(ops) != 1 || ops[0].Status != "committed" {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if rr = f.request("POST", "/api/missions", string(raw), true); rr.Code != 503 {
		t.Fatal(rr.Code)
	}
	req := httptest.NewRequest("GET", testOrigin+route, nil)
	req.AddCookie(f.cookie)
	req.Header.Set("Origin", "https://foreign.invalid")
	rr = httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatal(rr.Code)
	}
}

// Removing the admission route or bypassing its permission gates must fail this test.
func TestRepositoryInspectionRoutesRequireConsentAndPreserveGates(t *testing.T) {
	f := newFixture(t)
	const route = "/api/repository-imports/inspections"
	if rr := f.request("GET", route, "", false); rr.Code != 401 {
		t.Fatal(rr.Code)
	}
	f.authenticate()
	if rr := f.request("GET", route, "", true); rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatalf("history: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request("POST", route, `{"repo_path":"/must-not-be-read","consent":false,"spec":{}}`, true); rr.Code != 400 || !strings.Contains(rr.Body.String(), "consent_required") {
		t.Fatalf("consent: %d %s", rr.Code, rr.Body.String())
	}
	req := httptest.NewRequest("GET", testOrigin+route, nil)
	req.AddCookie(f.cookie)
	req.Header.Set("Origin", "https://foreign.invalid")
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatal(rr.Code)
	}
	f.handler.(*server).recoveryReadOnly = true
	if rr := f.request("POST", route, `{}`, true); rr.Code != 503 {
		t.Fatal(rr.Code)
	}
	if rr := f.request("GET", route, "", true); rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatal(rr.Code, rr.Body.String())
	}
}

func TestRepositoryInspectionBlockedHistoryIsSafeAndDoesNotRereadSource(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	const route = "/api/repository-imports/inspections"
	rr := f.request("POST", route, `{"repo_path":"/unavailable/RAW_FAILURE_MARKER","consent":true,"spec":{"scope":["file.txt"],"acceptance":["test -f file.txt"],"image":"configuration-only"}}`, true)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var v domain.RepositoryInspection
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Status != "blocked" || v.Code != domain.ImportUnavailable || v.FileCount != nil || strings.Contains(rr.Body.String(), "RAW_FAILURE_MARKER") {
		t.Fatal(rr.Body.String())
	}
	f.handler.(*server).recoveryReadOnly = true
	rr = f.request("GET", route, "", true)
	var rows []domain.RepositoryInspection
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &rows) != nil || len(rows) != 1 || rows[0].Digest != v.Digest {
		t.Fatal(rr.Code, rr.Body.String())
	}
	rr = f.request("POST", route, `{"consent":true,"unexpected":1}`, true)
	if rr.Code != 503 {
		t.Fatal(rr.Code)
	}
	f.handler.(*server).recoveryReadOnly = false
	rr = f.request("POST", route, `{"consent":true,"unexpected":1}`, true)
	if rr.Code != 400 {
		t.Fatal(rr.Code)
	}
}
