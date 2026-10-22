package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"foundry/internal/c4fixture"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/gitinspect"
	"foundry/internal/httpapi"
	"foundry/internal/sandbox"
	"foundry/internal/store"
	"foundry/internal/workspace"
)

// This wrapper injects one owned fixture failure without changing legacy fixture
// files, source bytes, or transport behavior. The mission is selected at seeding.
type c4ControlsDriver struct {
	c4FixtureDriver
	failureMission string
	failedRemoval  bool
	readiness      atomic.Int64
}

func (d *c4ControlsDriver) Inspect(ctx context.Context, c sandbox.C4Command) (sandbox.C4Inspection, error) {
	v, err := d.c4FixtureDriver.Inspect(ctx, c)
	if err == nil && v.Labels["foundry.mission"] == d.failureMission {
		v.ExitCode = 2
		v.Verification.Class = "infrastructure_failed"
		v.Verification.ExitCode = 2
		v.Verification.Tests = 0
	}
	return v, err
}
func (d *c4ControlsDriver) Remove(ctx context.Context, c sandbox.C4Command) error {
	d.mu.Lock()
	item, err := d.item(c)
	fail := err == nil && item.labels["foundry.mission"] == d.failureMission && !d.failedRemoval
	if fail {
		d.failedRemoval = true
	}
	d.mu.Unlock()
	if fail {
		return errors.New("SYNTHETIC controlled cleanup failure")
	}
	return d.c4FixtureDriver.Remove(ctx, c)
}

// The explicit fixture mode never accepts production configuration and never
// constructs account services, router, legacy capability or real transport.
func startC4ControlsAcceptance(webDir string) (_ *acceptanceRuntime, err error) {
	webDir, err = filepath.Abs(webDir)
	if err != nil {
		return nil, err
	}
	if info, e := os.Stat(filepath.Join(webDir, "index.html")); e != nil || !info.Mode().IsRegular() {
		return nil, errors.New("build the frontend first; --web-dir must contain index.html")
	}
	root, err := os.MkdirTemp("", "foundry-c4-controls-synthetic-")
	if err != nil {
		return nil, err
	}
	r := &acceptanceRuntime{Root: root, Repo: filepath.Join(root, "synthetic-c4-source"), serveErr: make(chan error, 1), scenarios: map[string]string{}, c4Controls: &c4ControlsDriver{}}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if err = seedC4Repository(r.Repo, "controlled_shared_base"); err != nil {
		return nil, err
	}
	r.store, err = store.Open(filepath.Join(root, "foundry.db"))
	if err != nil {
		return nil, err
	}
	profile := domain.Profile{ID: "synthetic-c4", Name: "SYNTHETIC C4 accountless model", Kind: "synthetic", Protocol: "chat_completions", BaseURL: "https://synthetic.invalid", Model: "C4-LOCAL-FIXTURE-NOT-CLOUD"}
	if err = r.store.PutProfile(profile); err != nil {
		return nil, err
	}
	if err = r.store.PutSettings(allRoleBindings(profile.ID, nil)); err != nil {
		return nil, err
	}
	ctx := context.Background()
	observation, err := gitinspect.Inspect(ctx, r.Repo)
	if err != nil {
		return nil, err
	}
	r.Repo = observation.Root
	spec := domain.RepositoryImportSpec{Scope: []string{"main.go"}, Protected: []string{"main_test.go", "go.mod", "go.sum", "fixture.json"}, Acceptance: []string{"/opt/foundry/verify-go-v1"}, Image: "synthetic:c4-fictional-no-image"}
	digest, _, _, err := workspace.PrepareRepositoryImport(ctx, r.Repo, observation.HEAD, filepath.Join(root, "admitted"), strings.Repeat("d", 32), spec)
	if err != nil {
		return nil, err
	}
	p := domain.C4Profile{SchemaVersion: 1, ControlVersion: 1, ID: "c4_go_v1", Mode: "synthetic", ImageID: "sha256:" + strings.Repeat("a", 64), Platform: "linux/arm64", DockerPath: "/synthetic/docker", Endpoint: "unix:///synthetic/docker.sock", Verifier: "/opt/foundry/verify-go-v1"}
	config := engine.C4Activation{SchemaVersion: 1, Enabled: true, Profile: p, SourceRoot: r.Repo, BaseCommit: observation.HEAD, BaseDigest: digest, ExpiresAt: time.Now().Add(4 * time.Hour).UTC().Format(time.RFC3339Nano)}
	capability, err := engine.NewC4ControlledSyntheticCapability(config, sandbox.NewC4SyntheticDriver(r.c4Controls), c4fixture.New(r.store), func(context.Context) (sandbox.C4ReadinessResult, error) {
		r.c4Controls.readiness.Add(1)
		return sandbox.C4ReadinessResult{Fingerprint: strings.Repeat("c", 64)}, nil
	})
	if err != nil {
		return nil, err
	}
	managed := filepath.Join(root, "managed")
	if err = os.MkdirAll(managed, 0700); err != nil {
		return nil, err
	}
	r.engine = engine.New(r.store, nil, engine.Options{DataDir: managed, C4Controlled: capability})
	for _, mode := range []string{"baseline_pending", "cleanup_failure"} {
		receipt, e := r.engine.InspectRepository(ctx, domain.RepositoryInspectionRequest{RepoPath: r.Repo, Consent: true, Spec: spec})
		if e != nil {
			return nil, e
		}
		m, e := r.engine.Create(ctx, engine.CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: receipt.ID, Digest: receipt.Digest}, CodingVersion: 1, WorkspaceMode: domain.WorkspaceModeGenerationsV1, RepoPath: r.Repo, Title: "SYNTHETIC C4 controls " + mode, Goal: c4fixture.Goal, Spec: domain.Spec{Scope: spec.Scope, Protected: spec.Protected, Acceptance: spec.Acceptance, Image: spec.Image, ProfileID: profile.ID, MaxCalls: 16, C4: &p}})
		if e != nil {
			return nil, e
		}
		r.scenarios["c4_controls_"+mode] = m.ID
		if mode != "cleanup_failure" {
			continue
		}
		r.c4Controls.failureMission = m.ID
		m, e = r.engine.PrepareC4Readiness(ctx, m.ID, m.Revision)
		if e != nil {
			return nil, e
		}
		control := m.C4Controls[0]
		m, e = r.engine.ApproveC4Control(ctx, m.ID, m.Revision, control.ID, control.MaterialDigest)
		if e != nil {
			return nil, e
		}
		m, e = r.engine.ExecuteC4Readiness(ctx, m.ID, m.Revision, control.ID, control.MaterialDigest)
		if e != nil {
			return nil, e
		}
		m, e = r.engine.PrepareC4Baseline(ctx, m.ID, m.Revision)
		if e != nil {
			return nil, e
		}
		op := m.C4Operations[0]
		m, e = r.engine.ApproveC4Baseline(ctx, m.ID, m.Revision, op.ID, op.MaterialDigest)
		if e != nil {
			return nil, e
		}
		m, e = r.engine.ExecuteC4Baseline(ctx, m.ID, m.Revision, op.ID, op.MaterialDigest)
		if !errors.Is(e, sandbox.ErrC4Execution) || m.C4Operations[0].CleanupState() != "failed" {
			return nil, errors.New("controlled synthetic failure did not stop")
		}
	}
	listener, err := listenAcceptance()
	if err != nil {
		return nil, err
	}
	address := listener.Addr().String()
	r.URL = "http://" + address
	serverCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.server = &http.Server{Handler: httpapi.New(r.store, r.engine, nil, nil, httpapi.Options{Address: address, WebDir: webDir, C4ControlledOnly: true}), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, BaseContext: func(net.Listener) context.Context { return serverCtx }}
	go func() { r.serveErr <- r.server.Serve(listener) }()
	return r, nil
}
