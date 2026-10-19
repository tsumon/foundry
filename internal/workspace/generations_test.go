package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func TestGenerationProposalValidationDoesNotClassifyFilesystemFailure(t *testing.T) {
	dir, source := generationFixture(t)
	_, _, err := PrepareGeneration(dir, generationID, source, nil, []Edit{{"src/run.sh", strings.Repeat("x", 512*1024+1)}}, []string{"src/"}, nil)
	var invalid *ProposalValidationError
	if !errors.As(err, &invalid) || invalid.Unwrap() == nil {
		t.Fatal("proposal error is not identified", err)
	}
	if err = os.WriteFile(filepath.Join(dir, "generations"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err = PrepareGeneration(dir, generationID, source, nil, []Edit{{"src/run.sh", "valid edit"}}, []string{"src/"}, nil)
	if err == nil || errors.As(err, &invalid) {
		t.Fatal("filesystem failure relabeled model denial", err)
	}
}

const generationID = "0123456789abcdef0123456789abcdef"

func generationFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "base")
	for p, b := range map[string]string{"src/run.sh": "old\n", "src/nested/keep.txt": "keep\n", "tests/check.txt": "protected\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(source, p)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, p), []byte(b), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(source, "src/run.sh"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir, source
}

func TestPrepareGenerationCopiesCompleteTreeWithoutMutatingSource(t *testing.T) {
	dir, source := generationFixture(t)
	before, err := Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	ref, changes, err := PrepareGeneration(dir, generationID, source, nil, []Edit{{"src/run.sh", "new\n"}, {"src/new.txt", "added\n"}}, []string{"src/"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := GenerationPath(dir, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || changes[0].Before != "old\n" || changes[1].After != "added\n" {
		t.Fatalf("changes: %+v", changes)
	}
	for p, want := range map[string]string{"src/run.sh": "new\n", "src/new.txt": "added\n", "src/nested/keep.txt": "keep\n", "tests/check.txt": "protected\n"} {
		b, err := os.ReadFile(filepath.Join(tree, p))
		if err != nil || string(b) != want {
			t.Fatalf("%s: %q %v", p, b, err)
		}
		original, err := os.Stat(filepath.Join(source, p))
		if err == nil {
			copied, _ := os.Stat(filepath.Join(tree, p))
			if os.SameFile(original, copied) {
				t.Fatalf("hardlinked %s", p)
			}
		}
	}
	original, _ := os.Stat(filepath.Join(source, "src/run.sh"))
	copied, _ := os.Stat(filepath.Join(tree, "src/run.sh"))
	if copied.Mode().Perm() != 0755 || os.SameFile(original, copied) {
		t.Fatal("lost executable bits or hardlinked source")
	}
	if after, _ := Digest(source); after != before {
		t.Fatal("source changed")
	}
	if err := VerifyGeneration(dir, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "candidate")); !os.IsNotExist(err) {
		t.Fatal("mutable alias created")
	}
}

func TestResetGenerationCopiesBaseWhileRetainingPriorGeneration(t *testing.T) {
	dir, source := generationFixture(t)
	first, _, err := PrepareGeneration(dir, generationID, source, nil, []Edit{{"src/run.sh", "edited\n"}}, []string{"src/"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reset, changes, err := PrepareGeneration(dir, "2123456789abcdef0123456789abcdef", source, &first, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	baseDigest, _ := Digest(source)
	if reset.Digest != baseDigest || len(changes) != 0 {
		t.Fatalf("reset differs from base: %+v %+v", reset, changes)
	}
	if err := VerifyGeneration(dir, first); err != nil {
		t.Fatal("prior generation lost", err)
	}
}

func TestGenerationSealRejectsTamperMissingAndIdentityMismatch(t *testing.T) {
	for _, kind := range []string{"content", "mode", "missing", "extra", "seal", "identity"} {
		t.Run(kind, func(t *testing.T) {
			dir, source := generationFixture(t)
			ref, _, err := PrepareGeneration(dir, generationID, source, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			tree, _ := GenerationPath(dir, ref)
			switch kind {
			case "content":
				os.WriteFile(filepath.Join(tree, "src/run.sh"), []byte("tamper"), 0755)
			case "mode":
				os.Chmod(filepath.Join(tree, "src/run.sh"), 0644)
			case "missing":
				os.Remove(filepath.Join(tree, "src/run.sh"))
			case "extra":
				os.WriteFile(filepath.Join(tree, "extra"), []byte("extra"), 0644)
			case "seal":
				os.Remove(filepath.Join(filepath.Dir(tree), "manifest.json"))
			case "identity":
				ref.Digest = strings.Repeat("0", 64)
			}
			if err := VerifyGeneration(dir, ref); err == nil {
				t.Fatal("accepted corrupt generation")
			}
		})
	}
}

func TestGenerationRejectsUnsafeInputAndOccupiedDestination(t *testing.T) {
	for _, kind := range []string{"traversal", "protected", "symlink", "root-symlink", "managed-symlink", "case", "directory-case", "oversize", "id", "occupied", "parent", "outside"} {
		t.Run(kind, func(t *testing.T) {
			dir, source := generationFixture(t)
			id := generationID
			edits := []Edit{{"src/run.sh", "new\n"}}
			var parent *domain.GenerationRef
			switch kind {
			case "traversal":
				edits[0].Path = "src/../escape"
			case "protected":
				edits[0].Path = "tests/check.txt"
			case "symlink":
				os.Symlink("run.sh", filepath.Join(source, "src/link"))
			case "root-symlink":
				link := filepath.Join(t.TempDir(), "link")
				os.Symlink(source, link)
				source = link
			case "managed-symlink":
				os.Symlink(t.TempDir(), filepath.Join(dir, "generations"))
			case "case":
				edits = append(edits, Edit{"src/RUN.sh", "collision"})
			case "directory-case":
				edits = append(edits, Edit{"SRC/other.txt", "collision"})
			case "oversize":
				edits[0].Content = strings.Repeat("x", 512*1024+1)
			case "id":
				id = "../escape"
			case "occupied":
				os.MkdirAll(filepath.Join(dir, "generations", id), 0700)
			case "parent":
				parent = &domain.GenerationRef{ID: generationID, Digest: strings.Repeat("0", 64)}
			case "outside":
				source = t.TempDir()
			}
			before, _ := Digest(source)
			if _, _, err := PrepareGeneration(dir, id, source, parent, edits, []string{"src/", "SRC/", "tests/"}, nil); err == nil {
				t.Fatal("accepted unsafe generation")
			}
			if after, _ := Digest(source); before != after {
				t.Fatal("source changed")
			}
		})
	}
}

func TestGenerationParentBindsVerifiedSource(t *testing.T) {
	dir, source := generationFixture(t)
	first, _, err := PrepareGeneration(dir, generationID, source, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := GenerationPath(dir, first)
	next, _, err := PrepareGeneration(dir, "1123456789abcdef0123456789abcdef", path, &first, []Edit{{"src/run.sh", "next\n"}}, []string{"src/"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyGeneration(dir, next); err != nil {
		t.Fatal(err)
	}
	if err := VerifyGeneration(dir, first); err != nil {
		t.Fatal("prior generation changed", err)
	}
}
