package vmruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/dependencybundle"
	"foundry/internal/domain"
	"foundry/internal/platformfs"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func hashV2Test(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func targetV2Test(id string) ProbeTargetV2 {
	h := strings.Repeat("a", 64)
	return ProbeTargetV2{ProfileID: id, Identity: domain.C4VMIdentity{SchemaVersion: 2, ManifestDigest: h, HostPlatform: "darwin/arm64", GuestPlatform: "linux/arm64", RuntimeDigest: h, KernelDigest: h, RootFSDigest: h, VerifierDigest: h, Protocol: "foundry-vm-v2", PolicyVersion: "multilang-vm-v2"}, RuntimeInventoryDigest: h}
}
func probeV2Test(id string) WireRequestV2 {
	return WireRequestV2{SchemaVersion: 2, Action: "probe", Nonce: strings.Repeat("b", 64), Secret: strings.Repeat("c", 64), Target: targetV2Test(id)}
}
func fixtureV2Test(t *testing.T, id string) (WireRequestV2, []SnapshotFile, string) {
	t.Helper()
	q := probeV2Test(id)
	q.Action = "verify"
	eco, language, runner, verifier, test := "node", "javascript", "node-test-v2", "/opt/foundry/verify-node-v2", "tests/a.test.js"
	files := []SnapshotFile{{Path: "package-lock.json", Mode: 0644, Data: []byte("lock")}, {Path: "package.json", Mode: 0644, Data: []byte("{}")}}
	if id == "c4_python_model_v2" {
		eco, language, runner, verifier, test = "python", "python", "python-unittest-v2", "/opt/foundry/verify-python-v2", "tests/test_a.py"
		files = []SnapshotFile{{Path: "foundry.python.lock.json", Mode: 0644, Data: []byte("lock")}}
	}
	if id == "c4_typescript_model_v2" {
		language, runner, verifier, test = "typescript", "typescript-node-test-v2", "/opt/foundry/verify-typescript-v2", "tests/a.test.ts"
		files = append(files, SnapshotFile{Path: "tsconfig.json", Mode: 0644, Data: []byte("{}")})
	}
	c := domain.C4InputContractV2{SchemaVersion: 2, ProfileID: id, AdmissionDigest: strings.Repeat("d", 64), SourceFiles: []domain.C4SourceDigestV2{}, Protected: []domain.C4InputFileV2{}, ProtectedPrefixes: []string{".foundry-build", ".venv", "node_modules", "site-packages", "tests"}, TestFiles: []string{test}, SourceRoots: []string{"."}, HarnessVersion: "foundry-test-v2"}
	for _, f := range files {
		role := "manifest"
		if strings.Contains(f.Path, "lock") {
			role = "lock"
		}
		if f.Path == "tsconfig.json" {
			role = "config"
		}
		c.SourceFiles = append(c.SourceFiles, domain.C4SourceDigestV2{Path: f.Path, Bytes: int64(len(f.Data)), SHA256: hashV2Test(f.Data)})
		c.Protected = append(c.Protected, domain.C4InputFileV2{Path: f.Path, Mode: f.Mode, Bytes: int64(len(f.Data)), SHA256: hashV2Test(f.Data), Role: role})
	}
	files = append(files, SnapshotFile{Path: test, Mode: 0644, Data: []byte("test")}, SnapshotFile{Path: "src/a.txt", Mode: 0755, Data: []byte{}})
	c.Protected = append(c.Protected, domain.C4InputFileV2{Path: test, Mode: 0644, Bytes: 4, SHA256: hashV2Test([]byte("test")), Role: "test"})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Slice(c.Protected, func(i, j int) bool { return c.Protected[i].Path < c.Protected[j].Path })
	if language == "typescript" {
		c.TypeScript = &domain.C4TypeScriptContractV2{ConfigPath: "tsconfig.json", ConfigDigest: hashV2Test([]byte("{}")), InputFiles: []string{test}, OutputRoot: "/foundry-build/compiled"}
	}
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root, src := filepath.Join(base, "store"), filepath.Join(base, "deps")
	for _, p := range []string{root, src} {
		if e = platformfs.PrivateDirectory(p); e != nil {
			t.Fatal(e)
		}
	}
	data := []byte{0, 1, 2, 255}
	if e = os.WriteFile(filepath.Join(src, "dep"), data, 0644); e != nil {
		t.Fatal(e)
	}
	m := dependencybundle.Manifest{SchemaVersion: 1, Ecosystem: eco, LockSHA256: hashV2Test([]byte("lock")), Files: []dependencybundle.File{{Path: "dep", Size: 4, SHA256: hashV2Test(data)}}}
	b, e := dependencybundle.ImportFrozen(context.Background(), root, src, m)
	if e != nil {
		t.Fatal(e)
	}
	c.Dependencies = domain.C4DependencyBindingV2(b)
	c, e = c.WithDigests()
	if e != nil {
		t.Fatal(e)
	}
	p := domain.C4Profile{SchemaVersion: 2, ControlVersion: 1, ID: id, Mode: "embedded_vm", Platform: "linux/arm64", Verifier: verifier, EmbeddedVM: &q.Target.Identity, V2: &domain.C4ProfileV2{Language: language, RunnerID: runner, RuntimeInventoryDigest: q.Target.RuntimeInventoryDigest, DependencyBundleDigest: b.BundleDigest, ProtectedInputsDigest: c.ProtectedInputsDigest, ExecutionContractDigest: c.ExecutionContractDigest}}
	owner, e := OwnerDigestV2(Request{OperationID: "op", MissionID: "mission", Owner: "owner", Name: "name"})
	if e != nil {
		t.Fatal(e)
	}
	policy, e := PolicyDigestV2(id)
	if e != nil {
		t.Fatal(e)
	}
	source, e := SourceMetadataV2(files)
	if e != nil {
		t.Fatal(e)
	}
	candidate, e := SnapshotDigest(files)
	if e != nil {
		t.Fatal(e)
	}
	q.Verify = &VerifyRequestV2{OwnerDigest: owner, Profile: p, ProfileDigest: p.ProfileDigest(), PolicyDigest: policy, Inputs: c, CandidateDigest: candidate, SourceFiles: source, DependencyManifest: m}
	return q, files, root
}
func rawV2Test(b []byte) []byte {
	out := binary.BigEndian.AppendUint32([]byte(HeaderV2), uint32(len(b)))
	return append(out, b...)
}
func wireV2Test(t *testing.T, q WireRequestV2, source []SnapshotFile, root string) []byte {
	t.Helper()
	var b bytes.Buffer
	if e := WriteRequestV2(context.Background(), &b, q, source, root); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func drainV2Test(_ uint8, _ uint32, _ SourceFileV2, r io.Reader) error {
	_, e := io.Copy(io.Discard, r)
	return e
}
func TestV2RequestRoundTripProfilesAndNoReadAhead(t *testing.T) {
	for _, id := range []string{"c4_node_model_v2", "c4_typescript_model_v2", "c4_python_model_v2"} {
		t.Run(id, func(t *testing.T) {
			q, source, root := fixtureV2Test(t, id)
			wire := wireV2Test(t, q, source, root)
			r := bytes.NewReader(append(wire, 42))
			var bodies [][]byte
			got, e := ReadRequestV2(context.Background(), r, func(kind uint8, index uint32, file SourceFileV2, body io.Reader) error {
				b, e := io.ReadAll(body)
				bodies = append(bodies, b)
				if kind == 2 && file.Mode != 0444 {
					t.Fatal("dependency mode")
				}
				return e
			})
			if e != nil || !reflect.DeepEqual(q, got) {
				t.Fatalf("read: %v", e)
			}
			if len(bodies) != len(source)+1 {
				t.Fatal("missing zero-byte frame")
			}
			for i := range source {
				if !bytes.Equal(bodies[i], source[i].Data) {
					t.Fatal("payload changed")
				}
			}
			if r.Len() != 1 {
				t.Fatal("reader consumed post-terminal bytes")
			}
			probe := probeV2Test(id)
			pwire := wireV2Test(t, probe, nil, "")
			if _, e := ReadRequestV2(context.Background(), bytes.NewReader(pwire), func(uint8, uint32, SourceFileV2, io.Reader) error { t.Fatal("probe payload"); return nil }); e != nil {
				t.Fatal(e)
			}
			for _, bad := range []struct {
				s    []SnapshotFile
				root string
			}{{source, ""}, {nil, root}} {
				if WriteRequestV2(context.Background(), io.Discard, probe, bad.s, bad.root) == nil {
					t.Fatal("probe project input accepted")
				}
			}
		})
	}
}
func TestV2RequestRejectsFrameAndBodyFaults(t *testing.T) {
	q, s, root := fixtureV2Test(t, "c4_node_model_v2")
	wire := wireV2Test(t, q, s, root)
	offset := len(HeaderV2) + 4 + int(binary.BigEndian.Uint32(wire[len(HeaderV2):]))
	mutations := map[string]func([]byte) []byte{
		"kind": func(b []byte) []byte { b[offset] = 2; return b }, "index": func(b []byte) []byte { b[offset+4] = 1; return b }, "size": func(b []byte) []byte { b[offset+12]++; return b }, "overflow": func(b []byte) []byte {
			for i := offset + 5; i < offset+13; i++ {
				b[i] = 255
			}
			return b
		}, "body hash": func(b []byte) []byte { b[offset+13] ^= 1; return b }, "terminal hash": func(b []byte) []byte { b[len(b)-1] ^= 1; return b }, "terminal index": func(b []byte) []byte { b[len(b)-41] = 1; return b }, "missing terminal": func(b []byte) []byte { return b[:len(b)-45] }, "partial terminal": func(b []byte) []byte { return b[:len(b)-1] }, "partial body": func(b []byte) []byte { return b[:offset+14] }, "second source": func(b []byte) []byte { return append(append([]byte{}, b[:offset+17]...), b[offset:]...) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			if _, e := ReadRequestV2(context.Background(), bytes.NewReader(mutate(bytes.Clone(wire))), drainV2Test); e == nil {
				t.Fatal("bad frame accepted")
			}
		})
	}
	if _, e := ReadRequestV2(context.Background(), bytes.NewReader(wire), func(uint8, uint32, SourceFileV2, io.Reader) error { return nil }); e == nil {
		t.Fatal("partial sink accepted")
	}
	if _, e := ReadRequestV2(context.Background(), bytes.NewReader(wire), func(uint8, uint32, SourceFileV2, io.Reader) error { return errors.New("private body") }); e == nil || strings.Contains(e.Error(), "private") {
		t.Fatal("sink error leaked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := ReadRequestV2(ctx, bytes.NewReader(wire), drainV2Test); e == nil {
		t.Fatal("canceled reader")
	}
	if WriteRequestV2(ctx, io.Discard, q, s, root) == nil {
		t.Fatal("canceled writer")
	}
	if WriteRequestV2(context.Background(), shortWriterV2{}, q, s, root) == nil {
		t.Fatal("short writer accepted")
	}
}

type shortWriterV2 struct{}

func (shortWriterV2) Write(b []byte) (int, error) { return len(b) - 1, nil }
func TestV2RejectsInvalidMetadataBeforePayload(t *testing.T) {
	q, _, _ := fixtureV2Test(t, "c4_node_model_v2")
	raw, _ := json.Marshal(q)
	mutations := []string{
		strings.Replace(string(raw), `"schema_version":2`, `"schema_version":1`, 1), strings.Replace(string(raw), `"action":"verify"`, `"action":"probe"`, 1),
		strings.Replace(string(raw), `"nonce":`, `"Nonce":`, 1), strings.Replace(string(raw), `"nonce":`, `"extra":1,"nonce":`, 1), strings.Replace(string(raw), `"action":"verify"`, `"action":"verify","action":"verify"`, 1),
		strings.Replace(string(raw), `"schema_version":2`, `"schema_version":2.0`, 1), strings.Replace(string(raw), `"schema_version":2`, `"schema_version":2e0`, 1), strings.Replace(string(raw), `"size":4`, `"size":-0`, 1),
		strings.Replace(string(raw), `"mode":420`, `"mode":null`, 1), string(raw) + `{}`, strings.Replace(string(raw), `"action":"verify"`, `"action":"\ud800"`, 1),
	}
	mutations = append(mutations, string(append([]byte{255}, raw...)))
	for i, raw := range mutations {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			if _, e := ReadRequestV2(context.Background(), bytes.NewReader(rawV2Test([]byte(raw))), func(uint8, uint32, SourceFileV2, io.Reader) error { t.Fatal("payload exposed"); return nil }); e == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
	// Header bound must be rejected without reading or allocating the declared body.
	header := binary.BigEndian.AppendUint32([]byte(HeaderV2), 8<<20+1)
	if _, e := ReadRequestV2(context.Background(), bytes.NewReader(header), nil); e == nil {
		t.Fatal("oversize metadata")
	}
}
func TestV2RejectsStaleBindingsAndCanonicalInventory(t *testing.T) {
	mutations := map[string]func(*WireRequestV2){
		"owner": func(q *WireRequestV2) { q.Verify.OwnerDigest = "" }, "profile": func(q *WireRequestV2) { q.Verify.Profile.ID = "c4_python_model_v2" }, "profile digest": func(q *WireRequestV2) { q.Verify.ProfileDigest = strings.Repeat("f", 64) }, "policy": func(q *WireRequestV2) { q.Verify.PolicyDigest = strings.Repeat("f", 64) }, "target": func(q *WireRequestV2) { q.Target.Identity.RuntimeDigest = strings.Repeat("f", 64) }, "inventory": func(q *WireRequestV2) { q.Target.RuntimeInventoryDigest = strings.Repeat("f", 64) }, "inputs": func(q *WireRequestV2) { q.Verify.Inputs.AdmissionDigest = strings.Repeat("f", 64) }, "protected": func(q *WireRequestV2) { q.Verify.SourceFiles[0].SHA256 = strings.Repeat("f", 64) }, "dep": func(q *WireRequestV2) { q.Verify.DependencyManifest.Files[0].SHA256 = strings.Repeat("f", 64) }, "lock": func(q *WireRequestV2) { q.Verify.DependencyManifest.LockSHA256 = strings.Repeat("f", 64) }, "null source": func(q *WireRequestV2) { q.Verify.SourceFiles = nil }, "null deps": func(q *WireRequestV2) { q.Verify.DependencyManifest.Files = nil }, "order": func(q *WireRequestV2) {
			q.Verify.SourceFiles[0], q.Verify.SourceFiles[1] = q.Verify.SourceFiles[1], q.Verify.SourceFiles[0]
		}, "source bound": func(q *WireRequestV2) { q.Verify.SourceFiles[2].Size = 10<<20 + 1 }, "dep file bound": func(q *WireRequestV2) { q.Verify.DependencyManifest.Files[0].Size = 8<<20 + 1 }, "alias": func(q *WireRequestV2) { q.Verify.SourceFiles[2].Path = "Tests/other.js" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			q, _, _ := fixtureV2Test(t, "c4_node_model_v2")
			mutate(&q)
			if ValidateRequestV2(q) == nil {
				t.Fatal("stale metadata accepted")
			}
		})
	}
}
