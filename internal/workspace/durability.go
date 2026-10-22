package workspace

import (
	"foundry/internal/platformfs"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type filesystem struct {
	write   func(string, []byte, os.FileMode) error
	sync    func(string) error
	publish func(string, string) error
}

func durableFS() filesystem {
	return filesystem{write: func(p string, b []byte, mode os.FileMode) error {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		n, err := f.Write(b)
		if err == nil && n != len(b) {
			err = io.ErrShortWrite
		}
		if err == nil {
			err = f.Chmod(mode)
		}
		if err == nil {
			err = platformfs.FlushWritten(f)
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	}, sync: platformfs.SyncPath, publish: publishAbsent}
}

func syncTree(fsys filesystem, dir string) error {
	var dirs []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		return fsys.sync(p)
	})
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, p := range dirs {
		if err := fsys.sync(p); err != nil {
			return err
		}
	}
	return nil
}
