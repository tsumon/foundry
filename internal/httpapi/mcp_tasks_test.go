package httpapi

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/testutil"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/gitinspect"
	"foundry/internal/mcptest"
	"foundry/internal/model"
	"foundry/internal/skills"
	"foundry/internal/store"
)

// The HTTP adapter and controller are real; only paid model completions are
// replaced with deterministic synthetic responses.
type mcpTaskGateway struct{ Echo bool }

func (g mcpTaskGateway) Complete(_ context.Context, r model.Request) (model.Result, error) {
	if g.Echo {
		return model.Result{Text: r.System + "\n" + r.Prompt}, nil
	}
	text := `{"summary":"Synthetic plan","steps":[{"id":"one","title":"Update text","description":"Update text","files":["src/main.txt"],"depends_on":[]}]}`
	if r.Role == domain.Researcher {
		text = `{"kind":"mcp","tool_id":"reference.lookup","arguments":{"topic":"mcp-workflow"},"reason":"Inspect synthetic reference"}`
		if strings.Contains(r.Prompt, "SYNTHETIC LOCAL TEST:") {
			text = `{"kind":"final","research":{"summary":"Synthetic result inspected","findings":[],"unknowns":[]}}`
		}
	}
	return model.Result{Text: text}, nil
}

type mcpHTTPFixture struct {
	*fixture
	http    *httptest.Server
	engine  *engine.Engine
	service *mcptest.Service
	dbPath  string
}

func newMCPHTTPFixture(t *testing.T) *mcpHTTPFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "foundry.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{t: t, store: db, secrets: &memorySecrets{}, dataDir: t.TempDir()}
	service, err := mcptest.Start(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	d := service.Descriptor()
	e := engine.New(f.store, mcpTaskGateway{}, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}, LocalMCPTest: &d})
	t.Cleanup(func() { e.Close() })
	server := httptest.NewUnstartedServer(nil)
	f.handler = New(f.store, e, model.NewClient(f.secrets), f.secrets, Options{Address: server.Listener.Addr().String()})
	server.Config.Handler = f.handler
	server.Start()
	t.Cleanup(server.Close)
	out := &mcpHTTPFixture{f, server, e, service, dbPath}
	response, body := out.send("POST", "/api/session", `{}`, false)
	if response.StatusCode != 200 || len(response.Cookies()) != 1 {
		t.Fatalf("session %d %s", response.StatusCode, body)
	}
	f.cookie = response.Cookies()[0]
	return out
}
func (f *mcpHTTPFixture) send(method, path, body string, auth bool) (*http.Response, string) {
	f.t.Helper()
	req, _ := http.NewRequest(method, f.http.URL+path, strings.NewReader(body))
	if method != "GET" {
		req.Header.Set("Origin", f.http.URL)
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.AddCookie(f.cookie)
	}
	response, err := f.http.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	return response, string(raw)
}
func (f *mcpHTTPFixture) ok(method, path string, body any) string {
	f.t.Helper()
	raw, _ := json.Marshal(body)
	response, text := f.send(method, path, string(raw), true)
	if response.StatusCode != 200 {
		f.t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, text)
	}
	assertMCPNoSecrets(f.t, text)
	return text
}
func assertMCPNoSecrets(t *testing.T, text string) {
	t.Helper()
	for _, secret := range []string{`"credential_ref"`, "opaque-secret-reference", `"mcp_bindings"`, `"prompt"`, "FROZEN-SKILL-BODY"} {
		if strings.Contains(text, secret) {
			t.Fatalf("unsafe API field %q: %s", secret, text)
		}
	}
}
func (f *mcpHTTPFixture) create() domain.Mission {
	f.t.Helper()
	t := f.t
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src/main.txt"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "base"}} {
		cmd := testutil.Git(t, args...)
		cmd.Dir = repo
		if raw, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v %s", err, raw)
		}
	}
	repository, err := f.store.SaveGitRepository(domain.GitRepository{ID: "source", Name: "Synthetic source", Path: repo}, 0)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := gitinspect.Inspect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	obs.ID = "observation"
	obs.RepositoryID = repository.ID
	obs.RepositoryRevision = repository.Revision
	if _, err = f.store.SaveGitObservation(obs); err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutProfile(domain.Profile{ID: "fixture", Name: "Synthetic model", Protocol: "chat_completions", BaseURL: "http://127.0.0.1:9", Model: "fixture-model"}); err != nil {
		t.Fatal(err)
	}
	var selection struct {
		Enabled   bool                 `json:"enabled"`
		Eligible  bool                 `json:"eligible"`
		Selection *engine.MCPSelection `json:"selection"`
	}
	if err = json.Unmarshal([]byte(f.ok("GET", "/api/mcp-local-test", nil)), &selection); err != nil {
		t.Fatal(err)
	}
	if !selection.Enabled || !selection.Eligible || selection.Selection == nil {
		t.Fatalf("selection %+v", selection)
	}
	skill, err := skills.Parse([]domain.SkillFile{{Path: "SKILL.md", Content: "---\nname: synthetic\ndescription: Synthetic leak sentinel\n---\nFROZEN-SKILL-BODY\n"}}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	skill.ID = "synthetic-skill"
	skill, err = f.store.SaveSkillRevision(skill, 0)
	if err != nil {
		t.Fatal(err)
	}
	request := engine.CreateRequest{Title: "Synthetic MCP task", Goal: "Inspect synthetic reference", RepoPath: repo, Spec: domain.Spec{Scope: []string{"src/"}, Acceptance: []string{"test -f /workspace/src/main.txt"}, Image: "fixture:local", ProfileID: "fixture", Research: true, MaxCalls: 16}, WorkspaceMode: domain.WorkspaceModeControlledMCPReadV2, ToolMode: domain.ToolModeControlledMCPReadV2, CapabilitySelection: &domain.CapabilitySelection{RepositoryID: repository.ID, RepositoryRevision: repository.Revision, ObservationID: obs.ID}, MCPSelection: selection.Selection}
	request.CapabilitySelection.Skills = []domain.SkillBinding{{ID: skill.ID, Revision: skill.Revision, Role: domain.Researcher}}
	var result struct {
		Mission domain.Mission `json:"mission"`
	}
	json.Unmarshal([]byte(f.ok("POST", "/api/missions", request)), &result)
	if result.Mission.ID == "" {
		t.Fatal("missing mission")
	}
	return result.Mission
}

func TestMCPTaskMissionProjectionAlsoProtectsInternalV1Bindings(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	m, err := f.store.CreateMission(domain.Mission{ID: "internal-v1", WorkspaceMode: domain.WorkspaceModeControlledMCPReadV1, ToolMode: domain.ToolModeControlledMCPReadV1, State: "draft", MCPBindings: &domain.MCPTaskBindings{CredentialRef: "opaque-secret-reference"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/missions", "/api/missions/" + m.ID} {
		response := f.request("GET", path, "", true)
		if response.Code != 200 {
			t.Fatal(response.Code)
		}
		assertMCPNoSecrets(t, response.Body.String())
	}
}
func (f *mcpHTTPFixture) wait(id, state, stage string) domain.Mission {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m, err := f.store.GetMission(id)
		if err != nil {
			f.t.Fatal(err)
		}
		if m.State == state && (stage == "" || m.Stage == stage) {
			return m
		}
		if m.State == "blocked" {
			f.t.Fatalf("blocked: %s", m.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatal("checkpoint timeout")
	return domain.Mission{}
}

func TestMCPTaskHTTPApprovalReplayAndSafeHistory(t *testing.T) {
	f := newMCPHTTPFixture(t)
	m := f.create()
	base := "/api/missions/" + m.ID
	f.ok("POST", base+"/plan", map[string]any{"revision": m.Revision, "controlled_consent": true})
	m = f.wait(m.ID, "planning", "awaiting_tool_approval")
	calls, err := f.store.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 0 {
		t.Fatal("call before approval", err)
	}
	var card struct {
		Decision struct {
			ProposalID     string `json:"proposal_id"`
			ProposalDigest string `json:"proposal_digest"`
			Revision       int64  `json:"revision"`
			Current        bool   `json:"current"`
			CanDecide      bool   `json:"can_decide"`
		} `json:"decision"`
	}
	json.Unmarshal([]byte(f.ok("GET", base+"/mcp-decision", nil)), &card)
	if !card.Decision.Current || !card.Decision.CanDecide || card.Decision.ProposalID == "" {
		t.Fatalf("card %+v", card)
	}
	path := base + "/mcp-proposals/" + card.Decision.ProposalID + "/decision"
	payload := map[string]any{"expected_revision": m.Revision, "proposal_digest": card.Decision.ProposalDigest, "decision": "approve", "request_id": "decision-one"}
	bad := fmt.Sprintf(`{"expected_revision":%d,"proposal_digest":%q,"decision":"approve","request_id":"bad","arguments":{}}`, m.Revision, card.Decision.ProposalDigest)
	if response, _ := f.send("POST", path, bad, true); response.StatusCode != 400 {
		t.Fatalf("replacement args status %d", response.StatusCode)
	}
	stale := fmt.Sprintf(`{"expected_revision":0,"proposal_digest":%q,"decision":"approve","request_id":"stale"}`, card.Decision.ProposalDigest)
	if response, _ := f.send("POST", path, stale, true); response.StatusCode != 409 {
		t.Fatalf("stale status %d", response.StatusCode)
	}
	f.ok("POST", path, payload)
	m = f.wait(m.ID, "awaiting_approval", "")
	f.ok("POST", path, payload)
	receipt := f.ok("GET", base+"/mcp-decisions/decision-one", nil)
	if !strings.Contains(receipt, `"invocation_id"`) || !strings.Contains(receipt, `"grant_id"`) || strings.Contains(receipt, `"grant"`) {
		t.Fatal(receipt)
	}
	calls, err = f.store.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 1 || calls[0].Status != "succeeded" {
		t.Fatalf("calls %+v %v", calls, err)
	}
	for _, path := range []string{"/api/missions", base, base + "/mcp-invocations", base + "/mcp-invocations/" + calls[0].ID, base + "/read-grants"} {
		raw := f.ok("GET", path, nil)
		if strings.Contains(raw, `"required_state"`) {
			t.Fatal("full grant", raw)
		}
	}
	models, _ := f.store.ListInvocations(m.ID, 0, 100)
	for _, v := range models {
		raw := f.ok("GET", base+"/invocations/"+v.ID, nil)
		var got struct {
			Invocation domain.InvocationDetail `json:"invocation"`
		}
		json.Unmarshal([]byte(raw), &got)
		if got.Invocation.InputPreview != "" {
			t.Fatal("raw model input", raw)
		}
		if !strings.Contains(raw, v.Input.Digest) {
			t.Fatal("missing provenance")
		}
	}
	if raw := f.ok("GET", base+"/mcp-decision", nil); !strings.Contains(raw, `"decision":null`) {
		t.Fatal(raw)
	}
	off := engine.New(f.store, mcpTaskGateway{}, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}})
	defer off.Close()
	f.handler.(*server).engine = off
	f.ok("GET", base+"/mcp-invocations/"+calls[0].ID, nil)
	if response, _ := f.send("POST", base+"/research-continue", fmt.Sprintf(`{"expected_revision":%d,"intent_id":"disabled"}`, m.Revision), true); response.StatusCode != 409 {
		t.Fatalf("disabled continue %d", response.StatusCode)
	}
}

func TestMCPTaskDefaultOffAuthAndClosedGenericRoutes(t *testing.T) {
	f := newMCPHTTPFixture(t)
	if response, _ := f.send("GET", "/api/mcp-local-test", "", false); response.StatusCode != 401 {
		t.Fatal(response.StatusCode)
	}
	for _, field := range []string{"host", "origin", "json"} {
		req, _ := http.NewRequest("POST", f.http.URL+"/api/missions/missing/research-continue", strings.NewReader(`{}`))
		req.AddCookie(f.cookie)
		req.Header.Set("Origin", f.http.URL)
		req.Header.Set("Content-Type", "application/json")
		switch field {
		case "host":
			req.Host = "other.invalid"
		case "origin":
			req.Header.Set("Origin", "https://other.invalid")
		case "json":
			req.Header.Set("Content-Type", "text/plain")
		}
		response, err := f.http.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatalf("%s %d", field, response.StatusCode)
		}
	}
	for _, path := range []string{"/api/mcp/call", "/api/scripts/run", "/api/git/write", "/api/missions/nope/mcp-call"} {
		if response, _ := f.send("POST", path, `{}`, true); response.StatusCode != 404 {
			t.Fatalf("open generic route %s %d", path, response.StatusCode)
		}
	}
	off := engine.New(f.store, nil, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}})
	defer off.Close()
	f.handler.(*server).engine = off
	raw := f.ok("GET", "/api/mcp-local-test", nil)
	if !strings.Contains(raw, `"enabled":false`) || !strings.Contains(raw, `"eligible":false`) {
		t.Fatal(raw)
	}
	response, raw := f.send("POST", "/api/missions", `{"workspace_mode":"generations_controlled_mcp_read_v2","tool_mode":"controlled_mcp_read_v2"}`, true)
	if response.StatusCode != 409 {
		t.Fatalf("off creation %d %s", response.StatusCode, raw)
	}
}

func TestMCPTaskCapabilityCredentialReferenceIsNotABrowserDTO(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	response := f.request("POST", "/api/capabilities/mcp", `{"name":"private","endpoint":"http://127.0.0.1:9/mcp","enabled":true,"api_key":"fake-secret"}`, true)
	if response.Code != 200 {
		t.Fatalf("save %d %s", response.Code, response.Body.String())
	}
	for _, raw := range []string{response.Body.String(), f.request("GET", "/api/capabilities", "", true).Body.String()} {
		if strings.Contains(raw, "credential_ref") || strings.Contains(raw, "fake-secret") || !strings.Contains(raw, `"has_credential":true`) {
			t.Fatal("unsafe connection", raw)
		}
	}
}

func TestMCPTaskUntrustedModelEchoDoesNotExposePromptOrSkill(t *testing.T) {
	f := newMCPHTTPFixture(t)
	d := f.service.Descriptor()
	e := engine.New(f.store, mcpTaskGateway{Echo: true}, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}, LocalMCPTest: &d})
	defer e.Close()
	f.handler.(*server).engine = e
	m := f.create()
	base := "/api/missions/" + m.ID
	f.ok("POST", base+"/plan", map[string]any{"revision": m.Revision, "controlled_consent": true})
	f.wait(m.ID, "blocked", "")
	models, err := f.store.ListInvocations(m.ID, 0, 100)
	if err != nil || len(models) != 1 {
		t.Fatalf("models %+v %v", models, err)
	}
	raw := f.ok("GET", base+"/invocations/"+models[0].ID, nil)
	if !strings.Contains(raw, `"input_redacted":true`) || !strings.Contains(raw, `"result_redacted":true`) {
		t.Fatal("missing explicit redaction", raw)
	}
}

type repositoryTaskGateway struct{}

func (repositoryTaskGateway) Complete(ctx context.Context, r model.Request) (model.Result, error) {
	if r.Role != domain.Researcher {
		return (mcpTaskGateway{}).Complete(ctx, r)
	}
	var prompt struct {
		Extra struct {
			Observations []json.RawMessage `json:"observations"`
		} `json:"extra"`
	}
	if err := json.Unmarshal([]byte(r.Prompt), &prompt); err != nil {
		return model.Result{}, err
	}
	if len(prompt.Extra.Observations) == 0 {
		return model.Result{Text: `{"kind":"repository","tool":"repo.read","arguments":{"path":"src/main.txt","start_line":1,"max_lines":80}}`}, nil
	}
	return model.Result{Text: `{"kind":"final","research":{"summary":"Synthetic repository read","findings":[],"unknowns":[]}}`}, nil
}

func TestMCPTaskRepositoryEvidenceRemainsInspectable(t *testing.T) {
	f := newMCPHTTPFixture(t)
	d := f.service.Descriptor()
	e := engine.New(f.store, repositoryTaskGateway{}, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}, LocalMCPTest: &d})
	defer e.Close()
	f.handler.(*server).engine = e
	m := f.create()
	base := "/api/missions/" + m.ID
	f.ok("POST", base+"/plan", map[string]any{"revision": m.Revision, "controlled_consent": true})
	f.wait(m.ID, "awaiting_approval", "")
	invocations, err := f.store.ListInvocations(m.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range invocations {
		if v.Kind != "repository" {
			continue
		}
		found = true
		var result struct {
			Invocation domain.InvocationDetail `json:"invocation"`
		}
		json.Unmarshal([]byte(f.ok("GET", base+"/invocations/"+v.ID, nil)), &result)
		if !strings.Contains(result.Invocation.InputPreview, "src/main.txt") || !strings.Contains(result.Invocation.ResultPreview, "fixture") {
			t.Fatalf("repository evidence hidden %+v", result)
		}
	}
	if !found {
		t.Fatal("missing repository observation")
	}
}

func TestMCPTaskExactActionJSON(t *testing.T) {
	f := newMCPHTTPFixture(t)
	for _, body := range []string{
		`{"expected_revision":1,"proposal_digest":"a","decision":"approve","decision":"reject","request_id":"one"}`,
		`{"Expected_revision":1,"proposal_digest":"a","decision":"approve","request_id":"one"}`,
		`{"expected_revision":null,"proposal_digest":"a","decision":"approve","request_id":"one"}`,
		`{"proposal_digest":"a","decision":"approve","request_id":"one"}`,
		`{"expected_revision":1,"proposal_digest":"a","decision":"approve","request_id":"\ud800"}`,
	} {
		response, raw := f.send("POST", "/api/missions/missing/mcp-proposals/missing/decision", body, true)
		if response.StatusCode != 400 {
			t.Errorf("accepted ambiguous JSON %s: %d %s", body, response.StatusCode, raw)
		}
	}
	for _, body := range []string{`{"expected_revision":1,"intent_id":"a","intent_id":"b"}`, `{"expected_revision":1}`, `{"expected_revision":null,"intent_id":"one"}`} {
		response, raw := f.send("POST", "/api/missions/missing/research-continue", body, true)
		if response.StatusCode != 400 {
			t.Errorf("accepted continue JSON %s: %d %s", body, response.StatusCode, raw)
		}
	}
}

func TestMCPTaskMissionListIsolatesCorruptionButPropagatesSQLFailure(t *testing.T) {
	f := newMCPHTTPFixture(t)
	healthy := f.create()
	_, err := f.store.CreateMission(domain.Mission{ID: "corrupt", WorkspaceMode: domain.WorkspaceModeControlledMCPReadV2, ToolMode: domain.ToolModeControlledMCPReadV2, State: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	raw := f.ok("GET", "/api/missions", nil)
	if !strings.Contains(raw, healthy.ID) || !strings.Contains(raw, `"id":"corrupt"`) || !strings.Contains(raw, `"why_not":"history_unavailable"`) {
		t.Fatal("corrupt owner hid healthy missions", raw)
	}
	db, err := sql.Open("sqlite3", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`DROP TABLE research_turn_applications`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/missions", "/api/missions/" + healthy.ID} {
		response, raw := f.send("GET", path, "", true)
		if response.StatusCode != 500 || strings.Contains(raw, "no such table") || strings.Contains(raw, "research_status") {
			t.Errorf("infrastructure failure swallowed %s %d %s", path, response.StatusCode, raw)
		}
	}
}

func TestMCPTaskPendingAuthorityDisableAndSafeSSE(t *testing.T) {
	for _, disable := range []string{"runtime", "connection"} {
		t.Run(disable, func(t *testing.T) {
			f := newMCPHTTPFixture(t)
			m := f.create()
			base := "/api/missions/" + m.ID
			f.ok("POST", base+"/plan", map[string]any{"revision": m.Revision, "controlled_consent": true})
			m = f.wait(m.ID, "planning", "awaiting_tool_approval")
			// SSE is numbered metadata, never a serialized task, grant or source prompt.
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", f.http.URL+"/api/events?after=0", nil)
			req.AddCookie(f.cookie)
			response, err := f.http.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(response.Body)
			seen := false
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "data: ") {
					assertMCPNoSecrets(t, line)
					var event map[string]any
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
						t.Fatal(line)
					}
					for key := range event {
						if key != "seq" && key != "mission_id" && key != "kind" && key != "created_at" && key != "revision" {
							t.Fatal("nonmetadata event", line)
						}
					}
					seen = true
					break
				}
			}
			response.Body.Close()
			if !seen {
				t.Fatal("no replay event")
			}
			if disable == "runtime" {
				off := engine.New(f.store, nil, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}})
				defer off.Close()
				f.handler.(*server).engine = off
			} else {
				c, err := f.store.GetMCPConnection(m.MCPBindings.ConnectionID)
				if err != nil {
					t.Fatal(err)
				}
				c.Enabled = false
				if _, err = f.store.SaveMCPConnection(c, c.Revision); err != nil {
					t.Fatal(err)
				}
			}
			var card struct {
				Decision struct {
					Current        bool   `json:"current"`
					CanDecide      bool   `json:"can_decide"`
					WhyNot         string `json:"why_not"`
					ProposalID     string `json:"proposal_id"`
					ProposalDigest string `json:"proposal_digest"`
				} `json:"decision"`
			}
			json.Unmarshal([]byte(f.ok("GET", base+"/mcp-decision", nil)), &card)
			if card.Decision.Current || card.Decision.CanDecide || card.Decision.WhyNot == "" || card.Decision.ProposalID == "" {
				t.Fatalf("ineligible card %+v", card)
			}
			raw, _ := json.Marshal(map[string]any{"expected_revision": m.Revision, "proposal_digest": card.Decision.ProposalDigest, "decision": "approve", "request_id": "disabled"})
			response, body := f.send("POST", base+"/mcp-proposals/"+card.Decision.ProposalID+"/decision", string(raw), true)
			if response.StatusCode != 409 {
				t.Fatalf("disabled decision %d %s", response.StatusCode, body)
			}
			f.ok("GET", base+"/mcp-invocations", nil)
			f.ok("GET", base+"/read-grants", nil)
			calls, err := f.store.ListMCPInvocations(m.ID)
			if err != nil || len(calls) != 0 {
				t.Fatal("disabled decision reserved work", err)
			}
			f.ok("POST", base+"/pause", map[string]any{"revision": m.Revision})
			m, err = f.store.GetMission(m.ID)
			if err != nil || m.State != "paused" {
				t.Fatalf("idle pause %+v %v", m, err)
			}
		})
	}
}

func TestMCPTaskRequestIDRejectsUnsafePathIdentities(t *testing.T) {
	f := newMCPHTTPFixture(t)
	m := f.create()
	base := "/api/missions/" + m.ID
	f.ok("POST", base+"/plan", map[string]any{"revision": m.Revision, "controlled_consent": true})
	m = f.wait(m.ID, "planning", "awaiting_tool_approval")
	proposals, err := f.store.ListMCPProposals(m.ID)
	if err != nil || len(proposals) != 1 {
		t.Fatal("pending proposal", err)
	}
	p := proposals[0]
	for _, id := range []string{"one/two", "%2F", "%252F", ".", "..", "../one", "one\\two", "one?two", "one#two", "one.two", "one two", " one", "one\n", "é", "", strings.Repeat("a", 257)} {
		for _, decision := range []string{"reject", "approve"} {
			raw, _ := json.Marshal(map[string]any{"expected_revision": m.Revision, "proposal_digest": p.Digest, "decision": decision, "request_id": id})
			response, body := f.send("POST", base+"/mcp-proposals/"+p.ID+"/decision", string(raw), true)
			if response.StatusCode != 400 {
				receipt, _ := f.send("GET", base+"/mcp-decisions/"+url.PathEscape(id), "", true)
				t.Fatalf("unsafe ID %q: decision status %d (want 400), receipt status %d; %s", id, response.StatusCode, receipt.StatusCode, body)
			}
		}
		if _, err := f.store.GetMCPDecision(m.ID, id); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("unsafe ID %q persisted decision: %v", id, err)
		}
		raw, _ := json.Marshal(map[string]any{"expected_revision": m.Revision, "intent_id": id})
		if response, body := f.send("POST", base+"/research-continue", string(raw), true); response.StatusCode != 400 {
			t.Fatalf("unsafe intent %q: %d %s", id, response.StatusCode, body)
		}
	}
	current, err := f.store.GetMission(m.ID)
	if err != nil || current.Revision != m.Revision || current.Stage != m.Stage {
		t.Fatalf("invalid identities changed mission: %+v %v", current, err)
	}
	calls, err := f.store.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 0 {
		t.Fatal("invalid identities dispatched", calls, err)
	}
}

func TestMCPTaskRequestIDPermittedFormsRoundTrip(t *testing.T) {
	for _, id := range []string{"a", "7ac04b40-82d9-4bf5-943c-f77da97f6e48", strings.Repeat("a", 192) + "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"} {
		t.Run(fmt.Sprintf("length_%d", len(id)), func(t *testing.T) {
			f := newMCPHTTPFixture(t)
			m := f.create()
			base := "/api/missions/" + m.ID
			f.ok("POST", base+"/plan", map[string]any{"revision": m.Revision, "controlled_consent": true})
			m = f.wait(m.ID, "planning", "awaiting_tool_approval")
			proposals, err := f.store.ListMCPProposals(m.ID)
			if err != nil || len(proposals) != 1 {
				t.Fatal("pending proposal", err)
			}
			p := proposals[0]
			payload := map[string]any{"expected_revision": m.Revision, "proposal_digest": p.Digest, "decision": "reject", "request_id": id}
			f.ok("POST", base+"/mcp-proposals/"+p.ID+"/decision", payload)
			var receipt struct {
				Decision store.MCPDecision `json:"decision"`
			}
			if err := json.Unmarshal([]byte(f.ok("GET", base+"/mcp-decisions/"+id, nil)), &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.Decision.RequestID != id || receipt.Decision.Approved {
				t.Fatalf("receipt identity mismatch: %+v", receipt.Decision)
			}
		})
	}
}

func TestMCPTaskRejectHasReceiptWithoutInvocation(t *testing.T) {
	f := newMCPHTTPFixture(t)
	m := f.create()
	base := "/api/missions/" + m.ID
	f.ok("POST", base+"/plan", map[string]any{"revision": m.Revision, "controlled_consent": true})
	m = f.wait(m.ID, "planning", "awaiting_tool_approval")
	proposals, err := f.store.ListMCPProposals(m.ID)
	if err != nil || len(proposals) != 1 {
		t.Fatal(err)
	}
	p := proposals[0]
	payload := map[string]any{"expected_revision": m.Revision, "proposal_digest": p.Digest, "decision": "reject", "request_id": "reject-one"}
	f.ok("POST", base+"/mcp-proposals/"+p.ID+"/decision", payload)
	f.ok("POST", base+"/mcp-proposals/"+p.ID+"/decision", payload)
	raw := f.ok("GET", base+"/mcp-decisions/reject-one", nil)
	if !strings.Contains(raw, `"approved":false`) || strings.Contains(raw, `"invocation_id"`) || strings.Contains(raw, `"grant_id"`) {
		t.Fatal(raw)
	}
	calls, err := f.store.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 0 {
		t.Fatal("reject dispatched", err)
	}
	if response, _ := f.send("GET", base+"/mcp-decisions/other-request", "", true); response.StatusCode != 404 {
		t.Fatal(response.StatusCode)
	}
}
