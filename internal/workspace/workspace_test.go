package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"foundry/internal/testutil"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := testutil.Git(t, args...)
	c.Dir = dir
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git: %v %s", e, b)
	}
	return strings.TrimSpace(string(b))
}
func repository(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	git(t, p, "init", "-q")
	for n, b := range map[string]string{"src/main.txt": "before\n", "tests/acceptance.txt": "frozen\n", "package.json": "{}\n"} {
		os.MkdirAll(filepath.Dir(filepath.Join(p, n)), 0755)
		os.WriteFile(filepath.Join(p, n), []byte(b), 0644)
	}
	git(t, p, "add", ".")
	git(t, p, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "base")
	return p
}

func TestEditPathBoundary(t *testing.T) {
	for _, p := range []string{"tests/a.ts", "src/a.test.ts", "package.json", "src/tsconfig.json", "src/../escape", "/tmp/a", "src/.env", "src/a\\b", "src/a\n"} {
		if ValidateEditPath(p, []string{"src/", "tests/", "package.json"}, nil) == nil {
			t.Errorf("accepted %q", p)
		}
	}
	if e := ValidateEditPath("src/main.txt", []string{"src/"}, nil); e != nil {
		t.Fatal(e)
	}
}
func TestSnapshotAndRootedEdits(t *testing.T) {
	repo := repository(t)
	head := git(t, repo, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(repo, "src/main.txt"), []byte("dirty\n"), 0644)
	dir := filepath.Join(t.TempDir(), "mission")
	commit, manifest, e := Snapshot(context.Background(), repo, dir, []string{"src/"})
	if e != nil {
		t.Fatal(e)
	}
	if commit != head || len(manifest) != 1 {
		t.Fatalf("snapshot: %s %+v", commit, manifest)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "candidate/src/main.txt"))
	if string(b) != "before\n" {
		t.Fatal(string(b))
	}
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(dir, "candidate/src/link"))
	if _, e := Apply(filepath.Join(dir, "candidate"), []Edit{{Path: "src/link/escape", Content: "oops"}}, []string{"src/"}, nil); e == nil {
		t.Fatal("followed symlink")
	}
	if _, e := os.Stat(filepath.Join(outside, "escape")); !os.IsNotExist(e) {
		t.Fatal("escaped")
	}
	b, _ = os.ReadFile(filepath.Join(repo, "src/main.txt"))
	if string(b) != "dirty\n" {
		t.Fatal("source changed")
	}
}

func TestSnapshotUsesTrustedGitAndCannotRunRepositoryConfiguration(t *testing.T) {
	repo := repository(t)
	sentinel := filepath.Join(t.TempDir(), "executed")
	script := filepath.Join(t.TempDir(), "trap.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf executed > '"+sentinel+"'\nexit 91\n"), 0700); err != nil {
		t.Fatal(err)
	}
	poison := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(poison, []byte("#!/bin/sh\nprintf executed > '"+sentinel+"'\nexit 92\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("* filter=evil\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".gitattributes")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "attributes")
	for _, pair := range [][2]string{{"core.fsmonitor", script}, {"core.hooksPath", filepath.Dir(script)}, {"filter.evil.clean", script}, {"filter.evil.smudge", script}, {"filter.evil.process", script}} {
		git(t, repo, "config", pair[0], pair[1])
	}
	if err := os.WriteFile(filepath.Join(repo, "src/main.txt"), []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src/untracked.txt"), []byte("untracked\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := fixtureTreeDigest(t, repo)
	t.Setenv("PATH", filepath.Dir(poison))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "include.path")
	t.Setenv("GIT_CONFIG_VALUE_0", script)
	t.Setenv("GIT_DIR", "/nonexistent")
	t.Setenv("GIT_SSH_COMMAND", script)
	t.Setenv("SSH_ASKPASS", script)
	t.Setenv("DYLD_INSERT_LIBRARIES", "/nonexistent-malicious-loader")

	dir := filepath.Join(t.TempDir(), "mission")
	if _, _, err := Snapshot(context.Background(), repo, dir, []string{"src/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("PATH, hook, filter, fsmonitor, or inherited configuration command ran")
	}
	if after := fixtureTreeDigest(t, repo); after != before {
		t.Fatal("snapshot changed source worktree or Git metadata")
	}
	b, err := os.ReadFile(filepath.Join(dir, "base/src/main.txt"))
	if err != nil || string(b) != "before\n" {
		t.Fatalf("dirty content entered snapshot: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "base/src/untracked.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked content entered snapshot")
	}
}

func fixtureTreeDigest(t *testing.T, root string) string {
	t.Helper()
	var records []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		records = append(records, fmt.Sprintf("%s:%o:%x", rel, info.Mode(), sum))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(records)
	sum := sha256.Sum256([]byte(strings.Join(records, "\n")))
	return fmt.Sprintf("%x", sum)
}
func TestSnapshotRejectsSecretsAndSymlinks(t *testing.T) {
	for _, name := range []string{".env", "link"} {
		t.Run(name, func(t *testing.T) {
			repo := repository(t)
			if name == "link" {
				os.Symlink("src/main.txt", filepath.Join(repo, name))
			} else {
				os.WriteFile(filepath.Join(repo, name), []byte("secret"), 0644)
			}
			git(t, repo, "add", ".")
			git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "unsafe")
			if _, _, e := Snapshot(context.Background(), repo, filepath.Join(t.TempDir(), "mission"), []string{"src/"}); e == nil {
				t.Fatal("unsafe snapshot accepted")
			}
		})
	}
}
func TestExportIsGitApplyCompatible(t *testing.T) {
	repo := repository(t)
	dir := filepath.Join(t.TempDir(), "mission")
	_, _, e := Snapshot(context.Background(), repo, dir, []string{"src/"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = Apply(filepath.Join(dir, "candidate"), []Edit{{Path: "src/main.txt", Content: "after\n"}, {Path: "src/new.txt", Content: "new\n"}}, []string{"src/"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	patch, _, e := Export(context.Background(), dir, "report")
	if e != nil {
		t.Fatal(e)
	}
	git(t, repo, "apply", "--check", patch)
	if git(t, repo, "status", "--porcelain") != "" {
		t.Fatal("source changed")
	}
}
func TestExportPreservesPatchContentAndExecutableMode(t *testing.T) {
	repo := repository(t)
	os.Chmod(filepath.Join(repo, "src/main.txt"), 0755)
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "mode")
	dir := filepath.Join(t.TempDir(), "mission")
	_, _, err := Snapshot(context.Background(), repo, dir, []string{"src/"})
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(dir, "candidate/src/main.txt"))
	if info.Mode().Perm() != nativeWritableMode() {
		t.Fatal("executable mode lost")
	}
	before, _ := Digest(filepath.Join(dir, "candidate"))
	os.Chmod(filepath.Join(dir, "candidate/src/main.txt"), nativeChangedMode())
	after, _ := Digest(filepath.Join(dir, "candidate"))
	if before == after {
		t.Fatal("mode change did not invalidate digest")
	}
	os.Chmod(filepath.Join(dir, "candidate/src/main.txt"), 0755)
	Apply(filepath.Join(dir, "candidate"), []Edit{{Path: "src/main.txt", Content: "keep a/base/value and b/candidate/value\n"}}, []string{"src/"}, nil)
	patch, _, err := Export(context.Background(), dir, "report")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(patch)
	if !strings.Contains(string(b), "+keep a/base/value and b/candidate/value") {
		t.Fatal("patch content rewritten")
	}
	git(t, repo, "apply", "--check", patch)
}
func nativeWritableMode() os.FileMode {
	if runtime.GOOS == "windows" {
		return 0666
	}
	return 0755
}
func nativeChangedMode() os.FileMode {
	if runtime.GOOS == "windows" {
		return 0444
	}
	return 0644
}
func TestContextBoundsRejectRatherThanTruncate(t *testing.T) {
	repo := repository(t)
	os.WriteFile(filepath.Join(repo, "src/main.txt"), []byte(strings.Repeat("x", 200*1024+1)), 0644)
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "large")
	if _, _, err := Snapshot(context.Background(), repo, filepath.Join(t.TempDir(), "mission"), []string{"src/"}); err == nil {
		t.Fatal("oversized context silently accepted")
	}
}

func TestCredentialConfigAndRootSymlinkRejected(t *testing.T) {
	for _, p := range []string{".npmrc", ".netrc", ".aws/config", ".ssh/config"} {
		if err := safePath(p); err == nil {
			t.Errorf("credential path accepted: %s", p)
		}
	}
	target := t.TempDir()
	os.WriteFile(filepath.Join(target, "file"), []byte("private"), 0644)
	link := filepath.Join(t.TempDir(), "candidate")
	os.Symlink(target, link)
	if _, err := Digest(link); err == nil {
		t.Fatal("followed candidate root symlink")
	}
}
