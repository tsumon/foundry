package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func importFixture(t *testing.T) (*Engine, *store.Store, string, string) {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-qb", "main"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture"}} {
		importGit(t, repo, args...)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	data := t.TempDir()
	e := New(db, &scripted{}, Options{DataDir: data, Runner: &fakeRunner{err: errors.New("runner forbidden")}})
	return e, db, repo, data
}

// Each row catches a whole-tree/policy bypass, not a live-file or runner check.
func TestInspectRepositoryFailureMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, stage string
		code        domain.ImportError
		change      func(*testing.T, string, *domain.RepositoryInspectionRequest)
	}{
		{"missing_head", "identity", domain.ImportMissingHEAD, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			importGit(t, p, "update-ref", "-d", "refs/heads/main")
		}},
		{"head_is_tree", "tree", domain.ImportObjectsUnavailable, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			tree := importGit(t, p, "rev-parse", "HEAD^{tree}")
			if err := os.WriteFile(filepath.Join(p, ".git", "HEAD"), []byte(tree+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"layout", "identity", domain.ImportLayout, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			importGit(t, p, "config", "include.path", "/private/RAW_FAILURE_MARKER")
		}},
		{"binary_outside_scope", "tree", domain.ImportBinary, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			writeImportFile(t, p, "outside.bin", "RAW_FAILURE_MARKER\x00")
		}},
		{"symlink", "tree", domain.ImportUnsupportedEntry, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			if err := os.Symlink("file.txt", filepath.Join(p, "outside")); err != nil {
				t.Fatal(err)
			}
			commitImportFixture(t, p)
		}},
		{"submodule", "tree", domain.ImportUnsupportedEntry, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			head := importGit(t, p, "rev-parse", "HEAD")
			importGit(t, p, "update-index", "--add", "--cacheinfo", "160000,"+head+",module")
			importGit(t, p, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "submodule")
		}},
		{"sensitive", "tree", domain.ImportUnsafePath, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			writeImportFile(t, p, ".env.RAW_FAILURE_MARKER", "RAW_FAILURE_MARKER")
		}},
		{"case_collision", "tree", domain.ImportCaseCollision, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			blob := importGit(t, p, "rev-parse", "HEAD:file.txt")
			importGit(t, p, "update-index", "--add", "--cacheinfo", "100644,"+blob+",FILE.txt")
			importGit(t, p, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "case")
		}},
		{"missing_blob", "tree", domain.ImportObjectsUnavailable, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			blob := importGit(t, p, "rev-parse", "HEAD:file.txt")
			if err := os.Remove(filepath.Join(p, ".git", "objects", blob[:2], blob[2:])); err != nil {
				t.Fatal(err)
			}
		}},
		{"file_limit", "tree", domain.ImportTreeLimit, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			for i := 0; i < 500; i++ {
				if err := os.WriteFile(filepath.Join(p, fmt.Sprintf("extra-%03d", i)), []byte("x"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			commitImportFixture(t, p)
		}},
		{"byte_limit", "tree", domain.ImportTreeLimit, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			writeImportFile(t, p, "large.txt", strings.Repeat("x", 10*1024*1024+1))
		}},
		{"aggregate_byte_limit", "tree", domain.ImportTreeLimit, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			for i := 0; i < 2; i++ {
				if err := os.WriteFile(filepath.Join(p, fmt.Sprintf("large-%d", i)), []byte(strings.Repeat("x", 6*1024*1024)), 0644); err != nil {
					t.Fatal(err)
				}
			}
			commitImportFixture(t, p)
		}},
		{"context_files", "tree", domain.ImportContextLimit, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			os.Mkdir(filepath.Join(p, "src"), 0755)
			for i := 0; i < 31; i++ {
				os.WriteFile(filepath.Join(p, "src", fmt.Sprintf("f-%d", i)), []byte("x"), 0644)
			}
			r.Spec.Scope = []string{"src"}
			commitImportFixture(t, p)
		}},
		{"context_bytes", "tree", domain.ImportContextLimit, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			writeImportFile(t, p, "file.txt", strings.Repeat("x", 200*1024+1))
		}},
		{"empty_context", "tree", domain.ImportEmptyContext, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) { r.Spec.Scope = []string{"absent"} }},
		{"acceptance", "acceptance", domain.ImportAcceptance, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			r.Spec.Acceptance = []string{"npm test"}
		}},
		{"script_graph", "acceptance", domain.ImportAcceptance, func(t *testing.T, p string, r *domain.RepositoryInspectionRequest) {
			writeImportFile(t, p, "verify.sh", "sh verify.sh\n")
			r.Spec.Acceptance = []string{"sh verify.sh"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, db, repo, data := importFixture(t)
			r := importRequest(repo)
			tc.change(t, repo, &r)
			before := importSourceDigest(t, repo)
			v, err := e.InspectRepository(context.Background(), r)
			if err != nil || v.Status != "blocked" || v.Stage != tc.stage || v.Code != tc.code {
				t.Fatalf("%+v %v", v, err)
			}
			if v.FileCount != nil || v.TotalBytes != nil || v.ContextFileCount != nil || v.ContextBytes != nil || len(v.ContextManifest) != 0 {
				t.Fatal("invented measurements", v)
			}
			if err := domain.ValidateImportSelection(v, domain.ImportSelection{InspectionID: v.ID, Digest: v.Digest}); err == nil {
				t.Fatal("failed record selected")
			}
			raw, _ := json.Marshal(v)
			if strings.Contains(string(raw), "RAW_FAILURE_MARKER") {
				t.Fatalf("unsafe projection: %s", raw)
			}
			rows, err := db.ListRepositoryInspections()
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			missions, _ := db.ListMissions()
			if len(missions) != 0 {
				t.Fatal("created mission")
			}
			entries, _ := os.ReadDir(data)
			if len(entries) != 0 {
				t.Fatal("created workspace")
			}
			if importSourceDigest(t, repo) != before {
				t.Fatal("inspection changed source")
			}
		})
	}
}
func commitImportFixture(t *testing.T, repo string) {
	t.Helper()
	importGit(t, repo, "add", ".")
	importGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture change")
}
func writeImportFile(t *testing.T, repo, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	commitImportFixture(t, repo)
}

func importSourceDigest(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		fmt.Fprintf(h, "%s:%s\n", rel, info.Mode())
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			h.Write([]byte(link))
		} else if !d.IsDir() {
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h.Write(raw)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestInspectRepositoryCancellationAndOverlap(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			e, _, repo, _ := importFixture(t)
			var ctx context.Context
			var cancel context.CancelFunc
			want := domain.ImportTimeout
			if cancelled {
				ctx, cancel = context.WithCancel(context.Background())
				want = domain.ImportCanceled
			} else {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			cancel()
			v, err := e.InspectRepository(ctx, importRequest(repo))
			if err != nil || v.Code != want || v.Status != "blocked" {
				t.Fatal(v, err)
			}
		})
	}
	e, _, repo, _ := importFixture(t)
	e.options.DataDir = filepath.Join(repo, "data")
	v, err := e.InspectRepository(context.Background(), importRequest(repo))
	if err != nil || v.Code != domain.ImportLocation {
		t.Fatal(v, err)
	}
	if _, err = os.Stat(e.options.DataDir); !os.IsNotExist(err) {
		t.Fatal("created overlapping directory")
	}
}

func TestInspectRepositoryRegisteredIdentityAndCommittedAcceptanceGraph(t *testing.T) {
	e, db, repo, _ := importFixture(t)
	writeImportFile(t, repo, "verify.sh", "sh helper.sh\n")
	writeImportFile(t, repo, "helper.sh", "test -f file.txt\n")
	os.WriteFile(filepath.Join(repo, "helper.sh"), []byte("npm test\n"), 0644)
	registered, err := db.SaveGitRepository(domain.GitRepository{ID: "repo", Name: "fixture", Path: repo}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r := importRequest(repo)
	r.RepoPath = ""
	r.RepositoryID = registered.ID
	r.RepositoryRevision = registered.Revision
	r.Spec.Acceptance = []string{"sh verify.sh"}
	v, err := e.InspectRepository(context.Background(), r)
	if err != nil || v.Status != "ready" || v.RepositoryRevision != 1 || v.RepositoryID != "repo" {
		t.Fatal(v, err)
	}
	if strings.Join(v.ProtectedInputs, ",") != "helper.sh,verify.sh" {
		t.Fatal(v.ProtectedInputs)
	}
	r.RepoPath = repo
	if _, err = e.InspectRepository(context.Background(), r); err != domain.ImportInvalidRequest {
		t.Fatal(err)
	}
}

// Plumbing must ignore repository hooks, conversion filters and transport helpers.
func TestInspectRepositoryNeverInvokesHelpersOrFetches(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			e, _, repo, _ := importFixture(t)
			writeImportFile(t, repo, ".gitattributes", "* filter=fixture\n")
			fixtures := t.TempDir()
			marker := filepath.Join(fixtures, "invoked")
			script := filepath.Join(fixtures, "trap")
			if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf invoked > '"+marker+"'\nexit 91\n"), 0755); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"core.fsmonitor", "filter.fixture.clean", "filter.fixture.smudge", "filter.fixture.process", "credential.helper"} {
				importGit(t, repo, "config", key, script)
			}
			importGit(t, repo, "config", "core.hooksPath", fixtures)
			raw, _ := os.ReadFile(script)
			for _, name := range []string{"post-checkout", "post-index-change", "git", "git-remote-foundryfixture"} {
				if err := os.WriteFile(filepath.Join(fixtures, name), raw, 0755); err != nil {
					t.Fatal(err)
				}
			}
			importGit(t, repo, "config", "remote.origin.url", "foundryfixture::local")
			importGit(t, repo, "config", "remote.origin.promisor", "true")
			importGit(t, repo, "config", "remote.origin.partialclonefilter", "blob:none")
			if missing {
				blob := importGit(t, repo, "rev-parse", "HEAD:file.txt")
				if err := os.Remove(filepath.Join(repo, ".git", "objects", blob[:2], blob[2:])); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", fixtures)
			t.Setenv("GIT_SSH_COMMAND", script)
			t.Setenv("GIT_CONFIG_COUNT", "1")
			t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
			t.Setenv("GIT_CONFIG_VALUE_0", script)
			before := importSourceDigest(t, repo)
			v, err := e.InspectRepository(context.Background(), importRequest(repo))
			if err != nil || !missing && v.Status != "ready" || missing && v.Code != domain.ImportObjectsUnavailable {
				t.Fatal(v, err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("helper invoked")
			}
			if importSourceDigest(t, repo) != before {
				t.Fatal("source changed")
			}
		})
	}
}
func importGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	c := exec.Command("/usr/bin/git", args...)
	c.Dir = repo
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, b, err)
	}
	return strings.TrimSpace(string(b))
}
func importRequest(repo string) domain.RepositoryInspectionRequest {
	return domain.RepositoryInspectionRequest{RepoPath: repo, Consent: true, Spec: domain.RepositoryImportSpec{Scope: []string{"file.txt"}, Acceptance: []string{"test -f file.txt"}, Image: "configuration-only"}}
}

func TestInspectRepositoryRecordsFactsWithoutMissionOrSourceMutation(t *testing.T) {
	e, db, repo, data := importFixture(t)
	head := importGit(t, repo, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(repo, "file.txt"), []byte("dirty\n"), 0644)
	v, err := e.InspectRepository(context.Background(), importRequest(repo))
	if err != nil || v.Status != "ready" || v.Commit != head || v.WorkingTreeStatus != "not_checked" {
		t.Fatalf("%+v %v", v, err)
	}
	if v.FileCount == nil || *v.FileCount != 1 || v.TotalBytes == nil || *v.TotalBytes != 7 || v.ContextManifest[0].SHA256 != "9160d4be34c8695bd172a76c7c7966587ea5a4d991ad22c87b2b91af54aa9ebb" {
		t.Fatal(v)
	}
	if err := domain.ValidateRepositoryInspection(v); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetRepositoryInspection(v.ID)
	if err != nil || got.Digest != v.Digest {
		t.Fatal(got, err)
	}
	missions, err := db.ListMissions()
	if err != nil || len(missions) != 0 {
		t.Fatal(missions, err)
	}
	entries, _ := os.ReadDir(data)
	if len(entries) != 0 {
		t.Fatal("created workspace", entries)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, "file.txt"))
	if string(raw) != "dirty\n" {
		t.Fatal("source changed")
	}
}

func TestInspectRepositoryConsentAndRegistryFailBeforeIO(t *testing.T) {
	e, db, _, _ := importFixture(t)
	r := importRequest("/must-not-be-read")
	r.Consent = false
	if _, err := e.InspectRepository(context.Background(), r); err != domain.ImportConsentRequired {
		t.Fatal(err)
	}
	registered, err := db.SaveGitRepository(domain.GitRepository{ID: "repo", Name: "fixture", Path: "/must-not-be-read"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r.Consent = true
	r.RepoPath = ""
	r.RepositoryID = registered.ID
	r.RepositoryRevision = registered.Revision + 1
	if _, err = e.InspectRepository(context.Background(), r); err != domain.ImportSourceChanged {
		t.Fatal(err)
	}
	rows, err := db.ListRepositoryInspections()
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}
