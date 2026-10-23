//go:build darwin

package vmruntime

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type macProcess struct {
	cmd              *exec.Cmd
	in               io.WriteCloser
	out              io.ReadCloser
	mu               sync.Mutex
	waited           bool
	stderr           *diagnosticBuffer
	exitCode         *int64
	signal           *int
	helper           *helperDiagnostic
	diagnosticStatus string
}

func (p *macProcess) Input() io.WriteCloser { return p.in }
func (p *macProcess) Output() io.ReadCloser { return p.out }
func (p *macProcess) Terminated() bool      { p.mu.Lock(); defer p.mu.Unlock(); return p.waited }
func (p *macProcess) Wait() error {
	e := p.cmd.Wait()
	p.mu.Lock()
	p.waited = p.cmd.ProcessState != nil
	if p.waited {
		if code := p.cmd.ProcessState.ExitCode(); code >= 0 {
			n := int64(code)
			p.exitCode = &n
		}
		if status, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			signal := int(status.Signal())
			p.signal = &signal
		}
	}
	if p.stderr != nil {
		p.helper, p.diagnosticStatus = p.stderr.parse(p.exitCode)
		p.stderr.discard()
	}
	p.mu.Unlock()
	return e
}
func (p *macProcess) diagnostics() nativeFacts {
	p.mu.Lock()
	f := nativeFacts{TerminationConfirmed: p.waited, ExitCode: p.exitCode, Signal: p.signal, AdmissionStage: "running", DiagnosticStatus: p.diagnosticStatus, Helper: p.helper}
	if f.DiagnosticStatus == "" {
		f.DiagnosticStatus = "absent"
	}
	p.mu.Unlock()
	return f
}
func (p *macProcess) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.in.Close()
	if p.waited {
		return nil
	}
	e := p.cmd.Process.Kill()
	if errors.Is(e, os.ErrProcessDone) {
		return nil
	}
	return e
}
func launchNative(ctx context.Context, c command) (nativeProcess, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Env = append([]string(nil), c.Env...)
	cmd.Dir = c.Dir
	// This independent bounded channel accepts only typed failure metadata.
	// It cannot satisfy or alter the authenticated guest serial protocol.
	diagnostics := &diagnosticBuffer{}
	cmd.Stderr = diagnostics
	in, e := cmd.StdinPipe()
	if e != nil {
		return nil, e
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		in.Close()
		return nil, e
	}
	if e := cmd.Start(); e != nil {
		in.Close()
		out.Close()
		return nil, admissionFailure("helper_spawn", e)
	}
	return &macProcess{cmd: cmd, in: in, out: out, stderr: diagnostics}, nil
}
