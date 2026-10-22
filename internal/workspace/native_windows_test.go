package workspace

import (
	"context"
	"foundry/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
