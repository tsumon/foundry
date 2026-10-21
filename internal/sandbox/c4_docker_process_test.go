package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Only this owned test binary is ever executed; no shell or Docker is used.
func TestC4DockerProcessHelper(t *testing.T) {
	mode := os.Getenv("C4_OWNED_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "env":
		b, _ := io.ReadAll(os.Stdin)
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Env   []string
			Input string
		}{os.Environ(), string(b)})
	case "noisy":
		var wg sync.WaitGroup
		for _, w := range []io.Writer{os.Stdout, os.Stderr} {
			wg.Add(1)
			go func(w io.Writer) { defer wg.Done(); _, _ = io.WriteString(w, strings.Repeat("x", 2*1024*1024)) }(w)
		}
		wg.Wait()
	case "nonzero":
		os.Exit(7)
	case "cancel":
		_, _ = fmt.Fprintln(os.Stdout, os.Getpid())
		time.Sleep(30 * time.Second)
	}
	os.Exit(0)
}

func helperC4Command(t *testing.T, mode string) C4Command {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return C4Command{Path: path, Args: []string{"-test.run=^TestC4DockerProcessHelper$"}, Env: []string{"C4_OWNED_HELPER=" + mode}}
}

// Catches inherited environment/stdin and treating a nonzero child exit as a transport failure.
func TestC4DockerProcessEnvironmentAndExit(t *testing.T) {
	t.Setenv("C4_PARENT_SECRET", "must-not-inherit")
	var out, stderr bytes.Buffer
	exit, err := (c4OSProcess{}).Run(context.Background(), helperC4Command(t, "env"), &out, &stderr)
	var got struct {
		Env   []string
		Input string
	}
	if err != nil || exit != 0 || json.Unmarshal(out.Bytes(), &got) != nil || len(got.Env) != 1 || got.Env[0] != "C4_OWNED_HELPER=env" || got.Input != "" {
		t.Fatalf("uncontrolled child: %d %v %q", exit, err, out.String())
	}
	exit, err = (c4OSProcess{}).Run(context.Background(), helperC4Command(t, "nonzero"), io.Discard, io.Discard)
	if err != nil || exit != 7 {
		t.Fatalf("lost numeric exit: %d %v", exit, err)
	}
}

// Catches cancellation being normalized to an ordinary exit and Run returning before reaping.
func TestC4DockerProcessCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	before := time.Now()
	exit, err := (c4OSProcess{}).Run(ctx, helperC4Command(t, "cancel"), &out, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) || exit != -1 || time.Since(before) > 3*time.Second {
		t.Fatalf("cancellation lost: %d %v", exit, err)
	}
	var pid int
	if _, err := fmt.Sscan(out.String(), &pid); err != nil {
		t.Fatal("helper never started", out.String())
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Release()
	if _, err = proc.Wait(); err == nil {
		t.Fatal("Run did not reap owned child")
	}
}

// Catches unbounded retention, failure to drain either stream, and racing writes.
func TestC4DockerProcessBoundedDrain(t *testing.T) {
	out, stderr := &c4DockerCapture{limit: 1024}, &c4DockerCapture{limit: 2048}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exit, err := (c4OSProcess{}).Run(ctx, helperC4Command(t, "noisy"), out, stderr)
	a, at := out.snapshot()
	b, bt := stderr.snapshot()
	if err != nil || exit != 0 || len(a) != 1024 || len(b) != 2048 || !at || !bt {
		t.Fatalf("drain/bounds: %d %v %d %d %v %v", exit, err, len(a), len(b), at, bt)
	}
	shared := &c4DockerCapture{limit: 100}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = shared.Write([]byte(strings.Repeat("y", 100))) }()
	}
	wg.Wait()
	data, truncated := shared.snapshot()
	if len(data) != 100 || !truncated {
		t.Fatal("concurrent capture lost bounds")
	}
}

// Catches ambient PATH/environment fallback or raw OS diagnostics crossing the boundary.
func TestC4DockerProcessRejectsUnsafeRequests(t *testing.T) {
	for _, name := range []string{"relative", "nil env", "capture", "missing", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			c := helperC4Command(t, "env")
			ctx := context.Background()
			switch name {
			case "relative":
				c.Path = "never-look-up-this-binary"
			case "nil env":
				c.Env = nil
			case "capture":
				c.CaptureID = "not-an-executable"
			case "missing":
				c.Path = "/synthetic/nonexistent-owned-test-executable"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			var out bytes.Buffer
			exit, err := (c4OSProcess{}).Run(ctx, c, &out, io.Discard)
			if err == nil || exit != -1 || out.Len() != 0 || strings.Contains(err.Error(), "synthetic/") {
				t.Fatal("unsafe process request", exit, err, out.String())
			}
			if name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("lost pre-cancel")
			}
		})
	}
}
