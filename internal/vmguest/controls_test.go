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
	if v, ok := c.writes[p]; ok {
		return []byte(v + "\n"), nil
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

func TestEachChildRequiresFreshExclusiveCgroupAndVerifiedLimits(t *testing.T) {
	existing := map[string]bool{cgroupPath: true}
	writes := map[string]string{}
	mkdir := func(p string) error {
		if existing[p] {
			return errors.New("stale cgroup")
		}
		existing[p] = true
		return nil
	}
	read := func(p string) ([]byte, error) { return []byte(writes[p] + "\n"), nil }
	write := func(p, v string) error { writes[p] = v; return nil }
	if err := createLimitedCgroup(cgroupPath, read, write, mkdir); err == nil || len(writes) != 0 {
		t.Fatal("previously used cgroup accepted")
	}
	for _, group := range []string{cgroupPath + "-1", cgroupPath + "-2"} {
		if err := createLimitedCgroup(group, read, write, mkdir); err != nil {
			t.Fatal(err)
		}
		if writes[group+"/memory.max"] != "1073741824" || writes[group+"/pids.max"] != "128" || writes[group+"/cpu.max"] != "100000 100000" {
			t.Fatalf("new group lacks limits: %v", writes)
		}
	}
	if err := createLimitedCgroup(cgroupPath+"-3", func(string) ([]byte, error) { return []byte("wrong"), nil }, write, mkdir); err == nil {
		t.Fatal("unverified limits accepted")
	}
}

func TestCleanupKillsOnlyExactPhaseAndRequiresEmptyGroup(t *testing.T) {
	for _, tc := range []struct {
		name, events string
		writeErr     bool
		wantErr      bool
	}{{"empty", "populated 0\nfrozen 0\n", false, false}, {"live", "populated 1\nfrozen 0\n", false, true}, {"malformed", "populated 00\n", false, true}, {"kill failed", "populated 0\n", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			group := cgroupPath + "-2"
			var writes []string
			reads, reaps := 0, 0
			write := func(p, v string) error {
				writes = append(writes, p+"="+v)
				if tc.writeErr {
					return errors.New("kill denied")
				}
				return nil
			}
			read := func(p string) ([]byte, error) {
				if p != group+"/cgroup.events" {
					t.Fatalf("read unrelated cgroup %s", p)
				}
				reads++
				return []byte(tc.events), nil
			}
			err := cleanupCgroup(group, read, write, func() { reaps++ }, func() {})
			if (err != nil) != tc.wantErr || !reflect.DeepEqual(writes, []string{group + "/cgroup.kill=1"}) {
				t.Fatalf("unsafe cleanup err=%v writes=%v", err, writes)
			}
			if !tc.writeErr && (reads == 0 || reads > 50 || reaps != reads) {
				t.Fatalf("unbounded or unverified cleanup reads=%d reaps=%d", reads, reaps)
			}
		})
	}
}
