package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/sandbox"
	"foundry/internal/store"
)

type catalogFailSecrets struct{}

func (catalogFailSecrets) Set(string, string) error { panic("catalog invoked secrets.Set") }
func (catalogFailSecrets) Get(string) (string, error) {
	panic("catalog invoked secrets.Get")
}
func (catalogFailSecrets) Delete(string) error { panic("catalog invoked secrets.Delete") }

type catalogFailGateway struct{}

func (catalogFailGateway) Complete(context.Context, model.Request) (model.Result, error) {
	panic("catalog invoked model gateway")
}

type catalogFailRunner struct{}

func (catalogFailRunner) Resolve(context.Context, string) (string, error) {
	panic("catalog invoked runner.Resolve")
}
func (catalogFailRunner) Run(context.Context, string, string, string, string) (domain.CommandResult, error) {
	panic("catalog invoked runner.Run")
}
func (catalogFailRunner) Reconcile(context.Context) error {
	panic("catalog invoked runner.Reconcile")
}

var _ model.Gateway = catalogFailGateway{}
var _ sandbox.Runner = catalogFailRunner{}

type catalogFailingBody struct{ err error }

func (b catalogFailingBody) Read([]byte) (int, error) { return 0, b.err }
func (catalogFailingBody) Close() error               { return nil }

type catalogFixture struct {
	t       *testing.T
	path    string
	store   *store.Store
	handler http.Handler
	cookie  *http.Cookie
}

func newCatalogFixture(t *testing.T) *catalogFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "foundry.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	secrets := catalogFailSecrets{}
	client := model.NewClient(secrets)
	e := engine.New(s, catalogFailGateway{}, engine.Options{DataDir: t.TempDir(), Runner: catalogFailRunner{}})
	t.Cleanup(func() { _ = e.Close() })
	return &catalogFixture{
		t:       t,
		path:    path,
		store:   s,
		handler: New(s, e, client, secrets, Options{Address: "127.0.0.1:4317"}),
	}
}

func (f *catalogFixture) request(method, target, body string, authenticated bool) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.requestWith(method, target, body, authenticated, "127.0.0.1:4317", testOrigin, "application/json")
}

func (f *catalogFixture) requestWith(method, target, body string, authenticated bool, host, origin, contentType string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, testOrigin+target, strings.NewReader(body))
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if authenticated && f.cookie != nil {
		req.AddCookie(f.cookie)
	}
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

func (f *catalogFixture) authenticate() {
	f.t.Helper()
	rr := f.request(http.MethodPost, "/api/session", `{}`, false)
	if rr.Code != http.StatusOK {
		f.t.Fatalf("bootstrap status=%d body=%s", rr.Code, rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		f.t.Fatalf("cookies=%d", len(cookies))
	}
	f.cookie = cookies[0]
}

func catalogErrorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Error.Code
}

func TestCatalogRequiresSessionAndRejectsForgedState(t *testing.T) {
	f := newCatalogFixture(t)
	rr := f.request(http.MethodGet, "/api/catalog", "", false)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", rr.Code, rr.Body.String())
	}
	f.authenticate()
	rr = f.request(http.MethodGet, "/api/catalog", "", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := `{"expected_revision":0,"kind":"project.create","input":{"name":"product","description":"","deployed":true}}`
	rr = f.request(http.MethodPost, "/api/catalog/commands", body, true)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("forged state status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCatalogUsesExactHostOriginAndJSONGuards(t *testing.T) {
	f := newCatalogFixture(t)
	f.authenticate()
	tests := []struct {
		name, method, host, origin, contentType string
	}{
		{name: "wrong host read", method: http.MethodGet, host: "localhost:4317", origin: testOrigin, contentType: "application/json"},
		{name: "wrong origin read", method: http.MethodGet, host: "127.0.0.1:4317", origin: "https://attacker.example", contentType: "application/json"},
		{name: "wrong origin mutation", method: http.MethodPost, host: "127.0.0.1:4317", origin: "https://attacker.example", contentType: "application/json"},
		{name: "non json mutation", method: http.MethodPost, host: "127.0.0.1:4317", origin: testOrigin, contentType: "text/plain"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := f.requestWith(tc.method, "/api/catalog/commands", `{}`, true, tc.host, tc.origin, tc.contentType)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestCatalogRejectsMalformedCommandEnvelopes(t *testing.T) {
	f := newCatalogFixture(t)
	f.authenticate()
	validInput := `"kind":"project.create","input":{"name":"product","description":""}`
	tests := []struct {
		name, body string
	}{
		{name: "missing revision", body: `{` + validInput + `}`},
		{name: "negative revision", body: `{"expected_revision":-1,` + validInput + `}`},
		{name: "floating revision", body: `{"expected_revision":0.5,` + validInput + `}`},
		{name: "duplicate field", body: `{"expected_revision":0,"expected_revision":1,` + validInput + `}`},
		{name: "two documents", body: `{"expected_revision":0,` + validInput + `}{}`},
		{name: "oversized", body: `{"expected_revision":0,"kind":"project.create","input":{"name":"` + strings.Repeat("x", (1<<20)+1) + `","description":""}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := f.request(http.MethodPost, "/api/catalog/commands", tc.body, true)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if code := catalogErrorCode(t, rr); code != "invalid_catalog" {
				t.Fatalf("error code=%q body=%s", code, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), strings.Repeat("x", 32)) {
				t.Fatalf("request content leaked: %s", rr.Body.String())
			}
		})
	}
}

func TestCatalogBodyReadFailureIsSanitizedAndDoesNotMutate(t *testing.T) {
	f := newCatalogFixture(t)
	f.authenticate()
	before, err := f.store.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	const privateDetail = "fixture transport credential detail"
	req := httptest.NewRequest(
		http.MethodPost,
		testOrigin+"/api/catalog/commands",
		catalogFailingBody{err: errors.New(privateDetail)},
	)
	req.Host = "127.0.0.1:4317"
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(f.cookie)
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if code := catalogErrorCode(t, rr); code != "internal" {
		t.Fatalf("error code=%q body=%s", code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), privateDetail) {
		t.Fatalf("read error detail leaked: %s", rr.Body.String())
	}
	after, err := f.store.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("catalog mutated after body read failure: before=%+v after=%+v", before, after)
	}
}

func TestCatalogMapsCommandsEventsAndDomainErrors(t *testing.T) {
	f := newCatalogFixture(t)
	f.authenticate()

	for _, body := range []string{
		`{"expected_revision":0,"kind":"deployment.execute","input":{}}`,
		`{"expected_revision":0,"kind":"project.update","input":{"id":"missing","name":"product","description":""}}`,
	} {
		rr := f.request(http.MethodPost, "/api/catalog/commands", body, true)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("not found mapping status=%d body=%s", rr.Code, rr.Body.String())
		}
		if code := catalogErrorCode(t, rr); code != "not_found" {
			t.Fatalf("not found error code=%q body=%s", code, rr.Body.String())
		}
	}

	create := `{"expected_revision":0,"kind":"project.create","input":{"name":"product","description":"metadata only"}}`
	rr := f.request(http.MethodPost, "/api/catalog/commands", create, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created struct {
		Catalog domain.CatalogSnapshot `json:"catalog"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Catalog.Revision != 1 || len(created.Catalog.Projects) != 1 || created.Catalog.Projects[0].Name != "product" {
		t.Fatalf("catalog=%+v", created.Catalog)
	}

	rr = f.request(http.MethodPost, "/api/catalog/commands", create, true)
	if rr.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", rr.Code, rr.Body.String())
	}
	if code := catalogErrorCode(t, rr); code != "conflict" {
		t.Fatalf("conflict error code=%q body=%s", code, rr.Body.String())
	}

	rr = f.request(http.MethodGet, "/api/catalog/events", "", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("default events status=%d body=%s", rr.Code, rr.Body.String())
	}
	var events struct {
		Events []domain.CatalogEvent `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &events); err != nil {
		t.Fatal(err)
	}
	if len(events.Events) != 1 || events.Events[0].CatalogRevision != 1 || events.Events[0].Kind != "project.create" {
		t.Fatalf("events=%+v", events.Events)
	}
}

func TestCatalogRejectsIllegalEventQueries(t *testing.T) {
	f := newCatalogFixture(t)
	f.authenticate()
	for _, target := range []string{
		"/api/catalog/events?after=-1",
		"/api/catalog/events?after=1.5",
		"/api/catalog/events?limit=0",
		"/api/catalog/events?limit=101",
		"/api/catalog/events?limit=nope",
		"/api/catalog/events?after=0&after=1",
		"/api/catalog/events?unknown=1",
	} {
		rr := f.request(http.MethodGet, target, "", true)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s status=%d body=%s", target, rr.Code, rr.Body.String())
		}
	}
}

func TestCatalogCorruptionTakesPriorityAndIsSanitized(t *testing.T) {
	f := newCatalogFixture(t)
	f.authenticate()
	db, err := sql.Open("sqlite3", f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`DELETE FROM catalog`); err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodGet, "/api/catalog", "", true)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if code := catalogErrorCode(t, rr); code != "catalog_corrupt" {
		t.Fatalf("error code=%q body=%s", code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "catalog row") || strings.Contains(rr.Body.String(), "SQL") {
		t.Fatalf("storage detail leaked: %s", rr.Body.String())
	}
}

func TestCatalogHealthAndUnsupportedSideEffectRoutes(t *testing.T) {
	f := newCatalogFixture(t)
	f.authenticate()
	rr := f.request(http.MethodGet, "/api/health", "", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", rr.Code, rr.Body.String())
	}
	var health struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if !health.Capabilities["catalog"] {
		t.Fatalf("catalog capability missing: %+v", health.Capabilities)
	}
	for _, capability := range []string{"package_build", "deployment", "database_migration"} {
		if value, ok := health.Capabilities[capability]; !ok || value {
			t.Errorf("capability %q=%t present=%t", capability, value, ok)
		}
	}
	for _, target := range []string{
		"/api/deploy", "/api/deployment", "/api/migrate", "/api/restore",
		"/api/catalog/deploy", "/api/catalog/migrate", "/api/catalog/restore", "/api/catalog/execute",
	} {
		rr = f.request(http.MethodPost, target, `{}`, true)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s status=%d body=%s", target, rr.Code, rr.Body.String())
		}
	}
}

func TestCatalogDoesNotChangeExistingDeliverRoute(t *testing.T) {
	f := newCatalogFixture(t)
	f.authenticate()
	rr := f.request(http.MethodPost, "/api/missions/missing/deliver", `{"revision":0}`, true)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("deliver status=%d body=%s", rr.Code, rr.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "not_found" {
		t.Fatalf("deliver response=%s", rr.Body.String())
	}
}
