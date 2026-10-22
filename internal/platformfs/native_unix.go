//go:build !windows

package platformfs

import (
	"errors"
	"os"
	"syscall"
)

func OpenLock(path string) (*File, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("controller lock must be regular")
	}
	return &File{File: f}, nil
}

func OpenRegular(path string) (*File, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	return &File{File: f}, nil
}
func OpenRootRegular(root *os.Root, path string) (*File, error) {
	f, e := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	return &File{File: f}, nil
}
func CanonicalMode(mode os.FileMode) (os.FileMode, error) { return mode.Perm(), nil }
func ValidateRelative(string) error                       { return nil }
func ValidateAbsolute(string) error                       { return nil }
func ValidateDirectory(string) error                      { return nil }
func PrivateDirectory(path string) error                  { return os.MkdirAll(path, 0700) }
func PrivateTemp(parent, prefix string) (string, error)   { return os.MkdirTemp(parent, prefix) }
func FlushWritten(*os.File) error                         { return nil }
func SyncPath(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
