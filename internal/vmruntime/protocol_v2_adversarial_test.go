package vmruntime

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"foundry/internal/dependencybundle"
	"foundry/internal/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

// This constructor intentionally bypasses writer validation so reader tests can
// exercise independently authenticated but invalid payloads and frame layouts.
func uncheckedWireV2Test(q WireRequestV2, source []SnapshotFile, dep []byte) []byte {
	raw, _ := json.Marshal(q)
	out := rawV2Test(raw)
	frame := func(kind byte, index uint32, p []byte) {
		out = append(out, kind)
		out = binary.BigEndian.AppendUint32(out, index)
		out = binary.BigEndian.AppendUint64(out, uint64(len(p)))
		out = append(out, p...)
	}
	for i, f := range source {
		frame(1, uint32(i), f.Data)
	}
	if q.Verify != nil {
		for i := range q.Verify.DependencyManifest.Files {
			frame(2, uint32(i), dep)
		}
	}
	d, _ := RequestDigestV2(q)
	terminal, _ := hex.DecodeString(d)
	frame(3, 0, terminal)
	return out
}
func rebindV2Test(t *testing.T, q *WireRequestV2) {
	t.Helper()
	b, e := q.Verify.DependencyManifest.Binding()
	if e != nil {
		t.Fatal(e)
	}
	q.Verify.Inputs.Dependencies = domain.C4DependencyBindingV2(b)
	c, e := q.Verify.Inputs.WithDigests()
	if e != nil {
		t.Fatal(e)
	}
	q.Verify.Inputs = c
	q.Verify.Profile.V2.DependencyBundleDigest = b.BundleDigest
	q.Verify.Profile.V2.ProtectedInputsDigest = c.ProtectedInputsDigest
	q.Verify.Profile.V2.ExecutionContractDigest = c.ExecutionContractDigest
	q.Verify.ProfileDigest = q.Verify.Profile.ProfileDigest()
}
func TestV2ReaderValidatesSourceTextAcrossChunkBoundaries(t *testing.T) {
	for _, data := range [][]byte{[]byte("hello ☀ 世界"), bytes.Repeat([]byte("界"), 22000), {0}, {255}, {0xe2, 0x82}, {0xed, 0xa0, 0x80}} {
		q, s, _ := fixtureV2Test(t, "c4_node_model_v2")
		i := 2
		s[i].Data = data
		q.Verify.SourceFiles[i].Size = int64(len(data))
		q.Verify.SourceFiles[i].SHA256 = hashV2Test(data)
		var snapshot bytes.Buffer
		for _, f := range s {
			fmt.Fprintf(&snapshot, "%d:%s:%d:%o:", len(f.Path), f.Path, len(f.Data), f.Mode)
			snapshot.Write(f.Data)
		}
		q.Verify.CandidateDigest = hashV2Test(snapshot.Bytes())
		wire := uncheckedWireV2Test(q, s, []byte{0, 1, 2, 255})
		_, e := ReadRequestV2(context.Background(), iotest.OneByteReader(bytes.NewReader(wire)), drainV2Test)
		valid := len(data) > 3
		if valid && e != nil {
			t.Fatal("split UTF-8 rejected", e)
		}
		if !valid && e == nil {
			t.Fatal("nontext source accepted")
		}
	}
}
func TestV2RejectsCandidateMismatchAndWriterMetadataMismatch(t *testing.T) {
	q, s, root := fixtureV2Test(t, "c4_node_model_v2")
	q.Verify.CandidateDigest = strings.Repeat("f", 64)
	if WriteRequestV2(context.Background(), io.Discard, q, s, root) == nil {
		t.Fatal("writer ignored snapshot digest")
	}
	if _, e := ReadRequestV2(context.Background(), bytes.NewReader(uncheckedWireV2Test(q, s, []byte{0, 1, 2, 255})), drainV2Test); e == nil {
		t.Fatal("reader ignored snapshot digest")
	}
	q.Verify.CandidateDigest, _ = SnapshotDigest(s)
	s[2].Data = []byte("drift")
	if WriteRequestV2(context.Background(), io.Discard, q, s, root) == nil {
		t.Fatal("writer ignored per-file metadata")
	}
	s[0], s[1] = s[1], s[0]
	if _, e := SourceMetadataV2(s); e == nil {
		t.Fatal("source metadata normalized ordering")
	}
}

type mutateWriterV2 struct {
	bytes.Buffer
	at     int
	mutate func()
	done   bool
}

func (w *mutateWriterV2) Write(p []byte) (int, error) {
	n, e := w.Buffer.Write(p)
	if !w.done && w.Len() >= w.at {
		w.done = true
		w.mutate()
	}
	return n, e
}
func TestV2WriterNeverTerminatesAfterLateStoreDrift(t *testing.T) {
	for _, name := range []string{"payload", "manifest"} {
		t.Run(name, func(t *testing.T) {
			q, s, root := fixtureV2Test(t, "c4_node_model_v2")
			good := wireV2Test(t, q, s, root)
			b := q.Verify.Inputs.Dependencies
			// Mutation runs synchronously after the final dependency frame body was
			// written, while ReadVerified still owns its final inventory/manifest checks.
			w := &mutateWriterV2{at: len(good) - 45}
			w.mutate = func() {
				p := filepath.Join(root, b.BundleDigest, "manifest.json")
				data := []byte("bad")
				if name == "payload" {
					p = filepath.Join(root, b.BundleDigest, "payload/dep")
					data = []byte{4, 3, 2, 1}
				}
				if e := os.WriteFile(p, data, 0644); e != nil {
					t.Fatal(e)
				}
			}
			if e := WriteRequestV2(context.Background(), w, q, s, root); e == nil {
				t.Fatal("late drift emitted successful transaction")
			}
			if !w.done || w.Len() != len(good)-45 {
				t.Fatal("terminal was emitted on late drift")
			}
			if _, e := ReadRequestV2(context.Background(), bytes.NewReader(w.Bytes()), drainV2Test); e == nil {
				t.Fatal("partial transaction committed")
			}
		})
	}
}
func TestV2CanceledTransferAndSinkCannotReadNextFrame(t *testing.T) {
	q, s, root := fixtureV2Test(t, "c4_node_model_v2")
	wire := wireV2Test(t, q, s, root)
	ctx, cancel := context.WithCancel(context.Background())
	var retained io.Reader
	_, e := ReadRequestV2(ctx, bytes.NewReader(wire), func(_ uint8, _ uint32, _ SourceFileV2, r io.Reader) error {
		retained = r
		_, e := io.Copy(io.Discard, r)
		cancel()
		return e
	})
	if e == nil {
		t.Fatal("canceled sink committed")
	}
	if _, e = retained.Read(make([]byte, 1)); e == nil {
		t.Fatal("retained sink reader remains live")
	}
	ctx, cancel = context.WithCancel(context.Background())
	w := &mutateWriterV2{at: len(wire) - 45, mutate: cancel}
	if WriteRequestV2(ctx, w, q, s, root) == nil || w.Len() != len(wire)-45 {
		t.Fatal("cancellation emitted terminal")
	}
	if _, e := ReadRequestV2(context.Background(), iotest.HalfReader(bytes.NewReader(wire)), drainV2Test); e != nil {
		t.Fatal(e)
	}
}
func TestV2IndependentRawMetadataBounds(t *testing.T) {
	q, _, _ := fixtureV2Test(t, "c4_node_model_v2")
	raw, _ := json.Marshal(q)
	for _, bound := range []struct {
		key   string
		limit int
	}{{"source_files", 1 << 20}, {"inputs", 1 << 20}, {"dependency_manifest", 4 << 20}} {
		t.Run(bound.key, func(t *testing.T) {
			// Insert whitespace inside the exact requested subvalue (source_files occurs
			// twice, so locate the verify-level inventory by its first path/mode tuple).
			needle := `"` + bound.key + `":`
			offset := strings.LastIndex(string(raw), needle) + len(needle)
			changed := append(bytes.Clone(raw[:offset+1]), bytes.Repeat([]byte(" "), bound.limit)...)
			changed = append(changed, raw[offset+1:]...)
			if _, e := ReadRequestV2(context.Background(), bytes.NewReader(rawV2Test(changed)), func(uint8, uint32, SourceFileV2, io.Reader) error {
				t.Fatal("oversized metadata reached sink")
				return nil
			}); e == nil {
				t.Fatal("subvalue bound ignored")
			}
		})
	}
}
func TestV2RejectsDependencyAncestorInodeOverflow(t *testing.T) {
	q, _, _ := fixtureV2Test(t, "c4_node_model_v2")
	file := q.Verify.DependencyManifest.Files[0]
	file.Size = 0
	file.SHA256 = hashV2Test(nil)
	q.Verify.DependencyManifest.Files = q.Verify.DependencyManifest.Files[:0]
	for i := 0; i < 1100; i++ {
		f := file
		f.Path = fmt.Sprintf("%04d/", i) + strings.Repeat("d/", 30) + "f"
		q.Verify.DependencyManifest.Files = append(q.Verify.DependencyManifest.Files, f)
	}
	rebindV2Test(t, &q)
	if ValidateRequestV2(q) == nil {
		t.Fatal("ancestor directories not charged to dependency inodes")
	}
}
func TestV2RejectsSourceAndDependencyCountByteAndManifestBounds(t *testing.T) {
	for _, kind := range []string{"source count", "source bytes", "dependency count", "dependency bytes", "dependency manifest"} {
		t.Run(kind, func(t *testing.T) {
			q, _, _ := fixtureV2Test(t, "c4_node_model_v2")
			switch kind {
			case "source count":
				for i := 0; i < 501; i++ {
					q.Verify.SourceFiles = append(q.Verify.SourceFiles, SourceFileV2{Path: fmt.Sprintf("z%04d", i), Mode: 0644, SHA256: hashV2Test(nil)})
				}
			case "source bytes":
				q.Verify.SourceFiles[2].Size = 10 << 20
			case "dependency count":
				f := q.Verify.DependencyManifest.Files[0]
				q.Verify.DependencyManifest.Files = nil
				for i := 0; i < 10001; i++ {
					f.Path = fmt.Sprintf("f%05d", i)
					q.Verify.DependencyManifest.Files = append(q.Verify.DependencyManifest.Files, f)
				}
			case "dependency bytes":
				f := q.Verify.DependencyManifest.Files[0]
				q.Verify.DependencyManifest.Files = nil
				for i := 0; i < 9; i++ {
					f.Path = fmt.Sprintf("f%05d", i)
					f.Size = 8 << 20
					q.Verify.DependencyManifest.Files = append(q.Verify.DependencyManifest.Files, f)
				}
			case "dependency manifest":
				f := q.Verify.DependencyManifest.Files[0]
				q.Verify.DependencyManifest.Files = nil
				for i := 0; i < 5000; i++ {
					f.Path = fmt.Sprintf("f%05d/", i) + strings.Repeat("abcdefghijklmnopqrstuvwxyzABCDE/", 30) + "f"
					q.Verify.DependencyManifest.Files = append(q.Verify.DependencyManifest.Files, f)
				}
			}
			if ValidateRequestV2(q) == nil {
				t.Fatal("bound ignored")
			}
		})
	}
}
func TestV2OwnerDigestBindsOnlyExactOwnership(t *testing.T) {
	q := Request{OperationID: "operation", MissionID: "mission", Owner: "owner", Name: "name"}
	d, e := OwnerDigestV2(q)
	if e != nil {
		t.Fatal(e)
	}
	want := hashV2Test([]byte("foundry:vm:owner:v2\n" + `{"operation_id":"operation","mission_id":"mission","owner":"owner","name":"name"}`))
	if d != want {
		t.Fatal("owner digest domain/fields")
	}
	for _, change := range []func(*Request){func(q *Request) { q.OperationID += "x" }, func(q *Request) { q.MissionID += "x" }, func(q *Request) { q.Owner += "x" }, func(q *Request) { q.Name += "x" }} {
		other := q
		change(&other)
		next, e := OwnerDigestV2(other)
		if e != nil || next == d {
			t.Fatal("owner field unbound")
		}
	}
	q.CandidatePath = "private path"
	q.CandidateDigest = strings.Repeat("a", 64)
	if next, _ := OwnerDigestV2(q); next != d {
		t.Fatal("host path/candidate in owner digest")
	}
	for _, invalid := range []string{"", strings.Repeat("x", 257), "x\n", "x\x00", string([]byte{255})} {
		q.Owner = invalid
		if _, e := OwnerDigestV2(q); e == nil {
			t.Fatal("invalid owner")
		}
	}
}

func TestV2MountCapacityIncludesActualFilePageAllocation(t *testing.T) {
	q, _, _ := fixtureV2Test(t, "c4_node_model_v2")
	f := q.Verify.DependencyManifest.Files[0]
	q.Verify.DependencyManifest.Files = nil
	for i := 0; i < 7000; i++ {
		f.Path = fmt.Sprintf("f%04d", i)
		f.Size = 8193
		q.Verify.DependencyManifest.Files = append(q.Verify.DependencyManifest.Files, f)
	}
	rebindV2Test(t, &q)
	if ValidateRequestV2(q) == nil {
		t.Fatal("page-rounded dependency payload exceeds 80 MiB mount")
	}
	// Source inventory cannot independently exceed 16 MiB under its 10 MiB/500
	// limits, but the admission helper must charge real metadata, not just count.
	if mountFitsV2([]SourceFileV2{{Path: "a", Size: 4097}}, domain.C4Tmpfs{SizeBytes: 8191, Inodes: 2}) {
		t.Fatal("source file page allocation ignored")
	}
	if !mountFitsV2([]SourceFileV2{{Path: "a", Size: 4097}}, domain.C4Tmpfs{SizeBytes: 8192, Inodes: 2}) {
		t.Fatal("exact capacity rejected")
	}
}
func TestV2RejectsMixedSchemaAndBindsRequestDigestDomain(t *testing.T) {
	q := probeV2Test("c4_node_model_v2")
	wire := wireV2Test(t, q, nil, "")
	if _, e := ReadRequest(bytes.NewReader(wire)); e == nil {
		t.Fatal("v1 accepted v2")
	}
	var old bytes.Buffer
	WriteRequest(&old, probeRequest())
	if _, e := ReadRequestV2(context.Background(), &old, nil); e == nil {
		t.Fatal("v2 accepted v1")
	}
	d, e := RequestDigestV2(q)
	if e != nil {
		t.Fatal(e)
	}
	q.Secret = ""
	raw, _ := json.Marshal(q)
	if d != hashV2Test(append([]byte("foundry:vm:request:v2\n"), raw...)) {
		t.Fatal("request digest domain/secret stripping")
	}
	for _, mutate := range []func(*WireRequestV2){func(q *WireRequestV2) { q.Action = "exec" }, func(q *WireRequestV2) { q.Target.ProfileID = "c4_go_model_v1" }, func(q *WireRequestV2) { q.Nonce = strings.Repeat("A", 64) }, func(q *WireRequestV2) { q.Secret = "bad" }, func(q *WireRequestV2) { q.Target.Identity.SchemaVersion = 1 }} {
		q = probeV2Test("c4_node_model_v2")
		mutate(&q)
		if ValidateRequestV2(q) == nil {
			t.Fatal("invalid identity/action accepted")
		}
	}
}

func TestV2WriterRejectsInvalidatedMetadataBeforeTerminal(t *testing.T) {
	q, s, root := fixtureV2Test(t, "c4_node_model_v2")
	w := &mutateWriterV2{at: 1, mutate: func() { q.Verify.OwnerDigest = "" }}
	if WriteRequestV2(context.Background(), w, q, s, root) == nil {
		t.Fatal("writer claimed success after terminal digest validation failed")
	}
}

func TestV2AcceptsExactSourceAndDependencyAdmissionBounds(t *testing.T) {
	for _, kind := range []string{"source count", "source bytes", "dependency count", "dependency bytes"} {
		t.Run(kind, func(t *testing.T) {
			q, _, _ := fixtureV2Test(t, "c4_node_model_v2")
			switch kind {
			case "source count":
				for i := 0; len(q.Verify.SourceFiles) < 500; i++ {
					q.Verify.SourceFiles = append(q.Verify.SourceFiles, SourceFileV2{Path: fmt.Sprintf("z%04d", i), Mode: 0644, SHA256: hashV2Test(nil)})
				}
			case "source bytes":
				q.Verify.SourceFiles[2].Size = 10<<20 - 10
			case "dependency count", "dependency bytes":
				f := q.Verify.DependencyManifest.Files[0]
				q.Verify.DependencyManifest.Files = nil
				n := 10000
				f.Size = 0
				if kind == "dependency bytes" {
					n = 8
					f.Size = 8 << 20
				}
				for i := 0; i < n; i++ {
					f.Path = fmt.Sprintf("f%05d", i)
					q.Verify.DependencyManifest.Files = append(q.Verify.DependencyManifest.Files, f)
				}
				rebindV2Test(t, &q)
			}
			if ValidateRequestV2(q) != nil {
				t.Fatal("exact admission bound rejected")
			}
		})
	}
}
func TestV2VerifyEmptyDependencyBundleStillChecksRetainedStore(t *testing.T) {
	q, s, root := fixtureV2Test(t, "c4_node_model_v2")
	q.Verify.DependencyManifest.Files = []dependencybundle.File{}
	rebindV2Test(t, &q)
	empty, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if _, e := dependencybundle.ImportFrozen(context.Background(), root, empty, q.Verify.DependencyManifest); e != nil {
		t.Fatal(e)
	}
	wire := wireV2Test(t, q, s, root)
	count := 0
	if _, e = ReadRequestV2(context.Background(), bytes.NewReader(wire), func(kind uint8, index uint32, f SourceFileV2, r io.Reader) error {
		if kind != 1 {
			t.Fatal("empty dependency frame")
		}
		count++
		return drainV2Test(kind, index, f, r)
	}); e != nil || count != len(s) {
		t.Fatal("empty dependencies", e)
	}
	if WriteRequestV2(context.Background(), io.Discard, q, s, "") == nil {
		t.Fatal("empty dependencies bypassed store validation")
	}
}
