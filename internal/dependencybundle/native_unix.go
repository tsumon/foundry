//go:build darwin || linux

package dependencybundle

import (
	"crypto/rand"
	"encoding/hex"
	"golang.org/x/sys/unix"
	"os"
	"strings"
	"syscall"
)

// Open every absolute component without following links, retaining the terminal
// directory handle to check the os.Root endpoint even if names race afterward.
func holdDirectory(p string) (directoryHandle, error) {
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
func privateDirectory(_ string, info os.FileInfo) error {
	if info.Mode().Perm() != 0700 {
		return ErrInvalid
	}
	return nil
}
func payloadMode(m os.FileMode) bool {
	return m.IsRegular() && m.Perm()&0111 == 0 && m&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
}
func singleLink(_ *os.File, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink != 1 {
		return ErrIntegrity
	}
	return nil
}
func makeDirectory(r *os.Root, p string) error {
	e := r.Mkdir(p, 0700)
	if os.IsExist(e) {
		info, se := r.Lstat(p)
		if se == nil && info.IsDir() && info.Mode().Perm() == 0700 {
			return nil
		}
	}
	return e
}
func syncFile(f *os.File) error { return f.Sync() }
func syncRoot(r *os.Root) error {
	f, e := r.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

func makeStage(r *os.Root) (string, error) {
	var random [16]byte
	if _, e := rand.Read(random[:]); e != nil {
		return "", e
	}
	name := ".partial-" + hex.EncodeToString(random[:])
	if e := r.Mkdir(name, 0700); e != nil {
		return "", e
	}
	return name, nil
}
