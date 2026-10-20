package httpapi

import (
	"database/sql"
	"encoding/json"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Fails if implementation status is inferred from account data, omitted when
// SIWC is present, or reading the projection changes the existing denied flags.
func TestModelAcceptanceSubscriptionAdapterProjectionIsOptionalAndReadOnly(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a, err := f.store.SaveModelAccount(domain.ModelAccount{ID: "adapter", Name: "Synthetic", Provider: "openai", BaseURL: "https://api.openai.com/v1", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	srv := f.handler.(*server)
	srv.secrets = forbiddenSecrets{t}
	srv.accounts = accounts.New(f.store, forbiddenSecrets{t})
	for _, present := range []bool{false, true} {
		for _, recovery := range []bool{false, true} {
			srv.recoveryReadOnly = recovery
			srv.siwc = nil
			if present {
				srv.siwc = accounts.NewSIWCService(nil)
			}
			rr := f.request(http.MethodGet, "/api/model-accounts/"+a.ID+"/acceptance", "", true)
			if rr.Code != 200 {
				t.Fatalf("projection status=%d", rr.Code)
			}
			var payload struct {
				Acceptance struct {
					Preparation map[string]json.RawMessage `json:"openai_preparation"`
				} `json:"acceptance"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			p := payload.Acceptance.Preparation
			adapter, found := p["subscription_adapter"]
			if found != present {
				t.Fatalf("optional adapter status present=%v want=%v", found, present)
			}
			if present && string(adapter) != `{"state":"implemented_disabled","validation":"local_simulation_only"}` {
				t.Fatalf("unsafe adapter metadata=%s", adapter)
			}
			admission, admissionFound := p["production_admission"]
			if admissionFound != present || present && string(admission) != `{"state":"implemented_disabled","validation":"local_simulation_only","native_keychain":"not_checked","operator_activation":"explicit_startup"}` {
				t.Fatalf("unsafe or missing production admission preparation=%s", admission)
			}
			for _, field := range []string{"real_connection", "real_catalog", "real_inference"} {
				if string(p[field]) != `"blocked"` {
					t.Fatalf("%s no longer blocked", field)
				}
			}
			if string(p["quota"]) != `"unknown"` || string(p["remote_billing"]) != `"unverified"` {
				t.Fatal("projection claimed remote evidence")
			}
			after, err := f.store.GetModelAccount(a.ID)
			if err != nil || !reflect.DeepEqual(a, after) {
				t.Fatal("read mutated account", err)
			}
		}
	}
}

func TestModelAcceptanceHTTPAuthorizationOperationsReadFailure(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a, err := f.store.SaveModelAccount(domain.ModelAccount{ID: "oauth", Name: "Synthetic", Provider: "openai", BaseURL: "http://127.0.0.1:1234/v1", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.BeginModelAuthorization(a.ID, "login", "sign_in", "auth"); err != nil {
		t.Fatal(err)
	}
	b := domain.ModelAuthorizationBinding{ID: "auth", AccountID: a.ID, Issuer: "https://issuer.invalid", Subject: "synthetic", ClientID: "fake-client", SpendClass: "subscription_only", Scopes: []string{"resource.invoke", "chatgpt.tokens.use.direct"}}
	if _, err = f.store.CommitModelAuthorization("login", b, "2099-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`DROP TABLE model_authorization_operations`); err != nil {
		t.Fatal(err)
	}
	srv := f.handler.(*server)
	srv.secrets = forbiddenSecrets{t}
	srv.accounts = accounts.New(f.store, forbiddenSecrets{t})
	for _, recovery := range []bool{false, true} {
		srv.recoveryReadOnly = recovery
		rr := f.request(http.MethodGet, "/api/model-accounts/"+a.ID+"/acceptance", "", true)
		if rr.Code != 503 || !strings.Contains(rr.Body.String(), "model_account_storage_unavailable") || strings.Contains(rr.Body.String(), "model_authorization_operations") || strings.Contains(rr.Body.String(), `"acceptance"`) {
			t.Fatalf("operations read failure=%d %s", rr.Code, rr.Body.String())
		}
	}
}

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
