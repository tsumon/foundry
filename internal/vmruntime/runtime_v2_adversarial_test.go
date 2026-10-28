package vmruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeV2FailedStartKeepsSecretUntilObservedCleanup(t *testing.T) {
	for _, returnsOwner := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-process", true: "retained-process"}[returnsOwner], func(t *testing.T) {
			r, q := testRuntimeV2(t, "c4_node_model_v2")
			p := &failedStartNative{}
			r.launch = func(context.Context, command) (nativeProcess, error) {
				if returnsOwner {
					return p, errors.New("failed admission")
				}
				return nil, errors.New("failed creation")
			}
			s, e := r.CreateV2(context.Background(), q)
			if e != nil {
				t.Fatal(e)
			}
			secret := s.v2.wire.Secret
			payload := s.v2.source[0].Data
			if s.Start(context.Background()) == nil {
				t.Fatal("failed launch accepted")
			}
			if s.Wait(context.Background()) == nil {
				t.Fatal("failed launch result")
			}
			if returnsOwner {
				if e = s.Close(context.Background()); e == nil {
					t.Fatal("unknown cleanup accepted")
				}
				if !s.Running() || s.v2.wire.Secret != secret || s.v2.source == nil {
					t.Fatal("unknown cleanup lost owner/payload")
				}
				if _, e = os.Stat(filepath.Join(s.dir, "closed.json")); !os.IsNotExist(e) {
					t.Fatal("unknown cleanup has closed receipt")
				}
				p.mu.Lock()
				p.observable = true
				p.mu.Unlock()
			}
			if e = s.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			if s.v2.wire.Secret != "" || s.v2.source != nil {
				t.Fatal("confirmed cleanup retained payload")
			}
			if !bytes.Equal(payload, make([]byte, len(payload))) {
				t.Fatal("source backing memory not erased")
			}
			if _, ok := s.Result(); ok {
				t.Fatal("failed start authorized result")
			}
			if got, ok := r.Lookup(s.ID()); !ok || got != s {
				t.Fatal("exact owner lost")
			}
		})
	}
}
func TestRuntimeV2UnknownSuccessfulProcessTermination(t *testing.T) {
	r, q := testRuntimeV2(t, "c4_node_model_v2")
	r.launch = func(ctx context.Context, c command) (nativeProcess, error) {
		p, e := fakeLaunchV2(false, false)(ctx, c)
		return &unconfirmedNative{p.(*fakeNative)}, e
	}
	s, e := r.CreateV2(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	if s.Wait(context.Background()) == nil {
		t.Fatal("unconfirmed termination accepted")
	}
	if _, ok := s.Result(); ok {
		t.Fatal("unconfirmed result")
	}
	if s.Close(context.Background()) == nil || s.v2.wire.Secret == "" || s.v2.source == nil {
		t.Fatal("unknown cleanup erased owned state")
	}
}
func TestRuntimeV2MatchesEverySemanticFieldNotLocations(t *testing.T) {
	r, q := testRuntimeV2(t, "c4_typescript_model_v2")
	s, e := r.CreateV2(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	other := cloneRequestV2(q)
	other.Owner.CandidatePath = "/absent-candidate"
	other.DependencyStoreRoot = "/absent-store"
	if !s.MatchesV2(other) {
		t.Fatal("location treated as content authority")
	}
	for name, change := range map[string]func(*RequestV2){
		"operation": func(q *RequestV2) { q.Owner.OperationID += "x" }, "mission": func(q *RequestV2) { q.Owner.MissionID += "x" }, "owner": func(q *RequestV2) { q.Owner.Owner += "x" }, "name": func(q *RequestV2) { q.Owner.Name += "x" }, "candidate": func(q *RequestV2) { q.Owner.CandidateDigest = strings.Repeat("f", 64) },
		"profile": func(q *RequestV2) { q.Profile.V2.Language = "javascript" }, "identity": func(q *RequestV2) { q.Profile.EmbeddedVM.RootFSDigest = strings.Repeat("f", 64) }, "inventory": func(q *RequestV2) { q.Profile.V2.RuntimeInventoryDigest = strings.Repeat("f", 64) }, "dependency": func(q *RequestV2) { q.Inputs.Dependencies.ManifestDigest = strings.Repeat("f", 64) },
		"admission": func(q *RequestV2) { q.Inputs.AdmissionDigest = strings.Repeat("f", 64) }, "source-lock": func(q *RequestV2) { q.Inputs.SourceFiles[0].SHA256 = strings.Repeat("f", 64) }, "protected": func(q *RequestV2) { q.Inputs.Protected[0].Mode = 0755 }, "prefixes": func(q *RequestV2) { q.Inputs.ProtectedPrefixes[0] += "x" }, "tests": func(q *RequestV2) { q.Inputs.TestFiles[0] = "tests/other.test.ts" }, "roots": func(q *RequestV2) { q.Inputs.SourceRoots = []string{"src"} }, "compiler": func(q *RequestV2) { q.Inputs.TypeScript.InputFiles[0] = "tests/other.test.ts" },
	} {
		t.Run(name, func(t *testing.T) {
			v := cloneRequestV2(q)
			change(&v)
			if s.MatchesV2(v) {
				t.Fatal("semantic drift matches")
			}
		})
	}
	// A different but internally valid admission is still a different request.
	other = cloneRequestV2(q)
	other.Inputs.AdmissionDigest = strings.Repeat("e", 64)
	other.Inputs, e = other.Inputs.WithDigests()
	if e != nil {
		t.Fatal(e)
	}
	other.Profile.V2.ExecutionContractDigest = other.Inputs.ExecutionContractDigest
	if other.Inputs.ValidateProfileBinding(other.Profile) != nil {
		t.Fatal("fixture not valid")
	}
	if s.MatchesV2(other) {
		t.Fatal("valid different contract accepted")
	}
	v1, q1 := testRuntime(t)
	old, e := v1.Create(context.Background(), q1)
	if e != nil {
		t.Fatal(e)
	}
	defer old.Close(context.Background())
	if old.MatchesV2(q) {
		t.Fatal("v1 session matches v2")
	}
}

// Err is called synchronously at each copy boundary, after full installed load.
type mutationContextV2 struct {
	context.Context
	mutate func()
	done   bool
}

func (c *mutationContextV2) Err() error {
	if !c.done {
		c.done = true
		c.mutate()
	}
	return nil
}
func TestRuntimeV2RejectsSourceAssetMutationDuringStaging(t *testing.T) {
	r, q := testRuntimeV2(t, "c4_node_model_v2")
	s, e := r.CreateV2(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	ctx := &mutationContextV2{Context: context.Background(), mutate: func() {
		if e := os.WriteFile(filepath.Join(r.bundleRoot, "kernel"), []byte("replaced during staging"), 0600); e != nil {
			t.Fatal(e)
		}
	}}
	if _, e = s.prepare(ctx); e == nil {
		t.Fatal("source drift during copy accepted")
	}
}

type recordingInputV2 struct {
	io.WriteCloser
	capture *mutateWriterV2
}

func (w recordingInputV2) Write(b []byte) (int, error) {
	n, e := w.WriteCloser.Write(b)
	w.capture.Write(b[:n])
	return n, e
}

type recordingNativeV2 struct {
	nativeProcess
	input io.WriteCloser
}

func (p recordingNativeV2) Input() io.WriteCloser { return p.input }
func TestRuntimeV2LateTransferFailureEmitsNoTerminal(t *testing.T) {
	r, q := testRuntimeV2(t, "c4_node_model_v2")
	s, e := r.CreateV2(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	good := wireV2Test(t, s.v2.wire, s.v2.source, q.DependencyStoreRoot)
	capture := &mutateWriterV2{at: len(good) - 45, mutate: func() {
		p := filepath.Join(q.DependencyStoreRoot, q.Inputs.Dependencies.BundleDigest, "manifest.json")
		if e := os.WriteFile(p, []byte("late drift"), 0600); e != nil {
			t.Error(e)
		}
	}}
	r.launch = func(ctx context.Context, c command) (nativeProcess, error) {
		p, e := fakeLaunchV2(false, false)(ctx, c)
		return recordingNativeV2{p, recordingInputV2{p.Input(), capture}}, e
	}
	if s.Start(context.Background()) == nil {
		t.Fatal("late store drift accepted")
	}
	if s.Wait(context.Background()) == nil {
		t.Fatal("late store drift yielded result")
	}
	if !capture.done || capture.Len() != len(good)-45 {
		t.Fatal("terminal emitted after late failure")
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestRuntimeV2ProbeRefusesChangedTargetsAndUnknownSchemas(t *testing.T) {
	r, q := testRuntimeV2(t, "c4_node_model_v2")
	r.launch = func(context.Context, command) (nativeProcess, error) { t.Fatal("unexpected launch"); return nil, nil }
	target, _ := r.ProbeTargetV2(q.Profile.ID)
	for _, kind := range []string{"identity", "inventory", "profile", "schema"} {
		v := target
		switch kind {
		case "identity":
			v.Identity.ManifestDigest = strings.Repeat("f", 64)
		case "inventory":
			v.RuntimeInventoryDigest = strings.Repeat("f", 64)
		case "profile":
			v.ProfileID = "unknown"
		case "schema":
			v.Identity = domain.C4VMIdentity{SchemaVersion: 3}
		}
		if r.ProbeV2(context.Background(), v) == nil {
			t.Fatal("invalid probe target accepted")
		}
	}
	os.WriteFile(filepath.Join(r.bundleRoot, RuntimeInventoryPathV2), []byte("bad"), 0600)
	if r.ProbeV2(context.Background(), target) == nil {
		t.Fatal("stale installed inventory accepted")
	}
	entries, _ := os.ReadDir(r.instanceRoot)
	if len(entries) != 0 {
		t.Fatal("rejected metadata allocated session")
	}
	r.identity.SchemaVersion = 3
	if _, e := r.CreateV2(context.Background(), q); e == nil {
		t.Fatal("unknown schema created")
	}
	if _, e := r.Create(context.Background(), q.Owner); e == nil {
		t.Fatal("unknown schema created v1")
	}
}

func TestRuntimeV2RejectsValidInstalledIdentityRotation(t *testing.T) {
	for _, stage := range []string{"create", "start"} {
		t.Run(stage, func(t *testing.T) {
			r, q := testRuntimeV2(t, "c4_node_model_v2")
			r.launch = func(context.Context, command) (nativeProcess, error) {
				t.Fatal("launched rotated identity")
				return nil, nil
			}
			var s *Session
			var e error
			if stage == "start" {
				s, e = r.CreateV2(context.Background(), q)
				if e != nil {
					t.Fatal(e)
				}
			}
			// The replacement is internally consistent and loads as a new identity. It
			// still cannot be used by this already-bound Runtime or Session.
			m := r.manifestV2
			d := r.inventoryV2
			d.Node.Executable.SHA256 = strings.Repeat("e", 64)
			raw, e := d.CanonicalBytes()
			if e != nil {
				t.Fatal(e)
			}
			m.RuntimeInventoryDigest, e = d.Digest()
			if e != nil {
				t.Fatal(e)
			}
			m.Assets = append([]Asset(nil), m.Assets...)
			for i := range m.Assets {
				if m.Assets[i].Path == RuntimeInventoryPathV2 {
					m.Assets[i] = Asset{RuntimeInventoryPathV2, hashV2Test(raw), int64(len(raw))}
				}
			}
			if e = os.WriteFile(filepath.Join(r.bundleRoot, RuntimeInventoryPathV2), raw, 0600); e != nil {
				t.Fatal(e)
			}
			raw, e = json.Marshal(m)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(r.bundleRoot, "manifest.json"), raw, 0600); e != nil {
				t.Fatal(e)
			}
			fresh, e := LoadV2(r.bundleRoot, r.instanceRoot)
			if e != nil || fresh.Identity() == r.Identity() {
				t.Fatal("fixture rotation invalid", e)
			}
			if stage == "create" {
				if _, e = r.CreateV2(context.Background(), q); e == nil {
					t.Fatal("new identity admitted to old runtime")
				}
			} else {
				if s.Start(context.Background()) == nil {
					t.Fatal("new identity started by old session")
				}
				if e = s.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
