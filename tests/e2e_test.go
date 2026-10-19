package tests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/httpapi"
	"foundry/internal/model"
	"foundry/internal/store"
)

const (
	testAPIKey  = "acceptance-key-must-not-leak"
	researchOut = `{"summary":"observed committed source","findings":[{"severity":"info","summary":"greeting is stored in the committed source","paths":["src/main.txt"]}],"unknowns":[]}`
	planOut     = `{"summary":"update the greeting","steps":[{"id":"edit-greeting","title":"Update greeting","description":"Replace the greeting text","depends_on":[],"files":["src/main.txt"]}]}`
	editOut     = `{"summary":"updated greeting","edits":[{"path":"src/main.txt","content":"after\n"}]}`
	reviewOut   = `{"passed":true,"summary":"candidate and deterministic evidence agree","findings":[]}`
)

type memorySecrets struct {
	values map[string]string
}

func (s *memorySecrets) Set(id, value string) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[id] = value
	return nil
}

func (s *memorySecrets) Get(id string) (string, error) {
	value, ok := s.values[id]
	if !ok {
		return "", errors.New("missing test credential")
	}
	return value, nil
}

func (s *memorySecrets) Delete(id string) error {
	delete(s.values, id)
	return nil
}

type fakeProvider struct {
	server  *httptest.Server
	mu      sync.Mutex
	outputs []string
	calls   int
	errors  []string
}

func newFakeProvider(t *testing.T, outputs ...string) *fakeProvider {
	t.Helper()
	p := &fakeProvider{outputs: outputs}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			p.errors = append(p.errors, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.Path))
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
			p.errors = append(p.errors, "production adapter did not send the configured test credential")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var request struct {
			Model               string `json:"model"`
			MaxCompletionTokens int    `json:"max_completion_tokens"`
			Messages            []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			p.errors = append(p.errors, "invalid production adapter request")
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if request.Model != "fixture-model" || request.MaxCompletionTokens != 8192 || len(request.Messages) != 2 {
			p.errors = append(p.errors, fmt.Sprintf("unexpected adapter envelope: %+v", request))
			http.Error(w, "invalid envelope", http.StatusBadRequest)
			return
		}
		if p.calls >= len(p.outputs) {
			p.errors = append(p.errors, "unexpected additional provider call")
			http.Error(w, "no scripted output", http.StatusInternalServerError)
			return
		}
		output := p.outputs[p.calls]
		p.calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": output}}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakeProvider) snapshot() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, append([]string(nil), p.errors...)
}

type deterministicRunner struct {
	mu              sync.Mutex
	runs            int
	reconciliations int
}

func (r *deterministicRunner) Resolve(context.Context, string) (string, error) {
	return "sha256:acceptance-fixture", nil
}

func (r *deterministicRunner) Run(_ context.Context, _, candidate, _, command string) (domain.CommandResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs++
	result := domain.CommandResult{Command: command, Output: "deterministic verifier inspected candidate", ExitCode: 0}
	content, err := os.ReadFile(filepath.Join(candidate, "src/main.txt"))
	if err != nil || string(content) != "after\n" || command != "test -f /workspace/src/main.txt" {
		result.ExitCode = 1
		result.Output = "candidate or command did not match frozen acceptance input"
	}
	return result, nil
}

func (r *deterministicRunner) Reconcile(context.Context) error {
	r.mu.Lock()
	r.reconciliations++
	r.mu.Unlock()
	return nil
}

func TestFirstDeliveryWorkflowUsesProductionAdapterWithoutTouchingSource(t *testing.T) {
	for _, mode := range []string{"", domain.WorkspaceModeGenerationsV1} {
		name := mode
		if name == "" {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) { firstDeliveryWorkflow(t, mode) })
	}
}

func firstDeliveryWorkflow(t *testing.T, mode string) {
	repo := newGitRepository(t)
	beforeHead := gitOutput(t, repo, "rev-parse", "HEAD")
	beforeStatus := gitOutput(t, repo, "status", "--porcelain=v1", "--untracked-files=all")
	provider := newFakeProvider(t, researchOut, planOut, editOut, reviewOut, reviewOut)
	dataDir := t.TempDir()
	s, client, runner := configuredRuntime(t, dataDir, provider.server.URL)
	e := engine.New(s, client, engine.Options{DataDir: dataDir, Runner: runner})
	t.Cleanup(func() { _ = e.Close() })

	mission, err := e.Create(context.Background(), engine.CreateRequest{
		WorkspaceMode: mode,
		Title:         "Update greeting", Goal: "Change the committed greeting from before to after", RepoPath: repo,
		Spec: domain.Spec{
			Scope: []string{"src/"}, Acceptance: []string{"test -f /workspace/src/main.txt"},
			Image: "local-fixture", ProfileID: "acceptance-profile", Research: true, MaxCalls: 16,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Plan(context.Background(), mission.ID, mission.Revision); err != nil {
		t.Fatal(err)
	}
	mission = waitForState(t, s, mission.ID, "awaiting_approval")
	if mission.Research == nil || mission.Plan == nil || len(mission.Research.Findings) != 1 {
		t.Fatalf("research/plan not persisted: %+v", mission)
	}
	if _, err = e.Approve(context.Background(), mission.ID, mission.Revision); err != nil {
		t.Fatal(err)
	}
	mission = waitForState(t, s, mission.ID, "awaiting_delivery")
	if len(mission.Evidence) != 1 || !mission.Evidence[0].Passed || len(mission.Reviews) != 2 {
		t.Fatalf("verification/reviews not complete: %+v", mission)
	}

	candidatePath := filepath.Join(dataDir, "missions", mission.ID, "candidate", "src", "main.txt")
	if mode != "" {
		candidatePath = filepath.Join(dataDir, "missions", mission.ID, "generations", mission.CandidateGeneration.ID, "tree", "src", "main.txt")
	}
	if err := os.WriteFile(candidatePath, []byte("tampered\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Deliver(context.Background(), mission.ID, mission.Revision); err == nil {
		t.Fatal("delivery accepted a candidate modified after verification")
	}
	if err := os.WriteFile(candidatePath, []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mission, err = e.Deliver(context.Background(), mission.ID, mission.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if mission.State != "delivered" || mission.Delivery == nil {
		t.Fatalf("delivery not exported: %+v", mission)
	}

	applyRepo := filepath.Join(t.TempDir(), "apply-target")
	runCommand(t, "", "git", "clone", "-q", repo, applyRepo)
	runCommand(t, applyRepo, "git", "apply", mission.Delivery.PatchPath)
	content, err := os.ReadFile(filepath.Join(applyRepo, "src/main.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "after\n" {
		t.Fatalf("exported patch did not produce expected source: %q", content)
	}
	report, err := os.ReadFile(mission.Delivery.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(report), testAPIKey) {
		t.Fatal("credential leaked into exported report")
	}
	if mode != "" {
		checkGenerationDownloads(t, s, e, client, mission)
	}
	afterHead := gitOutput(t, repo, "rev-parse", "HEAD")
	afterStatus := gitOutput(t, repo, "status", "--porcelain=v1", "--untracked-files=all")
	if beforeHead != afterHead || beforeStatus != afterStatus {
		t.Fatalf("source repository was modified by delivery workflow: head %q -> %q, status %q -> %q", beforeHead, afterHead, beforeStatus, afterStatus)
	}
	if source, err := os.ReadFile(filepath.Join(repo, "src/main.txt")); err != nil || string(source) != "before\n" {
		t.Fatalf("source worktree changed: %q err=%v", source, err)
	}
	if calls, providerErrors := provider.snapshot(); calls != 5 || len(providerErrors) != 0 {
		t.Fatalf("provider calls=%d errors=%v", calls, providerErrors)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.runs != 1 {
		t.Fatalf("deterministic verifier runs=%d", runner.runs)
	}
}

func checkGenerationDownloads(t *testing.T, s *store.Store, e *engine.Engine, client *model.Client, m domain.Mission) {
	t.Helper()
	handler := httpapi.New(s, e, client, &memorySecrets{}, httpapi.Options{})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4317/api/session", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:4317")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 200 || len(rr.Result().Cookies()) != 1 {
		t.Fatal("bootstrap", rr.Code)
	}
	cookie := rr.Result().Cookies()[0]
	get := func(kind string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4317/api/missions/"+m.ID+"/"+kind, nil)
		req.AddCookie(cookie)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}
	for _, kind := range []string{"patch", "report", "local-operations"} {
		rr = get(kind)
		if rr.Code != 200 || rr.Body.Len() == 0 {
			t.Fatal(kind, rr.Code, rr.Body.String())
		}
	}
	raw, err := os.ReadFile(m.Delivery.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(m.Delivery.ReportPath, []byte("drift"), 0600); err != nil {
		t.Fatal(err)
	}
	rr = get("report")
	if rr.Code != 404 {
		t.Fatal("drift download", rr.Code)
	}
	if err = os.WriteFile(m.Delivery.ReportPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Descriptive stored paths must not redirect the download even to a file
	// that has exactly the expected bytes.
	copyPath := filepath.Join(t.TempDir(), "report.md")
	if err = os.WriteFile(copyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateMission(m.ID, m.Revision, "fixture.path_drift", func(current *domain.Mission) error { current.Delivery.ReportPath = copyPath; return nil })
	if err != nil {
		t.Fatal(err)
	}
	rr = get("report")
	if rr.Code != 404 {
		t.Fatal("untrusted delivery path served", rr.Code)
	}
}

func TestRecoveryBlocksInflightMissionWithoutCallingProvider(t *testing.T) {
	repo := newGitRepository(t)
	provider := newFakeProvider(t)
	dataDir := t.TempDir()
	s, client, runner := configuredRuntime(t, dataDir, provider.server.URL)
	e := engine.New(s, client, engine.Options{DataDir: dataDir, Runner: runner})
	mission, err := e.Create(context.Background(), engine.CreateRequest{
		Title: "Interrupted work", Goal: "Prove restart does not replay a provider request", RepoPath: repo,
		Spec: domain.Spec{
			Scope: []string{"src/"}, Acceptance: []string{"test -f /workspace/src/main.txt"},
			Image: "local-fixture", ProfileID: "acceptance-profile", Research: true, MaxCalls: 16,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mission, err = s.UpdateMission(mission.ID, mission.Revision, "acceptance.interrupted", func(m *domain.Mission) error {
		m.State = "running"
		m.Stage = "build"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.Open(filepath.Join(dataDir, "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recoveredEngine := engine.New(reopened, client, engine.Options{DataDir: dataDir, Runner: runner})
	defer recoveredEngine.Close()
	if err := recoveredEngine.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, err := reopened.GetMission(mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != "blocked" || !strings.Contains(recovered.Error, "interrupted") || recovered.Epoch != mission.Epoch+1 {
		t.Fatalf("in-flight mission not fenced: %+v", recovered)
	}
	if calls, providerErrors := provider.snapshot(); calls != 0 || len(providerErrors) != 0 {
		t.Fatalf("recovery called provider: calls=%d errors=%v", calls, providerErrors)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.reconciliations != 1 {
		t.Fatalf("recovery did not reconcile the production runner boundary: %d", runner.reconciliations)
	}
}

func configuredRuntime(t *testing.T, dataDir, providerURL string) (*store.Store, *model.Client, *deterministicRunner) {
	t.Helper()
	s, err := store.Open(filepath.Join(dataDir, "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.PutProfile(domain.Profile{
		ID: "acceptance-profile", Name: "Acceptance fixture", Kind: "direct", Protocol: "chat_completions",
		BaseURL: providerURL + "/v1", Model: "fixture-model", HasKey: true,
	}); err != nil {
		t.Fatal(err)
	}
	secrets := &memorySecrets{values: map[string]string{"acceptance-profile": testAPIKey}}
	return s, model.NewClient(secrets), &deterministicRunner{}
}

func newGitRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src/main.txt"), []byte("before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, repo, "git", "init", "-q")
	runCommand(t, repo, "git", "add", "src/main.txt")
	runCommand(t, repo, "git", "-c", "user.name=Acceptance", "-c", "user.email=acceptance@example.invalid", "commit", "-qm", "base")
	return repo
}

func waitForState(t *testing.T, s *store.Store, id, wanted string) domain.Mission {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mission, err := s.GetMission(id)
		if err != nil {
			t.Fatal(err)
		}
		if mission.State == wanted {
			return mission
		}
		if mission.State == "blocked" && wanted != "blocked" {
			t.Fatalf("mission blocked: %s", mission.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	mission, _ := s.GetMission(id)
	t.Fatalf("wanted state %q, got %+v", wanted, mission)
	return domain.Mission{}
}

func gitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func runCommand(t *testing.T, directory, name string, args ...string) {
	t.Helper()
	command := exec.Command(name, args...)
	if directory != "" {
		command.Dir = directory
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
}
