package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"foundry/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const MaxSnapshotBytes = 10 * 1024 * 1024

const trustedGit = "/usr/bin/git"

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("command output exceeds bound")
	}
	return b.Buffer.Write(p)
}
func gitCommand(ctx context.Context, dir string, limit int, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	prefix := []string{
		"--no-pager", "--no-optional-locks", "--no-replace-objects",
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
		"-c", "diff.external=", "-c", "core.quotePath=false",
		"-c", "protocol.allow=never", "-c", "maintenance.auto=false", "-c", "gc.auto=0",
	}
	c := exec.CommandContext(ctx, trustedGit, append(prefix, args...)...)
	c.Dir = dir
	c.Env = []string{
		"PATH=/usr/bin:/bin", "HOME=/dev/null", "XDG_CONFIG_HOME=/dev/null", "LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1",
		"GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "GIT_PROTOCOL_FROM_USER=0", "GIT_ATTR_NOSYSTEM=1",
	}
	c.WaitDelay = 100 * time.Millisecond
	b := &limitedBuffer{limit: limit}
	c.Stdout = b
	c.Stderr = &limitedBuffer{limit: 4096}
	e := c.Run()
	return b.Bytes(), e
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
