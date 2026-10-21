package httpapi

import (
	"encoding/json"
	"foundry/internal/acceptance"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInferenceHistoryMetadataOnly(t *testing.T) {
	f, a, r, _ := operatorHTTPFixture(t)
	s := f.handler.(*server)
	s.acceptance = acceptance.NewProductionInference(f.store, acceptance.ProductionInferenceOptions{Route: r, Resolver: s.accounts})
	if e := s.acceptance.Recover(); e != nil {
		t.Fatal(e)
	}
	response := f.request(http.MethodGet, "/api/model-accounts/"+a.ID+"/acceptance-operations", "", true)
	var body struct {
		Inference  bool   `json:"production_inference_enabled"`
		Blocker    string `json:"production_inference_blocker"`
		Connection bool   `json:"production_connection_enabled"`
		Admission  *struct {
			Profile string `json:"profile"`
		} `json:"production_admission"`
	}
	if json.Unmarshal(response.Body.Bytes(), &body) != nil || response.Code != 200 || body.Inference || body.Connection || body.Blocker != "authorization_unavailable" || body.Admission == nil || body.Admission.Profile != "openai_siwc_inference_v1" {
		t.Fatalf("wrong inference history: %d %s", response.Code, response.Body.String())
	}
	s.recoveryReadOnly = true
	response = f.request(http.MethodGet, "/api/model-accounts/"+a.ID+"/acceptance-operations", "", true)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"production_inference_enabled":false`) || !strings.Contains(response.Body.String(), `"production_inference_blocker":"admission_unavailable"`) {
		t.Fatal("recovery projection not closed")
	}
	response = f.request(http.MethodPost, "/api/model-accounts/"+a.ID+"/acceptance-operations", `{}`, true)
	if response.Code != 503 {
		t.Fatal("recovery mutation allowed")
	}
}

func TestInferenceHTTPRejectsUntrustedAuthorityAndCSRF(t *testing.T) {
	f, a, r, _ := operatorHTTPFixture(t)
	s := f.handler.(*server)
	s.acceptance = acceptance.NewProductionInference(f.store, acceptance.ProductionInferenceOptions{Route: r, Resolver: s.accounts})
	if e := s.acceptance.Recover(); e != nil {
		t.Fatal(e)
	}
	path := "/api/model-accounts/" + a.ID + "/acceptance-operations"
	if rr := f.request("POST", path, `{"phase":"inference","route_id":"production-route","expected_account_revision":1,"prerequisite_id":"missing","model":"gpt-6.1-sol"}`, true); rr.Code != 403 {
		t.Fatalf("fixed model rejected before gate: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request("GET", path, "", false); rr.Code != 401 {
		t.Fatal("unauthenticated history")
	}
	for _, field := range []string{"production_admission", "request_url", "authorization", "source", "live_access", "credential", "instructions", "input", "inference_prompt", "response_byte_limit", "max_output_tokens", "stream", "store", "tools"} {
		body := `{"phase":"inference","route_id":"production-route","expected_account_revision":1,"` + field + `":"forged"}`
		if rr := f.request("POST", path, body, true); rr.Code != 400 {
			t.Fatalf("accepted authority field %s: %d", field, rr.Code)
		}
	}
	for _, kind := range []string{"origin", "host", "content type"} {
		req := httptest.NewRequest("POST", testOrigin+path, strings.NewReader(`{"phase":"inference","route_id":"production-route","expected_account_revision":1}`))
		req.AddCookie(f.cookie)
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("Content-Type", "application/json")
		switch kind {
		case "origin":
			req.Header.Set("Origin", "https://evil.invalid")
		case "host":
			req.Host = "evil.invalid"
		case "content type":
			req.Header.Set("Content-Type", "text/plain")
		}
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, req)
		if rr.Code < 400 {
			t.Fatalf("accepted %s", kind)
		}
	}
	items, e := s.acceptance.List(a.ID)
	if e != nil || len(items) != 0 {
		t.Fatal("rejected HTTP request mutated history")
	}
}
