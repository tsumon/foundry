package workspace

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

func checkTreeShape(entries, depth int) error {
	if entries > maxTreeEntries || depth > maxTreeDepth {
		return errors.New("tree entry or depth bound exceeded")
	}
	return nil
}

// Git lists files without their parent directories. Count those implicit entries
// against the same shape policy before inspection can advertise a ready import.
func recordTreePath(entries map[string]bool, p string) error {
	depth := strings.Count(p, "/") + 1
	for p != "." && !entries[p] {
		if err := checkTreeShape(len(entries)+1, depth); err != nil {
			return err
		}
		entries[p] = true
		p, depth = path.Dir(p), depth-1
	}
	return nil
}

const (
	maxTreeEntries = 4096 // Includes the root and empty directories.
	maxTreeDepth   = 64   // Root is depth zero.
	treeReadBatch  = 64
)

// WalkBoundedTree reads a caller-owned, root-contained filesystem (os.Root.FS)
// incrementally. Unlike fs.WalkDir it never loads a whole directory. Enumeration
// is unsorted; consumers that hash files must retain their own canonical order.
// It rejects symlinks and bounds all fetched entries, not just visited files.
func WalkBoundedTree(root fs.FS, visit func(string, fs.DirEntry) error) error {
	info, err := fs.Stat(root, ".")
	if err != nil {
		return err
	}
	entries := 1
	var walk func(string, fs.DirEntry, int) error
	walk = func(p string, entry fs.DirEntry, depth int) error {
		if err := checkTreeShape(entries, depth); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink refused")
		}
		if err := visit(p, entry); err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		before, err := entry.Info()
		if err != nil {
			return err
		}
		file, err := root.Open(p)
		if err != nil {
			return err
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
			return errors.New("tree directory changed during read")
		}
		dir, ok := file.(fs.ReadDirFile)
		if !ok {
			return errors.New("bounded directory reads unavailable")
		}
		for {
			// Fetch one extra entry to detect overflow; never buffer more than
			// 4097 entries in total, including ancestors' pending batches.
			batch, readErr := dir.ReadDir(min(treeReadBatch, maxTreeEntries-entries+1))
			entries += len(batch)
			if err := checkTreeShape(entries, depth); err != nil {
				return err
			}
			if readErr != nil && readErr != io.EOF {
				return readErr
			}
			for _, child := range batch {
				if err := walk(path.Join(p, child.Name()), child, depth+1); err != nil {
					return err
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if len(batch) == 0 {
				return io.ErrNoProgress
			}
		}
	}
	return walk(".", fs.FileInfoToDirEntry(info), 0)
}
