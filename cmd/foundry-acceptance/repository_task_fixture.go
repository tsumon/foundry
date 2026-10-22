package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/desktophost"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/httpapi"
	"foundry/internal/platformfs"
	"foundry/internal/repositorytask"
	"foundry/internal/sandbox"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// Only lifecycle bookkeeping is reused; this evaluator does not inspect Double
// files or call a real transport. Frozen assertions are loaded from managed base.
type repositoryTaskDriver struct {
	c4FixtureDriver
	store   *store.Store
	managed string
}

func (d *repositoryTaskDriver) Inspect(_ context.Context, c sandbox.C4Command) (sandbox.C4Inspection, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	item, err := d.item(c)
	if err != nil {
		return sandbox.C4Inspection{}, err
	}
	m, err := d.store.GetMission(item.labels["foundry.mission"])
	if err != nil {
		return sandbox.C4Inspection{}, err
	}
	if m.Spec.C4 == nil || m.Spec.C4.Mode != "synthetic" || m.Spec.C4.ControlVersion != 0 || m.RepositoryImport == nil {
		return sandbox.C4Inspection{}, errors.New("repository tasks are uncontrolled synthetic only")
	}
	var op *domain.C4Operation
	for i := range m.C4Operations {
		if m.C4Operations[i].ID == item.labels["foundry.operation"] {
			op = &m.C4Operations[i]
		}
	}
	if op == nil || op.Owner != item.labels["foundry.owner"] || op.ContainerName != item.name || op.MissionID != m.ID {
		return sandbox.C4Inspection{}, errors.New("unknown local operation")
	}
	dir := filepath.Join(d.managed, "missions", m.ID)
	base := filepath.Join(dir, "base")
	expected := base
	if op.Kind == "candidate" {
		if op.Epoch != m.Epoch || !reflect.DeepEqual(op.CandidateGeneration, m.CandidateGeneration) {
			return sandbox.C4Inspection{}, errors.New("stale candidate operation")
		}
		expected, err = workspace.GenerationPath(dir, *m.CandidateGeneration)
		if err != nil {
			return sandbox.C4Inspection{}, err
		}
	}
	expected, err = filepath.EvalSymlinks(expected)
	if err != nil || expected != item.path {
		return sandbox.C4Inspection{}, errors.New("unexpected synthetic mount")
	}
	digest, err := workspace.Digest(base)
	if err != nil || digest != m.RepositoryImport.BaseDigest {
		return sandbox.C4Inspection{}, errors.New("frozen base drift")
	}
	frozen, err := readRepositoryTaskTree(base)
	if err != nil {
		return sandbox.C4Inspection{}, err
	}
	definition, err := repositorytask.Parse([]byte(frozen[repositorytask.Descriptor]))
	if err != nil {
		return sandbox.C4Inspection{}, err
	}
	candidate, err := readRepositoryTaskTree(item.path)
	if err != nil {
		return sandbox.C4Inspection{}, err
	}
	result := repositorytask.Evaluate(definition, frozen, candidate, op.Kind)
	return sandbox.C4Inspection{ID: item.id, Name: item.name, Labels: item.labels, ExitCode: result.ExitCode, Verification: &result}, nil
}

func (d *repositoryTaskDriver) Logs(_ context.Context, _ sandbox.C4Command, w io.Writer) (bool, error) {
	_, err := io.WriteString(w, "SYNTHETIC repository-task exact-content checks (synthetic_stub). No Go compiler, tests, Docker, provider or account used.")
	return false, err
}
func (d *repositoryTaskDriver) Remove(_ context.Context, c sandbox.C4Command) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	item, err := d.item(c)
	if err != nil {
		return err
	}
	item.removed = true
	return nil
}

func readRepositoryTaskTree(path string) (map[string]string, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := map[string]string{}
	total := 0
	err = workspace.WalkBoundedTree(root.FS(), func(p string, e fs.DirEntry) error {
		if e.IsDir() {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 512*1024 || len(files) >= 500 {
			return errors.New("unsupported repository file")
		}
		f, err := root.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return errors.New("repository file changed")
		}
		raw, err := io.ReadAll(io.LimitReader(f, 512*1024+1))
		if err != nil {
			return err
		}
		total += len(raw)
		if len(raw) > 512*1024 || total > workspace.MaxSnapshotBytes {
			return errors.New("repository read exceeds bound")
		}
		files[p] = string(raw)
		return nil
	})
	return files, err
}

func readRepositoryDescriptor(path string) ([]byte, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(repositorytask.Descriptor)
	if err != nil || !info.Mode().IsRegular() || info.Size() > repositorytask.MaxDescriptorBytes {
		return nil, errors.New("bounded regular foundry.task.json required")
	}
	f, err := root.Open(repositorytask.Descriptor)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("descriptor changed during read")
	}
	raw, err := io.ReadAll(io.LimitReader(f, repositorytask.MaxDescriptorBytes+1))
	if err != nil || len(raw) > repositorytask.MaxDescriptorBytes {
		return nil, errors.New("descriptor exceeds bound")
	}
	return raw, nil
}

func validateRepositoryTaskFlags(controls, repository bool, path string) error {
	if controls && repository || path != "" && !repository {
		return errors.New("--repository-task-path requires --repository-tasks-fixture, which cannot be combined with --c4-controls-fixture")
	}
	return nil
}

func startRepositoryTasksAcceptance(webDir, source string) (_ *acceptanceRuntime, err error) {
	return startRepositoryTasksDesktop(webDir, source, "", "")
}

func startRepositoryTasksDesktop(webDir, source, retainedParent, capability string) (_ *acceptanceRuntime, err error) {
	webDir, err = filepath.Abs(webDir)
	if err != nil {
		return nil, err
	}
	if info, e := os.Stat(filepath.Join(webDir, "index.html")); e != nil || !info.Mode().IsRegular() {
		return nil, errors.New("build the frontend first; --web-dir must contain index.html")
	}
	root, err := platformfs.PrivateTemp(retainedParent, "foundry-repository-tasks-synthetic-")
	if err != nil {
		return nil, err
	}
	r := &acceptanceRuntime{Root: root, serveErr: make(chan error, 1), scenarios: map[string]string{}, repositoryTasks: true}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	r.store, err = store.Open(filepath.Join(root, "foundry.db"))
	if err != nil {
		return nil, err
	}
	profile := domain.Profile{ID: "repository-task-local", Name: "SYNTHETIC descriptor proposals", Kind: "synthetic", Protocol: "chat_completions", BaseURL: "https://synthetic.invalid", Model: "DESCRIPTOR-NOT-A-MODEL"}
	if err = r.store.PutProfile(profile); err != nil {
		return nil, err
	}
	if err = r.store.PutSettings(allRoleBindings(profile.ID, nil)); err != nil {
		return nil, err
	}
	managed := filepath.Join(root, "managed")
	if err = os.MkdirAll(managed, 0700); err != nil {
		return nil, err
	}
	driver := &repositoryTaskDriver{store: r.store, managed: managed}
	r.engine = engine.New(r.store, nil, engine.Options{DataDir: managed, Runner: syntheticRunner{}, C4: engine.NewC4SyntheticCapability(sandbox.NewC4SyntheticDriver(driver), repositorytask.New(r.store), "/synthetic/docker", "unix:///synthetic/docker.sock")})
	if err = r.engine.Recover(context.Background()); err != nil {
		return nil, err
	}
	sources := map[string]string{}
	if source != "" {
		source, err = filepath.Abs(source)
		if err != nil {
			return nil, err
		}
		sources["imported"] = source
	} else {
		for _, name := range []string{"labels", "pricing"} {
			repo := filepath.Join(root, "repository-"+name)
			if err = seedRepositoryTask(repo, name); err != nil {
				return nil, err
			}
			sources[name] = repo
		}
	}
	for name, repo := range sources {
		raw, e := readRepositoryDescriptor(repo)
		if e != nil {
			return nil, e
		}
		definition, e := repositorytask.Parse(raw)
		if e != nil {
			return nil, e
		}
		spec := definition.ImportSpec()
		receipt, e := r.engine.InspectRepository(context.Background(), domain.RepositoryInspectionRequest{RepoPath: repo, Consent: true, Spec: spec})
		if e != nil {
			return nil, e
		}
		if receipt.Status != "ready" {
			return nil, fmt.Errorf("repository task import blocked: %s", receipt.Code)
		}
		// Verify the bytes parsed from the working tree are the exact committed
		// descriptor in the receipt, before any mission is created.
		found := false
		for _, f := range receipt.ContextManifest {
			if f.Path == repositorytask.Descriptor {
				found = f.Bytes == int64(len(raw)) && f.SHA256 == fmt.Sprintf("%x", sha256.Sum256(raw))
			}
		}
		if !found {
			return nil, errors.New("descriptor differs from inspected committed context")
		}
		p := &domain.C4Profile{SchemaVersion: 1, ID: "c4_go_v1", Mode: "synthetic", ImageID: "sha256:" + strings.Repeat("a", 64), Platform: "linux/arm64", DockerPath: "/synthetic/docker", Endpoint: "unix:///synthetic/docker.sock", Verifier: "/opt/foundry/verify-go-v1"}
		m, e := r.engine.Create(context.Background(), engine.CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: receipt.ID, Digest: receipt.Digest}, CodingVersion: 1, WorkspaceMode: domain.WorkspaceModeGenerationsV1, RepoPath: receipt.Root, Title: "SYNTHETIC repository task: " + definition.Goal, Goal: definition.Goal, Spec: domain.Spec{Scope: spec.Scope, Protected: spec.Protected, Acceptance: spec.Acceptance, Image: spec.Image, ProfileID: profile.ID, MaxCalls: 16, C4: p}})
		if e != nil {
			return nil, e
		}
		frozen, e := readRepositoryTaskTree(filepath.Join(managed, "missions", m.ID, "base"))
		if e != nil || frozen[repositorytask.Descriptor] != string(raw) {
			return nil, errors.New("imported descriptor mismatch")
		}
		if repositorytask.Evaluate(definition, frozen, frozen, "baseline").Class != "passed" {
			return nil, errors.New("unsupported frozen task")
		}
		m, e = r.engine.PrepareC4Baseline(context.Background(), m.ID, m.Revision)
		if e != nil {
			return nil, e
		}
		r.scenarios["repository_"+name] = m.ID
		if r.Repo == "" {
			r.Repo = receipt.Root
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
	r.server = &http.Server{Handler: desktophost.Protect(capability, httpapi.New(r.store, r.engine, nil, nil, httpapi.Options{Address: address, WebDir: webDir, C4ControlledOnly: true})), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, BaseContext: func(net.Listener) context.Context { return serverCtx }}
	go func() { r.serveErr <- r.server.Serve(listener) }()
	return r, nil
}

func seedRepositoryTask(repo, name string) error {
	// These files belong only to the newly allocated acceptance root. Existing
	// --repository-task-path sources never enter this seed/write function.
	text := func(s string) *string { return &s }
	d := repositorytask.Definition{SchemaVersion: 1, Goal: "Normalize labels", Summary: "Scripted nested-path formatting proposal", Scope: []string{"internal/label"}, Protected: []string{repositorytask.Descriptor, "go.mod", "go.sum", "internal/label/label_test.go"}}
	baseline := "package label\n\nfunc Normalize(s string) string { return s }\n"
	broken := "package label\n\nfunc Normalize(s string) string { return \"wrong\" }\n"
	fixed := "package label\n\nimport \"strings\"\n\nfunc Normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }\n"
	files := map[string]string{"internal/label/label.go": baseline, "internal/label/label_test.go": "package label\n\nimport \"testing\"\n\nfunc TestNormalize(t *testing.T) { if Normalize(\"label\") != \"label\" { t.Fatal(\"label\") } }\n"}
	d.Steps = []repositorytask.Step{{ID: "normalize", Title: "Normalize labels", Description: "Change the nested label implementation only", DependsOn: []string{}, Edits: []repositorytask.Edit{{Path: "internal/label/label.go", Content: text(broken)}}}}
	d.Repairs = [][]repositorytask.Edit{{{Path: "internal/label/label.go", Content: text(fixed)}}}
	d.Checks = []repositorytask.Edit{{Path: "internal/label/label.go", Content: text(fixed)}}
	if name == "pricing" {
		d.Goal = "Format prices in cents"
		d.Summary = "Scripted dependent multifile pricing proposal"
		d.Scope = []string{"internal/pricing"}
		d.Protected = []string{repositorytask.Descriptor, "go.mod", "go.sum", "internal/pricing/price_test.go"}
		price := "package pricing\n\nfunc Cents(units int) int { return units * 100 }\n"
		format := "package pricing\n\nimport \"fmt\"\n\nfunc Label(units int) string { return fmt.Sprintf(\"%d cents\", Cents(units)) }\n"
		files = map[string]string{"internal/pricing/price.go": "package pricing\n\nfunc Cents(units int) int { return units }\n", "internal/pricing/price_test.go": "package pricing\n\nimport \"testing\"\n\nfunc TestZero(t *testing.T) { if Cents(0) != 0 { t.Fatal(\"zero\") } }\n"}
		d.Steps = []repositorytask.Step{{ID: "cents", Title: "Convert units", Description: "Represent units as cents", DependsOn: []string{}, Edits: []repositorytask.Edit{{Path: "internal/pricing/price.go", Content: text("package pricing\n\nfunc Cents(units int) int { return units * 10 }\n")}}}, {ID: "format", Title: "Format label", Description: "Use the cents representation", DependsOn: []string{"cents"}, Edits: []repositorytask.Edit{{Path: "internal/pricing/format.go", Content: text(format)}}}}
		d.Repairs = [][]repositorytask.Edit{{{Path: "internal/pricing/price.go", Content: text(price)}}}
		d.Checks = []repositorytask.Edit{{Path: "internal/pricing/price.go", Content: text(price)}, {Path: "internal/pricing/format.go", Content: text(format)}}
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	files[repositorytask.Descriptor] = string(raw) + "\n"
	files["go.mod"] = "module synthetic-repository-task\n\ngo 1.27.3\n"
	files["go.sum"] = ""
	for p, c := range files {
		path := filepath.Join(repo, p)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(c), 0600); err != nil {
			return err
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Synthetic Acceptance", "-c", "user.email=synthetic@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "SYNTHETIC repository task frozen base"}} {
		cmd, cleanup, err := fixtureGitCommand(args...)
		if err != nil {
			return err
		}
		defer cleanup()
		cmd.Dir = repo
		if raw, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("seed repository task: %w %s", err, raw)
		}
	}
	return nil
}
