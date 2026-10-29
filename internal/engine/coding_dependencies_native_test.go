package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/dependencybundle"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/platformfs"
	"foundry/internal/store"
	"foundry/internal/vmruntime"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

type dependencyMetadataGateway struct{ calls atomic.Int32 }

func (g *dependencyMetadataGateway) Complete(context.Context, model.Request) (model.Result, error) {
	g.calls.Add(1)
	return model.Result{}, errors.New("metadata fixture forbids model calls")
}
func (g *dependencyMetadataGateway) ResolveModelCredential(context.Context, domain.RoleModelSelection) (model.CredentialLease, error) {
	g.calls.Add(1)
	return model.CredentialLease{}, errors.New("metadata fixture forbids credentials")
}

func dependencyMetadataHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Only valid, inert metadata: never copy or launch a helper/test executable.
// The older Apple-only execution fixtures retain their original host scope.
func dependencyMetadataRuntime(t *testing.T) (*vmruntime.Runtime, string) {
	t.Helper()
	host := runtime.GOOS + "/" + runtime.GOARCH
	if host != "darwin/arm64" && host != "windows/amd64" {
		t.Skip("metadata integration requires a supported installed-runtime host")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	instances, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	guest, helper := "linux/arm64", "runtime/foundry-vm-helper"
	if runtime.GOOS == "windows" {
		guest, helper = "linux/amd64", "runtime/qemu-system-x86_64.exe"
	}
	m := vmruntime.ManifestV2{SchemaVersion: 2, HostPlatform: host, GuestPlatform: guest, Protocol: "foundry-vm-v2", PolicyVersion: "multilang-vm-v2", Runtime: helper, Kernel: "kernel", RootFS: "initramfs.cpio.gz", Verifier: "foundry-vm-guest-v2", RuntimeInventory: vmruntime.RuntimeInventoryPathV2}
	raw, err := os.ReadFile("../vmruntime/testdata/runtime-inventory-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := vmruntime.ParseRuntimeInventoryV2(raw)
	if err != nil {
		t.Fatal(err)
	}
	stub := []byte("owned inert metadata, not an executable\n")
	d.GuestPlatform, d.Supervisor.Size, d.Supervisor.SHA256 = guest, int64(len(stub)), dependencyMetadataHash(stub)
	raw, err = d.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	m.RuntimeInventoryDigest, err = d.Digest()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{m.Runtime, m.Kernel, m.RootFS, m.Verifier, m.RuntimeInventory} {
		body := stub
		if p == m.RuntimeInventory {
			body = raw
		}
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, body, 0600); err != nil {
			t.Fatal(err)
		}
		m.Assets = append(m.Assets, vmruntime.Asset{Path: p, SHA256: dependencyMetadataHash(body), Size: int64(len(body))})
	}
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	v, err := vmruntime.LoadV2(root, instances)
	if err != nil {
		t.Fatal(err)
	}
	return v, instances
}

func dependencyMetadataWheel(t *testing.T) ([]byte, map[string][]byte) {
	t.Helper()
	prefix := "fixture_dep-1.0.dist-info/"
	files := map[string][]byte{
		"fixture_dep.py":            []byte("value = 1\n"),
		prefix + "METADATA":         []byte("Metadata-Version: 2.4\nName: fixture-dep\nVersion: 1.0\n\nOwned fixture\n"),
		prefix + "WHEEL":            []byte("Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n"),
		prefix + "licenses/LICENSE": []byte("Owned fixture license\n"),
	}
	names := []string{}
	for p := range files {
		names = append(names, p)
	}
	sort.Strings(names)
	rows := [][]string{}
	for _, p := range names {
		h := sha256.Sum256(files[p])
		rows = append(rows, []string{p, "sha256=" + base64.RawURLEncoding.EncodeToString(h[:]), fmt.Sprint(len(files[p]))})
	}
	record := prefix + "RECORD"
	rows = append(rows, []string{record, "", ""})
	var csvBody bytes.Buffer
	cw := csv.NewWriter(&csvBody)
	cw.WriteAll(rows)
	if err := cw.Error(); err != nil {
		t.Fatal(err)
	}
	files[record] = csvBody.Bytes()
	names = append(names, record)
	sort.Strings(names)
	var body bytes.Buffer
	zw := zip.NewWriter(&body)
	for _, p := range names {
		h := &zip.FileHeader{Name: p, Method: zip.Deflate}
		h.SetMode(0644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(files[p]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), files
}

type dependencyMetadataFixture struct {
	e                 *Engine
	s                 *store.Store
	dbPath, instances string
	request           CodingDependencyImportRequest
	create            CreateRequest
	expected          map[string][]byte
	verifier          string
	gateway           *dependencyMetadataGateway
}

func newDependencyMetadataFixture(t *testing.T, profile string) *dependencyMetadataFixture {
	t.Helper()
	vm, instances := dependencyMetadataRuntime(t)
	dbPath := filepath.Join(t.TempDir(), "import.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	e, _, q, _, _ := codingImportWithStore(t, "http://127.0.0.1:1/v1", s)
	f := &dependencyMetadataFixture{e: e, s: s, dbPath: dbPath, instances: instances, create: q, gateway: &dependencyMetadataGateway{}, verifier: "/opt/foundry/verify-node-v2"}
	t.Cleanup(func() { f.e.Close(); f.s.Close() })
	router := model.NewRouter(f.gateway, f.gateway, model.RouterOptions{CodingAdmission: func(context.Context, model.Request) error {
		f.gateway.calls.Add(1)
		return errors.New("metadata fixture forbids dispatch")
	}})
	cap, err := NewCodingVMCapabilityWithV2(router, nil, vm)
	if err != nil {
		t.Fatal(err)
	}
	e.options.C4Controlled, e.gateway = cap, f.gateway
	entries, err := os.ReadDir(q.RepoPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != ".git" {
			if err := os.RemoveAll(filepath.Join(q.RepoPath, entry.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	project := map[string][]byte{
		"src/main.js":        []byte("export const value = 1;\n"),
		"tests/main.test.js": []byte("// owned metadata-only test\n"),
		"package.json":       []byte(`{"name":"app","version":"1.0.0","dependencies":{"fixture-dep":"1.0.0"}}`),
		"package-lock.json":  []byte(`{"lockfileVersion":3,"packages":{"":{"name":"app","version":"1.0.0","dependencies":{"fixture-dep":"1.0.0"}},"node_modules/fixture-dep":{"name":"fixture-dep","version":"1.0.0"}}}`),
	}
	f.expected = map[string][]byte{"fixture-dep/package.json": []byte(`{"name":"fixture-dep","version":"1.0.0","types":"index.d.ts"}`), "fixture-dep/index.js": []byte("exports.value = 1;\n"), "fixture-dep/index.d.ts": []byte("export declare const value: number;\n")}
	inputFiles := f.expected
	if profile == "c4_typescript_model_v2" {
		project["src/main.ts"], project["tests/main.test.ts"] = project["src/main.js"], project["tests/main.test.js"]
		delete(project, "src/main.js")
		delete(project, "tests/main.test.js")
		project["tsconfig.json"] = []byte(`{"compilerOptions":{"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext","strict":true,"noEmitOnError":true,"allowJs":false,"skipLibCheck":false},"files":["src/main.ts","tests/main.test.ts"]}`)
		f.verifier = "/opt/foundry/verify-typescript-v2"
	}
	if profile == "c4_python_model_v2" {
		wheel, expanded := dependencyMetadataWheel(t)
		f.expected = expanded
		name := "fixture_dep-1.0-py3-none-any.whl"
		inputFiles = map[string][]byte{name: wheel}
		lock, err := json.Marshal(map[string]any{"schema_version": 1, "python": "3.13.5", "selected_extras": []string{}, "roots": []map[string]string{{"name": "fixture-dep", "version": "1.0"}}, "wheels": []map[string]any{{"name": "fixture-dep", "version": "1.0", "filename": name, "sha256": dependencyMetadataHash(wheel), "size": len(wheel)}}})
		if err != nil {
			t.Fatal(err)
		}
		project = map[string][]byte{"src/main.py": []byte("value = 1\n"), "tests/test_main.py": []byte("# owned metadata-only test\n"), "foundry.python.lock.json": lock}
		f.verifier = "/opt/foundry/verify-python-v2"
	}
	input, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for root, files := range map[string]map[string][]byte{q.RepoPath: project, input: inputFiles} {
		for p, body := range files {
			full := filepath.Join(root, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, body, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	importGit(t, q.RepoPath, "add", "-A")
	importGit(t, q.RepoPath, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "owned metadata dependencies")
	target, err := vm.ProbeTargetV2(profile)
	if err != nil {
		t.Fatal(err)
	}
	f.request = CodingDependencyImportRequest{RepoPath: q.RepoPath, Consent: true, ProfileID: profile, EmbeddedVM: target.Identity, RuntimeInventoryDigest: target.RuntimeInventoryDigest, InputDirectory: input}
	return f
}

func (f *dependencyMetadataFixture) assertNoExecution(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(f.instances)
	if err != nil || len(entries) != 0 || f.gateway.calls.Load() != 0 || f.e.active != nil {
		t.Fatalf("metadata import caused execution: entries=%d gateway=%d err=%v", len(entries), f.gateway.calls.Load(), err)
	}
}

func TestNativeCodingDependencyImport(t *testing.T) {
	for _, profile := range []string{"c4_node_model_v2", "c4_typescript_model_v2", "c4_python_model_v2"} {
		t.Run(profile, func(t *testing.T) {
			f := newDependencyMetadataFixture(t, profile)
			defer f.assertNoExecution(t)
			ctx := context.Background()
			root, err := f.e.dependencyStoreRoot()
			if err != nil {
				t.Fatal(err)
			}
			absent := func() {
				t.Helper()
				if _, err := os.Lstat(root); !os.IsNotExist(err) {
					t.Fatalf("fresh or rejected request created a dependency store: %v", err)
				}
			}
			absent()
			for _, bad := range []CodingDependencyImportRequest{func() CodingDependencyImportRequest { q := f.request; q.Consent = false; return q }(), func() CodingDependencyImportRequest {
				q := f.request
				q.RuntimeInventoryDigest = strings.Repeat("f", 64)
				return q
			}()} {
				if got, err := f.e.ImportCodingDependencies(ctx, bad); err == nil || got.AdmissionDigest != "" {
					t.Fatal("unapproved or stale request admitted")
				}
				absent()
			}
			before := importSourceDigest(t, f.request.RepoPath)
			result, err := f.e.ImportCodingDependencies(ctx, f.request)
			if err != nil {
				t.Fatalf("fresh engine dependency import: %v", err)
			}
			binding := dependencybundle.Binding(result.Dependencies)
			manifest, err := dependencybundle.DescribeVerified(ctx, root, binding)
			if err != nil || len(manifest.Files) != len(f.expected) || result.Summary.PackageCount != 1 || result.AdmissionDigest == "" {
				t.Fatalf("complete bound dependency inventory: %+v %v", result, err)
			}
			if err := platformfs.PrivateDirectory(root); err != nil {
				t.Fatalf("import root lacks native private protection: %v", err)
			}
			for _, file := range manifest.Files {
				body, exists := f.expected[file.Path]
				if !exists || file.SHA256 != dependencyMetadataHash(body) || file.Size != int64(len(body)) {
					t.Fatalf("wrong exact dependency file: %+v", file)
				}
			}
			saved, err := f.s.GetDependencyAdmission(result.AdmissionDigest)
			if err != nil || saved.Validate() != nil || saved.Bundle != binding || !reflect.DeepEqual(saved.SourceFiles, result.SourceFiles) {
				t.Fatalf("unbound persisted admission: %v", err)
			}
			if err := f.e.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.s.Close(); err != nil {
				t.Fatal(err)
			}
			f.s, err = store.Open(f.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			f.e = New(f.s, f.gateway, f.e.options)
			reopened, err := f.s.GetDependencyAdmission(result.AdmissionDigest)
			if err != nil || !reflect.DeepEqual(saved, reopened) {
				t.Fatalf("reopen changed admission: %v", err)
			}
			again, err := f.e.ImportCodingDependencies(ctx, f.request)
			if err != nil || !reflect.DeepEqual(result, again) {
				t.Fatalf("repeated metadata import not idempotent: %v", err)
			}
			spec := domain.RepositoryImportSpec{Scope: []string{"src/"}, Protected: []string{}, Acceptance: []string{f.verifier}, VerificationV2: &domain.C4ImportSelectionV2{SchemaVersion: 2, ProfileID: profile, EmbeddedVM: f.request.EmbeddedVM, RuntimeInventoryDigest: f.request.RuntimeInventoryDigest, AdmissionDigest: result.AdmissionDigest, Dependencies: result.Dependencies, SourceRoot: ".", TestRoot: "tests", TestSupportPrefixes: []string{}}}
			inspection, err := f.e.InspectRepository(ctx, domain.RepositoryInspectionRequest{RepoPath: f.request.RepoPath, Consent: true, Spec: spec})
			if err != nil || inspection.Status != "ready" {
				t.Fatalf("metadata inspection: %+v %v", inspection, err)
			}
			q := f.create
			q.ImportSelection = &domain.ImportSelection{InspectionID: inspection.ID, Digest: inspection.Digest}
			q.Spec.Scope, q.Spec.Protected, q.Spec.Acceptance, q.Spec.Image = spec.Scope, []string{}, spec.Acceptance, ""
			q.Spec.C4 = &domain.C4Profile{SchemaVersion: 2, ID: profile, Mode: "embedded_vm"}
			m, err := f.e.Create(ctx, q)
			if err != nil || m.State != "draft" || m.Budget.UsedCalls != 0 || m.Spec.C4.V2.DependencyBundleDigest != binding.BundleDigest {
				t.Fatalf("metadata-only task creation: %v", err)
			}
			invocations, err := f.s.ListInvocations(m.ID, 0, 100)
			if err != nil || len(invocations) != 0 {
				t.Fatalf("metadata creation dispatched an invocation: %v", err)
			}
			if importSourceDigest(t, f.request.RepoPath) != before {
				t.Fatal("metadata import changed committed or working source")
			}
			entry := manifest.Files[0]
			payload := filepath.Join(root, binding.BundleDigest, "payload", filepath.FromSlash(entry.Path))
			if err := os.WriteFile(payload, []byte("tampered"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := dependencybundle.DescribeVerified(ctx, root, binding); err == nil {
				t.Fatal("tampered dependency snapshot accepted")
			}
			if got, err := f.e.ImportCodingDependencies(ctx, f.request); err == nil || got.AdmissionDigest != "" {
				t.Fatal("import overwrote or accepted tampered retained bundle")
			}
			if body, err := os.ReadFile(payload); err != nil || string(body) != "tampered" {
				t.Fatal("failed import repaired tampered bytes")
			}
		})
	}
}
