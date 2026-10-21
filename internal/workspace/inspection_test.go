package workspace

import (
	"context"
	"foundry/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

// Reading live files instead of committed blobs changes these literal facts.
func TestInspectCommittedTreeMeasuresWithoutMaterializing(t *testing.T) {
	repo := repository(t)
	commit := git(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "src/main.txt"), []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := objectDatabaseDigest(t, repo)
	facts, err := InspectCommittedTree(context.Background(), repo, commit, domain.RepositoryImportSpec{Scope: []string{"src/"}, Acceptance: []string{"test -f src/main.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if facts.FileCount != 3 || facts.TotalBytes != 17 || facts.ContextBytes != 7 || len(facts.ContextManifest) != 1 {
		t.Fatalf("facts: %+v", facts)
	}
	if f := facts.ContextManifest[0]; f.Path != "src/main.txt" || f.Bytes != 7 || f.SHA256 != "9160d4be34c8695bd172a76c7c7966587ea5a4d991ad22c87b2b91af54aa9ebb" {
		t.Fatal(f)
	}
	if len(facts.ProtectedInputs) != 2 || facts.ProtectedInputs[0] != "package.json" || facts.ProtectedInputs[1] != "tests/acceptance.txt" {
		t.Fatal(facts.ProtectedInputs)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, "src/main.txt"))
	if string(raw) != "dirty\n" || objectDatabaseDigest(t, repo) != before {
		t.Fatal("source changed")
	}
	entries, _ := os.ReadDir(repo)
	if len(entries) != 4 {
		t.Fatalf("inspection created files: %v", entries)
	}
}

// Strict admission must not retroactively tighten the public historical Snapshot
// entry point: linked worktrees still snapshot their committed bytes and modes.
func TestHistoricalSnapshotStillSupportsLinkedWorktreeAndExecutableMode(t *testing.T) {
	repo := repository(t)
	if err := os.Chmod(filepath.Join(repo, "src/main.txt"), 0755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "mode")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, repo, "worktree", "add", "--detach", linked, "HEAD")
	dir := filepath.Join(t.TempDir(), "mission")
	commit, manifest, err := Snapshot(context.Background(), linked, dir, []string{"src/"})
	if err != nil || commit != git(t, repo, "rev-parse", "HEAD") || len(manifest) != 1 {
		t.Fatal(commit, manifest, err)
	}
	for _, tree := range []string{"base", "candidate"} {
		p := filepath.Join(dir, tree, "src/main.txt")
		raw, err := os.ReadFile(p)
		if err != nil || string(raw) != "before\n" {
			t.Fatal(string(raw), err)
		}
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0755 {
			t.Fatal(info, err)
		}
	}
}
