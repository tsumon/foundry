package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func seedHTTPAuthorization(t *testing.T, f *fixture) (domain.ModelAuthorization, string) {
	return seedHTTPAuthorizationHistory(t, f, true)
}

func seedHTTPAuthorizationHistory(t *testing.T, f *fixture, recordRemoteRevocation bool) (domain.ModelAuthorization, string) {
	t.Helper()
	account, err := f.store.SaveModelAccount(domain.ModelAccount{
		ID: "oauth-account", Name: "Synthetic subscription", Provider: "openai",
		BaseURL: "https://api.openai.com/v1", AuthMethod: "openai_siwc",
		BillingMode: "subscription", Enabled: true,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	binding := domain.ModelAuthorizationBinding{
		ID: "authorization-one", AccountID: account.ID,
		Issuer: "https://issuer.invalid", Subject: "subject-one", ClientID: "client-one",
		SpendClass: "subscription_only",
		Scopes:     []string{"openid", "resource.invoke", "chatgpt.tokens.use.direct"},
	}
	operation, err := f.store.BeginModelAuthorization(account.ID, "sign-in-one", "sign_in", binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := f.store.CommitModelAuthorization(operation.ID, binding, "2099-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.RevokeModelAuthorization(binding.ID); err != nil {
		t.Fatal(err)
	}
	if recordRemoteRevocation {
		if _, err = f.store.RecordSIWCRemoteRevocation(binding.ID, "unconfirmed"); err != nil {
			t.Fatal(err)
		}
	}
	return authorization, operation.SecretRef
}

func TestDiagnosticModelAuthorizationReadDoesNotCreateOptionalSIWCSchema(t *testing.T) {
	f := newFixture(t)
	seedHTTPAuthorizationHistory(t, f, false)
	srv := f.handler.(*server)
	f.handler = New(f.store, srv.engine, srv.client, f.secrets, Options{
		Address: "127.0.0.1:4317", Accounts: srv.accounts, ModelRouter: srv.modelRouter,
		RecoveryReadOnly: true,
	})
	f.authenticate()

	inspection, err := sql.Open("sqlite3", f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	var beforeVersion, afterVersion, beforeObjects, afterObjects int
	if err = inspection.QueryRow(`PRAGMA schema_version`).Scan(&beforeVersion); err != nil {
		t.Fatal(err)
	}
	if err = inspection.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'siwc_%'`).Scan(&beforeObjects); err != nil {
		t.Fatal(err)
	}
	if beforeObjects != 0 {
		t.Fatalf("test setup already has optional SIWC schema objects: %d", beforeObjects)
	}
	rr := f.request(http.MethodGet, "/api/model-authorizations", "", true)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"id":"authorization-one"`) || !strings.Contains(rr.Body.String(), `"remote_revocations":[]`) {
		t.Fatalf("diagnostic read=%d %s", rr.Code, rr.Body.String())
	}
	if err = inspection.QueryRow(`PRAGMA schema_version`).Scan(&afterVersion); err != nil {
		t.Fatal(err)
	}
	if err = inspection.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'siwc_%'`).Scan(&afterObjects); err != nil {
		t.Fatal(err)
	}
	if afterVersion != beforeVersion || afterObjects != beforeObjects {
		t.Fatalf("diagnostic GET mutated schema: version %d->%d objects %d->%d", beforeVersion, afterVersion, beforeObjects, afterObjects)
	}
}

func TestModelAuthorizationReadIsExplicitSafeMetadataOnly(t *testing.T) {
	f := newFixture(t)
	authorization, privateRef := seedHTTPAuthorization(t, f)
	f.authenticate()

	if rr := f.request(http.MethodGet, "/api/model-authorizations", "", false); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated read=%d %s", rr.Code, rr.Body.String())
	}
	rr := f.request(http.MethodGet, "/api/model-authorizations", "", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("read=%d %s", rr.Code, rr.Body.String())
	}
	for _, forbidden := range []string{privateRef, "secret_ref", "cleanup_pending", "write_claimed", "access_token", "refresh_token", "id_token", "handoff_url", "operation_id"} {
		if strings.Contains(rr.Body.String(), forbidden) {
			t.Fatalf("private lifecycle field %q leaked: %s", forbidden, rr.Body.String())
		}
	}
	var body struct {
		Authorizations    []map[string]any `json:"authorizations"`
		RemoteRevocations []map[string]any `json:"remote_revocations"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Authorizations) != 1 || len(body.RemoteRevocations) != 1 {
		t.Fatalf("body=%s", rr.Body.String())
	}
	assertExactJSONFields(t, body.Authorizations[0], []string{"id", "account_id", "issuer", "subject", "client_id", "scopes", "spend_class", "state", "generation", "expires_at"})
	assertExactJSONFields(t, body.RemoteRevocations[0], []string{"authorization_id", "outcome", "updated_at"})
	if body.Authorizations[0]["id"] != authorization.Binding.ID || body.Authorizations[0]["state"] != "revoked" || body.Authorizations[0]["generation"] != float64(1) || body.RemoteRevocations[0]["outcome"] != "unconfirmed" {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestModelAuthorizationReadUsesStrictQueryAndStaysReadOnlyInRecovery(t *testing.T) {
	f := newFixture(t)
	seedHTTPAuthorization(t, f)
	f.authenticate()
	if rr := f.request(http.MethodGet, "/api/model-authorizations?extra=1", "", true); rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown query=%d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request(http.MethodPost, "/api/model-authorizations/start", `{}`, true); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"code":"invalid_model_authorization"`) {
		t.Fatalf("empty start request=%d %s", rr.Code, rr.Body.String())
	}

	srv := f.handler.(*server)
	f.handler = New(f.store, srv.engine, srv.client, f.secrets, Options{
		Address: "127.0.0.1:4317", Accounts: srv.accounts, ModelRouter: srv.modelRouter,
		RecoveryReadOnly: true,
	})
	f.cookie = nil
	f.authenticate()
	rr := f.request(http.MethodGet, "/api/model-authorizations", "", true)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"state":"revoked"`) {
		t.Fatalf("recovery read=%d %s", rr.Code, rr.Body.String())
	}
}

func TestModelAuthorizationReadReturnsNonNullEmptyArrays(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	rr := f.request(http.MethodGet, "/api/model-authorizations", "", true)
	if rr.Code != http.StatusOK || rr.Body.String() != `{"authorizations":[],"remote_revocations":[]}`+"\n" {
		t.Fatalf("empty contract=%d %s", rr.Code, rr.Body.String())
	}
}

func assertExactJSONFields(t *testing.T, value map[string]any, want []string) {
	t.Helper()
	if len(value) != len(want) {
		t.Fatalf("fields=%v want=%v", value, want)
	}
	for _, name := range want {
		if _, ok := value[name]; !ok {
			t.Fatalf("missing field %q in %v", name, value)
		}
	}
}
