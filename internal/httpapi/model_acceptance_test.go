package httpapi

import (
	"encoding/json"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestModelAcceptanceHTTPReadOnlySafeBoundary(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a, err := f.store.SaveModelAccount(domain.ModelAccount{ID: "acceptance", Name: "Synthetic", Provider: "kimi", BaseURL: "http://127.0.0.1:1234/v1", AuthMethod: "api_key", BillingMode: "subscription", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "fake", SecretRef: "foundry-model-account-fake"}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.FinishModelCredential("fake", "available"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CreateModelRoute(domain.ModelRoute{ID: "route", AccountID: a.ID, Adapter: "kimi_code_chat", Protocol: "chat_completions", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "interactive_package", ExtraUsage: "off"}); err != nil {
		t.Fatal(err)
	}
	srv := f.handler.(*server)
	srv.secrets = forbiddenSecrets{t}
	srv.accounts = accounts.New(f.store, forbiddenSecrets{t})
	path := "/api/model-accounts/" + a.ID + "/acceptance"
	for _, recovery := range []bool{false, true} {
		srv.recoveryReadOnly = recovery
		rr := f.request("GET", path, "", true)
		if rr.Code != 200 {
			t.Fatalf("read=%d %s", rr.Code, rr.Body.String())
		}
		var payload map[string]json.RawMessage
		if err = json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload) != 1 {
			t.Fatal("unexpected wrapper", payload)
		}
		var report map[string]json.RawMessage
		if err = json.Unmarshal(payload["acceptance"], &report); err != nil {
			t.Fatal(err)
		}
		if len(report) != 7 {
			t.Fatal("unexpected fields", report)
		}
		for _, key := range []string{"account_id", "account_revision", "scope", "live_access", "quota", "checks", "routes"} {
			if _, ok := report[key]; !ok {
				t.Fatal("missing", key)
			}
		}
		if string(report["live_access"]) != `"blocked"` || string(report["quota"]) != `"unknown"` || string(report["account_revision"]) != "3" {
			t.Fatal(report)
		}
		var routes []map[string]json.RawMessage
		if err = json.Unmarshal(report["routes"], &routes); err != nil {
			t.Fatal(err)
		}
		if len(routes) != 1 || len(routes[0]) != 7 || string(routes[0]["remote_extra_usage"]) != `"unverified"` {
			t.Fatal(routes)
		}
		for _, unsafe := range []string{"secret_ref", "foundry-model-account", "127.0.0.1", "base_url", "operation_id"} {
			if strings.Contains(rr.Body.String(), unsafe) {
				t.Fatal("unsafe response", rr.Body.String())
			}
		}
	}
	srv.recoveryReadOnly = false
	for _, tc := range []struct {
		method, path string
		auth         bool
		want         int
	}{{"GET", path, false, 401}, {"GET", path + "?x=1", true, 404}, {"GET", path + "?", true, 404}, {"GET", path + "/extra", true, 404}, {"GET", "/api/model-accounts/missing/acceptance", true, 404}, {"POST", path, true, 404}, {"HEAD", path, true, 404}} {
		rr := f.request(tc.method, tc.path, "{}", tc.auth)
		if rr.Code != tc.want {
			t.Fatalf("%s %s = %d %s", tc.method, tc.path, rr.Code, rr.Body.String())
		}
	}
	for _, header := range []string{"Host", "Origin"} {
		req := httptest.NewRequest(http.MethodGet, testOrigin+path, nil)
		req.AddCookie(f.cookie)
		req.Host = "127.0.0.1:4317"
		if header == "Host" {
			req.Host = "attacker.invalid"
		} else {
			req.Header.Set("Origin", "https://attacker.invalid")
		}
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, req)
		if rr.Code != 403 {
			t.Fatalf("%s=%d", header, rr.Code)
		}
	}
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	rr := f.request("GET", path, "", true)
	if rr.Code != 503 || !strings.Contains(rr.Body.String(), "model_account_storage_unavailable") || strings.Contains(rr.Body.String(), "database") {
		t.Fatalf("storage failure=%d %s", rr.Code, rr.Body.String())
	}
}

func TestModelAcceptanceHTTPRejectsUnsafePersistedIdentifiers(t *testing.T) {
	for _, field := range []string{"account", "route"} {
		t.Run(field, func(t *testing.T) {
			f := newFixture(t)
			f.authenticate()
			accountID, routeID := "acceptance", "route"
			unsafeID := "https://private.invalid/secret"
			if field == "account" {
				unsafeID = "account.private.invalid"
				accountID = unsafeID
			} else {
				routeID = unsafeID
			}
			a, err := f.store.SaveModelAccount(domain.ModelAccount{ID: accountID, Name: "Synthetic", Provider: "kimi", BaseURL: "http://127.0.0.1:1234/v1", AuthMethod: "api_key", BillingMode: "subscription", Enabled: true}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "fake", SecretRef: "foundry-model-account-fake"}, 1); err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.FinishModelCredential("fake", "available"); err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.CreateModelRoute(domain.ModelRoute{ID: routeID, AccountID: a.ID, Adapter: "kimi_code_chat", Protocol: "chat_completions", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "interactive_package", ExtraUsage: "off"}); err != nil {
				t.Fatal(err)
			}
			before, err := f.store.GetModelAccount(a.ID)
			if err != nil {
				t.Fatal(err)
			}
			srv := f.handler.(*server)
			srv.secrets = forbiddenSecrets{t}
			srv.accounts = accounts.New(f.store, forbiddenSecrets{t})
			for _, recovery := range []bool{false, true} {
				srv.recoveryReadOnly = recovery
				rr := f.request(http.MethodGet, "/api/model-accounts/"+a.ID+"/acceptance", "", true)
				if rr.Code != 503 || !strings.Contains(rr.Body.String(), "model_account_storage_unavailable") {
					t.Errorf("unsafe %s diagnostic=%d %s", field, rr.Code, rr.Body.String())
				}
				if strings.Contains(rr.Body.String(), unsafeID) || strings.Contains(rr.Body.String(), `"acceptance"`) {
					t.Error("unsafe identifier or partial report escaped error boundary")
				}
			}
			after, err := f.store.GetModelAccount(a.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("diagnostic changed account", after, err)
			}
		})
	}
}
