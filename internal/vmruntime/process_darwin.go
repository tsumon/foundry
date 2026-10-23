//go:build darwin

package vmruntime

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
)

type macProcess struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    io.ReadCloser
	mu     sync.Mutex
	waited bool
}

func (p *macProcess) Input() io.WriteCloser { return p.in }
func (p *macProcess) Output() io.ReadCloser { return p.out }
func (p *macProcess) Terminated() bool      { p.mu.Lock(); defer p.mu.Unlock(); return p.waited }
func (p *macProcess) Wait() error {
	e := p.cmd.Wait()
	p.mu.Lock()
	p.waited = p.cmd.ProcessState != nil
	p.mu.Unlock()
	return e
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
	// stderr is deliberately not part of the authority channel, nor retained as
	// uncontrolled diagnostics. Stdout is only the private guest serial stream.
	cmd.Stderr = io.Discard
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
		return nil, e
	}
	return &macProcess{cmd: cmd, in: in, out: out}, nil
}
