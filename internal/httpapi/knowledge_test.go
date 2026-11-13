package httpapi

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/knowledge"
	"net/http"
	"testing"
)

func TestKnowledgeAuthorizationAndLocalOnly(t *testing.T) {
	f := newCatalogFixture(t)
	for _, route := range []string{"/api/knowledge", "/api/knowledge/schema", "/api/knowledge/rebuild"} {
		method := http.MethodGet
		if route == "/api/knowledge/rebuild" {
			method = http.MethodPost
		}
		if rr := f.request(method, route, "{}", false); rr.Code != http.StatusUnauthorized {
			t.Fatal(route, rr.Code)
		}
	}
	f.authenticate()
	for _, route := range []string{"/api/knowledge", "/api/knowledge/schema"} {
		if rr := f.requestWith(http.MethodGet, route, "", true, "127.0.0.1:4317", "https://evil.invalid", "application/json"); rr.Code != http.StatusForbidden {
			t.Fatal(route, rr.Code)
		}
	}
	if rr := f.requestWith(http.MethodPost, "/api/knowledge/rebuild", "{}", true, "127.0.0.1:4317", "https://evil.invalid", "application/json"); rr.Code != http.StatusForbidden {
		t.Fatal(rr.Code)
	}
	// Fixture model, runner and secrets panic on any call.
	for _, route := range []string{"/api/knowledge", "/api/knowledge/schema"} {
		if rr := f.request(http.MethodGet, route, "", true); rr.Code != http.StatusOK {
			t.Fatal(route, rr.Code, rr.Body.String())
		}
	}
	if rr := f.request(http.MethodPost, "/api/knowledge/rebuild", "{}", true); rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for _, query := range []string{"?depth=5", "?limit=201", "?mode=sql", "?depth=0", "?limit=x", "?root=a&root=b", "?unknown=1", "?q=%xx"} {
		if rr := f.request(http.MethodGet, "/api/knowledge"+query, "", true); rr.Code != http.StatusBadRequest {
			t.Fatal(query, rr.Code)
		}
	}
	for _, body := range []string{`{"sql":"DELETE"}`, `{} {}`, `null`} {
		if rr := f.request(http.MethodPost, "/api/knowledge/rebuild", body, true); rr.Code != http.StatusBadRequest {
			t.Fatal(body, rr.Code)
		}
	}
	srv := f.handler.(*server)
	srv.c4ControlledOnly = true
	for _, route := range []string{"/api/knowledge", "/api/knowledge/schema", "/api/knowledge/rebuild"} {
		method := http.MethodGet
		if route == "/api/knowledge/rebuild" {
			method = http.MethodPost
		}
		if rr := f.request(method, route, "{}", true); rr.Code != http.StatusForbidden {
			t.Fatal(route, rr.Code)
		}
	}
}

func TestKnowledgeRecoveryReadsDoNotSync(t *testing.T) {
	f := newCatalogFixture(t)
	f.authenticate()
	if err := f.store.SyncKnowledge(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.QueryKnowledge(context.Background(), knowledge.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CreateMission(domain.Mission{ID: "late", Title: "new task", State: "draft"}); err != nil {
		t.Fatal(err)
	}
	f.handler.(*server).recoveryReadOnly = true
	rr := f.request(http.MethodGet, "/api/knowledge", "", true)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var g knowledge.Graph
	if err = json.Unmarshal(rr.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if g.SourceDigest != before.SourceDigest || len(g.Nodes) != len(before.Nodes) {
		t.Fatal("recovery synchronized business source")
	}
	if rr = f.request(http.MethodPost, "/api/knowledge/rebuild", "{}", true); rr.Code != http.StatusServiceUnavailable {
		t.Fatal(rr.Code)
	}
	f.handler.(*server).recoveryReadOnly = false
	if rr = f.request(http.MethodGet, "/api/knowledge", "", true); rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	json.Unmarshal(rr.Body.Bytes(), &g)
	if g.SourceDigest == before.SourceDigest {
		t.Fatal("normal read did not sync")
	}
}
