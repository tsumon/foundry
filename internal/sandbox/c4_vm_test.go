package sandbox

import (
	"context"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/domain"
	"foundry/internal/vmruntime"
	"strings"
	"testing"
	"time"
)

type vmTestSession struct {
	q                      vmruntime.Request
	r                      c4verify.Result
	valid, started, closed bool
	closeErr               error
	startCtx, waitCtx      context.Context
	cancelOnStart          context.CancelFunc
}

func (s *vmTestSession) ID() string { return strings.Repeat("d", 64) }
func (s *vmTestSession) Start(ctx context.Context) error {
	s.startCtx, s.started = ctx, true
	if s.cancelOnStart != nil {
		s.cancelOnStart()
	}
	return nil
}
func (s *vmTestSession) Wait(ctx context.Context) error  { s.waitCtx = ctx; return ctx.Err() }
func (s *vmTestSession) Result() (c4verify.Result, bool) { return s.r, s.valid && s.started }
func (s *vmTestSession) Running() bool                   { return false }
func (s *vmTestSession) Close(context.Context) error {
	if s.closeErr != nil {
		return s.closeErr
	}
	s.closed = true
	return nil
}
func (s *vmTestSession) Matches(q vmruntime.Request) bool { return q == s.q }

type vmTestLifecycle struct {
	*c4VMLifecycle
	created int
}

func (d *vmTestLifecycle) create(context.Context) (string, error) {
	d.created++
	return d.session.ID(), nil
}

// Only allocation is replaced: real VM session adaptation, durable transitions,
// result classification and cleanup are exercised without production provenance.
func TestC4VMLifecycleResultAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name, class                         string
		exit, tests                         int
		valid, truncated, cleanupFail, pass bool
	}{
		{"pass", "passed", 0, 2, true, false, false, true},
		{"failure", "test_failed", 1, 1, true, false, false, false},
		{"zero", "passed", 0, 0, true, false, false, false},
		{"partial", "passed", 0, 2, false, false, false, false},
		{"truncated", "passed", 0, 2, true, true, false, false},
		{"unknown", "invented", 0, 2, true, false, false, false},
		{"unclean", "passed", 0, 2, true, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newC4Harness(t)
			q := vmRequest(h.op, h.candidate)
			s := &vmTestSession{q: q, r: c4verify.Result{Class: tc.class, ExitCode: tc.exit, Tests: tc.tests, Output: "bounded guest evidence", Truncated: tc.truncated}, valid: tc.valid}
			if tc.cleanupFail {
				s.closeErr = errors.New("owned termination unconfirmed")
			}
			d := &vmTestLifecycle{c4VMLifecycle: &c4VMLifecycle{request: q, session: s}}
			o, err := executeC4Lifecycle(context.Background(), h.op, d, h.persist)
			if o.Passed() != tc.pass || (err == nil) != tc.pass || d.created != 1 || !s.started || o.Dispatch == nil || o.Started == nil || o.Result == nil {
				t.Fatalf("unexpected lifecycle: %+v, %v", o, err)
			}
			if tc.name == "failure" && (o.Result.Class != "test_failed" || o.Result.Tests != 1 || o.Result.Output != "bounded guest evidence" || !s.closed) {
				t.Fatalf("lost repair evidence: %+v", o)
			}
			if !tc.cleanupFail && !s.closed {
				t.Fatal("owned session not closed")
			}
			if _, replayErr := executeC4Lifecycle(context.Background(), o, d, h.persist); replayErr == nil || d.created != 1 {
				t.Fatal("consumed operation replayed", replayErr)
			}
		})
	}
}

func TestC4VMLifecycleNeverCleansForeignSession(t *testing.T) {
	s := &vmTestSession{q: vmruntime.Request{OperationID: "original", Owner: "owner"}}
	d := &c4VMLifecycle{request: vmruntime.Request{OperationID: "different", Owner: "owner"}, session: s}
	if err := d.remove(context.Background(), s.ID()); err == nil || s.closed {
		t.Fatal("foreign operation cleaned")
	}
	d.request = s.q
	if err := d.remove(context.Background(), strings.Repeat("e", 64)); err == nil || s.closed {
		t.Fatal("foreign instance cleaned")
	}
}

func TestC4VMLifecyclePersistenceStopsBeforeEffects(t *testing.T) {
	h := newC4Harness(t)
	h.failPersist = "dispatch"
	q := vmRequest(h.op, h.candidate)
	s := &vmTestSession{q: q}
	d := &vmTestLifecycle{c4VMLifecycle: &c4VMLifecycle{request: q, session: s}}
	_, err := executeC4Lifecycle(context.Background(), h.op, d, h.persist)
	if !errors.Is(err, ErrC4Persistence) || d.created != 0 || s.started || s.closed {
		t.Fatal("effects after failed durable dispatch", err)
	}
}

func TestC4VMProductionWrappersRejectUntrustedRuntime(t *testing.T) {
	for _, r := range []*vmruntime.Runtime{nil, {}} {
		if ready, err := CheckC4VMReadiness(context.Background(), domain.C4Profile{}, r); err == nil || ready.Fingerprint != "" {
			t.Fatal("untrusted readiness")
		}
		if _, err := ExecuteC4VM(context.Background(), domain.C4Profile{}, domain.C4Operation{}, r, "", nil); err == nil {
			t.Fatal("untrusted execution")
		}
		if _, err := CleanupC4VM(context.Background(), domain.C4Profile{}, domain.C4Operation{}, r, "", "", nil); err == nil {
			t.Fatal("untrusted cleanup")
		}
	}
}

func (s *vmTestSession) MatchesV2(q vmruntime.RequestV2) bool { return s.Matches(q.Owner) }
func TestVMV2ZeroCaseInfrastructureRetained(t *testing.T) {
	q := vmruntime.Request{OperationID: "op", MissionID: "mission", Owner: "owner", Name: "name"}
	s := &vmTestSession{q: q, r: c4verify.Result{Class: "infrastructure_failed", ExitCode: 1, Tests: 0, Output: "synthetic compiler evidence"}, valid: true, started: true}
	d := &c4VMLifecycle{request: q, requestV2: &vmruntime.RequestV2{Owner: q}, session: s}
	v, err := d.inspect(context.Background(), s.ID())
	if err != nil || v.Verification == nil || v.Verification.Output != "synthetic compiler evidence" {
		t.Fatal("discarded authenticated infrastructure", v, err)
	}
	r := classifyC4(v, nil, nil)
	if r.Class != "infrastructure_failed" || r.Tests != 0 {
		t.Fatal(r)
	}
	s.r.Class = "test_failed"
	v, _ = d.inspect(context.Background(), s.ID())
	r = classifyC4(v, nil, nil)
	if r.Class == "test_failed" {
		t.Fatal("zero cases eligible for repair")
	}
}
func TestVMV2TruncatedFailureNeverRepairs(t *testing.T) {
	q := vmruntime.Request{OperationID: "op", MissionID: "m", Owner: "owner", Name: "n"}
	s := &vmTestSession{q: q, started: true, valid: true, r: c4verify.Result{Class: "test_failed", ExitCode: 1, Tests: 2, Truncated: true, Output: "synthetic truncated evidence"}}
	d := &c4VMLifecycle{request: q, requestV2: &vmruntime.RequestV2{Owner: q}, session: s}
	v, err := d.inspect(context.Background(), s.ID())
	if err != nil || v.Verification == nil {
		t.Fatal(err)
	}
	if r := classifyC4(v, nil, nil); r.Class != "infrastructure_failed" {
		t.Fatal("truncated failure eligible for repair", r)
	}
}

func assertC4ExecutionDeadline(t *testing.T, ctx context.Context, before, after time.Time, seconds int) time.Time {
	t.Helper()
	if ctx == nil {
		t.Fatal("execution context not forwarded")
	}
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Before(before.Add(time.Duration(seconds)*time.Second)) || deadline.After(after.Add(time.Duration(seconds)*time.Second)) {
		t.Fatalf("wrong execution window: deadline=%v want=[%v,%v]", deadline, before.Add(time.Duration(seconds)*time.Second), after.Add(time.Duration(seconds)*time.Second))
	}
	return deadline
}

func TestC4VMLifecycleUsesV2LifetimeForStartAndWaitOnly(t *testing.T) {
	for _, profile := range []string{"", "c4_node_model_v2", "c4_typescript_model_v2", "c4_python_model_v2", "unknown"} {
		t.Run("profile="+profile, func(t *testing.T) {
			for _, parentKind := range []string{"unbounded", "earlier", "cancel-during-start", "already-cancelled"} {
				t.Run(parentKind, func(t *testing.T) {
					h := newC4Harness(t)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					if parentKind == "earlier" {
						var stop context.CancelFunc
						ctx, stop = context.WithTimeout(ctx, time.Minute)
						defer stop()
					}
					if parentKind == "already-cancelled" {
						cancel()
					}
					q := vmRequest(h.op, h.candidate)
					s := &vmTestSession{q: q, valid: true, r: c4verify.Result{Class: "passed", ExitCode: 0, Tests: 2}}
					if parentKind == "cancel-during-start" {
						s.cancelOnStart = cancel
					}
					d := &vmTestLifecycle{c4VMLifecycle: &c4VMLifecycle{request: q, session: s}}
					if profile != "" {
						d.requestV2 = &vmruntime.RequestV2{Owner: q, Profile: domain.C4Profile{SchemaVersion: 2, ID: profile}}
					}
					before := time.Now()
					o, e := executeC4Lifecycle(ctx, h.op, d, h.persist)
					after := time.Now()
					if parentKind == "already-cancelled" {
						if e == nil || d.created != 0 || s.startCtx != nil || s.waitCtx != nil || o.Passed() {
							t.Fatal("cancelled parent authorized effects", o, e)
						}
						return
					}
					if s.startCtx == nil || s.waitCtx != s.startCtx || d.created != 1 || !s.closed || o.CleanupState() != "confirmed" {
						t.Fatal("lost shared execution context or owned cleanup", o, e)
					}
					if parentKind == "earlier" {
						want, _ := ctx.Deadline()
						got, _ := s.startCtx.Deadline()
						if !got.Equal(want) {
							t.Fatal("earlier parent deadline replaced", got, want)
						}
					} else {
						seconds := 120
						if profile != "" && profile != "unknown" {
							seconds = 180
						}
						assertC4ExecutionDeadline(t, s.startCtx, before, after, seconds)
					}
					if parentKind == "cancel-during-start" {
						if e == nil || s.waitCtx.Err() != context.Canceled || o.Result == nil || o.Result.Class != "interrupted" || o.Passed() {
							t.Fatal("parent cancellation became success", o, e)
						}
					} else if e != nil || !o.Passed() {
						t.Fatal("inert lifecycle fixture failed", o, e)
					}
				})
			}
		})
	}
}

func TestC4CommandLifecyclesKeepStartAndWaitAt120Seconds(t *testing.T) {
	t.Run("synthetic", func(t *testing.T) {
		h := newC4Harness(t)
		before := time.Now()
		o, e := h.run()
		after := time.Now()
		if e != nil || !o.Passed() || h.driver.startCtx != h.driver.waitCtx {
			t.Fatal("synthetic lifecycle failed", o, e)
		}
		assertC4ExecutionDeadline(t, h.driver.startCtx, before, after, 120)
	})
	t.Run("docker-process-fixture", func(t *testing.T) {
		h, f, trust := newC4DockerHarness(t)
		before := time.Now()
		o, e := runC4DockerHarness(h, f, trust)
		after := time.Now()
		if e != nil || !o.Passed() {
			t.Fatal("inert Docker lifecycle failed", o, e)
		}
		var deadlines []time.Time
		for i, step := range f.steps {
			if len(step.args) >= 4 && step.args[2] == "container" && (step.args[3] == "start" || step.args[3] == "wait") {
				deadlines = append(deadlines, assertC4ExecutionDeadline(t, f.contexts[i], before, after, 120))
			}
		}
		if len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) {
			t.Fatal("Docker start/wait did not share the unchanged window", deadlines)
		}
	})
}
