package workspace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

type measuredTreeFS struct {
	fs.FS
	requests []int
	fetched  int
}

func (m *measuredTreeFS) Open(name string) (fs.File, error) {
	f, err := m.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return &measuredTreeDir{ReadDirFile: f.(fs.ReadDirFile), owner: m}, nil
}

type measuredTreeDir struct {
	fs.ReadDirFile
	owner *measuredTreeFS
}

func (d *measuredTreeDir) ReadDir(n int) ([]fs.DirEntry, error) {
	d.owner.requests = append(d.owner.requests, n)
	entries, err := d.ReadDirFile.ReadDir(n)
	d.owner.fetched += len(entries)
	return entries, err
}

func TestBoundedTreeReadsIncrementalBatches(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5000; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("d-%04d", i)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	measured := &measuredTreeFS{FS: dir.FS()}
	visited := 0
	err = WalkBoundedTree(measured, func(_ string, _ fs.DirEntry) error {
		visited++
		return nil
	})
	if err == nil || visited > 4096 || measured.fetched > 4096 || len(measured.requests) == 0 {
		t.Fatalf("unbounded traversal: visited=%d fetched=%d reads=%d error=%v", visited, measured.fetched, len(measured.requests), err)
	}
	for _, n := range measured.requests {
		if n <= 0 || n > 64 {
			t.Fatalf("unbounded actual ReadDir(%d)", n)
		}
	}
}

func TestReadTreeRejectsExcessiveDirectories(t *testing.T) {
	for _, shape := range []string{"wide", "deep"} {
		t.Run(shape, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("unchanged"), 0755); err != nil {
				t.Fatal(err)
			}
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
			before := fixtureTreeDigest(t, root)
			if _, err := Digest(root); err == nil {
				t.Error("excessive directory tree accepted")
			}
			if _, err := Apply(root, []Edit{{Path: "file.txt", Content: "changed"}}, []string{"file.txt"}, nil); err == nil {
				t.Error("edit in excessive directory tree accepted")
			}
			if after := fixtureTreeDigest(t, root); after != before {
				t.Fatal("rejected tree changed")
			}
		})
	}
}
