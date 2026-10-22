package workspace

import (
	"context"
	"foundry/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestWindowsControlledGitLongPathsProducesApplicablePatch(t *testing.T) {
	// Cleanup is t.TempDir's exact owned root; neither system TEMP nor any
	// existing repository is removed. Every component is below NTFS limits.
	dir := filepath.Join(t.TempDir(), "长路径 Unicode fixture")
	relDir := filepath.Join("internal", "pricing")
	for len(utf16.Encode([]rune(filepath.Join(dir, "candidate", relDir, "price.go")))) <= 320 {
		relDir = filepath.Join(relDir, "owned long segment")
	}
	rel := filepath.Join(relDir, "price.go")
	for _, name := range []string{"base", "candidate"} {
		p := filepath.Join(dir, name, rel)
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(name+"\n"), 0644); e != nil {
			t.Fatal(e)
		}
	}
	// Ambient command-scope settings must not disable the controlled setting
	// or introduce additional Git configuration keys.
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "core.longpaths")
	t.Setenv("GIT_CONFIG_VALUE_0", "false")
	t.Setenv("GIT_CONFIG_KEY_1", "foundry.ambient")
	t.Setenv("GIT_CONFIG_VALUE_1", "must-not-inherit")
	b, e := gitCommand(context.Background(), dir, 4096, "config", "--get", "foundry.ambient")
	if e == nil || len(b) != 0 {
		t.Fatal("ambient Git configuration inherited")
	}
	args := []string{"diff", "--no-index", "--no-renames", "--no-ext-diff", "--no-textconv", "--", "base", "candidate"}
	legacy, stderr, legacyErr := gitCommandOutput(context.Background(), dir, 4096, append([]string{"-c", "core.longpaths=false"}, args...)...)
	if legacyErr == nil || (len(legacy) != 0 && len(stderr) == 0) {
		t.Fatalf("legacy long-path negative control did not fail: bytes=%d stderr=%d failed=%t", len(legacy), len(stderr), legacyErr != nil)
	}
	patch, e := generatePatch(context.Background(), dir, "candidate")
	if e != nil {
		t.Fatal("controlled long-path patch generation failed", e)
	}
	if !strings.Contains(string(patch), "-base\n+candidate\n") {
		t.Fatal("long-path diff missing intended change")
	}
	patchFile := filepath.Join(t.TempDir(), "long-path.patch")
	if e := os.WriteFile(patchFile, patch, 0600); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"apply", "--check", patchFile}, {"apply", patchFile}} {
		if _, e := gitCommand(context.Background(), filepath.Join(dir, "base"), 4096, args...); e != nil {
			t.Fatal("long-path patch not applicable", e)
		}
	}
	b, e = os.ReadFile(filepath.Join(dir, "base", rel))
	if e != nil || string(b) != "candidate\n" {
		t.Fatal("patch failed to update intended long-path file", e)
	}
	t.Logf("native long-path control: utf16=%d legacy_bytes=%d legacy_stderr_bytes=%d production_patch_bytes=%d applied=true", len(utf16.Encode([]rune(filepath.Join(dir, "candidate", rel)))), len(legacy), len(stderr), len(patch))
}

func TestWindowsRejectsExecutableAnywhereInCommittedTree(t *testing.T) {
	repo := repository(t)
	git(t, repo, "update-index", "--chmod=+x", "tests/acceptance.txt")
	git(t, repo, "commit", "-qm", "unsupported outside scope")
	spec := domain.RepositoryImportSpec{Scope: []string{"src/"}, Acceptance: []string{"test -f src/main.txt"}}
	if _, e := InspectCommittedTree(context.Background(), repo, git(t, repo, "rev-parse", "HEAD"), spec); e == nil {
		t.Fatal("executable outside selected scope admitted")
	}
}
func TestWindowsReadonlyModeSurvivesGenerationAndReset(t *testing.T) {
	dir, source := generationFixture(t)
	p := filepath.Join(source, "src/run.sh")
	if e := os.Chmod(p, 0444); e != nil {
		t.Fatal(e)
	}
	ref, _, e := PrepareGeneration(dir, generationID, source, nil, nil, []string{"src/"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	tree, e := GenerationPath(dir, ref)
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(filepath.Join(tree, "src/run.sh"))
	if e != nil || info.Mode().Perm() != 0444 {
		t.Fatal("readonly mode laundered", e)
	}
	before, e := Digest(tree)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(filepath.Join(tree, "src/run.sh"), 0666); e != nil {
		t.Fatal(e)
	}
	after, e := Digest(tree)
	if e != nil || before == after {
		t.Fatal("readonly mutation ignored", e)
	}
	if e = Reset(source, tree); e != nil {
		t.Fatal(e)
	}
	info, e = os.Stat(filepath.Join(tree, "src/run.sh"))
	if e != nil || info.Mode().Perm() != 0444 {
		t.Fatal("reset laundered readonly", e)
	}
}
func TestWindowsOrdinaryImportPatchHasNoExecutableModeChange(t *testing.T) {
	repo := repository(t)
	dir := filepath.Join(t.TempDir(), "mission")
	if _, _, e := Snapshot(context.Background(), repo, dir, []string{"src/"}); e != nil {
		t.Fatal(e)
	}
	if _, e := Apply(filepath.Join(dir, "candidate"), []Edit{{Path: "src/main.txt", Content: "after\n"}}, []string{"src/"}, nil); e != nil {
		t.Fatal(e)
	}
	patch, _, e := Export(context.Background(), dir, "Windows ordinary patch")
	if e != nil {
		t.Fatal(e)
	}
	body, e := os.ReadFile(patch)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(body), "100755") || strings.Contains(string(body), "old mode") || strings.Contains(string(body), "new mode") {
		t.Fatal("patch manufactured executable change")
	}
	git(t, repo, "apply", "--check", patch)
}
