package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"foundry/internal/c4fixture"
	"foundry/internal/c4verify"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/sandbox"
)

const c4FixtureGoal = c4fixture.Goal
const c4BaselineSource = c4fixture.BaselineSource
const c4BrokenSource = c4fixture.BrokenSource
const c4RepairedSource = c4fixture.RepairedSource
const c4FrozenTest = c4fixture.FrozenTest

type c4FixtureContainer struct {
	id, name, path   string
	labels           map[string]string
	started, removed bool
	removeAttempts   int
}
type c4FixtureDriver struct {
	mu         sync.Mutex
	containers map[string]*c4FixtureContainer
}

func (d *c4FixtureDriver) Create(_ context.Context, c sandbox.C4Command) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	item := &c4FixtureContainer{labels: map[string]string{}}
	for i, a := range c.Args {
		if i+1 >= len(c.Args) {
			continue
		}
		switch a {
		case "--name":
			item.name = c.Args[i+1]
		case "--label":
			parts := strings.SplitN(c.Args[i+1], "=", 2)
			if len(parts) == 2 {
				item.labels[parts[0]] = parts[1]
			}
		case "--mount":
			for _, part := range strings.Split(c.Args[i+1], ",") {
				if strings.HasPrefix(part, "src=") {
					item.path = strings.TrimPrefix(part, "src=")
				}
				if strings.HasPrefix(part, "source=") {
					item.path = strings.TrimPrefix(part, "source=")
				}
			}
		}
	}
	if item.name == "" || item.path == "" {
		return "", errors.New("synthetic create requires fixed handoff")
	}
	item.id = fmt.Sprintf("%x", sha256.Sum256([]byte(item.name)))
	if d.containers == nil {
		d.containers = map[string]*c4FixtureContainer{}
	}
	if d.containers[item.id] != nil {
		return "", errors.New("synthetic operation already created")
	}
	d.containers[item.id] = item
	return item.id, nil
}
func (d *c4FixtureDriver) item(c sandbox.C4Command) (*c4FixtureContainer, error) {
	id := c.CaptureID
	if len(c.Args) > 0 {
		id = c.Args[len(c.Args)-1]
	}
	item := d.containers[id]
	if item == nil || item.removed {
		return nil, errors.New("unknown synthetic exact container")
	}
	return item, nil
}
func (d *c4FixtureDriver) Start(_ context.Context, c sandbox.C4Command) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	item, err := d.item(c)
	if err != nil {
		return err
	}
	if item.started {
		return errors.New("synthetic replay refused")
	}
	item.started = true
	return nil
}
func (d *c4FixtureDriver) Wait(_ context.Context, c sandbox.C4Command) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	item, err := d.item(c)
	if err != nil {
		return err
	}
	if !item.started {
		return errors.New("synthetic wait before start")
	}
	return nil
}
func (d *c4FixtureDriver) Inspect(_ context.Context, c sandbox.C4Command) (sandbox.C4Inspection, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	item, err := d.item(c)
	if err != nil {
		return sandbox.C4Inspection{}, err
	}
	source, readErr := os.ReadFile(filepath.Join(item.path, "main.go"))
	tests, testErr := os.ReadFile(filepath.Join(item.path, "main_test.go"))
	marker, _ := os.ReadFile(filepath.Join(item.path, "fixture.json"))
	class, code, count := "infrastructure_failed", 2, 0
	if readErr == nil && testErr == nil && string(tests) == c4FrozenTest && !strings.Contains(string(marker), "cleanup_failure") {
		switch string(source) {
		case c4BaselineSource, c4RepairedSource:
			class, code, count = "passed", 0, 1
		case c4BrokenSource:
			class, code, count = "test_failed", 1, 1
		}
	}
	return sandbox.C4Inspection{ID: item.id, Name: item.name, Labels: item.labels, ExitCode: code, Verification: &c4verify.Result{Class: class, ExitCode: code, Tests: count}}, nil
}
func (d *c4FixtureDriver) Logs(_ context.Context, _ sandbox.C4Command, w io.Writer) (bool, error) {
	_, err := io.WriteString(w, "SYNTHETIC content comparison only (source_kind=synthetic_stub). No Go compiler, tests, Docker or container ran. Cleanup-failure scenario simulates infrastructure failure; explicit exact-owned cleanup cannot change that result.")
	return false, err
}
func (d *c4FixtureDriver) Remove(_ context.Context, c sandbox.C4Command) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	item, err := d.item(c)
	if err != nil {
		return err
	}
	item.removeAttempts++
	marker, _ := os.ReadFile(filepath.Join(item.path, "fixture.json"))
	if strings.Contains(string(marker), "cleanup_failure") && item.removeAttempts == 1 {
		return errors.New("SYNTHETIC first cleanup failure; separate explicit cleanup required")
	}
	item.removed = true
	return nil
}

func seedC4Repository(repo, mode string) error {
	if err := os.MkdirAll(repo, 0700); err != nil {
		return err
	}
	for name, body := range map[string]string{"main.go": c4BaselineSource, "main_test.go": c4FrozenTest, "go.mod": "module synthetic-c4\n\ngo 1.27.3\n", "go.sum": "", "fixture.json": fmt.Sprintf("{\"mode\":%q}\n", mode)} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0600); err != nil {
			return err
		}
	}
	// Only this freshly owned disposable repository is committed. No user source
	// or product Git write is reachable through any runtime action.
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Synthetic Acceptance", "-c", "user.email=synthetic@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "SYNTHETIC C4 frozen base"}} {
		cmd, cleanup, err := fixtureGitCommand(args...)
		if err != nil {
			return err
		}
		defer cleanup()
		cmd.Dir = repo
		if raw, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("seed C4 Git: %w %s", err, raw)
		}
	}
	return nil
}

func seedC4Scenarios(r *acceptanceRuntime) (err error) {
	settings, err := r.store.GetSettings()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, r.store.PutSettings(settings)) }()
	profile := domain.Profile{ID: "synthetic-c4", Name: "SYNTHETIC C4 accountless model", Kind: "synthetic", Protocol: "chat_completions", BaseURL: "https://synthetic.invalid", Model: "C4-LOCAL-FIXTURE-NOT-CLOUD"}
	if err = r.store.PutProfile(profile); err != nil {
		return err
	}
	if err = r.store.PutSettings(allRoleBindings(profile.ID, nil)); err != nil {
		return err
	}
	for _, mode := range []string{"baseline_pending", "cleanup_failure"} {
		repo := filepath.Join(r.Root, "synthetic-c4-"+mode)
		if err = seedC4Repository(repo, mode); err != nil {
			return err
		}
		receipt, e := r.engine.InspectRepository(context.Background(), domain.RepositoryInspectionRequest{RepoPath: repo, Consent: true, Spec: domain.RepositoryImportSpec{Scope: []string{"main.go"}, Protected: []string{"main_test.go", "go.mod", "go.sum", "fixture.json"}, Acceptance: []string{"/opt/foundry/verify-go-v1"}, Image: "synthetic:c4-fictional-no-image"}})
		if e != nil {
			return e
		}
		if receipt.Status != "ready" {
			return fmt.Errorf("C4 synthetic import not ready: %s", receipt.Status)
		}
		m, e := r.engine.Create(context.Background(), engine.CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: receipt.ID, Digest: receipt.Digest}, CodingVersion: 1, WorkspaceMode: domain.WorkspaceModeGenerationsV1, RepoPath: repo, Title: "SYNTHETIC C4 " + mode, Goal: c4FixtureGoal, Spec: domain.Spec{Scope: receipt.Spec.Scope, Protected: receipt.Spec.Protected, Acceptance: receipt.Spec.Acceptance, Image: receipt.Spec.Image, ProfileID: profile.ID, MaxCalls: 16, C4: &domain.C4Profile{SchemaVersion: 1, ID: "c4_go_v1", Mode: "synthetic", ImageID: "sha256:" + strings.Repeat("a", 64), Platform: "linux/arm64", DockerPath: "/synthetic/docker", Endpoint: "unix:///synthetic/docker.sock", Verifier: "/opt/foundry/verify-go-v1"}}})
		if e != nil {
			return e
		}
		m, e = r.engine.PrepareC4Baseline(context.Background(), m.ID, m.Revision)
		if e != nil {
			return e
		}
		r.scenarios["c4_"+mode] = m.ID
		if mode == "cleanup_failure" {
			o := m.C4Operations[0]
			m, e = r.engine.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
			if e != nil {
				return e
			}
			m, e = r.engine.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
			if !errors.Is(e, sandbox.ErrC4Execution) || m.C4Operations[0].CleanupState() != "failed" {
				return errors.New("C4 failure fixture did not stop at failed cleanup")
			}
		}
	}
	return nil
}
