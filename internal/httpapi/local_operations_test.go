package httpapi

import (
	"foundry/internal/domain"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalOperationsReadOnlyAndMissing(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	if _, err := f.store.CreateMission(domain.Mission{ID: "ledger", State: "draft"}); err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodGet, "/api/missions/ledger/local-operations", "", true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"local_operations":[]`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	rr = f.request(http.MethodGet, "/api/missions/missing/local-operations", "", true)
	if rr.Code != 404 {
		t.Fatal(rr.Code)
	}
	rr = f.request(http.MethodPost, "/api/missions/ledger/local-operations", `{"revision":1}`, true)
	if rr.Code != 404 {
		t.Fatal(rr.Code)
	}
}

func TestDownloadRejectsOutsideConfiguredManagedRoot(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	dir := filepath.Join(t.TempDir(), "external", "delivery")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"changes.patch", "report.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("external data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.CreateMission(domain.Mission{ID: "external", State: "delivered", Delivery: &domain.Delivery{PatchPath: filepath.Join(dir, "changes.patch"), ReportPath: filepath.Join(dir, "report.md")}}); err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodGet, "/api/missions/external/patch", "", true)
	if rr.Code != 404 {
		t.Fatalf("outside root served: %d %s", rr.Code, rr.Body.String())
	}
}

func TestCreateUnsupportedWorkspaceModeIsValidationError(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	rr := f.request(http.MethodPost, "/api/missions", `{"title":"unknown mode","goal":"read only","workspace_mode":"future_v2"}`, true)
	if rr.Code != 400 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	missions, err := f.store.ListMissions()
	if err != nil || len(missions) != 0 {
		t.Fatal("unsupported mode created mission", err)
	}
}

func TestBrowserCannotDeclareVerificationSource(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	for _, body := range []string{
		`{"title":"spoof","source_kind":"isolated_execution","source_version":1}`,
		`{"title":"spoof","spec":{"source_kind":"isolated_execution"}}`,
		`{"title":"spoof","evidence":[{"source_version":1,"source_kind":"isolated_execution"}]}`,
	} {
		rr := f.request(http.MethodPost, "/api/missions", body, true)
		if rr.Code != 400 || !strings.Contains(rr.Body.String(), `"code":"invalid_json"`) {
			t.Fatalf("browser source not rejected by schema: %d %s", rr.Code, rr.Body.String())
		}
	}
	missions, err := f.store.ListMissions()
	if err != nil || len(missions) != 0 {
		t.Fatal("spoof created mission", err)
	}
}
