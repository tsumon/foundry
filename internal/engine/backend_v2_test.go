package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"foundry/internal/c4verify"
	"foundry/internal/dependencyadmission"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"foundry/internal/vmruntime"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func inertV2Runtime(t *testing.T) (*vmruntime.Runtime, string) {
	v, i, _ := inertV2RuntimeBundle(t)
	return v, i
}
func inertV2RuntimeBundle(t *testing.T) (*vmruntime.Runtime, string, string) {
	t.Helper()
	_, instances, _, root := newInertVMRuntimeBundle(t)
	raw, _ := os.ReadFile(filepath.Join(root, "manifest.json"))
	var old vmruntime.Manifest
	json.Unmarshal(raw, &old)
	os.Remove(filepath.Join(root, old.Verifier))
	raw, err := os.ReadFile("../vmruntime/testdata/runtime-inventory-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := vmruntime.ParseRuntimeInventoryV2(raw)
	if err != nil {
		t.Fatal(err)
	}
	supervisor := []byte("synthetic v2 supervisor, never executable")
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	d.Supervisor.Size = int64(len(supervisor))
	d.Supervisor.SHA256 = sum(supervisor)
	raw, err = d.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	m := vmruntime.ManifestV2{SchemaVersion: 2, HostPlatform: old.HostPlatform, GuestPlatform: old.GuestPlatform, Protocol: "foundry-vm-v2", PolicyVersion: "multilang-vm-v2", Runtime: old.Runtime, Kernel: old.Kernel, RootFS: old.RootFS, Verifier: "foundry-vm-guest-v2", RuntimeInventory: vmruntime.RuntimeInventoryPathV2}
	m.RuntimeInventoryDigest, _ = d.Digest()
	for _, a := range old.Assets {
		if a.Path != old.Verifier {
			m.Assets = append(m.Assets, a)
		}
	}
	for p, b := range map[string][]byte{m.Verifier: supervisor, m.RuntimeInventory: raw} {
		os.WriteFile(filepath.Join(root, p), b, 0600)
		m.Assets = append(m.Assets, vmruntime.Asset{Path: p, SHA256: sum(b), Size: int64(len(b))})
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, m.Runtime), binary, 0700)
	os.Chmod(filepath.Join(root, m.Runtime), 0700)
	for i := range m.Assets {
		if m.Assets[i].Path == m.Runtime {
			m.Assets[i].SHA256 = sum(binary)
			m.Assets[i].Size = int64(len(binary))
		}
	}
	sort.Slice(m.Assets, func(i, j int) bool { return m.Assets[i].Path < m.Assets[j].Path })
	raw, _ = json.Marshal(m)
	os.WriteFile(filepath.Join(root, "manifest.json"), raw, 0600)
	vm, err := vmruntime.LoadV2(root, instances)
	if err != nil {
		t.Fatal(err)
	}
	return vm, instances, root
}
func codingV2Import(t *testing.T) (*Engine, CreateRequest, *vmruntime.Runtime, string) {
	return codingV2ImportProfile(t, "c4_node_model_v2")
}
func codingV2ImportProfile(t *testing.T, profileID string, persistent ...*store.Store) (*Engine, CreateRequest, *vmruntime.Runtime, string) {
	t.Helper()
	var saved *store.Store
	if len(persistent) > 0 {
		saved = persistent[0]
	}
	e, _, q, _, _ := codingImportWithStore(t, "http://127.0.0.1:1/v1", saved)
	entries, _ := os.ReadDir(q.RepoPath)
	for _, entry := range entries {
		if entry.Name() != ".git" {
			os.RemoveAll(filepath.Join(q.RepoPath, entry.Name()))
		}
	}
	files := map[string]string{"src/main.js": "export const label = 'old';\n", "tests/main.test.js": "// synthetic frozen test\n", "tests/helper.txt": "synthetic helper\n", "package.json": `{"name":"app","version":"1.0.0"}`, "package-lock.json": `{"lockfileVersion":3,"packages":{"":{"name":"app","version":"1.0.0"}}}`}
	verifier := "/opt/foundry/verify-node-v2"
	if profileID == "c4_python_model_v2" {
		files = map[string]string{"src/main.py": "label='old'\n", "tests/test_main.py": "# synthetic frozen test\n", "tests/helper.txt": "synthetic helper\n", "foundry.python.lock.json": `{"schema_version":1,"python":"3.13.5","selected_extras":[],"roots":[],"wheels":[]}`}
		verifier = "/opt/foundry/verify-python-v2"
	}
	if profileID == "c4_typescript_model_v2" {
		files["src/main.ts"] = files["src/main.js"]
		delete(files, "src/main.js")
		files["tests/main.test.ts"] = files["tests/main.test.js"]
		delete(files, "tests/main.test.js")
		files["tsconfig.json"] = `{"compilerOptions":{"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext","strict":true,"noEmitOnError":true,"allowJs":false,"skipLibCheck":false},"files":["src/main.ts","tests/main.test.ts"]}`
		verifier = "/opt/foundry/verify-typescript-v2"
	}
	for p, b := range files {
		name := filepath.Join(q.RepoPath, p)
		os.MkdirAll(filepath.Dir(name), 0755)
		os.WriteFile(name, []byte(b), 0644)
	}
	importGit(t, q.RepoPath, "add", "-A")
	importGit(t, q.RepoPath, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "synthetic v2 repository")
	vm, instances := inertV2Runtime(t)
	cap, err := NewCodingVMCapabilityWithV2(e.gateway.(*model.Router), nil, vm)
	if err != nil {
		t.Fatal(err)
	}
	e.options.C4Controlled = cap
	target, _ := vm.ProbeTargetV2(profileID)
	input, _ := filepath.EvalSymlinks(t.TempDir())
	result, err := e.ImportCodingDependencies(context.Background(), CodingDependencyImportRequest{RepoPath: q.RepoPath, Consent: true, ProfileID: target.ProfileID, EmbeddedVM: target.Identity, RuntimeInventoryDigest: target.RuntimeInventoryDigest, InputDirectory: input})
	if err != nil {
		t.Fatal(err)
	}
	spec := domain.RepositoryImportSpec{Scope: []string{"src/"}, Protected: []string{}, Acceptance: []string{verifier}, VerificationV2: &domain.C4ImportSelectionV2{SchemaVersion: 2, ProfileID: target.ProfileID, EmbeddedVM: target.Identity, RuntimeInventoryDigest: target.RuntimeInventoryDigest, AdmissionDigest: result.AdmissionDigest, Dependencies: result.Dependencies, SourceRoot: ".", TestRoot: "tests", TestSupportPrefixes: []string{}}}
	v, err := e.InspectRepository(context.Background(), domain.RepositoryInspectionRequest{RepoPath: q.RepoPath, Consent: true, Spec: spec})
	if err != nil || v.Status != "ready" {
		t.Fatalf("inspection %+v %v", v, err)
	}
	q.ImportSelection = &domain.ImportSelection{InspectionID: v.ID, Digest: v.Digest}
	q.Spec.Scope = spec.Scope
	q.Spec.Protected = []string{}
	q.Spec.Acceptance = spec.Acceptance
	q.Spec.Image = ""
	q.Spec.C4 = &domain.C4Profile{SchemaVersion: 2, ID: target.ProfileID, Mode: "embedded_vm"}
	return e, q, vm, instances
}
func TestBackendV2AdmissionCreationAndImmutableRelations(t *testing.T) {
	e, q, _, instances := codingV2Import(t)
	m, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if m.Spec.C4.Validate() != nil || m.Spec.C4.SchemaVersion != 2 || len(m.Spec.Protected) < 6 {
		t.Fatal("unbound task", m.Spec)
	}
	again, err := e.Create(context.Background(), q)
	if err != nil || again.ID != m.ID {
		t.Fatal("non-idempotent", err)
	}
	for _, change := range []func(*domain.Mission){func(m *domain.Mission) { m.Spec.C4 = nil }, func(m *domain.Mission) { m.Spec.C4.V2.DependencyBundleDigest = strings.Repeat("f", 64) }, func(m *domain.Mission) { m.Spec.Protected = []string{} }, func(m *domain.Mission) { m.ContextManifest = nil }} {
		_, err = e.s.UpdateMission(m.ID, m.Revision, "synthetic.tamper", func(x *domain.Mission) error { change(x); return nil })
		if err == nil {
			t.Fatal("accepted mission substitution")
		}
	}
	entries, _ := os.ReadDir(instances)
	if len(entries) != 0 {
		t.Fatal("metadata booted runtime")
	}
	q.Title = "different intent"
	if _, err = e.Create(context.Background(), q); err != domain.ImportIntentConflict {
		t.Fatal("intent reset", err)
	}
}
func TestBackendV2DependencySummaryAndHistoricalCatalog(t *testing.T) {
	e, q, _, _ := codingV2Import(t)
	m, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := e.v2ContextSummary(m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"verification_v2": summary})
	if err = e.validateV2ContextSummary(m, string(raw)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), e.options.DataDir) || strings.Contains(string(raw), "source_files") || summary.SelectedTestFileCount != 1 || summary.Dependencies.PackageCount != 0 {
		t.Fatal(string(raw))
	}
	if e.validateV2ContextSummary(m, `{"verification_v2":{}}`) == nil {
		t.Fatal("unapproved summary accepted")
	}
	e.options.C4Controlled.currentV2 = nil
	if err = e.s.ValidateMissionRepositoryImport(m); err != nil {
		t.Fatal("historical record depended on catalog", err)
	}
	if e.options.C4Controlled.vmForProfile(*m.Spec.C4) == nil {
		t.Fatal("lost exact retained runtime")
	}
	if _, err = e.Create(context.Background(), q); err == nil {
		t.Fatal("missing current catalog admitted creation")
	}
	root, _ := e.dependencyStoreRoot()
	os.RemoveAll(filepath.Join(root, m.Spec.C4.V2.DependencyBundleDigest))
	if _, _, _, err = e.resolveV2Material(context.Background(), m); err == nil {
		t.Fatal("missing payload used")
	}
}

// A repository-owned inert native child: no VM, framework, compiler or project
// code executes. The synthetic evidence exercises real host transfer/EOF/exit.
func init() {
	if filepath.Base(os.Args[0]) != "foundry-vm-helper" {
		return
	}
	if len(os.Args) != 5 || os.Args[1] != "--kernel" || os.Args[3] != "--initrd" {
		os.Exit(90)
	}
	if _, err := io.WriteString(os.Stdout, vmruntime.ReadyMarkerV2); err != nil {
		os.Exit(91)
	}
	broken, infra := false, false
	q, err := vmruntime.ReadRequestV2(context.Background(), os.Stdin, func(kind uint8, index uint32, file vmruntime.SourceFileV2, r io.Reader) error {
		b, err := io.ReadAll(r)
		if kind == 1 {
			broken = broken || strings.Contains(string(b), "broken")
			infra = infra || strings.Contains(string(b), "synthetic_compile_error")
		}
		return err
	})
	if err != nil {
		os.Exit(92)
	}
	result := c4verify.Result{Class: "passed", Tests: 1, Output: "synthetic inert native fixture; no project test executed"}
	phase := "test-node"
	if q.Target.ProfileID == "c4_python_model_v2" {
		phase = "test-python"
	}
	if q.Action == "probe" {
		result = c4verify.Result{Class: "probe_passed", Output: "synthetic inert readiness"}
		phase = "probe-node"
		if q.Target.ProfileID == "c4_python_model_v2" {
			phase = "probe-python"
		}
		if q.Target.ProfileID == "c4_typescript_model_v2" {
			phase = "probe-typescript"
		}
	}
	if broken {
		result.Class = "test_failed"
		result.ExitCode = 1
	}
	if infra {
		result.Class = "infrastructure_failed"
		result.Tests = 0
		result.ExitCode = 1
	}
	checks := vmruntime.SelfCheckV2{ReadOnlyWorkspace: true, ReadOnlyToolchain: true, NoNetwork: true, PrivateControl: true, Unprivileged: true, NoNewPrivileges: true, CgroupLimits: true, TmpfsLimits: true, ReadOnlyDependencies: true, RuntimeInventoryMatches: true, InputBindingValid: true, PhaseCleanupConfirmed: true}
	phases := []vmruntime.PhaseEvidenceV2{{Phase: phase, Exited: true, ExitCode: result.ExitCode, ResourceLimitsOK: true, CleanupConfirmed: true}}
	if q.Action != "probe" && q.Target.ProfileID == "c4_typescript_model_v2" {
		phases = append([]vmruntime.PhaseEvidenceV2{{Phase: "compile-typescript", Exited: true, ResourceLimitsOK: true, CleanupConfirmed: true, CompiledOutputDigest: strings.Repeat("e", 64)}}, phases...)
	}
	if err = vmruntime.WriteResponseV2(os.Stdout, q, checks, result, phases); err != nil {
		os.Exit(93)
	}
	os.Exit(0)
}
func TestBackendV2SyntheticNativeBaseline(t *testing.T) {
	e, q, _, _ := codingV2Import(t)
	m, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	if len(m.C4Operations) != 1 || !m.C4Operations[0].Passed() || !strings.Contains(m.C4Operations[0].Result.Output, "synthetic inert") {
		t.Fatal("native fixture baseline evidence missing", m.C4Operations)
	}
}

type backendV2SyntheticModels struct {
	e        *Engine
	script   *codingReviewGateway
	requests []model.Request
}

func (g *backendV2SyntheticModels) PreflightModelSelection(ctx context.Context, schema int, selection domain.RoleModelSelection) error {
	return ctx.Err()
}
func (g *backendV2SyntheticModels) Complete(ctx context.Context, q model.Request) (model.Result, error) {
	if err := g.e.AdmitCodingModelCall(ctx, q); err != nil {
		return model.Result{}, err
	}
	g.requests = append(g.requests, q)
	r, err := g.script.Complete(ctx, q)
	r.RequestID = "synthetic-backend-v2"
	r.ResolvedModel = "synthetic-model"
	r.CredentialGeneration = 1
	r.UsageKnown = true
	r.InputTokens = 10
	r.OutputTokens = 5
	return r, err
}
func TestBackendV2CompleteSyntheticWorkflowAndRecovery(t *testing.T) {
	e, q, _, _ := codingV2Import(t)
	outputs := []string{`{"summary":"synthetic observations","findings":[],"unknowns":[]}`, `{"summary":"synthetic change","steps":[{"id":"label","title":"change label","description":"change implementation","depends_on":[],"files":["src/main.js"]}]}`, `{"summary":"synthetic failing candidate","edits":[{"path":"src/main.js","content":"export const label = 'broken';\n"}]}`, `{"summary":"synthetic repair","edits":[{"path":"src/main.js","content":"export const label = 'ready';\n"}]}`, codingPass, codingPass}
	gateway := &backendV2SyntheticModels{e: e, script: &codingReviewGateway{script: &scripted{outputs: outputs}}}
	e.gateway = gateway
	e.options.C4Controlled.models = gateway
	before := importSourceDigest(t, q.RepoPath)
	m, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err == nil {
		t.Fatal("baseline/readiness approval bypass")
	}
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("context consent bypass")
	}
	if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, err = e.s.GetMission(m.ID)
	if err != nil || m.State != "awaiting_approval" {
		t.Fatal(m.State, m.Error, err)
	}
	if _, err = e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, err = e.s.GetMission(m.ID)
	if err != nil || m.State != "awaiting_delivery" {
		t.Fatal(m.State, m.Error, err)
	}
	if len(m.C4Operations) != 3 || m.C4Operations[1].Result.Class != "test_failed" || !m.C4Operations[2].Passed() || m.C4Operations[2].CleanupState() != "confirmed" || m.Budget.UsedCalls != 6 || len(gateway.requests) != 6 || len(m.Profiles) != 7 {
		t.Fatalf("bad synthetic lifecycle %+v budget %+v calls %d", m.C4Operations, m.Budget, len(gateway.requests))
	}
	for _, req := range gateway.requests {
		if !strings.Contains(req.Prompt, `"verification_v2"`) || strings.Contains(req.Prompt, "dependency-bundles") || strings.Contains(req.Prompt, "input_directory") {
			t.Fatal("unsafe/missing summary")
		}
	}
	if _, err = e.Deliver(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m, _ = e.s.GetMission(m.ID)
	report, err := e.ReadDelivery(m.ID, "report")
	if err != nil || !strings.Contains(string(report), "synthetic") {
		t.Fatal("missing synthetic label", err)
	}
	if importSourceDigest(t, q.RepoPath) != before {
		t.Fatal("source modified")
	}
	e.options.C4Controlled.currentV2 = nil
	e.options.C4Controlled.config.RuntimeID = "synthetic-restart"
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	again, err := e.ReadDelivery(m.ID, "report")
	if err != nil || string(again) != string(report) {
		t.Fatal("retained recovery changed delivery", err)
	}
}
func TestBackendV2InfrastructureNeverRepairs(t *testing.T) {
	e, q, _, _ := codingV2Import(t)
	gateway := &backendV2SyntheticModels{e: e, script: &codingReviewGateway{script: &scripted{outputs: []string{`{"summary":"synthetic observations","findings":[],"unknowns":[]}`, `{"summary":"synthetic change","steps":[{"id":"label","title":"change","description":"change","depends_on":[],"files":["src/main.js"]}]}`, `{"summary":"synthetic infrastructure","edits":[{"path":"src/main.js","content":"// synthetic_compile_error\n"}]}`}}}}
	e.gateway = gateway
	e.options.C4Controlled.models = gateway
	m, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = e.s.GetMission(m.ID)
	if _, err = e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = e.s.GetMission(m.ID)
	if len(m.C4Operations) != 2 || m.C4Operations[1].Result == nil || m.C4Operations[1].Result.Class != "infrastructure_failed" || m.C4Operations[1].Result.Tests != 0 || m.C4Operations[1].CleanupState() != "confirmed" || len(gateway.requests) != 3 || m.State != "blocked" {
		t.Fatalf("infrastructure discarded or repaired: state %s operations %+v calls %d", m.State, m.C4Operations, len(gateway.requests))
	}
}
func TestBackendV2ReadinessDoesNotReadDependencyPayload(t *testing.T) {
	e, q, _, _ := codingV2Import(t)
	m, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := e.dependencyStoreRoot()
	os.RemoveAll(filepath.Join(root, m.Spec.C4.V2.DependencyBundleDigest))
	m = controlReady(t, e, m)
	m, err = e.PrepareC4Baseline(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	o := m.C4Operations[len(m.C4Operations)-1]
	m, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err == nil {
		t.Fatal("missing dependency payload used")
	}
}
func TestBackendV2PythonAndTypeScriptAdmissionBaseline(t *testing.T) {
	for _, id := range []string{"c4_python_model_v2", "c4_typescript_model_v2"} {
		t.Run(id, func(t *testing.T) {
			e, q, _, _ := codingV2ImportProfile(t, id)
			m, err := e.Create(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			m = controlReady(t, e, m)
			m = c4Baseline(t, e, m)
			if len(m.C4Operations) != 1 || !m.C4Operations[0].Passed() {
				t.Fatal("profile dispatch failed", m.C4Operations)
			}
		})
	}
}
func TestBackendV2DependencySummaryBoundAndOrdering(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	store := filepath.Join(root, "bundles")
	input := filepath.Join(root, "input")
	os.Mkdir(store, 0700)
	os.Mkdir(input, 0700)
	deps := map[string]string{}
	packages := map[string]any{}
	for i := 0; i < 300; i++ {
		name := fmt.Sprintf("pkg%03d-", i) + strings.Repeat("a", 120)
		deps[name] = "1.0.0"
		meta := map[string]any{"name": name, "version": "1.0.0"}
		packages["node_modules/"+name] = meta
		b, _ := json.Marshal(meta)
		os.Mkdir(filepath.Join(input, name), 0700)
		os.WriteFile(filepath.Join(input, name, "package.json"), b, 0644)
	}
	manifest := map[string]any{"name": "app", "version": "1.0.0", "dependencies": deps}
	packages[""] = manifest
	pkg, _ := json.Marshal(manifest)
	lock, _ := json.Marshal(map[string]any{"lockfileVersion": 3, "packages": packages})
	a, err := dependencyadmission.ImportNode(context.Background(), store, input, dependencyadmission.NodeSource{PackageJSON: dependencyadmission.SourceBytes{Path: "package.json", Data: pkg}, PackageLock: dependencyadmission.SourceBytes{Path: "package-lock.json", Data: lock}})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := dependencySummary(a)
	if err != nil {
		t.Fatal(err)
	}
	v := verificationContextV2{Language: "typescript", SourceRoots: []string{"src"}, SelectedTestFileCount: 500, ExecutionContractDigest: strings.Repeat("a", 64), Dependencies: summary}
	raw, _ := json.Marshal(v)
	if len(raw) > 64<<10 || summary.OmittedCount == 0 || len(summary.Packages)+summary.OmittedCount != 300 {
		t.Fatal("summary bounds", len(raw), summary.OmittedCount)
	}
	for i := 1; i < len(summary.Packages); i++ {
		if summary.Packages[i-1].Location >= summary.Packages[i].Location {
			t.Fatal("unstable summary order")
		}
	}
	again, _ := dependencySummary(a)
	raw2, _ := json.Marshal(verificationContextV2{Language: v.Language, SourceRoots: v.SourceRoots, SelectedTestFileCount: v.SelectedTestFileCount, ExecutionContractDigest: v.ExecutionContractDigest, Dependencies: again})
	if string(raw) != string(raw2) {
		t.Fatal("summary nondeterminism")
	}
}
func TestBackendV2RestartReopensImmutableRecordsAndExactRuntime(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	e, q, _, _ := codingV2ImportProfile(t, "c4_node_model_v2", s)
	m, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	vm, instances, source := inertV2RuntimeBundle(t)
	if vm.Identity() != *m.Spec.C4.EmbeddedVM {
		t.Fatal("fixture runtime is not byte-identical")
	}
	archiveParent, _ := filepath.EvalSymlinks(t.TempDir())
	os.Chmod(archiveParent, 0700)
	archive := filepath.Join(archiveParent, "archive")
	retained, err := vmruntime.Retain(source, archive, vm.Identity())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(source, source+"-uninstalled"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Rename(source+"-uninstalled", source) })
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	loaded, err := vmruntime.LoadRetained(archive, instances)
	if err != nil || len(loaded) != 1 {
		t.Fatal(err)
	}
	router := model.NewRouter(nil, nil, model.RouterOptions{})
	cap, err := NewCodingVMCapabilityWithV2(router, nil, nil, loaded...)
	if err != nil {
		t.Fatal(err)
	}
	options := e.options
	options.C4Controlled = cap
	restarted := New(s, router, options)
	if err = restarted.CheckC4ControlledRecovery(); err != nil {
		t.Fatal("exact historical inputs rejected", err)
	}
	if err = restarted.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetMission(m.ID)
	if err != nil || after.Spec.C4.ProfileDigest() != m.Spec.C4.ProfileDigest() || after.Budget != m.Budget {
		t.Fatal("restart migrated identity/budget", err)
	}
	if _, err = restarted.PrepareC4Readiness(context.Background(), m.ID, after.Revision); err != nil {
		t.Fatal("retained identity could not prepare fresh readiness", err)
	}
	os.WriteFile(filepath.Join(retained, "kernel"), []byte("tampered retained runtime"), 0600)
	if restarted.CheckC4ControlledRecovery() == nil {
		t.Fatal("tampered retained runtime passed recovery")
	}
}
func TestBackendV2RejectsIndividuallyValidForeignMaterial(t *testing.T) {
	e, q, _, _ := codingV2Import(t)
	m, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	original, err := e.s.GetRepositoryInspection(q.ImportSelection.InspectionID)
	if err != nil {
		t.Fatal(err)
	}
	foreignSpec := original.Spec
	selection := *foreignSpec.VerificationV2
	selection.SourceRoot = "src"
	foreignSpec.VerificationV2 = &selection
	other, err := e.InspectRepository(context.Background(), domain.RepositoryInspectionRequest{RepoPath: q.RepoPath, Consent: true, Spec: foreignSpec})
	if err != nil || other.Status != "ready" {
		t.Fatal("foreign receipt must be individually valid", err)
	}
	admission, err := e.s.GetDependencyAdmission(selection.AdmissionDigest)
	if err != nil {
		t.Fatal(err)
	}
	profile, _, err := domain.DeriveV2MissionMaterial(other, admission)
	if err != nil || profile.Validate() != nil {
		t.Fatal(err)
	}
	if _, err = e.s.UpdateMission(m.ID, m.Revision, "synthetic.foreign_profile", func(x *domain.Mission) error { x.Spec.C4 = &profile; return nil }); err == nil {
		t.Fatal("individually valid unrelated profile accepted")
	}
	if _, err = e.s.UpdateMission(m.ID, m.Revision, "synthetic.foreign_inspection", func(x *domain.Mission) error {
		b := *x.RepositoryImport
		b.InspectionID = other.ID
		b.InspectionDigest = other.Digest
		b.ImportSpecDigest = domain.RepositoryImportSpecDigest(other.Spec)
		x.RepositoryImport = &b
		return nil
	}); err == nil {
		t.Fatal("individually valid unrelated inspection accepted")
	}
	input, _ := filepath.EvalSymlinks(t.TempDir())
	root, _ := e.dependencyStoreRoot()
	pkg, _ := os.ReadFile(filepath.Join(q.RepoPath, "package.json"))
	lock, _ := os.ReadFile(filepath.Join(q.RepoPath, "package-lock.json"))
	lock = append(lock, ' ')
	foreignAdmission, err := dependencyadmission.ImportNode(context.Background(), root, input, dependencyadmission.NodeSource{PackageJSON: dependencyadmission.SourceBytes{Path: "package.json", Data: pkg}, PackageLock: dependencyadmission.SourceBytes{Path: "package-lock.json", Data: lock}})
	if err != nil || foreignAdmission.Validate() != nil {
		t.Fatal(err)
	}
	if err = e.s.SaveDependencyAdmission(foreignAdmission); err != nil {
		t.Fatal(err)
	}
	spec := original.Spec
	s := *spec.VerificationV2
	s.AdmissionDigest = foreignAdmission.Digest
	s.Dependencies = domain.C4DependencyBindingV2(foreignAdmission.Bundle)
	spec.VerificationV2 = &s
	blocked, err := e.InspectRepository(context.Background(), domain.RepositoryInspectionRequest{RepoPath: q.RepoPath, Consent: true, Spec: spec})
	if err != nil || blocked.Status != "blocked" || blocked.Code != domain.ImportDependencies || blocked.VerificationV2 != nil {
		t.Fatal("foreign admitted source metadata authorized inspection", blocked, err)
	}
}
