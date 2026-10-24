package httpapi

import (
	"encoding/json"
	"foundry/internal/accounts"
	"foundry/internal/desktophost"
	"foundry/internal/domain"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionRenewalHTTPApprovalIsMetadataOnly(t *testing.T) {
	f, a, _, _ := operatorHTTPFixture(t)
	s := f.handler.(*server)
	s.codingWorkbench = true
	s.sessionRenewal = accounts.NewSessionRenewal(s.siwc, time.Now)
	defer s.sessionRenewal.Close()
	b := domain.ModelAuthorizationBinding{ID: "synthetic-renewal-binding", AccountID: a.ID, Issuer: "https://auth.openai.com", Subject: "synthetic", ClientID: "oaiapp_synthetic", SpendClass: "subscription_only", Scopes: []string{"openid", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}}
	op, err := f.store.BeginModelAuthorization(a.ID, "synthetic-renewal-signin", "sign_in", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CommitModelAuthorization(op.ID, b, time.Now().Add(time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	path := "/api/model-accounts/" + a.ID + "/session-renewal"
	rr := f.request("POST", path, `{"action":"prepare","expected_account_revision":1}`, true)
	if rr.Code != 200 {
		t.Fatalf("prepare: %d %s", rr.Code, rr.Body.String())
	}
	var status domain.SessionRenewalStatus
	if err = json.Unmarshal(rr.Body.Bytes(), &status); err != nil || status.Material == nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"action": "approve", "material_id": status.Material.ID, "expected_revision": status.Revision})
	rr = f.request("POST", path, string(body), true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"enabled":true`) {
		t.Fatalf("approve: %d %s", rr.Code, rr.Body.String())
	}
	if rr = f.request("POST", path, string(body), true); rr.Code != 409 {
		t.Fatalf("stale: %d", rr.Code)
	}
	// forbiddenSecrets in operatorHTTPFixture fails the test on any credential I/O.
}

func TestSessionRenewalHTTPClosedAndProtected(t *testing.T) {
	f, a, _, _ := operatorHTTPFixture(t)
	s := f.handler.(*server)
	path := "/api/model-accounts/" + a.ID + "/session-renewal"
	if rr := f.request("GET", path, "", true); rr.Code != 403 {
		t.Fatalf("default capability: %d", rr.Code)
	}
	s.codingWorkbench = true
	s.sessionRenewal = accounts.NewSessionRenewal(s.siwc, time.Now)
	defer s.sessionRenewal.Close()
	if rr := f.request("GET", path, "", true); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"enabled":false`) {
		t.Fatalf("metadata: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request("GET", path, "", false); rr.Code != 401 {
		t.Fatal("session bypass")
	}
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, testOrigin+path, strings.NewReader(`{"action":"prepare","expected_account_revision":1}`))
		r.AddCookie(f.cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://untrusted.example")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, r)
		if rr.Code != 403 {
			t.Fatal("origin bypass")
		}
	}
	r := httptest.NewRequest("GET", testOrigin+path, nil)
	r.AddCookie(f.cookie)
	rr := httptest.NewRecorder()
	desktophost.Protect("synthetic", s).ServeHTTP(rr, r)
	if rr.Code != 403 {
		t.Fatal("desktop capability bypass")
	}
	for _, body := range []string{`{"action":"prepare","expected_account_revision":1,"enabled":true}`, `{"action":"tick"}`, `{"action":"prepare","expected_account_revision":1,"action":"approve"}`} {
		if rr := f.request("POST", path, body, true); rr.Code != 400 {
			t.Fatalf("unsafe JSON %s: %d", body, rr.Code)
		}
	}
	s.recoveryReadOnly = true
	if rr := f.request("POST", path, `{"action":"disable","expected_revision":1}`, true); rr.Code != 503 {
		t.Fatalf("read-only: %d", rr.Code)
	}
}
