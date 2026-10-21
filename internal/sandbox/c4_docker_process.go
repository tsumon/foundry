package sandbox

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type c4DockerProcess interface {
	Run(context.Context, C4Command, io.Writer, io.Writer) (int, error)
}
type c4OSProcess struct{}

// Captures always acknowledge the entire write so exec keeps draining even
// after retention is full. Separate stdout/stderr retain framing boundaries.
type c4DockerCapture struct {
	mu        sync.Mutex
	limit     int
	data      []byte
	truncated bool
}

func (b *c4DockerCapture) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	keep := min(n, max(0, b.limit-len(b.data)))
	b.data = append(b.data, p[:keep]...)
	b.truncated = b.truncated || keep < n
	return n, nil
}
func (b *c4DockerCapture) snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...), b.truncated
}

var errC4DockerProcess = errors.New("C4 Docker process failed")

// A normal nonzero child exit is data, not an exec transport failure. In
// particular attach can exit 1 for a failing test. Cancellation is never data.
func (c4OSProcess) Run(ctx context.Context, c C4Command, stdout, stderr io.Writer) (int, error) {
	if !filepath.IsAbs(c.Path) || filepath.Clean(c.Path) != c.Path || c.Env == nil || c.CaptureID != "" {
		return -1, errC4DockerProcess
	}
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Env = append([]string{}, c.Env...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		return exit.ExitCode(), nil
	}
	return -1, errC4DockerProcess
}
