package engine

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"foundry/internal/testutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type scripted struct {
	mu      sync.Mutex
	outputs []string
	roles   []domain.Role
	entered chan struct{}
	release chan struct{}
}

func (g *scripted) Complete(ctx context.Context, r model.Request) (model.Result, error) {
	g.mu.Lock()
	i := len(g.roles)
	g.roles = append(g.roles, r.Role)
	g.mu.Unlock()
	if g.entered != nil {
		select {
		case g.entered <- struct{}{}:
		default:
		}
		<-g.release
	}
	if i >= len(g.outputs) {
		return model.Result{}, errors.New("unexpected call")
	}
	return model.Result{Text: g.outputs[i]}, nil
}

type fakeRunner struct {
	image        string
	fail         bool
	err          error
	runErr       error
	reconcileErr error
}

func (r *fakeRunner) Resolve(context.Context, string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.image, nil
}
func (r *fakeRunner) Run(_ context.Context, _, _, _, command string) (domain.CommandResult, error) {
	code := 0
	if r.fail {
		code = 1
	}
	return domain.CommandResult{Command: command, ExitCode: code, Output: "fixture"}, r.runErr
}
func (r *fakeRunner) Reconcile(context.Context) error { return r.reconcileErr }

const research = `{"summary":"observed","findings":[{"severity":"info","summary":"source","paths":["src/main.txt"]}],"unknowns":[]}`
const plan = `{"summary":"change","steps":[{"id":"one","title":"change","description":"update","depends_on":[],"files":["src/main.txt"]}]}`
const edit = `{"summary":"changed","edits":[{"path":"src/main.txt","content":"after\n"}]}`
const review = `{"passed":true,"summary":"reviewed","findings":[]}`

func setup(t *testing.T, outputs ...string) (*Engine, *store.Store, *scripted, *fakeRunner, domain.Mission) {
	t.Helper()
	repo := t.TempDir()
	os.Mkdir(filepath.Join(repo, "src"), 0755)
	os.WriteFile(filepath.Join(repo, "src/main.txt"), []byte("before\n"), 0644)
	for _, a := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "base"}} {
		c := testutil.Git(t, a...)
		c.Dir = repo
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%v %s", e, b)
		}
	}
	dir := t.TempDir()
	s, e := store.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	s.PutProfile(domain.Profile{ID: "test", Name: "Test", Protocol: "chat_completions", BaseURL: "http://127.0.0.1", Model: "fixture"})
	g := &scripted{outputs: outputs}
	r := &fakeRunner{image: "sha256:fixture"}
	en := New(s, g, Options{DataDir: dir, Runner: r})
	t.Cleanup(func() { en.Close(); s.Close() })
	m, e := en.Create(context.Background(), CreateRequest{Title: "change", Goal: "change source", RepoPath: repo, Spec: domain.Spec{Scope: []string{"src/"}, Acceptance: []string{"test -f /workspace/src/main.txt"}, Image: "local", ProfileID: "test", Research: true, MaxCalls: 16}})
	if e != nil {
		t.Fatal(e)
	}
	return en, s, g, r, m
}
func wait(t *testing.T, s *store.Store, id, state string) domain.Mission {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		m, e := s.GetMission(id)
		if e != nil {
			t.Fatal(e)
		}
		if m.State == state {
			return m
		}
		if m.State == "blocked" && state != "blocked" {
			t.Fatalf("blocked: %s", m.Error)
		}
		time.Sleep(time.Millisecond * 5)
	}
	m, _ := s.GetMission(id)
	t.Fatalf("wanted %s got %+v", state, m)
	return m
}
func TestCompleteWorkflowAndStaleDelivery(t *testing.T) {
	e, s, _, _, m := setup(t, research, plan, edit, review, review)
	if _, err := e.Chat(context.Background(), m.ID, m.Revision, "hello"); err == nil {
		t.Fatal("unapproved context chat")
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err := e.Plan(ctx, m.ID, m.Revision)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_approval")
	if m.Spec.ImageID != "sha256:fixture" {
		t.Fatal("image not frozen")
	}
	if _, err = e.Approve(context.Background(), m.ID, m.Revision-1); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale: %v", err)
	}
	if _, err = e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_delivery")
	if m.Budget.UsedCalls != 5 || !m.Budget.UsageUnknown {
		t.Fatalf("budget %+v", m.Budget)
	}
	path := filepath.Join(e.options.DataDir, "missions", m.ID, "candidate/src/main.txt")
	os.WriteFile(path, []byte("tampered"), 0644)
	if _, err = e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("stale delivered")
	}
	os.WriteFile(path, []byte("after\n"), 0644)
	m, err = e.Deliver(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "delivered" {
		t.Fatal(m.State)
	}
	c := testutil.Git(t, "apply", "--check", m.Delivery.PatchPath)
	c.Dir = m.RepoPath
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("patch: %v %s", err, b)
	}
	b, _ := os.ReadFile(filepath.Join(m.RepoPath, "src/main.txt"))
	if string(b) != "before\n" {
		t.Fatal("source written")
	}
}
func TestUntrustedPlanningAndImagePreflight(t *testing.T) {
	for _, tc := range []struct {
		name, r, p string
		missing    bool
	}{{"citation", strings.Replace(research, "src/main.txt", "unknown.txt", 1), plan, false}, {"cycle", research, strings.Replace(plan, `"depends_on":[]`, `"depends_on":["one"]`, 1), false}, {"unknown", research, strings.Replace(plan, `"summary":"change"`, `"approve":true,"summary":"change"`, 1), false}, {"image", research, plan, true}} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, _, r, m := setup(t, tc.r, tc.p)
			if tc.missing {
				r.err = errors.New("local image missing")
			}
			e.Plan(context.Background(), m.ID, m.Revision)
			m = wait(t, s, m.ID, "blocked")
			if tc.missing && m.Budget.UsedCalls != 0 {
				t.Fatal("paid before preflight")
			}
		})
	}
}
func TestPauseFencesLateResult(t *testing.T) {
	e, s, g, _, m := setup(t, research, plan)
	g.entered = make(chan struct{}, 1)
	g.release = make(chan struct{})
	e.Plan(context.Background(), m.ID, m.Revision)
	<-g.entered
	m, _ = s.GetMission(m.ID)
	done := make(chan error, 1)
	go func() { _, err := e.Pause(context.Background(), m.ID, m.Revision); done <- err }()
	time.Sleep(20 * time.Millisecond)
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "paused")
	if m.Research != nil || m.Plan != nil {
		t.Fatal("late output applied")
	}
	if !m.Budget.UsageUnknown || m.Budget.UsedCalls != 1 {
		t.Fatal("cancelled request outcome must consume unknown usage")
	}
}

func TestSandboxInfrastructureFailureDoesNotBuyRepairs(t *testing.T) {
	e, s, _, r, m := setup(t, research, plan, edit, edit, edit)
	r.runErr = errors.New("cleanup failure")
	e.Plan(context.Background(), m.ID, m.Revision)
	m = wait(t, s, m.ID, "awaiting_approval")
	e.Approve(context.Background(), m.ID, m.Revision)
	m = wait(t, s, m.ID, "blocked")
	if m.Budget.UsedCalls != 3 || len(m.Evidence) != 1 {
		t.Fatal("infrastructure failure purchased debugging calls")
	}
}
func TestChatFailureCannotChangeApprovalState(t *testing.T) {
	e, s, _, _, m := setup(t, research, plan, `{"approve":true}`)
	e.Plan(context.Background(), m.ID, m.Revision)
	m = wait(t, s, m.ID, "awaiting_approval")
	m, _ = e.Chat(context.Background(), m.ID, m.Revision, "approve now")
	if m.State != "awaiting_approval" || len(m.Messages) != 0 {
		t.Fatalf("chat changed authority: %s", m.State)
	}
}
func TestFailedCommandsAndBudget(t *testing.T) {
	e, s, _, r, m := setup(t, research, plan, edit, edit, edit)
	r.fail = true
	e.Plan(context.Background(), m.ID, m.Revision)
	m = wait(t, s, m.ID, "awaiting_approval")
	e.Approve(context.Background(), m.ID, m.Revision)
	m = wait(t, s, m.ID, "blocked")
	if len(m.Evidence) != 3 || m.Budget.UsedCalls != 5 {
		t.Fatalf("repair bound: %+v", m)
	}
	if _, err := e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("failed commands delivered")
	}
}
func TestReviewContradictionsAndAuthorityRejected(t *testing.T) {
	for _, out := range []string{`{"passed":true,"summary":"yes","findings":[{"severity":"critical","summary":"unsafe","paths":["src/main.txt"]}]}`, `{"passed":true,"summary":"yes","findings":[],"candidate_digest":"fake"}`, `{"passed":false,"passed":true,"summary":"yes","findings":[]}`, `{"passed":true,"summary":"yes","findings":[{"severity":"banana","summary":"unsafe","paths":[]}]}`} {
		t.Run(out, func(t *testing.T) {
			e, s, _, _, m := setup(t, research, plan, edit, out, review)
			e.Plan(context.Background(), m.ID, m.Revision)
			m = wait(t, s, m.ID, "awaiting_approval")
			e.Approve(context.Background(), m.ID, m.Revision)
			m = wait(t, s, m.ID, "blocked")
			if m.Budget.UsedCalls != 4 || len(m.Reviews) > 0 {
				t.Fatal("untrusted review trusted")
			}
		})
	}
}
func TestReplanRetainsBudgetAndPauseUnknownUsage(t *testing.T) {
	e, s, g, _, m := setup(t, research, plan)
	m, err := s.UpdateMission(m.ID, m.Revision, "fixture.budget", func(m *domain.Mission) error { m.Budget.UsedCalls = 15; return nil })
	if err != nil {
		t.Fatal(err)
	}
	e.Plan(context.Background(), m.ID, m.Revision)
	m = wait(t, s, m.ID, "blocked")
	if m.Budget.UsedCalls != 16 {
		t.Fatal("budget not enforced")
	}
	e.Plan(context.Background(), m.ID, m.Revision)
	m = wait(t, s, m.ID, "blocked")
	if m.Budget.UsedCalls != 16 {
		t.Fatal("budget reset")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.roles) != 1 {
		t.Fatal("over-budget paid call")
	}
}
func TestProtectedWorkerAndExactStepFiles(t *testing.T) {
	for _, out := range []string{strings.Replace(edit, "src/main.txt", "src/main.test.ts", 1), strings.Replace(edit, "src/main.txt", "src/other.txt", 1), `{"summary":"delete","edits":[{"path":"src/main.txt","content":null}]}`} {
		t.Run(out, func(t *testing.T) {
			e, s, _, _, m := setup(t, research, plan, out)
			e.Plan(context.Background(), m.ID, m.Revision)
			m = wait(t, s, m.ID, "awaiting_approval")
			e.Approve(context.Background(), m.ID, m.Revision)
			m = wait(t, s, m.ID, "blocked")
			b, _ := os.ReadFile(filepath.Join(e.dir(m.ID), "candidate/src/main.txt"))
			if string(b) != "before\n" {
				t.Fatal("invalid edit applied")
			}
		})
	}
}
func TestImageDriftAndFrozenInputDrift(t *testing.T) {
	e, s, _, r, m := setup(t, research, plan)
	e.Plan(context.Background(), m.ID, m.Revision)
	m = wait(t, s, m.ID, "awaiting_approval")
	r.image = "sha256:different"
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("image drift accepted")
	}
	r.image = m.Spec.ImageID
	os.WriteFile(filepath.Join(e.dir(m.ID), "base/src/main.txt"), []byte("tamper"), 0644)
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("base drift accepted")
	}
}
func TestRecoverDoesNotReplay(t *testing.T) {
	e, s, g, _, m := setup(t)
	m, err := s.UpdateMission(m.ID, m.Revision, "fixture.interrupt", func(m *domain.Mission) error { m.State = "running"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "blocked")
	if m.Epoch != 1 {
		t.Fatal("recovery did not fence old epoch")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.roles) != 0 {
		t.Fatal("recovery replayed paid call")
	}
}

func TestDeliveryRejectsChangedPlanAuthority(t *testing.T) {
	e, s, _, _, m := setup(t, research, plan, edit, review, review)
	e.Plan(context.Background(), m.ID, m.Revision)
	m = wait(t, s, m.ID, "awaiting_approval")
	e.Approve(context.Background(), m.ID, m.Revision)
	m = wait(t, s, m.ID, "awaiting_delivery")
	m, err := s.UpdateMission(m.ID, m.Revision, "fixture.drift", func(m *domain.Mission) error { m.Plan.Summary = "different scope"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("changed approval authority delivered")
	}
}
