//go:build darwin || linux

package dependencyadmission

import (
	"golang.org/x/sys/unix"
	"os"
	"strings"
	"syscall"
)

func pythonHoldDirectory(p string) (pythonDirectoryHandle, error) {
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), p), nil
}
func pythonSingleLink(_ *os.File, info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Nlink == 1
}
func pythonPrivateDirectory(_ string, info os.FileInfo) error {
	if info.Mode().Perm() != 0700 {
		return errInvalid
	}
	return nil
}
