//go:build linux

package vmguest

import (
	"golang.org/x/sys/unix"
	"io"
	"os"
	"runtime"
)

func fdStatV2(fd int) (unix.Stat_t, int, error) {
	var s unix.Stat_t
	e := unix.Fstat(fd, &s)
	if e != nil {
		return s, 0, e
	}
	flags, e := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	return s, flags, e
}
func privatePipeV2(fd int) bool {
	s, f, e := fdStatV2(fd)
	return e == nil && s.Mode&unix.S_IFMT == unix.S_IFIFO && s.Uid == 0 && s.Gid == 0 && s.Mode&0777 == 0600 && f&unix.O_ACCMODE == unix.O_WRONLY
}
func childMainV2(p phaseV2) error {
	runtime.LockOSThread()
	// This static entry runs before loading any dynamic interpreter or project.
	if e := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); e != nil {
		return e
	}
	nnp, e := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if e != nil {
		return e
	}
	groups, e := os.Getgroups()
	if e != nil {
		return e
	}
	cwd, e := os.Getwd()
	if e != nil {
		return e
	}
	in, flags, e := fdStatV2(0)
	if e != nil {
		return e
	}
	contract, cf, e := fdStatV2(4)
	if e != nil {
		return e
	}
	seals, e := unix.FcntlInt(4, unix.F_GET_SEALS, 0)
	if e != nil {
		return e
	}
	sealMask := unix.F_SEAL_SEAL | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE
	readonly := contract.Mode&unix.S_IFMT == unix.S_IFREG && contract.Uid == 0 && contract.Gid == 0 && contract.Mode&0777 == 0400 && contract.Size > 0 && contract.Size <= 1<<20 && cf&unix.O_ACCMODE == unix.O_RDONLY
	report := privatePipeV2(3)
	if !p.test() {
		if _, e := unix.FcntlInt(3, unix.F_GETFD, 0); e != unix.EBADF {
			return errGuestV2
		}
	}
	o := childObservationV2{uid: os.Getuid(), euid: os.Geteuid(), gid: os.Getgid(), egid: os.Getegid(), ppid: os.Getppid(), nnp: nnp, groups: groups, env: os.Environ(), cwd: cwd, stdinNull: in.Mode&unix.S_IFMT == unix.S_IFCHR && uint64(in.Rdev) == unix.Mkdev(1, 3) && flags&unix.O_ACCMODE == unix.O_RDONLY, stdoutPipe: privatePipeV2(1), stderrPipe: privatePipeV2(2), reportPipe: report, contractReadonly: readonly, contractSealed: seals&sealMask == sealMask, privateDescriptors: true}
	if validateChildObservationV2(p, o) != nil {
		return errGuestV2
	}
	f := os.NewFile(4, "owned-child-contract")
	raw, e := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if e != nil {
		return e
	}
	if _, e = parseChildContractV2(raw, p); e != nil {
		return e
	}
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	// Verify the same immutable roots and inaccessible control/PID1 boundary
	// from the actual credential-dropped process before the dynamic loader.
	if e = checkPreparedLinuxV2(); e != nil {
		return e
	}
	for _, path := range []string{"/dev/hvc0", "/dev/ttyS0", "/dev/console", "/proc/1/mem", "/proc/1/environ", "/proc/1/fd/0"} {
		for _, flag := range []int{os.O_RDONLY, os.O_WRONLY} {
			f, e := os.OpenFile(path, flag, 0)
			if e == nil {
				f.Close()
				return errGuestV2
			}
		}
	}
	if e = unix.CloseRange(5, ^uint(0), 0); e != nil {
		return e
	}
	path, args, e := executionV2(p)
	if e != nil {
		return e
	}
	return unix.Exec(path, append([]string{path}, args...), append([]string{}, envV2...))
}
