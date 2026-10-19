package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
	b, e = gitCommand(ctx, repo, 512*1024, "ls-tree", "-rz", "--full-tree", "-l", commit)
	if e != nil {
		return "", nil, errors.New("cannot inspect bounded local Git tree; materialize missing objects outside FOUNDRY")
	}
	type file struct {
		p, id string
		size  int
		mode  os.FileMode
	}
	files := []file{}
	seen := map[string]bool{}
	total := 0
	for _, entry := range bytes.Split(b, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		parts := strings.SplitN(string(entry), "\t", 2)
		if len(parts) != 2 {
			return "", nil, errors.New("invalid tree entry")
		}
		fields := strings.Fields(parts[0])
		if len(fields) != 4 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			return "", nil, errors.New("symlinks/submodules/special entries are unsupported")
		}
		p := parts[1]
		if e := safePath(p); e != nil {
			return "", nil, fmt.Errorf("snapshot refused %q: %w", p, e)
		}
		lower := strings.ToLower(p)
		if seen[lower] {
			return "", nil, errors.New("case-colliding paths")
		}
		seen[lower] = true
		size, e := strconv.Atoi(fields[3])
		if e != nil || size < 0 || size > MaxSnapshotBytes {
			return "", nil, errors.New("invalid or unavailable local blob size; materialize missing objects outside FOUNDRY")
		}
		total += size
		mode := os.FileMode(0644)
		if fields[0] == "100755" {
			mode = 0755
		}
		files = append(files, file{p, fields[2], size, mode})
		if len(files) > 500 || total > MaxSnapshotBytes {
			return "", nil, errors.New("snapshot exceeds 500 files / 10 MiB")
		}
	}
	if e := os.Mkdir(dir, 0700); e != nil {
		return "", nil, e
	}
	for _, d := range []string{"base", "candidate"} {
		if e := os.Mkdir(filepath.Join(dir, d), 0755); e != nil {
			return "", nil, e
		}
	}
	manifest := []domain.FileMeta{}
	contextBytes := 0
	for _, f := range files {
		b, e := gitCommand(ctx, repo, f.size+1, "cat-file", "blob", f.id)
		if e != nil || len(b) != f.size {
			return "", nil, errors.New("bounded local blob read failed; materialize missing objects outside FOUNDRY")
		}
		if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
			return "", nil, errors.New("binary file refused")
		}
		for _, d := range []string{"base", "candidate"} {
			if e := writeFile(filepath.Join(dir, d), f.p, b); e != nil {
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
		include := false
		for _, r := range scope {
			include = include || Matches(f.p, r)
		}
		if include {
			manifest = append(manifest, domain.FileMeta{Path: f.p, SHA256: hash(b), Bytes: int64(len(b))})
			contextBytes += len(b)
			if len(manifest) > 30 || contextBytes > 200*1024 {
				return "", nil, errors.New("context exceeds 30 files / 200 KiB; narrow scope")
			}
		}
	}
	if len(manifest) == 0 {
		return "", nil, errors.New("scope contains no committed context files")
	}
	return commit, manifest, nil
}
