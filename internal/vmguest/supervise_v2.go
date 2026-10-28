package vmguest

import (
	"context"
	"foundry/internal/frameworkreport"
	"io"
	"strconv"
	"strings"
	"time"
)

func checkCounterV2(b []byte, key string) error {
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			return errGuestV2
		}
		for _, c := range f[1] {
			if c < '0' || c > '9' {
				return errGuestV2
			}
		}
		n, e := strconv.ParseUint(f[1], 10, 64)
		if e != nil {
			return errGuestV2
		}
		if f[0] == key {
			if found || n != 0 {
				return errGuestV2
			}
			found = true
		}
	}
	if !found {
		return errGuestV2
	}
	return nil
}

type phaseStreamsV2 struct{ stdout, stderr, report io.ReadCloser }
type drainResultV2 struct {
	err    error
	report bool
}

// Wait observes only the direct process, never the pipe drains. Its stdout and
// stderr must be native files, so Cmd.Wait has no implicit copier to wait for.
// Every termination path retires the group before waiting for bounded EOF.
func superviseV2(ctx context.Context, s phaseStreamsV2, wait func() frameworkreport.Result, cleanup, resources func() error, stdout, stderr, report io.Writer, finalizationTimeout time.Duration) (o phaseOutcomeV2) {
	defer func() {
		if ctx.Err() == context.DeadlineExceeded {
			o.Process.DeadlineExceeded = true
			o.Process.Canceled = false
		} else if ctx.Err() != nil {
			o.Process.Canceled = true
		}
	}()
	exit := make(chan frameworkreport.Result, 1)
	go func() { exit <- wait() }()
	drains := make(chan drainResultV2, 3)
	invalid := make(chan struct{}, 1)
	streams := []io.ReadCloser{s.stdout, s.stderr, s.report}
	writers := []io.Writer{stdout, stderr, report}
	count := 0
	for i, r := range streams {
		if r == nil {
			continue
		}
		count++
		go func(r io.ReadCloser, w io.Writer, isReport bool) {
			defer r.Close()
			buf := make([]byte, 32<<10)
			var failure error
			for {
				n, e := r.Read(buf)
				if n > 0 && failure == nil {
					written, we := w.Write(buf[:n])
					if we != nil || written != n {
						failure = errGuestV2
						select {
						case invalid <- struct{}{}:
						default:
						}
					}
				}
				if e != nil {
					if e != io.EOF {
						failure = errGuestV2
					}
					drains <- drainResultV2{failure, isReport}
					return
				}
				if n == 0 {
					failure = errGuestV2
					select {
					case invalid <- struct{}{}:
					default:
					}
					drains <- drainResultV2{failure, isReport}
					return
				}
			}
		}(r, writers[i], i == 2)
	}
	gotExit := false
	select {
	case o.Process = <-exit:
		gotExit = true
	case <-ctx.Done():
	case <-invalid:
		o.Process.Err = errGuestV2
		o.FailureStage = drainFailedV2
	}
	// The expired work context still permits one bounded exact-group cleanup.
	// Every finalization stage consumes this same deadline, never a fresh timer.
	deadline := time.Now().Add(finalizationTimeout)
	timer := time.NewTimer(finalizationTimeout)
	defer timer.Stop()
	fail := func(stage failureStageV2) {
		// An exhausted finalization boundary must remain visible even when the
		// child or an earlier returning control already reported a failure.
		switch stage {
		case cleanupDeadlineV2, waitDeadlineV2, resourcesDeadlineV2, drainDeadlineV2:
			o.FailureStage = stage
		default:
			if o.FailureStage == "" {
				o.FailureStage = stage
			}
		}
		if o.Process.Err == nil {
			o.Process.Err = errGuestV2
		}
	}
	// Close cannot hold the finalization owner beyond its deadline. Native
	// callers supply owned pipe handles; late close/drain results grant no facts.
	defer func() {
		for _, r := range streams {
			if r != nil {
				go r.Close()
			}
		}
	}()
	control := func(fn func() error, failure, timeout failureStageV2) (bool, bool) {
		if !time.Now().Before(deadline) {
			fail(timeout)
			return false, true
		}
		done := make(chan error, 1)
		go func() { done <- fn() }()
		select {
		case e := <-done:
			// A ready result cannot win a select against an already-expired timer.
			if !time.Now().Before(deadline) {
				fail(timeout)
				return false, true
			}
			if e != nil {
				fail(failure)
				return false, false
			}
			return true, false
		case <-timer.C:
			fail(timeout)
			return false, true
		}
	}
	var expired bool
	if o.CleanupConfirmed, expired = control(cleanup, cleanupFailedV2, cleanupDeadlineV2); expired {
		o.Process.ReportTruncated = s.report != nil
		return o
	}
	if !gotExit {
		select {
		case p := <-exit:
			if !time.Now().Before(deadline) {
				fail(waitDeadlineV2)
				o.Process.ReportTruncated = s.report != nil
				return o
			}
			prior := o.Process.Err
			o.Process = p
			if prior != nil {
				o.Process.Err = prior
			}
		case <-timer.C:
			fail(waitDeadlineV2)
			o.Process.ReportTruncated = s.report != nil
			return o
		}
	}
	if o.Process.Err != nil && o.FailureStage == "" {
		o.FailureStage = waitFailedV2
	}
	if o.ResourcesOK, expired = control(resources, resourcesFailedV2, resourcesDeadlineV2); expired {
		o.Process.ReportTruncated = s.report != nil
		return o
	}
	for i := 0; i < count; i++ {
		select {
		case d := <-drains:
			if !time.Now().Before(deadline) {
				fail(drainDeadlineV2)
				o.Process.ReportTruncated = s.report != nil
				return o
			}
			if d.err != nil {
				fail(drainFailedV2)
				if d.report {
					o.Process.ReportTruncated = true
				}
			}
		case <-timer.C:
			fail(drainDeadlineV2)
			o.Process.ReportTruncated = s.report != nil
			return o
		}
	}
	return o
}

// A failed Start owns no child exit or streams, but still owns its exact group.
func finalizeStartFailureV2(ctx context.Context, startErr error, cleanup, resources func() error) phaseOutcomeV2 {
	o := superviseV2(ctx, phaseStreamsV2{}, func() frameworkreport.Result {
		return frameworkreport.Result{Err: startErr}
	}, cleanup, resources, io.Discard, io.Discard, io.Discard, time.Second)
	// A canceled context may win before the inert wait result is observed.
	// Start's known error remains authoritative; no child exit is invented.
	o.Process.Err = startErr
	return o
}
