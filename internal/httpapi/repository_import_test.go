package httpapi

import (
	"encoding/json"
	"foundry/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
