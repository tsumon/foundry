package httpapi

import (
	"encoding/json"
	"foundry/internal/acceptance"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogHistoryMetadataOnly(t *testing.T) {
	f, a, r, _ := operatorHTTPFixture(t)
	s := f.handler.(*server)
	s.acceptance = acceptance.NewProductionCatalog(f.store, acceptance.ProductionCatalogOptions{Route: r, Resolver: s.accounts})
	if e := s.acceptance.Recover(); e != nil {
		t.Fatal(e)
	}
	response := f.request(http.MethodGet, "/api/model-accounts/"+a.ID+"/acceptance-operations", "", true)
	var body struct {
		Catalog    bool   `json:"production_catalog_enabled"`
		Blocker    string `json:"production_catalog_blocker"`
		Connection bool   `json:"production_connection_enabled"`
		Admission  *struct {
			Profile string `json:"profile"`
		} `json:"production_admission"`
	}
	if json.Unmarshal(response.Body.Bytes(), &body) != nil || response.Code != 200 || body.Catalog || body.Connection || body.Blocker != "authorization_unavailable" || body.Admission == nil || body.Admission.Profile != "openai_siwc_catalog_v1" {
		t.Fatalf("wrong catalog history: %d %s", response.Code, response.Body.String())
	}
}

func TestCatalogHTTPRejectsUntrustedAuthorityAndCSRF(t *testing.T) {
	f, a, r, _ := operatorHTTPFixture(t)
	s := f.handler.(*server)
	s.acceptance = acceptance.NewProductionCatalog(f.store, acceptance.ProductionCatalogOptions{Route: r, Resolver: s.accounts})
	if e := s.acceptance.Recover(); e != nil {
		t.Fatal(e)
	}
	path := "/api/model-accounts/" + a.ID + "/acceptance-operations"
	if rr := f.request("GET", path, "", false); rr.Code != 401 {
		t.Fatal("unauthenticated history")
	}
	for _, field := range []string{"production_admission", "request_url", "authorization", "source", "live_access", "credential"} {
		body := `{"phase":"catalog","route_id":"production-route","expected_account_revision":1,"` + field + `":"forged"}`
		if rr := f.request("POST", path, body, true); rr.Code != 400 {
			t.Fatalf("accepted authority field %s: %d", field, rr.Code)
		}
	}
	for _, kind := range []string{"origin", "host", "content type"} {
		req := httptest.NewRequest("POST", testOrigin+path, strings.NewReader(`{"phase":"catalog","route_id":"production-route","expected_account_revision":1}`))
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
