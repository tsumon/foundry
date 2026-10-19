package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
)

type revisionGateway struct{}

func (revisionGateway) Complete(context.Context, model.Request) (model.Result, error) {
	return model.Result{Text: `{"summary":"Implement","steps":[{"id":"one","title":"Update","description":"Edit source","depends_on":[],"files":["src/main.txt"]}]}`}, nil
}

func revisionFixture(t *testing.T) (*fixture, domain.Mission) {
	t.Helper()
	f := newFixture(t)
	f.authenticate()
	server := f.handler.(*server)
	e := engine.New(f.store, revisionGateway{}, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}})
	server.engine = e
	t.Cleanup(func() { e.Close() })
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src/main.txt"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "base"}} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	if err := f.store.PutProfile(domain.Profile{ID: "test", Name: "Test", Protocol: "chat_completions", BaseURL: "http://127.0.0.1", Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	m, err := e.Create(context.Background(), engine.CreateRequest{Title: "Revision", Goal: "Edit source", RepoPath: repo, Spec: domain.Spec{Scope: []string{"src/"}, Acceptance: []string{"test -f /workspace/src/main.txt"}, Image: "local", ProfileID: "test", MaxCalls: 16}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m, err = f.store.GetMission(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if m.State == "blocked" {
			t.Fatal(m.Error)
		}
		if m.State == "awaiting_approval" {
			// Persistence precedes clearing the planner's active operation. Join it
			// before handing this fixture to revision requests, then use a fresh
			// controller (Close intentionally makes the first one unavailable).
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			e = engine.New(f.store, revisionGateway{}, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}})
			server.engine = e
			return f, m
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("planning timed out")
	return nil, domain.Mission{}
}

func revisionBody(m domain.Mission) string {
	return fmt.Sprintf(`{"expected_mission_revision":%d,"expected_plan_revision":%d,"reason":"Clarify","plan":{"summary":"Revised","steps":[{"id":"one","title":"Update","description":"Edit source","depends_on":[],"files":["src/main.txt"]}]}}`, m.Revision, m.Plan.Revision)
}

func TestPlanRevisionsHTTPContract(t *testing.T) {
	f, m := revisionFixture(t)
	url := "/api/missions/" + m.ID + "/plan-revisions"
	rr := f.request(http.MethodGet, url, "", true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"plan_revisions":[]`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	rr = f.request(http.MethodPost, url, revisionBody(m), true)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var response struct {
		Mission      domain.Mission      `json:"mission"`
		PlanRevision domain.PlanRevision `json:"plan_revision"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Mission.Revision != m.Revision+1 || response.PlanRevision.Source != "user" || response.PlanRevision.PlanRevision != m.Plan.Revision+1 || response.Mission.State != "awaiting_approval" {
		t.Fatal(rr.Body.String())
	}
	rr = f.request(http.MethodGet, url, "", true)
	var history struct {
		Revisions []domain.PlanRevision `json:"plan_revisions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if rr.Code != 200 || len(history.Revisions) != 2 || history.Revisions[0].Source != "planner" || history.Revisions[1].PlanDigest == "" {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for _, body := range []string{revisionBody(m), strings.Replace(revisionBody(response.Mission), fmt.Sprintf(`"expected_plan_revision":%d`, response.Mission.Plan.Revision), fmt.Sprintf(`"expected_plan_revision":%d`, m.Plan.Revision), 1)} {
		rr = f.request(http.MethodPost, url, body, true)
		if rr.Code != 409 {
			t.Fatal(rr.Code, rr.Body.String())
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rr = f.request(method, "/api/missions/missing/plan-revisions", revisionBody(m), true)
		if rr.Code != 404 {
			t.Fatal(rr.Code, rr.Body.String())
		}
	}
}

func TestPlanRevisionsHTTPRejectsAuthorityFieldsInvalidUTF8AndState(t *testing.T) {
	f, m := revisionFixture(t)
	url := "/api/missions/" + m.ID + "/plan-revisions"
	body := revisionBody(m)
	for name, bad := range map[string]string{
		"revision":     strings.Replace(body, `"plan":{`, `"plan":{"revision":99,`, 1),
		"status":       strings.Replace(body, `"id":"one"`, `"status":"done","id":"one"`, 1),
		"scope":        strings.Replace(body, `"reason":"Clarify"`, `"scope":["/"],"reason":"Clarify"`, 1),
		"budget":       strings.Replace(body, `"reason":"Clarify"`, `"budget":{"max_calls":99},"reason":"Clarify"`, 1),
		"description":  strings.Replace(body, `"Edit source"`, `" "`, 1),
		"raw utf8":     strings.Replace(body, "Clarify", string([]byte{255}), 1),
		"reason limit": strings.Replace(body, "Clarify", strings.Repeat("x", 1001), 1),
		"null plan":    strings.Replace(body, `"summary":"Revised"`, `"summary":null`, 1),
		"trailing":     body + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			rr := f.request(http.MethodPost, url, bad, true)
			if rr.Code != 400 {
				t.Fatal(rr.Code, rr.Body.String())
			}
		})
	}
	got, _ := f.store.GetMission(m.ID)
	if got.Revision != m.Revision {
		t.Fatal("invalid input mutated mission")
	}
	m, err := f.store.UpdateMission(m.ID, m.Revision, "test.state", func(n *domain.Mission) error { n.State = "running"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodPost, url, revisionBody(m), true)
	if rr.Code != 400 {
		t.Fatal(rr.Code, rr.Body.String())
	}
}

func TestPlanRevisionsHTTPRequiresSessionAndOrigin(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rr := f.request(method, "/api/missions/missing/plan-revisions", `{}`, false)
		if rr.Code != 401 {
			t.Fatal(rr.Code)
		}
	}
	// Same-origin policy is enforced before the new route is dispatched.
	req := httptest.NewRequest(http.MethodPost, testOrigin+"/api/missions/missing/plan-revisions", strings.NewReader(`{}`))
	req.AddCookie(f.cookie)
	req.Header.Set("Origin", "https://elsewhere.invalid")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatal(rr.Code)
	}
}

func TestPlanRevisionRefusalsGiveSafeCorrectionHints(t *testing.T) {
	for _, input := range []struct{ name, old, value string }{
		{"protected", "src/main.txt", "src/.env"},
		{"dependency", `"depends_on":[]`, `"depends_on":["missing"]`},
		{"text", `"summary":"Revised"`, `"summary":" "`},
	} {
		t.Run(input.name, func(t *testing.T) {
			f, m := revisionFixture(t)
			rr := f.request(http.MethodPost, "/api/missions/"+m.ID+"/plan-revisions", strings.Replace(revisionBody(m), input.old, input.value, 1), true)
			if rr.Code != 400 || !strings.Contains(rr.Body.String(), "check plan fields") || strings.Contains(rr.Body.String(), f.dataDir) {
				t.Fatalf("missing safe input correction hint: %d %s", rr.Code, rr.Body.String())
			}
		})
	}
	for _, scenario := range []string{"state", "preflight"} {
		t.Run(scenario, func(t *testing.T) {
			f, m := revisionFixture(t)
			var err error
			if scenario == "state" {
				m, err = f.store.UpdateMission(m.ID, m.Revision, "test.state", func(n *domain.Mission) error { n.State = "running"; return nil })
			} else {
				err = os.Remove(filepath.Join(f.dataDir, "missions", m.ID, "base", "src", "main.txt"))
			}
			if err != nil {
				t.Fatal(err)
			}
			rr := f.request(http.MethodPost, "/api/missions/"+m.ID+"/plan-revisions", revisionBody(m), true)
			if rr.Code != 400 || !strings.Contains(rr.Body.String(), "refresh") || strings.Contains(rr.Body.String(), "check plan fields") || strings.Contains(rr.Body.String(), f.dataDir) || strings.Contains(rr.Body.String(), "main.txt") {
				t.Fatalf("missing safe state/preflight correction hint: %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}
