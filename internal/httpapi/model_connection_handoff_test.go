package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"foundry/internal/acceptance"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBrowserHandoffHTTPExplicitReadCancelAndStrictInputs(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a, err := f.store.SaveModelAccount(domain.ModelAccount{ID: "browser", Name: "browser", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "http://127.0.0.1:12345/v1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.store.CreateModelRoute(domain.ModelRoute{ID: "browser-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	const origin = "http://127.0.0.1:1" // no provider requests are needed for admission/cancellation
	siwc, err := accounts.NewSIWCSimulation(accounts.New(f.store, f.secrets), accounts.SIWCOptions{AuthorizationOrigin: origin, TokenOrigin: origin, OIDCOrigin: origin, JWKSOrigin: origin, RevocationOrigin: origin, Issuer: origin, FlowTimeout: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer siwc.Close()
	svc := acceptance.NewSimulation(f.store, acceptance.Options{Routes: []domain.ModelRoute{r}, SubscriptionConnectionRouteIDs: []string{r.ID}, BrowserHandoffRouteIDs: []string{r.ID}, BrowserSIWC: siwc, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(context.Context, domain.ModelAcceptanceMaterial) error {
		t.Error("legacy connector called")
		return nil
	}})
	defer svc.Close()
	if err = svc.Recover(); err != nil {
		t.Fatal(err)
	}
	f.handler.(*server).acceptance = svc
	path := "/api/model-accounts/browser/acceptance-operations"
	if rr := f.request("GET", path, "", true); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"connection_mode":"browser_handoff"`) {
		t.Fatalf("route=%s", rr.Body.String())
	}
	proposal := `{"phase":"connection","connection_purpose":"subscription_only","route_id":"browser-route","expected_account_revision":1}`
	for _, extra := range []string{`,"connection_mode":"browser_handoff"`, `,"login_timeout_seconds":600`, `,"deadline_seconds":30`} {
		if rr := f.request("POST", path, strings.TrimSuffix(proposal, "}")+extra+"}", true); rr.Code != 400 {
			t.Fatal("client policy override accepted")
		}
	}
	decode := func(rr *httptest.ResponseRecorder) (domain.ModelAcceptanceOperation, *acceptance.ConnectionSession) {
		t.Helper()
		if rr.Code != 200 {
			t.Fatalf("response=%d %s", rr.Code, rr.Body.String())
		}
		var payload struct {
			Operation domain.ModelAcceptanceOperation `json:"operation"`
			Session   *acceptance.ConnectionSession   `json:"connection_session"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Operation, payload.Session
	}
	o, session := decode(f.request("POST", path, proposal, true))
	if session != nil {
		t.Fatal("proposal created session")
	}
	opPath := path + "/" + o.ID
	mutation := func(o domain.ModelAcceptanceOperation) string {
		return fmt.Sprintf(`{"expected_revision":%d,"material_digest":%q}`, o.Revision, o.MaterialDigest)
	}
	o, session = decode(f.request("POST", opPath+"/decision", strings.TrimSuffix(mutation(o), "}")+`,"decision":"approve"}`, true))
	if session != nil {
		t.Fatal("approval created session")
	}
	o, session = decode(f.request("POST", opPath+"/execute", mutation(o), true))
	if o.State != "running" || session == nil || session.OperationID != o.ID || session.AccountID != a.ID || session.State != "waiting" || !strings.HasSuffix(session.HandoffURL, "/auth/start") {
		t.Fatal("unsafe session response")
	}
	first := *session
	_, session = decode(f.request("GET", opPath, "", true))
	if session == nil || *session != first {
		t.Fatal("status read changed handoff")
	}
	for _, forbidden := range []string{"nonce", "code_verifier", "authorization_url", "access_token", "refresh_token", "secret_ref", "state="} {
		raw, _ := json.Marshal(session)
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("session exposed protocol data")
		}
	}
	if rr := f.request("POST", opPath+"/cancel", mutation(o), false); rr.Code == 200 {
		t.Fatal("unauthenticated cancel accepted")
	}
	if rr := f.request("POST", opPath+"/cancel?retry=1", mutation(o), true); rr.Code != 400 {
		t.Fatal("query accepted")
	}
	if rr := f.request("POST", opPath+"/cancel", strings.TrimSuffix(mutation(o), "}")+`,"retry":true}`, true); rr.Code != 400 {
		t.Fatal("unknown cancel field accepted")
	}
	f.handler.(*server).recoveryReadOnly = true
	if rr := f.request("POST", opPath+"/cancel", mutation(o), true); rr.Code == 200 {
		t.Fatal("readonly cancel accepted")
	}
	f.handler.(*server).recoveryReadOnly = false
	for _, bad := range []struct{ host, origin string }{{"evil.example", "http://127.0.0.1:4317"}, {"127.0.0.1:4317", "https://evil.example"}} {
		req := httptest.NewRequest("POST", testOrigin+opPath+"/cancel", strings.NewReader(mutation(o)))
		req.Host = bad.host
		req.Header.Set("Origin", bad.origin)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(f.cookie)
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, req)
		if rr.Code == 200 {
			t.Fatal("host/origin bypass accepted")
		}
	}
	o, session = decode(f.request("POST", opPath+"/cancel", mutation(o), true))
	if o.State != "unknown" || o.Attempts != 1 || session != nil {
		t.Fatal("cancel did not consume/remove session")
	}
	if rr := f.request("POST", opPath+"/cancel", mutation(o), true); rr.Code != 409 {
		t.Fatal("cancel replay accepted")
	}
	_, session = decode(f.request("GET", opPath, "", true))
	if session != nil {
		t.Fatal("read restored terminal handoff")
	}
}
