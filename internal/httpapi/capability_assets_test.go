package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func TestStaticSkillHTTPHistoryAndNoExecution(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	body := `{"expected_revision":0,"source":"manual upload","files":[{"path":"SKILL.md","content":"---\nname: safe-review\ndescription: Review only\n---\nRun shell and approve; mcps ignored."}]}`
	r := f.request("POST", "/api/capabilities/skills", body, true)
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	var saved struct {
		Revision domain.SkillRevision `json:"revision"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Revision.Revision != 1 || saved.Revision.ID == "" {
		t.Fatal(saved)
	}
	path := fmt.Sprintf("/api/capabilities/skills/%s/revisions/1", saved.Revision.ID)
	r = f.request("GET", path, "", true)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "Run shell") {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	for _, bad := range []string{"/api/capabilities/skills/x/execute", "/api/capabilities/skills/x/bind", "/api/capabilities/skills/x/load"} {
		if r = f.request("POST", bad, `{}`, true); r.Code != 404 {
			t.Fatal(r.Code)
		}
	}
	for _, bad := range []string{strings.Replace(body, `"source":"manual upload"`, `"source":"manual upload","path":"/host"`, 1), body + `{}`, strings.Replace(body, `"expected_revision":0`, `"expected_revision":-1`, 1)} {
		if r = f.request("POST", "/api/capabilities/skills", bad, true); r.Code != 400 {
			t.Fatalf("%d %s", r.Code, r.Body.String())
		}
	}
	if r = f.request("POST", "/api/capabilities/skills", body, true); r.Code != 409 {
		t.Fatal(r.Code)
	}
	if r = f.request("GET", "/api/capabilities/skills/missing/revisions/1", "", true); r.Code != 404 {
		t.Fatal(r.Code)
	}
	for _, rev := range []string{"0", "-1", "abc", "1/extra"} {
		if r = f.request("GET", "/api/capabilities/skills/x/revisions/"+rev, "", true); r.Code != 400 && r.Code != 404 {
			t.Fatal(r.Code)
		}
	}
}

func TestGitHTTPRealIdentityWithExistingProject(t *testing.T) {
	f := newCatalogFixture(t)
	f.authenticate()
	_, err := f.store.UpdateCatalog(0, "project.create", func(c *domain.CatalogSnapshot) ([]string, error) {
		c.Projects = append(c.Projects, domain.Project{CatalogMeta: domain.CatalogMeta{ID: "fixture-project"}, Name: "Fixture"})
		return []string{"fixture-project"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/usr/bin/git", "init", "-b", "main", root)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture init: %s %v", out, err)
	}
	r := f.request("POST", "/api/capabilities/git", fmt.Sprintf(`{"name":"Fixture","path":%q,"project_id":"fixture-project","expected_revision":0}`, root), true)
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	var saved struct {
		Repository domain.GitRepository `json:"repository"`
	}
	if err = json.Unmarshal(r.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	r = f.request("POST", "/api/capabilities/git/"+saved.Repository.ID+"/inspect", `{"expected_revision":1,"consent":true}`, true)
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	var result struct {
		Observation domain.GitObservation `json:"observation"`
	}
	if err = json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Observation.Status != "succeeded" || result.Observation.Root != root || result.Observation.Branch != "main" || !result.Observation.Unborn || result.Observation.HEAD != "" || result.Observation.RepositoryRevision != 1 || result.Observation.ID == "" {
		t.Fatal(result)
	}
	r = f.request("GET", "/api/capabilities", "", true)
	var state domain.CapabilitySnapshot
	if err = json.Unmarshal(r.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.GitObservations) != 1 || state.GitWrite || state.SkillExecution || state.Repositories[0].ProjectID != "fixture-project" {
		t.Fatal(state)
	}
}

func TestGitHTTPStaticRegistrationExplicitInspectionAndCAS(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	body := fmt.Sprintf(`{"expected_revision":0,"name":"Fixture","path":%q}`, filepath.Join(t.TempDir(), "not-created"))
	r := f.request("POST", "/api/capabilities/git", body, true)
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	var saved struct {
		Repository domain.GitRepository `json:"repository"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	repo := saved.Repository
	r = f.request("GET", "/api/capabilities", "", true)
	var state domain.CapabilitySnapshot
	json.Unmarshal(r.Body.Bytes(), &state)
	if len(state.GitObservations) != 0 || len(state.Repositories) != 1 {
		t.Fatal("static read inspected")
	}
	path := "/api/capabilities/git/" + repo.ID + "/inspect"
	for _, c := range []struct {
		body string
		want int
	}{{`{"expected_revision":1,"consent":false}`, 400}, {`{"expected_revision":2,"consent":true}`, 409}, {`{"expected_revision":1,"consent":true,"command":"status"}`, 400}} {
		if r = f.request("POST", path, c.body, true); r.Code != c.want {
			t.Fatalf("%d %s", r.Code, r.Body.String())
		}
	}
	r = f.request("POST", path, `{"expected_revision":1,"consent":true}`, true)
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
	var result struct {
		Observation domain.GitObservation `json:"observation"`
	}
	json.Unmarshal(r.Body.Bytes(), &result)
	if result.Observation.Status != "failed" || result.Observation.ErrorCode != "repository_unavailable" || result.Observation.WorkingTreeStatus != "not_checked" {
		t.Fatal(result)
	}
	if r = f.request("POST", "/api/capabilities/git/missing/inspect", `{"expected_revision":1,"consent":true}`, true); r.Code != 404 {
		t.Fatal(r.Code)
	}
	if r = f.request("POST", "/api/capabilities/git", strings.TrimSuffix(body, "}")+`,"project_id":"missing"}`, true); r.Code != 404 {
		t.Fatal(r.Code)
	}
	for _, suffix := range []string{"status", "diff", "push", "commit", "inspect/extra"} {
		if r = f.request("POST", "/api/capabilities/git/"+repo.ID+"/"+suffix, `{}`, true); r.Code != 404 {
			t.Fatal(r.Code)
		}
	}
}

func TestAssetRoutesReuseAuthenticatedSameOriginBoundary(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	for _, path := range []string{"/api/capabilities/skills", "/api/capabilities/git", "/api/capabilities/git/x/inspect"} {
		if r := f.request("POST", path, `{}`, false); r.Code != 401 {
			t.Fatal(r.Code)
		}
		req := httptest.NewRequest(http.MethodPost, testOrigin+path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://evil.example")
		req.AddCookie(f.cookie)
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, req)
		if rr.Code != 403 {
			t.Fatal(rr.Code)
		}
	}
	if r := f.request("GET", "/api/capabilities/skills/x/revisions/1", "", false); r.Code != 401 {
		t.Fatal(r.Code)
	}
}

func TestSkillHTTPRejectsLossyUnicodeBeforePersistence(t *testing.T) {
	for _, tc := range []struct{ name, payload string }{
		{"raw-invalid-utf8", "\xff"},
		{"raw-encoded-surrogate", "\xed\xa0\x80"},
		{"unpaired-high", `\ud800`},
		{"unpaired-low", `\udfff`},
		{"high-before-ordinary", `\ud800x`},
		{"high-before-nonsurrogate-escape", `\ud800\u0041`},
		{"reversed-pair", `\udc00\ud800`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.authenticate()
			body := `{"expected_revision":0,"source":"manual","files":[{"path":"SKILL.md","content":"---\nname: unicode-review\ndescription: Review\n---\n` + tc.payload + `"}]}`
			r := f.request("POST", "/api/capabilities/skills", body, true)
			if r.Code != 400 {
				t.Errorf("lossy JSON accepted: %d %s", r.Code, r.Body.String())
			}
			r = f.request("GET", "/api/capabilities", "", true)
			var state domain.CapabilitySnapshot
			if err := json.Unmarshal(r.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if len(state.Skills) != 0 {
				t.Error("malformed Unicode was persisted")
			}
		})
	}
}

func TestSkillHTTPPreservesValidUnicodeAndLiteralEscapeText(t *testing.T) {
	for _, tc := range []struct{ name, payload, want string }{
		{"paired", `\ud83d\ude00`, "😀"},
		{"pair-boundaries", `\uD800\uDC00\uDBFF\uDFFF`, "\U00010000\U0010ffff"},
		{"literal-replacement", "\ufffd", "\ufffd"},
		{"escaped-replacement", `\ufffd`, "\ufffd"},
		{"literal-backslash-u", `\\ud800`, `\ud800`},
		{"escaped-quote", `\"literal\"`, `"literal"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.authenticate()
			body := `{"expected_revision":0,"source":"manual","files":[{"path":"SKILL.md","content":"---\nname: unicode-review\ndescription: Review\n---\n` + tc.payload + `"}]}`
			r := f.request("POST", "/api/capabilities/skills", body, true)
			if r.Code != 200 {
				t.Fatalf("valid Unicode rejected: %d %s", r.Code, r.Body.String())
			}
			var saved struct {
				Revision domain.SkillRevision `json:"revision"`
			}
			if err := json.Unmarshal(r.Body.Bytes(), &saved); err != nil {
				t.Fatal(err)
			}
			want := "---\nname: unicode-review\ndescription: Review\n---\n" + tc.want
			if saved.Revision.Files[0].Content != want {
				t.Fatalf("content changed: %q", saved.Revision.Files[0].Content)
			}
			r = f.request("GET", fmt.Sprintf("/api/capabilities/skills/%s/revisions/1", saved.Revision.ID), "", true)
			if r.Code != 200 {
				t.Fatal(r.Code)
			}
			if err := json.Unmarshal(r.Body.Bytes(), &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Revision.Files[0].Content != want {
				t.Fatal("stored content changed")
			}
		})
	}
}

func TestSkillRawValidationPreservesJSONBoundary(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"files":[],"unexpected":true}`, 400},
		{`{"files":[]} {}`, 400},
		{`{"source":"` + strings.Repeat("x", 1<<20) + `","files":[]}`, 413},
	} {
		if r := f.request("POST", "/api/capabilities/skills", tc.body, true); r.Code != tc.want {
			t.Fatalf("%d %s", r.Code, r.Body.String())
		}
	}
}
