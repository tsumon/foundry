//go:build !windows

package keychaincheck

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

type journalFile struct{ *os.File }

func createDesktopDirectory(path string) error { return os.Mkdir(path, 0700) }
func validateDesktopDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) == path {
		return errors.New("desktop diagnostic directory must be clean and absolute")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("desktop diagnostic directory must exist without symlinks")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("desktop diagnostic directory must be private")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return errors.New("desktop diagnostic directory must be user-owned")
	}
	return nil
}

func createPrivateJournalFile(path string) (*journalFile, error) {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	return &journalFile{os.NewFile(uintptr(fd), path)}, nil
}
func openPrivateJournalFile(path string) (*journalFile, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return &journalFile{os.NewFile(uintptr(fd), path)}, nil
}
func createPrivateJournalTemp(path string) (*journalFile, error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return nil, err
	}
	return &journalFile{f}, nil
}
func secureJournalFile(f *journalFile) error     { return f.Chmod(0600) }
func privateJournalMode(info os.FileInfo) bool   { return info.Mode().Perm() == 0600 }
func replaceJournal(source, target string) error { return os.Rename(source, target) }
