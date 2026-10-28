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
func superviseV2(ctx context.Context, s phaseStreamsV2, wait func() frameworkreport.Result, cleanup, resources func() error, stdout, stderr, report io.Writer, drainTimeout time.Duration) (o phaseOutcomeV2) {
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
	}
	if e := cleanup(); e == nil {
		o.CleanupConfirmed = true
	} else {
		o.Process.Err = errGuestV2
	}
	if !gotExit {
		timer := time.NewTimer(drainTimeout)
		select {
		case p := <-exit:
			prior := o.Process.Err
			o.Process = p
			if prior != nil {
				o.Process.Err = prior
			}
		case <-timer.C:
			o.Process.Err = errGuestV2
		}
		timer.Stop()
	}
	if e := resources(); e == nil {
		o.ResourcesOK = true
	} else {
		o.Process.Err = errGuestV2
	}

	timer := time.NewTimer(drainTimeout)
	defer timer.Stop()
	for i := 0; i < count; i++ {
		select {
		case d := <-drains:
			if d.err != nil {
				o.Process.Err = errGuestV2
				if d.report {
					o.Process.ReportTruncated = true
				}
			}
		case <-timer.C:
			for _, r := range streams {
				if r != nil {
					_ = r.Close()
				}
			}
			o.Process.Err = errGuestV2
			o.Process.ReportTruncated = s.report != nil
			return o
		}
	}
	return o
}
