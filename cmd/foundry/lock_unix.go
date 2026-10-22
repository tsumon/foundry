//go:build !windows

package main

import (
	"os"
	"syscall"
)

func lockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
func unlockExclusive(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
