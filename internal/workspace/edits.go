package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/platformfs"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type Edit struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func readTree(dir string) (map[string][]byte, error) {
	if err := platformfs.ValidateDirectory(dir); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("workspace root must be a real directory")
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	files := map[string][]byte{}
	seen := map[string]bool{}
	total := 0
	e = WalkBoundedTree(root.FS(), func(p string, d fs.DirEntry) error {
		if p == "." {
			return nil
		}
		if e := safePath(p); e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink refused")
		}
		if seen[strings.ToLower(p)] {
			return errors.New("case collision")
		}
		seen[strings.ToLower(p)] = true
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || info.Size() > MaxSnapshotBytes {
			return errors.New("unsupported candidate file")
		}
		f, e := platformfs.OpenRootRegular(root, p)
		if e != nil {
			return e
		}
		defer f.Close()
		opened, e := f.Stat()
		if e != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			return errors.New("candidate file changed during read")
		}
		if _, e := platformfs.CanonicalMode(opened.Mode()); e != nil {
			return e
		}
		// Files may grow after the metadata check. Bound the actual read too.
		b, e := io.ReadAll(io.LimitReader(f, int64(MaxSnapshotBytes-total)+1))
		if e != nil {
			return e
		}
		total += len(b)
		if len(files) >= 500 || total > MaxSnapshotBytes || !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
			return errors.New("candidate bounds exceeded")
		}
		files[p] = b
		return nil
	})
	return files, e
}
func writeFile(dir, p string, b []byte) error {
	root, e := os.OpenRoot(dir)
	if e != nil {
		return e
	}
	defer root.Close()
	if e := root.MkdirAll(filepath.ToSlash(filepath.Dir(p)), 0755); e != nil {
		return e
	}
	return root.WriteFile(p, b, 0644)
}
func Digest(dir string) (string, error) {
	files, e := readTree(dir)
	if e != nil {
		return "", e
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		return "", e
	}
	defer root.Close()
	keys := make([]string, 0, len(files))
	for p := range files {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, p := range keys {
		info, e := root.Stat(p)
		if e != nil {
			return "", e
		}
		mode, e := platformfs.CanonicalMode(info.Mode())
		if e != nil {
			return "", e
		}
		fmt.Fprintf(h, "%d:%s:%d:%o:", len(p), p, len(files[p]), mode)
		h.Write(files[p])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func Context(dir string, manifest []domain.FileMeta) (map[string]string, error) {
	files, e := readTree(dir)
	if e != nil {
		return nil, e
	}
	out := map[string]string{}
	total := 0
	for _, f := range manifest {
		b, ok := files[f.Path]
		if !ok || hash(b) != f.SHA256 || int64(len(b)) != f.Bytes {
			return nil, errors.New("context manifest drift")
		}
		total += len(b)
		out[f.Path] = string(b)
	}
	if len(out) != len(manifest) || len(out) > 30 || total > 200*1024 {
		return nil, errors.New("invalid context bounds")
	}
	return out, nil
}
func Apply(dir string, edits []Edit, scope, protected []string) ([]domain.Change, error) {
	files, e := readTree(dir)
	if e != nil {
		return nil, e
	}
	if len(edits) == 0 || len(edits) > 30 {
		return nil, errors.New("edits must contain 1–30 files")
	}
	seen := map[string]bool{}
	changes := []domain.Change{}
	for _, edit := range edits {
		if e := ValidateEditPath(edit.Path, scope, protected); e != nil {
			return nil, e
		}
		if len(edit.Content) > 512*1024 || !utf8.ValidString(edit.Content) || strings.ContainsRune(edit.Content, 0) {
			return nil, errors.New("edit exceeds text bounds")
		}
		low := strings.ToLower(edit.Path)
		if seen[low] {
			return nil, errors.New("duplicate edit")
		}
		seen[low] = true
		for p := range files {
			if strings.EqualFold(p, edit.Path) && p != edit.Path {
				return nil, errors.New("case collision")
			}
		}
		changes = append(changes, domain.Change{Path: edit.Path, Before: string(files[edit.Path]), After: edit.Content})
	}
	for _, edit := range edits {
		if e := writeFile(dir, edit.Path, []byte(edit.Content)); e != nil {
			return nil, e
		}
	}
	return changes, nil
}
func Reset(base, candidate string) error {
	files, e := readTree(base)
	if e != nil {
		return e
	}
	if _, e := readTree(candidate); e != nil {
		return e
	}
	root, e := os.OpenRoot(candidate)
	if e != nil {
		return e
	}
	defer root.Close()
	entries, e := fs.ReadDir(root.FS(), ".")
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if e := root.RemoveAll(entry.Name()); e != nil {
			return e
		}
	}
	src, e := os.OpenRoot(base)
	if e != nil {
		return e
	}
	defer src.Close()
	for p, b := range files {
		if e := writeFile(candidate, p, b); e != nil {
			return e
		}
		info, e := src.Stat(p)
		if e != nil {
			return e
		}
		mode, e := platformfs.CanonicalMode(info.Mode())
		if e != nil {
			return e
		}
		if e := root.Chmod(p, mode); e != nil {
			return e
		}
	}
	return nil
}
