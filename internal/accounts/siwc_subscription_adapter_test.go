package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
)

type subscriptionRoundTrip func(*http.Request) (*http.Response, error)

func (f subscriptionRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Removing the release gate must fail even with an unusable lifecycle: account
// lookup, host registration, credential access and callback binding come later.
func TestSubscriptionProductionAdmissionHasNoDependencies(t *testing.T) {
	s := NewSIWCService(nil)
	s.http.Transport = subscriptionRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Error("closed entry used transport")
		return nil, errors.New("forbidden")
	})
	for _, id := range []string{"", "missing", "mutable-account"} {
		_, err := s.Begin(context.Background(), id, 1)
		var provider *model.ProviderError
		if !errors.As(err, &provider) || provider.Code != "live_provider_access_pending_confirmation" || provider.Certainty != "not_sent" {
			t.Fatalf("production admission: %v", err)
		}
		_, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(context.Background(), id, 1)
		if stop != nil || !errors.As(err, &provider) || provider.Certainty != "not_sent" || len(s.flows) != 0 {
			t.Fatalf("owned production admission: %v", err)
		}
	}
}

// Fails if official connection construction depends on simulation eligibility,
// protocol parameters drift, signed proof is skipped, or commit loses binding.
func TestSubscriptionOfficialProfileOfflineProtocolAndReauthorization(t *testing.T) {
	lifecycle, db, secrets, _ := setupAccounts(t)
	account := newSIWCAccount(t, lifecycle, "https://api.openai.com/v1")
	s := NewSIWCService(lifecycle)
	defer s.Close()
	issuer := newSIWCIssuerFixture(t, "https://api.openai.com/v1")
	var query url.Values
	var requests []string
	s.http.Transport = subscriptionRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.String())
		var body any
		switch r.Method + " " + r.URL.String() {
		case "POST https://auth.openai.com/api/accounts/oauth/token":
			_ = r.ParseForm()
			form := r.PostForm
			digest := sha256.Sum256([]byte(form.Get("code_verifier")))
			if len(form) != 6 || form.Get("grant_type") != "authorization_code" || form.Get("client_id") != "oaiapp_synthetic" || form.Get("code") != "synthetic-code" || form.Get("redirect_uri") != query.Get("redirect_uri") || form.Get("resource") != "https://api.openai.com/v1" || base64.RawURLEncoding.EncodeToString(digest[:]) != query.Get("code_challenge") || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Error("incorrect code exchange contract")
			}
			body = map[string]any{"access_token": "SYNTHETIC-ACCESS", "refresh_token": "SYNTHETIC-REFRESH", "token_type": "Bearer", "expires_in": 3600, "scope": "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct", "id_token": issuer.signedToken(map[string]any{"alg": "RS256", "kid": "key-1", "typ": "JWT"}, map[string]any{"iss": "https://auth.openai.com", "aud": "oaiapp_synthetic", "sub": "synthetic-subject", "nonce": query.Get("nonce"), "exp": time.Now().Add(time.Hour).Unix()})}
		case "GET https://auth.openai.com/.well-known/openid-configuration":
			body = map[string]string{"issuer": "https://auth.openai.com", "jwks_uri": "https://auth.openai.com/jwks"}
		case "GET https://auth.openai.com/jwks":
			body = map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "key-1", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(issuer.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(issuer.key.E)).Bytes())}}}
		default:
			t.Errorf("unexpected offline request %s %s", r.Method, r.URL)
			return nil, errors.New("no network available")
		}
		raw, _ := json.Marshal(body)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw))), Request: r}, nil
	})
	var previous url.Values
	for iteration := 0; iteration < 2; iteration++ {
		account, _ = db.GetModelAccount(account.ID)
		var stop func()
		flow, err := s.beginSIWCConnection(context.Background(), context.Background(), account.ID, account.Revision, siwcPurposeSubscription, "https://api.openai.com/v1", &stop)
		if err != nil {
			t.Fatalf("official profile cannot enter shared protocol core: %v", err)
		}
		defer stop()
		response, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Get(flow.HandoffURL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		location, _ := url.Parse(response.Header.Get("Location"))
		query = location.Query()
		if location.Scheme+"://"+location.Host+location.Path != "https://auth.openai.com/api/accounts/authorize" || query.Get("scope") != "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct" || query.Get("resource") != "https://api.openai.com/v1" || query.Get("code_challenge_method") != "S256" || query.Get("response_type") != "code" || query.Get("state") == "" || query.Get("nonce") == "" || query.Get("ext_agent_host_id") == "" {
			t.Fatal("incorrect official authorization request")
		}
		callback, _ := url.Parse(query.Get("redirect_uri"))
		if callback.Scheme != "http" || callback.Hostname() != "127.0.0.1" || callback.Port() == "" || callback.Path != "/auth/callback" {
			t.Fatal("incorrect callback")
		}
		if iteration == 0 {
			if query.Get("client_id") != "dynamic_agent_client" || query.Get("agent_name_hint") != "FOUNDRY" || len(query) != 11 {
				t.Fatal("incorrect initial registration")
			}
		} else if query.Get("client_id") != "oaiapp_synthetic" || query.Has("agent_name_hint") || len(query) != 10 || query.Get("ext_agent_host_id") != previous.Get("ext_agent_host_id") || query.Get("state") == previous.Get("state") || query.Get("nonce") == previous.Get("nonce") || query.Get("code_challenge") == previous.Get("code_challenge") {
			t.Fatal("reauthorization did not reuse host/client with fresh public-client protection")
		}
		response, err = http.Get(query.Get("redirect_uri") + "?" + url.Values{"code": {"synthetic-code"}, "state": {query.Get("state")}, "client_id": {"oaiapp_synthetic"}}.Encode())
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		stop()
		if response.StatusCode != 200 {
			t.Fatalf("callback status=%d", response.StatusCode)
		}
		current, err := db.CurrentModelAuthorization(account.ID)
		if err != nil || current.State != "available" || current.Binding.Issuer != "https://auth.openai.com" || current.Binding.ClientID != "oaiapp_synthetic" || current.Binding.Subject != "synthetic-subject" || len(current.Binding.Scopes) != 6 {
			t.Fatalf("missing validated commit: %+v %v", current, err)
		}
		var bundle storedModelTokenBundle
		if json.Unmarshal([]byte(secrets.values[current.SecretRef]), &bundle) != nil || bundle.Resource != "https://api.openai.com/v1" || bundle.AccessToken != "SYNTHETIC-ACCESS" {
			t.Fatal("incorrect committed bundle")
		}
		previous = query
	}
	want := []string{"POST https://auth.openai.com/api/accounts/oauth/token", "GET https://auth.openai.com/.well-known/openid-configuration", "GET https://auth.openai.com/jwks"}
	if !reflect.DeepEqual(requests, append(want, want...)) {
		t.Fatalf("protocol sequence=%v", requests)
	}
}

// Fails if the owned adapter accepts a generic callback client or a malformed
// transport profile, while leaving legacy simulation semantics unchanged.
func TestSubscriptionAdapterRejectsInvalidIssuedClient(t *testing.T) {
	for _, client := range []string{"issued-client", "dynamic_agent_client", "oaiapp_", "oaiapp_bad/client", "oaiapp_bad value"} {
		t.Run(client, func(t *testing.T) {
			lifecycle, db, secrets, _ := setupAccounts(t)
			issuer := newSIWCIssuerFixture(t, "https://api.openai.com/v1")
			account := newSIWCAccount(t, lifecycle, issuer.server.URL+"/v1")
			s, _ := NewSIWCSimulation(lifecycle, issuer.options())
			flow, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(context.Background(), account.ID, account.Revision)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			query := openHandoff(t, flow)
			issuer.mu.Lock()
			issuer.nonce = query.Get("nonce")
			issuer.mu.Unlock()
			response, err := http.Get(query.Get("redirect_uri") + "?" + url.Values{"code": {"synthetic-code"}, "state": {query.Get("state")}, "client_id": {client}}.Encode())
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			stop()
			if response.StatusCode != 400 || issuer.tokenCalls.Load() != 0 || secrets.sets != 0 {
				t.Fatal("invalid issued client reached exchange or commit")
			}
			if _, err = db.CurrentModelAuthorization(account.ID); err == nil {
				t.Fatal("invalid callback authorized")
			}
		})
	}
}

func TestSubscriptionAdapterRejectsMalformedProfilesBeforeEffects(t *testing.T) {
	for _, field := range []string{"authorization", "token", "discovery", "jwks", "issuer", "revocation"} {
		t.Run(field, func(t *testing.T) {
			lifecycle, _, secrets, _ := setupAccounts(t)
			account := newSIWCAccount(t, lifecycle, "http://127.0.0.1:1/v1")
			s := NewSIWCService(lifecycle)
			s.simulation = true
			s.options = SIWCOptions{AuthorizationOrigin: "http://127.0.0.1:1", TokenOrigin: "http://127.0.0.1:1", OIDCOrigin: "http://127.0.0.1:1", JWKSOrigin: "http://127.0.0.1:1", Issuer: "http://127.0.0.1:1", RevocationOrigin: "http://127.0.0.1:1", FlowTimeout: time.Minute}
			switch field {
			case "authorization":
				s.options.AuthorizationOrigin = "https://auth.openai.com"
			case "token":
				s.options.TokenOrigin = "http://localhost:1"
			case "discovery":
				s.options.OIDCOrigin = "http://127.0.0.1:1/path"
			case "jwks":
				s.options.JWKSOrigin = "http://127.0.0.1:01"
			case "issuer":
				s.options.Issuer = "https://foreign.invalid"
			case "revocation":
				s.options.RevocationOrigin = "http://127.0.0.1:1?"
			}
			_, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(context.Background(), account.ID, account.Revision)
			if stop != nil {
				stop()
			}
			if !errors.Is(err, ErrInvalid) || len(s.flows) != 0 || secrets.sets != 0 {
				t.Fatalf("malformed profile was allowed to start: %v", err)
			}
		})
	}
}

// The local transport executes the same subscription profile while storage
// remains bound to the synthetic resource, never the official audience.
func TestSubscriptionOwnedLoopbackKeepsSimulationBinding(t *testing.T) {
	lifecycle, db, secrets, _ := setupAccounts(t)
	issuer := newSIWCIssuerFixture(t, "https://api.openai.com/v1")
	account := newSIWCAccount(t, lifecycle, issuer.server.URL+"/v1")
	s, _ := NewSIWCSimulation(lifecycle, issuer.options())
	flow, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	query := openHandoff(t, flow)
	issuer.mu.Lock()
	issuer.nonce = query.Get("nonce")
	issuer.mu.Unlock()
	response, err := http.Get(query.Get("redirect_uri") + "?" + url.Values{"code": {"synthetic-code"}, "state": {query.Get("state")}, "client_id": {"oaiapp_synthetic"}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	stop()
	if response.StatusCode != 200 || query.Get("resource") != "https://api.openai.com/v1" || issuer.lastToken.Get("resource") != "https://api.openai.com/v1" {
		t.Fatal("local adapter did not run subscription protocol")
	}
	current, err := db.CurrentModelAuthorization(account.ID)
	if err != nil || current.Binding.Issuer != issuer.server.URL {
		t.Fatal("local binding promoted", err)
	}
	var bundle storedModelTokenBundle
	if json.Unmarshal([]byte(secrets.values[current.SecretRef]), &bundle) != nil || bundle.Resource != account.BaseURL {
		t.Fatal("local credential binding promoted")
	}
}

// An explicit callback grant cannot silently become an absent grant. Otherwise
// the token response could broaden a user-visible empty consent result.
func TestSubscriptionAdapterRejectsExplicitEmptyCallbackScope(t *testing.T) {
	lifecycle, db, secrets, _ := setupAccounts(t)
	issuer := newSIWCIssuerFixture(t, "https://api.openai.com/v1")
	account := newSIWCAccount(t, lifecycle, issuer.server.URL+"/v1")
	s, _ := NewSIWCSimulation(lifecycle, issuer.options())
	flow, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	query := openHandoff(t, flow)
	issuer.mu.Lock()
	issuer.nonce = query.Get("nonce")
	issuer.mu.Unlock()
	response, err := http.Get(query.Get("redirect_uri") + "?" + url.Values{"code": {"synthetic-code"}, "state": {query.Get("state")}, "client_id": {"oaiapp_synthetic"}, "scope": {""}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	stop()
	if response.StatusCode != 400 || secrets.sets != 0 {
		t.Fatal("empty callback grant authorized")
	}
	if _, err := db.CurrentModelAuthorization(account.ID); err == nil {
		t.Fatal("empty callback grant committed")
	}
}

func TestSubscriptionAdapterRejectsPreviouslyIssuedInvalidClientBeforeStart(t *testing.T) {
	for _, client := range []string{"issued-client", "oaiapp_", "dynamic_agent_client"} {
		t.Run(client, func(t *testing.T) {
			lifecycle, db, secrets, _ := setupAccounts(t)
			issuer := newSIWCIssuerFixture(t, "https://api.openai.com/v1")
			account := newSIWCAccount(t, lifecycle, issuer.server.URL+"/v1")
			_, err := lifecycle.BeginModelAuthorization(context.Background(), account.ID, "existing", "sign_in", "existing-auth")
			if err != nil {
				t.Fatal(err)
			}
			_, err = lifecycle.CommitModelAuthorization(context.Background(), "existing", domain.ModelAuthorizationBinding{ID: "existing-auth", AccountID: account.ID, Issuer: issuer.server.URL, Subject: "synthetic-subject", ClientID: client, SpendClass: "subscription_only", Scopes: []string{"openid", "profile", "email"}}, ModelTokenBundle{IDToken: "SYNTHETIC-ID", AccessToken: "SYNTHETIC-ACCESS", Resource: account.BaseURL, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
			if err != nil {
				t.Fatal(err)
			}
			account, _ = db.GetModelAccount(account.ID)
			s, _ := NewSIWCSimulation(lifecycle, issuer.options())
			sets, gets := secrets.sets, secrets.gets
			_, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(context.Background(), account.ID, account.Revision)
			if stop != nil {
				stop()
			}
			if !errors.Is(err, ErrInvalid) || len(s.flows) != 0 || secrets.sets != sets || secrets.gets != gets || issuer.tokenCalls.Load() != 0 {
				t.Fatal("invalid persisted client admitted", err)
			}
		})
	}
}

func TestSubscriptionOfficialProfileRejectsForeignOriginsAndIneligibleAccounts(t *testing.T) {
	for _, field := range []string{"authorization", "token", "discovery", "jwks", "issuer", "revocation", "timeout", "account", "revision", "purpose", "resource"} {
		t.Run(field, func(t *testing.T) {
			lifecycle, _, secrets, _ := setupAccounts(t)
			baseURL := "https://api.openai.com/v1"
			if field == "account" {
				baseURL = "http://127.0.0.1:1/v1"
			}
			account := newSIWCAccount(t, lifecycle, baseURL)
			s := NewSIWCService(lifecycle)
			purpose, resource := siwcPurposeSubscription, "https://api.openai.com/v1"
			switch field {
			case "authorization":
				s.options.AuthorizationOrigin = "https://auth.openai.com/"
			case "token":
				s.options.TokenOrigin = "https://foreign.invalid"
			case "discovery":
				s.options.OIDCOrigin = "http://auth.openai.com"
			case "jwks":
				s.options.JWKSOrigin = "https://auth.openai.com:443"
			case "issuer":
				s.options.Issuer = "https://foreign.invalid"
			case "revocation":
				s.options.RevocationOrigin = "https://foreign.invalid"
			case "timeout":
				s.options.FlowTimeout = 0
			case "revision":
				account.Revision++
			case "purpose":
				purpose = siwcPurposeIdentity
			case "resource":
				resource = "https://foreign.invalid/v1"
			}
			var stop func()
			_, err := s.beginSIWCConnection(context.Background(), context.Background(), account.ID, account.Revision, purpose, resource, &stop)
			if stop != nil {
				stop()
			}
			if !errors.Is(err, ErrInvalid) || len(s.flows) != 0 || secrets.sets != 0 {
				t.Fatal("invalid profile admitted", err)
			}
		})
	}
}

func TestSubscriptionDiscoveryPinsIssuerAndJWKSWithoutRequiringRevocation(t *testing.T) {
	for _, tc := range []struct {
		name, issuer, jwks, revocation string
		connectionOK, legacyOK         bool
	}{
		{"missing-revocation", "https://auth.openai.com", "https://auth.openai.com/jwks", "", true, false},
		{"pinned-revocation", "https://auth.openai.com", "https://auth.openai.com/jwks", "https://auth.openai.com/revoke", true, true},
		{"foreign-revocation", "https://auth.openai.com", "https://auth.openai.com/jwks", "https://foreign.invalid/revoke", false, false},
		{"foreign-issuer", "https://foreign.invalid", "https://auth.openai.com/jwks", "", false, false},
		{"foreign-jwks", "https://auth.openai.com", "https://foreign.invalid/jwks", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSIWCService(nil)
			s.http.Transport = subscriptionRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://auth.openai.com/.well-known/openid-configuration" {
					t.Fatal("unpinned discovery request")
				}
				raw, _ := json.Marshal(map[string]string{"issuer": tc.issuer, "jwks_uri": tc.jwks, "revocation_endpoint": tc.revocation})
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})
			_, err := s.readSIWCDiscovery(context.Background(), true)
			if (err == nil) != tc.connectionOK {
				t.Fatal("connection discovery pinning", err)
			}
			_, err = s.fetchSIWCDiscovery(context.Background())
			if (err == nil) != tc.legacyOK {
				t.Fatal("legacy discovery gate changed", err)
			}
		})
	}
}

// Real loopback exchange failures consume the attempt, including unknown
// completion. Reopening the durable lifecycle cannot replay the request.
func TestSubscriptionConnectionFailureNeverRetriesOrReplaysAfterRestart(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		status                                     int
		drop, partialWrite                         bool
		wantStage, wantCode, wantCommit, wantClean string
	}{
		{"eligibility", 403, false, false, "token_exchange", "auth_invalid", "not_committed", "completed"},
		{"rate-limit", 429, false, false, "token_exchange", "rate_limited", "not_committed", "completed"},
		{"server", 503, false, false, "token_exchange", "server_error", "not_committed", "completed"},
		{"lost-response", 0, true, false, "token_exchange", "transport_unknown", "not_committed", "completed"},
		{"partial-credential-write", 0, false, true, "credential_commit", "storage_error", "unknown", "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lifecycle, db, secrets, path := setupAccounts(t)
			issuer := newSIWCIssuerFixture(t, "https://api.openai.com/v1")
			issuer.tokenStatus, issuer.dropToken = tc.status, tc.drop
			if tc.partialWrite {
				secrets.setErr = errors.New("synthetic partial write")
				secrets.deleteErr = errors.New("synthetic cleanup unavailable")
			}
			account := newSIWCAccount(t, lifecycle, issuer.server.URL+"/v1")
			s, _ := NewSIWCSimulation(lifecycle, issuer.options())
			flow, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(context.Background(), account.ID, account.Revision)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			query := openHandoff(t, flow)
			issuer.mu.Lock()
			issuer.nonce = query.Get("nonce")
			issuer.mu.Unlock()
			callback := query.Get("redirect_uri") + "?" + url.Values{"code": {"synthetic-code"}, "state": {query.Get("state")}, "client_id": {"oaiapp_synthetic"}}.Encode()
			response, err := http.Get(callback)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			stop()
			terminal, waitErr := s.Wait(flow.OperationID)
			if waitErr != nil || terminal.ConnectionDiagnostic == nil || terminal.ConnectionDiagnostic.Stage != tc.wantStage || terminal.ConnectionDiagnostic.Code != tc.wantCode || terminal.ConnectionDiagnostic.CredentialCommit != tc.wantCommit || terminal.ConnectionDiagnostic.Cleanup != tc.wantClean {
				t.Fatalf("diagnostic=%+v %v", terminal.ConnectionDiagnostic, waitErr)
			}
			if response.StatusCode != 400 || issuer.tokenCalls.Load() != 1 {
				t.Fatal("failure retried or reported success")
			}
			if replay, err := http.Get(callback); err == nil {
				replay.Body.Close()
				if replay.StatusCode == 200 {
					t.Fatal("replay accepted")
				}
			}
			if _, err := db.CurrentModelAuthorization(account.ID); err == nil {
				t.Fatal("failed attempt became current")
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			recovered := New(reopened, secrets)
			if tc.partialWrite {
				if err := recovered.Recover(context.Background()); err == nil || len(secrets.values) != 1 {
					t.Fatal("unresolved cleanup lost durable intent")
				}
				secrets.deleteErr = nil
			}
			if err := recovered.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			if issuer.tokenCalls.Load() != 1 || len(secrets.values) != 0 {
				t.Fatal("recovery replayed exchange or retained failed credential")
			}
			op, err := reopened.GetModelAuthorizationOperation(flow.OperationID)
			if err != nil || op.State == "available" || op.State == "pending" {
				t.Fatal("failure lost consumed state", err)
			}
		})
	}
}

func TestSubscriptionConnectionDiagnosticStagesForBoundedProtocolFaults(t *testing.T) {
	for _, tc := range []struct {
		name, stage, code string
		configure         func(*siwcIssuerFixture, *SIWCOptions)
	}{
		{"malformed-token", "token_exchange", "response_incomplete", func(issuer *siwcIssuerFixture, _ *SIWCOptions) {
			issuer.identityResponse = func(_, _ string) map[string]any {
				return map[string]any{"access_token": "SYNTHETIC", "expires_in": "not-a-number"}
			}
		}},
		{"oversized-token", "token_exchange", "response_incomplete", func(issuer *siwcIssuerFixture, _ *SIWCOptions) {
			issuer.accessToken = strings.Repeat("x", 65537)
		}},
		{"grant-mismatch", "grant_validation", "entitlement_denied", func(issuer *siwcIssuerFixture, _ *SIWCOptions) {
			issuer.scopes = append(issuer.scopes, "provider.private.scope")
		}},
		{"discovery-failure", "oidc_discovery", "auth_invalid", func(_ *siwcIssuerFixture, options *SIWCOptions) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
			t.Cleanup(server.Close)
			options.OIDCOrigin = server.URL
		}},
		{"invalid-jwt", "identity_validation", "auth_invalid", func(issuer *siwcIssuerFixture, _ *SIWCOptions) {
			issuer.identityResponse = func(_, _ string) map[string]any {
				return map[string]any{"access_token": "SYNTHETIC-ACCESS", "refresh_token": "SYNTHETIC-REFRESH", "id_token": "not-a-signed-jwt", "token_type": "Bearer", "expires_in": 3600, "scope": strings.Join(requestedSIWCScopes, " ")}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lifecycle, db, secrets, _ := setupAccounts(t)
			issuer := newSIWCIssuerFixture(t, "https://api.openai.com/v1")
			options := issuer.options()
			tc.configure(issuer, &options)
			account := newSIWCAccount(t, lifecycle, issuer.server.URL+"/v1")
			s, err := NewSIWCSimulation(lifecycle, options)
			if err != nil {
				t.Fatal(err)
			}
			flow, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(context.Background(), account.ID, account.Revision)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			query := openHandoff(t, flow)
			issuer.mu.Lock()
			issuer.nonce = query.Get("nonce")
			issuer.mu.Unlock()
			callback := query.Get("redirect_uri") + "?" + url.Values{"code": {"synthetic-code"}, "state": {query.Get("state")}, "client_id": {"oaiapp_synthetic"}}.Encode()
			response, err := http.Get(callback)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			stop()
			terminal, err := s.Wait(flow.OperationID)
			if err != nil || terminal.State != "failed" || terminal.ConnectionDiagnostic == nil || terminal.ConnectionDiagnostic.Stage != tc.stage || terminal.ConnectionDiagnostic.Code != tc.code || terminal.ConnectionDiagnostic.CredentialCommit != "not_committed" || terminal.ConnectionDiagnostic.Cleanup != "completed" {
				t.Fatalf("terminal=%+v %v", terminal, err)
			}
			if issuer.tokenCalls.Load() != 1 || len(secrets.values) != 0 {
				t.Fatal("fault retried or retained a credential")
			}
			if _, err = db.CurrentModelAuthorization(account.ID); err == nil {
				t.Fatal("invalid proof became current")
			}
		})
	}
}

func TestSubscriptionPostCommitCleanupFailurePreservesCommittedDiagnostic(t *testing.T) {
	lifecycle, db, secrets, _ := setupAccounts(t)
	selection := selectionFixture(t, lifecycle, db, true)
	first := commitAuthorization(t, lifecycle, &selection, "initial-sign-in", "sign_in", []string{"openid", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"})
	const operationID = "post-commit-cleanup-failure"
	if _, err := lifecycle.BeginModelAuthorization(context.Background(), first.Binding.AccountID, operationID, "sign_in", first.Binding.ID); err != nil {
		t.Fatal(err)
	}
	secrets.deleteErr = errors.New("SYNTHETIC-PRIVATE-CLEANUP-FAILURE")
	_, failure := lifecycle.CommitModelAuthorization(context.Background(), operationID, first.Binding, ModelTokenBundle{AccessToken: "SYNTHETIC-REPLACEMENT-ACCESS", RefreshToken: "SYNTHETIC-REPLACEMENT-REFRESH", IDToken: "SYNTHETIC-REPLACEMENT-ID", ExpiresAt: "2099-01-01T00:00:00Z", EarliestRefreshAt: "2098-01-01T00:00:00Z", Resource: selection.Route.BaseURL})
	if failure == nil || strings.Contains(failure.Error(), "SYNTHETIC-PRIVATE") {
		t.Fatalf("post-commit cleanup failure was missing or unsafe: %v", failure)
	}
	op, err := db.GetModelAuthorizationOperation(operationID)
	if err != nil || op.State != "available" {
		t.Fatalf("successful credential commit was lost: %+v %v", op, err)
	}
	service := NewSIWCService(lifecycle)
	attempt := &siwcAttempt{public: SIWCFlow{OperationID: operationID}}
	diagnostic := service.siwcConnectionDiagnostic(attempt, "session", "transport_unknown", siwcAtStage("credential_commit", "storage_error", failure))
	if diagnostic == nil || diagnostic.Stage != "credential_commit" || diagnostic.Code != "storage_error" || diagnostic.CredentialCommit != "committed" || diagnostic.Cleanup != "unknown" {
		t.Fatalf("post-commit diagnostic=%+v", diagnostic)
	}
}
