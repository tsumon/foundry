package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"foundry/internal/domain"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestModelContinuationHTTPCandidatesScopedReadOnly(t *testing.T) {
	f, missions := continuationBrowserSeed(t)
	_, secretRef := seedHTTPAuthorization(t, f) // unrelated account must not leak
	before := diagnosticDurableSnapshot(t, f)
	grantsBefore, err := f.store.ListModelAuthorizations()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"ready", "applied"} {
		path := "/api/missions/" + missions[mode].ID + "/model-continuations/candidates"
		rr := f.request("GET", path, "", true)
		if rr.Code != 200 {
			t.Fatalf("candidate projection: %d %s", rr.Code, rr.Body.String())
		}
		var body struct {
			Authorizations []map[string]any `json:"authorizations"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Authorizations) != 1 {
			t.Fatal("not scoped to task account", rr.Body.String())
		}
		grant := body.Authorizations[0]
		if len(grant) != 5 || grant["id"] != "synthetic-replacement-grant" || grant["account_id"] != "production" || grant["state"] != "available" || grant["generation"] == nil || grant["expires_at"] == nil {
			t.Fatal("unexpected candidate fields", rr.Body.String())
		}
		for _, forbidden := range []string{"subject", "client_id", "issuer", "scopes", "secret", "token", "private-client", "private-subject", "oauth-account", secretRef} {
			if strings.Contains(rr.Body.String(), forbidden) {
				t.Fatal("private/unrelated metadata escaped", forbidden)
			}
		}
		for _, tc := range []struct {
			method, suffix, body string
			status               int
		}{
			{"GET", "?account_id=oauth-account", "", 400}, {"GET", "?", "", 400},
			{"GET", "/extra", "", 404}, {"POST", "", `{}`, 404}, {"GET", "", `{}`, 400},
		} {
			if rr := f.request(tc.method, path+tc.suffix, tc.body, true); rr.Code != tc.status {
				t.Fatal(tc, rr.Code, rr.Body.String())
			}
		}
		if rr := f.request("GET", path, "", false); rr.Code != 401 {
			t.Fatal("unauthenticated read", rr.Code)
		}
	}
	if rr := f.request("GET", "/api/missions/missing/model-continuations/candidates", "", true); rr.Code != 404 {
		t.Fatal(rr.Code)
	}
	// Account settings intentionally retains its existing identity contract.
	general := f.request("GET", "/api/model-authorizations", "", true)
	if general.Code != 200 || !strings.Contains(general.Body.String(), `"subject"`) || !strings.Contains(general.Body.String(), `"client_id"`) {
		t.Fatal("changed account-management contract")
	}
	grantsAfter, err := f.store.ListModelAuthorizations()
	if err != nil || !reflect.DeepEqual(grantsBefore, grantsAfter) || before != diagnosticDurableSnapshot(t, f) {
		t.Fatal("candidate reads mutated persisted state", err)
	}
}

// Persist deliberately malformed history in temporary SQLite; do not bypass the
// production decoder/projection by constructing a pre-sanitized Mission view.
func corruptContinuationBrowserHistory(t *testing.T, f *fixture, m domain.Mission) domain.Mission {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	data["model_continuations"] = json.RawMessage(`{"unexpected":"private-malformed-history"}`)
	raw, err = json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("UPDATE missions SET data=? WHERE id=?", string(raw), m.ID); err != nil {
		t.Fatal(err)
	}
	m, err = f.store.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestModelContinuationHTTPMalformedHistoryPreservesOriginalDisplay(t *testing.T) {
	f, missions := continuationBrowserSeed(t)
	m := missions["ready"]
	original, _ := json.Marshal(modelSnapshotView(m.ModelSnapshot))
	m = corruptContinuationBrowserHistory(t, f, m)
	before := diagnosticDurableSnapshot(t, f)
	rr := f.request("GET", "/api/missions/"+m.ID, "", true)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var body struct {
		Mission map[string]json.RawMessage `json:"mission"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	contract, err := os.ReadFile("../../web/src/test/modelContinuationInvalidHistoryContract.json")
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]json.RawMessage
	if err = json.Unmarshal(contract, &status); err != nil {
		t.Fatal(err)
	}
	for key, expected := range status {
		if !bytes.Equal(body.Mission[key], expected) {
			t.Errorf("HTTP/UI contract %s: got %s, want %s", key, body.Mission[key], expected)
		}
	}
	if !bytes.Equal(body.Mission["model_snapshot"], original) || body.Mission["effective_model_snapshot"] != nil || body.Mission["model_continuations"] != nil || strings.Contains(rr.Body.String(), "private-malformed-history") {
		t.Fatal("original hidden or malformed authority exposed", rr.Body.String())
	}
	if err = f.store.CheckModelCall(m.ID, domain.Planner); err == nil {
		t.Fatal("invalid authority permits model admission")
	}
	if rr := f.request("GET", "/api/missions/"+m.ID+"/model-continuations/candidates", "", true); rr.Code != 409 {
		t.Error("invalid history candidates", rr.Code)
	}
	if rr := f.request("POST", "/api/missions/"+m.ID+"/model-continuations", fmt.Sprintf(`{"revision":%d,"authorization_id":"synthetic-replacement-grant","catalog_operation_id":"synthetic-catalog"}`, m.Revision), true); rr.Code != 409 {
		t.Error("invalid history prepare", rr.Code, rr.Body.String())
	}
	for _, action := range []string{"approve", "apply"} {
		if rr := f.request("POST", "/api/missions/"+m.ID+"/model-continuations/material/"+action, fmt.Sprintf(`{"revision":%d,"digest":%q}`, m.Revision, strings.Repeat("c", 64)), true); rr.Code != 409 {
			t.Error("invalid history continuation action", action, rr.Code, rr.Body.String())
		}
	}
	for _, action := range []string{"plan", "approve"} {
		if rr := f.request("POST", "/api/missions/"+m.ID+"/"+action, fmt.Sprintf(`{"revision":%d}`, m.Revision), true); rr.Code != 409 {
			t.Error("invalid history model action", action, rr.Code, rr.Body.String())
		}
	}
	if before != diagnosticDurableSnapshot(t, f) {
		t.Fatal("invalid history changed durable state")
	}
}

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
