package sandbox

import (
	"context"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/domain"
	"foundry/internal/vmruntime"
	"strings"
	"testing"
)

type vmTestSession struct {
	q                      vmruntime.Request
	r                      c4verify.Result
	valid, started, closed bool
	closeErr               error
}

func (s *vmTestSession) ID() string                      { return strings.Repeat("d", 64) }
func (s *vmTestSession) Start(context.Context) error     { s.started = true; return nil }
func (s *vmTestSession) Wait(context.Context) error      { return nil }
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
