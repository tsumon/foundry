package workspace

import (
	"context"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

// Inject at every actual write, file/directory sync, and publication boundary;
// all other filesystem operations remain real.
func TestGenerationFailureBoundariesPreserveSource(t *testing.T) {
	for _, primitive := range []string{"write", "sync", "publish"} {
		for nth := 1; ; nth++ {
			dir, source := generationFixture(t)
			before, _ := Digest(source)
			fsys := durableFS()
			count := 0
			injected := errors.New("injected failure")
			switch primitive {
			case "write":
				actual := fsys.write
				fsys.write = func(p string, b []byte, m os.FileMode) error {
					count++
					if count == nth {
						return injected
					}
					return actual(p, b, m)
				}
			case "sync":
				actual := fsys.sync
				fsys.sync = func(p string) error {
					count++
					if count == nth {
						return injected
					}
					return actual(p)
				}
			case "publish":
				actual := fsys.publish
				fsys.publish = func(a, b string) error {
					count++
					if count == nth {
						return injected
					}
					return actual(a, b)
				}
			}
			ref, _, err := prepareGeneration(fsys, dir, generationID, source, nil, []Edit{{"src/run.sh", "one\n"}, {"src/new.txt", "two\n"}}, []string{"src/"}, nil)
			after, digestErr := Digest(source)
			if digestErr != nil || before != after {
				t.Fatalf("%s/%d mutated source: %v", primitive, nth, digestErr)
			}
			if count < nth {
				if err != nil {
					t.Fatal(err)
				}
				break
			}
			if !errors.Is(err, injected) || ref.ID != "" {
				t.Fatalf("%s/%d exposed failed preparation: %+v %v", primitive, nth, ref, err)
			}
			// A post-publication parent-sync failure may retain a complete orphan; it
			// must never return authority or a partial published generation.
			final := filepath.Join(dir, "generations", generationID)
			if _, statErr := os.Stat(final); statErr == nil {
				digest, digestErr := Digest(filepath.Join(final, "tree"))
				if digestErr != nil {
					t.Fatal(digestErr)
				}
				if err := VerifyGeneration(dir, domain.GenerationRef{ID: generationID, Digest: digest}); err != nil {
					t.Fatal("partial published tree", err)
				}
			}
			t.Log(fmt.Sprintf("%s boundary %d preserves full source digest", primitive, nth))
		}
	}
}

func TestExportFailureBoundariesPreserveSelectedGeneration(t *testing.T) {
	for _, primitive := range []string{"write", "sync", "publish"} {
		for nth := 1; ; nth++ {
			dir, source := generationFixture(t)
			ref, _, err := PrepareGeneration(dir, generationID, source, nil, []Edit{{"src/run.sh", "changed\n"}}, []string{"src/"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			candidate, _ := GenerationPath(dir, ref)
			before, _ := Digest(source)
			fsys := durableFS()
			count := 0
			injected := errors.New("injected failure")
			switch primitive {
			case "write":
				actual := fsys.write
				fsys.write = func(p string, b []byte, m os.FileMode) error {
					count++
					if count == nth {
						return injected
					}
					return actual(p, b, m)
				}
			case "sync":
				actual := fsys.sync
				fsys.sync = func(p string) error {
					count++
					if count == nth {
						return injected
					}
					return actual(p)
				}
			case "publish":
				actual := fsys.publish
				fsys.publish = func(a, b string) error {
					count++
					if count == nth {
						return injected
					}
					return actual(a, b)
				}
			}
			result, err := prepareExport(context.Background(), fsys, dir, generationID, candidate, "stable report")
			if after, digestErr := Digest(source); digestErr != nil || after != before {
				t.Fatalf("%s/%d mutated base", primitive, nth)
			}
			if err := VerifyGeneration(dir, ref); err != nil {
				t.Fatalf("%s/%d mutated candidate: %v", primitive, nth, err)
			}
			if count < nth {
				if err != nil {
					t.Fatal(err)
				}
				break
			}
			if !errors.Is(err, injected) || result != (PreparedExport{}) {
				t.Fatalf("%s/%d returned prepared output: %+v %v", primitive, nth, result, err)
			}
			// Explicit retry is safe both before publication and after parent-sync
			// failure, and can only return a fully verified identical pair.
			retried, err := PrepareExport(context.Background(), dir, generationID, candidate, "stable report")
			if err != nil {
				t.Fatalf("%s/%d retry: %v", primitive, nth, err)
			}
			if err := VerifyExport(dir, generationID, retried); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s boundary %d preserves base and selected generation", primitive, nth)
		}
	}
}

func TestPublishNeverReplacesOccupiedDirectory(t *testing.T) {
	dir := t.TempDir()
	stage := filepath.Join(dir, "stage")
	final := filepath.Join(dir, "final")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(final, 0700); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(final)
	if err := durableFS().publish(stage, final); err == nil {
		t.Fatal("replaced occupied empty destination")
	}
	after, _ := os.Stat(final)
	if !os.SameFile(before, after) {
		t.Fatal("destination changed")
	}
}
