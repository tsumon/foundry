// Package gitinspect observes identity only. In particular, status/diff are not
// safe substitutes: they can invoke repository-controlled conversion filters.
package gitinspect

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"foundry/internal/domain"
)

const trustedGit = "/usr/bin/git"

type inspectionError string

func (e inspectionError) Error() string { return "git identity inspection failed: " + string(e) }

// The child receives an allowlist, not a filtered copy of the process environment.
// No inherited GIT_*, SSH, proxy, pager, loader, or credential-helper environment.
func childEnvironment() []string {
	return []string{
		"PATH=/usr/bin:/bin", "HOME=/dev/null", "XDG_CONFIG_HOME=/dev/null", "LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1",
		"GIT_ALLOW_PROTOCOL=", "GIT_PROTOCOL_FROM_USER=0", "GIT_ATTR_NOSYSTEM=1",
	}
}

type boundedOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8192 {
		b.overflow = true
		return 0, errors.New("output limit")
	}
	return b.Buffer.Write(p)
}

func run(ctx context.Context, root string, args ...string) (string, int, error) {
	fixed := []string{"--no-pager", "--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "protocol.allow=never", "-c", "maintenance.auto=false", "-c", "gc.auto=0"}
	if root != "" {
		fixed = append(fixed, "--git-dir="+filepath.Join(root, ".git"), "--work-tree="+root)
	}
	cmd := exec.CommandContext(ctx, trustedGit, append(fixed, args...)...)
	cmd.Env = childEnvironment()
	cmd.Dir = "/"
	if root != "" {
		cmd.Dir = root
	}
	cmd.WaitDelay = 100 * time.Millisecond
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else {
			code = -1
		}
	}
	if output.overflow {
		return "", -1, inspectionError("inspection_failed")
	}
	return strings.TrimSuffix(output.String(), "\n"), code, err
}

var versionPattern = regexp.MustCompile(`^git version ([0-9]+)\.([0-9]+)\.`)
var hashPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
var configSection = regexp.MustCompile(`^\[([A-Za-z0-9-]+)(?:\s+"[^"\r\n]*")?\]\s*(?:[#;].*)?$`)
var configKey = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*)(?:\s*=.*)?$`)

func Inspect(parent context.Context, path string) (domain.GitObservation, error) {
	v := domain.GitObservation{Status: "failed", WorkingTreeStatus: "not_checked"}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	fail := func(code string) (domain.GitObservation, error) {
		if errors.Is(ctx.Err(), context.Canceled) {
			code = "canceled"
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "timeout"
		}
		v.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
		v.ErrorCode = code
		return v, inspectionError(code)
	}
	if ctx.Err() != nil {
		return fail("canceled")
	}
	if !filepath.IsAbs(path) || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsFunc(path, unicode.IsControl) {
		return fail("invalid_repository")
	}
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fail("repository_unavailable")
	}
	root = filepath.Clean(root)
	if err = checkMetadata(ctx, root); err != nil {
		var safe inspectionError
		if errors.As(err, &safe) {
			return fail(string(safe))
		}
		return fail("repository_unavailable")
	}
	version, _, err := run(ctx, "", "--version")
	matches := versionPattern.FindStringSubmatch(version)
	if err != nil || len(matches) != 3 {
		return fail("git_unavailable")
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	if major < 2 || (major == 2 && minor < 36) {
		return fail("git_unavailable")
	}
	actual, _, err := run(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil || actual != root {
		return fail("inspection_failed")
	}
	branch, branchCode, branchErr := run(ctx, root, "symbolic-ref", "--quiet", "HEAD")
	if branchErr != nil && branchCode != 1 {
		return fail("inspection_failed")
	}
	if branchCode == 0 && (!strings.HasPrefix(branch, "refs/heads/") || len(branch) > 1024 || !utf8.ValidString(branch) || strings.ContainsFunc(branch, unicode.IsControl)) {
		return fail("inspection_failed")
	}
	branch = strings.TrimPrefix(branch, "refs/heads/")
	if branchCode == 0 && branch == "" {
		return fail("inspection_failed")
	}
	head, headCode, headErr := run(ctx, root, "rev-parse", "--verify", "HEAD")
	if headErr != nil {
		if headCode != 128 || branchCode != 0 {
			return fail("inspection_failed")
		}
		// A missing branch ref is unborn; malformed refs/config must not masquerade as it.
		_, refCode, _ := run(ctx, root, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
		if refCode != 1 {
			return fail("inspection_failed")
		}
		v.Unborn = true
		head = ""
	} else if !hashPattern.MatchString(head) {
		return fail("inspection_failed")
	}
	if ctx.Err() != nil {
		return fail("inspection_failed")
	}
	v.Root = root
	v.Branch = branch
	v.HEAD = head
	v.Detached = branchCode == 1
	v.Status = "succeeded"
	v.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return v, nil
}

// Restrict layout before plumbing. This check is not treated as a TOCTOU-proof
// execution barrier: every command itself stays identity-only with fixed safety
// flags. Repository status, filters, hooks, and object fetching are never used.
func checkMetadata(ctx context.Context, root string) error {
	gitdir := filepath.Join(root, ".git")
	info, err := os.Lstat(gitdir)
	if err != nil {
		return inspectionError("invalid_repository")
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return inspectionError("unsupported_layout")
	}
	count := 0
	err = filepath.WalkDir(gitdir, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		count++
		if count > 20000 {
			return inspectionError("unsupported_layout")
		}
		if d.Type()&os.ModeSymlink != 0 || (!d.IsDir() && !d.Type().IsRegular()) {
			return inspectionError("unsupported_layout")
		}
		rel, _ := filepath.Rel(gitdir, p)
		switch rel {
		case "commondir", "objects/info/alternates", "objects/info/http-alternates", "config.worktree":
			return inspectionError("unsupported_layout")
		}
		return nil
	})
	if err != nil {
		return err
	}
	raw, err := readMetadataConfig(filepath.Join(gitdir, "config"))
	if err != nil {
		return err
	}
	if len(raw) > 65536 || !utf8.Valid(raw) || bytes.ContainsRune(raw, 0) {
		return inspectionError("unsupported_layout")
	}
	section := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasSuffix(line, "\\") {
			return inspectionError("unsupported_layout")
		}
		if strings.HasPrefix(line, "[") {
			match := configSection.FindStringSubmatch(line)
			if len(match) != 2 {
				return inspectionError("unsupported_layout")
			}
			section = strings.ToLower(match[1])
			if section == "include" || section == "includeif" || section == "extensions" {
				return inspectionError("unsupported_layout")
			}
			continue
		}
		match := configKey.FindStringSubmatch(line)
		if section == "" || len(match) != 2 {
			return inspectionError("unsupported_layout")
		}
		if section == "core" && strings.EqualFold(match[1], "worktree") {
			return inspectionError("unsupported_layout")
		}
		if section == "core" && strings.EqualFold(match[1], "bare") {
			pair := strings.SplitN(line, "=", 2)
			if len(pair) != 2 || strings.TrimSpace(strings.ToLower(pair[1])) != "false" {
				return inspectionError("unsupported_layout")
			}
		}
	}
	return nil
}

// Protect the actual config read too: preflight metadata checks are not an
// atomic guarantee against a concurrent rename to a symlink or named pipe.
func readMetadataConfig(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, inspectionError("unsupported_layout")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, inspectionError("unsupported_layout")
	}
	return io.ReadAll(io.LimitReader(f, 65537))
}
