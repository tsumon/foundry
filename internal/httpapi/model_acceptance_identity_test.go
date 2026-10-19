package httpapi

import (
	"context"
	"encoding/json"
	"foundry/internal/acceptance"
	"foundry/internal/domain"
	"foundry/internal/model"
	"strings"
	"testing"
)

func TestIdentityAcceptanceHTTPPinnedViewAndStrictMaterial(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a, e := f.store.SaveModelAccount(domain.ModelAccount{ID: "identity", Name: "identity", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "http://127.0.0.1:12345/v1"}, 0)
	if e != nil {
		t.Fatal(e)
	}
	route, e := f.store.CreateModelRoute(domain.ModelRoute{ID: "identity-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	svc := acceptance.NewSimulation(f.store, acceptance.Options{Routes: []domain.ModelRoute{route}, IdentityOnlyRouteIDs: []string{route.ID}, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(context.Context, domain.ModelAcceptanceMaterial) error { calls++; return nil }})
	if e = svc.Recover(); e != nil {
		t.Fatal(e)
	}
	f.handler.(*server).acceptance = svc
	path := "/api/model-accounts/identity/acceptance-operations"
	rr := f.request("GET", path, "", true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"connection_purpose":"identity_only"`) || !strings.Contains(rr.Body.String(), `"spend_class":"subscription_only"`) {
		t.Fatalf("identity route view=%d %s", rr.Code, rr.Body.String())
	}
	for _, test := range []struct {
		extra  string
		status int
	}{
		{``, 403}, {`,"connection_purpose":"subscription_only"`, 403},
		{`,"connection_purpose":"identity_only","requested_scopes":["openid"]`, 400},
		{`,"connection_purpose":"identity_only","outcome":"approve"`, 400},
		{`,"connection_purpose":"identity_only","token_origin":"http://127.0.0.1:1"`, 400},
		{`,"connection_purpose":"identity_only","connection_purpose":"subscription_only"`, 400},
	} {
		rr = f.request("POST", path, `{"phase":"connection","route_id":"identity-route","expected_account_revision":1`+test.extra+`}`, true)
		if rr.Code != test.status {
			t.Fatalf("unsafe identity proposal status=%d want=%d %s", rr.Code, test.status, rr.Body.String())
		}
	}
	rr = f.request("POST", path, `{"phase":"connection","route_id":"identity-route","expected_account_revision":1,"connection_purpose":"identity_only"}`, true)
	if rr.Code != 200 {
		t.Fatalf("identity proposal=%d %s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Operation domain.ModelAcceptanceOperation `json:"operation"`
	}
	if e = json.Unmarshal(rr.Body.Bytes(), &envelope); e != nil {
		t.Fatal(e)
	}
	o := envelope.Operation
	if o.Material.ConnectionPurpose != "identity_only" || strings.Join(o.Material.RequestedScopes, " ") != "openid profile email" || o.MaterialDigest != domain.AcceptanceDigest(o.Material) {
		t.Fatal("server did not derive bound identity material")
	}
	if rr = f.request("POST", "/api/model-authorizations/start", `{"account_id":"identity","expected_account_revision":1}`, true); rr.Code != 403 {
		t.Fatal("legacy identity authorization bypass")
	}
	f.handler.(*server).recoveryReadOnly = true
	if rr = f.request("POST", path, `{"phase":"connection","route_id":"identity-route","expected_account_revision":1,"connection_purpose":"identity_only"}`, true); rr.Code == 200 {
		t.Fatal("read-only identity mutation")
	}
	if calls != 0 {
		t.Fatal("identity review caused side effect")
	}
}
