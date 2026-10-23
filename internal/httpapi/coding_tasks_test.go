package httpapi

import (
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/testutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodingTasksMetadataClosedWithoutCapability(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	r := f.request(http.MethodGet, "/api/coding-tasks", "", true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var view struct {
		Enabled   bool   `json:"enabled"`
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	if json.Unmarshal(r.Body.Bytes(), &view) != nil || view.Enabled || view.Available || view.Reason == "" {
		t.Fatal(r.Body.String())
	}
	r = f.request(http.MethodPost, "/api/coding-tasks", `{}`, true)
	if r.Code != 403 {
		t.Fatal(r.Code, r.Body.String())
	}
	missions, err := f.store.ListMissions()
	if err != nil || len(missions) != 0 {
		t.Fatal(missions, err)
	}
}

func TestCodingTasksCreatesOnlyInspectedAccountBoundDraft(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	repo := t.TempDir()
	for name, body := range map[string]string{"main.go": "package demo\n", "go.mod": "module demo\n\ngo 1.27.3\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-qb", "main"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "owned"}} {
		cmd := testutil.Git(t, args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	a, err := f.store.SaveModelAccount(domain.ModelAccount{ID: "coding", Name: "Metadata only", Provider: "openai", AuthMethod: "api_key", BillingMode: "api_credits", BaseURL: "https://api.openai.com/v1", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "fixture", SecretRef: "foundry-model-account-not-installed"}, a.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.FinishModelCredential("fixture", "available"); err != nil {
		t.Fatal(err)
	}
	route, err := f.store.CreateModelRoute(domain.ModelRoute{ID: "coding-route", AccountID: a.ID, Adapter: "openai_api_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "explicit_api", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutProfile(domain.Profile{ID: "coding-model", Name: "metadata", Kind: "openai", AccountID: a.ID, RouteID: route.ID, BaseURL: a.BaseURL, Protocol: "responses", Model: "declared-model"}); err != nil {
		t.Fatal(err)
	}
	inspection := domain.RepositoryInspectionRequest{RepoPath: repo, Consent: true, Spec: domain.RepositoryImportSpec{Scope: []string{"main.go"}, Acceptance: []string{"/opt/foundry/verify-go-v1"}, Image: "local"}}
	raw, _ := json.Marshal(inspection)
	rr := f.request("POST", "/api/repository-imports/inspections", string(raw), true)
	var receipt domain.RepositoryInspection
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &receipt) != nil || receipt.Status != "ready" {
		t.Fatal(rr.Code, rr.Body.String())
	}
	profile := &domain.C4Profile{SchemaVersion: 1, ControlVersion: 1, ID: "c4_go_model_v1", Mode: "docker", ImageID: "sha256:" + strings.Repeat("a", 64), Platform: "linux/arm64", DockerPath: "/nonexistent/docker", Endpoint: "unix:///nonexistent/docker.sock", Verifier: "/opt/foundry/verify-go-v1"}
	request := engine.CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: receipt.ID, Digest: receipt.Digest}, CodingVersion: 1, WorkspaceMode: "generations_v1", Title: "coding", Goal: "change", RepoPath: receipt.Root, Spec: domain.Spec{Scope: inspection.Spec.Scope, Acceptance: inspection.Spec.Acceptance, Image: "local", ProfileID: "coding-model", C4: profile}}
	raw, _ = json.Marshal(request)
	if rr = f.request("POST", "/api/missions", string(raw), true); rr.Code < 400 {
		t.Fatal("normal runtime accepted model C4")
	}
	router := model.NewRouter(nil, nil, model.RouterOptions{})
	cap, err := engine.NewCodingDockerCapability(router)
	if err != nil {
		t.Skip("no platform trust adapter")
	}
	e := engine.New(f.store, router, engine.Options{DataDir: f.dataDir, C4Controlled: cap})
	defer e.Close()
	server := f.handler.(*server)
	server.engine = e
	server.codingWorkbench = true
	if rr = f.request("POST", "/api/coding-tasks", string(raw), true); rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var result struct {
		Mission domain.Mission `json:"mission"`
	}
	if json.Unmarshal(rr.Body.Bytes(), &result) != nil {
		t.Fatal(rr.Body.String())
	}
	m := result.Mission
	if m.State != "draft" || m.ModelSnapshot == nil || m.ModelSnapshot.SchemaVersion != 2 || m.CandidateGeneration == nil || len(m.Approvals) != 1 || m.Approvals[0].Kind != "snapshot" || len(m.C4Controls) != 0 || len(m.C4Operations) != 0 || m.Budget.UsedCalls != 0 {
		t.Fatal("not a pure imported draft", m)
	}
	var response struct {
		Mission struct {
			Runtime modelRuntimeView `json:"model_runtime"`
		} `json:"mission"`
	}
	if json.Unmarshal(rr.Body.Bytes(), &response) != nil || !response.Mission.Runtime.LiveInference {
		t.Fatal("UI cannot admit configured coding models", rr.Body.String())
	}
	for _, adapter := range response.Mission.Runtime.Adapters {
		if adapter.LiveEnabled != (adapter.Provider == "openai") {
			t.Fatal("wrong coding adapter projection", adapter)
		}
	}
	rows, err := f.store.ListInvocations(m.ID, 0, 100)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	if rr = f.request("POST", "/api/coding-tasks", string(raw), true); rr.Code != 200 {
		t.Fatal("idempotent create failed", rr.Code, rr.Body.String())
	}
}

func TestCodingWorkbenchDoesNotEnableLegacyProbe(t *testing.T) {
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{"data":[]}`)) }))
	defer provider.Close()
	f := newFixture(t)
	if err := f.store.PutProfile(domain.Profile{ID: "legacy", Name: "Legacy", Protocol: "responses", BaseURL: provider.URL, Model: "test"}); err != nil {
		t.Fatal(err)
	}
	server := f.handler.(*server)
	server.codingWorkbench = true
	f.authenticate()
	r := f.request(http.MethodPost, "/api/profiles/legacy/probe", `{}`, true)
	if r.Code != 403 || calls != 0 {
		t.Fatal("legacy probe bypassed coding-only transport", r.Code, calls)
	}
}

func TestCodingTasksMetadataEnablesConfigurationOnly(t *testing.T) {
	f := newFixture(t)
	router := model.NewRouter(nil, nil, model.RouterOptions{})
	cap, err := engine.NewCodingDockerCapability(router)
	if err != nil {
		t.Skip("platform has no trusted Docker adapter")
	}
	e := engine.New(f.store, router, engine.Options{DataDir: f.dataDir, C4Controlled: cap})
	defer e.Close()
	f.handler = New(f.store, e, nil, f.secrets, Options{Address: "127.0.0.1:4317", CodingWorkbench: true})
	f.authenticate()
	r := f.request(http.MethodGet, "/api/coding-tasks", "", true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var v map[string]any
	json.Unmarshal(r.Body.Bytes(), &v)
	if v["enabled"] != true || v["available"] != true {
		t.Fatal(v)
	}
	p := v["profile"].(map[string]any)
	if p["id"] != "c4_go_model_v1" || p["image_id"] != "" {
		t.Fatal("default profile could execute", p)
	}
	r = f.request(http.MethodPost, "/api/coding-tasks", `{}`, true)
	if r.Code < 400 {
		t.Fatal("empty request accepted")
	}
	missions, _ := f.store.ListMissions()
	if len(missions) != 0 || len(f.secrets.values) != 0 {
		t.Fatal("configuration had effects")
	}
}
