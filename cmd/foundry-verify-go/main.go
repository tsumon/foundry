// foundry-verify-go is intended only for the separately approved future C4
// image, installed as /opt/foundry/verify-go-v1. It is not a host C4 transport.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/c4verify"
	"io"
	"os"
	"os/exec"
	"time"
)

type goProcess struct{}

func (goProcess) Run(ctx context.Context, r c4verify.Request, stdout, stderr io.Writer) (int, error) {
	c := exec.CommandContext(ctx, r.Path, r.Args...)
	c.Dir = r.Dir
	c.Env = r.Env
	c.Stdout = stdout
	c.Stderr = stderr
	c.WaitDelay = 3 * time.Second
	err := c.Run()
	if err == nil {
		return 0, nil
	}
	var status *exec.ExitError
	if errors.As(err, &status) {
		return status.ExitCode(), nil
	}
	return -1, err
}

func run(ctx context.Context, p c4verify.Process, w io.Writer) int {
	r := c4verify.Run(ctx, p)
	if json.NewEncoder(w).Encode(r) != nil {
		return 2
	}
	switch r.Class {
	case "passed":
		return 0
	case "test_failed":
		return 1
	default:
		return 2
	}
}

func main() {
	// Only fixed scratch directories are created. Repository scripts and command
	// line/environment overrides are never read. Tests inject the process seam.
	for _, dir := range []string{"/foundry-build/cache", "/foundry-build/tmp", "/foundry-build/gopath"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			os.Exit(2)
		}
	}
	os.Exit(run(context.Background(), goProcess{}, os.Stdout))
}
