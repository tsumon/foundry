package httpapi

import (
	"context"
	"encoding/json"
	"foundry/internal/acceptance"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAcceptanceHistoryDeclaresOnlyTrustedLegacySIWCCapability(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer issuer.Close()
	f := newFixture(t)
	account, err := f.store.SaveModelAccount(domain.ModelAccount{ID: "legacy", Name: "legacy", Provider: "openai", BaseURL: issuer.URL + "/v1", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.authenticate()
	path := "/api/model-accounts/" + account.ID + "/acceptance-operations"
	if rr := f.request(http.MethodGet, path, "", true); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"legacy_siwc_simulation_enabled":false`) {
		t.Fatalf("production capability=%d %s", rr.Code, rr.Body.String())
	}
	siwc, err := accounts.NewSIWCSimulation(f.handler.(*server).accounts, accounts.SIWCOptions{AuthorizationOrigin: issuer.URL, TokenOrigin: issuer.URL, OIDCOrigin: issuer.URL, JWKSOrigin: issuer.URL, RevocationOrigin: issuer.URL, Issuer: issuer.URL, FlowTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer siwc.Close()
	installSIWC(t, f, siwc)
	if rr := f.request(http.MethodGet, path, "", true); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"simulation_enabled":false`) || !strings.Contains(rr.Body.String(), `"legacy_siwc_simulation_enabled":true`) {
		t.Fatalf("legacy capability=%d %s", rr.Code, rr.Body.String())
	}
	s := f.handler.(*server)
	s.recoveryReadOnly = true
	if rr := f.request(http.MethodGet, path, "", true); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"legacy_siwc_simulation_enabled":false`) {
		t.Fatalf("recovery capability=%d %s", rr.Code, rr.Body.String())
	}
	s.recoveryReadOnly = false
	route, err := f.store.CreateModelRoute(domain.ModelRoute{ID: "identity-route", AccountID: account.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: account.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	s.acceptance = acceptance.NewSimulation(f.store, acceptance.Options{Routes: []domain.ModelRoute{route}})
	if rr := f.request(http.MethodGet, path, "", true); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"legacy_siwc_simulation_enabled":false`) {
		t.Fatalf("owned capability=%d %s", rr.Code, rr.Body.String())
	}
	s.acceptance = acceptance.NewSimulation(f.store, acceptance.Options{Routes: []domain.ModelRoute{route}, IdentityOnlyRouteIDs: []string{route.ID}})
	if rr := f.request(http.MethodGet, path, "", true); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"legacy_siwc_simulation_enabled":false`) {
		t.Fatalf("identity-owned capability=%d %s", rr.Code, rr.Body.String())
	}
	s.acceptance = acceptance.NewSimulation(f.store, acceptance.Options{Routes: []domain.ModelRoute{route}, IdentityOnlyRouteIDs: []string{"missing-route"}})
	if rr := f.request(http.MethodGet, path, "", true); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"legacy_siwc_simulation_enabled":false`) {
		t.Fatalf("invalid owned policy capability=%d %s", rr.Code, rr.Body.String())
	}
}

func TestAcceptanceOperationsProductionHTTP(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a, e := f.store.SaveModelAccount(domain.ModelAccount{ID: "gate", Name: "gate", Provider: "kimi", BaseURL: "http://127.0.0.1:12345/v1", AuthMethod: "api_key", BillingMode: "subscription", Enabled: true}, 0)
	if e != nil {
		t.Fatal(e)
	}
	path := "/api/model-accounts/" + a.ID + "/acceptance-operations"
	rr := f.request("GET", path, "", true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"simulation_enabled":false`) {
		t.Fatalf("list %d %s", rr.Code, rr.Body.String())
	}
	for _, body := range []string{`{"phase":"connection","route_id":"forged","expected_account_revision":1}`, `{"phase":"connection","phase":"catalog","route_id":"forged","expected_account_revision":1}`, `{"phase":"connection","route_id":"forged","expected_account_revision":1,"live":true}`} {
		rr = f.request("POST", path, body, true)
		if rr.Code != 400 && rr.Code != 403 {
			t.Fatalf("mutation %d %s", rr.Code, rr.Body.String())
		}
	}
	if rr = f.request("GET", path+"?x=1", "", true); rr.Code == 200 {
		t.Fatal("query accepted")
	}
	f.handler.(*server).recoveryReadOnly = true
	if rr = f.request(http.MethodPost, path, `{}`, true); rr.Code == 200 {
		t.Fatal("recovery mutation")
	}
	if rr = f.request("GET", path, "", true); rr.Code != 200 {
		t.Fatalf("recovery GET %d", rr.Code)
	}
}

func TestAcceptanceOperationsBlockDirectBypasses(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	routes := []domain.ModelRoute{}
	for _, provider := range []string{"openai", "kimi"} {
		a := domain.ModelAccount{ID: provider, Name: provider, Provider: provider, BaseURL: "http://127.0.0.1:12345/v1", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true}
		r := domain.ModelRoute{ID: provider + "-route", AccountID: provider, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"}
		if provider == "kimi" {
			a.AuthMethod = "api_key"
			r.Adapter = "kimi_code_chat"
			r.Protocol = "chat_completions"
			r.SpendClass = "interactive_package"
		}
		if _, e := f.store.SaveModelAccount(a, 0); e != nil {
			t.Fatal(e)
		}
		r, e := f.store.CreateModelRoute(r)
		if e != nil {
			t.Fatal(e)
		}
		routes = append(routes, r)
		if e = f.store.PutProfile(domain.Profile{ID: provider + "-profile", AccountID: provider, RouteID: r.ID, BaseURL: a.BaseURL, Model: "fake"}); e != nil {
			t.Fatal(e)
		}
	}
	svc := acceptance.NewSimulation(f.store, acceptance.Options{Routes: routes, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(context.Context, domain.ModelAcceptanceMaterial) error { return nil }})
	if e := svc.Recover(); e != nil {
		t.Fatal(e)
	}
	f.handler.(*server).acceptance = svc
	for _, q := range []struct{ path, body string }{{"/api/model-authorizations/start", `{"account_id":"openai","expected_account_revision":1}`}, {"/api/model-accounts/kimi/credentials", `{"expected_revision":1,"operation_id":"bypass","key":"must-not-be-imported"}`}, {"/api/profiles/kimi-profile/probe", `{}`}} {
		rr := f.request("POST", q.path, q.body, true)
		if rr.Code != 403 || !strings.Contains(rr.Body.String(), "acceptance_gate_required") {
			t.Fatalf("bypass %s %d %s", q.path, rr.Code, rr.Body.String())
		}
	}
}

func TestAcceptanceOperationsSecurityAndReadDoesNotRecover(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a, e := f.store.SaveModelAccount(domain.ModelAccount{ID: "gate", Name: "gate", Provider: "kimi", BaseURL: "http://127.0.0.1:12345/v1", AuthMethod: "api_key", BillingMode: "subscription", Enabled: true}, 0)
	if e != nil {
		t.Fatal(e)
	}
	route, e := f.store.CreateModelRoute(domain.ModelRoute{ID: "route", AccountID: a.ID, Adapter: "kimi_code_chat", Protocol: "chat_completions", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "interactive_package", ExtraUsage: "off"})
	if e != nil {
		t.Fatal(e)
	}
	o, e := f.store.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", RouteID: route.ID, ExpectedAccountRevision: 1}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	o, e = f.store.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	o, e = f.store.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	path := "/api/model-accounts/gate/acceptance-operations/" + o.ID
	for _, recovery := range []bool{false, true} {
		f.handler.(*server).recoveryReadOnly = recovery
		rr := f.request("GET", path, "", true)
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"state":"running"`) {
			t.Fatalf("GET reconciled %d %s", rr.Code, rr.Body.String())
		}
	}
	f.handler.(*server).recoveryReadOnly = false
	if rr := f.request("GET", path, "", false); rr.Code != 401 {
		t.Fatalf("unauth %d", rr.Code)
	}
	for _, kind := range []string{"host", "origin", "content_type"} {
		req := httptest.NewRequest("POST", testOrigin+path+"/execute", strings.NewReader(`{}`))
		req.AddCookie(f.cookie)
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("Content-Type", "application/json")
		switch kind {
		case "host":
			req.Host = "evil.invalid"
		case "origin":
			req.Header.Set("Origin", "http://evil.invalid")
		case "content_type":
			req.Header.Set("Content-Type", "text/plain")
		}
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, req)
		if rr.Code >= 200 && rr.Code < 300 {
			t.Fatalf("%s accepted", kind)
		}
	}
	for _, action := range []string{"decision", "execute", "cancel"} {
		for _, body := range []string{`{"expected_revision":1,"expected_revision":2,"material_digest":"x"}`, `{"expected_revision":1,"material_digest":"x","unknown":true}`} {
			rr := f.request("POST", path+"/"+action, body, true)
			if rr.Code != 400 {
				t.Fatalf("strict %s %d", action, rr.Code)
			}
		}
	}
	// Otherwise valid requests must reject output-only evidence at every write
	// boundary; an invalid digest must not accidentally make this test pass.
	for _, action := range []string{"proposal", "decision", "execute", "cancel"} {
		for _, field := range []string{"identity_check", "connection_diagnostic"} {
			body := map[string]any{"expected_revision": o.Revision, "material_digest": o.MaterialDigest}
			target := path + "/" + action
			if action == "decision" {
				body["decision"] = "approve"
			}
			if action == "proposal" {
				body = map[string]any{"phase": "connection", "route_id": route.ID, "expected_account_revision": a.Revision}
				target = "/api/model-accounts/gate/acceptance-operations"
			}
			if field == "identity_check" {
				body[field] = "audience"
			} else {
				body[field] = map[string]any{"version": 1, "stage": "identity_validation", "code": "auth_invalid", "identity_check": "audience", "credential_commit": "not_committed", "cleanup": "completed"}
			}
			raw, _ := json.Marshal(body)
			if rr := f.request("POST", target, string(raw), true); rr.Code != 400 {
				t.Fatalf("output-only %s on %s: %d %s", field, action, rr.Code, rr.Body.String())
			}
		}
	}
	for _, tail := range []string{"?x=1", "?", "/%20", "/../"} {
		rr := f.request("GET", path+tail, "", true)
		if rr.Code == 200 {
			t.Fatalf("unsafe route %s", tail)
		}
	}
	got, e := f.store.GetModelAcceptance(a.ID, o.ID)
	if e != nil || got.Revision != o.Revision || got.State != "running" {
		t.Fatal("read or rejected request mutated receipt")
	}
}
