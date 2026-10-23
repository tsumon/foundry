//go:build linux

package vmguest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"foundry/internal/c4verify"
	"foundry/internal/vmruntime"
	"golang.org/x/sys/unix"
)

// Main is invoked only by a static /init. All privileged mutations are below
// the root/PID1 guard. --child is a fresh, credential-dropped exec with no
// inherited operation secret or control descriptors, not a forked verifier.
func Main() error {
	if len(os.Args) > 1 {
		if os.Geteuid() != 65534 || os.Getegid() != 65534 || os.Getppid() != 1 {
			return errors.New("invalid guest child")
		}
		return childMain(os.Args[1:])
	}
	if err := guardPID1(runtime.GOOS, os.Getpid(), os.Geteuid()); err != nil {
		return err
	}
	defer unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
	ctx, cancel := context.WithTimeout(context.Background(), 175*time.Second)
	defer cancel()
	go func() { <-ctx.Done(); _ = unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF) }()
	if err := bootstrap(); err != nil {
		return fmt.Errorf("guest bootstrap: %w", err)
	}
	console := "/dev/hvc0"
	if runtime.GOARCH == "amd64" {
		console = "/dev/ttyS0"
	}
	control, err := os.OpenFile(console, os.O_RDWR, 0)
	if err != nil {
		return errors.New("private console missing")
	}
	defer control.Close()
	if err := privateConsole(control); err != nil {
		return err
	}
	return Serve(ctx, control, control, "linux/"+runtime.GOARCH, &linuxMachine{control: console})
}

func mount(source, target, kind string, flags uintptr, data string) error {
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}
	return unix.Mount(source, target, kind, flags, data)
}

func bootstrap() error {
	unix.Umask(0077)
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return err
	}
	if err := mount("proc", "/proc", "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "hidepid=2"); err != nil {
		return err
	}
	if err := mount("devtmpfs", "/dev", "devtmpfs", unix.MS_NOSUID|unix.MS_NOEXEC, "mode=0755"); err != nil {
		return err
	}
	if err := mount("sysfs", "/sys", "sysfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, ""); err != nil {
		return err
	}
	if err := mount("cgroup2", "/sys/fs/cgroup", "cgroup2", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, ""); err != nil {
		return err
	}
	for _, m := range []struct {
		path, data string
		flags      uintptr
	}{
		{"/workspace", "size=16m,mode=0755", unix.MS_NOEXEC},
		{"/tmp", "size=64m,mode=1777", unix.MS_NOEXEC},
		{"/foundry-build", "size=256m,mode=0755", 0},
	} {
		if err := mount("tmpfs", m.path, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|m.flags, m.data); err != nil {
			return err
		}
	}
	for _, name := range []string{"cache", "gopath", "tmp"} {
		p := filepath.Join("/foundry-build", name)
		if err := os.Mkdir(p, 0700); err != nil {
			return err
		}
		if err := os.Chown(p, 65534, 65534); err != nil {
			return err
		}
	}
	// No unprivileged process may access kernel devices, including secondary
	// consoles. Only harmless byte-stream devices are available to tests.
	if err := filepath.WalkDir("/dev", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		mode := os.FileMode(0600)
		switch path {
		case "/dev/null", "/dev/zero", "/dev/random", "/dev/urandom":
			mode = 0666
		}
		if err := os.Chown(path, 0, 0); err != nil {
			return err
		}
		return os.Chmod(path, mode)
	}); err != nil {
		return err
	}
	return configureCgroup(os.ReadFile, writeControl, func(p string) error { return os.Mkdir(p, 0700) })
}

func writeControl(p, value string) error {
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.WriteString(f, value)
	return err
}

func privateConsole(f *os.File) error {
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if err := f.Chown(0, 0); err != nil {
		return err
	}
	t, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		return errors.New("console termios unavailable")
	}
	t.Iflag = 0
	t.Oflag = 0
	t.Lflag = 0
	t.Cflag = t.Cflag&^(unix.CSIZE|unix.PARENB|unix.CSTOPB|unix.CRTSCTS) | unix.CS8 | unix.CREAD | unix.CLOCAL
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	return unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS, t)
}

type linuxMachine struct{ control string }

func (m *linuxMachine) Prepare(r vmruntime.WireRequest) (vmruntime.SelfCheck, c4verify.Process, error) {
	var checks vmruntime.SelfCheck
	// Validate again at the filesystem boundary: no alternate caller can bypass
	// protocol admission, and all destinations are on a newly-created tmpfs.
	if err := vmruntime.ValidateRequest(r); err != nil {
		return checks, nil, err
	}
	for _, file := range r.Files {
		target := filepath.Join("/workspace", filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return checks, nil, err
		}
		if err := os.WriteFile(target, file.Data, 0444); err != nil {
			return checks, nil, err
		}
	}
	// Umask deliberately starts private; directories are explicitly made
	// traversable only after all untrusted bytes have been materialized.
	if err := filepath.WalkDir("/workspace", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(p, 0555)
		}
		return os.Chmod(p, 0444)
	}); err != nil {
		return checks, nil, err
	}
	for _, p := range []string{"/workspace", "/usr/local/go"} {
		if err := readOnlyBind(p); err != nil {
			return checks, nil, err
		}
	}
	// Recursive bind preserves child mounts; remount makes the initramfs root
	// immutable while its explicitly bounded scratch mounts remain writable.
	if err := unix.Mount("/", "/", "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		return checks, nil, err
	}
	if err := unix.Mount("", "/", "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
		return checks, nil, err
	}
	if err := unix.Mount("", "/sys", "", unix.MS_REMOUNT|unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, ""); err != nil {
		return checks, nil, err
	}
	if err := checkMounts(); err != nil {
		return checks, nil, err
	}
	for name, want := range limits {
		b, err := os.ReadFile(filepath.Join(cgroupPath, name))
		if err != nil || strings.TrimSpace(string(b)) != want {
			return checks, nil, errors.New("cgroup limit mismatch")
		}
	}
	interfaces, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return checks, nil, err
	}
	for _, v := range interfaces {
		if v.Name() != "lo" {
			return checks, nil, errors.New("network adapter present")
		}
	}
	cg, err := os.Open(cgroupPath)
	if err != nil {
		return checks, nil, err
	}
	p := &linuxProcess{cgroup: cg}
	// This subprocess tests the same credential and exec path as the verifier,
	// including denied /proc PID1 and control-device access. It runs no Go code
	// from the snapshot and cannot authenticate a response.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := restrictedCommand("/init", []string{"--check", m.control}, []string{}, int(cg.Fd()))
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err = runChild(ctx, cmd, cgroupPath)
	if err != nil || out.String() != "restricted-child-ok\n" {
		cg.Close()
		return checks, nil, errors.New("restricted child self-check failed")
	}
	checks = vmruntime.SelfCheck{ReadOnlyWorkspace: true, ReadOnlyToolchain: true, NoNetwork: true, PrivateControl: true, Unprivileged: true, NoNewPrivileges: true, CgroupLimits: true, TmpfsLimits: true}
	return checks, p, nil
}

func readOnlyBind(p string) error {
	if err := unix.Mount(p, p, "", unix.MS_BIND, ""); err != nil {
		return err
	}
	flags := uintptr(unix.MS_BIND | unix.MS_REMOUNT | unix.MS_RDONLY | unix.MS_NOSUID | unix.MS_NODEV)
	if p == "/workspace" {
		flags |= unix.MS_NOEXEC
	}
	return unix.Mount("", p, "", flags, "")
}
func checkMounts() error {
	for _, p := range []string{"/", "/workspace", "/usr/local/go"} {
		var s unix.Statfs_t
		if err := unix.Statfs(p, &s); err != nil {
			return err
		}
		if s.Flags&unix.ST_RDONLY == 0 {
			return errors.New("mutable guest root or inputs")
		}
	}
	for p, size := range map[string]uint64{"/tmp": 64 << 20, "/foundry-build": 256 << 20} {
		var s unix.Statfs_t
		if err := unix.Statfs(p, &s); err != nil {
			return err
		}
		if s.Type != unix.TMPFS_MAGIC || s.Blocks*uint64(s.Bsize) > size || s.Flags&unix.ST_NOSUID == 0 || s.Flags&unix.ST_NODEV == 0 || (p == "/tmp" && s.Flags&unix.ST_NOEXEC == 0) {
			return errors.New("unsafe scratch mount")
		}
	}
	return nil
}

func restrictedCommand(path string, args, env []string, cgroupFD int) *exec.Cmd {
	c := exec.Command(path, args...)
	c.Env = append([]string{}, env...)
	c.Dir = "/workspace"
	c.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{}}, UseCgroupFD: true, CgroupFD: cgroupFD, Pdeathsig: syscall.SIGKILL}
	return c
}

func runChild(ctx context.Context, cmd *exec.Cmd, group string) error {
	// Linux PDEATHSIG follows the creating OS thread, not just PID1's process.
	// Keep that thread alive until the direct child and its cgroup are reaped.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = writeControl(filepath.Join(group, "cgroup.kill"), "1")
		_ = cmd.Process.Kill()
		err = <-done
		if err == nil {
			err = ctx.Err()
		}
	}
	// A test can leave descendants holding stdout open or running after its
	// parent. Always kill the entire owned cgroup, never process names/PIDs.
	if cleanupErr := cleanupCgroup(group, os.ReadFile, writeControl, func() {
		// The direct child is already reaped by Cmd.Wait. Reap adopted
		// descendants only now, avoiding a race with Go's process waiter.
		for {
			var status unix.WaitStatus
			pid, _ := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			if pid <= 0 {
				break
			}
		}
	}, func() { time.Sleep(10 * time.Millisecond) }); cleanupErr != nil {
		return cleanupErr
	}
	return err
}

type linuxProcess struct {
	cgroup   *os.File
	sequence int
}

func (p *linuxProcess) Run(ctx context.Context, r c4verify.Request, stdout, stderr io.Writer) (int, error) {
	spec, err := childSpec(r)
	if err != nil {
		io.WriteString(stderr, "guest_command_contract_rejected\n")
		return -1, err
	}
	// Previous phase has completed owned cleanup; its cgroup is deliberately
	// left retired until VM shutdown, never used for a second clone.
	if p.cgroup != nil {
		if err := p.cgroup.Close(); err != nil {
			return -1, err
		}
		p.cgroup = nil
	}
	p.sequence++
	group := fmt.Sprintf("%s-%d", cgroupPath, p.sequence)
	if err := createLimitedCgroup(group, os.ReadFile, writeControl, func(path string) error { return os.Mkdir(path, 0700) }); err != nil {
		return -1, err
	}
	cg, err := os.Open(group)
	if err != nil {
		return -1, err
	}
	p.cgroup = cg
	cmd := restrictedCommand(spec.Path, spec.Args, spec.Env, int(p.cgroup.Fd()))
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	err = runChild(ctx, cmd, group)
	// Cgroup OOM/resource exhaustion is infrastructure failure, never a valid
	// completed test failure that could authorize a repair.
	for file, key := range map[string]string{"memory.events": "oom_kill", "pids.events": "max"} {
		b, e := os.ReadFile(filepath.Join(group, file))
		if e != nil {
			fmt.Fprintf(stderr, "guest_resource_counter_unreadable %s\n", file)
			return -1, e
		}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[0] == key && f[1] != "0" {
				fmt.Fprintf(stderr, "guest_resource_limit_reached %s\n", key)
				return -1, errors.New("guest resource limit reached")
			}
		}
	}
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			fmt.Fprintf(stderr, "guest_child_signal=%d\n", status.Signal())
			return -1, errors.New("guest child terminated by signal")
		}
		return exit.ExitCode(), nil
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		fmt.Fprintf(stderr, "guest_process_start_or_cleanup_failed errno=%d\n", uintptr(errno))
	} else {
		io.WriteString(stderr, "guest_process_start_or_cleanup_failed\n")
	}
	return -1, err
}

func childMain(args []string) error {
	runtime.LockOSThread()
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	if len(args) == 2 && args[0] == "--check" {
		return childCheck(args[1])
	}
	if len(args) < 2 || args[0] != "--child" {
		return errors.New("invalid child action")
	}
	r := c4verify.Request{Path: "/usr/local/go/bin/go", Dir: "/workspace", Args: args[1:], Env: os.Environ()}
	if _, err := childSpec(r); err != nil {
		return err
	}
	if err := os.Chdir("/workspace"); err != nil {
		return err
	}
	return unix.Exec(r.Path, append([]string{r.Path}, r.Args...), r.Env)
}

func childCheck(control string) error {
	if control != "/dev/hvc0" && control != "/dev/ttyS0" {
		return errors.New("invalid console")
	}
	nnp, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil || nnp != 1 {
		return errors.New("no-new-privileges absent")
	}
	if os.Getuid() != 65534 || os.Getgid() != 65534 || len(os.Environ()) != 0 {
		return errors.New("child identity mismatch")
	}
	groups, err := os.Getgroups()
	if err != nil || len(groups) != 0 {
		return errors.New("supplementary groups present")
	}
	for _, p := range []string{control, "/dev/console", "/proc/1/mem", "/proc/1/environ", "/proc/1/fd/0"} {
		for _, flag := range []int{os.O_RDONLY, os.O_WRONLY} {
			f, err := os.OpenFile(p, flag, 0)
			if err == nil {
				f.Close()
				return errors.New("privileged state accessible")
			}
		}
	}
	for _, p := range []string{"/workspace/.forbidden", "/usr/local/go/.forbidden", "/.forbidden"} {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			f.Close()
			return errors.New("immutable state writable")
		}
	}
	if err := checkMounts(); err != nil {
		return err
	}
	_, err = io.WriteString(os.Stdout, "restricted-child-ok\n")
	return err
}
