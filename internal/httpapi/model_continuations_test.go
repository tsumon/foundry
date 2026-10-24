package httpapi

import (
	"encoding/json"
	"fmt"
	"foundry/internal/domain"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestModelContinuationHTTPStrictIndependentActions(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	for _, path := range []string{"/api/missions/m/model-continuations", "/api/missions/m/model-continuations/c/approve", "/api/missions/m/model-continuations/c/apply"} {
		for _, body := range []string{`{}`, `{"revision":1}`, `{"revision":1,"revision":2,"digest":"d"}`, `{"Revision":1,"digest":"d"}`, `{"revision":null,"digest":"d"}`, `{"revision":1,"digest":"d","token":"forged"}`} {
			if rr := f.request("POST", path, body, true); rr.Code != 400 {
				t.Fatalf("strict action %s accepted/misrouted %s: %d %s", path, body, rr.Code, rr.Body.String())
			}
		}
		if rr := f.request("POST", path, `{}`, false); rr.Code != 401 {
			t.Fatal("unauthenticated mutation")
		}
		req := httptest.NewRequest("POST", testOrigin+path, strings.NewReader(`{}`))
		req.AddCookie(f.cookie)
		req.Header.Set("Origin", "https://evil.invalid")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, req)
		if rr.Code != 403 {
			t.Fatal("cross-origin mutation")
		}
	}
}

// Only store metadata is exercised: production lifecycle, synthetic dispatch
// result, no resolver, provider request, login, secret read or VM operation.
func continuationHTTPCatalog(t *testing.T, f *fixture, a domain.ModelAccount, r domain.ModelRoute, grant string) string {
	t.Helper()
	now := time.Now()
	admission := domain.ProductionAdmission{Generation: strings.Repeat("a", 32), Profile: "openai_siwc_subscription_v1"}
	connection, err := f.store.ProposeProductionConnection(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, admission, now)
	if err != nil {
		t.Fatal(err)
	}
	connection, err = f.store.DecideModelAcceptance(a.ID, connection.ID, connection.Revision, connection.MaterialDigest, "approve", now)
	if err != nil {
		t.Fatal(err)
	}
	connection, err = f.store.ClaimModelAcceptance(a.ID, connection.ID, connection.Revision, connection.MaterialDigest, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.ReserveProductionConnectionDispatch(connection, admission, now); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.BeginModelAuthorization(a.ID, grant+"-signin", "sign_in", grant); err != nil {
		t.Fatal(err)
	}
	binding := domain.ModelAuthorizationBinding{ID: grant, AccountID: a.ID, Issuer: "https://auth.openai.com", Subject: "private-subject", ClientID: "oaiapp_private-client", SpendClass: "subscription_only", Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}}
	if _, err = f.store.CommitModelAuthorization(grant+"-signin", binding, now.Add(time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	pending, err := f.store.PendingModelAuthorizationCleanup()
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range pending {
		if err = f.store.CompleteModelAuthorizationCleanup(op.ID); err != nil {
			t.Fatal(err)
		}
	}
	connection, err = f.store.FinishProductionConnection(connection, "succeeded", "completed", "", now)
	if err != nil {
		t.Fatal("finish connection", err)
	}
	admission.Profile = "openai_siwc_catalog_v1"
	catalog, err := f.store.ProposeProductionCatalog(a.ID, domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: connection.ID}, admission, now)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err = f.store.DecideModelAcceptance(a.ID, catalog.ID, catalog.Revision, catalog.MaterialDigest, "approve", now)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err = f.store.ClaimModelAcceptance(a.ID, catalog.ID, catalog.Revision, catalog.MaterialDigest, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.ReserveProductionCatalogDispatch(catalog, admission, now); err != nil {
		t.Fatal(err)
	}
	catalog, err = f.store.FinishProductionCatalog(catalog, "succeeded", "completed", "", []domain.ModelAcceptanceCatalogEntry{{ID: "gpt-5.1", DisplayName: "Fixture"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.ID
}

func TestModelContinuationHTTPThreeIndependentOperations(t *testing.T) {
	f, a, r, _ := operatorHTTPFixture(t)
	catalog := continuationHTTPCatalog(t, f, a, r, "original-proof")
	p := domain.Profile{ID: "continuation-profile", Name: "Fixture", Kind: "direct", Protocol: r.Protocol, BaseURL: r.BaseURL, AccountID: a.ID, RouteID: r.ID, Model: "gpt-5.1", CatalogOperationID: catalog}
	if err := f.store.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if err := f.store.PutSettings(domain.Settings{DefaultProfileID: p.ID, RoleBindings: map[domain.Role]string{}}); err != nil {
		t.Fatal(err)
	}
	def, profiles, snapshot, err := f.store.ResolveMissionModels("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := f.store.CreateMission(domain.Mission{ID: "http-continuation", State: "blocked", CodingVersion: 1, WorkspaceMode: "generations_account_snapshot_v2", Spec: domain.Spec{ProfileID: def}, Profiles: profiles, ModelSnapshot: snapshot, Budget: domain.Budget{MaxCalls: 7}})
	if err != nil {
		t.Fatal(err)
	}
	catalog = continuationHTTPCatalog(t, f, a, r, "replacement-proof")
	base := "/api/missions/" + m.ID + "/model-continuations"
	prepared := f.request("POST", base, fmt.Sprintf(`{"revision":%d,"authorization_id":"replacement-proof","catalog_operation_id":%q}`, m.Revision, catalog), true)
	if prepared.Code != 200 {
		t.Fatal(prepared.Code, prepared.Body.String())
	}
	var envelope struct {
		Mission SafeMissionView `json:"mission"`
	}
	if err = json.Unmarshal(prepared.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Mission.ModelContinuations) != 1 || envelope.Mission.ModelContinuations[0].State != "prepared" {
		t.Fatal("prepare activated authority")
	}
	c := envelope.Mission.ModelContinuations[0]
	revision := envelope.Mission.Revision
	action := func(name string, rev int64, digest string) *httptest.ResponseRecorder {
		return f.request("POST", base+"/"+c.ID+"/"+name, fmt.Sprintf(`{"revision":%d,"digest":%q}`, rev, digest), true)
	}
	if rr := action("apply", revision, c.Digest); rr.Code != 409 {
		t.Fatal("unapproved applied", rr.Code, rr.Body.String())
	}
	if rr := action("approve", revision-1, c.Digest); rr.Code != 409 {
		t.Fatal("stale revision accepted", rr.Code)
	}
	if rr := action("approve", revision, strings.Repeat("0", 64)); rr.Code != 409 {
		t.Fatal("wrong digest accepted", rr.Code)
	}
	approved := action("approve", revision, c.Digest)
	if approved.Code != 200 {
		t.Fatal(approved.Code, approved.Body.String())
	}
	if err = json.Unmarshal(approved.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Mission.ModelContinuations[0].State != "approved" || envelope.Mission.EffectiveModelSnapshot.Digest != snapshot.Digest {
		t.Fatal("approval activated replacement")
	}
	applied := action("apply", envelope.Mission.Revision, c.Digest)
	if applied.Code != 200 {
		t.Fatal(applied.Code, applied.Body.String())
	}
	if err = json.Unmarshal(applied.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Mission.ModelContinuations[0].State != "applied" || envelope.Mission.EffectiveModelSnapshot.Digest == snapshot.Digest || envelope.Mission.ModelSnapshot.Digest != snapshot.Digest || envelope.Mission.State != "blocked" {
		t.Fatal("apply changed original or advanced next gate")
	}
	if rr := action("apply", envelope.Mission.Revision, c.Digest); rr.Code != 409 {
		t.Fatal("duplicate apply accepted", rr.Code)
	}
	for _, rr := range []*httptest.ResponseRecorder{prepared, approved, applied} {
		for _, private := range []string{"private-subject", "private-client", "secret_ref", "access_token"} {
			if strings.Contains(rr.Body.String(), private) {
				t.Fatal("identity leak", private)
			}
		}
	}
}

func TestModelContinuationMissionProjectionNeverLeaksIdentity(t *testing.T) {
	f := newFixture(t)
	s := f.handler.(*server)
	snapshot := &domain.MissionModelSnapshot{SchemaVersion: 2, Digest: strings.Repeat("a", 64), Roles: map[domain.Role]domain.RoleModelSelection{domain.Planner: {Authorization: &domain.ModelAuthorizationBinding{ID: "safe-grant", AccountID: "safe-account", Subject: "private-subject", ClientID: "private-client", Issuer: "private-issuer", Scopes: []string{"private-scope"}}}}}
	m := domain.Mission{ID: "safe", WorkspaceMode: "account_snapshot_v2", ModelSnapshot: snapshot, ModelContinuations: []domain.ModelContinuation{{ID: "safe-continuation", Replacement: *snapshot}}}
	view, err := s.missionView(m)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-subject", "private-client", "\"subject\"", "\"client_id\"", "\"secret_ref\""} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("mission DTO leaked %s", private)
		}
	}
	if !strings.Contains(string(raw), "invalid_or_unsupported") {
		t.Fatal("malformed continuation was not visible as blocked")
	}
}
