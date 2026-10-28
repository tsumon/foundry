package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"foundry/internal/accounts"
	"foundry/internal/domain"
)

const (
	acceptanceFixturePath     = "/acceptance-fixture/"
	acceptanceFixtureAuthPath = "/acceptance-fixture/auth/start"
)

var fixtureRequestedScopes = []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}
var fixtureIdentityScopes = []string{"openid", "profile", "email"}

type ownedLoopbackServer struct {
	origin   string
	listener net.Listener
	server   *http.Server
}

func acceptanceAddressAllowed(address string) bool {
	host, port, err := net.SplitHostPort(address)
	return err == nil && host == "127.0.0.1" && port != "" && port != "4337"
}

func listenAcceptance() (net.Listener, error) {
	for attempt := 0; attempt < 8; attempt++ {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		if acceptanceAddressAllowed(listener.Addr().String()) {
			return listener, nil
		}
		_ = listener.Close()
	}
	return nil, errors.New("unable to allocate a non-production acceptance listener")
}

func startOwnedLoopback(handler http.Handler) (*ownedLoopbackServer, error) {
	listener, err := listenAcceptance()
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	owned := &ownedLoopbackServer{origin: "http://" + listener.Addr().String(), listener: listener, server: server}
	go func() { _ = server.Serve(listener) }()
	return owned, nil
}

func (s *ownedLoopbackServer) Close() {
	if s == nil || s.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.server.Shutdown(ctx); err != nil {
		_ = s.server.Close()
	}
}

type fixtureAuthorizationRequest struct {
	callback, state, nonce, challenge, resource string
	clientID                                    string
	decided                                     bool
	identityOnly                                bool
}

type fixtureAuthorizationCode struct {
	callback, nonce, challenge, resource, clientID string
	scopes                                         []string
	used                                           bool
	identityOnly                                   bool
}

type fixtureRefreshGrant struct {
	clientID, resource string
	scopes             []string
	current            string
	accesses           map[string]bool
	revoked            bool
}

// protocolFixture owns every upstream used by the acceptance process. It is
// deliberately unavailable to the production binary and never imports an
// environment credential or sends a request outside literal 127.0.0.1.
type protocolFixture struct {
	mu              sync.Mutex
	acceptanceGates bool

	key       *rsa.PrivateKey
	issuer    *ownedLoopbackServer
	model     *ownedLoopbackServer
	requests  map[string]*fixtureAuthorizationRequest
	codes     map[string]*fixtureAuthorizationCode
	tokens    map[string]bool
	refreshes map[string]*fixtureRefreshGrant
	modelHits map[string]int

	explicitKey    string
	kimiKey        string
	invalid        string
	directories    int
	modelRequests  int
	tokenCalls     int
	refreshCalls   int
	pkce           bool
	signed         int
	revokeCalls    int
	revokeRequests int
	closed         atomic.Bool
}

func newProtocolFixture() (_ *protocolFixture, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	f := &protocolFixture{
		key: key, requests: map[string]*fixtureAuthorizationRequest{}, codes: map[string]*fixtureAuthorizationCode{},
		tokens: map[string]bool{}, refreshes: map[string]*fixtureRefreshGrant{}, modelHits: map[string]int{},
	}
	if f.explicitKey, err = randomFixtureValue(32); err != nil {
		return nil, err
	}
	if f.kimiKey, err = randomFixtureValue(32); err != nil {
		return nil, err
	}
	f.model, err = startOwnedLoopback(http.HandlerFunc(f.handleModel))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			f.Close()
		}
	}()
	f.issuer, err = startOwnedLoopback(http.HandlerFunc(f.handleIssuer))
	if err != nil {
		return nil, err
	}
	return f, nil
}

func randomFixtureValue(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (f *protocolFixture) issuerOrigin() string { return f.issuer.origin }
func (f *protocolFixture) modelOrigin() string  { return f.model.origin }
func (f *protocolFixture) modelBaseURL() string { return f.model.origin + "/v1" }
func (f *protocolFixture) kimiBaseURL() string  { return f.model.origin + "/coding/v1" }

func (f *protocolFixture) siwcOptions() accounts.SIWCOptions {
	return accounts.SIWCOptions{
		AuthorizationOrigin: f.issuer.origin,
		TokenOrigin:         f.issuer.origin,
		OIDCOrigin:          f.issuer.origin,
		JWKSOrigin:          f.issuer.origin,
		RevocationOrigin:    f.issuer.origin,
		Issuer:              f.issuer.origin,
		FlowTimeout:         2 * time.Minute,
	}
}

func (f *protocolFixture) Close() {
	if f == nil || f.closed.Swap(true) {
		return
	}
	f.issuer.Close()
	f.model.Close()
}

func (f *protocolFixture) setInvalid(problem string) {
	f.mu.Lock()
	if f.invalid == "" {
		f.invalid = problem
	}
	f.mu.Unlock()
}

func (f *protocolFixture) invalidRequest() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.invalid
}

func (f *protocolFixture) inferenceCount(model string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.modelHits[model]
}

func (f *protocolFixture) directoryCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.directories
}

func (f *protocolFixture) tokenCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenCalls
}

func (f *protocolFixture) pkceWasValidated() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pkce
}

func (f *protocolFixture) signedTokenCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.signed
}

func (f *protocolFixture) revokeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.revokeCalls
}

func (f *protocolFixture) secretMarkers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	markers := []string{f.explicitKey, f.kimiKey}
	for token := range f.tokens {
		markers = append(markers, token)
	}
	for token := range f.refreshes {
		markers = append(markers, token)
	}
	return markers
}

func literal127URL(raw, path string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != path {
		return false
	}
	port := u.Port()
	portNumber, err := strconv.Atoi(port)
	return err == nil && portNumber >= 1 && portNumber <= 65535 && portNumber != 4337 && strconv.Itoa(portNumber) == port && u.Host == "127.0.0.1:"+port
}

func literal127RedirectURL(raw, path string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	u.RawQuery = ""
	return literal127URL(u.String(), path)
}

func exactQuery(values url.Values, required, optional []string) bool {
	allowed := map[string]bool{}
	for _, key := range append(append([]string(nil), required...), optional...) {
		allowed[key] = true
	}
	for key, list := range values {
		if !allowed[key] || len(list) != 1 {
			return false
		}
	}
	for _, key := range required {
		if values.Get(key) == "" {
			return false
		}
	}
	return true
}

func (f *protocolFixture) handleIssuer(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/accounts/authorize":
		f.handleAuthorizationPage(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/fixture/authorize/"):
		f.handleAuthorizationDecision(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/accounts/oauth/token":
		f.handleToken(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/.well-known/openid-configuration":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": f.issuer.origin, "jwks_uri": f.issuer.origin + "/jwks", "revocation_endpoint": f.issuer.origin + "/revoke"})
	case r.Method == http.MethodGet && r.URL.Path == "/jwks":
		w.Header().Set("Content-Type", "application/json")
		exponent := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.PublicKey.E)).Bytes())
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "synthetic-key-1", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(f.key.PublicKey.N.Bytes()), "e": exponent}}})
	case r.Method == http.MethodPost && r.URL.Path == "/revoke":
		f.mu.Lock()
		f.revokeRequests++
		f.mu.Unlock()
		if err := r.ParseForm(); err != nil || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || !exactQuery(r.PostForm, []string{"token", "token_type_hint", "client_id"}, nil) {
			http.Error(w, "invalid revocation request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		grant := f.refreshes[r.PostForm.Get("token")]
		valid := grant != nil && !grant.revoked && grant.current == r.PostForm.Get("token") && grant.clientID == r.PostForm.Get("client_id") && r.PostForm.Get("token_type_hint") == "refresh_token"
		if !valid {
			f.mu.Unlock()
			http.Error(w, "invalid revocation request", http.StatusBadRequest)
			return
		}
		grant.revoked = true
		for token := range grant.accesses {
			f.tokens[token] = false
		}
		f.revokeCalls++
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (f *protocolFixture) handleAuthorizationPage(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	identityOnly := values.Get("scope") == strings.Join(fixtureIdentityScopes, " ")
	required := []string{"client_id", "ext_agent_host_id", "response_type", "redirect_uri", "scope", "state", "nonce", "code_challenge_method", "code_challenge"}
	validGrant := identityOnly && !values.Has("resource")
	if !identityOnly {
		required = append(required, "resource")
		validGrant = (values.Get("resource") == f.modelBaseURL() || values.Get("resource") == domain.OpenAISubscriptionRequestedResource) && values.Get("scope") == strings.Join(fixtureRequestedScopes, " ")
	}
	validClient := values.Get("client_id") == "dynamic_agent_client" && values.Get("agent_name_hint") == "FOUNDRY" || values.Get("client_id") == "oaiapp_foundry_synthetic" && !values.Has("agent_name_hint")
	if !exactQuery(values, required, []string{"agent_name_hint"}) || !validGrant || !validClient || values.Get("response_type") != "code" || values.Get("code_challenge_method") != "S256" || !literal127URL(values.Get("redirect_uri"), "/auth/callback") || !strings.HasPrefix(values.Get("ext_agent_host_id"), "urn:uuid:") {
		f.setInvalid("synthetic authorization request violated the reviewed SIWC contract")
		http.Error(w, "invalid synthetic authorization request", http.StatusBadRequest)
		return
	}
	id, err := randomFixtureValue(18)
	if err != nil {
		http.Error(w, "fixture unavailable", http.StatusInternalServerError)
		return
	}
	f.mu.Lock()
	f.requests[id] = &fixtureAuthorizationRequest{
		callback: values.Get("redirect_uri"), state: values.Get("state"), nonce: values.Get("nonce"), challenge: values.Get("code_challenge"), resource: values.Get("resource"), clientID: "oaiapp_foundry_synthetic",
		identityOnly: identityOnly,
	}
	f.mu.Unlock()
	callbackURL, err := url.Parse(values.Get("redirect_uri"))
	if err != nil {
		http.Error(w, "invalid synthetic authorization request", http.StatusBadRequest)
		return
	}
	callbackOrigin := callbackURL.Scheme + "://" + callbackURL.Host
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' "+callbackOrigin+"; frame-ancestors 'none'")
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><title>SYNTHETIC local authorization</title></head><body><main><h1>SYNTHETIC local simulation</h1><p>No real account, cloud provider, entitlement, balance, or reset time is used or verified.</p><form data-outcome="approve" method="post" action="/fixture/authorize/%s/approve"><button>Approve synthetic direct-use grant</button></form><form data-outcome="identity" method="post" action="/fixture/authorize/%s/identity"><button>Approve identity-only grant</button></form><form data-outcome="decline" method="post" action="/fixture/authorize/%s/decline"><button>Decline</button></form></main></body></html>`, html.EscapeString(id), html.EscapeString(id), html.EscapeString(id))
}

func (f *protocolFixture) handleAuthorizationDecision(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/fixture/authorize/"), "/")
	if len(parts) != 2 || r.URL.RawQuery != "" || (parts[1] != "approve" && parts[1] != "identity" && parts[1] != "decline") {
		http.Error(w, "invalid synthetic decision", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	request := f.requests[parts[0]]
	if request == nil || request.decided {
		f.mu.Unlock()
		http.Error(w, "synthetic decision consumed", http.StatusConflict)
		return
	}
	request.decided = true
	if !literal127URL(request.callback, "/auth/callback") {
		f.mu.Unlock()
		http.Error(w, "callback refused", http.StatusBadRequest)
		return
	}
	if parts[1] == "decline" {
		location := request.callback + "?error=access_denied&state=" + url.QueryEscape(request.state)
		f.mu.Unlock()
		http.Redirect(w, r, location, http.StatusFound)
		return
	}
	code, err := randomFixtureValue(24)
	if err != nil {
		f.mu.Unlock()
		http.Error(w, "fixture unavailable", http.StatusInternalServerError)
		return
	}
	scopes := append([]string(nil), fixtureRequestedScopes...)
	if parts[1] == "identity" || request.identityOnly {
		scopes = append([]string(nil), fixtureIdentityScopes...)
	}
	f.codes[code] = &fixtureAuthorizationCode{callback: request.callback, nonce: request.nonce, challenge: request.challenge, resource: request.resource, clientID: request.clientID, scopes: scopes, identityOnly: request.identityOnly}
	location := request.callback + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(request.state) + "&client_id=" + url.QueryEscape(request.clientID) + "&scope=" + url.QueryEscape(strings.Join(scopes, " "))
	if request.identityOnly {
		location = request.callback + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(request.state) + "&client_id=" + url.QueryEscape(request.clientID)
	}
	f.mu.Unlock()
	http.Redirect(w, r, location, http.StatusFound)
}

func (f *protocolFixture) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		http.Error(w, "invalid token request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.tokenCalls++
	f.mu.Unlock()
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		f.handleAuthorizationCode(w, r)
	case "refresh_token":
		f.mu.Lock()
		f.refreshCalls++
		f.mu.Unlock()
		f.handleRefreshToken(w, r)
	default:
		writeInvalidGrant(w)
	}
}

func writeInvalidGrant(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
}

func (f *protocolFixture) handleAuthorizationCode(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	code := f.codes[r.PostForm.Get("code")]
	if code == nil || code.used {
		f.mu.Unlock()
		writeInvalidGrant(w)
		return
	}
	code.used = true
	required := []string{"grant_type", "client_id", "code", "code_verifier", "redirect_uri"}
	if !code.identityOnly {
		required = append(required, "resource")
	}
	digest := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	pkce := base64.RawURLEncoding.EncodeToString(digest[:]) == code.challenge
	valid := exactQuery(r.PostForm, required, nil) && pkce && r.PostForm.Get("client_id") == code.clientID && r.PostForm.Get("redirect_uri") == code.callback && r.PostForm.Get("resource") == code.resource
	if !valid {
		f.mu.Unlock()
		writeInvalidGrant(w)
		return
	}
	f.pkce = true
	if code.identityOnly {
		f.mu.Unlock()
		idToken, err := f.signIDToken(code.clientID, code.nonce)
		if err != nil {
			http.Error(w, "fixture unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": idToken})
		return
	}
	access, accessErr := randomFixtureValue(32)
	refresh, refreshErr := randomFixtureValue(32)
	if accessErr != nil || refreshErr != nil {
		f.mu.Unlock()
		http.Error(w, "fixture unavailable", http.StatusInternalServerError)
		return
	}
	f.tokens[access] = true
	offline := false
	for _, scope := range code.scopes {
		if scope == "offline_access" {
			offline = true
			break
		}
	}
	if offline {
		grant := &fixtureRefreshGrant{clientID: code.clientID, resource: code.resource, scopes: append([]string(nil), code.scopes...), current: refresh, accesses: map[string]bool{access: true}}
		f.refreshes[refresh] = grant
	}
	f.mu.Unlock()
	idToken, err := f.signIDToken(code.clientID, code.nonce)
	if err != nil {
		http.Error(w, "fixture unavailable", http.StatusInternalServerError)
		return
	}
	payload := map[string]any{
		"access_token": access, "id_token": idToken, "token_type": "Bearer", "expires_in": 3600,
		"scope": strings.Join(code.scopes, " "), "earliest_refresh_at": time.Now().Add(-time.Minute).Unix(),
	}
	if offline {
		payload["refresh_token"] = refresh
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (f *protocolFixture) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	if !exactQuery(r.PostForm, []string{"grant_type", "client_id", "refresh_token", "resource"}, nil) {
		writeInvalidGrant(w)
		return
	}
	f.mu.Lock()
	previous := r.PostForm.Get("refresh_token")
	grant := f.refreshes[previous]
	valid := grant != nil && !grant.revoked && grant.current == previous && grant.clientID == r.PostForm.Get("client_id") && grant.resource == r.PostForm.Get("resource")
	if !valid {
		f.mu.Unlock()
		writeInvalidGrant(w)
		return
	}
	access, accessErr := randomFixtureValue(32)
	refresh, refreshErr := randomFixtureValue(32)
	if accessErr != nil || refreshErr != nil {
		f.mu.Unlock()
		http.Error(w, "fixture unavailable", http.StatusInternalServerError)
		return
	}
	f.tokens[access] = true
	grant.accesses[access] = true
	grant.current = refresh
	f.refreshes[refresh] = grant
	clientID := grant.clientID
	scopes := append([]string(nil), grant.scopes...)
	f.mu.Unlock()
	idToken, err := f.signIDToken(clientID, "")
	if err != nil {
		http.Error(w, "fixture unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": access, "refresh_token": refresh, "id_token": idToken, "token_type": "Bearer", "expires_in": 3600,
		"scope": strings.Join(scopes, " "), "earliest_refresh_at": time.Now().Add(-time.Minute).Unix(),
	})
}

func (f *protocolFixture) signIDToken(clientID, nonce string) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "synthetic-key-1", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{"iss": f.issuer.origin, "aud": clientID, "sub": "synthetic-subject", "nonce": nonce, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Add(-time.Minute).Unix()})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	f.signed++
	f.mu.Unlock()
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (f *protocolFixture) authorized(r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	defer f.mu.Unlock()
	return token != "" && (f.tokens[token] || token == f.explicitKey || token == f.kimiKey)
}

func (f *protocolFixture) handleModel(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.modelRequests++ // Count rejected requests too: zero means no model transport.
	gate := f.acceptanceGates
	f.mu.Unlock()
	if gate {
		f.handleGateModel(w, r)
		return
	}
	if !f.authorized(r) || r.Header.Get("User-Agent") != "Foundry/0.2.0" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "invalid_api_key"}})
		return
	}
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
		f.mu.Lock()
		f.directories++
		isSIWC := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if isSIWC {
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]any{"slug": "synthetic-siwc-success", "display_name": "SYNTHETIC SIWC model", "visibility": "list"}}})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "synthetic-explicit-model", "display_name": "SYNTHETIC explicit API directory model"}}})
		}
		return
	}
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/responses") {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil || !sameKeys(payload, []string{"model", "instructions", "input", "stream", "store"}) || string(payload["stream"]) != "true" || string(payload["store"]) != "false" {
		f.setInvalid("SIWC inference sent unsupported or missing fields")
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	var modelID, instructions string
	var input []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if json.Unmarshal(payload["model"], &modelID) != nil || json.Unmarshal(payload["instructions"], &instructions) != nil || json.Unmarshal(payload["input"], &input) != nil || len(input) != 1 || input[0].Role != "user" || input[0].Content == "" {
		f.setInvalid("SIWC inference request schema was not the reviewed input array")
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.modelHits[modelID]++
	f.mu.Unlock()
	w.Header().Set("X-Request-ID", "synthetic-request-"+safeFixtureID(modelID))
	if strings.HasPrefix(modelID, "synthetic-siwc-success-") {
		f.completedSSE(w, modelID, syntheticRoleOutput(instructions))
		return
	}
	switch modelID {
	case "synthetic-quota-exhausted":
		f.providerError(w, http.StatusTooManyRequests, "subscription_sharing_usage_limit_exceeded")
	case "synthetic-permission-denied":
		f.providerError(w, http.StatusForbidden, "permission_error")
	case "synthetic-temporary-provider-error":
		f.providerError(w, http.StatusServiceUnavailable, "overloaded_error")
	case "synthetic-sse-outcome-unknown":
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial synthetic text\"}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	default:
		f.setInvalid("unknown synthetic model id reached inference: " + safeFixtureID(modelID))
		f.providerError(w, http.StatusBadRequest, "model_not_found")
	}
}

func sameKeys(values map[string]json.RawMessage, want []string) bool {
	if len(values) != len(want) {
		return false
	}
	for _, key := range want {
		if _, ok := values[key]; !ok {
			return false
		}
	}
	return true
}

func safeFixtureID(value string) string {
	var result strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_", r) {
			result.WriteRune(r)
		}
	}
	if result.Len() == 0 {
		return "unknown"
	}
	return result.String()
}

func (f *protocolFixture) providerError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code}})
}

func (f *protocolFixture) completedSSE(w http.ResponseWriter, modelID, output string) {
	response := map[string]any{
		"object": "response", "status": "completed", "model": modelID, "error": nil,
		"output": []any{
			map[string]any{
				"type": "message", "role": "assistant", "status": "completed",
				"content": []any{map[string]string{"type": "output_text", "text": output}},
			},
		},
		"usage": map[string]int64{"input_tokens": 11, "output_tokens": 7},
	}
	event := map[string]any{"type": "response.completed", "response": response}
	raw, _ := json.Marshal(event)
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", raw)
}

func syntheticRoleOutput(instructions string) string {
	switch {
	case strings.Contains(instructions, "At most six acyclic steps"):
		return `{"summary":"SYNTHETIC local protocol plan; execution still needs explicit approval","steps":[{"id":"one","title":"Update synthetic text","description":"Change only the disposable fixture","depends_on":[],"files":["src/main.txt"]}]}`
	case strings.Contains(instructions, "Propose full-file UTF-8 edits only"):
		return `{"summary":"SYNTHETIC local protocol edit","edits":[{"path":"src/main.txt","content":"SYNTHETIC source after actual local protocol\n"}]}`
	case strings.Contains(instructions, "Independently review current changes"):
		return `{"passed":true,"summary":"SYNTHETIC protocol review of deterministic local evidence only","findings":[]}`
	case strings.Contains(instructions, "Research repository facts only"):
		return `{"summary":"SYNTHETIC local observation","findings":[{"severity":"info","summary":"Synthetic fixture source only","paths":["src/main.txt"]}],"unknowns":["No cloud provider was tested"]}`
	default:
		return `{"answer":"SYNTHETIC local protocol response; no cloud provider was used."}`
	}
}

// htmlFormAction reads only this package's fixed fixture markup.
func htmlFormAction(page, outcome string) string {
	marker := `data-outcome="` + outcome + `"`
	index := strings.Index(page, marker)
	if index < 0 {
		return ""
	}
	rest := page[index+len(marker):]
	action := strings.Index(rest, `action="`)
	if action < 0 {
		return ""
	}
	rest = rest[action+len(`action="`):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return html.UnescapeString(rest[:end])
}

func completeFixtureAuthorization(ctx context.Context, siwc *accounts.SIWCService, account domain.ModelAccount, outcome string) error {
	flow, err := siwc.Begin(ctx, account.ID, account.Revision)
	if err != nil {
		return err
	}
	return driveFixtureAuthorization(ctx, siwc, flow, outcome)
}

func completeOwnedFixtureAuthorization(ctx context.Context, siwc *accounts.SIWCService, account domain.ModelAccount, outcome string) error {
	flow, stop, err := siwc.BeginOwnedSimulation(ctx, account.ID, account.Revision)
	if err != nil {
		return err
	}
	defer stop()
	return driveFixtureAuthorization(ctx, siwc, flow, outcome)
}

func completeOwnedSubscriptionConnectionAuthorization(ctx context.Context, siwc *accounts.SIWCService, account domain.ModelAccount, outcome string) error {
	flow, stop, err := siwc.BeginOwnedSubscriptionConnectionSimulation(ctx, account.ID, account.Revision)
	if err != nil {
		return err
	}
	defer stop()
	return driveFixtureAuthorization(ctx, siwc, flow, outcome)
}

func completeOwnedIdentityFixtureAuthorization(ctx context.Context, siwc *accounts.SIWCService, account domain.ModelAccount, outcome string) error {
	flow, stop, err := siwc.BeginOwnedIdentitySimulation(ctx, account.ID, account.Revision)
	if err != nil {
		return err
	}
	defer stop()
	return driveFixtureAuthorization(ctx, siwc, flow, outcome)
}

func driveFixtureAuthorization(ctx context.Context, siwc *accounts.SIWCService, flow accounts.SIWCFlow, outcome string) error {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(target string) (*http.Response, error) {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if requestErr != nil {
			return nil, requestErr
		}
		return client.Do(request)
	}
	handoff, err := get(flow.HandoffURL)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, handoff.Body)
	handoff.Body.Close()
	if handoff.StatusCode != http.StatusFound {
		return fmt.Errorf("synthetic handoff status %d", handoff.StatusCode)
	}
	authorization, err := get(handoff.Header.Get("Location"))
	if err != nil {
		return err
	}
	page, readErr := io.ReadAll(io.LimitReader(authorization.Body, 1<<20))
	authorization.Body.Close()
	if readErr != nil || authorization.StatusCode != http.StatusOK {
		return errors.New("synthetic authorization page unavailable")
	}
	action := htmlFormAction(string(page), outcome)
	if action == "" {
		return errors.New("synthetic authorization outcome unavailable")
	}
	actionURL, err := url.Parse(handoff.Header.Get("Location"))
	if err != nil {
		return err
	}
	actionURL.Path, actionURL.RawQuery = action, ""
	decision, _ := http.NewRequestWithContext(ctx, http.MethodPost, actionURL.String(), bytes.NewReader(nil))
	decided, err := client.Do(decision)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, decided.Body)
	decided.Body.Close()
	if decided.StatusCode != http.StatusFound || !literal127RedirectURL(decided.Header.Get("Location"), "/auth/callback") {
		return errors.New("synthetic authorization callback was not literal loopback")
	}
	callback, err := get(decided.Header.Get("Location"))
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, callback.Body)
	callback.Body.Close()
	if outcome == "decline" {
		if callback.StatusCode != http.StatusOK {
			return fmt.Errorf("synthetic decline callback status %d", callback.StatusCode)
		}
		return nil
	}
	if callback.StatusCode != http.StatusOK {
		return fmt.Errorf("synthetic authorization callback status %d", callback.StatusCode)
	}
	status, err := siwc.Status(flow.OperationID)
	if err != nil || status.State != "available" {
		return fmt.Errorf("synthetic authorization terminal state %s: %w", status.State, err)
	}
	return nil
}
