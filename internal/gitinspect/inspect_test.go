package gitinspect

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestMetadataConfigOpenRejectsSwappedSymlinkAndFIFO(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("[core]\n bare=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "symlink")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(root, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{symlink, fifo} {
		if _, err := readMetadataConfig(path); err == nil {
			t.Fatalf("opened non-regular config %s", path)
		}
	}
}

func fixtureGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("/usr/bin/git", args...)
	cmd.Dir = root
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git %v: %s %v", args, b, err)
	}
	return strings.TrimSpace(string(b))
}
func repoFixture(t *testing.T, commit bool) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, root, "init", "-b", "main")
	if commit {
		if err = os.WriteFile(filepath.Join(root, "file.txt"), []byte("fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
		fixtureGit(t, root, "add", "file.txt")
		fixtureGit(t, root, "commit", "-m", "fixture")
	}
	return root
}
func TestIdentityOrdinaryDetachedAndUnborn(t *testing.T) {
	for _, committed := range []bool{false, true} {
		root := repoFixture(t, committed)
		v, err := Inspect(context.Background(), root)
		if err != nil || v.Status != "succeeded" || v.Root != root || v.Branch != "main" || v.Unborn == committed || v.Detached || v.WorkingTreeStatus != "not_checked" {
			t.Fatalf("%#v %v", v, err)
		}
		if committed {
			head := fixtureGit(t, root, "rev-parse", "HEAD")
			if v.HEAD != head {
				t.Fatal(v)
			}
			fixtureGit(t, root, "checkout", "--detach")
			v, err = Inspect(context.Background(), root)
			if err != nil || !v.Detached || v.Branch != "" || v.HEAD != head {
				t.Fatalf("%#v %v", v, err)
			}
		}
	}
}

func TestBranchIdentityDoesNotUseAmbiguousAbbreviation(t *testing.T) {
	root := repoFixture(t, true)
	fixtureGit(t, root, "tag", "main")
	v, err := Inspect(context.Background(), root)
	if err != nil || v.Branch != "main" || v.Detached {
		t.Fatalf("%#v %v", v, err)
	}
}
func TestInspectionCannotRunRepositoryCommandsOrInheritedEnvironment(t *testing.T) {
	root := repoFixture(t, true)
	sentinel := filepath.Join(root, "executed")
	script := filepath.Join(root, "trap.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+sentinel+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"core.fsmonitor", script}, {"core.hooksPath", root}, {"filter.evil.clean", script}, {"filter.evil.process", script}, {"remote.origin.url", "https://credential@example.invalid/repo"}} {
		fixtureGit(t, root, "config", pair[0], pair[1])
	}
	for _, hook := range []string{"post-checkout", "post-index-change", "pre-commit"} {
		if err := os.WriteFile(filepath.Join(root, hook), []byte("#!/bin/sh\ntouch '"+sentinel+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("* filter=evil\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "include.path")
	t.Setenv("GIT_CONFIG_VALUE_0", script)
	t.Setenv("GIT_DIR", "/nonexistent")
	t.Setenv("GIT_SSH_COMMAND", script)
	t.Setenv("SSH_ASKPASS", script)
	t.Setenv("GIT_CONFIG_GLOBAL", script)
	t.Setenv("GIT_PROXY_COMMAND", script)
	t.Setenv("DYLD_INSERT_LIBRARIES", "/nonexistent-malicious-loader")
	t.Setenv("LD_PRELOAD", "/nonexistent-malicious-loader")
	paths := []string{".git/config", ".git/index", "file.txt", ".gitattributes"}
	before := map[string][32]byte{}
	for _, p := range paths {
		b, e := os.ReadFile(filepath.Join(root, p))
		if e != nil {
			t.Fatal(e)
		}
		before[p] = sha256.Sum256(b)
	}
	v, err := Inspect(context.Background(), root)
	if err != nil || v.Status != "succeeded" {
		t.Fatalf("%#v %v", v, err)
	}
	if _, err = os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("repository command ran")
	}
	for _, p := range paths {
		b, e := os.ReadFile(filepath.Join(root, p))
		if e != nil || sha256.Sum256(b) != before[p] {
			t.Fatalf("changed %s", p)
		}
	}
}
func TestRejectUnsupportedLayoutsAndCanceledInspection(t *testing.T) {
	for _, kind := range []string{"include", "includeif", "worktree", "commondir", "alternates", "http-alternates", "symlink", "config-symlink", "git-symlink", "gitfile", "extensions"} {
		t.Run(kind, func(t *testing.T) {
			root := repoFixture(t, false)
			switch kind {
			case "include":
				fixtureGit(t, root, "config", "include.path", filepath.Join(root, "secret-config"))
			case "includeif":
				fixtureGit(t, root, "config", "includeIf.gitdir:/.path", filepath.Join(root, "secret-config"))
			case "worktree":
				fixtureGit(t, root, "config", "core.worktree", root)
			case "commondir":
				os.WriteFile(filepath.Join(root, ".git/commondir"), []byte("elsewhere"), 0600)
			case "alternates":
				os.WriteFile(filepath.Join(root, ".git/objects/info/alternates"), []byte("elsewhere"), 0600)
			case "http-alternates":
				os.WriteFile(filepath.Join(root, ".git/objects/info/http-alternates"), []byte("https://credential@example.invalid"), 0600)
			case "extensions":
				fixtureGit(t, root, "config", "extensions.partialClone", "origin")
			case "config-symlink":
				os.Rename(filepath.Join(root, ".git/config"), filepath.Join(root, "outside-config"))
				os.Symlink(filepath.Join(root, "outside-config"), filepath.Join(root, ".git/config"))
			case "git-symlink":
				os.Rename(filepath.Join(root, ".git"), filepath.Join(root, "metadata"))
				os.Symlink(filepath.Join(root, "metadata"), filepath.Join(root, ".git"))
			case "symlink":
				os.Symlink(filepath.Join(root, "outside"), filepath.Join(root, ".git/refs/evil"))
			case "gitfile":
				os.Rename(filepath.Join(root, ".git"), filepath.Join(root, "metadata"))
				os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: metadata"), 0600)
			}
			v, err := Inspect(context.Background(), root)
			if err == nil || v.Status != "failed" || v.ErrorCode != "unsupported_layout" || v.WorkingTreeStatus != "not_checked" {
				t.Fatalf("%#v %v", v, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v, err := Inspect(ctx, repoFixture(t, false))
	if err == nil || v.ErrorCode != "canceled" {
		t.Fatalf("%#v %v", v, err)
	}
}
