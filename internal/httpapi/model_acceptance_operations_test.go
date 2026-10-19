package httpapi

import (
	"context"
	"foundry/internal/acceptance"
	"foundry/internal/domain"
	"foundry/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
	for _, action := range []string{"decision", "execute"} {
		for _, body := range []string{`{"expected_revision":1,"expected_revision":2,"material_digest":"x"}`, `{"expected_revision":1,"material_digest":"x","unknown":true}`} {
			rr := f.request("POST", path+"/"+action, body, true)
			if rr.Code != 400 {
				t.Fatalf("strict %s %d", action, rr.Code)
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
