package httpapi

import (
	"foundry/internal/acceptance"
	"foundry/internal/desktophost"
	"foundry/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopPreparationDefaultClosed(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	for _, method := range []string{"GET", "POST"} {
		rr := f.request(method, "/api/model-accounts/production/desktop-preparation", `{}`, true)
		if rr.Code != 403 {
			t.Fatalf("default %s: %d", method, rr.Code)
		}
	}
}
func TestDesktopPreparationHTTPBoundaries(t *testing.T) {
	f, a, r, _ := operatorHTTPFixture(t)
	s := f.handler.(*server)
	calls := 0
	s.codingWorkbench = true
	s.desktopAccounts = acceptance.NewDesktopCoordinator(f.store, acceptance.DesktopOptions{SIWC: s.siwc, Resolver: s.accounts, Probe: func() error { calls++; return nil }})
	defer s.desktopAccounts.Close()
	path := "/api/model-accounts/" + a.ID + "/desktop-preparation"
	body := `{"phase":"connection","route_id":"` + r.ID + `","expected_account_revision":1}`
	if rr := f.request("GET", path, "", true); rr.Code != 200 || calls != 0 {
		t.Fatal("metadata effects")
	}
	if rr := f.request("POST", path, body, false); rr.Code != 401 {
		t.Fatal("unauthenticated prepare")
	}
	request := httptest.NewRequest("POST", testOrigin+path, strings.NewReader(body))
	request.AddCookie(f.cookie)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://untrusted.example")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, request)
	if rr.Code != 403 || calls != 0 {
		t.Fatal("cross origin prepare")
	}
	guarded := desktophost.Protect("synthetic-capability", s)
	request = httptest.NewRequest(http.MethodGet, testOrigin+path, nil)
	request.AddCookie(f.cookie)
	rr = httptest.NewRecorder()
	guarded.ServeHTTP(rr, request)
	if rr.Code != 403 || calls != 0 {
		t.Fatalf("capability bypass %d", rr.Code)
	}
	if rr := f.request("POST", path, strings.TrimSuffix(body, "}")+`,"readiness":true}`, true); rr.Code != 400 || calls != 0 {
		t.Fatal("JSON injected authority")
	}
	if rr := f.request("POST", path, body, true); rr.Code != 200 || calls != 1 {
		t.Fatalf("prepare: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request("GET", "/api/model-accounts/production/acceptance-operations", "", true); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"production_connection_enabled":true`) {
		t.Fatalf("not integrated: %s", rr.Body.String())
	}
}
func TestDesktopSubscriptionDoesNotBlockDistinctAPIKeyAccount(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	s := f.handler.(*server)
	s.desktopAccounts = acceptance.NewDesktopCoordinator(f.store, acceptance.DesktopOptions{})
	defer s.desktopAccounts.Close()
	_, e := f.store.SaveModelAccount(domain.ModelAccount{ID: "api-account", Name: "Synthetic API", Provider: "openai", AuthMethod: "api_key", BillingMode: "metered_api", BaseURL: "https://api.openai.com/v1", Enabled: true}, 0)
	if e != nil {
		t.Fatal(e)
	}
	rr := f.request("POST", "/api/model-accounts/api-account/credentials", `{"expected_revision":1,"operation_id":"synthetic-key-op","key":"synthetic-memory-only-key"}`, true)
	if rr.Code != 200 {
		t.Fatalf("unrelated API account blocked: %d %s", rr.Code, rr.Body.String())
	}
}

func TestDesktopInferencePreparationHTTPKeepsIndependentApproval(t *testing.T) {
	f, a, r, _ := operatorHTTPFixture(t)
	s := f.handler.(*server)
	s.codingWorkbench = true
	s.desktopAccounts = acceptance.NewDesktopCoordinator(f.store, acceptance.DesktopOptions{Resolver: s.accounts, Probe: func() error { t.Fatal("inference performed Keychain probe"); return nil }})
	defer s.desktopAccounts.Close()
	path := "/api/model-accounts/" + a.ID + "/desktop-preparation"
	body := `{"phase":"inference","route_id":"` + r.ID + `","expected_account_revision":1}`
	if rr := f.request("POST", path, body, false); rr.Code != 401 {
		t.Fatal("unauthenticated inference preparation")
	}
	if rr := f.request("POST", path, body, true); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"phase":"inference"`) {
		t.Fatalf("missing desktop inference phase: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request("GET", "/api/model-accounts/"+a.ID+"/acceptance-operations", "", true); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"production_inference_enabled":false`) {
		t.Fatal("missing prerequisite treated as approval")
	}
	if rr := f.request("POST", path, strings.Replace(body, `"inference"`, `"arbitrary"`, 1), true); rr.Code != 400 {
		t.Fatal("arbitrary phase enabled")
	}
}
