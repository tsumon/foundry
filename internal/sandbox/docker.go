package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Runner interface {
	Resolve(context.Context, string) (string, error)
	Run(context.Context, string, string, string, string) (domain.CommandResult, error)
	Reconcile(context.Context) error
}
type Docker struct{ Path, Owner string }

func New(path, owner string) *Docker {
	if path == "" {
		path = "docker"
	}
	return &Docker{Path: path, Owner: owner}
}

type capped struct {
	mu        sync.Mutex
	b         bytes.Buffer
	max       int
	truncated bool
}

func (b *capped) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := b.max - b.b.Len()
	if len(p) > left {
		p = p[:left]
		b.truncated = true
	}
	b.b.Write(p)
	return n, nil
}
func (b *capped) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.b.String()
	if b.truncated {
		s += "\n[output truncated]"
	}
	return s
}
func (d *Docker) command(ctx context.Context, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, d.Path, args...)
	b := &capped{max: 1024 * 1024}
	c.Stdout = b
	c.Stderr = &capped{max: 4096}
	e := c.Run()
	if b.truncated {
		return nil, errors.New("Docker response too large")
	}
	return []byte(b.String()), e
}
func parseImage(b []byte) (string, error) {
	var images []struct {
		ID     string `json:"Id"`
		Config struct {
			Volumes map[string]json.RawMessage `json:"Volumes"`
		} `json:"Config"`
	}
	if e := json.Unmarshal(b, &images); e != nil || len(images) != 1 || !strings.HasPrefix(images[0].ID, "sha256:") {
		return "", errors.New("invalid local image metadata")
	}
	if len(images[0].Config.Volumes) > 0 {
		return "", errors.New("images declaring volumes are not allowed")
	}
	return images[0].ID, nil
}
func (d *Docker) Resolve(ctx context.Context, image string) (string, error) {
	if strings.TrimSpace(image) == "" || strings.HasPrefix(image, "-") {
		return "", errors.New("local image required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, e := d.command(ctx, "image", "inspect", image)
	if e != nil {
		return "", errors.New("Docker/local image unavailable; start Docker and prepare the image explicitly (no automatic pull)")
	}
	return parseImage(b)
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func runArgs(owner, mission, name, candidate, image, command string) ([]string, error) {
	if !identifier.MatchString(owner) || !identifier.MatchString(mission) || !identifier.MatchString(name) || !filepath.IsAbs(candidate) || strings.ContainsAny(candidate, ",\n\r") || !strings.HasPrefix(image, "sha256:") {
		return nil, errors.New("invalid sandbox identity or mount")
	}
	return []string{"run", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--no-healthcheck", "--pids-limit=128", "--memory=512m", "--cpus=1", "--user=65534:65534", "--entrypoint=/bin/sh", "--log-driver=none", "--name", name, "--label", "foundry.owner=" + owner, "--label", "foundry.mission=" + mission, "--mount", "type=bind,src=" + candidate + ",dst=/workspace,readonly", "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=64m,mode=1777", "--workdir=/workspace", image, "-c", command}, nil
}
func (d *Docker) remove(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, e := d.command(ctx, "rm", "-f", name)
	return e
}
func (d *Docker) Run(ctx context.Context, mission, candidate, image, command string) (domain.CommandResult, error) {
	result := domain.CommandResult{Command: command, ExitCode: -1}
	b := make([]byte, 8)
	if _, e := rand.Read(b); e != nil {
		return result, e
	}
	name := "foundry-" + d.Owner + "-" + hex.EncodeToString(b)
	args, e := runArgs(d.Owner, mission, name, candidate, image, command)
	if e != nil {
		return result, e
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, d.Path, args...)
	out := &capped{max: 64 * 1024}
	c.Stdout = out
	c.Stderr = out
	c.Cancel = func() error {
		_ = d.remove(name)
		if c.Process != nil {
			return c.Process.Kill()
		}
		return nil
	}
	c.WaitDelay = 3 * time.Second
	err := c.Run()
	cleanup := d.remove(name)
	result.Output = out.String()
	result.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if cleanup != nil {
		return result, errors.New("sandbox cleanup failed; execution is blocked")
	}
	if err == nil {
		result.ExitCode = 0
		return result, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	return result, errors.New("Docker execution failed")
}
func (d *Docker) Reconcile(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	b, e := d.command(ctx, "ps", "-aq", "--filter", "label=foundry.owner="+d.Owner)
	if e != nil {
		return errors.New("cannot reconcile owned Docker containers")
	}
	for _, id := range strings.Fields(string(b)) {
		if !regexp.MustCompile(`^[0-9a-f]{12,64}$`).MatchString(id) {
			return errors.New("invalid container ID")
		}
		data, e := d.command(ctx, "container", "inspect", id)
		if e != nil {
			return e
		}
		var rows []struct {
			Config struct{ Labels map[string]string }
		}
		if json.Unmarshal(data, &rows) != nil || len(rows) != 1 || rows[0].Config.Labels["foundry.owner"] != d.Owner {
			return errors.New("container ownership mismatch")
		}
		if e := d.remove(id); e != nil {
			return fmt.Errorf("owned container cleanup failed")
		}
	}
	return nil
}
