package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryMaterialDirectoryBounds(t *testing.T) {
	for _, shape := range []string{"wide", "deep"} {
		t.Run(shape, func(t *testing.T) {
			root := t.TempDir()
			path := root
			count := 5000
			if shape == "deep" {
				count = 65
			}
			for i := 0; i < count; i++ {
				if shape == "wide" {
					path = filepath.Join(root, fmt.Sprintf("dir-%04d", i))
				} else {
					path = filepath.Join(path, "d")
				}
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := readCodingReviewTree(root); err == nil {
				t.Fatal("excessive directory tree accepted")
			}
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				t.Fatal("rejected directory was changed", err)
			}
		})
	}
}

// The reread must enforce its own bounds and containment even if a preceding
// digest check observed a smaller, ordinary tree before replacement.
func TestRecoveryMaterialRereadBoundsAndContainment(t *testing.T) {
	for _, kind := range []string{"ordinary", "oversized", "symlink", "symlink_root"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "file.txt")
			if kind == "symlink" {
				outside := filepath.Join(t.TempDir(), "source.txt")
				if err := os.WriteFile(outside, []byte("source must not enter review"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte("bounded"), 0755); err != nil {
					t.Fatal(err)
				}
				if kind == "oversized" {
					if err := os.Truncate(path, 10*1024*1024+1); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "symlink_root" {
					link := filepath.Join(t.TempDir(), "root")
					if err := os.Symlink(root, link); err != nil {
						t.Fatal(err)
					}
					root = link
				}
			}
			files, err := readCodingReviewTree(root)
			if kind == "ordinary" {
				if err != nil || len(files) != 1 || string(files["file.txt"].content) != "bounded" || files["file.txt"].mode.Perm() != 0755 {
					t.Fatal(files, err)
				}
			} else if err == nil {
				t.Fatal("unsafe replacement read was accepted")
			}
		})
	}
}
