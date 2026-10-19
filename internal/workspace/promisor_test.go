package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotDoesNotFetchMissingPromisedBlob(t *testing.T) {
	repo := repository(t)
	blob := git(t, repo, "rev-parse", "HEAD:src/main.txt")
	object := filepath.Join(repo, ".git", "objects", blob[:2], blob[2:])
	if err := os.Remove(object); err != nil {
		t.Fatal(err)
	}
	helperDir := t.TempDir()
	marker := filepath.Join(helperDir, "invoked")
	// This local transport helper cannot access a network; invocation itself is the violation.
	helper := "#!/bin/sh\nprintf invoked > '" + marker + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(helperDir, "git-remote-foundryfixture"), []byte(helper), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", helperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	git(t, repo, "config", "remote.origin.url", "foundryfixture::local")
	git(t, repo, "config", "remote.origin.promisor", "true")
	git(t, repo, "config", "remote.origin.partialclonefilter", "blob:none")
	before := objectDatabaseDigest(t, repo)
	_, _, err := Snapshot(context.Background(), repo, filepath.Join(t.TempDir(), "mission"), []string{"src/"})
	if err == nil {
		t.Error("missing object accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("snapshot invoked promisor transport")
	}
	after := objectDatabaseDigest(t, repo)
	if before != after {
		t.Error("source object database changed")
	}
	if err != nil && !strings.Contains(err.Error(), "materialize") {
		t.Errorf("missing actionable local-object error: %v", err)
	}
}

func objectDatabaseDigest(t *testing.T, repo string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(filepath.Join(repo, ".git", "objects"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s:%x\n", p, b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
