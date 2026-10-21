package httpapi

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/sandbox"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These requests exercise the real dispatcher/middleware, not a direct handler:
// missing route, unknown body keys, or bypassed origin/session/read-only gates fail.
func TestC4RoutePermissionsAndStrictActions(t *testing.T) {
	f := newFixture(t)
	route := "/api/missions/missing/c4"
	if rr := f.request("POST", route, `{"action":"prepare","revision":1}`, false); rr.Code != 401 {
		t.Fatal(rr.Code)
	}
	f.authenticate()
	for _, body := range []string{`{"Action":"prepare","revision":1}`, `{"action":"prepare","action":"prepare","revision":1}`, `{"action":"prepare","revision":1,"mode":"docker"}`, `{"action":"prepare","revision":1,"command":"go test"}`, `{"action":"approve","revision":1}`, `{"action":"unknown","revision":1}`, `{"action":"prepare","revision":0}`, `{"action":"prepare","revision":1,"operation_id":"extra"}`} {
		if rr := f.request("POST", route, body, true); rr.Code != 400 {
			t.Fatal(rr.Code, rr.Body.String())
		}
	}
	if rr := f.request("GET", route, "", true); rr.Code != 404 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for _, mutation := range []string{"host", "origin", "content"} {
		req := httptest.NewRequest("POST", testOrigin+route, strings.NewReader(`{"action":"prepare","revision":1}`))
		req.AddCookie(f.cookie)
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("Content-Type", "application/json")
		if mutation == "host" {
			req.Host = "localhost:4317"
		}
		if mutation == "origin" {
			req.Header.Set("Origin", "https://foreign.invalid")
		}
		if mutation == "content" {
			req.Header.Set("Content-Type", "text/plain")
		}
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, req)
		if rr.Code != 403 {
			t.Fatal(mutation, rr.Code)
		}
	}
	f.handler.(*server).recoveryReadOnly = true
	if rr := f.request("POST", route, `{"action":"prepare","revision":1}`, true); rr.Code != 503 {
		t.Fatal(rr.Code)
	}
}

func TestC4HTTPPreparedApprovalFactsAndStaleBindings(t *testing.T) {
	f := newFixture(t)
	// No execution is requested in this route test. The injected driver wrapper
	// has no transport; the engine lifecycle tests cover execution itself.
	e := engine.New(f.store, catalogFailGateway{}, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}, C4: engine.NewC4SyntheticCapability(sandbox.NewC4SyntheticDriver(nil), catalogFailGateway{}, "/synthetic/docker", "unix:///synthetic/docker.sock")})
	defer e.Close()
	f.handler.(*server).engine = e
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-qb", "main"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture"}} {
		cmd := exec.Command("/usr/bin/git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	if err := f.store.PutProfile(domain.Profile{ID: "synthetic", Kind: "synthetic", Name: "synthetic", Protocol: "chat_completions", BaseURL: "https://synthetic.invalid", Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	v, err := e.InspectRepository(context.Background(), domain.RepositoryInspectionRequest{RepoPath: repo, Consent: true, Spec: domain.RepositoryImportSpec{Scope: []string{"main.go"}, Acceptance: []string{"/opt/foundry/verify-go-v1"}, Image: "synthetic"}})
	if err != nil {
		t.Fatal(err)
	}
	create := engine.CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: v.ID, Digest: v.Digest}, CodingVersion: 1, WorkspaceMode: "generations_v1", Title: "C4", Goal: "change", RepoPath: repo, Spec: domain.Spec{Scope: v.Spec.Scope, Acceptance: v.Spec.Acceptance, Image: v.Spec.Image, ProfileID: "synthetic", C4: &domain.C4Profile{SchemaVersion: 1, ID: "c4_go_v1", Mode: "synthetic", ImageID: "sha256:" + strings.Repeat("a", 64), Platform: "linux/arm64", DockerPath: "/synthetic/docker", Endpoint: "unix:///synthetic/docker.sock", Verifier: "/opt/foundry/verify-go-v1"}}}
	f.authenticate()
	raw, _ := json.Marshal(create)
	rr := f.request("POST", "/api/missions", string(raw), true)
	var result struct {
		Mission domain.Mission `json:"mission"`
	}
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &result) != nil {
		t.Fatal(rr.Code, rr.Body.String())
	}
	m := result.Mission
	route := "/api/missions/" + m.ID + "/c4"
	action := func(body map[string]any, want int) domain.Mission {
		t.Helper()
		raw, _ := json.Marshal(body)
		rr := f.request("POST", route, string(raw), true)
		if rr.Code != want {
			t.Fatal(rr.Code, rr.Body.String())
		}
		var result struct {
			Mission domain.Mission `json:"mission"`
		}
		json.Unmarshal(rr.Body.Bytes(), &result)
		return result.Mission
	}
	m = action(map[string]any{"action": "prepare", "revision": m.Revision}, 200)
	o := m.C4Operations[0]
	action(map[string]any{"action": "approve", "revision": m.Revision - 1, "operation_id": o.ID, "material_digest": o.MaterialDigest}, 409)
	action(map[string]any{"action": "approve", "revision": m.Revision, "operation_id": o.ID, "material_digest": strings.Repeat("0", 64)}, 409)
	m = action(map[string]any{"action": "approve", "revision": m.Revision, "operation_id": o.ID, "material_digest": o.MaterialDigest}, 200)
	rr = f.request("GET", route, "", true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"state":"approved"`) || !strings.Contains(rr.Body.String(), `"real_execution":false`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if len(m.Approvals) != 1 || m.C4Operations[0].Dispatch != nil {
		t.Fatal("route approval dispatched or granted context")
	}
	for _, method := range []string{"PUT", "PATCH", "DELETE", "HEAD"} {
		if rr := f.request(method, route, `{}`, true); rr.Code != 404 {
			t.Fatal(method, rr.Code)
		}
	}
	// A request can never turn the Go-only capability on.
	f.handler.(*server).engine = engine.New(f.store, catalogFailGateway{}, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}})
	action(map[string]any{"action": "execute", "revision": m.Revision, "operation_id": o.ID, "material_digest": o.MaterialDigest}, 409)
	rr = f.request("GET", route, "", true)
	if !strings.Contains(rr.Body.String(), `"read_only":true`) {
		t.Fatal(rr.Body.String())
	}
}

func TestC4HealthDefaultClosed(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	for _, readOnly := range []bool{false, true} {
		f.handler.(*server).recoveryReadOnly = readOnly
		rr := f.request("GET", "/api/health", "", true)
		var view struct {
			Capabilities map[string]bool `json:"capabilities"`
		}
		if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &view) != nil {
			t.Fatal(rr.Code, rr.Body.String())
		}
		for _, key := range []string{"c4_simulation", "c4_real_execution"} {
			enabled, present := view.Capabilities[key]
			if !present || enabled {
				t.Fatal("C4 default not explicitly closed", key, view.Capabilities)
			}
		}
	}
}
