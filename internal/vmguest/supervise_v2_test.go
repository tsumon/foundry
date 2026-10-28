package vmguest

import (
	"context"
	"errors"
	"foundry/internal/frameworkreport"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestV2ResourceCountersRequiredAndNumeric(t *testing.T) {
	for _, b := range []string{"", "other 0\n", "oom_kill nope\n", "oom_kill -1\n", "oom_kill 0\noom_kill 0\n", "oom_kill 1\n", "oom_kill 18446744073709551616\n"} {
		if checkCounterV2([]byte(b), "oom_kill") == nil {
			t.Fatal("bad counter", b)
		}
	}
	if checkCounterV2([]byte("low 0\noom_kill 0\n"), "oom_kill") != nil {
		t.Fatal("good counter")
	}
}
func TestV2DirectExitPrecedesHeldPipeAndCleanup(t *testing.T) {
	out, w := io.Pipe()
	var cleaned atomic.Bool
	o := superviseV2(context.Background(), phaseStreamsV2{stdout: out}, func() frameworkreport.Result { return frameworkreport.Result{Exited: true} }, func() error { cleaned.Store(true); return w.Close() }, func() error { return nil }, io.Discard, io.Discard, io.Discard, 100*time.Millisecond)
	if !o.CleanupConfirmed || !o.ResourcesOK || o.Process.Err != nil || !o.Process.Exited || !cleaned.Load() {
		t.Fatal(o)
	}
}
func TestV2ParserOverflowKillsGroupAndBoundedDrain(t *testing.T) {
	report, w := io.Pipe()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.WriteString(w, strings.Repeat("x", 4096)); _ = w.Close() }()
	var cleanup atomic.Bool
	p := frameworkreport.New(frameworkreport.NodeTestV2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	exited := make(chan struct{})
	o := superviseV2(ctx, phaseStreamsV2{report: report}, func() frameworkreport.Result { <-exited; return frameworkreport.Result{Exited: true, ExitCode: 1} }, func() error { cleanup.Store(true); close(exited); return w.Close() }, func() error { return nil }, io.Discard, io.Discard, p, 100*time.Millisecond)
	<-done
	if !cleanup.Load() || o.Process.Err == nil || !o.CleanupConfirmed {
		t.Fatal(o)
	}
}
func TestV2UnknownCleanupResourcesOrDrainCannotPass(t *testing.T) {
	for _, bad := range []string{"cleanup", "resources", "drain", "deadline"} {
		t.Run(bad, func(t *testing.T) {
			r, w := io.Pipe()
			defer w.Close()
			ctx, cancel := context.WithCancel(context.Background())
			if bad == "deadline" {
				var c context.CancelFunc
				ctx, c = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer c()
			}
			defer cancel()
			exit := make(chan struct{})
			if bad != "deadline" {
				close(exit)
			}
			o := superviseV2(ctx, phaseStreamsV2{stdout: r}, func() frameworkreport.Result { <-exit; return frameworkreport.Result{Exited: true} }, func() error {
				if bad == "deadline" {
					close(exit)
				}
				if bad != "drain" {
					_ = w.Close()
				}
				if bad == "cleanup" {
					return errors.New("unknown")
				}
				return nil
			}, func() error {
				if bad == "resources" {
					return errors.New("missing")
				}
				return nil
			}, io.Discard, io.Discard, io.Discard, 10*time.Millisecond)
			if bad == "deadline" {
				if !o.Process.DeadlineExceeded || o.Process.Canceled {
					t.Fatal(o)
				}
			} else if o.Process.Err == nil {
				t.Fatal("failure passed", o)
			}
		})
	}
}
func TestV2ReportGateSealsBeforeVerdict(t *testing.T) {
	p := frameworkreport.New(frameworkreport.NodeTestV2)
	g := &reportGateV2{parser: p}
	if _, e := io.WriteString(g, oneReportV2); e != nil {
		t.Fatal(e)
	}
	v := g.finish(frameworkreport.Result{Exited: true})
	if v.Class != "passed" {
		t.Fatal(v)
	}
	if _, e := g.Write([]byte("late drain")); e == nil {
		t.Fatal("late drain could mutate finished parser")
	}
	if p.Finish(frameworkreport.Result{Exited: true}).Class != "passed" {
		t.Fatal("late writer reached parser")
	}
}
func TestV2DeadlineDuringDrainIsObserved(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	o := superviseV2(ctx, phaseStreamsV2{stdout: r}, func() frameworkreport.Result { return frameworkreport.Result{Exited: true} }, func() error { return nil }, func() error { return nil }, io.Discard, io.Discard, io.Discard, 20*time.Millisecond)
	if !o.Process.DeadlineExceeded || o.Process.Canceled {
		t.Fatal("drain deadline hidden", o)
	}
}
