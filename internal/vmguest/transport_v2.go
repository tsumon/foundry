package vmguest

import (
	"context"
	"errors"
	"io"
	"os"
	"time"
)

const transportShutdownV2 = time.Second

type receiveObservationV2 struct {
	n   int
	err error
}
type receiveMonitorV2 struct {
	input    io.ReadCloser
	done     chan receiveObservationV2
	joined   chan struct{}
	observed *receiveObservationV2
	stopped  bool
	stopErr  error
}

func (m *receiveMonitorV2) start(cancel context.CancelFunc) {
	m.done = make(chan receiveObservationV2, 1)
	m.joined = make(chan struct{})
	go func() {
		defer close(m.joined)
		var b [1]byte
		n, e := m.input.Read(b[:])
		m.done <- receiveObservationV2{n, e}
		if n != 0 || e != io.EOF {
			cancel()
		}
	}()
}
func (m *receiveMonitorV2) poll() bool {
	if m.observed == nil && m.done != nil {
		select {
		case v := <-m.done:
			m.observed = &v
		default:
		}
	}
	return m.observed != nil
}
func (m *receiveMonitorV2) violated() bool {
	return m.poll() && (m.observed.n != 0 || m.observed.err != io.EOF)
}

// shutdown is the receive-side cutoff. Own Close is required, and the observer
// must be joined before the last authenticated-frame byte may be published.
// Only a standard close wakeup from this owned close is expected; pending data,
// unrelated errors, unsuccessful close and unjoined reads all reject commit.
func (m *receiveMonitorV2) shutdown() error {
	if m.stopped {
		return m.stopErr
	}
	m.stopped = true
	pendingFailure := m.violated()
	timer := time.NewTimer(transportShutdownV2)
	defer timer.Stop()
	closed := make(chan error, 1)
	go func() { closed <- m.input.Close() }()
	select {
	case e := <-closed:
		if e != nil {
			m.stopErr = errGuestV2
		}
	case <-timer.C:
		m.stopErr = errGuestV2
		return m.stopErr
	}
	if m.joined != nil {
		select {
		case <-m.joined:
		case <-timer.C:
			m.stopErr = errGuestV2
			return m.stopErr
		}
		if !m.poll() {
			m.stopErr = errGuestV2
			return m.stopErr
		}
	}
	if pendingFailure {
		m.stopErr = errGuestV2
	}
	if m.observed != nil {
		v := m.observed
		if v.n != 0 || (v.err != io.EOF && !errors.Is(v.err, os.ErrClosed) && !errors.Is(v.err, io.ErrClosedPipe)) {
			m.stopErr = errGuestV2
		}
	}
	return m.stopErr
}
func closeOutputV2(output io.Closer) error {
	done := make(chan error, 1)
	go func() { done <- output.Close() }()
	timer := time.NewTimer(transportShutdownV2)
	defer timer.Stop()
	select {
	case e := <-done:
		return e
	case <-timer.C:
		return errGuestV2
	}
}
