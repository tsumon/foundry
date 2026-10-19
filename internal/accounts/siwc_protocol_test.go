package accounts

import (
	"bufio"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/model"
)

var requestedSIWCScopes = []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}

type siwcIssuerFixture struct {
	t                 *testing.T
	server            *httptest.Server
	key               *rsa.PrivateKey
	resource          string
	mu                sync.Mutex
	nonce             string
	callback          string
	challenge         string
	issuedClient      string
	subject           string
	scopes            []string
	tokenCalls        atomic.Int32
	revokeCalls       atomic.Int32
	revokeStatus      int
	onRevoke          func()
	onToken           func(url.Values)
	dropToken         bool
	tokenStatus       int
	tokenError        string
	tokenRedirectURL  string
	revocationURL     string
	earliestRefreshAt int64
	accessToken       string
	refreshToken      string
	lastToken         url.Values
	lastRevoke        url.Values
}

func newSIWCIssuerFixture(t *testing.T, resource string) *siwcIssuerFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &siwcIssuerFixture{
		t: t, key: key, resource: resource, issuedClient: "oaiapp_synthetic", subject: "subject-synthetic",
		scopes: append([]string(nil), requestedSIWCScopes...), accessToken: "FAKE-SIWC-ACCESS", refreshToken: "FAKE-SIWC-REFRESH",
		earliestRefreshAt: time.Now().Add(-time.Minute).Unix(),
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *siwcIssuerFixture) options() SIWCOptions {
	return SIWCOptions{AuthorizationOrigin: f.server.URL, TokenOrigin: f.server.URL, OIDCOrigin: f.server.URL, JWKSOrigin: f.server.URL, RevocationOrigin: f.server.URL, Issuer: f.server.URL, FlowTimeout: time.Minute}
}

func (f *siwcIssuerFixture) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		w.Header().Set("Content-Type", "application/json")
		revocationURL := f.revocationURL
		if revocationURL == "" {
			revocationURL = f.server.URL + "/revoke"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": f.server.URL, "jwks_uri": f.server.URL + "/jwks", "revocation_endpoint": revocationURL})
	case "/jwks":
		w.Header().Set("Content-Type", "application/json")
		e := big.NewInt(int64(f.key.PublicKey.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "key-1", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(f.key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)}}})
	case "/api/accounts/oauth/token":
		f.tokenCalls.Add(1)
		if err := r.ParseForm(); err != nil {
			f.t.Error(err)
		}
		f.mu.Lock()
		f.lastToken = r.PostForm
		nonce, client := f.nonce, r.PostForm.Get("client_id")
		f.mu.Unlock()
		if f.onToken != nil {
			f.onToken(r.PostForm)
		}
		if f.dropToken {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				f.t.Error("test server cannot drop connection")
				return
			}
			connection, _, err := hijacker.Hijack()
			if err != nil {
				f.t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		if f.tokenRedirectURL != "" {
			http.Redirect(w, r, f.tokenRedirectURL, http.StatusFound)
			return
		}
		if f.tokenStatus != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(f.tokenStatus)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": f.tokenError})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":        f.accessToken,
			"refresh_token":       f.refreshToken,
			"id_token":            f.idToken(client, nonce, f.subject, time.Now().Add(time.Hour)),
			"token_type":          "Bearer",
			"expires_in":          3600,
			"scope":               strings.Join(f.scopes, " "),
			"earliest_refresh_at": f.earliestRefreshAt,
		})
	case "/revoke":
		f.revokeCalls.Add(1)
		_ = r.ParseForm()
		f.mu.Lock()
		f.lastRevoke = r.PostForm
		f.mu.Unlock()
		if f.onRevoke != nil {
			f.onRevoke()
		}
		status := f.revokeStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
	default:
		http.NotFound(w, r)
	}
}

func (f *siwcIssuerFixture) idToken(client, nonce, subject string, expires time.Time) string {
	f.t.Helper()
	return f.signedToken(
		map[string]any{"alg": "RS256", "kid": "key-1", "typ": "JWT"},
		map[string]any{"iss": f.server.URL, "aud": client, "sub": subject, "nonce": nonce, "exp": expires.Unix(), "iat": time.Now().Add(-time.Minute).Unix()},
	)
}

func (f *siwcIssuerFixture) signedToken(headerClaims, payloadClaims map[string]any) string {
	f.t.Helper()
	header, _ := json.Marshal(headerClaims)
	payload, _ := json.Marshal(payloadClaims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func openHandoff(t *testing.T, flow SIWCFlow) url.Values {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(flow.HandoffURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("handoff status=%d", response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return location.Query()
}

func completeInitialSIWCSignIn(t *testing.T, siwc *SIWCService, account domain.ModelAccount, issuer *siwcIssuerFixture) (SIWCFlow, url.Values) {
	t.Helper()
	flow, err := siwc.Begin(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	query := openHandoff(t, flow)
	issuer.mu.Lock()
	issuer.nonce = query.Get("nonce")
	issuer.callback = query.Get("redirect_uri")
	issuer.challenge = query.Get("code_challenge")
	issuer.mu.Unlock()
	callback := query.Get("redirect_uri") + "?code=synthetic-code&state=" + url.QueryEscape(query.Get("state")) + "&client_id=" + issuer.issuedClient + "&scope=" + url.QueryEscape(strings.Join(issuer.scopes, " "))
	response, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status=%d", response.StatusCode)
	}
	return flow, query
}

func TestSIWCBeginUsesOwnedListenerPKCEAndCommitsValidatedIdentity(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resourceServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("resource called during sign-in") }))
	defer resourceServer.Close()
	account := newSIWCAccount(t, service, resourceServer.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, err := NewSIWCSimulation(service, issuer.options())
	if err != nil {
		t.Fatal(err)
	}
	flow, err := siwc.Begin(context.Background(), account.ID, account.Revision)
	if err != nil || flow.OperationID == "" || flow.State != "waiting" || flow.HandoffURL == "" || flow.ExpiresAt == "" {
		t.Fatalf("bad start: %+v %v", flow, err)
	}
	query := openHandoff(t, flow)
	for _, key := range []string{"state", "nonce", "code_challenge", "redirect_uri", "ext_agent_host_id"} {
		if query.Get(key) == "" {
			t.Fatalf("missing authorization parameter %s", key)
		}
	}
	if query.Get("client_id") != "dynamic_agent_client" || query.Get("agent_name_hint") != "FOUNDRY" || query.Get("code_challenge_method") != "S256" || query.Get("resource") != account.BaseURL || query.Get("scope") != strings.Join(requestedSIWCScopes, " ") {
		t.Fatalf("bad authorization query: %v", query)
	}
	redirect, err := url.Parse(query.Get("redirect_uri"))
	if err != nil || redirect.Scheme != "http" || redirect.Hostname() != "127.0.0.1" || redirect.Path != "/auth/callback" {
		t.Fatalf("bad callback URI: %q %v", query.Get("redirect_uri"), err)
	}
	if !strings.HasPrefix(query.Get("ext_agent_host_id"), "urn:uuid:") {
		t.Fatalf("bad host ID: %q", query.Get("ext_agent_host_id"))
	}
	issuer.mu.Lock()
	issuer.nonce = query.Get("nonce")
	issuer.callback = query.Get("redirect_uri")
	issuer.challenge = query.Get("code_challenge")
	issuer.mu.Unlock()

	callback := query.Get("redirect_uri") + "?code=synthetic-code&state=" + url.QueryEscape(query.Get("state")) + "&client_id=" + issuer.issuedClient + "&scope=" + url.QueryEscape(strings.Join(issuer.scopes, " "))
	response, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status=%d", response.StatusCode)
	}
	status, err := siwc.Status(flow.OperationID)
	if err != nil || status.State != "available" || status.HandoffURL != "" {
		t.Fatalf("bad terminal flow: %+v %v", status, err)
	}
	authorization, err := db.CurrentModelAuthorization(account.ID)
	if err != nil || authorization.State != "available" || authorization.Binding.Issuer != issuer.server.URL || authorization.Binding.Subject != issuer.subject || authorization.Binding.ClientID != issuer.issuedClient || !reflect.DeepEqual(authorization.Binding.Scopes, requestedSIWCScopes) {
		t.Fatalf("bad authorization: %+v %v", authorization, err)
	}
	if issuer.tokenCalls.Load() != 1 || secrets.sets != 1 {
		t.Fatalf("token calls=%d secret writes=%d", issuer.tokenCalls.Load(), secrets.sets)
	}
	issuer.mu.Lock()
	form := issuer.lastToken
	challenge := issuer.challenge
	issuer.mu.Unlock()
	verifierDigest := sha256.Sum256([]byte(form.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(verifierDigest[:]) != challenge || form.Get("grant_type") != "authorization_code" || form.Get("client_id") != issuer.issuedClient || form.Get("redirect_uri") != query.Get("redirect_uri") || form.Get("resource") != account.BaseURL || form.Get("code") != "synthetic-code" {
		t.Fatalf("bad token form: %v", form)
	}
	if form.Get("client_secret") != "" {
		t.Fatal("public client sent a client secret")
	}
	public, _ := json.Marshal([]any{flow, status, authorization})
	if strings.Contains(string(public), "FAKE-SIWC") || strings.Contains(string(public), form.Get("code_verifier")) || strings.Contains(string(public), query.Get("nonce")) {
		t.Fatalf("secret material serialized: %s", public)
	}

	replay, replayErr := http.Get(callback)
	if replayErr == nil {
		replay.Body.Close()
		if replay.StatusCode == http.StatusOK {
			t.Fatal("callback replay reported success")
		}
	}
	if issuer.tokenCalls.Load() != 1 {
		t.Fatalf("callback replay reached exchange: calls=%d", issuer.tokenCalls.Load())
	}
}

func TestSIWCSuccessfulCallbackFlushesResponseThenClosesAcceptedSocket(t *testing.T) {
	service, _, _, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	flow, err := siwc.Begin(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	query := openHandoff(t, flow)
	issuer.mu.Lock()
	issuer.nonce = query.Get("nonce")
	issuer.mu.Unlock()
	callback, err := url.Parse(query.Get("redirect_uri"))
	if err != nil {
		t.Fatal(err)
	}
	callback.RawQuery = url.Values{
		"code":      {"synthetic-code"},
		"state":     {query.Get("state")},
		"client_id": {issuer.issuedClient},
		"scope":     {strings.Join(issuer.scopes, " ")},
	}.Encode()
	connection, err := net.Dial("tcp4", callback.Host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintf(connection, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: keep-alive\r\n\r\n", callback.RequestURI(), callback.Host); err != nil {
		connection.Close()
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodGet})
	if err != nil {
		connection.Close()
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || string(body) != "Authorization complete. You may close this window." {
		connection.Close()
		t.Fatalf("completion response: status=%d body=%q err=%v", response.StatusCode, body, readErr)
	}
	requireSIWCSocketClosed(t, connection)
}

func TestSIWCRejectsExactTokenReflectionBeforeMetadataCommit(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*siwcIssuerFixture)
		secret string
	}{
		{name: "subject", mutate: func(f *siwcIssuerFixture) { f.subject = f.accessToken }, secret: "FAKE-SIWC-ACCESS"},
		{name: "client", mutate: func(f *siwcIssuerFixture) { f.issuedClient = f.refreshToken }, secret: "FAKE-SIWC-REFRESH"},
		{name: "scope", mutate: func(f *siwcIssuerFixture) { f.accessToken = "openid" }, secret: "openid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, db, secrets, path := setupAccounts(t)
			resourceServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			defer resourceServer.Close()
			account := newSIWCAccount(t, service, resourceServer.URL+"/v1")
			issuer := newSIWCIssuerFixture(t, account.BaseURL)
			test.mutate(issuer)
			siwc, err := NewSIWCSimulation(service, issuer.options())
			if err != nil {
				t.Fatal(err)
			}
			flow, err := siwc.Begin(context.Background(), account.ID, account.Revision)
			if err != nil {
				t.Fatal(err)
			}
			query := openHandoff(t, flow)
			issuer.mu.Lock()
			issuer.nonce = query.Get("nonce")
			issuer.mu.Unlock()
			callback := query.Get("redirect_uri") + "?code=synthetic-code&state=" + url.QueryEscape(query.Get("state")) + "&client_id=" + url.QueryEscape(issuer.issuedClient) + "&scope=" + url.QueryEscape(strings.Join(issuer.scopes, " "))
			response, requestErr := http.Get(callback)
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				t.Fatal("token-reflecting identity was accepted")
			}
			status, statusErr := siwc.Status(flow.OperationID)
			if statusErr != nil || status.State != "failed" || status.HandoffURL != "" {
				t.Fatalf("unsafe failure status: %+v %v", status, statusErr)
			}
			if _, currentErr := db.CurrentModelAuthorization(account.ID); currentErr == nil {
				t.Fatal("token-reflecting metadata was committed")
			}
			if secrets.sets != 0 {
				t.Fatalf("token bundle was written before reflection rejection: %d", secrets.sets)
			}
			public, _ := json.Marshal(status)
			if strings.Contains(string(public), test.secret) {
				t.Fatalf("public flow reflected token: %s", public)
			}
			for _, suffix := range []string{"", "-wal"} {
				raw, readErr := os.ReadFile(path + suffix)
				if readErr != nil && !os.IsNotExist(readErr) {
					t.Fatal(readErr)
				}
				if strings.Contains(string(raw), test.secret) {
					t.Fatalf("token reflected into SQLite%s", suffix)
				}
			}
		})
	}

	binding := domain.ModelAuthorizationBinding{Issuer: "issuer", Subject: "subject", ClientID: "client", Scopes: []string{"scope"}}
	for _, token := range []siwcTokenResponse{
		{AccessToken: "subject", RefreshToken: "refresh", IDToken: "id"},
		{AccessToken: "access", RefreshToken: "client", IDToken: "id"},
		{AccessToken: "access", RefreshToken: "refresh", IDToken: "scope"},
	} {
		if validateSIWCMetadataNoTokenReflection(binding, token) == nil {
			t.Fatal("exact token reflection accepted")
		}
	}
}

func TestSIWCCallbackRequiresExactSingleUseParametersAndConsumesDenial(t *testing.T) {
	service, _, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	flow, err := siwc.Begin(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	query := openHandoff(t, flow)
	callback := query.Get("redirect_uri")
	state := url.QueryEscape(query.Get("state"))
	wrongHostRequest, _ := http.NewRequest(http.MethodGet, callback+"?code=one&state="+state+"&client_id="+issuer.issuedClient, nil)
	wrongHostRequest.Host = "localhost"
	wrongHostResponse, err := http.DefaultClient.Do(wrongHostRequest)
	if err != nil {
		t.Fatal(err)
	}
	wrongHostResponse.Body.Close()
	if wrongHostResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong callback Host status=%d", wrongHostResponse.StatusCode)
	}

	invalid := []string{
		"?code=one&state=" + state + "&state=" + state + "&client_id=" + issuer.issuedClient,
		"?code=one&state=wrong&client_id=" + issuer.issuedClient,
		"?code=one&state=" + state + "&client_id=" + issuer.issuedClient + "&unknown=value",
		"?code=one&error=access_denied&state=" + state + "&client_id=" + issuer.issuedClient,
	}
	for _, suffix := range invalid {
		response, requestErr := http.Get(callback + suffix)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid callback %q status=%d", suffix, response.StatusCode)
		}
		status, statusErr := siwc.Status(flow.OperationID)
		if statusErr != nil || status.State != "waiting" {
			t.Fatalf("invalid callback consumed flow: %+v %v", status, statusErr)
		}
	}

	response, err := http.Get(callback + "?error=access_denied&error_description=" + url.QueryEscape("FAKE-SIWC-ACCESS") + "&state=" + state)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("denial status=%d", response.StatusCode)
	}
	status, err := siwc.Status(flow.OperationID)
	if err != nil || status.State != "declined" || status.HandoffURL != "" || issuer.tokenCalls.Load() != 0 || secrets.sets != 0 {
		t.Fatalf("unsafe denial: %+v err=%v token_calls=%d secret_sets=%d", status, err, issuer.tokenCalls.Load(), secrets.sets)
	}
	public, _ := json.Marshal(status)
	if strings.Contains(string(public), "FAKE-SIWC-ACCESS") {
		t.Fatalf("error description reached public state: %s", public)
	}
}

func TestSIWCIDTokenRejectsEveryPinnedIdentityBoundary(t *testing.T) {
	service, _, _, _ := setupAccounts(t)
	issuer := newSIWCIssuerFixture(t, "http://127.0.0.1:1/v1")
	siwc, err := NewSIWCSimulation(service, issuer.options())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	validHeader := map[string]any{"alg": "RS256", "kid": "key-1", "typ": "JWT"}
	validClaims := map[string]any{"iss": issuer.server.URL, "aud": "issued-client", "sub": "subject", "nonce": "nonce", "exp": now.Add(time.Hour).Unix(), "nbf": now.Add(-time.Minute).Unix()}
	clone := func(source map[string]any) map[string]any {
		copy := make(map[string]any, len(source))
		for key, value := range source {
			copy[key] = value
		}
		return copy
	}
	tests := []struct {
		name   string
		header map[string]any
		claims map[string]any
		mutate func(string) string
		client string
		nonce  string
	}{
		{name: "issuer", header: validHeader, claims: func() map[string]any { v := clone(validClaims); v["iss"] = "http://127.0.0.1:1"; return v }(), client: "issued-client", nonce: "nonce"},
		{name: "audience", header: validHeader, claims: validClaims, client: "different-client", nonce: "nonce"},
		{name: "nonce", header: validHeader, claims: validClaims, client: "issued-client", nonce: "different-nonce"},
		{name: "expiry", header: validHeader, claims: func() map[string]any { v := clone(validClaims); v["exp"] = now.Add(-time.Minute).Unix(); return v }(), client: "issued-client", nonce: "nonce"},
		{name: "not-before", header: validHeader, claims: func() map[string]any { v := clone(validClaims); v["nbf"] = now.Add(time.Hour).Unix(); return v }(), client: "issued-client", nonce: "nonce"},
		{name: "subject", header: validHeader, claims: func() map[string]any { v := clone(validClaims); v["sub"] = ""; return v }(), client: "issued-client", nonce: "nonce"},
		{name: "algorithm", header: map[string]any{"alg": "none", "kid": "key-1", "typ": "JWT"}, claims: validClaims, client: "issued-client", nonce: "nonce"},
		{name: "key", header: map[string]any{"alg": "RS256", "kid": "other", "typ": "JWT"}, claims: validClaims, client: "issued-client", nonce: "nonce"},
		{name: "signature", header: validHeader, claims: validClaims, mutate: func(token string) string {
			parts := strings.Split(token, ".")
			signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
			signature[0] ^= 0xff
			parts[2] = base64.RawURLEncoding.EncodeToString(signature)
			return strings.Join(parts, ".")
		}, client: "issued-client", nonce: "nonce"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token := issuer.signedToken(test.header, test.claims)
			if test.mutate != nil {
				token = test.mutate(token)
			}
			if _, err := siwc.validateSIWCIDToken(context.Background(), issuer.server.URL+"/jwks", token, test.client, test.nonce); err == nil {
				t.Fatal("invalid signed identity accepted")
			}
		})
	}
	valid := issuer.signedToken(validHeader, validClaims)
	claims, err := siwc.validateSIWCIDToken(context.Background(), issuer.server.URL+"/jwks", valid, "issued-client", "nonce")
	if err != nil || claims.Subject != "subject" {
		t.Fatalf("valid signed identity rejected: %+v %v", claims, err)
	}
}

func TestSIWCTokenRedirectIsNotFollowed(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	issuer.tokenRedirectURL = target.URL + "/capture"
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	flow, err := siwc.Begin(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	query := openHandoff(t, flow)
	callback := query.Get("redirect_uri") + "?code=synthetic-code&state=" + url.QueryEscape(query.Get("state")) + "&client_id=" + issuer.issuedClient + "&scope=" + url.QueryEscape(strings.Join(issuer.scopes, " "))
	response, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK || redirected.Load() != 0 || secrets.sets != 0 {
		t.Fatalf("token redirect escaped pin: status=%d redirected=%d sets=%d", response.StatusCode, redirected.Load(), secrets.sets)
	}
	if _, err = db.CurrentModelAuthorization(account.ID); err == nil {
		t.Fatal("redirected token exchange committed authorization")
	}
}

func TestSIWCIdentityOnlyGrantPersistsButCannotLease(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	issuer.scopes = []string{"openid", "profile", "email"}
	issuer.refreshToken = ""
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	authorization, err := db.CurrentModelAuthorization(account.ID)
	if err != nil || authorization.State != "available" || !reflect.DeepEqual(authorization.Binding.Scopes, issuer.scopes) {
		t.Fatalf("identity-only authorization not persisted: %+v %v", authorization, err)
	}
	route, err := db.CreateModelRoute(domain.ModelRoute{ID: "route-identity-only", AccountID: account.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: account.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	selection := domain.RoleModelSelection{Profile: domain.Profile{ID: "profile", AccountID: account.ID, AccountRevision: account.Revision, RouteID: route.ID, Protocol: route.Protocol, BaseURL: route.BaseURL, Model: "synthetic"}, Account: &account, Route: &route, Authorization: &authorization.Binding}
	gets := secrets.gets
	if _, err = service.ResolveModelCredential(context.Background(), selection); err == nil {
		t.Fatal("identity-only grant leased an inference token")
	}
	if secrets.gets != gets {
		t.Fatal("entitlement denial occurred after secret read")
	}
	tokenCalls := issuer.tokenCalls.Load()
	if _, err = siwc.Refresh(context.Background(), authorization.Binding.ID, account.Revision); err == nil {
		t.Fatal("identity-only authorization without offline_access attempted refresh")
	}
	current, currentErr := db.CurrentModelAuthorization(account.ID)
	if currentErr != nil || current.State != "available" || current.Binding.ID != authorization.Binding.ID || issuer.tokenCalls.Load() != tokenCalls {
		t.Fatalf("identity-only refresh changed state or reached provider: %+v %v calls=%d", current, currentErr, issuer.tokenCalls.Load()-tokenCalls)
	}
}

func TestSIWCReauthorizationReusesClientAndHostButCreatesFreshBinding(t *testing.T) {
	service, db, _, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	_, initialQuery := completeInitialSIWCSignIn(t, siwc, account, issuer)
	initial, err := db.CurrentModelAuthorization(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	flow, err := siwc.Begin(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	query := openHandoff(t, flow)
	if query.Get("client_id") != initial.Binding.ClientID || query.Has("agent_name_hint") || query.Get("ext_agent_host_id") != initialQuery.Get("ext_agent_host_id") {
		t.Fatalf("reauth changed stable metadata or retained initial hint: %v", query)
	}
	issuer.mu.Lock()
	issuer.nonce = query.Get("nonce")
	issuer.mu.Unlock()
	callback := query.Get("redirect_uri") + "?code=reauth-code&state=" + url.QueryEscape(query.Get("state")) + "&scope=" + url.QueryEscape(strings.Join(issuer.scopes, " "))
	response, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reauth callback status=%d", response.StatusCode)
	}
	current, err := db.CurrentModelAuthorization(account.ID)
	if err != nil || current.Binding.ID == initial.Binding.ID || current.Binding.ClientID != initial.Binding.ClientID || current.Binding.Subject != initial.Binding.Subject {
		t.Fatalf("bad reauth binding: initial=%+v current=%+v err=%v", initial.Binding, current.Binding, err)
	}
}

func TestSIWCReauthorizationRejectsDifferentSubjectBeforeSecretWrite(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	initial, _ := db.CurrentModelAuthorization(account.ID)
	sets := secrets.sets
	issuer.subject = "different-subject"
	flow, err := siwc.Begin(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	query := openHandoff(t, flow)
	issuer.mu.Lock()
	issuer.nonce = query.Get("nonce")
	issuer.mu.Unlock()
	callback := query.Get("redirect_uri") + "?code=reauth-code&state=" + url.QueryEscape(query.Get("state")) + "&scope=" + url.QueryEscape(strings.Join(issuer.scopes, " "))
	response, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	current, currentErr := db.CurrentModelAuthorization(account.ID)
	if response.StatusCode == http.StatusOK || currentErr != nil || current.Binding.ID != initial.Binding.ID || secrets.sets != sets {
		t.Fatalf("different subject changed binding: status=%d current=%+v err=%v sets=%d", response.StatusCode, current.Binding, currentErr, secrets.sets-sets)
	}
}

func TestSIWCScopeValidationRejectsExpansionDuplicatesAndCallbackMismatch(t *testing.T) {
	if _, err := validateSIWCGrant("openid profile email unknown", "", nil); err == nil {
		t.Fatal("scope expansion accepted")
	}
	if _, err := validateSIWCGrant("openid profile email email", "", nil); err == nil {
		t.Fatal("duplicate scope accepted")
	}
	if _, err := validateSIWCGrant("openid profile email resource.invoke", "openid profile email", nil); err == nil {
		t.Fatal("callback/token grant mismatch accepted")
	}
	if _, err := validateSIWCGrant("openid profile email resource.invoke", "", []string{"openid", "profile", "email"}); err == nil {
		t.Fatal("refresh scope expansion accepted")
	}
}

func TestSIWCAccountDisableDuringExchangePreventsCommit(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	issuer.onToken = func(url.Values) {
		latest, err := db.GetModelAccount(account.ID)
		if err != nil {
			t.Error(err)
			return
		}
		latest.Enabled = false
		if _, err = service.Save(latest, latest.Revision); err != nil {
			t.Error(err)
		}
	}
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	flow, err := siwc.Begin(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	query := openHandoff(t, flow)
	issuer.mu.Lock()
	issuer.nonce = query.Get("nonce")
	issuer.mu.Unlock()
	callback := query.Get("redirect_uri") + "?code=synthetic-code&state=" + url.QueryEscape(query.Get("state")) + "&client_id=" + issuer.issuedClient + "&scope=" + url.QueryEscape(strings.Join(issuer.scopes, " "))
	response, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK || secrets.sets != 0 {
		t.Fatalf("disabled account committed authorization: status=%d sets=%d", response.StatusCode, secrets.sets)
	}
	if _, err = db.CurrentModelAuthorization(account.ID); err == nil {
		t.Fatal("disabled account obtained current authorization")
	}
}

func TestSIWCRefreshUsesPersistedIntentExactBundleAndFrozenScopeOrder(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resourceServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resourceServer.Close()
	account := newSIWCAccount(t, service, resourceServer.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, err := NewSIWCSimulation(service, issuer.options())
	if err != nil {
		t.Fatal(err)
	}
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	first, err := db.CurrentModelAuthorization(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	issuer.mu.Lock()
	issuer.scopes = []string{"chatgpt.tokens.use.direct", "resource.invoke", "offline_access", "email", "profile", "openid"}
	issuer.nonce = ""
	issuer.mu.Unlock()
	refreshed, err := siwc.Refresh(context.Background(), first.Binding.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Generation != first.Generation+1 || refreshed.State != "available" || !reflect.DeepEqual(refreshed.Binding.Scopes, first.Binding.Scopes) {
		t.Fatalf("bad refresh: %+v", refreshed)
	}
	issuer.mu.Lock()
	form := issuer.lastToken
	issuer.mu.Unlock()
	if form.Get("grant_type") != "refresh_token" || form.Get("client_id") != first.Binding.ClientID || form.Get("refresh_token") != "FAKE-SIWC-REFRESH" || form.Get("resource") != account.BaseURL || form.Has("scope") || form.Has("code") || form.Has("code_verifier") {
		t.Fatalf("bad refresh form: %v", form)
	}
	raw := secrets.values[refreshed.SecretRef]
	var bundle storedModelTokenBundle
	if json.Unmarshal([]byte(raw), &bundle) != nil || bundle.Resource != account.BaseURL || bundle.EarliestRefreshAt == "" || bundle.IDToken == "" {
		t.Fatalf("bad refreshed bundle")
	}
}

func TestSIWCRefreshBeforeEarliestIsNotSentAndLeavesAuthorizationAvailable(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	issuer.earliestRefreshAt = time.Now().Add(time.Hour).Unix()
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	authorization, _ := db.CurrentModelAuthorization(account.ID)
	calls := issuer.tokenCalls.Load()
	_, err := siwc.Refresh(context.Background(), authorization.Binding.ID, account.Revision)
	var provider *model.ProviderError
	if !errors.As(err, &provider) || provider.Code != "refresh_not_ready" || provider.Certainty != "not_sent" {
		t.Fatalf("early refresh classification: %#v %v", provider, err)
	}
	if issuer.tokenCalls.Load() != calls {
		t.Fatal("early refresh reached provider")
	}
	current, currentErr := db.CurrentModelAuthorization(account.ID)
	if currentErr != nil || current.State != "available" || current.Generation != authorization.Generation || current.SecretRef != authorization.SecretRef {
		t.Fatalf("early refresh changed authorization: %+v %v", current, currentErr)
	}
	if secrets.values[authorization.SecretRef] == "" {
		t.Fatal("early refresh erased current token bundle")
	}
}

func TestSIWCRefreshSerializesAcrossServices(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	first, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, first, account, issuer)
	authorization, _ := db.CurrentModelAuthorization(account.ID)
	issuer.mu.Lock()
	issuer.nonce = ""
	issuer.mu.Unlock()

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	issuer.onToken = func(form url.Values) {
		if form.Get("grant_type") == "refresh_token" {
			once.Do(func() { close(entered) })
			<-release
		}
	}
	second, _ := NewSIWCSimulation(New(db, secrets), issuer.options())
	firstResult := make(chan error, 1)
	go func() {
		_, err := first.Refresh(context.Background(), authorization.Binding.ID, account.Revision)
		firstResult <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first refresh did not reach token service")
	}
	if _, err := second.Refresh(context.Background(), authorization.Binding.ID, account.Revision); err == nil {
		t.Fatal("concurrent refresh was accepted")
	}
	if issuer.tokenCalls.Load() != 2 { // initial sign-in plus one refresh
		t.Fatalf("concurrent refresh reached token service: calls=%d", issuer.tokenCalls.Load())
	}
	close(release)
	if err := <-firstResult; err != nil {
		t.Fatalf("serialized refresh failed: %v", err)
	}
}

func TestSIWCLostRefreshResponseDeniesAndDoesNotReplayOrEraseOldBundle(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	authorization, _ := db.CurrentModelAuthorization(account.ID)
	oldSecretRef := authorization.SecretRef
	oldBundle := secrets.values[oldSecretRef]
	issuer.dropToken = true

	if _, err := siwc.Refresh(context.Background(), authorization.Binding.ID, account.Revision); err == nil {
		t.Fatal("dropped refresh response reported success")
	}
	unknown, err := db.GetModelAuthorization(authorization.Binding.ID)
	if err != nil || unknown.State != "refresh_unknown" || unknown.SecretRef != oldSecretRef || secrets.values[oldSecretRef] != oldBundle {
		t.Fatalf("lost response did not retain denied prior bundle: %+v %v", unknown, err)
	}
	calls := issuer.tokenCalls.Load()
	if _, err = siwc.Refresh(context.Background(), authorization.Binding.ID, account.Revision); err == nil {
		t.Fatal("unknown refresh state was automatically retried")
	}
	if issuer.tokenCalls.Load() != calls {
		t.Fatal("unknown refresh replayed provider request")
	}
	route, err := db.CreateModelRoute(domain.ModelRoute{ID: "route-refresh-unknown", AccountID: account.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: account.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	selection := domain.RoleModelSelection{Profile: domain.Profile{ID: "profile", AccountID: account.ID, AccountRevision: account.Revision, RouteID: route.ID, Protocol: route.Protocol, BaseURL: route.BaseURL, Model: "synthetic"}, Account: &account, Route: &route, Authorization: &authorization.Binding}
	gets := secrets.gets
	if _, err = service.ResolveModelCredential(context.Background(), selection); err == nil || secrets.gets != gets {
		t.Fatalf("unknown refresh leased/read retained bundle: err=%v gets=%d", err, secrets.gets-gets)
	}
}

func TestSIWCRefreshPreservesTerminalInvalidClientCode(t *testing.T) {
	service, db, _, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	authorization, _ := db.CurrentModelAuthorization(account.ID)
	issuer.tokenStatus = http.StatusBadRequest
	issuer.tokenError = "invalid_client"
	_, err := siwc.Refresh(context.Background(), authorization.Binding.ID, account.Revision)
	var provider *model.ProviderError
	if !errors.As(err, &provider) || provider.ProviderCode != "invalid_client" || provider.Code != "auth_invalid" || provider.Certainty != "rejected" {
		t.Fatalf("terminal refresh code lost: %#v %v", provider, err)
	}
}

func TestSIWCTerminalRefreshErrorsRevokeTokensWithoutIdentityOrNetworkReplay(t *testing.T) {
	terminalCodes := []string{
		"invalid_grant",
		"invalid_refresh_token",
		"token_expired",
		"refresh_token_expired",
		"refresh_token_invalidated",
		"refresh_token_reused",
	}
	for _, code := range terminalCodes {
		t.Run(code, func(t *testing.T) {
			service, db, secrets, _ := setupAccounts(t)
			resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			defer resource.Close()
			account := newSIWCAccount(t, service, resource.URL+"/v1")
			issuer := newSIWCIssuerFixture(t, account.BaseURL)
			siwc, _ := NewSIWCSimulation(service, issuer.options())
			completeInitialSIWCSignIn(t, siwc, account, issuer)
			authorization, _ := db.CurrentModelAuthorization(account.ID)
			hostID, err := db.GetOrCreateSIWCHostID()
			if err != nil {
				t.Fatal(err)
			}
			issuer.tokenStatus = http.StatusBadRequest
			issuer.tokenError = code
			calls := issuer.tokenCalls.Load()
			_, err = siwc.Refresh(context.Background(), authorization.Binding.ID, account.Revision)
			var provider *model.ProviderError
			if !errors.As(err, &provider) || provider.HTTPStatus != http.StatusBadRequest || provider.ProviderCode != code {
				t.Fatalf("terminal provider error lost: %#v %v", provider, err)
			}
			stored, storedErr := db.GetModelAuthorization(authorization.Binding.ID)
			if storedErr != nil || stored.State != "revoked" || !reflect.DeepEqual(stored.Binding, authorization.Binding) {
				t.Fatalf("terminal refresh did not preserve revoked identity: %+v %v", stored, storedErr)
			}
			if _, exists := secrets.values[authorization.SecretRef]; exists {
				t.Fatal("confirmed-dead refresh bundle survived")
			}
			if nextHostID, hostErr := db.GetOrCreateSIWCHostID(); hostErr != nil || nextHostID != hostID {
				t.Fatalf("terminal refresh changed installation identity: %q %v", nextHostID, hostErr)
			}
			if _, err = siwc.Refresh(context.Background(), authorization.Binding.ID, account.Revision); err == nil || issuer.tokenCalls.Load() != calls+1 {
				t.Fatalf("terminal refresh replayed provider: err=%v calls=%d", err, issuer.tokenCalls.Load()-calls)
			}
		})
	}
}

func TestSIWCUncertainAndInvalidClientRefreshRetainBundleButDenyRetry(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		code   string
	}{
		{name: "invalid_client", status: http.StatusBadRequest, code: "invalid_client"},
		{name: "server_error", status: http.StatusServiceUnavailable, code: "temporarily_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, db, secrets, _ := setupAccounts(t)
			resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			defer resource.Close()
			account := newSIWCAccount(t, service, resource.URL+"/v1")
			issuer := newSIWCIssuerFixture(t, account.BaseURL)
			siwc, _ := NewSIWCSimulation(service, issuer.options())
			completeInitialSIWCSignIn(t, siwc, account, issuer)
			authorization, _ := db.CurrentModelAuthorization(account.ID)
			oldBundle := secrets.values[authorization.SecretRef]
			issuer.tokenStatus = test.status
			issuer.tokenError = test.code
			calls := issuer.tokenCalls.Load()
			if _, err := siwc.Refresh(context.Background(), authorization.Binding.ID, account.Revision); err == nil {
				t.Fatal("failed refresh reported success")
			}
			stored, err := db.GetModelAuthorization(authorization.Binding.ID)
			if err != nil || stored.State != "refresh_unknown" || stored.SecretRef != authorization.SecretRef || secrets.values[authorization.SecretRef] != oldBundle {
				t.Fatalf("uncertain refresh did not retain denied bundle: %+v %v", stored, err)
			}
			if _, err = siwc.Refresh(context.Background(), authorization.Binding.ID, account.Revision); err == nil || issuer.tokenCalls.Load() != calls+1 {
				t.Fatalf("uncertain refresh replayed provider: err=%v calls=%d", err, issuer.tokenCalls.Load()-calls)
			}
		})
	}
}

func TestSIWCRevokeDeniesLocallyBeforePinnedRemoteAttemptAndRecordsOutcome(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resourceServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resourceServer.Close()
	account := newSIWCAccount(t, service, resourceServer.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, err := NewSIWCSimulation(service, issuer.options())
	if err != nil {
		t.Fatal(err)
	}
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	authorization, err := db.CurrentModelAuthorization(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	route, err := db.CreateModelRoute(domain.ModelRoute{ID: "route-revoke", AccountID: account.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: account.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	selection := domain.RoleModelSelection{Profile: domain.Profile{ID: "profile", AccountID: account.ID, AccountRevision: account.Revision, RouteID: route.ID, Protocol: route.Protocol, BaseURL: route.BaseURL, Model: "synthetic"}, Account: &account, Route: &route, Authorization: &authorization.Binding}
	if _, err = service.ResolveModelCredential(context.Background(), selection); err != nil {
		t.Fatal(err)
	}
	issuer.onRevoke = func() {
		if _, leaseErr := service.ResolveModelCredential(context.Background(), selection); leaseErr == nil {
			t.Error("credential lease remained available during remote revoke")
		}
	}
	result, err := siwc.Revoke(context.Background(), authorization.Binding.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if result.LocalState != "revoked" || result.RemoteState != "confirmed" || result.UpdatedAt == "" || issuer.revokeCalls.Load() != 1 {
		t.Fatalf("bad revoke result: %+v calls=%d", result, issuer.revokeCalls.Load())
	}
	issuer.mu.Lock()
	form := issuer.lastRevoke
	issuer.mu.Unlock()
	if form.Get("token") != "FAKE-SIWC-REFRESH" || form.Get("token_type_hint") != "refresh_token" || form.Get("client_id") != authorization.Binding.ClientID || len(form) != 3 {
		t.Fatalf("bad revoke form: %v", form)
	}
	stored, err := db.GetModelAuthorization(authorization.Binding.ID)
	if err != nil || stored.State != "revoked" {
		t.Fatalf("local revoke missing: %+v %v", stored, err)
	}
	receipt, err := db.GetSIWCRemoteRevocation(authorization.Binding.ID)
	if err != nil || receipt.Outcome != "confirmed" {
		t.Fatalf("remote receipt missing: %+v %v", receipt, err)
	}
	if _, exists := secrets.values[authorization.SecretRef]; exists {
		t.Fatal("revoked token bundle survived local cleanup")
	}
	if _, err = siwc.Revoke(context.Background(), authorization.Binding.ID, account.Revision); err != nil || issuer.revokeCalls.Load() != 1 {
		t.Fatalf("revoke replayed remote request: %v calls=%d", err, issuer.revokeCalls.Load())
	}
}

func TestSIWCConcurrentRevokeAcrossServicesElectsOneRemoteSender(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	first, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, first, account, issuer)
	authorization, _ := db.CurrentModelAuthorization(account.ID)
	second, _ := NewSIWCSimulation(New(db, secrets), issuer.options())

	remoteEntered := make(chan struct{})
	releaseRemote := make(chan struct{})
	var entered sync.Once
	issuer.onRevoke = func() {
		entered.Do(func() { close(remoteEntered) })
		<-releaseRemote
	}
	start := make(chan struct{})
	results := make(chan SIWCRevokeResult, 2)
	errorsSeen := make(chan error, 2)
	for _, candidate := range []*SIWCService{first, second} {
		go func(candidate *SIWCService) {
			<-start
			result, err := candidate.Revoke(context.Background(), authorization.Binding.ID, account.Revision)
			results <- result
			errorsSeen <- err
		}(candidate)
	}
	close(start)
	select {
	case <-remoteEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("remote revoke was not attempted")
	}
	select {
	case loser := <-results:
		if loser.LocalState != "revoked" || loser.RemoteState != "unconfirmed" {
			t.Fatalf("loser did not return durable unconfirmed state: %+v", loser)
		}
	case <-time.After(500 * time.Millisecond):
		close(releaseRemote)
		t.Fatalf("second service also blocked in remote revoke; calls=%d", issuer.revokeCalls.Load())
	}
	close(releaseRemote)
	winner := <-results
	if winner.LocalState != "revoked" || winner.RemoteState != "confirmed" {
		t.Fatalf("winner result: %+v", winner)
	}
	for range 2 {
		if err := <-errorsSeen; err != nil {
			t.Fatal(err)
		}
	}
	if issuer.revokeCalls.Load() != 1 {
		t.Fatalf("remote revoke calls=%d", issuer.revokeCalls.Load())
	}
	receipt, err := db.GetSIWCRemoteRevocation(authorization.Binding.ID)
	if err != nil || receipt.Outcome != "confirmed" {
		t.Fatalf("final remote receipt: %+v %v", receipt, err)
	}
}

func TestSIWCRevokeFailureRemainsLocallyDeniedAndDurablyUnconfirmed(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	authorization, _ := db.CurrentModelAuthorization(account.ID)
	issuer.revokeStatus = http.StatusServiceUnavailable
	result, err := siwc.Revoke(context.Background(), authorization.Binding.ID, account.Revision)
	if err == nil || result.LocalState != "revoked" || result.RemoteState != "unconfirmed" {
		t.Fatalf("bad uncertain revoke result: %+v %v", result, err)
	}
	stored, _ := db.GetModelAuthorization(authorization.Binding.ID)
	receipt, receiptErr := db.GetSIWCRemoteRevocation(authorization.Binding.ID)
	if stored.State != "revoked" || receiptErr != nil || receipt.Outcome != "unconfirmed" {
		t.Fatalf("uncertain revoke not durable: auth=%+v receipt=%+v err=%v", stored, receipt, receiptErr)
	}
	if _, exists := secrets.values[authorization.SecretRef]; exists {
		t.Fatal("uncertain remote result kept locally callable token")
	}
	calls := issuer.revokeCalls.Load()
	if replay, replayErr := siwc.Revoke(context.Background(), authorization.Binding.ID, account.Revision); replayErr != nil || replay.RemoteState != "unconfirmed" || issuer.revokeCalls.Load() != calls {
		t.Fatalf("recovery replayed uncertain remote revoke: %+v %v calls=%d", replay, replayErr, issuer.revokeCalls.Load())
	}
}

func TestSIWCRevokeRejectsUnpinnedDiscoveredEndpointWithoutRequest(t *testing.T) {
	service, db, _, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	authorization, _ := db.CurrentModelAuthorization(account.ID)
	var externalCalls atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { externalCalls.Add(1) }))
	defer external.Close()
	issuer.revocationURL = external.URL + "/revoke"
	result, err := siwc.Revoke(context.Background(), authorization.Binding.ID, account.Revision)
	if err == nil || result.LocalState != "revoked" || result.RemoteState != "unconfirmed" || externalCalls.Load() != 0 {
		t.Fatalf("unpinned revoke endpoint used: result=%+v err=%v calls=%d", result, err, externalCalls.Load())
	}
}

func TestSIWCRevokedWithoutReceiptBecomesUnconfirmedWithoutRemoteReplay(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	authorization, _ := db.CurrentModelAuthorization(account.ID)
	if _, err := db.RevokeModelAuthorization(authorization.Binding.ID); err != nil {
		t.Fatal(err)
	}
	result, err := siwc.Revoke(context.Background(), authorization.Binding.ID, account.Revision)
	if err != nil || result.LocalState != "revoked" || result.RemoteState != "unconfirmed" || issuer.revokeCalls.Load() != 0 {
		t.Fatalf("missing receipt recovery: %+v %v remote_calls=%d", result, err, issuer.revokeCalls.Load())
	}
	if _, exists := secrets.values[authorization.SecretRef]; exists {
		t.Fatal("missing receipt recovery left revoked bundle behind")
	}
	receipt, err := db.GetSIWCRemoteRevocation(authorization.Binding.ID)
	if err != nil || receipt.Outcome != "unconfirmed" {
		t.Fatalf("missing receipt not recorded: %+v %v", receipt, err)
	}
}

func TestSIWCProductionRevokeIsLocalOnlyAndRemoteUnconfirmed(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resourceServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resourceServer.Close()
	account := newSIWCAccount(t, service, resourceServer.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	simulation, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, simulation, account, issuer)
	authorization, _ := db.CurrentModelAuthorization(account.ID)
	gets := secrets.gets
	production := NewSIWCService(service)
	result, err := production.Revoke(context.Background(), authorization.Binding.ID, account.Revision)
	var provider *model.ProviderError
	if !errors.As(err, &provider) || provider.Code != "live_provider_access_pending_confirmation" || result.LocalState != "revoked" || result.RemoteState != "unconfirmed" {
		t.Fatalf("bad production revoke: %+v %v", result, err)
	}
	if secrets.gets != gets || issuer.revokeCalls.Load() != 0 {
		t.Fatalf("production remote revoke read token or used network: gets=%d calls=%d", secrets.gets-gets, issuer.revokeCalls.Load())
	}
	stored, _ := db.GetModelAuthorization(authorization.Binding.ID)
	if stored.State != "revoked" {
		t.Fatal("production gate skipped local revoke")
	}
}

func TestSIWCProductionRevokePreservesCommittedLocalDenialWhenCleanupFails(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	resourceServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resourceServer.Close()
	account := newSIWCAccount(t, service, resourceServer.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	simulation, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, simulation, account, issuer)
	authorization, _ := db.CurrentModelAuthorization(account.ID)
	route, err := db.CreateModelRoute(domain.ModelRoute{ID: "route-production-cleanup-failure", AccountID: account.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: account.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	selection := domain.RoleModelSelection{Profile: domain.Profile{ID: "profile", AccountID: account.ID, AccountRevision: account.Revision, RouteID: route.ID, Protocol: route.Protocol, BaseURL: route.BaseURL, Model: "synthetic"}, Account: &account, Route: &route, Authorization: &authorization.Binding}
	secrets.deleteErr = errors.New("private cleanup failure")
	remoteCalls := issuer.revokeCalls.Load()

	result, err := NewSIWCService(service).Revoke(context.Background(), authorization.Binding.ID, account.Revision)
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe cleanup error: %v", err)
	}
	if result.AuthorizationID != authorization.Binding.ID || result.LocalState != "revoked" || result.RemoteState != "unconfirmed" || result.UpdatedAt != "" {
		t.Fatalf("committed local denial was lost: %+v", result)
	}
	stored, readErr := db.GetModelAuthorization(authorization.Binding.ID)
	if readErr != nil || stored.State != "revoked" {
		t.Fatalf("local denial not durable: %+v %v", stored, readErr)
	}
	if _, leaseErr := service.ResolveModelCredential(context.Background(), selection); leaseErr == nil {
		t.Fatal("revoked authorization remained runnable")
	}
	pending, pendingErr := db.PendingModelAuthorizationCleanup()
	if pendingErr != nil || len(pending) != 1 || pending[0].AuthorizationID != authorization.Binding.ID {
		t.Fatalf("cleanup intent not retained: %+v %v", pending, pendingErr)
	}
	if issuer.revokeCalls.Load() != remoteCalls {
		t.Fatalf("production cleanup failure reached provider: before=%d after=%d", remoteCalls, issuer.revokeCalls.Load())
	}
}
