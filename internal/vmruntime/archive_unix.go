//go:build !windows

package vmruntime

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Absolute destination operations are safe only while every ancestor is
// controlled by this user or root. Inspect from the filesystem root down so an
// untrusted owner/writer cannot replace a component after it is checked. Sticky
// shared directories protect the next trusted-owned component (e.g. /private/tmp).
// This is the existing local-user trust boundary, not same-user process isolation.
func protectArchiveParent(path string) (func(), error) {
	if err := canonicalDirectory(path); err != nil {
		return nil, err
	}
	var chain []string
	for p := path; ; p = filepath.Dir(p) {
		chain = append(chain, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		info, err := os.Lstat(chain[i])
		if err != nil || !info.IsDir() {
			return nil, errors.New("unsafe runtime archive ancestor")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (owner.Uid != 0 && owner.Uid != uint32(os.Geteuid())) {
			return nil, errors.New("untrusted runtime archive ancestor owner")
		}
		if info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return nil, errors.New("mutable runtime archive ancestor")
		}
	}
	if err := checkArchiveDirectory(path); err != nil {
		return nil, err
	}
	return func() {}, nil
}

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
