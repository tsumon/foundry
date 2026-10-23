package vmruntime

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type failedStartNative struct {
	mu          sync.Mutex
	terminated  bool
	stops       int
	stopFailure bool
	observable  bool
}

func (p *failedStartNative) Input() io.WriteCloser { return discardCloser{} }
func (p *failedStartNative) Output() io.ReadCloser { return io.NopCloser(emptyReader{}) }
func (p *failedStartNative) Wait() error           { return errors.New("termination unconfirmed") }
func (p *failedStartNative) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stops++
	if p.stopFailure {
		return errors.New("terminate denied")
	}
	return nil
}
func (p *failedStartNative) Terminated() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.terminated }
func (p *failedStartNative) observeTermination(context.Context) error {
	p.mu.Lock()
	if p.observable {
		p.terminated = true
	}
	p.mu.Unlock()
	if !p.Terminated() {
		return errors.New("termination unconfirmed")
	}
	return nil
}

type discardCloser struct{}

func (discardCloser) Write(b []byte) (int, error) { return len(b), nil }
func (discardCloser) Close() error                { return nil }

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

func TestFailedStartRetainsExactOwnerUntilConfirmedCleanup(t *testing.T) {
	r, q := testRuntime(t)
	p := &failedStartNative{}
	r.launch = func(context.Context, command) (nativeProcess, error) { return p, errors.New("assignment failed") }
	s, e := r.Create(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	if s.Start(context.Background()) == nil {
		t.Fatal("failed native launch accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = s.Close(ctx); e == nil {
		t.Fatal("failed-start ownership lost: uncertain cleanup reported success")
	}
	if _, e = os.Stat(filepath.Join(s.dir, "closed.json")); !os.IsNotExist(e) {
		t.Fatal("uncertain cleanup persisted closed receipt")
	}
	if !s.Running() {
		t.Fatal("unconfirmed retained native process reported stopped")
	}
	p.mu.Lock()
	p.terminated = true
	p.mu.Unlock()
	if e = s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	if got, ok := r.Lookup(s.ID()); !ok || got != s {
		t.Fatal("exact owner not retained")
	}
	if _, ok := s.Result(); ok {
		t.Fatal("failed start became evidence")
	}
}

func TestCleanupObservesExitEvenAfterTerminationRequestFails(t *testing.T) {
	r, q := testRuntime(t)
	p := &failedStartNative{stopFailure: true, observable: true}
	r.launch = func(context.Context, command) (nativeProcess, error) { return p, errors.New("resume failed") }
	s, e := r.Create(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	if s.Start(context.Background()) == nil {
		t.Fatal("failed start accepted")
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatalf("positive signaled handle could not confirm cleanup: %v", e)
	}
}

func TestCloseConsumesSessionBeforeReceiptFailure(t *testing.T) {
	r, q := testRuntime(t)
	launched := false
	r.launch = func(context.Context, command) (nativeProcess, error) {
		launched = true
		return nil, errors.New("must not run")
	}
	s, e := r.Create(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(s.dir, "closed.json"), []byte("occupied"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(context.Background()); e == nil {
		t.Fatal("receipt collision accepted")
	}
	if e = s.Start(context.Background()); e == nil || launched {
		t.Fatal("Close reservation did not exclude future Start")
	}
	os.Remove(filepath.Join(s.dir, "closed.json"))
	if e = s.Close(context.Background()); e != nil {
		t.Fatal("cleanup persistence retry failed", e)
	}
}

func TestPrivateInstanceDirectoryIsExclusive(t *testing.T) {
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	p := filepath.Join(parent, "instance")
	if e := createPrivateDirectory(p); e != nil {
		t.Fatal(e)
	}
	if e := createPrivateDirectory(p); e == nil {
		t.Fatal("existing private instance reused")
	}
	info, e := os.Stat(p)
	if e != nil || !info.IsDir() {
		t.Fatal("directory missing")
	}
}

func TestCloseWaitsForInFlightLaunchAndExcludesRestart(t *testing.T) {
	r, q := testRuntime(t)
	entered, release := make(chan struct{}), make(chan struct{})
	r.launch = func(ctx context.Context, _ command) (nativeProcess, error) {
		close(entered)
		<-release
		return nil, ctx.Err()
	}
	s, e := r.Create(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	startDone := make(chan error, 1)
	go func() { startDone <- s.Start(context.Background()) }()
	<-entered
	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close(context.Background()) }()
	deadline := time.After(time.Second)
	for {
		s.mu.Lock()
		closing := s.closing
		s.mu.Unlock()
		if closing {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Close did not reserve session")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case e := <-closeDone:
		t.Fatalf("cleanup completed before pending launch resolved: %v", e)
	default:
	}
	close(release)
	if e := <-startDone; e == nil {
		t.Fatal("canceled launch accepted")
	}
	if e := <-closeDone; e != nil {
		t.Fatal(e)
	}
	if s.Start(context.Background()) == nil {
		t.Fatal("closed session restarted")
	}
}
