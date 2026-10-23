package vmruntime

import (
	"context"
	"errors"
	"sync"
)

// The native-call seam is private. Values are retained OS handles, never PIDs.
// It permits deterministic failure tests of the Windows suspended-start path
// without manufacturing production runtime provenance or VM evidence.
type ownedProcessCalls struct {
	assign           func(job, process uintptr) error
	resume           func(thread uintptr) error
	terminateProcess func(uintptr) error
	terminateJob     func(uintptr) error
	wait             func(handle uintptr, millis uint32) (bool, error)
	exitCode         func(uintptr) (uint32, error)
	close            func(uintptr) error
}
type ownedProcess struct {
	mu                   sync.Mutex
	waitGate             chan struct{}
	process, job         uintptr
	assigned, terminated bool
	calls                ownedProcessCalls
}

func (p *ownedProcess) admit(ctx context.Context, thread uintptr) error {
	if e := p.calls.assign(p.job, p.process); e != nil {
		return e
	}
	p.assigned = true
	if e := ctx.Err(); e != nil {
		return e
	}
	return p.calls.resume(thread)
}
func (p *ownedProcess) stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.terminated {
		return nil
	}
	// Assignment may have failed. Closing or terminating an empty Job cannot
	// terminate that suspended child; only its exact retained process can.
	if !p.assigned {
		return p.calls.terminateProcess(p.process)
	}
	return p.calls.terminateJob(p.job)
}
func (p *ownedProcess) terminatedState() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.terminated }
func (p *ownedProcess) waitForTermination(ctx context.Context) error {
	p.mu.Lock()
	if p.waitGate == nil {
		p.waitGate = make(chan struct{}, 1)
	}
	gate := p.waitGate
	p.mu.Unlock()
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-gate }()
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		p.mu.Lock()
		if p.terminated {
			p.mu.Unlock()
			return nil
		}
		handle := p.process
		p.mu.Unlock()
		signaled, e := p.calls.wait(handle, 100)
		if e != nil {
			return errors.New("native process termination observation failed")
		}
		if !signaled {
			continue
		}
		p.mu.Lock()
		code, exitErr := p.calls.exitCode(p.process)
		// A signaled process handle is positive termination evidence even when
		// the exit code is nonzero or cannot be queried. Only now release handles.
		p.terminated = true
		processErr := p.calls.close(p.process)
		jobErr := p.calls.close(p.job)
		p.process, p.job = 0, 0
		p.mu.Unlock()
		if exitErr != nil || processErr != nil || jobErr != nil || code != 0 {
			return errors.New("native process exited unsuccessfully")
		}
		return nil
	}
}
