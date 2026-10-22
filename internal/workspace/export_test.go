package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExportPreservesNestedDirectoryPrefixes(t *testing.T) {
	repo := repository(t)
	paths := []string{"src/a/base/file.txt", "src/b/candidate/file.txt"}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, p)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, p), []byte("original\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "nested paths")
	dir := filepath.Join(t.TempDir(), "mission")
	if _, _, err := Snapshot(context.Background(), repo, dir, []string{"src/"}); err != nil {
		t.Fatal(err)
	}
	edits := []Edit{{Path: paths[0], Content: "first intended change\n"}, {Path: paths[1], Content: "second intended change\n"}, {Path: "src/a/base/new.txt", Content: "new intended file\n"}}
	if _, err := Apply(filepath.Join(dir, "candidate"), edits, []string{"src/"}, nil); err != nil {
		t.Fatal(err)
	}
	patch, _, err := Export(context.Background(), dir, "report")
	if err != nil {
		t.Fatal(err)
	}
	git(t, repo, "apply", "--check", patch)
	// Applying to a separate disposable clone checks targets and contents while
	// retaining the source-untouched assertion for the actual export operation.
	clone := filepath.Join(t.TempDir(), "apply-target")
	git(t, repo, "clone", "--quiet", "--no-hardlinks", repo, clone)
	git(t, clone, "apply", patch)
	for _, edit := range edits {
		b, err := os.ReadFile(filepath.Join(clone, edit.Path))
		if err != nil || string(b) != edit.Content {
			t.Fatalf("intended file %s: %q %v", edit.Path, b, err)
		}
	}
	if got := git(t, clone, "diff", "--name-only"); got != "src/a/base/file.txt\nsrc/b/candidate/file.txt" {
		t.Fatalf("wrong modified targets: %q", got)
	}
	if git(t, repo, "status", "--porcelain") != "" {
		t.Fatal("source changed during export")
	}
}

func TestPreparedExportIsSealedStableAndAppliesManagedGeneration(t *testing.T) {
	repo := repository(t)
	paths := []string{"src/a/base/file name.txt", "src/b/candidate/quote\".txt"}
	if runtime.GOOS == "windows" {
		paths[1] = "src/b/candidate/quote-safe.txt"
	}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, p)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, p), []byte("before\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "paths")
	dir := filepath.Join(t.TempDir(), "mission")
	if _, _, err := Snapshot(context.Background(), repo, dir, []string{"src/"}); err != nil {
		t.Fatal(err)
	}
	edits := []Edit{{paths[0], "keep a/base/value and b/candidate/value\n"}, {paths[1], "quote change\n"}, {"src/new space.txt", "added\n"}}
	ref, _, err := PrepareGeneration(dir, generationID, filepath.Join(dir, "base"), nil, edits, []string{"src/"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidate, _ := GenerationPath(dir, ref)
	result, err := PrepareExport(context.Background(), dir, generationID, candidate, "stable report\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyExport(dir, generationID, result); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(result.PatchPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "+keep a/base/value and b/candidate/value") {
		t.Fatal("rewrote hunk")
	}
	git(t, repo, "apply", "--check", result.PatchPath)
	prior, _ := os.Stat(result.PatchPath)
	again, err := PrepareExport(context.Background(), dir, generationID, candidate, "stable report\n")
	if err != nil || again != result {
		t.Fatalf("unstable retry: %+v %v", again, err)
	}
	next, _ := os.Stat(result.PatchPath)
	if !os.SameFile(prior, next) {
		t.Fatal("retry rewrote identical artifact")
	}
	if _, err := PrepareExport(context.Background(), dir, generationID, candidate, "different report\n"); err == nil {
		t.Fatal("overwrote different bundle")
	}
	if git(t, repo, "status", "--porcelain") != "" {
		t.Fatal("export changed source")
	}
}

func TestPreparedExportRejectsCorruptionUnknownOccupancyAndBounds(t *testing.T) {
	for _, kind := range []string{"patch", "report", "manifest", "extra", "symlink", "root-symlink", "unknown", "large-report", "outside", "identity", "path", "candidate-id", "candidate-digest", "base-digest"} {
		t.Run(kind, func(t *testing.T) {
			dir, source := generationFixture(t)
			ref, _, err := PrepareGeneration(dir, generationID, source, nil, []Edit{{"src/run.sh", "new\n"}}, []string{"src/"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			baseDigest, err := Digest(source)
			if err != nil {
				t.Fatal(err)
			}
			candidate, _ := GenerationPath(dir, ref)
			switch kind {
			case "unknown":
				os.MkdirAll(filepath.Join(dir, "exports", generationID), 0700)
			case "large-report":
				if _, err := PrepareExport(context.Background(), dir, generationID, candidate, strings.Repeat("x", 1024*1024+1)); err == nil {
					t.Fatal("unbounded report")
				}
				return
			case "outside":
				candidate = source
			case "root-symlink":
				os.Symlink(t.TempDir(), filepath.Join(dir, "exports"))
			}
			result, err := PrepareExport(context.Background(), dir, generationID, candidate, "report")
			if kind == "unknown" || kind == "outside" || kind == "root-symlink" {
				if err == nil {
					t.Fatal("unsafe export allowed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "patch":
				os.WriteFile(result.PatchPath, []byte("bad"), 0600)
			case "report":
				os.Remove(result.ReportPath)
			case "manifest":
				os.WriteFile(filepath.Join(filepath.Dir(result.PatchPath), "manifest.json"), []byte("{}"), 0600)
			case "extra":
				os.WriteFile(filepath.Join(filepath.Dir(result.PatchPath), "extra"), []byte("x"), 0600)
			case "symlink":
				os.Remove(result.ReportPath)
				os.Symlink(filepath.Join(source, "src/run.sh"), result.ReportPath)
			case "identity":
				result.ReportSHA256 = strings.Repeat("0", 64)
			case "path":
				result.PatchPath = filepath.Join(source, "src/run.sh")
			case "candidate-id", "candidate-digest", "base-digest":
				manifestPath := filepath.Join(filepath.Dir(result.PatchPath), "manifest.json")
				var seal exportManifest
				if err := readManifest(manifestPath, &seal); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "candidate-id":
					seal.Candidate.ID = strings.Repeat("a", 32)
				case "candidate-digest":
					seal.Candidate.Digest = strings.Repeat("b", 64)
				case "base-digest":
					seal.BaseDigest = strings.Repeat("c", 64)
				}
				b, err := json.Marshal(seal)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(manifestPath, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := VerifyExportBinding(dir, generationID, result, ref, baseDigest); err == nil {
				t.Fatal("accepted corrupt export")
			}
			if kind != "candidate-id" && kind != "candidate-digest" && kind != "base-digest" {
				if err := VerifyExport(dir, generationID, result); err == nil {
					t.Fatal("accepted corrupt export through legacy verifier")
				}
			}
			if _, err := PrepareExport(context.Background(), dir, generationID, candidate, "report"); err == nil && kind != "identity" && kind != "path" {
				t.Fatal("reused corrupt export")
			}
		})
	}
}
