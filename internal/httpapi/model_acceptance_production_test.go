package httpapi

import (
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"net/http"
	"strings"
	"testing"
)

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
	if rr := f.request(http.MethodGet, path, "", true); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"simulation_enabled":false`) || !strings.Contains(rr.Body.String(), `"legacy_siwc_simulation_enabled":false`) {
		t.Fatalf("false eligibility: %d %s", rr.Code, rr.Body.String())
	}
	after, err := f.store.GetModelAccount(a.ID)
	if err != nil || after != a {
		t.Fatal("denied request mutated account")
	}
}
