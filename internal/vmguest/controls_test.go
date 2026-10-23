package vmguest

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type controlFixture struct {
	controllers, missing string
	writes               map[string]string
	created              bool
}

func (c *controlFixture) read(p string) ([]byte, error) {
	if strings.HasSuffix(p, "cgroup.controllers") {
		return []byte(c.controllers), nil
	}
	return nil, errors.New("unknown control")
}
func (c *controlFixture) write(p, v string) error {
	if strings.HasSuffix(p, c.missing) && c.missing != "" {
		return errors.New("unavailable control")
	}
	c.writes[p] = v
	return nil
}
func (c *controlFixture) mkdir(p string) error {
	if p != "/sys/fs/cgroup/tests" {
		return errors.New("unexpected group")
	}
	c.created = true
	return nil
}
func TestRequiredCgroupControllersAndEveryLimitFailClosed(t *testing.T) {
	for _, controllers := range []string{"cpu pids", "memory pids", "cpu memory", "cpu memory pids"} {
		f := &controlFixture{controllers: controllers, writes: map[string]string{}}
		err := configureCgroup(f.read, f.write, f.mkdir)
		if controllers != "cpu memory pids" {
			if err == nil || f.created || len(f.writes) > 0 {
				t.Fatalf("missing controller accepted: %+v", f)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"/sys/fs/cgroup/cgroup.subtree_control": "+memory +pids +cpu", "/sys/fs/cgroup/tests/memory.max": "1073741824", "/sys/fs/cgroup/tests/memory.swap.max": "0", "/sys/fs/cgroup/tests/memory.oom.group": "1", "/sys/fs/cgroup/tests/pids.max": "128", "/sys/fs/cgroup/tests/cpu.max": "100000 100000"}
		if !f.created || !reflect.DeepEqual(f.writes, want) {
			t.Fatalf("wrong enforced limits: %+v", f)
		}
	}
	for _, missing := range []string{"memory.max", "memory.swap.max", "memory.oom.group", "pids.max", "cpu.max", "cgroup.subtree_control"} {
		f := &controlFixture{controllers: "memory pids cpu", missing: missing, writes: map[string]string{}}
		if err := configureCgroup(f.read, f.write, f.mkdir); err == nil {
			t.Fatalf("missing %s accepted", missing)
		}
	}
}
