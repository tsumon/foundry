//go:build linux

package vmguest

import (
	"context"
	"foundry/internal/vmruntime"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// MainV2 is a distinct static image entry. No host process can reach privileged
// setup: the root Linux PID1 guard precedes every mount or cgroup mutation.
func MainV2() error {
	if len(os.Args) == 3 && os.Args[1] == "--child" {
		return childMainV2(phaseV2(os.Args[2]))
	}
	if len(os.Args) != 1 {
		return errGuestV2
	}
	if e := guardPID1(runtime.GOOS, os.Getpid(), os.Geteuid()); e != nil {
		return e
	}
	defer unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
	ctx, cancel := context.WithTimeout(context.Background(), 175*time.Second)
	defer cancel()
	go func() { <-ctx.Done(); _ = unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF) }()
	if e := bootstrapV2(); e != nil {
		return e
	}
	console := "/dev/hvc0"
	if runtime.GOARCH == "amd64" {
		console = "/dev/ttyS0"
	}
	control, e := os.OpenFile(console, os.O_RDWR, 0)
	if e != nil {
		return errGuestV2
	}
	defer control.Close()
	unix.CloseOnExec(int(control.Fd()))
	if e = privateConsole(control); e != nil {
		return e
	}
	go func() { <-ctx.Done(); _ = control.Close() }()
	staged, e := newStagerV2("/workspace", "/foundry-deps/store", os.Getpagesize())
	if e != nil {
		return e
	}
	return ServeV2(ctx, control, control, "linux/"+runtime.GOARCH, &linuxMachineV2{stager: staged})
}
func bootstrapV2() error {
	unix.Umask(0077)
	if e := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); e != nil {
		return e
	}
	for _, m := range []struct {
		source, target, kind, data string
		flags                      uintptr
	}{
		{"proc", "/proc", "proc", "hidepid=2", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC},
		{"devtmpfs", "/dev", "devtmpfs", "mode=0755", unix.MS_NOSUID | unix.MS_NOEXEC},
		{"sysfs", "/sys", "sysfs", "", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC},
		{"cgroup2", "/sys/fs/cgroup", "cgroup2", "", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC},
		{"tmpfs", "/workspace", "tmpfs", "size=16m,nr_inodes=16384,mode=0755", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC},
		{"tmpfs", "/foundry-deps", "tmpfs", "size=80m,nr_inodes=32768,mode=0700", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC},
	} {
		if e := mount(m.source, m.target, m.kind, m.flags, m.data); e != nil {
			return e
		}
	}
	for _, m := range scratchMounts() {
		flags := uintptr(unix.MS_NOSUID | unix.MS_NODEV)
		if m.noexec {
			flags |= unix.MS_NOEXEC
		}
		if e := mount("tmpfs", m.path, "tmpfs", flags, m.options); e != nil {
			return e
		}
	}
	for _, p := range []string{"/foundry-deps/store", "/foundry-deps/python", "/foundry-build/compiled", "/foundry-build/compiled/node_modules"} {
		if e := os.Mkdir(p, 0700); e != nil {
			return e
		}
	}
	if e := os.Chown("/foundry-build/compiled", 65534, 65534); e != nil {
		return e
	}
	if e := filepath.WalkDir("/dev", func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		mode := os.FileMode(0600)
		switch p {
		case "/dev/null", "/dev/zero", "/dev/random", "/dev/urandom":
			mode = 0666
		}
		if e := os.Chown(p, 0, 0); e != nil {
			return e
		}
		return os.Chmod(p, mode)
	}); e != nil {
		return e
	}
	return configureCgroup(os.ReadFile, writeControl, func(p string) error { return os.Mkdir(p, 0700) })
}

type linuxMachineV2 struct {
	stager   *stagerV2
	sequence int
}

func (m *linuxMachineV2) Stage(k uint8, i uint32, f vmruntime.SourceFileV2, b io.Reader) error {
	return m.stager.Stage(k, i, f, b)
}
func readBoundedV2(p string, max int64) ([]byte, error) {
	st, e := os.Lstat(p)
	if e != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > max {
		return nil, errGuestV2
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max+1))
}
func runtimeObservationLinuxV2(p string) (runtimeObservationV2, error) {
	st, e := os.Lstat(p)
	if e != nil {
		return runtimeObservationV2{}, e
	}
	native, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return runtimeObservationV2{}, errGuestV2
	}
	size, digest, e := hashRegularV2(p, 128<<20)
	return runtimeObservationV2{st.Mode().IsRegular(), st.Mode().Perm() | st.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky), native.Uid, native.Gid, size, digest}, e
}
func (m *linuxMachineV2) Prepare(q vmruntime.WireRequestV2) (vmruntime.SelfCheckV2, phaseExecutorV2, error) {
	var checks vmruntime.SelfCheckV2
	if vmruntime.ValidateRequestV2(q) != nil {
		return checks, nil, errGuestV2
	}
	raw, e := readBoundedV2("/opt/foundry/runtime-inventory-v2.json", vmruntime.MaxRuntimeInventoryBytesV2)
	if e != nil {
		return checks, nil, e
	}
	st, e := os.Lstat("/opt/foundry/runtime-inventory-v2.json")
	if e != nil {
		return checks, nil, e
	}
	native, ok := st.Sys().(*syscall.Stat_t)
	if !ok || native.Uid != 0 || native.Gid != 0 || st.Mode().Perm() != 0444 {
		return checks, nil, errGuestV2
	}
	if e = verifyInventoryV2(raw, q.Target, runtimeObservationLinuxV2); e != nil {
		return checks, nil, e
	}
	if q.Verify != nil {
		for _, f := range q.Verify.SourceFiles {
			n, h, e := hashRegularV2(filepath.Join(m.stager.source, filepath.FromSlash(f.Path)), 10<<20)
			if e != nil || n != f.Size || h != f.SHA256 {
				return checks, nil, errGuestV2
			}
		}
		for _, f := range q.Verify.DependencyManifest.Files {
			n, h, e := hashRegularV2(filepath.Join(m.stager.deps, filepath.FromSlash(f.Path)), 8<<20)
			if e != nil || n != f.Size || h != f.SHA256 {
				return checks, nil, errGuestV2
			}
		}
	}
	if e = os.Mkdir("/workspace/node_modules", 0700); e != nil {
		return checks, nil, e
	}
	// Each nested mount is frozen separately. A nonrecursive source bind cannot
	// confer immutability on these dependency aliases.
	for _, p := range []string{"/workspace/node_modules", "/foundry-deps/python", "/foundry-build/compiled/node_modules"} {
		if e = unix.Mount(m.stager.deps, p, "", unix.MS_BIND, ""); e != nil {
			return checks, nil, e
		}
	}
	if e = os.Chmod("/foundry-deps", 0555); e != nil {
		return checks, nil, e
	}
	for _, p := range []string{m.stager.source, m.stager.deps} {
		if e = freezeTreeV2(p); e != nil {
			return checks, nil, e
		}
	}
	for _, p := range []string{m.stager.deps, "/workspace/node_modules", "/foundry-deps/python", "/foundry-build/compiled/node_modules", "/workspace", "/foundry-deps"} {
		if e = readOnlyBindV2(p, true); e != nil {
			return checks, nil, e
		}
	}
	if e = unix.Mount("/", "/", "", unix.MS_BIND|unix.MS_REC, ""); e != nil {
		return checks, nil, e
	}
	if e = unix.Mount("", "/", "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); e != nil {
		return checks, nil, e
	}
	if e = unix.Mount("", "/sys", "", unix.MS_REMOUNT|unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, ""); e != nil {
		return checks, nil, e
	}
	if e = checkPreparedLinuxV2(); e != nil {
		return checks, nil, e
	}
	for name, want := range limits {
		b, e := os.ReadFile(filepath.Join(cgroupPath, name))
		if e != nil || strings.TrimSpace(string(b)) != want {
			return checks, nil, errGuestV2
		}
	}
	checks = vmruntime.SelfCheckV2{ReadOnlyWorkspace: true, ReadOnlyToolchain: true, NoNetwork: true, PrivateControl: true, Unprivileged: true, NoNewPrivileges: true, CgroupLimits: true, TmpfsLimits: true, ReadOnlyDependencies: true, RuntimeInventoryMatches: true, InputBindingValid: true, PhaseCleanupConfirmed: true}
	return checks, m, nil
}
func readOnlyBindV2(p string, noexec bool) error {
	if e := unix.Mount(p, p, "", unix.MS_BIND, ""); e != nil {
		return e
	}
	flags := uintptr(unix.MS_BIND | unix.MS_REMOUNT | unix.MS_RDONLY | unix.MS_NOSUID | unix.MS_NODEV)
	if noexec {
		flags |= unix.MS_NOEXEC
	}
	return unix.Mount("", p, "", flags, "")
}
func mountObservationLinuxV2(p string) (mountObservationV2, error) {
	var s unix.Statfs_t
	if e := unix.Statfs(p, &s); e != nil {
		return mountObservationV2{}, e
	}
	if s.Bsize <= 0 || s.Blocks > ^uint64(0)/uint64(s.Bsize) {
		return mountObservationV2{}, errGuestV2
	}
	return mountObservationV2{s.Blocks * uint64(s.Bsize), s.Files, s.Type == unix.TMPFS_MAGIC, s.Flags&unix.ST_NOSUID != 0, s.Flags&unix.ST_NODEV != 0, s.Flags&unix.ST_NOEXEC != 0, s.Flags&unix.ST_RDONLY != 0}, nil
}
func checkPreparedLinuxV2() error {
	if e := checkInputMountsV2(mountObservationLinuxV2); e != nil {
		return e
	}
	for _, p := range []string{"/foundry-deps/store", "/foundry-deps/python"} {
		o, e := mountObservationLinuxV2(p)
		if e != nil || !safeMountV2(o, mountSpecV2{80 << 20, 32768, true}, true) {
			return errGuestV2
		}
	}
	if e := checkScratchV2(mountObservationLinuxV2); e != nil {
		return e
	}
	for _, p := range []string{"/", "/sys"} {
		var s unix.Statfs_t
		if unix.Statfs(p, &s) != nil || s.Flags&unix.ST_RDONLY == 0 {
			return errGuestV2
		}
	}
	b, e := readBoundedProcV2("/proc/self/mountinfo", 1<<20)
	if e != nil || checkMountListV2(b) != nil {
		return errGuestV2
	}
	interfaces, e := os.ReadDir("/sys/class/net")
	if e != nil {
		return e
	}
	for _, entry := range interfaces {
		if entry.Name() != "lo" {
			return errGuestV2
		}
	}

	return nil
}
func readBoundedProcV2(p string, max int64) ([]byte, error) {
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(b)) > max {
		return nil, errGuestV2
	}
	return b, nil
}
func (m *linuxMachineV2) FreezeCompiled(q vmruntime.WireRequestV2) (string, error) {
	if q.Verify == nil || q.Verify.Inputs.TypeScript == nil {
		return "", errGuestV2
	}
	const root = "/foundry-build/compiled"
	var expected vmruntime.SourceFileV2
	for _, f := range q.Verify.SourceFiles {
		if f.Path == "package.json" {
			expected = f
		}
	}
	pkg, e := readBoundedV2("/workspace/package.json", 8<<20)
	if e != nil || int64(len(pkg)) != expected.Size {
		return "", errGuestV2
	}
	size, digest, e := hashRegularV2("/workspace/package.json", 8<<20)
	if e != nil || size != expected.Size || digest != expected.SHA256 {
		return "", errGuestV2
	}
	f, e := os.OpenFile(filepath.Join(root, "package.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
	if e != nil {
		return "", e
	}
	_, e = f.Write(pkg)
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return "", errGuestV2
	}
	before, e := compiledInventoryV2(root, q.Verify.Inputs.TypeScript.InputFiles)
	if e != nil {
		return "", e
	}
	if e = freezeCompiledTreeV2(root); e != nil {
		return "", e
	}
	if e = readOnlyBindV2(root, true); e != nil {
		return "", e
	}
	var s unix.Statfs_t
	if e = unix.Statfs(root, &s); e != nil || s.Flags&(unix.ST_RDONLY|unix.ST_NOSUID|unix.ST_NODEV|unix.ST_NOEXEC) != (unix.ST_RDONLY|unix.ST_NOSUID|unix.ST_NODEV|unix.ST_NOEXEC) {
		return "", errGuestV2
	}
	after, e := compiledInventoryV2(root, q.Verify.Inputs.TypeScript.InputFiles)
	if e != nil || before != after {
		return "", errGuestV2
	}
	return after, nil
}
