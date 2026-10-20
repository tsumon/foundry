package httpapi

import (
	"encoding/json"
	"fmt"
	"foundry/internal/acceptance"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func operatorHTTPFixture(t *testing.T) (*fixture, domain.ModelAccount, domain.ModelRoute, *atomic.Bool) {
	t.Helper()
	f := newFixture(t)
	f.authenticate()
	a, err := f.store.SaveModelAccount(domain.ModelAccount{ID: "production", Name: "production", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "https://api.openai.com/v1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.store.CreateModelRoute(domain.ModelRoute{ID: "production-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	s := f.handler.(*server)
	s.accounts = accounts.New(f.store, forbiddenSecrets{t})
	s.siwc = accounts.NewSIWCService(s.accounts)
	t.Cleanup(func() { _ = s.siwc.Close() })
	ready := &atomic.Bool{}
	ready.Store(true)
	s.acceptance = acceptance.NewProductionConnection(f.store, acceptance.ProductionConnectionOptions{Route: r, SIWC: s.siwc, CredentialReady: ready.Load})
	t.Cleanup(func() { _ = s.acceptance.Close() })
	if err := s.acceptance.Recover(); err != nil {
		t.Fatal(err)
	}
	return f, a, r, ready
}

// Catches history which hides admitted production routes, conflates them with
// simulation, or loses read-only provenance when credential readiness expires.
func TestOperatorProductionHistoryRetainsProvenanceAfterExpiry(t *testing.T) {
	f, a, r, ready := operatorHTTPFixture(t)
	s := f.handler.(*server)
	o, err := s.acceptance.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision})
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.acceptance.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "reject")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/model-accounts/production/acceptance-operations"
	generation := ""
	for _, fresh := range []bool{true, false} {
		ready.Store(fresh)
		rr := f.request(http.MethodGet, path, "", true)
		var history struct {
			Enabled    *bool                             `json:"production_connection_enabled"`
			Simulation bool                              `json:"simulation_enabled"`
			Admission  *domain.ProductionAdmission       `json:"production_admission"`
			Routes     []acceptanceRouteView             `json:"routes"`
			Operations []domain.ModelAcceptanceOperation `json:"operations"`
		}
		if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &history) != nil {
			t.Fatalf("history: %d %s", rr.Code, rr.Body.String())
		}
		if history.Enabled == nil || *history.Enabled != fresh || history.Simulation || history.Admission == nil || history.Admission.Profile != "openai_siwc_subscription_v1" || len(history.Admission.Generation) != 32 {
			t.Fatalf("production history authority: %s", rr.Body.String())
		}
		if generation != "" && history.Admission.Generation != generation {
			t.Fatal("expiry changed generation")
		}
		generation = history.Admission.Generation
		if len(history.Routes) != 1 || history.Routes[0].ID != r.ID || history.Routes[0].Environment != "live" || history.Routes[0].Adapter != "openai_siwc_responses" || history.Routes[0].ConnectionPurpose != "subscription_only" || history.Routes[0].ConnectionMode != "browser_handoff" {
			t.Fatalf("owned route: %+v", history.Routes)
		}
		if len(history.Operations) != 1 || history.Operations[0].ID != o.ID || history.Operations[0].Source != "production_connection" || history.Operations[0].LiveAccess != "connection_only" || history.Operations[0].State != "rejected" {
			t.Fatalf("lost receipt: %s", rr.Body.String())
		}
	}
	// Default restart still reads receipts, without reviving admission or links.
	if err := s.acceptance.Close(); err != nil {
		t.Fatal(err)
	}
	s.acceptance = acceptance.New(f.store)
	if err := s.acceptance.Recover(); err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodGet, path, "", true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"production_connection_enabled":false`) || !strings.Contains(rr.Body.String(), o.ID) {
		t.Fatalf("default restart lost history: %s", rr.Body.String())
	}
	var history map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if _, ok := history["production_admission"]; ok {
		t.Fatal("default restart revived admission")
	}
	rr = f.request(http.MethodGet, path+"/"+o.ID, "", true)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), "connection_session") || strings.Contains(rr.Body.String(), "handoff_url") {
		t.Fatalf("history restored handoff: %s", rr.Body.String())
	}
}

func TestOperatorProductionHTTPKeepsMutationAndLegacyBoundaries(t *testing.T) {
	f, a, r, ready := operatorHTTPFixture(t)
	s := f.handler.(*server)
	path := "/api/model-accounts/production/acceptance-operations"
	body := `{"phase":"connection","connection_purpose":"subscription_only","route_id":"production-route","expected_account_revision":1}`
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if rr := f.request(method, path, body, false); rr.Code != 401 {
			t.Fatalf("unauthenticated admission: %d %s", rr.Code, rr.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodPost, testOrigin+path, strings.NewReader(body))
	req.Host = "127.0.0.1:4317"
	req.AddCookie(f.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://attacker.invalid")
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("CSRF admission: %d", rr.Code)
	}
	for _, phase := range []string{"malformed", "catalog", "inference"} {
		rr := f.request(http.MethodPost, path, strings.Replace(body, `"phase":"connection"`, `"phase":"`+phase+`"`, 1), true)
		want := 403
		if phase == "malformed" {
			want = 400
		}
		if rr.Code != want {
			t.Fatalf("phase %s: %d %s", phase, rr.Code, rr.Body.String())
		}
	}
	for _, suffix := range []string{"", ",\"production_connection_enabled\":true"} {
		s.recoveryReadOnly = true
		rr = f.request(http.MethodPost, path, strings.TrimSuffix(body, "}")+suffix+"}", true)
		if rr.Code != 503 {
			t.Fatalf("read-only activation: %d %s", rr.Code, rr.Body.String())
		}
	}
	rr = f.request(http.MethodGet, path, "", true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"production_connection_enabled":false`) {
		t.Fatalf("read-only projection: %s", rr.Body.String())
	}
	s.recoveryReadOnly = false
	rr = f.request(http.MethodPost, path, body, true)
	var response struct {
		Operation domain.ModelAcceptanceOperation `json:"operation"`
	}
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &response) != nil {
		t.Fatalf("proposal: %d %s", rr.Code, rr.Body.String())
	}
	o := response.Operation
	decision := fmt.Sprintf(`{"expected_revision":%d,"material_digest":%q,"decision":"approve"}`, o.Revision, o.MaterialDigest)
	rr = f.request(http.MethodPost, path+"/"+o.ID+"/decision", decision, true)
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &response) != nil || response.Operation.Attempts != 0 || strings.Contains(rr.Body.String(), "connection_session") {
		t.Fatalf("approval caused dispatch: %d %s", rr.Code, rr.Body.String())
	}
	o = response.Operation
	ready.Store(false)
	for _, request := range []struct{ path, body string }{
		{path, body},
		{path + "/" + o.ID + "/decision", decision},
		{path + "/" + o.ID + "/execute", fmt.Sprintf(`{"expected_revision":%d,"material_digest":%q}`, o.Revision, o.MaterialDigest)},
	} {
		rr := f.request(http.MethodPost, request.path, request.body, true)
		if rr.Code != 403 || !strings.Contains(rr.Body.String(), "acceptance_simulation_disabled") {
			t.Fatalf("stale mutation: %d %s", rr.Code, rr.Body.String())
		}
	}
	ready.Store(true)
	if rr := f.request(http.MethodPost, "/api/model-authorizations/start", `{"account_id":"production","expected_account_revision":1}`, true); rr.Code != 403 || !strings.Contains(rr.Body.String(), "acceptance_gate_required") || strings.Contains(rr.Body.String(), "synthetic") {
		t.Fatalf("legacy login bypass: %d %s", rr.Code, rr.Body.String())
	}
	// An existing synthetic identifier is enough to test the refresh bypass;
	// no token or native store is created or read.
	if _, err := f.store.BeginModelAuthorization(a.ID, "operator-old-auth", "sign_in", "operator-auth"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CommitModelAuthorization("operator-old-auth", domain.ModelAuthorizationBinding{ID: "operator-auth", AccountID: a.ID, Issuer: "https://auth.openai.com", Subject: "synthetic", ClientID: "oaiapp_synthetic", SpendClass: "subscription_only", Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}}, time.Now().Add(time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if rr := f.request(http.MethodPost, "/api/model-authorizations/operator-auth/refresh", `{"expected_account_revision":1}`, true); rr.Code != 403 || !strings.Contains(rr.Body.String(), "acceptance_gate_required") {
		t.Fatalf("refresh bypass: %d %s", rr.Code, rr.Body.String())
	}
	// A forged constructor route cannot emit enabling metadata even after recovery.
	r.AccountID = "forged"
	if err := s.acceptance.Close(); err != nil {
		t.Fatal(err)
	}
	s.acceptance = acceptance.NewProductionConnection(f.store, acceptance.ProductionConnectionOptions{Route: r, SIWC: s.siwc, CredentialReady: ready.Load})
	if err := s.acceptance.Recover(); err != nil {
		t.Fatal(err)
	}
	rr = f.request(http.MethodGet, path, "", true)
	var invalid map[string]json.RawMessage
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &invalid) != nil || string(invalid["production_connection_enabled"]) != "false" || string(invalid["routes"]) != "[]" {
		t.Fatalf("invalid policy projection: %s", rr.Body.String())
	}
	if _, ok := invalid["production_admission"]; ok {
		t.Fatal("invalid policy emitted admission")
	}
}

func TestProductionAdmissionHTTPHasNoActivationAndNoSecretEffects(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a, err := f.store.SaveModelAccount(domain.ModelAccount{ID: "production", Name: "production", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "https://api.openai.com/v1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CreateModelRoute(domain.ModelRoute{ID: "production-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"}); err != nil {
		t.Fatal(err)
	}
	s := f.handler.(*server)
	s.accounts = accounts.New(f.store, forbiddenSecrets{t})
	s.secrets = forbiddenSecrets{t}
	s.siwc = accounts.NewSIWCService(s.accounts)
	path := "/api/model-accounts/production/acceptance-operations"
	body := `{"phase":"connection","connection_purpose":"subscription_only","route_id":"production-route","expected_account_revision":1}`
	for _, field := range []string{`"production_admission":{"state":"enabled"}`, `"generation":"forged"`, `"credential_ready":true`, `"native_keychain":"checked"`, `"enabled":true`} {
		rr := f.request(http.MethodPost, path, strings.TrimSuffix(body, "}")+","+field+"}", true)
		if rr.Code != 400 {
			t.Fatalf("forged policy: %d %s", rr.Code, rr.Body.String())
		}
	}
	if rr := f.request(http.MethodPost, path, body, true); rr.Code != 403 {
		t.Fatalf("default admission: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request(http.MethodPost, "/api/model-authorizations/start", `{"account_id":"production","expected_account_revision":1}`, true); rr.Code != 409 || !strings.Contains(rr.Body.String(), "live_provider_access_pending_confirmation") {
		t.Fatalf("direct login: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request(http.MethodGet, path, "", true); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"simulation_enabled":false`) || !strings.Contains(rr.Body.String(), `"legacy_siwc_simulation_enabled":false`) || !strings.Contains(rr.Body.String(), `"production_connection_enabled":false`) {
		t.Fatalf("false eligibility: %d %s", rr.Code, rr.Body.String())
	}
	after, err := f.store.GetModelAccount(a.ID)
	if err != nil || after != a {
		t.Fatal("denied request mutated account")
	}
}
