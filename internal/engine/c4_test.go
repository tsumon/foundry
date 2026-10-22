package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/c4verify"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/sandbox"
	"foundry/internal/store"
	"foundry/internal/workspace"
)

// Removing the early gate would import source and create a mission without the
// Go-only capability. A request's mode declaration must never be permission.
func TestC4CreationClosedBeforeEffects(t *testing.T) {
	e, s, repo, data := importFixture(t)
	r := createImportRequest(t, e, repo)
	r.Spec.C4 = c4Profile()
	if _, err := e.Create(context.Background(), r); err == nil {
		t.Fatal("C4 created without capability")
	}
	ms, _ := s.ListMissions()
	entries, _ := os.ReadDir(data)
	if len(ms) != 0 || len(entries) != 0 {
		t.Fatal("closed creation had effects")
	}
}

func c4Profile() *domain.C4Profile {
	return &domain.C4Profile{SchemaVersion: 1, ID: "c4_go_v1", Mode: "synthetic", ImageID: "sha256:" + strings.Repeat("a", 64), Platform: "linux/arm64", DockerPath: "/synthetic/docker", Endpoint: "unix:///synthetic/docker.sock", Verifier: "/opt/foundry/verify-go-v1"}
}

type c4Forbidden struct{ calls int }

func (f *c4Forbidden) Complete(context.Context, model.Request) (model.Result, error) {
	f.calls++
	return model.Result{}, errors.New("normal gateway forbidden")
}
func (f *c4Forbidden) Resolve(context.Context, string) (string, error) {
	f.calls++
	return "", errors.New("legacy resolve forbidden")
}
func (f *c4Forbidden) Reconcile(context.Context) error {
	f.calls++
	return errors.New("legacy reconcile forbidden")
}
func (f *c4Forbidden) Run(context.Context, string, string, string, string) (domain.CommandResult, error) {
	f.calls++
	return domain.CommandResult{}, errors.New("legacy run forbidden")
}

type c4Driver struct {
	creates, starts, removes, inspects int
	labels                             map[string]string
	name, path, dbPath                 string
	class                              string
	cleanupFail                        bool
	beforeResult, afterCreate          func()
	beforeRemove                       func()
	waiting                            chan struct{}
}

func (d *c4Driver) Create(_ context.Context, c sandbox.C4Command) (string, error) {
	d.creates++
	d.labels = map[string]string{}
	for i, a := range c.Args {
		if a == "--label" {
			kv := strings.SplitN(c.Args[i+1], "=", 2)
			d.labels[kv[0]] = kv[1]
		}
		if a == "--name" {
			d.name = c.Args[i+1]
		}
		if a == "--mount" {
			for _, x := range strings.Split(c.Args[i+1], ",") {
				if strings.HasPrefix(x, "src=") {
					d.path = strings.TrimPrefix(x, "src=")
				}
				if strings.HasPrefix(x, "source=") {
					d.path = strings.TrimPrefix(x, "source=")
				}
			}
		}
	}
	if d.afterCreate != nil {
		d.afterCreate()
	}
	return strings.Repeat("b", 64), nil
}
func (d *c4Driver) Start(context.Context, sandbox.C4Command) error { d.starts++; return nil }
func (d *c4Driver) Wait(ctx context.Context, _ sandbox.C4Command) error {
	if d.waiting != nil {
		close(d.waiting)
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func (d *c4Driver) Inspect(context.Context, sandbox.C4Command) (sandbox.C4Inspection, error) {
	d.inspects++
	class := d.class
	if class == "" {
		class = "passed"
	}
	if b, err := os.ReadFile(filepath.Join(d.path, "main.go")); err == nil && strings.Contains(string(b), "broken") {
		class = "test_failed"
	}
	code := 0
	if class != "passed" {
		code = 1
	}
	return sandbox.C4Inspection{ID: strings.Repeat("b", 64), Name: d.name, Labels: d.labels, ExitCode: code, Verification: &c4verify.Result{Class: class, ExitCode: code, Tests: 1}}, nil
}
func (d *c4Driver) Logs(_ context.Context, _ sandbox.C4Command, w io.Writer) (bool, error) {
	if d.beforeResult != nil {
		d.beforeResult()
	}
	_, err := io.WriteString(w, "Synthetic verification; no Docker or Go process ran.")
	return false, err
}
func (d *c4Driver) Remove(context.Context, sandbox.C4Command) error {
	if d.beforeRemove != nil {
		d.beforeRemove()
	}
	d.removes++
	if d.cleanupFail {
		return errors.New("synthetic cleanup failure")
	}
	return nil
}

func c4Fixture(t *testing.T) (*Engine, *store.Store, domain.Mission, *c4Driver, *c4Forbidden, string) {
	return c4FixtureMode(t, false)
}
func c4FixtureMode(t *testing.T, controlled bool) (*Engine, *store.Store, domain.Mission, *c4Driver, *c4Forbidden, string) {
	t.Helper()
	e, _, repo, data := importFixture(t)
	dbPath := filepath.Join(t.TempDir(), "c4.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	e = New(s, nil, Options{DataDir: data})
	for name, body := range map[string]string{"main.go": "package demo\nfunc Value() int { return 1 }\n", "main_test.go": "package demo\n", "go.mod": "module demo\n\ngo 1.27.3\n", "go.sum": "", "config.json": "{}"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	importGit(t, repo, "add", ".")
	importGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Go fixture")
	if err := s.PutProfile(domain.Profile{ID: "synthetic", Name: "Synthetic", Kind: "synthetic", Protocol: "chat_completions", BaseURL: "https://synthetic.invalid", Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	d := &c4Driver{dbPath: dbPath}
	forbidden := &c4Forbidden{}
	e.gateway = forbidden
	e.runner = forbidden
	e.options.C4 = NewC4SyntheticCapability(sandbox.NewC4SyntheticDriver(d), &scripted{}, "/synthetic/docker", "unix:///synthetic/docker.sock")
	v, err := e.InspectRepository(context.Background(), domain.RepositoryInspectionRequest{RepoPath: repo, Consent: true, Spec: domain.RepositoryImportSpec{Scope: []string{"main.go"}, Acceptance: []string{"/opt/foundry/verify-go-v1"}, Image: "synthetic-c4"}})
	if err != nil || v.Status != "ready" {
		t.Fatal(v, err)
	}
	p := c4Profile()
	if controlled {
		p.ControlVersion = 1
		digest, _, _, err := workspace.PrepareRepositoryImport(context.Background(), v.Root, v.Commit, filepath.Join(t.TempDir(), "admitted"), strings.Repeat("d", 32), v.Spec)
		if err != nil {
			t.Fatal(err)
		}
		c := C4Activation{SchemaVersion: 1, Enabled: true, Profile: *p, SourceRoot: v.Root, BaseCommit: v.Commit, BaseDigest: digest, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
		cap, err := NewC4ControlledSyntheticCapability(c, sandbox.NewC4SyntheticDriver(d), &scripted{}, func(context.Context) (sandbox.C4ReadinessResult, error) {
			return sandbox.C4ReadinessResult{Fingerprint: strings.Repeat("c", 64)}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		e.options.C4, e.options.C4Controlled = nil, cap
		repo = v.Root
	}
	m, err := e.Create(context.Background(), CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: v.ID, Digest: v.Digest}, CodingVersion: 1, WorkspaceMode: "generations_v1", Title: "C4 synthetic", Goal: "change value", RepoPath: repo, Spec: domain.Spec{Scope: v.Spec.Scope, Acceptance: v.Spec.Acceptance, Image: v.Spec.Image, ProfileID: "synthetic", C4: p}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e, s, m, d, forbidden, repo
}

func c4Baseline(t *testing.T, e *Engine, m domain.Mission) domain.Mission {
	t.Helper()
	m, err := e.PrepareC4Baseline(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	o := m.C4Operations[len(m.C4Operations)-1]
	m, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	m, err = e.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil && !errors.Is(err, sandbox.ErrC4Execution) {
		t.Fatal(err)
	}
	return m
}

// Catches preparation granting consent, replay after consumption, baseline being
// mistaken for delivery evidence, and dropping it during ordinary epoch reset.
func TestC4BaselineIndependentConsentAndPlan(t *testing.T) {
	e, s, m, d, f, repo := c4Fixture(t)
	before := importSourceDigest(t, repo)
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("planned without baseline")
	}
	if _, err := e.Chat(context.Background(), m.ID, m.Revision, "hello"); err == nil {
		t.Fatal("chat without context")
	}
	m, err := e.PrepareC4Baseline(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	o := m.C4Operations[0]
	if d.creates != 0 || len(m.Approvals) != 1 {
		t.Fatal("preparation granted consent")
	}
	if _, err = e.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest); err == nil {
		t.Fatal("execute without approval")
	}
	if _, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision-1, o.ID, o.MaterialDigest); err == nil {
		t.Fatal("stale revision approved")
	}
	if _, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, strings.Repeat("0", 64)); err == nil {
		t.Fatal("stale material approved")
	}
	m, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	if d.creates != 0 {
		t.Fatal("approval dispatched")
	}
	m, err = e.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	if !m.C4Operations[0].Passed() || len(m.Evidence) != 0 {
		t.Fatal("baseline mixed with candidate", m.C4Operations)
	}
	if _, err = e.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest); err == nil {
		t.Fatal("duplicate dispatched")
	}
	if _, err = e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("baseline allowed delivery")
	}
	e.options.C4.models = &scripted{outputs: []string{`{"summary":"change","steps":[{"id":"one","title":"change","description":"update","depends_on":[],"files":["main.go"]}]}`}}
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if m.State != "awaiting_approval" || m.C4Operations[0].Epoch != o.Epoch || m.Epoch <= o.Epoch {
		t.Fatal(m.State, m.Error, m.C4Operations)
	}
	if d.creates != 1 || f.calls != 0 || before != importSourceDigest(t, repo) {
		t.Fatal("escaped or changed source", d.creates, f.calls)
	}
}

func TestC4CandidateFailureRepairPassAndNoLegacyEscape(t *testing.T) {
	testC4CandidateFailureRepairPass(t, false)
}
func TestC4ControlledCandidateFailureRepairPass(t *testing.T) {
	testC4CandidateFailureRepairPass(t, true)
}
func testC4CandidateFailureRepairPass(t *testing.T, controlled bool) {
	e, s, m, d, f, repo := c4FixtureMode(t, controlled)
	before := importSourceDigest(t, repo)
	if controlled {
		m = controlReady(t, e, m)
	}
	m = c4Baseline(t, e, m)
	// Changing live settings cannot redirect this mission's frozen profiles.
	if err := s.PutProfile(domain.Profile{ID: "real-after-create", Name: "unused real declaration", Kind: "openai", Protocol: "chat_completions", BaseURL: "https://example.invalid", Model: "not-used"}); err != nil {
		t.Fatal(err)
	}
	settings, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.DefaultProfileID = "real-after-create"
	for _, role := range domain.RolePolicyRoles() {
		settings.RoleBindings[role] = "real-after-create"
	}
	if err := s.PutSettings(settings); err != nil {
		t.Fatal(err)
	}
	script := &scripted{outputs: []string{`{"summary":"change","steps":[{"id":"one","title":"change","description":"update","depends_on":[],"files":["main.go"]}]}`, `{"summary":"changed","edits":[{"path":"main.go","content":"package demo // broken\n"}]}`, `{"summary":"repaired","edits":[{"path":"main.go","content":"package demo\nfunc Value() int { return 2 }\n"}]}`, codingPass, codingPass, `{"answer":"Synthetic explanation"}`}}
	if controlled {
		e.options.C4Controlled.models = &codingReviewGateway{script: script}
	} else {
		e.options.C4.models = &codingReviewGateway{script: script}
	}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if m.State != "awaiting_approval" {
		t.Fatal(m.State, m.Error)
	}
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if m.State != "awaiting_delivery" {
		t.Fatal(m.State, m.Error)
	}
	if len(m.C4Operations) != 3 || m.C4Operations[1].Result.Class != "test_failed" || !m.C4Operations[2].Passed() || len(m.Evidence) != 2 || m.Evidence[0].EffectiveSource() != "synthetic_stub" {
		t.Fatal("wrong candidate trace/source", m.C4Operations, m.Evidence)
	}
	if m.C4Operations[1].CandidateDigest == m.C4Operations[2].CandidateDigest {
		t.Fatal("same generation verified twice")
	}
	tampered := m
	tampered.Evidence = append([]domain.Verification(nil), m.Evidence...)
	tampered.Evidence[len(tampered.Evidence)-1].ID = "unrelated-command"
	if c4Delivery(tampered) == nil {
		t.Fatal("delivery accepted evidence detached from C4 operation")
	}
	if _, err := e.Chat(context.Background(), m.ID, m.Revision, "explain"); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	m, err = e.Deliver(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	report, err := e.ReadDelivery(m.ID, "report")
	if err != nil || !strings.Contains(string(report), "C4 · Synthetic") || !strings.Contains(string(report), "baseline") {
		t.Fatal("missing C4 report", string(report), err)
	}
	if f.calls != 0 || d.creates != 3 || d.removes != 3 || before != importSourceDigest(t, repo) {
		t.Fatal("escaped or changed source", f.calls, d.creates, d.removes)
	}
}

func TestC4FrozenProfilesContextAndProtectedInputs(t *testing.T) {
	for _, kind := range []string{"account", "real_url", "real_kind", "extra_role", "route", "key", "mode", "version", "acceptance", "protected_test", "protected_module", "protected_sum", "protected_config"} {
		t.Run(kind, func(t *testing.T) {
			e, s, m, d, f, _ := c4Fixture(t)
			if strings.HasPrefix(kind, "protected_") {
				path := map[string]string{"protected_test": "main_test.go", "protected_module": "go.mod", "protected_sum": "go.sum", "protected_config": "config.json"}[kind]
				m = c4Baseline(t, e, m)
				raw, _ := json.Marshal(map[string]any{"summary": "tamper", "steps": []any{map[string]any{"id": "one", "title": "tamper", "description": "update", "depends_on": []string{}, "files": []string{path}}}})
				e.options.C4.models = &scripted{outputs: []string{string(raw)}}
				if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
					t.Fatal(err)
				}
				finishCoding(e)
				m, _ = s.GetMission(m.ID)
				if m.State != "blocked" || d.creates != 1 || f.calls != 0 {
					t.Fatal("protected plan accepted", m.State, m.Error)
				}
				return
			}
			mutated := m
			p := mutated.Profiles[domain.Main]
			switch kind {
			case "extra_role":
				mutated.Profiles[domain.Role("future")] = domain.Profile{ID: "real", Kind: "openai", Model: "real", BaseURL: "https://example.invalid"}
			case "account":
				p.AccountID = "forbidden"
			case "real_url":
				p.BaseURL = "https://example.invalid"
			case "real_kind":
				p.Kind = "openai"
			case "route":
				p.RouteID = "forbidden"
			case "key":
				p.HasKey = true
			case "mode":
				cp := *m.Spec.C4
				cp.Mode = "docker"
				mutated.Spec.C4 = &cp
			case "version":
				cp := *m.Spec.C4
				cp.SchemaVersion = 2
				mutated.Spec.C4 = &cp
			case "acceptance":
				mutated.Spec.Acceptance = []string{"/opt/foundry/verify-go-v1 --unsafe"}
			}
			mutated.Profiles[domain.Main] = p
			if err := e.preflightModelRoles(context.Background(), mutated, domain.Planner); err == nil {
				t.Fatal("non-synthetic frozen role accepted", kind)
			}
			if d.creates != 0 || f.calls != 0 {
				t.Fatal("preflight escaped", kind)
			}
		})
	}
}

func TestC4MaterialRevisionAndCleanupFence(t *testing.T) {
	for _, stage := range []string{"prepared", "approved"} {
		t.Run(stage, func(t *testing.T) {
			e, s, m, d, f, _ := c4Fixture(t)
			m, err := e.PrepareC4Baseline(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			o := m.C4Operations[0]
			if stage == "approved" {
				m, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
				if err != nil {
					t.Fatal(err)
				}
			}
			m, err = s.UpdateMission(m.ID, m.Revision, "test.concurrent", func(m *domain.Mission) error { m.Title = "changed annotation"; return nil })
			if err != nil {
				t.Fatal(err)
			}
			if stage == "prepared" {
				_, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
			} else {
				_, err = e.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
			}
			if err == nil || d.creates != 0 || f.calls != 0 {
				t.Fatal("stale material revision consumed", err, d.creates, f.calls)
			}
		})
	}
	// A successful historical baseline does not bypass unresolved candidate cleanup.
	e, s, m, d, _, _ := c4Fixture(t)
	m = c4Baseline(t, e, m)
	d.cleanupFail = true
	script := &scripted{outputs: []string{`{"summary":"change","steps":[{"id":"one","title":"change","description":"update","depends_on":[],"files":["main.go"]}]}`, `{"summary":"changed","edits":[{"path":"main.go","content":"package demo\n"}]}`}}
	e.options.C4.models = script
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if _, err := e.PrepareC4Baseline(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("prepared another run with unresolved candidate cleanup")
	}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		finishCoding(e)
		t.Fatal("replanned with unconfirmed cleanup")
	}
}

func TestC4CreateRejectsRealSelectionAndRequestTamperBeforeSourceRead(t *testing.T) {
	e, s, m, d, f, repo := c4Fixture(t)
	receipt, err := s.GetRepositoryInspection(m.RepositoryImport.InspectionID)
	if err != nil {
		t.Fatal(err)
	}
	request := CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: receipt.ID, Digest: receipt.Digest}, CodingVersion: 1, WorkspaceMode: "generations_v1", Title: "C4", Goal: "change", RepoPath: repo, Spec: domain.Spec{Scope: receipt.Spec.Scope, Protected: receipt.Spec.Protected, Acceptance: receipt.Spec.Acceptance, Image: receipt.Spec.Image, ProfileID: "synthetic", C4: c4Profile()}}
	// A missing original source proves rejection happened at the selection gate,
	// not after admitting and trying to inspect/import the path.
	if err = os.Rename(repo, repo+"-gone"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(repo+"-gone", repo)
	for _, kind := range []string{"mode", "version", "coding", "workspace", "import", "acceptance", "real_role"} {
		t.Run(kind, func(t *testing.T) {
			r := request
			cp := *r.Spec.C4
			r.Spec.C4 = &cp
			switch kind {
			case "mode":
				r.Spec.C4.Mode = "docker"
			case "version":
				r.Spec.C4.SchemaVersion = 2
			case "coding":
				r.CodingVersion = 0
			case "workspace":
				r.WorkspaceMode = ""
			case "import":
				r.ImportVersion = 0
				r.ImportSelection = nil
			case "acceptance":
				r.Spec.Acceptance = []string{"go test ./..."}
			case "real_role":
				if err := s.PutProfile(domain.Profile{ID: "real", Name: "not permitted", Kind: "openai", Protocol: "chat_completions", BaseURL: "https://example.invalid", Model: "real"}); err != nil {
					t.Fatal(err)
				}
				settings, err := s.GetSettings()
				if err != nil {
					t.Fatal(err)
				}
				settings.RoleBindings[domain.Main] = "real"
				if err = s.PutSettings(settings); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = e.Create(context.Background(), r); !errors.Is(err, ErrC4Unavailable) {
				t.Fatal("did not reject before source", kind, err)
			}
		})
	}
	after, _ := s.ListMissions()
	if len(after) != 1 || d.creates != 0 || f.calls != 0 {
		t.Fatal("creation effects", len(after), d.creates, f.calls)
	}
}

func TestC4CanonicalHandoffRejectsMissionAlias(t *testing.T) {
	e, _, m, d, _, _ := c4Fixture(t)
	m, err := e.PrepareC4Baseline(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	o := m.C4Operations[0]
	m, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	root := e.dir(m.ID)
	moved := root + "-moved"
	if err = os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(moved, root); err != nil {
		t.Fatal(err)
	}
	defer func() { os.Remove(root); os.Rename(moved, root) }()
	if _, err = e.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest); err == nil || d.creates != 0 {
		t.Fatal("mission symlink accepted", err, d.creates)
	}
}

func TestC4InfrastructureAndUncleanBaselineCannotRepair(t *testing.T) {
	for _, kind := range []string{"baseline_failed", "baseline_cleanup", "candidate_infrastructure", "candidate_cleanup", "unchanged_repair"} {
		t.Run(kind, func(t *testing.T) {
			e, s, m, d, f, _ := c4Fixture(t)
			if kind == "baseline_failed" {
				d.class = "test_failed"
			}
			if kind == "baseline_cleanup" {
				d.cleanupFail = true
			}
			m = c4Baseline(t, e, m)
			if strings.HasPrefix(kind, "baseline") {
				if _, err := e.Plan(context.Background(), m.ID, m.Revision); err == nil {
					t.Fatal("unclean baseline planned")
				}
				if f.calls != 0 {
					t.Fatal("escaped")
				}
				return
			}
			if kind == "candidate_infrastructure" {
				d.class = "infrastructure_failed"
			}
			if kind == "candidate_cleanup" {
				d.cleanupFail = true
			}
			edit := `{"summary":"changed","edits":[{"path":"main.go","content":"package demo // broken\n"}]}`
			if kind == "candidate_infrastructure" {
				edit = `{"summary":"changed","edits":[{"path":"main.go","content":"package demo\n"}]}`
			}
			script := &scripted{outputs: []string{`{"summary":"change","steps":[{"id":"one","title":"change","description":"update","depends_on":[],"files":["main.go"]}]}`, edit, edit}}
			e.options.C4.models = script
			if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			if m.State != "blocked" || d.creates != 2 || f.calls != 0 {
				t.Fatal(m.State, m.Error, d.creates, f.calls)
			}
			debug := 0
			for _, r := range script.roles {
				if r == domain.Debugger {
					debug++
				}
			}
			if kind == "unchanged_repair" && debug != 1 || kind != "unchanged_repair" && debug != 0 {
				t.Fatal("wrong repair authority", script.roles)
			}
		})
	}
}
