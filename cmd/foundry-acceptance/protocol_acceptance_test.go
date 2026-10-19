package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
)

// Breaks if a fixture-issued refresh token, including a rotation, is omitted
// from the secret sentinel set and can therefore escape the SQLite/WAL scan.
func TestFixtureTracksEveryRotatedRefreshAsASecretSentinel(t *testing.T) {
	runtime := startProtocolAcceptanceForTest(t)
	mission, err := runtime.store.GetMission(runtime.scenarios["plan_success"])
	if err != nil {
		t.Fatal(err)
	}
	binding := mission.ModelSnapshot.Roles[domain.Planner].Authorization
	if binding == nil {
		t.Fatal("successful protocol mission lacks a frozen authorization")
	}
	authorization, err := runtime.store.GetModelAuthorization(binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	account, err := runtime.store.GetModelAccount(binding.AccountID)
	if err != nil {
		t.Fatal(err)
	}

	runtime.protocol.mu.Lock()
	before := len(runtime.protocol.refreshes)
	runtime.protocol.mu.Unlock()
	if _, err = runtime.siwc.Refresh(context.Background(), authorization.Binding.ID, account.Revision); err != nil {
		t.Fatal(err)
	}

	runtime.protocol.mu.Lock()
	issued := make([]string, 0, len(runtime.protocol.refreshes))
	for token := range runtime.protocol.refreshes {
		issued = append(issued, token)
	}
	runtime.protocol.mu.Unlock()
	if len(issued) != before+1 {
		t.Fatalf("refresh rotation was not retained as a sentinel: before=%d after=%d", before, len(issued))
	}
	markers := runtime.protocol.secretMarkers()
	for _, refresh := range issued {
		if !containsString(markers, refresh) {
			t.Fatal("issued refresh token missing from secret sentinel set")
		}
		if strings.Contains(string(mustJSON(t, runtime.manifest())), refresh) {
			t.Fatal("refresh token leaked through acceptance manifest")
		}
		for _, name := range []string{"foundry.db", "foundry.db-wal"} {
			raw, readErr := os.ReadFile(filepath.Join(runtime.Root, name))
			if readErr == nil && strings.Contains(string(raw), refresh) {
				t.Fatalf("refresh token reached SQLite file %s", name)
			}
		}
	}
}

// Breaks if revocation accepts an unissued/mismatched grant or remains a
// count-only endpoint that leaves the exact grant refreshable.
func TestFixtureRevocationValidatesAndInvalidatesExactRefreshGrant(t *testing.T) {
	runtime := startProtocolAcceptanceForTest(t)
	runtime.protocol.mu.Lock()
	var revokedToken string
	var revokedGrant *fixtureRefreshGrant
	var activeToken string
	var activeGrant *fixtureRefreshGrant
	for token, grant := range runtime.protocol.refreshes {
		if grant.revoked && revokedGrant == nil {
			revokedToken, revokedGrant = token, grant
		}
		if !grant.revoked && token == grant.current && activeGrant == nil {
			activeToken, activeGrant = token, grant
		}
	}
	runtime.protocol.mu.Unlock()
	if revokedGrant == nil || activeGrant == nil {
		t.Fatal("fixture did not retain both revoked and active refresh grants")
	}

	revocations := runtime.protocol.revokeCount()
	bad := url.Values{"token": {activeToken}, "token_type_hint": {"refresh_token"}, "client_id": {activeGrant.clientID + "-wrong"}}
	response, err := http.PostForm(runtime.protocol.issuerOrigin()+"/revoke", bad)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || runtime.protocol.revokeCount() != revocations {
		t.Fatalf("mismatched revocation was accepted: status=%d calls=%d", response.StatusCode, runtime.protocol.revokeCount())
	}

	before := runtime.protocol.tokenCount()
	refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {revokedGrant.clientID}, "refresh_token": {revokedToken}, "resource": {revokedGrant.resource}}
	response, err = http.PostForm(runtime.protocol.issuerOrigin()+"/api/accounts/oauth/token", refresh)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope struct {
		Error string `json:"error"`
	}
	if err = json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || envelope.Error != "invalid_grant" || runtime.protocol.tokenCount() != before+1 {
		t.Fatalf("revoked grant refresh was not finitely rejected by real HTTP: status=%d error=%q calls=%d", response.StatusCode, envelope.Error, runtime.protocol.tokenCount())
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func startProtocolAcceptanceForTest(t *testing.T) *acceptanceRuntime {
	t.Helper()
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("acceptance static fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime, err := startAcceptance(web)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		runtime.Close()
		_ = os.RemoveAll(runtime.Root)
	})
	return runtime
}

// Breaks if the acceptance command falls back to syntheticGateway for schema2,
// fabricates receipts, refunds an unknown request, or omits a frozen role.
func TestProtocolScenariosUseActualRouterEngineAuthorityAndEvidence(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("OPENAI_API_KEY", "ENV-CREDENTIAL-MUST-NOT-BE-IMPORTED")
	runtime := startProtocolAcceptanceForTest(t)

	manifest := runtime.manifest()
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"ENV-CREDENTIAL", "FAKE-SIWC", "code_verifier", "handoff_url", "auth.openai.com", "api.openai.com"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("safe manifest leaked %q: %s", forbidden, text)
		}
	}
	for _, secret := range runtime.protocol.secretMarkers() {
		if secret == "" || strings.Contains(text, secret) {
			t.Fatalf("safe manifest leaked an ephemeral credential")
		}
		for _, name := range []string{"foundry.db", "foundry.db-wal"} {
			raw, readErr := os.ReadFile(filepath.Join(runtime.Root, name))
			if readErr == nil && strings.Contains(string(raw), secret) {
				t.Fatalf("ephemeral credential reached SQLite file %s", name)
			}
		}
	}
	for _, name := range []string{
		"authorization_success", "authorization_identity", "authorization_decline", "authorization_revoked", "identity_only_denied",
		"explicit_directory_probe", "plan_success", "quota_exhausted",
		"permission_denied", "temporary_provider_error", "sse_outcome_unknown",
		"legacy_mcp_approval",
	} {
		if manifest.Scenarios[name] == "" {
			t.Fatalf("missing safe scenario id %q: %+v", name, manifest.Scenarios)
		}
	}
	if !strings.HasPrefix(manifest.URL, "http://127.0.0.1:") || strings.HasSuffix(manifest.URL, ":4337") {
		t.Fatalf("unsafe acceptance address %q", manifest.URL)
	}

	assertFrozenRoles := func(m domain.Mission) {
		t.Helper()
		if m.ModelSnapshot == nil || m.ModelSnapshot.SchemaVersion != 2 || len(m.ModelSnapshot.Roles) != 7 || len(m.Profiles) != 7 {
			t.Fatalf("scenario did not freeze all seven schema2 roles: %+v", m.ModelSnapshot)
		}
		for _, role := range []domain.Role{domain.Main, domain.Researcher, domain.Planner, domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
			if _, ok := m.ModelSnapshot.Roles[role]; !ok {
				t.Fatalf("missing frozen role %s", role)
			}
		}
	}
	success, err := runtime.store.GetMission(manifest.Scenarios["plan_success"])
	if err != nil {
		t.Fatal(err)
	}
	assertFrozenRoles(success)
	if success.State != "awaiting_approval" || success.Plan == nil || success.Budget.UsedCalls != 1 {
		t.Fatalf("successful protocol plan did not stop at real approval: %+v", success)
	}
	successCalls, err := runtime.store.ListInvocations(success.ID, 0, 100)
	if err != nil || len(successCalls) != 1 || successCalls[0].Status != "succeeded" || successCalls[0].ModelReceipt == nil || successCalls[0].ModelReceipt.Certainty != "completed" || successCalls[0].Result == nil {
		t.Fatalf("missing durable successful protocol evidence: %+v %v", successCalls, err)
	}
	detail, err := runtime.engine.InvocationDetail(success.ID, successCalls[0].ID)
	if err != nil || detail.ResultPreview == "" {
		t.Fatalf("actual sealed result is unavailable: %+v %v", detail, err)
	}

	wants := map[string]struct {
		status, code, certainty string
	}{
		"quota_exhausted":          {"failed", "quota_exhausted", "rejected"},
		"permission_denied":        {"failed", "entitlement_denied", "rejected"},
		"temporary_provider_error": {"failed", "server_error", "rejected"},
		"sse_outcome_unknown":      {"outcome_unknown", "response_incomplete", "unknown"},
	}
	for scenario, want := range wants {
		m, getErr := runtime.store.GetMission(manifest.Scenarios[scenario])
		if getErr != nil {
			t.Fatal(getErr)
		}
		assertFrozenRoles(m)
		calls, listErr := runtime.store.ListInvocations(m.ID, 0, 100)
		if listErr != nil || len(calls) != 1 || m.Budget.UsedCalls != 1 || calls[0].Status != want.status || calls[0].ErrorCode != want.code || calls[0].ModelReceipt == nil || calls[0].ModelReceipt.Certainty != want.certainty {
			t.Fatalf("scenario %s lacks actual durable failure evidence: mission=%+v calls=%+v err=%v", scenario, m, calls, listErr)
		}
	}
	if got := runtime.protocol.inferenceCount("synthetic-sse-outcome-unknown"); got != 1 {
		t.Fatalf("unknown request must be sent once with no replay, got %d", got)
	}

	identity, err := runtime.store.GetMission(manifest.Scenarios["identity_only_denied"])
	if err != nil {
		t.Fatal(err)
	}
	assertFrozenRoles(identity)
	identityBinding := identity.ModelSnapshot.Roles[domain.Planner].Authorization
	if identityBinding == nil || containsString(identityBinding.Scopes, "resource.invoke") || containsString(identityBinding.Scopes, "chatgpt.tokens.use.direct") {
		t.Fatalf("identity-only scenario froze direct-use authority: %+v", identityBinding)
	}
	if identity.State != "draft" || identity.Budget.UsedCalls != 0 || runtime.protocol.inferenceCount("synthetic-identity-only") != 0 {
		t.Fatalf("identity-only grant crossed inference gate: %+v", identity)
	}
	if _, err = runtime.engine.Plan(context.Background(), identity.ID, identity.Revision); err == nil || !strings.Contains(err.Error(), "entitlement") {
		t.Fatalf("identity-only inference denial missing: %v", err)
	}

	selection, err := runtime.store.ResolveModelProfile(manifest.Scenarios["explicit_directory_probe"])
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := runtime.router.ProbeSelection(context.Background(), 2, selection)
	if err != nil || len(catalog) == 0 || runtime.protocol.directoryCount() != 1 {
		t.Fatalf("explicit directory probe did not use real local HTTP: %+v %v", catalog, err)
	}
	if runtime.protocol.invalidRequest() != "" {
		t.Fatal(runtime.protocol.invalidRequest())
	}
	if runtime.protocol.revokeCount() != 1 {
		t.Fatalf("owned revocation endpoint was not exercised exactly once: %d", runtime.protocol.revokeCount())
	}
}

// Breaks if the browser simulation exposes a cloud link, accepts a non-local
// callback, skips signed OIDC/PKCE exchange, or allows callback/code replay.
func TestSyntheticAuthorizationPageIsSignedLocalAndSingleUse(t *testing.T) {
	runtime := startProtocolAcceptanceForTest(t)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	page, err := client.Get(runtime.URL + acceptanceFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	fixturePolicy := page.Header.Get("Content-Security-Policy")
	page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(string(body), "SYNTHETIC") || !strings.Contains(string(body), "no real account") || strings.Contains(string(body), "https://") {
		t.Fatalf("unsafe fixture page: status=%d body=%s", page.StatusCode, body)
	}
	if fixturePolicy != "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'" {
		t.Fatalf("harness start page should submit only to itself: %q", fixturePolicy)
	}

	start := runtime.URL + acceptanceFixtureAuthPath + "?scenario=authorization_success"
	request, _ := http.NewRequest(http.MethodPost, start, strings.NewReader(""))
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("start status=%d", response.StatusCode)
	}
	handoff := response.Header.Get("Location")
	if !literal127URL(handoff, "/auth/start") {
		t.Fatalf("unsafe handoff %q", handoff)
	}

	response, err = client.Get(handoff)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	authorize := response.Header.Get("Location")
	if response.StatusCode != http.StatusFound || !strings.HasPrefix(authorize, runtime.protocol.issuerOrigin()+"/api/accounts/authorize?") {
		t.Fatalf("bad authorization handoff: %d %q", response.StatusCode, authorize)
	}
	response, err = client.Get(authorize)
	if err != nil {
		t.Fatal(err)
	}
	authPage, _ := io.ReadAll(response.Body)
	authorizationPolicy := response.Header.Get("Content-Security-Policy")
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(authPage), "SYNTHETIC local simulation") || strings.Contains(string(authPage), "https://") {
		t.Fatalf("bad local authorization page: %d %s", response.StatusCode, authPage)
	}
	authorizationURL, err := url.Parse(authorize)
	if err != nil {
		t.Fatal(err)
	}
	callbackURL, err := url.Parse(authorizationURL.Query().Get("redirect_uri"))
	if err != nil || callbackURL.Scheme != "http" || callbackURL.Hostname() != "127.0.0.1" || callbackURL.Port() == "" {
		t.Fatalf("authorization did not carry a literal loopback callback: %q %v", authorizationURL.Query().Get("redirect_uri"), err)
	}
	callbackOrigin := callbackURL.Scheme + "://" + callbackURL.Host
	wantPolicy := "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' " + callbackOrigin + "; frame-ancestors 'none'"
	if authorizationPolicy != wantPolicy || strings.Contains(authorizationPolicy, "*") || strings.Contains(authorizationPolicy, callbackURL.Path) {
		t.Fatalf("authorization form redirect is not narrowly allowlisted: got=%q want=%q", authorizationPolicy, wantPolicy)
	}
	approve := htmlFormAction(string(authPage), "approve")
	if approve == "" || htmlFormAction(string(authPage), "identity") == "" || htmlFormAction(string(authPage), "decline") == "" {
		t.Fatalf("approve action missing: %s", authPage)
	}
	before := runtime.protocol.tokenCount()
	decision, _ := http.NewRequest(http.MethodPost, runtime.protocol.issuerOrigin()+approve, strings.NewReader(""))
	response, err = client.Do(decision)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	callback := response.Header.Get("Location")
	parsedCallback, _ := url.Parse(callback)
	if response.StatusCode != http.StatusFound || parsedCallback.Hostname() != "127.0.0.1" || parsedCallback.Path != "/auth/callback" {
		t.Fatalf("fixture redirected outside literal callback: %d %q", response.StatusCode, callback)
	}
	response, err = client.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || runtime.protocol.tokenCount() != before+1 || !runtime.protocol.pkceWasValidated() || runtime.protocol.signedTokenCount() < 1 {
		t.Fatalf("signed PKCE exchange did not complete: status=%d token=%d pkce=%v signed=%d", response.StatusCode, runtime.protocol.tokenCount(), runtime.protocol.pkceWasValidated(), runtime.protocol.signedTokenCount())
	}
	replay, err := client.Get(callback)
	if replay != nil {
		replay.Body.Close()
	}
	if runtime.protocol.tokenCount() != before+1 || err == nil && replay.StatusCode != http.StatusConflict {
		status := 0
		if replay != nil {
			status = replay.StatusCode
		}
		t.Fatalf("callback/code replay was accepted: status=%d calls=%d err=%v", status, runtime.protocol.tokenCount(), err)
	}
}

func TestAcceptanceClosesEveryOwnedProtocolListenerAndRejectsProductionPort(t *testing.T) {
	if acceptanceAddressAllowed("127.0.0.1:4337") || !acceptanceAddressAllowed("127.0.0.1:4338") || acceptanceAddressAllowed("0.0.0.0:4338") {
		t.Fatal("acceptance production-port/loopback gate is not exact")
	}
	runtime := startProtocolAcceptanceForTest(t)
	mainURL := runtime.URL
	issuerURL := runtime.protocol.issuerOrigin()
	modelURL := runtime.protocol.modelOrigin()
	client := &http.Client{Timeout: 500 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start, _ := http.NewRequest(http.MethodPost, runtime.URL+acceptanceFixtureAuthPath+"?scenario=authorization_decline", strings.NewReader(""))
	started, err := client.Do(start)
	if err != nil {
		t.Fatal(err)
	}
	handoff := started.Header.Get("Location")
	started.Body.Close()
	if started.StatusCode != http.StatusSeeOther || !literal127URL(handoff, "/auth/start") {
		t.Fatalf("active handoff setup failed: %d %q", started.StatusCode, handoff)
	}
	runtime.Close()
	client.CheckRedirect = nil
	for _, endpoint := range []string{mainURL, issuerURL, modelURL, handoff} {
		response, err := client.Get(endpoint)
		if response != nil {
			response.Body.Close()
		}
		if err == nil {
			t.Fatalf("owned listener survived shutdown: %s", endpoint)
		}
	}
}

// Breaks if an acceptance redirect can target the production port, a
// non-canonical/out-of-range port, or smuggle callback query state.
func TestLiteral127URLRequiresCanonicalSafePortAndExactPath(t *testing.T) {
	for name, test := range map[string]struct {
		raw  string
		want bool
	}{
		"minimum port":       {"http://127.0.0.1:1/auth/callback", true},
		"maximum port":       {"http://127.0.0.1:65535/auth/callback", true},
		"zero port":          {"http://127.0.0.1:0/auth/callback", false},
		"out of range":       {"http://127.0.0.1:65536/auth/callback", false},
		"leading zero":       {"http://127.0.0.1:04338/auth/callback", false},
		"production port":    {"http://127.0.0.1:4337/auth/callback", false},
		"callback query":     {"http://127.0.0.1:4338/auth/callback?code=smuggled", false},
		"different callback": {"http://127.0.0.1:4338/auth/start", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := literal127URL(test.raw, "/auth/callback"); got != test.want {
				t.Fatalf("literal127URL(%q)=%v want %v", test.raw, got, test.want)
			}
		})
	}
}

// Breaks if a provider decision redirect cannot carry callback parameters or
// if adding those parameters weakens the literal-loopback port/path boundary.
func TestLiteral127RedirectURLAllowsOnlyQueryOnSafeCallback(t *testing.T) {
	for name, test := range map[string]struct {
		raw  string
		want bool
	}{
		"callback parameters": {"http://127.0.0.1:4338/auth/callback?code=one&state=two", true},
		"production port":     {"http://127.0.0.1:4337/auth/callback?code=one", false},
		"leading zero":        {"http://127.0.0.1:04338/auth/callback?code=one", false},
		"fragment":            {"http://127.0.0.1:4338/auth/callback?code=one#fragment", false},
		"different path":      {"http://127.0.0.1:4338/auth/start?code=one", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := literal127RedirectURL(test.raw, "/auth/callback"); got != test.want {
				t.Fatalf("literal127RedirectURL(%q)=%v want %v", test.raw, got, test.want)
			}
		})
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
