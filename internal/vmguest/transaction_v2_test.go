package vmguest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"foundry/internal/dependencybundle"
	"foundry/internal/domain"
	"foundry/internal/platformfs"
	"foundry/internal/vmruntime"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func transactionFixtureV2(t *testing.T) (vmruntime.WireRequestV2, []vmruntime.SnapshotFile, string) {
	t.Helper()
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	q := probeV2(t, "c4_node_model_v2")
	q.Action = "verify"
	files := []vmruntime.SnapshotFile{{Path: "package-lock.json", Mode: 0644, Data: []byte("lock")}, {Path: "package.json", Mode: 0644, Data: []byte("{}")}, {Path: "tests/a.test.js", Mode: 0644, Data: []byte("owned test")}}
	c := domain.C4InputContractV2{SchemaVersion: 2, ProfileID: q.Target.ProfileID, AdmissionDigest: strings.Repeat("d", 64), SourceFiles: []domain.C4SourceDigestV2{}, Protected: []domain.C4InputFileV2{}, ProtectedPrefixes: []string{".foundry-build", ".venv", "node_modules", "site-packages", "tests"}, TestFiles: []string{"tests/a.test.js"}, SourceRoots: []string{"."}, HarnessVersion: "foundry-test-v2"}
	for _, f := range files {
		role := "test"
		switch f.Path {
		case "package-lock.json":
			role = "lock"
		case "package.json":
			role = "manifest"
		}
		c.Protected = append(c.Protected, domain.C4InputFileV2{Path: f.Path, Mode: f.Mode, Bytes: int64(len(f.Data)), SHA256: sum(f.Data), Role: role})
		if role != "test" {
			c.SourceFiles = append(c.SourceFiles, domain.C4SourceDigestV2{Path: f.Path, Bytes: int64(len(f.Data)), SHA256: sum(f.Data)})
		}
	}
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	store, source := filepath.Join(base, "store"), filepath.Join(base, "deps")
	for _, p := range []string{store, source} {
		if e = platformfs.PrivateDirectory(p); e != nil {
			t.Fatal(e)
		}
	}
	data := []byte("owned dependency")
	if e = os.WriteFile(filepath.Join(source, "dep"), data, 0600); e != nil {
		t.Fatal(e)
	}
	manifest := dependencybundle.Manifest{SchemaVersion: 1, Ecosystem: "node", LockSHA256: sum([]byte("lock")), Files: []dependencybundle.File{{Path: "dep", Size: int64(len(data)), SHA256: sum(data)}}}
	binding, e := dependencybundle.ImportFrozen(context.Background(), store, source, manifest)
	if e != nil {
		t.Fatal(e)
	}
	c.Dependencies = domain.C4DependencyBindingV2(binding)
	c, e = c.WithDigests()
	if e != nil {
		t.Fatal(e)
	}
	p := domain.C4Profile{SchemaVersion: 2, ControlVersion: 1, ID: c.ProfileID, Mode: "embedded_vm", Platform: q.Target.Identity.GuestPlatform, Verifier: "/opt/foundry/verify-node-v2", EmbeddedVM: &q.Target.Identity, V2: &domain.C4ProfileV2{Language: "javascript", RunnerID: "node-test-v2", RuntimeInventoryDigest: q.Target.RuntimeInventoryDigest, DependencyBundleDigest: binding.BundleDigest, ProtectedInputsDigest: c.ProtectedInputsDigest, ExecutionContractDigest: c.ExecutionContractDigest}}
	policy, _ := vmruntime.PolicyDigestV2(p.ID)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	metadata, e := vmruntime.SourceMetadataV2(files)
	if e != nil {
		t.Fatal(e)
	}
	candidate, e := vmruntime.SnapshotDigest(files)
	if e != nil {
		t.Fatal(e)
	}
	q.Verify = &vmruntime.VerifyRequestV2{OwnerDigest: strings.Repeat("e", 64), Profile: p, ProfileDigest: p.ProfileDigest(), PolicyDigest: policy, Inputs: c, CandidateDigest: candidate, SourceFiles: metadata, DependencyManifest: manifest}
	return q, files, store
}

type stagingMachineV2 struct {
	*fixtureV2
	stager *stagerV2
}

func (m *stagingMachineV2) Stage(k uint8, i uint32, f vmruntime.SourceFileV2, r io.Reader) error {
	return m.stager.Stage(k, i, f, r)
}
func TestV2TransactionStagesProvisionallyUntilTerminal(t *testing.T) {
	q, files, store := transactionFixtureV2(t)
	var wire bytes.Buffer
	if e := vmruntime.WriteRequestV2(context.Background(), &wire, q, files, store); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []bool{false, true} {
		root := t.TempDir()
		s, e := newStagerV2(filepath.Join(root, "source"), filepath.Join(root, "deps"), os.Getpagesize())
		if e != nil {
			t.Fatal(e)
		}
		m := &stagingMachineV2{fixtureV2: &fixtureV2{outcome: completedV2(), report: oneReportV2}, stager: s}
		raw := bytes.Clone(wire.Bytes())
		if bad {
			raw[len(raw)-1] ^= 1
		}
		var out memoryWireV2
		e = ServeV2(context.Background(), finiteReceiveV2{bytes.NewReader(raw)}, &out, "linux/amd64", m)
		if bad {
			if e == nil || m.prepares != 0 || len(m.phases) != 0 {
				t.Fatal("provisional content authorized execution")
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		v, e := vmruntime.ReadResponseV2(bytes.NewReader(bytes.TrimPrefix(out.Bytes(), []byte(vmruntime.ReadyMarkerV2))), q)
		if e != nil || v.Result.Class != "passed" || v.Result.Tests != 1 {
			t.Fatal(v, e)
		}
		for _, f := range files {
			got, e := os.ReadFile(filepath.Join(s.source, f.Path))
			if e != nil || !bytes.Equal(got, f.Data) {
				t.Fatal("source staging drift", f.Path, e)
			}
		}
		dep, e := os.ReadFile(filepath.Join(s.deps, "dep"))
		if e != nil || string(dep) != "owned dependency" {
			t.Fatal("dependency staging drift", e)
		}
	}
}
