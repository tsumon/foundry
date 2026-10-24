//go:build !windows

package vmruntime

import (
	"errors"
	"os"
	"syscall"
)

func checkArchiveDirectory(path string) error {
	s, err := os.Lstat(path)
	if err != nil || !s.IsDir() || s.Mode().Perm()&0077 != 0 {
		return errors.New("runtime archive is not private")
	}
	owner, ok := s.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return errors.New("runtime archive owner mismatch")
	}
	return nil
}
