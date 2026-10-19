package httpapi

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
)

type cleanupFailSecrets struct {
	delegate *memorySecrets
	deletes  atomic.Int64
}

func (s *cleanupFailSecrets) Set(id, value string) error { return s.delegate.Set(id, value) }
func (s *cleanupFailSecrets) Get(id string) (string, error) {
	return s.delegate.Get(id)
}
func (s *cleanupFailSecrets) Delete(string) error {
	s.deletes.Add(1)
	return errors.New("private cleanup failure")
}

func installSIWC(t *testing.T, f *fixture, siwc *accounts.SIWCService) {
	t.Helper()
	srv := f.handler.(*server)
	f.handler = New(f.store, srv.engine, srv.client, f.secrets, Options{
		Address: "127.0.0.1:4317", Accounts: srv.accounts, ModelRouter: srv.modelRouter, SIWC: siwc,
	})
	f.cookie = nil
	f.authenticate()
}

func createSIWCAccount(t *testing.T, f *fixture, baseURL string) domain.ModelAccount {
	t.Helper()
	a, err := f.handler.(*server).accounts.Save(domain.ModelAccount{
		Name: "Synthetic subscription", Provider: "openai", BaseURL: baseURL,
		AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func seedAvailableHTTPAuthorization(t *testing.T, f *fixture, account domain.ModelAccount, issuer string) domain.ModelAuthorization {
	t.Helper()
	binding := domain.ModelAuthorizationBinding{
		ID: "authorization-live", AccountID: account.ID, Issuer: issuer,
		Subject: "subject-live", ClientID: "client-live", SpendClass: "subscription_only",
		Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"},
	}
	operation, err := f.store.BeginModelAuthorization(account.ID, "sign-in-live", "sign_in", binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := f.store.CommitModelAuthorization(operation.ID, binding, "2099-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return authorization
}

func TestModelAuthorizationMutationsUseSharedPermissionAndStrictJSONGates(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	account := createSIWCAccount(t, f, "https://api.openai.com/v1")
	base := `{"account_id":"` + account.ID + `","expected_account_revision":1}`

	if rr := f.request(http.MethodPost, "/api/model-authorizations/start", base, false); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d %s", rr.Code, rr.Body.String())
	}
	for _, bad := range []string{
		strings.TrimSuffix(base, "}") + `,"unknown":true}`,
		strings.Replace(base, `"account_id":"`, `"account_id":"`+account.ID+`","account_id":"`, 1),
		strings.Replace(base, `"expected_account_revision":1`, `"expected_account_revision":0`, 1),
	} {
		rr := f.request(http.MethodPost, "/api/model-authorizations/start", bad, true)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("strict body=%d %s input=%s", rr.Code, rr.Body.String(), bad)
		}
	}
	if rr := f.request(http.MethodPost, "/api/model-authorizations/start?extra=1", base, true); rr.Code != http.StatusBadRequest {
		t.Fatalf("query=%d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request(http.MethodGet, "/api/model-authorizations/start", "", true); rr.Code != http.StatusNotFound {
		t.Fatalf("wrong method=%d %s", rr.Code, rr.Body.String())
	}
	for _, target := range []string{
		"/api/model-authorizations/authorization-one/refresh",
		"/api/model-authorizations/authorization-one/revoke",
	} {
		if rr := f.request(http.MethodPost, target, `{"expected_account_revision":1,"unknown":true}`, true); rr.Code != http.StatusBadRequest {
			t.Fatalf("strict lifecycle body %s=%d %s", target, rr.Code, rr.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodPost, testOrigin+"/api/model-authorizations/start", strings.NewReader(base))
	request.Host = "outside.invalid"
	request.Header.Set("Origin", testOrigin)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(f.cookie)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("wrong host=%d %s", response.Code, response.Body.String())
	}
	for _, tc := range []struct {
		origin, contentType string
	}{
		{"https://outside.invalid", "application/json"},
		{testOrigin, "text/plain"},
	} {
		request = httptest.NewRequest(http.MethodPost, testOrigin+"/api/model-authorizations/start", strings.NewReader(base))
		request.Host = "127.0.0.1:4317"
		request.Header.Set("Origin", tc.origin)
		request.Header.Set("Content-Type", tc.contentType)
		request.AddCookie(f.cookie)
		response = httptest.NewRecorder()
		f.handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("origin=%q content-type=%q status=%d %s", tc.origin, tc.contentType, response.Code, response.Body.String())
		}
	}

	srv := f.handler.(*server)
	f.handler = New(f.store, srv.engine, srv.client, f.secrets, Options{
		Address: "127.0.0.1:4317", Accounts: srv.accounts, ModelRouter: srv.modelRouter,
		SIWC: srv.siwc, RecoveryReadOnly: true,
	})
	f.cookie = nil
	f.authenticate()
	if rr := f.request(http.MethodPost, "/api/model-authorizations/start", base, true); rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), `"code":"recovery_read_only"`) {
		t.Fatalf("recovery mutation=%d %s", rr.Code, rr.Body.String())
	}
}

func TestAuthorizationRefreshNotReadyIsFiniteAndNeverReachesIssuer(t *testing.T) {
	var requests atomic.Int64
	issuer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer issuer.Close()
	f := newFixture(t)
	account := createSIWCAccount(t, f, issuer.URL+"/v1")
	service := f.handler.(*server).accounts
	binding := domain.ModelAuthorizationBinding{
		ID: "authorization-early", AccountID: account.ID, Issuer: issuer.URL,
		Subject: "subject-early", ClientID: "client-early", SpendClass: "subscription_only",
		Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"},
	}
	operation, err := service.BeginModelAuthorization(context.Background(), account.ID, "sign-in-early", "sign_in", binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CommitModelAuthorization(context.Background(), operation.ID, binding, accounts.ModelTokenBundle{
		AccessToken: "FAKE-EARLY-ACCESS", RefreshToken: "FAKE-EARLY-REFRESH", IDToken: "FAKE-EARLY-ID",
		ExpiresAt:         time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		EarliestRefreshAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Resource: account.BaseURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	siwc, err := accounts.NewSIWCSimulation(service, accounts.SIWCOptions{
		AuthorizationOrigin: issuer.URL, TokenOrigin: issuer.URL, OIDCOrigin: issuer.URL,
		JWKSOrigin: issuer.URL, RevocationOrigin: issuer.URL, Issuer: issuer.URL, FlowTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer siwc.Close()
	installSIWC(t, f, siwc)

	rr := f.request(http.MethodPost, "/api/model-authorizations/"+binding.ID+"/refresh", `{"expected_account_revision":1}`, true)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"refresh_not_ready"`) || requests.Load() != 0 {
		t.Fatalf("early refresh=%d requests=%d %s", rr.Code, requests.Load(), rr.Body.String())
	}
	current, err := f.store.GetModelAuthorization(binding.ID)
	if err != nil || current.State != "available" || current.Generation != 1 {
		t.Fatalf("early refresh changed state: %+v %v", current, err)
	}
}

func TestModelAuthorizationErrorsAreFiniteAndUnknownProviderCodesCollapse(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid", accounts.ErrInvalid, 400, "invalid_model_authorization"},
		{"not found", store.ErrNotFound, 404, "not_found"},
		{"conflict", store.ErrConflict, 409, "conflict"},
		{"in progress", accounts.ErrInProgress, 409, "model_authorization_in_progress"},
		{"unavailable", accounts.ErrUnavailable, 503, "model_authorization_unavailable"},
		{"live pending", &model.ProviderError{Code: "live_provider_access_pending_confirmation"}, 409, "live_provider_access_pending_confirmation"},
		{"refresh early", &model.ProviderError{Code: "refresh_not_ready"}, 409, "refresh_not_ready"},
		{"auth invalid", &model.ProviderError{Code: "auth_invalid"}, 401, "auth_invalid"},
		{"entitlement", &model.ProviderError{Code: "entitlement_denied"}, 403, "entitlement_denied"},
		{"quota", &model.ProviderError{Code: "quota_exhausted"}, 429, "quota_exhausted"},
		{"rate", &model.ProviderError{Code: "rate_limited"}, 429, "rate_limited"},
		{"request", &model.ProviderError{Code: "request_rejected"}, 400, "request_rejected"},
		{"server", &model.ProviderError{Code: "server_error"}, 502, "server_error"},
		{"transport", &model.ProviderError{Code: "transport_unknown"}, 502, "transport_unknown"},
		{"incomplete", &model.ProviderError{Code: "response_incomplete"}, 502, "response_incomplete"},
		{"unknown provider", &model.ProviderError{Code: "raw-provider-secret"}, 503, "model_authorization_unavailable"},
		{"unknown error", errors.New("private backend text"), 503, "model_authorization_unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyModelAuthorizationError(tc.err)
			if got.status != tc.status || got.code != tc.code || strings.Contains(got.message, "private") || strings.Contains(got.message, "raw-provider") {
				t.Fatalf("mapped=%+v", got)
			}
		})
	}
	revocation, valid := authorizationRevocationView(accounts.SIWCRevokeResult{
		AuthorizationID: "authorization-one", LocalState: "revoked", RemoteState: "unconfirmed",
	})
	if !valid {
		t.Fatal("safe partial revocation was rejected")
	}
	recorder := httptest.NewRecorder()
	writeModelAuthorizationError(recorder, &model.ProviderError{Code: "raw-provider-secret"}, &revocation)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"revocation"`) || !strings.Contains(recorder.Body.String(), `"updated_at":""`) || strings.Contains(recorder.Body.String(), "raw-provider-secret") {
		t.Fatalf("partial error contract=%d %s", recorder.Code, recorder.Body.String())
	}
	normalized := authorizationPartialRevocationView("authorization-one", accounts.SIWCRevokeResult{
		AuthorizationID: "authorization-one", LocalState: "revoked",
	})
	if normalized == nil || normalized.RemoteState != "unconfirmed" || normalized.UpdatedAt != "" {
		t.Fatalf("missing receipt was not safely normalized: %+v", normalized)
	}
	for _, invalid := range []accounts.SIWCRevokeResult{
		{},
		{AuthorizationID: "another", LocalState: "revoked"},
		{AuthorizationID: "authorization-one", LocalState: "available"},
		{AuthorizationID: "authorization-one", LocalState: "revoked", RemoteState: "raw-provider-state"},
	} {
		if got := authorizationPartialRevocationView("authorization-one", invalid); got != nil {
			t.Fatalf("invalid partial result normalized: input=%+v got=%+v", invalid, got)
		}
	}
}

func TestProductionAuthorizationDenialsPrecedeSecretsAndRevokePreservesSafeReceipt(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	account := createSIWCAccount(t, f, "https://api.openai.com/v1")
	authorization := seedAvailableHTTPAuthorization(t, f, account, "https://auth.openai.com")

	startBody := `{"account_id":"` + account.ID + `","expected_account_revision":1}`
	if rr := f.request(http.MethodPost, "/api/model-authorizations/start", startBody, true); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"live_provider_access_pending_confirmation"`) {
		t.Fatalf("production start=%d %s", rr.Code, rr.Body.String())
	}
	refreshPath := "/api/model-authorizations/" + authorization.Binding.ID + "/refresh"
	if rr := f.request(http.MethodPost, refreshPath, `{"expected_account_revision":1}`, true); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"live_provider_access_pending_confirmation"`) {
		t.Fatalf("production refresh=%d %s", rr.Code, rr.Body.String())
	}
	current, err := f.store.GetModelAuthorization(authorization.Binding.ID)
	if err != nil || current.State != "available" || current.Generation != authorization.Generation {
		t.Fatalf("refresh changed state: %+v %v", current, err)
	}
	revokePath := "/api/model-authorizations/" + authorization.Binding.ID + "/revoke"
	rr := f.request(http.MethodPost, revokePath, `{"expected_account_revision":1}`, true)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"live_provider_access_pending_confirmation"`) || !strings.Contains(rr.Body.String(), `"local_state":"revoked"`) || !strings.Contains(rr.Body.String(), `"remote_state":"unconfirmed"`) {
		t.Fatalf("production revoke=%d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "secret") || len(f.secrets.values) != 0 {
		t.Fatalf("production denial touched or exposed secrets: %s values=%v", rr.Body.String(), f.secrets.values)
	}
}

func TestProductionRevokeReceiptWriteFailurePreservesKnownLocalDenial(t *testing.T) {
	var providerRequests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		providerRequests.Add(1)
	}))
	defer provider.Close()
	f := newFixture(t)
	service := f.handler.(*server).accounts
	account := createSIWCAccount(t, f, provider.URL+"/v1")
	binding := domain.ModelAuthorizationBinding{
		ID: "authorization-receipt-failure", AccountID: account.ID, Issuer: "https://auth.openai.com",
		Subject: "subject-receipt-failure", ClientID: "client-receipt-failure", SpendClass: "subscription_only",
		Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"},
	}
	operation, err := service.BeginModelAuthorization(context.Background(), account.ID, "sign-in-receipt-failure", "sign_in", binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := service.CommitModelAuthorization(context.Background(), operation.ID, binding, accounts.ModelTokenBundle{
		AccessToken: "FAKE-RECEIPT-FAILURE-ACCESS", RefreshToken: "FAKE-RECEIPT-FAILURE-REFRESH",
		IDToken: "FAKE-RECEIPT-FAILURE-ID", ExpiresAt: "2099-01-01T00:00:00Z", Resource: account.BaseURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.secrets.Get(authorization.SecretRef); err != nil {
		t.Fatal("test authorization secret was not committed", err)
	}
	if _, err = f.store.RecordSIWCRemoteRevocation("schema-initializer", "unconfirmed"); err != nil {
		t.Fatal(err)
	}
	inspection, err := sql.Open("sqlite3", f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	if _, err = inspection.Exec(`CREATE TRIGGER fail_target_siwc_receipt BEFORE INSERT ON siwc_remote_revocations
WHEN NEW.authorization_id='authorization-receipt-failure' BEGIN SELECT RAISE(ABORT,'synthetic private receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	f.authenticate()
	rr := f.request(http.MethodPost, "/api/model-authorizations/"+binding.ID+"/revoke", `{"expected_account_revision":1}`, true)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), `"code":"model_authorization_unavailable"`) || !strings.Contains(rr.Body.String(), `"authorization_id":"`+binding.ID+`"`) || !strings.Contains(rr.Body.String(), `"local_state":"revoked"`) || !strings.Contains(rr.Body.String(), `"remote_state":"unconfirmed"`) || !strings.Contains(rr.Body.String(), `"updated_at":""`) || strings.Contains(rr.Body.String(), "synthetic private") {
		t.Fatalf("receipt failure=%d %s", rr.Code, rr.Body.String())
	}
	current, readErr := f.store.GetModelAuthorization(binding.ID)
	if readErr != nil || current.State != "revoked" {
		t.Fatalf("local denial not durable: %+v %v", current, readErr)
	}
	if _, secretErr := f.secrets.Get(authorization.SecretRef); secretErr == nil {
		t.Fatal("revoked authorization secret was not cleaned up")
	}
	if providerRequests.Load() != 0 {
		t.Fatalf("production receipt failure reached provider %d times", providerRequests.Load())
	}
	if _, receiptErr := f.store.GetSIWCRemoteRevocation(binding.ID); !errors.Is(receiptErr, store.ErrNotFound) {
		t.Fatalf("failed receipt unexpectedly persisted: %v", receiptErr)
	}
}

func TestProductionRevokeCleanupFailureReturnsKnownLocalDenial(t *testing.T) {
	var providerRequests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		providerRequests.Add(1)
	}))
	defer provider.Close()
	f := newFixture(t)
	account := createSIWCAccount(t, f, provider.URL+"/v1")
	authorization := seedAvailableHTTPAuthorization(t, f, account, "https://auth.openai.com")
	if err := f.secrets.Set(authorization.SecretRef, "PRIVATE-CLEANUP-TOKEN"); err != nil {
		t.Fatal(err)
	}
	failingSecrets := &cleanupFailSecrets{delegate: f.secrets}
	srv := f.handler.(*server)
	lifecycle := accounts.New(f.store, failingSecrets)
	f.handler = New(f.store, srv.engine, srv.client, f.secrets, Options{
		Address: "127.0.0.1:4317", Accounts: lifecycle, ModelRouter: srv.modelRouter,
		SIWC: accounts.NewSIWCService(lifecycle),
	})
	f.cookie = nil
	f.authenticate()

	rr := f.request(http.MethodPost, "/api/model-authorizations/"+authorization.Binding.ID+"/revoke", `{"expected_account_revision":1}`, true)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), `"code":"model_authorization_unavailable"`) || !strings.Contains(rr.Body.String(), `"authorization_id":"`+authorization.Binding.ID+`"`) || !strings.Contains(rr.Body.String(), `"local_state":"revoked"`) || !strings.Contains(rr.Body.String(), `"remote_state":"unconfirmed"`) || !strings.Contains(rr.Body.String(), `"updated_at":""`) {
		t.Fatalf("cleanup failure=%d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "private") || strings.Contains(rr.Body.String(), "PRIVATE-CLEANUP-TOKEN") {
		t.Fatalf("cleanup failure exposed private data: %s", rr.Body.String())
	}
	stored, err := f.store.GetModelAuthorization(authorization.Binding.ID)
	if err != nil || stored.State != "revoked" {
		t.Fatalf("local denial not durable: %+v %v", stored, err)
	}
	pending, err := f.store.PendingModelAuthorizationCleanup()
	if err != nil || len(pending) != 1 || pending[0].AuthorizationID != authorization.Binding.ID {
		t.Fatalf("cleanup intent not retained: %+v %v", pending, err)
	}
	if failingSecrets.deletes.Load() != 1 || providerRequests.Load() != 0 {
		t.Fatalf("unexpected side effects: deletes=%d provider=%d", failingSecrets.deletes.Load(), providerRequests.Load())
	}
}

func TestAuthorizationFlowOutlivesStartRequestAndReportsCanceledAndExpiredStates(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not completed in this test", http.StatusBadRequest)
	}))
	defer issuer.Close()
	f := newFixture(t)
	account := createSIWCAccount(t, f, issuer.URL+"/v1")
	options := accounts.SIWCOptions{
		AuthorizationOrigin: issuer.URL, TokenOrigin: issuer.URL, OIDCOrigin: issuer.URL,
		JWKSOrigin: issuer.URL, RevocationOrigin: issuer.URL, Issuer: issuer.URL, FlowTimeout: 80 * time.Millisecond,
	}
	siwc, err := accounts.NewSIWCSimulation(f.handler.(*server).accounts, options)
	if err != nil {
		t.Fatal(err)
	}
	defer siwc.Close()
	installSIWC(t, f, siwc)
	body := `{"account_id":"` + account.ID + `","expected_account_revision":1}`
	if rr := f.request(http.MethodPost, "/api/model-authorizations/start", strings.Replace(body, `:1}`, `:2}`, 1), true); rr.Code != http.StatusBadRequest {
		t.Fatalf("stale start revision=%d %s", rr.Code, rr.Body.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, testOrigin+"/api/model-authorizations/start", strings.NewReader(body)).WithContext(ctx)
	request.Host = "127.0.0.1:4317"
	request.Header.Set("Origin", testOrigin)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.AddCookie(f.cookie)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	cancel()
	if response.Code != http.StatusOK {
		t.Fatalf("start=%d %s", response.Code, response.Body.String())
	}
	var started struct {
		Flow accounts.SIWCFlow `json:"flow"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	var rawStart map[string]map[string]any
	if err = json.Unmarshal(response.Body.Bytes(), &rawStart); err != nil {
		t.Fatal(err)
	}
	if len(rawStart) != 1 {
		t.Fatalf("start envelope=%v", rawStart)
	}
	assertExactJSONFields(t, rawStart["flow"], []string{"operation_id", "state", "handoff_url", "expires_at"})
	statusPath := "/api/model-authorizations/operations/" + started.Flow.OperationID
	if rr := f.request(http.MethodGet, statusPath, "", true); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"state":"waiting"`) {
		t.Fatalf("request context canceled flow=%d %s", rr.Code, rr.Body.String())
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	handoff, err := noRedirect.Get(started.Flow.HandoffURL)
	if err != nil {
		t.Fatal(err)
	}
	handoff.Body.Close()
	if handoff.StatusCode != http.StatusFound {
		t.Fatalf("detached handoff=%d", handoff.StatusCode)
	}
	if rr := f.request(http.MethodPost, statusPath+"/cancel", `{}`, true); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"state":"canceled"`) || !strings.Contains(rr.Body.String(), `"handoff_url":""`) {
		t.Fatalf("cancel=%d %s", rr.Code, rr.Body.String())
	}

	rr := f.request(http.MethodPost, "/api/model-authorizations/start", body, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("second start=%d %s", rr.Code, rr.Body.String())
	}
	if err = json.Unmarshal(rr.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	statusPath = "/api/model-authorizations/operations/" + started.Flow.OperationID
	deadline := time.Now().Add(time.Second)
	for {
		rr = f.request(http.MethodGet, statusPath, "", true)
		if rr.Code == http.StatusOK && strings.Contains(rr.Body.String(), `"state":"expired"`) && strings.Contains(rr.Body.String(), `"handoff_url":""`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expiry=%d %s", rr.Code, rr.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type signedSIWCIssuer struct {
	t       *testing.T
	server  *httptest.Server
	key     *rsa.PrivateKey
	mu      sync.Mutex
	nonce   string
	scopes  string
	subject string
}

func newSignedSIWCIssuer(t *testing.T) *signedSIWCIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &signedSIWCIssuer{t: t, key: key, subject: "signed-subject"}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *signedSIWCIssuer) options() accounts.SIWCOptions {
	return accounts.SIWCOptions{
		AuthorizationOrigin: f.server.URL, TokenOrigin: f.server.URL, OIDCOrigin: f.server.URL,
		JWKSOrigin: f.server.URL, RevocationOrigin: f.server.URL, Issuer: f.server.URL, FlowTimeout: 2 * time.Second,
	}
}

func (f *signedSIWCIssuer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/accounts/authorize":
		query := r.URL.Query()
		f.mu.Lock()
		f.nonce, f.scopes = query.Get("nonce"), query.Get("scope")
		f.mu.Unlock()
		callback, err := url.Parse(query.Get("redirect_uri"))
		if err != nil {
			http.Error(w, "bad redirect", http.StatusBadRequest)
			return
		}
		callback.RawQuery = url.Values{
			"code": {"synthetic-code"}, "state": {query.Get("state")},
			"client_id": {"issued-client"}, "scope": {query.Get("scope")},
		}.Encode()
		http.Redirect(w, r, callback.String(), http.StatusFound)
	case "/api/accounts/oauth/token":
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		nonce, scopes := f.nonce, f.scopes
		f.mu.Unlock()
		if r.Form.Get("grant_type") == "refresh_token" {
			nonce = ""
		}
		token := f.signJWT(r.Form.Get("client_id"), nonce)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "FAKE-SIGNED-ACCESS", "refresh_token": "FAKE-SIGNED-REFRESH",
			"id_token": token, "token_type": "Bearer", "expires_in": 3600,
			"scope": scopes, "earliest_refresh_at": time.Now().Add(-time.Minute).Unix(),
		})
	case "/.well-known/openid-configuration":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer": f.server.URL, "jwks_uri": f.server.URL + "/jwks", "revocation_endpoint": f.server.URL + "/revoke",
		})
	case "/jwks":
		w.Header().Set("Content-Type", "application/json")
		e := big.NewInt(int64(f.key.PublicKey.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "fixture-key", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(f.key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e),
		}}})
	case "/revoke":
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (f *signedSIWCIssuer) signJWT(audience, nonce string) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "fixture-key", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{
		"iss": f.server.URL, "aud": audience, "sub": f.subject, "nonce": nonce,
		"exp": time.Now().Add(time.Hour).Unix(), "nbf": time.Now().Add(-time.Minute).Unix(),
	})
	encoded := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(encoded))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return encoded + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestAuthorizationHTTPRunsOwnedSignedProtocolForStartRefreshAndRevoke(t *testing.T) {
	issuer := newSignedSIWCIssuer(t)
	f := newFixture(t)
	account := createSIWCAccount(t, f, issuer.server.URL+"/v1")
	siwc, err := accounts.NewSIWCSimulation(f.handler.(*server).accounts, issuer.options())
	if err != nil {
		t.Fatal(err)
	}
	defer siwc.Close()
	installSIWC(t, f, siwc)

	start := f.request(http.MethodPost, "/api/model-authorizations/start", `{"account_id":"`+account.ID+`","expected_account_revision":1}`, true)
	if start.Code != http.StatusOK {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	var flowBody struct {
		Flow accounts.SIWCFlow `json:"flow"`
	}
	if err = json.Unmarshal(start.Body.Bytes(), &flowBody); err != nil {
		t.Fatal(err)
	}
	browserResponse, err := http.Get(flowBody.Flow.HandoffURL)
	if err != nil {
		t.Fatal(err)
	}
	browserResponse.Body.Close()
	if browserResponse.StatusCode != http.StatusOK {
		t.Fatalf("browser completion=%d", browserResponse.StatusCode)
	}
	statusPath := "/api/model-authorizations/operations/" + flowBody.Flow.OperationID
	deadline := time.Now().Add(2 * time.Second)
	for {
		status := f.request(http.MethodGet, statusPath, "", true)
		if status.Code == http.StatusOK && strings.Contains(status.Body.String(), `"state":"available"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("available status=%d %s", status.Code, status.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	list := f.request(http.MethodGet, "/api/model-authorizations", "", true)
	var listed struct {
		Authorizations []safeModelAuthorization `json:"authorizations"`
	}
	if err = json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed.Authorizations) != 1 {
		t.Fatalf("authorization list=%d %s err=%v", list.Code, list.Body.String(), err)
	}
	authorization := listed.Authorizations[0]
	if authorization.State != "available" || authorization.Generation != 1 || authorization.Subject != issuer.subject || strings.Contains(list.Body.String(), "FAKE-SIGNED") {
		t.Fatalf("unsafe authorization=%s", list.Body.String())
	}

	refreshPath := "/api/model-authorizations/" + authorization.ID + "/refresh"
	if stale := f.request(http.MethodPost, refreshPath, `{"expected_account_revision":2}`, true); stale.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale refresh=%d %s", stale.Code, stale.Body.String())
	}
	refresh := f.request(http.MethodPost, refreshPath, `{"expected_account_revision":1}`, true)
	if refresh.Code != http.StatusOK || !strings.Contains(refresh.Body.String(), `"generation":2`) || strings.Contains(refresh.Body.String(), "FAKE-SIGNED") {
		t.Fatalf("refresh=%d %s", refresh.Code, refresh.Body.String())
	}
	var refreshed map[string]map[string]any
	if err = json.Unmarshal(refresh.Body.Bytes(), &refreshed); err != nil || len(refreshed) != 1 {
		t.Fatalf("refresh envelope=%v err=%v", refreshed, err)
	}
	assertExactJSONFields(t, refreshed["authorization"], []string{"id", "account_id", "issuer", "subject", "client_id", "scopes", "spend_class", "state", "generation", "expires_at"})
	revokePath := "/api/model-authorizations/" + authorization.ID + "/revoke"
	if stale := f.request(http.MethodPost, revokePath, `{"expected_account_revision":2}`, true); stale.Code != http.StatusBadRequest {
		t.Fatalf("stale revoke=%d %s", stale.Code, stale.Body.String())
	}
	revoke := f.request(http.MethodPost, revokePath, `{"expected_account_revision":1}`, true)
	if revoke.Code != http.StatusOK || !strings.Contains(revoke.Body.String(), `"local_state":"revoked"`) || !strings.Contains(revoke.Body.String(), `"remote_state":"confirmed"`) {
		t.Fatalf("revoke=%d %s", revoke.Code, revoke.Body.String())
	}
	var revoked map[string]map[string]any
	if err = json.Unmarshal(revoke.Body.Bytes(), &revoked); err != nil || len(revoked) != 1 {
		t.Fatalf("revoke envelope=%v err=%v", revoked, err)
	}
	assertExactJSONFields(t, revoked["revocation"], []string{"authorization_id", "local_state", "remote_state", "updated_at"})
}
