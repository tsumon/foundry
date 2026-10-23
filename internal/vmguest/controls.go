package vmguest

import (
	"errors"
	"path/filepath"
	"strings"
)

const cgroupPath = "/sys/fs/cgroup/tests"

var limits = map[string]string{"memory.max": "1073741824", "memory.swap.max": "0", "memory.oom.group": "1", "pids.max": "128", "cpu.max": "100000 100000"}

// Kernel control I/O is injectable because mounting or mutating a host cgroup
// is forbidden. The production caller is exclusively guarded guest PID1.
func configureCgroup(read func(string) ([]byte, error), write func(string, string) error, mkdir func(string) error) error {
	b, err := read("/sys/fs/cgroup/cgroup.controllers")
	if err != nil {
		return err
	}
	for _, name := range []string{"memory", "pids", "cpu"} {
		if !containsWord(string(b), name) {
			return errors.New("required cgroup controller absent")
		}
	}
	if err := write("/sys/fs/cgroup/cgroup.subtree_control", "+memory +pids +cpu"); err != nil {
		return err
	}
	return createLimitedCgroup(cgroupPath, read, write, mkdir)
}

// A killed cgroup is never reused: some supported kernels retain a kill_seq
// that spuriously SIGKILLs later CLONE_INTO_CGROUP children. Exclusive creation
// also prevents accidentally adopting stale state. Every phase gets identical
// limits verified before its first child is admitted.
func createLimitedCgroup(group string, read func(string) ([]byte, error), write func(string, string) error, mkdir func(string) error) error {
	if err := mkdir(group); err != nil {
		return err
	}
	for name, value := range limits {
		if err := write(filepath.Join(group, name), value); err != nil {
			return err
		}
		b, err := read(filepath.Join(group, name))
		if err != nil || strings.TrimSpace(string(b)) != value {
			return errors.New("cgroup limit not enforced")
		}
	}
	return nil
}

func containsWord(s, w string) bool {
	for _, v := range strings.Fields(s) {
		if v == w {
			return true
		}
	}
	return false
}

func cleanupCgroup(group string, read func(string) ([]byte, error), write func(string, string) error, reap, pause func()) error {
	if err := write(filepath.Join(group, "cgroup.kill"), "1"); err != nil {
		return errors.New("cgroup cleanup failed")
	}
	for i := 0; i < 50; i++ {
		reap()
		b, err := read(filepath.Join(group, "cgroup.events"))
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(b), "\n") {
			if line == "populated 0" {
				return nil
			}
		}
		pause()
	}
	return errors.New("cgroup cleanup unconfirmed")
}
