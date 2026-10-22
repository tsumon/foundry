package httpapi

import (
	"context"
	"encoding/json"
	"foundry/internal/testutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/evidence"
	"foundry/internal/gitinspect"
	"foundry/internal/model"
)

type controlledConsentGateway struct{}

func (controlledConsentGateway) Complete(context.Context, model.Request) (model.Result, error) {
	return model.Result{Text: `{"kind":"final","research":{"summary":"FAKE controlled research","findings":[],"unknowns":[]}}`}, nil
}

func controlledLedgerFixture(t *testing.T, f *fixture, id string) (domain.Mission, domain.Invocation) {
	t.Helper()
	bindings := domain.TaskBindings{
		Git:    domain.GitSourceBinding{RepositoryID: "repo", RepositoryRevision: 1, ObservationID: "obs", Root: "/tmp/repo", BaseCommit: strings.Repeat("a", 40)},
		Skills: []domain.SkillBinding{}, PolicyVersion: "repo_read_v1",
		Tools: []string{"repo.list", "repo.read", "repo.search"}, Budget: domain.FixedReadBudget(),
	}
	digest, err := domain.TaskBindingDigest(bindings)
	if err != nil {
		t.Fatal(err)
	}
	m, err := f.store.CreateMission(domain.Mission{ID: id, WorkspaceMode: domain.WorkspaceModeControlledReadV1, ToolMode: domain.ToolModeControlledReadV1, TaskBindings: &bindings, State: "planning", Stage: "plan", Epoch: 1, Budget: domain.Budget{MaxCalls: 16}})
	if err != nil {
		t.Fatal(err)
	}
	grant := domain.ReadGrant{ID: "grant-" + id, MissionID: id, Epoch: 1, SessionID: "session-" + id, Role: domain.Researcher, RequiredState: "planning", BindingDigest: digest, AuthorityDigest: strings.Repeat("b", 64)}
	m, grant, err = f.store.CreateReadGrant(grant, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"path":"<script>alert(1)</script>"}`)
	envelope, err := evidence.New(filepath.Join(f.dataDir, "evidence")).Put(domain.EvidenceEnvelope{Kind: "model_input", InvocationID: "inv-" + id, MissionID: id, Epoch: 1, Producer: "researcher", ContentType: "application/json", BindingDigest: digest}, input)
	if err != nil {
		t.Fatal(err)
	}
	m, invocation, err := f.store.BeginInvocation(domain.Invocation{ID: "inv-" + id, MissionID: id, Epoch: 1, SessionID: grant.SessionID, Kind: "repository", Role: domain.Researcher, Name: "repo.read", BindingDigest: digest, AuthorityDigest: grant.AuthorityDigest, GrantID: grant.ID, Input: envelope}, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_ = m
	return m, invocation
}

func TestInvocationRoutesListGrantAndIntegrityCheckedDetail(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	m, invocation := controlledLedgerFixture(t, f, "ledger")

	for _, tc := range []struct{ path, field string }{
		{"/api/missions/" + m.ID + "/invocations", `"invocations"`},
		{"/api/missions/" + m.ID + "/invocations?before=0&limit=30", `"invocations"`},
		{"/api/missions/" + m.ID + "/read-grants", `"read_grants"`},
	} {
		rr := f.request(http.MethodGet, tc.path, "", true)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), tc.field) {
			t.Fatalf("%s: %d %s", tc.path, rr.Code, rr.Body.String())
		}
	}
	rr := f.request(http.MethodGet, "/api/missions/"+m.ID+"/invocations/"+invocation.ID, "", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Invocation domain.InvocationDetail `json:"invocation"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Invocation.Invocation.ID != invocation.ID || got.Invocation.InputPreview != `{"path":"<script>alert(1)</script>"}` {
		t.Fatalf("detail=%+v", got.Invocation)
	}

	controlledLedgerFixture(t, f, "other")
	rr = f.request(http.MethodGet, "/api/missions/other/invocations/"+invocation.ID, "", true)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross mission detail: %d %s", rr.Code, rr.Body.String())
	}
}

func TestInvocationRoutesRejectInvalidQueriesAndMutationShapes(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	controlledLedgerFixture(t, f, "ledger")
	for _, target := range []string{
		"/api/missions/ledger/invocations?before=-1", "/api/missions/ledger/invocations?before=nope",
		"/api/missions/ledger/invocations?before=%2B1", "/api/missions/ledger/invocations?limit=%2B1",
		"/api/missions/ledger/invocations?limit=0", "/api/missions/ledger/invocations?limit=101",
		"/api/missions/ledger/invocations?limit=1&limit=2", "/api/missions/ledger/invocations?other=1",
		"/api/missions/ledger/read-grants?limit=1", "/api/missions/ledger/invocations/inv-ledger?path=/etc/passwd",
		"/api/missions/ledger/invocations?%zz=1", "/api/missions/ledger/read-grants?%zz=1",
		"/api/missions/ledger/invocations/inv-ledger?%zz=1",
	} {
		rr := f.request(http.MethodGet, target, "", true)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", target, rr.Code, rr.Body.String())
		}
	}
	for _, target := range []string{
		"/api/missions/ledger/invocations", "/api/missions/ledger/read-grants",
		"/api/missions/ledger/invocations/inv-ledger", "/api/missions/ledger/call", "/api/missions/ledger/evidence",
	} {
		rr := f.request(http.MethodPost, target, `{}`, true)
		if rr.Code != http.StatusNotFound {
			t.Errorf("POST %s: %d %s", target, rr.Code, rr.Body.String())
		}
	}
}

func TestInvocationDetailCorruptionIsASafeFailureNotAnEmptyRecord(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	m, invocation := controlledLedgerFixture(t, f, "corrupt-detail")
	blob := filepath.Join(f.dataDir, "evidence", "blobs", invocation.Input.Digest[:2], invocation.Input.Digest)
	if err := os.WriteFile(blob, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodGet, "/api/missions/"+m.ID+"/invocations/"+invocation.ID, "", true)
	if rr.Code != http.StatusInternalServerError || strings.Contains(rr.Body.String(), f.dataDir) || strings.Contains(rr.Body.String(), "input_preview") {
		t.Fatalf("unsafe corruption response: %d %s", rr.Code, rr.Body.String())
	}
}

func TestInvocationReadsReuseAuthenticationHostAndOriginChecks(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	controlledLedgerFixture(t, f, "ledger")
	path := "/api/missions/ledger/invocations"
	if rr := f.request(http.MethodGet, path, "", false); rr.Code != http.StatusUnauthorized {
		t.Fatal(rr.Code)
	}
	for _, tc := range []struct{ host, origin string }{{"elsewhere:4317", ""}, {"127.0.0.1:4317", "https://elsewhere.invalid"}} {
		req := httptest.NewRequest(http.MethodGet, testOrigin+path, nil)
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		req.AddCookie(f.cookie)
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("host=%q origin=%q status=%d", tc.host, tc.origin, rr.Code)
		}
	}
}

func TestHealthProjectsControlledCreationGateIndependently(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		f := newFixture(t)
		f.authenticate()
		e := engine.New(f.store, nil, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}, DisableControlledReadCreation: disabled})
		f.handler.(*server).engine = e
		t.Cleanup(func() { _ = e.Close() })
		rr := f.request(http.MethodGet, "/api/health", "", true)
		var response struct {
			Capabilities map[string]bool `json:"capabilities"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]bool{
			"controlled_read_creation": !disabled,
			"controlled_builtin_tools": true,
			"skill_binding":            true,
			"tool_execution":           false,
			"mcp_tool_call":            false,
			"skill_execution":          false,
			"git_write":                false,
		} {
			got, present := response.Capabilities[key]
			if !present || got != want {
				t.Errorf("disabled=%v capability %q=(%v,present=%v), want %v", disabled, key, got, present, want)
			}
		}
		if t.Failed() {
			t.Fatalf("disabled=%v capabilities=%+v", disabled, response.Capabilities)
		}
	}
}

func TestControlledPlanConsentIsExplicitAndAcceptedOnlyOnPlan(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	repoPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoPath, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "src", "main.txt"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "base"}} {
		command := testutil.Git(t, args...)
		command.Dir = repoPath
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
	}
	repository, err := f.store.SaveGitRepository(domain.GitRepository{ID: "consent-source", Name: "FAKE consent source", Path: repoPath}, 0)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := gitinspect.Inspect(context.Background(), repoPath)
	if err != nil {
		t.Fatal(err)
	}
	observation.ID = "consent-observation"
	observation.RepositoryID = repository.ID
	observation.RepositoryRevision = repository.Revision
	if _, err = f.store.SaveGitObservation(observation); err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutProfile(domain.Profile{ID: "consent-profile", Name: "FAKE consent profile", Protocol: "chat_completions", BaseURL: "http://127.0.0.1", Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	controlledEngine := engine.New(f.store, controlledConsentGateway{}, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}})
	f.handler.(*server).engine = controlledEngine
	t.Cleanup(func() { _ = controlledEngine.Close() })
	m, err := controlledEngine.Create(context.Background(), engine.CreateRequest{
		Title: "Consent", Goal: "Exercise the controlled HTTP path", RepoPath: repoPath,
		Spec:          domain.Spec{Scope: []string{"src/"}, Acceptance: []string{"test -f /workspace/src/main.txt"}, Image: "fixture:local", ProfileID: "consent-profile", Research: true, MaxCalls: 16},
		WorkspaceMode: domain.WorkspaceModeControlledReadV1, ToolMode: domain.ToolModeControlledReadV1,
		CapabilitySelection: &domain.CapabilitySelection{RepositoryID: repository.ID, RepositoryRevision: repository.Revision, ObservationID: observation.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	planPath := "/api/missions/" + m.ID + "/plan"
	for _, body := range []string{
		`{"revision":` + strconv.FormatInt(m.Revision, 10) + `}`,
		`{"revision":` + strconv.FormatInt(m.Revision, 10) + `,"controlled_consent":false}`,
	} {
		rr := f.request(http.MethodPost, planPath, body, true)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("missing consent status=%d body=%s", rr.Code, rr.Body.String())
		}
	}
	got, err := f.store.GetMission(m.ID)
	if err != nil || got.Revision != m.Revision || got.State != "draft" {
		t.Fatalf("missing consent mutated mission: %+v err=%v", got, err)
	}
	rr := f.request(http.MethodPost, planPath, `{"revision":`+strconv.FormatInt(m.Revision, 10)+`,"controlled_consent":true}`, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("true consent controlled plan: %d %s", rr.Code, rr.Body.String())
	}
	var planned struct {
		Mission domain.Mission `json:"mission"`
	}
	if err = json.Unmarshal(rr.Body.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	grants, err := f.store.ListReadGrants(m.ID)
	if err != nil || planned.Mission.State != "planning" || len(grants) != 1 || grants[0].MissionID != m.ID {
		t.Fatalf("controlled plan outcome mission=%+v grants=%+v err=%v", planned.Mission, grants, err)
	}
	rr = f.request(http.MethodPost, "/api/missions/"+m.ID+"/approve", `{"revision":`+strconv.FormatInt(m.Revision, 10)+`,"controlled_consent":true}`, true)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("approve accepted controlled consent: %d %s", rr.Code, rr.Body.String())
	}
}
