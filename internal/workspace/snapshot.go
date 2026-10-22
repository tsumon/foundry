package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/gitexec"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const MaxSnapshotBytes = 10 * 1024 * 1024

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > b.limit {
		b.overflow = true
		return 0, errors.New("command output exceeds bound")
	}
	return b.buffer.Write(p)
}
func gitCommand(ctx context.Context, dir string, limit int, args ...string) ([]byte, error) {
	b, _, e := gitCommandOutput(ctx, dir, limit, args...)
	return b, e
}
func gitCommandOutput(ctx context.Context, dir string, limit int, args ...string) ([]byte, []byte, error) {
	config, err := gitexec.New()
	if err != nil {
		return nil, nil, err
	}
	defer config.Close()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	prefix := []string{
		"--no-pager", "--no-optional-locks", "--no-replace-objects",
		"-c", "core.hooksPath=" + config.Hooks, "-c", "core.fsmonitor=false",
		"-c", "diff.external=", "-c", "core.quotePath=false",
		"-c", "protocol.allow=never", "-c", "maintenance.auto=false", "-c", "gc.auto=0",
	}
	c := exec.CommandContext(ctx, config.Path, append(prefix, args...)...)
	c.Dir = dir
	c.Env = config.Environment
	c.WaitDelay = 100 * time.Millisecond
	b := &limitedBuffer{limit: limit}
	c.Stdout = b
	stderr := &limitedBuffer{limit: 4096}
	c.Stderr = stderr
	e := c.Run()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	// os/exec can prioritize an ExitError over a pipe-copy error. Do not let
	// diff's ordinary exit 1 disguise a truncated stdout or stderr stream.
	if b.overflow || stderr.overflow {
		return nil, nil, errors.New("command output exceeds bound")
	}
	return b.buffer.Bytes(), stderr.buffer.Bytes(), e
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func Snapshot(ctx context.Context, repo, dir string, scope []string) (string, []domain.FileMeta, error) {
	if err := CheckLocation(ctx, repo, dir); err != nil {
		return "", nil, err
	}
	if !filepath.IsAbs(repo) {
		return "", nil, errors.New("absolute repository path required")
	}
	if err := ValidateRules(scope); err != nil {
		return "", nil, err
	}
	resolved, e := filepath.EvalSymlinks(repo)
	if e != nil {
		return "", nil, errors.New("repository path cannot be resolved")
	}
	repo = resolved
	b, e := gitCommand(ctx, repo, 128, "rev-parse", "--verify", "HEAD^{commit}")
	if e != nil {
		return "", nil, errors.New("committed Git HEAD required locally; materialize missing objects outside FOUNDRY")
	}
	commit := strings.TrimSpace(string(b))
	if len(commit) != 40 && len(commit) != 64 {
		return "", nil, errors.New("invalid commit")
	}
	tree, e := readCommittedTree(ctx, repo, commit, scope)
	if e != nil {
		return "", nil, e
	}
	if e := os.Mkdir(dir, 0700); e != nil {
		return "", nil, e
	}
	for _, d := range []string{"base", "candidate"} {
		if e := os.Mkdir(filepath.Join(dir, d), 0755); e != nil {
			return "", nil, e
		}
	}
	for _, f := range tree.files {
		for _, d := range []string{"base", "candidate"} {
			if e := writeFile(filepath.Join(dir, d), f.p, f.content); e != nil {
				return "", nil, e
			}
			root, e := os.OpenRoot(filepath.Join(dir, d))
			if e != nil {
				return "", nil, e
			}
			e = root.Chmod(f.p, f.mode)
			root.Close()
			if e != nil {
				return "", nil, e
			}
		}
	}
	return commit, tree.facts.ContextManifest, nil
}
