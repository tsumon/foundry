package httpapi

import (
	"context"
	"encoding/json"
	"errors"
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

// No credential read can succeed, even if an operator clicks outside the
// metadata-only acceptance path. This fixture never starts a renewal ticker.
type renewalCatalogNoSecrets struct{ t *testing.T }

func (s renewalCatalogNoSecrets) Get(string) (string, error) {
	s.t.Error("renewal catalog fixture read a secret")
	return "", errors.New("synthetic fixture has no credentials")
}
func (s renewalCatalogNoSecrets) Set(string, string) error {
	s.t.Error("renewal catalog fixture wrote a secret")
	return errors.New("synthetic fixture has no credentials")
}
func (s renewalCatalogNoSecrets) Delete(string) error {
	s.t.Error("renewal catalog fixture deleted a secret")
	return errors.New("synthetic fixture has no credentials")
}

// Real store lifecycle, fabricated provider results only. The old task remains
// frozen at generation 1 with unknown dispatches and consumed 2/7 budget.
func sessionRenewalCatalogBrowserFixture(t *testing.T) (*fixture, domain.ModelAccount, domain.ModelRoute, domain.Mission, string) {
	t.Helper()
	f, a, r, _ := operatorHTTPFixture(t)
	a.Name = "SYNTHETIC renewed account — metadata only"
	var err error
	a, err = f.store.SaveModelAccount(a, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	catalogID := continuationHTTPCatalog(t, f, a, r, "synthetic-renewal-catalog-grant")
	catalog, err := f.store.GetModelAcceptance(a.ID, catalogID)
	if err != nil {
		t.Fatal(err)
	}
	p := domain.Profile{ID: "synthetic-renewal-profile", Name: "SYNTHETIC original catalog", Kind: "direct", Protocol: r.Protocol, BaseURL: r.BaseURL, AccountID: a.ID, RouteID: r.ID, Model: "gpt-5.1", CatalogOperationID: catalogID}
	if err = f.store.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutSettings(domain.Settings{DefaultProfileID: p.ID, RoleBindings: map[domain.Role]string{}}); err != nil {
		t.Fatal(err)
	}
	def, profiles, snapshot, err := f.store.ResolveMissionModels("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := f.store.CreateMission(domain.Mission{ID: "synthetic-renewal-original-task", Title: "SYNTHETIC original task — unchanged 2/7 budget", Goal: "Metadata-only renewal/catalog fixture. Original unknown attempts are never retried.", RepoPath: filepath.Join(f.dataDir, "synthetic-not-opened"), BaseCommit: strings.Repeat("a", 40), State: "planning", Stage: "plan", Epoch: 1, CodingVersion: 1, WorkspaceMode: "generations_account_snapshot_v2", Spec: domain.Spec{ProfileID: def, MaxCalls: 7}, Profiles: profiles, ModelSnapshot: snapshot, Budget: domain.Budget{MaxCalls: 7}, Approvals: []domain.Approval{{Kind: "context", Digest: strings.Repeat("b", 64)}}})
	if err != nil {
		t.Fatal(err)
	}
	for ordinal := 1; ordinal <= 2; ordinal++ {
		_, invocation, err := f.store.BeginInvocation(continuationBrowserIntent(t, m, fmt.Sprintf("synthetic-renewal-unknown-%d", ordinal)), m.Revision)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.ClaimInvocation(invocation.ID, invocation.Epoch); err != nil {
			t.Fatal(err)
		}
		if err = f.store.RecoverInvocations(m.ID); err != nil {
			t.Fatal(err)
		}
		m, err = f.store.GetMission(m.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	m, err = f.store.UpdateMission(m.ID, m.Revision, "fixture.blocked", func(m *domain.Mission) error { m.State = "blocked"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	before := diagnosticDurableSnapshot(t, f)
	auth, err := f.store.CurrentModelAuthorization(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	material, err := f.store.PrepareSessionRenewal(domain.SessionRenewalMaterial{ID: "synthetic-catalog-renewal", AccountID: a.ID, AccountRevision: a.Revision, Binding: auth.Binding, Resource: a.BaseURL, ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.ApproveSessionRenewal(a.ID, material.ID, material.Revision, time.Now()); err != nil {
		t.Fatal(err)
	}
	policy, err := f.store.SessionRenewalPolicy(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.ClaimSessionRenewal(policy, auth.Generation, time.Now()); err != nil {
		t.Fatal(err)
	}
	op, err := f.store.BeginModelAuthorization(a.ID, "synthetic-catalog-refresh", "refresh", auth.Binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := f.store.CommitModelAuthorizationSchedule(op.ID, auth.Binding, time.Now().Add(2*time.Hour).Format(time.RFC3339), "", policy.Revision)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := f.store.PendingModelAuthorizationCleanup()
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range pending {
		if err = f.store.CompleteModelAuthorizationCleanup(old.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.store.FinishSessionRenewal(auth.Binding.ID, auth.Generation, next.Generation, "succeeded", "", "confirmed", time.Now()); err != nil {
		t.Fatal(err)
	}
	if before != diagnosticDurableSnapshot(t, f) {
		t.Fatal("renewal changed original task/evidence/budget")
	}
	s := f.handler.(*server)
	s.codingWorkbench = true
	s.accounts = accounts.New(f.store, renewalCatalogNoSecrets{t})
	_ = s.siwc.Close()
	s.siwc = accounts.NewSIWCService(s.accounts)
	t.Cleanup(func() { _ = s.siwc.Close() })
	s.sessionRenewal = accounts.NewSessionRenewal(s.siwc, time.Now)
	t.Cleanup(s.sessionRenewal.Close)
	s.desktopAccounts = acceptance.NewDesktopCoordinator(f.store, acceptance.DesktopOptions{SIWC: s.siwc, Resolver: s.accounts, Probe: func() error {
		t.Error("catalog UI triggered connection diagnostic")
		return errors.New("fixture diagnostic unavailable")
	}})
	t.Cleanup(func() { _ = s.desktopAccounts.Close() })
	return f, a, r, m, catalog.Material.ConnectionID
}

func TestSessionRenewalCatalogBrowserPreparationIsMetadataOnly(t *testing.T) {
	f, a, route, mission, connectionID := sessionRenewalCatalogBrowserFixture(t)
	before := diagnosticDurableSnapshot(t, f)
	operations, err := f.store.ListModelAcceptance(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/model-accounts/" + a.ID
	rr := f.request("GET", base+"/session-renewal", "", true)
	var status domain.SessionRenewalStatus
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &status) != nil || !status.Enabled || status.AccessState != "usable" || status.Receipt == nil || status.Receipt.NewGeneration != 2 || status.Receipt.Certainty != "confirmed" {
		t.Fatal("renewed account unavailable", rr.Code, rr.Body.String())
	}
	rr = f.request("POST", base+"/desktop-preparation", fmt.Sprintf(`{"phase":"catalog","route_id":%q,"expected_account_revision":%d}`, route.ID, a.Revision), true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"catalog_ready":true`) || !strings.Contains(rr.Body.String(), `"catalog_choices":[]`) {
		t.Fatal("renewed catalog preparation unavailable or stale catalog inherited", rr.Code, rr.Body.String())
	}
	rr = f.request("POST", base+"/acceptance-operations", fmt.Sprintf(`{"phase":"catalog","route_id":%q,"expected_account_revision":%d,"prerequisite_id":%q}`, route.ID, a.Revision, connectionID), true)
	if rr.Code != 200 && rr.Code != 201 {
		t.Fatal("catalog proposal failed", rr.Code, rr.Body.String())
	}
	after, err := f.store.ListModelAcceptance(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(operations)+1 {
		t.Fatal("proposal did not create exactly one independent operation")
	}
	pending := 0
	for _, o := range after {
		if o.State == "pending" {
			pending++
			if o.Attempts != 0 || o.ClaimSequence != 0 || o.Material.Authorization.Generation != 2 || o.Phase != "catalog" {
				t.Fatal("proposal inherited dispatch authority")
			}
		}
	}
	if pending != 1 {
		t.Fatal("expected one pending independent approval")
	}
	for _, old := range operations {
		got, e := f.store.GetModelAcceptance(a.ID, old.ID)
		if e != nil || domain.AcceptanceDigest(got) != domain.AcceptanceDigest(old) {
			t.Fatal("old connection/catalog receipt changed", e)
		}
		if old.Phase == "catalog" {
			if _, e = f.store.PrepareModelContinuation(mission.ID, mission.Revision, "synthetic-renewal-catalog-grant", old.ID, time.Now()); e == nil {
				t.Fatal("renewal made the old catalog usable for task continuation")
			}
		}
	}
	if before != diagnosticDurableSnapshot(t, f) {
		t.Fatal("preparation changed original task/evidence/budget")
	}
	got, err := f.store.GetMission(mission.ID)
	if err != nil || got.Budget.UsedCalls != 2 || got.Budget.MaxCalls != 7 || len(got.ModelContinuations) != 0 {
		t.Fatal("original budget or continuation changed", err)
	}
}

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
	renewedCatalog := os.Getenv("FOUNDRY_TEST_SESSION_RENEWAL_CATALOG_BROWSER") == "1"
	if os.Getenv("FOUNDRY_TEST_SESSION_RENEWAL_BROWSER") != "1" && !renewedCatalog {
		t.Skip("explicit synthetic browser fixture only")
	}
	var f *fixture
	var a domain.ModelAccount
	if renewedCatalog {
		var mission domain.Mission
		f, a, _, mission, _ = sessionRenewalCatalogBrowserFixture(t)
		fmt.Printf("SESSION_RENEWAL_CATALOG_BROWSER_FIXTURE task=%s (original unknown 2/7; generation 2; prepare catalog then inspect independent pending approval only)\n", mission.ID)
	} else {
		f, a = sessionRenewalBrowserFixture(t)
	}
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
