//go:build !windows

package testutil

import "syscall"

func SpecialFile(p string) error { return syscall.Mkfifo(p, 0600) }
