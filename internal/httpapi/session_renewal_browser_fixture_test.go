package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"foundry/internal/acceptance"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// UI-only opt-in fixture: synthetic seeded grant and in-memory credentials.
// Protocol rotation is tested separately against the signed accounts fixture.
// No ticker is started; UI reads/prepare/approval never touch the token endpoint.
func sessionRenewalBrowserFixture(t *testing.T) (*fixture, domain.ModelAccount) {
	t.Helper()
	f := newFixture(t)
	s := f.handler.(*server)
	s.codingWorkbench = true
	transport := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("metadata UI reached provider transport")
		w.WriteHeader(500)
	}))
	t.Cleanup(transport.Close)
	a, err := f.store.SaveModelAccount(domain.ModelAccount{ID: "synthetic-renewal", Name: "SYNTHETIC renewal — no real provider", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", BaseURL: transport.URL + "/v1", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.siwc, err = accounts.NewSIWCSimulation(s.accounts, accounts.SIWCOptions{AuthorizationOrigin: transport.URL, TokenOrigin: transport.URL, OIDCOrigin: transport.URL, JWKSOrigin: transport.URL, RevocationOrigin: transport.URL, Issuer: transport.URL, FlowTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.siwc.Close() })
	b := domain.ModelAuthorizationBinding{ID: "synthetic-renewal-grant", AccountID: a.ID, Issuer: transport.URL, Subject: "synthetic-only", ClientID: "oaiapp_synthetic", SpendClass: "subscription_only", Scopes: []string{"openid", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}}
	op, err := s.accounts.BeginModelAuthorization(context.Background(), a.ID, "synthetic-renewal-signin", "sign_in", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.accounts.CommitModelAuthorization(context.Background(), op.ID, b, accounts.ModelTokenBundle{AccessToken: "SYNTHETIC-NONCREDENTIAL-ACCESS", RefreshToken: "SYNTHETIC-NONCREDENTIAL-REFRESH", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339), Resource: a.BaseURL})
	if err != nil {
		t.Fatal(err)
	}
	s.sessionRenewal = accounts.NewSessionRenewal(s.siwc, time.Now)
	t.Cleanup(s.sessionRenewal.Close)
	s.desktopAccounts = acceptance.NewDesktopCoordinator(f.store, acceptance.DesktopOptions{SIWC: s.siwc, Resolver: s.accounts, Probe: func() error { t.Error("UI triggered independent diagnostic"); return fmt.Errorf("unavailable") }})
	t.Cleanup(func() { _ = s.desktopAccounts.Close() })
	return f, a
}

func TestSessionRenewalBrowserFixtureActions(t *testing.T) {
	f, a := sessionRenewalBrowserFixture(t)
	f.authenticate()
	path := "/api/model-accounts/" + a.ID + "/session-renewal"
	rr := f.request("POST", path, `{"action":"prepare","expected_account_revision":1}`, true)
	if rr.Code != 200 {
		t.Fatalf("fixture prepare: %d %s", rr.Code, rr.Body.String())
	}
	var status domain.SessionRenewalStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"action": "approve", "material_id": status.Material.ID, "expected_revision": status.Revision})
	rr = f.request("POST", path, string(body), true)
	if rr.Code != 200 {
		t.Fatalf("fixture approve: %d %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil || !status.Enabled {
		t.Fatalf("enable: %+v %v", status, err)
	}
	body, _ = json.Marshal(map[string]any{"action": "disable", "expected_revision": status.Revision})
	rr = f.request("POST", path, string(body), true)
	if rr.Code != 200 {
		t.Fatalf("fixture disable: %d %s", rr.Code, rr.Body.String())
	}
}

func TestSessionRenewalBrowserFixture(t *testing.T) {
	if os.Getenv("FOUNDRY_TEST_SESSION_RENEWAL_BROWSER") != "1" {
		t.Skip("explicit synthetic browser fixture only")
	}
	f, a := sessionRenewalBrowserFixture(t)
	s := f.handler.(*server)
	var err error
	s.webDir, err = filepath.Abs("../../web/dist")
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(s)
	defer func() { app.CloseClientConnections(); app.Close() }()
	s.host = strings.TrimPrefix(app.URL, "http://")
	s.origin = app.URL
	fmt.Printf("SESSION_RENEWAL_BROWSER_FIXTURE url=%s account=%s (SYNTHETIC UI ONLY; no ticker/provider/native store)\n", app.URL, a.ID)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(25 * time.Minute):
	}
}
