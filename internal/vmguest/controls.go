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
	if err := mkdir(cgroupPath); err != nil {
		return err
	}
	for name, value := range limits {
		if err := write(filepath.Join(cgroupPath, name), value); err != nil {
			return err
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
