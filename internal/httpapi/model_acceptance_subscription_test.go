package httpapi

import (
	"context"
	"foundry/internal/acceptance"
	"foundry/internal/domain"
	"foundry/internal/model"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionAcceptanceHTTPProjectsPurposeAndRejectsClientPermissionFields(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a, err := f.store.SaveModelAccount(domain.ModelAccount{ID: "subscription", Name: "subscription", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "http://127.0.0.1:12345/v1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.store.CreateModelRoute(domain.ModelRoute{ID: "subscription-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutProfile(domain.Profile{ID: "subscription-profile", AccountID: a.ID, RouteID: r.ID, BaseURL: a.BaseURL, Model: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.BeginModelAuthorization(a.ID, "subscription-auth-op", "sign_in", "subscription-auth"); err != nil {
		t.Fatal(err)
	}
	binding := domain.ModelAuthorizationBinding{ID: "subscription-auth", AccountID: a.ID, Issuer: "http://127.0.0.1:12345", Subject: "synthetic", ClientID: "synthetic", SpendClass: "subscription_only", Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}}
	if _, err = f.store.CommitModelAuthorization("subscription-auth-op", binding, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc := acceptance.NewSimulation(f.store, acceptance.Options{Routes: []domain.ModelRoute{r}, SubscriptionConnectionRouteIDs: []string{r.ID}, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(context.Context, domain.ModelAcceptanceMaterial) error { calls++; return nil }})
	if err = svc.Recover(); err != nil {
		t.Fatal(err)
	}
	f.handler.(*server).acceptance = svc
	path := "/api/model-accounts/subscription/acceptance-operations"
	if rr := f.request("GET", path, "", true); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"connection_purpose":"subscription_only"`) {
		t.Fatalf("view=%d %s", rr.Code, rr.Body.String())
	}
	for _, extra := range []string{`,"requested_scopes":["openid"]`, `,"requested_resource":"https://api.openai.com/v1"`} {
		if rr := f.request("POST", path, `{"phase":"connection","route_id":"subscription-route","expected_account_revision":1,"connection_purpose":"subscription_only"`+extra+`}`, true); rr.Code != 400 {
			t.Fatalf("client permission accepted: %d %s", rr.Code, rr.Body.String())
		}
	}
	for _, bypass := range []struct{ path, body string }{
		{"/api/model-authorizations/start", `{"account_id":"subscription","expected_account_revision":1}`},
		{"/api/model-authorizations/subscription-auth/refresh", `{"expected_account_revision":1}`},
		{"/api/profiles/subscription-profile/probe", `{}`},
	} {
		rr := f.request("POST", bypass.path, bypass.body, true)
		if rr.Code != 403 || !strings.Contains(rr.Body.String(), "acceptance_gate_required") {
			t.Fatalf("subscription bypass %s=%d %s", bypass.path, rr.Code, rr.Body.String())
		}
	}
	if calls != 0 {
		t.Fatal("subscription bypass dispatched connector")
	}
	if rr := f.request("POST", path, `{"phase":"connection","route_id":"subscription-route","expected_account_revision":1,"connection_purpose":"subscription_only"}`, true); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"requested_resource":"https://api.openai.com/v1"`) {
		t.Fatalf("proposal=%d %s", rr.Code, rr.Body.String())
	}
}
