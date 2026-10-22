package workspace

import (
	"context"
	"foundry/internal/domain"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Keep the extracted generation policy strict about kind, not only spelling.
func TestValidateTreePathsPreservesParentKinds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		valid bool
	}{
		{"shared_directory", []string{"src/a.txt", "src/b.txt"}, true},
		{"file_directory_conflict", []string{"src", "src/a.txt"}, false},
		{"nested_file_directory_conflict", []string{"lib/src", "lib/src/a.txt"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string][]byte{}
			for _, p := range tc.paths {
				files[p] = []byte("text\n")
			}
			if err := validateTreePaths(files); (err == nil) != tc.valid {
				t.Fatalf("valid=%t, got %v", tc.valid, err)
			}
		})
	}
}

// Preparation shares admission policy and must reject before staging writes.
func TestPrepareRepositoryImportRejectsImplicitParentCollisionsBeforeWrites(t *testing.T) {
	for _, p := range []string{"SRC/other.txt", "SRC"} {
		t.Run(p, func(t *testing.T) {
			repo := repository(t)
			blob := git(t, repo, "rev-parse", "HEAD:src/main.txt")
			git(t, repo, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+p)
			git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "parent collision")
			commit := git(t, repo, "rev-parse", "HEAD")
			stage := filepath.Join(t.TempDir(), "stage")
			before := objectDatabaseDigest(t, repo)
			digest, ref, facts, err := PrepareRepositoryImport(context.Background(), repo, commit, stage, strings.Repeat("a", 32), domain.RepositoryImportSpec{Scope: []string{"src/"}, Acceptance: []string{"test -f src/main.txt"}})
			if err != domain.ImportCaseCollision {
				t.Fatalf("wanted case_collision before staging, got %v", err)
			}
			if digest != "" || ref.ID != "" || facts.FileCount != 0 || len(facts.ContextManifest) != 0 {
				t.Fatal("failed preparation returned successful facts", digest, ref, facts)
			}
			if _, err := os.Stat(stage); !os.IsNotExist(err) {
				t.Fatal("collision created staging directory", err)
			}
			if objectDatabaseDigest(t, repo) != before {
				t.Fatal("preparation changed source")
			}
		})
	}
}

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
	git(t, repo, "update-index", "--chmod=+x", "src/main.txt")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "mode")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, repo, "worktree", "add", "--detach", linked, "HEAD")
	dir := filepath.Join(t.TempDir(), "mission")
	commit, manifest, err := Snapshot(context.Background(), linked, dir, []string{"src/"})
	if runtime.GOOS == "windows" {
		if err == nil {
			t.Fatal("Windows admitted executable tree")
		}
		return
	}
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
