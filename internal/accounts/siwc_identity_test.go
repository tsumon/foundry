package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
)

func TestSIWCIdentityOnlyFlowPersistsSignedProofWithoutModelCredentials(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("identity sign-in contacted a model resource")
	}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	issuer.identityOnlyToken = true
	siwc, err := NewSIWCSimulation(service, issuer.options())
	if err != nil {
		t.Fatal(err)
	}

	flow, err := siwc.BeginIdentity(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	query := openHandoff(t, flow)
	if query.Get("client_id") != "dynamic_agent_client" || query.Get("scope") != "openid profile email" || query.Has("resource") || query.Get("response_type") != "code" || query.Get("code_challenge_method") != "S256" || query.Get("nonce") == "" || query.Get("code_challenge") == "" {
		t.Fatalf("identity authorization request was broadened or incomplete: %v", query)
	}
	for _, hint := range []string{"account", "account_hint", "email", "email_hint", "workspace", "workspace_hint", "organization"} {
		if query.Has(hint) {
			t.Fatalf("identity authorization sent selection hint %q: %v", hint, query)
		}
	}
	issuer.mu.Lock()
	issuer.nonce = query.Get("nonce")
	issuer.mu.Unlock()
	callback := query.Get("redirect_uri") + "?code=identity-code&state=" + url.QueryEscape(query.Get("state")) + "&client_id=" + issuer.issuedClient
	response, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("identity callback status=%d", response.StatusCode)
	}

	issuer.mu.Lock()
	form := issuer.lastToken
	issuer.mu.Unlock()
	if form.Get("resource") != "" || form.Get("grant_type") != "authorization_code" || form.Get("code_verifier") == "" {
		t.Fatalf("identity exchange retained resource or lost PKCE: %v", form)
	}
	authorization, err := db.CurrentModelAuthorization(account.ID)
	if err != nil || authorization.Binding.SpendClass != "identity_only" || !reflect.DeepEqual(authorization.Binding.Scopes, []string{"openid"}) {
		t.Fatalf("identity binding = %+v, %v", authorization, err)
	}
	secret, err := secrets.Get(authorization.SecretRef)
	if err != nil {
		t.Fatal(err)
	}
	var bundle storedModelTokenBundle
	if err := json.Unmarshal([]byte(secret), &bundle); err != nil || bundle.IDToken == "" || bundle.AccessToken != "" || bundle.RefreshToken != "" || bundle.Resource != account.BaseURL {
		t.Fatalf("identity bundle=%+v err=%v", bundle, err)
	}
	if expiry, err := time.Parse(time.RFC3339, bundle.ExpiresAt); err != nil || !expiry.After(time.Now().Add(30*time.Minute)) {
		t.Fatalf("identity expiry did not come from the signed token: %q %v", bundle.ExpiresAt, err)
	}
	route, err := db.CreateModelRoute(domain.ModelRoute{ID: "identity-only-route", AccountID: account.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: account.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	selection := domain.RoleModelSelection{Profile: domain.Profile{ID: "identity-only", AccountID: account.ID, AccountRevision: account.Revision, RouteID: route.ID, Protocol: route.Protocol, BaseURL: route.BaseURL, Model: "synthetic"}, Account: &account, Route: &route, Authorization: &authorization.Binding}
	gets, tokenCalls := secrets.gets, issuer.tokenCalls.Load()
	if _, err = service.ResolveModelCredential(context.Background(), selection); err == nil || !strings.Contains(err.Error(), "entitlement_denied") {
		t.Fatalf("identity authorization leased a credential: %v", err)
	}
	if _, err = siwc.Refresh(context.Background(), authorization.Binding.ID, account.Revision); err == nil {
		t.Fatal("identity authorization refreshed")
	}
	if secrets.gets != gets || issuer.tokenCalls.Load() != tokenCalls {
		t.Fatalf("identity denial touched secrets or issuer: gets=%d->%d calls=%d->%d", gets, secrets.gets, tokenCalls, issuer.tokenCalls.Load())
	}
}

func TestSIWCIdentityOnlyRejectsBroadenedGrantsBeforeSecretPersistence(t *testing.T) {
	for _, test := range []struct {
		name     string
		response func(*siwcIssuerFixture, string, string) map[string]any
	}{
		{
			name: "broadened scope",
			response: func(issuer *siwcIssuerFixture, client, nonce string) map[string]any {
				return map[string]any{"id_token": issuer.idToken(client, nonce, issuer.subject, time.Now().Add(time.Hour)), "scope": "openid profile email resource.invoke"}
			},
		},
		{
			name: "refresh token",
			response: func(issuer *siwcIssuerFixture, client, nonce string) map[string]any {
				return map[string]any{"id_token": issuer.idToken(client, nonce, issuer.subject, time.Now().Add(time.Hour)), "refresh_token": "synthetic-refresh"}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, db, secrets, _ := setupAccounts(t)
			resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			defer resource.Close()
			account := newSIWCAccount(t, service, resource.URL+"/v1")
			issuer := newSIWCIssuerFixture(t, account.BaseURL)
			issuer.identityResponse = func(client, nonce string) map[string]any { return test.response(issuer, client, nonce) }
			siwc, err := NewSIWCSimulation(service, issuer.options())
			if err != nil {
				t.Fatal(err)
			}
			flow, err := siwc.BeginIdentity(context.Background(), account.ID, account.Revision)
			if err != nil {
				t.Fatal(err)
			}
			query := openHandoff(t, flow)
			issuer.mu.Lock()
			issuer.nonce = query.Get("nonce")
			issuer.mu.Unlock()
			response, err := http.Get(query.Get("redirect_uri") + "?code=rejected&state=" + url.QueryEscape(query.Get("state")) + "&client_id=" + issuer.issuedClient)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode == http.StatusOK || secrets.sets != 0 {
				t.Fatalf("unsafe identity response persisted: status=%d sets=%d", response.StatusCode, secrets.sets)
			}
			if _, err := db.CurrentModelAuthorization(account.ID); err == nil {
				t.Fatal("rejected identity response created an authorization")
			}
		})
	}
}

func TestSIWCIdentityDoesNotReplaceBroaderAuthorization(t *testing.T) {
	service, db, _, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, err := NewSIWCSimulation(service, issuer.options())
	if err != nil {
		t.Fatal(err)
	}
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	current, err := db.CurrentModelAuthorization(account.ID)
	if err != nil || current.Binding.SpendClass != "subscription_only" {
		t.Fatalf("fixture did not establish a broader authorization: %+v %v", current, err)
	}
	calls := issuer.tokenCalls.Load()
	if _, err := siwc.BeginIdentity(context.Background(), account.ID, account.Revision); err == nil {
		t.Fatal("identity flow silently replaced broader authorization")
	}
	if issuer.tokenCalls.Load() != calls {
		t.Fatal("identity conversion reached the issuer")
	}
}

func TestOwnedSIWCIdentityStopCancelsCallbackBeforeAnySecretWrite(t *testing.T) {
	service, _, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	issuer.identityOnlyToken = true
	siwc, err := NewSIWCSimulation(service, issuer.options())
	if err != nil {
		t.Fatal(err)
	}
	flow, stop, err := siwc.BeginOwnedIdentitySimulation(context.Background(), account.ID, account.Revision)
	if err != nil || stop == nil {
		t.Fatalf("begin owned identity: %+v %v", flow, err)
	}
	query := openHandoff(t, flow)
	stop()
	issuer.mu.Lock()
	issuer.nonce = query.Get("nonce")
	issuer.mu.Unlock()
	response, callbackErr := http.Get(query.Get("redirect_uri") + "?code=late&state=" + url.QueryEscape(query.Get("state")) + "&client_id=" + issuer.issuedClient)
	if response != nil {
		response.Body.Close()
	}
	if callbackErr == nil {
		t.Fatal("stopped identity callback was still admitted")
	}
	status, err := siwc.Status(flow.OperationID)
	if err != nil || status.State != "canceled" || status.HandoffURL != "" || issuer.tokenCalls.Load() != 0 || secrets.sets != 0 {
		t.Fatalf("owned identity stop admitted late work: status=%+v err=%v calls=%d sets=%d", status, err, issuer.tokenCalls.Load(), secrets.sets)
	}
}

func TestSIWCIdentitySecretWriteFailureCleansExactProofReference(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	secrets.setErr = errors.New("synthetic identity storage failure")
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	issuer.identityOnlyToken = true
	siwc, err := NewSIWCSimulation(service, issuer.options())
	if err != nil {
		t.Fatal(err)
	}
	flow, err := siwc.BeginIdentity(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	query := openHandoff(t, flow)
	issuer.mu.Lock()
	issuer.nonce = query.Get("nonce")
	issuer.mu.Unlock()
	response, err := http.Get(query.Get("redirect_uri") + "?code=storage-failure&state=" + url.QueryEscape(query.Get("state")) + "&client_id=" + issuer.issuedClient)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK || secrets.sets != 1 || secrets.deletes != 1 || len(secrets.values) != 0 {
		t.Fatalf("failed identity proof left storage behind: status=%d sets=%d deletes=%d values=%d", response.StatusCode, secrets.sets, secrets.deletes, len(secrets.values))
	}
	if _, err := db.CurrentModelAuthorization(account.ID); err == nil {
		t.Fatal("failed identity proof became an authorization")
	}
}
