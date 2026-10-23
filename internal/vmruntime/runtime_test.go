package vmruntime

import (
	"bytes"
	"context"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/workspace"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeNative struct {
	in   *io.PipeWriter
	out  *io.PipeReader
	done chan struct{}
	stop sync.Once
}

type unconfirmedNative struct{ *fakeNative }

func (p *unconfirmedNative) Terminated() bool { return false }
func TestCleanupRequiresObservedNativeTermination(t *testing.T) {
	r, q := testRuntime(t)
	r.launch = func(ctx context.Context, c command) (nativeProcess, error) {
		p, e := fakeLaunch(false, false)(ctx, c)
		return &unconfirmedNative{p.(*fakeNative)}, e
	}
	s, e := r.Create(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"LICENSE", "foundry-vm-guest"} {
		if _, e := os.Stat(filepath.Join(s.dir, "assets", name)); !os.IsNotExist(e) {
			t.Fatal("non-boot inventory unnecessarily staged")
		}
	}
	s.Wait(context.Background())
	if e = s.Close(context.Background()); e == nil {
		t.Fatal("cleanup confirmed without native termination")
	}
}

func (p *fakeNative) Input() io.WriteCloser { return p.in }
func (p *fakeNative) Output() io.ReadCloser { return p.out }
func (p *fakeNative) Terminated() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
func (p *fakeNative) Wait() error { <-p.done; return nil }
func (p *fakeNative) Stop() error { p.stop.Do(func() { p.in.Close(); p.out.Close() }); return nil }
func testRuntime(t *testing.T) (*Runtime, Request) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("host bundle fixture")
	}
	bundle, instances := fixtureBundle(t)
	r, e := Load(bundle, instances)
	if e != nil {
		t.Fatal(e)
	}
	candidate, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for name, content := range map[string]string{"go.mod": "module example\n", "x_test.go": "package example\n"} {
		path := filepath.Join(candidate, name)
		if e := os.WriteFile(path, []byte(content), 0644); e != nil {
			t.Fatal(e)
		}
		// Snapshot admission uses exact modes, independent of the inherited umask.
		if e := os.Chmod(path, 0644); e != nil {
			t.Fatal(e)
		}
	}
	d, e := workspace.Digest(candidate)
	if e != nil {
		t.Fatal(e)
	}
	r.production = false
	return r, Request{OperationID: "op", MissionID: "mission", Owner: "owner", Name: "name", CandidatePath: candidate, CandidateDigest: d}
}
func fakeLaunch(extra bool, hang bool) func(context.Context, command) (nativeProcess, error) {
	return func(ctx context.Context, c command) (nativeProcess, error) {
		input, hostInput := io.Pipe()
		hostOutput, output := io.Pipe()
		p := &fakeNative{in: hostInput, out: hostOutput, done: make(chan struct{})}
		go func() {
			defer close(p.done)
			defer input.Close()
			defer output.Close()
			io.WriteString(output, ReadyMarker)
			q, e := ReadRequest(input)
			if e != nil {
				return
			}
			if hang {
				<-ctx.Done()
				return
			}
			var b bytes.Buffer
			WriteResponse(&b, q, goodChecks(), c4verify.Result{Class: "passed", Tests: 1})
			output.Write(b.Bytes())
			if extra {
				output.Write(b.Bytes())
			}
		}()
		return p, nil
	}
}
func TestLifecycleExactSnapshotAndOwnedCleanup(t *testing.T) {
	r, q := testRuntime(t)
	r.launch = fakeLaunch(false, false)
	if r.Trusted() {
		t.Fatal("test seam has production provenance")
	}
	s, e := r.Create(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	if len(s.ID()) != 64 || !s.Matches(q) {
		t.Fatal("ownership not bound")
	}
	os.WriteFile(filepath.Join(q.CandidatePath, "go.mod"), []byte("tampered"), 0644)
	if e = s.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	result, ok := s.Result()
	if !ok || result.Class != "passed" || result.Tests != 1 {
		t.Fatalf("bad result: %+v %v", result, ok)
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if s.Running() {
		t.Fatal("still running")
	}
	if _, ok := r.Lookup(strings.Repeat("0", 64)); ok {
		t.Fatal("invented session recovered")
	}
	restored, e := Load(r.bundleRoot, r.instanceRoot)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := restored.Lookup(s.ID()); ok {
		t.Fatal("receipt reconstructed process authority")
	}
}
func TestLifecycleDuplicateResultAndCancellation(t *testing.T) {
	for _, hang := range []bool{false, true} {
		t.Run(map[bool]string{false: "duplicate", true: "cancel"}[hang], func(t *testing.T) {
			r, q := testRuntime(t)
			r.launch = fakeLaunch(!hang, hang)
			s, e := r.Create(context.Background(), q)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			if e = s.Start(ctx); e != nil {
				t.Fatal(e)
			}
			e = s.Wait(ctx)
			if e == nil {
				t.Fatal("invalid or interrupted result accepted")
			}
			if _, ok := s.Result(); ok {
				t.Fatal("partial result became evidence")
			}
			closeCtx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			if e = s.Close(closeCtx); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestCreateRejectsDriftAndCancelledAdmission(t *testing.T) {
	r, q := testRuntime(t)
	q.CandidateDigest = strings.Repeat("0", 64)
	if _, e := r.Create(context.Background(), q); e == nil {
		t.Fatal("digest drift accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := r.Create(ctx, q); !errors.Is(e, context.Canceled) {
		t.Fatalf("wrong cancellation: %v", e)
	}
}
