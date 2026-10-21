package workspace

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Equal base/candidate digests alone cannot prove fidelity to committed bytes.
func TestRepositoryImportDetectsCorruptSuccessfulWrites(t *testing.T) {
	repo := repository(t)
	commit := git(t, repo, "rev-parse", "HEAD")
	for _, kind := range []string{"bytes", "mode"} {
		t.Run(kind, func(t *testing.T) {
			fsys := durableFS()
			actual := fsys.write
			fsys.write = func(p string, b []byte, m os.FileMode) error {
				if strings.HasSuffix(p, "package.json") {
					if kind == "bytes" {
						b = []byte("wrong\n")
					} else {
						m = 0755
					}
				}
				return actual(p, b, m)
			}
			digest, ref, _, err := prepareRepositoryImport(context.Background(), fsys, repo, commit, filepath.Join(t.TempDir(), "stage"), generationID, domain.RepositoryImportSpec{Scope: []string{"src/"}, Acceptance: []string{"test -f src/main.txt"}})
			if err == nil || digest != "" || ref.ID != "" {
				t.Fatal("corrupt source material accepted", digest, ref, err)
			}
		})
	}
}

func TestRepositoryImportPublicationFailuresNeverReturnSuccess(t *testing.T) {
	for _, primitive := range []string{"publish", "sync"} {
		for nth := 1; ; nth++ {
			repo := repository(t)
			dir := t.TempDir()
			stage := filepath.Join(dir, "stage")
			final := filepath.Join(dir, "mission")
			digest, ref, _, err := PrepareRepositoryImport(context.Background(), repo, git(t, repo, "rev-parse", "HEAD"), stage, generationID, domain.RepositoryImportSpec{Scope: []string{"src/"}, Acceptance: []string{"test -f src/main.txt"}})
			if err != nil {
				t.Fatal(err)
			}
			fsys := durableFS()
			count := 0
			injected := errors.New("injected")
			if primitive == "publish" {
				actual := fsys.publish
				fsys.publish = func(a, b string) error {
					count++
					if count == nth {
						return injected
					}
					return actual(a, b)
				}
			} else {
				actual := fsys.sync
				fsys.sync = func(p string) error {
					count++
					if count == nth {
						return injected
					}
					return actual(p)
				}
			}
			err = publishRepositoryImport(fsys, stage, final, ref, digest)
			if count < nth {
				if err != nil {
					t.Fatal(err)
				}
				break
			}
			if !errors.Is(err, injected) {
				t.Fatal(err)
			}
			preserved := stage
			if primitive == "sync" {
				preserved = final
			}
			if err = VerifyGeneration(preserved, ref); err != nil {
				t.Fatal("lost complete artifact", err)
			}
		}
	}
}

func TestRepositoryImportExactCommitAndNoReplace(t *testing.T) {
	repo := repository(t)
	if err := os.Chmod(filepath.Join(repo, "src/main.txt"), 0755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "mode")
	commit := git(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "src/main.txt"), []byte("later\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "later")
	dir := t.TempDir()
	stage := filepath.Join(dir, "stage")
	final := filepath.Join(dir, "mission")
	spec := domain.RepositoryImportSpec{Scope: []string{"src/"}, Acceptance: []string{"test -f src/main.txt"}}
	digest, ref, facts, err := PrepareRepositoryImport(context.Background(), repo, commit, stage, generationID, spec)
	if err != nil || facts.TotalBytes != 17 || digest != ref.Digest {
		t.Fatal(digest, ref, facts, err)
	}
	b, err := os.ReadFile(filepath.Join(stage, "base/src/main.txt"))
	if err != nil || string(b) != "before\n" {
		t.Fatal(string(b), err)
	}
	if err = PublishRepositoryImport(stage, final, ref, digest); err != nil {
		t.Fatal(err)
	}
	if err = VerifyGeneration(final, ref); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(final, "base/src/main.txt"), filepath.Join(final, "generations", ref.ID, "tree/src/main.txt")} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0755 {
			t.Fatal(info, err)
		}
	}
	other := filepath.Join(dir, "other")
	if _, _, _, err = PrepareRepositoryImport(context.Background(), repo, commit, other, generationID, spec); err != nil {
		t.Fatal(err)
	}
	if err = PublishRepositoryImport(other, final, ref, digest); err == nil {
		t.Fatal("existing destination replaced")
	}
	if _, err = os.Stat(other); err != nil {
		t.Fatal("failed staging removed", err)
	}
}

func TestRepositoryImportDurabilityFailuresPreserveArtifacts(t *testing.T) {
	for _, primitive := range []string{"write", "sync"} {
		for nth := 1; ; nth++ {
			repo := repository(t)
			commit := git(t, repo, "rev-parse", "HEAD")
			stage := filepath.Join(t.TempDir(), "stage")
			fsys := durableFS()
			count := 0
			injected := errors.New("injected")
			if primitive == "write" {
				actual := fsys.write
				fsys.write = func(p string, b []byte, m os.FileMode) error {
					count++
					if count == nth {
						return injected
					}
					return actual(p, b, m)
				}
			} else {
				actual := fsys.sync
				fsys.sync = func(p string) error {
					count++
					if count == nth {
						return injected
					}
					return actual(p)
				}
			}
			digest, ref, _, err := prepareRepositoryImport(context.Background(), fsys, repo, commit, stage, generationID, domain.RepositoryImportSpec{Scope: []string{"src/"}, Acceptance: []string{"test -f src/main.txt"}})
			if count < nth {
				if err != nil {
					t.Fatal(err)
				}
				break
			}
			if !errors.Is(err, injected) || digest != "" || ref.ID != "" {
				t.Fatal(primitive, nth, digest, ref, err)
			}
			if _, err = os.Stat(stage); err != nil {
				t.Fatal("failure artifacts erased", err)
			}
		}
	}
}
