package vmruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/c4verify"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testRuntimeV2(t *testing.T, id string) (*Runtime, RequestV2) {
	t.Helper()
	bundle, instances := fixtureBundleV2(t)
	r, e := LoadV2(bundle, instances)
	if e != nil {
		t.Fatal(e)
	}
	wire, files, store := fixtureV2Test(t, id)
	target, e := r.ProbeTargetV2(id)
	if e != nil {
		t.Fatal(e)
	}
	p := wire.Verify.Profile
	p.EmbeddedVM = &target.Identity
	p.Platform = target.Identity.GuestPlatform
	p.V2.RuntimeInventoryDigest = target.RuntimeInventoryDigest
	candidate, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range files {
		path := filepath.Join(candidate, filepath.FromSlash(f.Path))
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, f.Data, os.FileMode(f.Mode)); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(path, os.FileMode(f.Mode)); e != nil {
			t.Fatal(e)
		}
	}
	// Installed-host admission uses platformfs modes (Windows canonicalizes
	// executable bits); the in-memory wire fixture also covers 0755 separately.
	actual, e := snapshot(context.Background(), candidate)
	if e != nil {
		t.Fatal(e)
	}
	candidateDigest, e := SnapshotDigest(actual)
	if e != nil {
		t.Fatal(e)
	}
	return r, RequestV2{Owner: Request{OperationID: "op", MissionID: "mission", Owner: "owner", Name: "name", CandidatePath: candidate, CandidateDigest: candidateDigest}, Profile: p, Inputs: wire.Verify.Inputs, DependencyStoreRoot: store}
}
func fakeLaunchV2(extra, hang bool) func(context.Context, command) (nativeProcess, error) {
	return func(ctx context.Context, c command) (nativeProcess, error) {
		input, hostInput := io.Pipe()
		hostOutput, output := io.Pipe()
		p := &fakeNative{in: hostInput, out: hostOutput, done: make(chan struct{})}
		go func() {
			defer close(p.done)
			defer input.Close()
			defer output.Close()
			io.WriteString(output, ReadyMarkerV2)
			q, e := ReadRequestV2(ctx, input, drainV2Test)
			if e != nil {
				return
			}
			if hang {
				<-ctx.Done()
				return
			}
			res := c4verify.Result{Class: "passed", Tests: 1}
			if q.Action == "probe" {
				res = c4verify.Result{Class: "probe_passed"}
			}
			var b bytes.Buffer
			if WriteResponseV2(&b, q, checksV2Test(), res, phasesV2Test(q)) != nil {
				return
			}
			output.Write(b.Bytes())
			if extra {
				output.Write(b.Bytes())
			}
		}()
		return p, nil
	}
}
func TestRuntimeV2LifecycleImmutableAndPassiveReceipts(t *testing.T) {
	for _, id := range []string{"c4_node_model_v2", "c4_python_model_v2", "c4_typescript_model_v2"} {
		t.Run(id, func(t *testing.T) {
			r, q := testRuntimeV2(t, id)
			r.launch = fakeLaunchV2(false, false)
			s, e := r.CreateV2(context.Background(), q)
			if e != nil {
				t.Fatal(e)
			}
			if !s.MatchesV2(q) || s.Matches(q.Owner) {
				t.Fatal("ownership version mismatch")
			}
			secret := s.v2.wire.Secret
			original := s.v2.wire.Verify.Profile.ProfileDigest()
			q.Profile.V2.RunnerID = "bad"
			q.Profile.EmbeddedVM.ManifestDigest = strings.Repeat("f", 64)
			q.Inputs.TestFiles[0] = "bad"
			q.Inputs.Protected[0].SHA256 = strings.Repeat("f", 64)
			q.Inputs.SourceRoots[0] = "bad"
			if q.Inputs.TypeScript != nil {
				q.Inputs.TypeScript.InputFiles[0] = "bad"
			}
			os.WriteFile(filepath.Join(q.Owner.CandidatePath, "package.json"), []byte("changed after Create"), 0644)
			if s.v2.wire.Verify.Profile.ProfileDigest() != original || s.MatchesV2(q) {
				t.Fatal("caller mutated owned admission")
			}
			if e = s.Start(context.Background()); e != nil {
				t.Fatal(e)
			}
			if e = s.Wait(context.Background()); e != nil {
				t.Fatal(e)
			}
			if result, valid := s.Result(); !valid || result.Class != "passed" {
				t.Fatal("result missing")
			}
			restored, e := LoadV2(r.bundleRoot, r.instanceRoot)
			if e != nil {
				t.Fatal(e)
			}
			if _, ok := restored.Lookup(s.ID()); ok {
				t.Fatal("receipt recreated authority")
			}
			if e = s.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			if s.v2.wire.Secret != "" || s.v2.source != nil {
				t.Fatal("payload retained after cleanup")
			}
			for _, stage := range []string{"allocated", "started", "terminal", "closed"} {
				b, e := os.ReadFile(filepath.Join(s.dir, stage+".json"))
				if e != nil {
					t.Fatal(e)
				}
				for _, private := range []string{secret, q.DependencyStoreRoot, q.Owner.CandidatePath, "changed after Create"} {
					if bytes.Contains(b, []byte(private)) {
						t.Fatal("receipt disclosed private material")
					}
				}
				var m map[string]any
				if json.Unmarshal(b, &m) != nil || m["schema_version"] != float64(2) || m["stage"] != stage || m["operation_id"] != "op" {
					t.Fatal("receipt schema")
				}
			}
		})
	}
}
func TestRuntimeV2SchemaMixingAndProbe(t *testing.T) {
	r, q := testRuntimeV2(t, "c4_node_model_v2")
	r.launch = func(context.Context, command) (nativeProcess, error) { t.Fatal("unexpected launch"); return nil, nil }
	if _, e := r.Create(context.Background(), q.Owner); e == nil {
		t.Fatal("v1 Create on v2")
	}
	if e := r.Probe(context.Background()); e == nil {
		t.Fatal("v1 Probe on v2")
	}
	v1, _ := testRuntime(t)
	if _, e := v1.CreateV2(context.Background(), q); e == nil {
		t.Fatal("v2 Create on v1")
	}
	target, e := r.ProbeTargetV2(q.Profile.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = v1.ProbeV2(context.Background(), target); e == nil {
		t.Fatal("v2 Probe on v1")
	}
	if _, e = r.ProbeTargetV2("unknown"); e == nil {
		t.Fatal("unknown profile")
	}
	// Probe's only input is a target; removing all project/store paths is harmless.
	os.RemoveAll(q.Owner.CandidatePath)
	os.RemoveAll(q.DependencyStoreRoot)
	r.launch = fakeLaunchV2(false, false)
	if e = r.ProbeV2(context.Background(), target); e != nil {
		t.Fatal(e)
	}
}
func TestRuntimeV2RejectsDriftAndLateStoreFailure(t *testing.T) {
	for _, kind := range []string{"owner", "candidate", "profile", "contract", "dependency", "installed", "start", "late-store"} {
		t.Run(kind, func(t *testing.T) {
			r, q := testRuntimeV2(t, "c4_node_model_v2")
			r.launch = fakeLaunchV2(false, false)
			switch kind {
			case "owner":
				q.Owner.Owner = ""
			case "candidate":
				q.Owner.CandidateDigest = strings.Repeat("f", 64)
			case "profile":
				q.Profile.V2.RuntimeInventoryDigest = strings.Repeat("f", 64)
			case "contract":
				q.Inputs.TestFiles[0] = "other"
			case "dependency":
				q.Inputs.Dependencies.TotalBytes++
			case "installed":
				os.WriteFile(filepath.Join(r.bundleRoot, RuntimeInventoryPathV2), []byte("bad"), 0600)
			}
			s, e := r.CreateV2(context.Background(), q)
			if kind != "start" && kind != "late-store" {
				if e == nil {
					t.Fatal("drift accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if kind == "start" {
				os.WriteFile(filepath.Join(r.bundleRoot, "kernel"), []byte("bad"), 0600)
				r.launch = func(context.Context, command) (nativeProcess, error) {
					t.Fatal("launched changed assets")
					return nil, nil
				}
			} else {
				os.RemoveAll(q.DependencyStoreRoot)
			}
			if e = s.Start(context.Background()); e == nil {
				t.Fatal("late drift accepted")
			}
			if _, ok := s.Result(); ok {
				t.Fatal("late drift yielded result")
			}
			if e = s.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestRuntimeV2ExactEOFAndCancellation(t *testing.T) {
	for _, hang := range []bool{false, true} {
		r, q := testRuntimeV2(t, "c4_node_model_v2")
		r.launch = fakeLaunchV2(!hang, hang)
		s, e := r.CreateV2(context.Background(), q)
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		if e = s.Start(ctx); e != nil {
			t.Fatal(e)
		}
		if e = s.Wait(ctx); e == nil {
			t.Fatal("invalid transaction accepted")
		}
		if _, ok := s.Result(); ok {
			t.Fatal("invalid result")
		}
		if e = s.Close(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	r, q := testRuntimeV2(t, "c4_node_model_v2")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := r.CreateV2(ctx, q); !errors.Is(e, context.Canceled) {
		t.Fatal("canceled admission")
	}
}

func TestRuntimeV2CanonicalSnapshotOrder(t *testing.T) {
	r, q := testRuntimeV2(t, "c4_node_model_v2")
	// WalkDir visits src/a.txt before src.txt, while the wire inventory is sorted
	// by complete portable path. Newly added unprotected files are legal.
	p := filepath.Join(q.Owner.CandidatePath, "src.txt")
	if e := os.WriteFile(p, []byte("new"), 0644); e != nil {
		t.Fatal(e)
	}
	os.Chmod(p, 0644)
	files, e := snapshot(context.Background(), q.Owner.CandidatePath)
	if e != nil {
		t.Fatal(e)
	}
	q.Owner.CandidateDigest, e = SnapshotDigest(files)
	if e != nil {
		t.Fatal(e)
	}
	s, e := r.CreateV2(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
}
