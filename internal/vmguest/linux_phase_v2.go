//go:build linux

package vmguest

import (
	"context"
	"encoding/json"
	"fmt"
	"foundry/internal/frameworkreport"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

func sealedContractV2(c childContractV2) (*os.File, error) {
	raw, e := json.Marshal(c)
	if e != nil || !c.valid(c.Phase) || len(raw) > 1<<20 {
		return nil, errGuestV2
	}
	fd, e := unix.MemfdCreate("foundry-child-v1", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "owned-contract")
	defer f.Close()
	if _, e = f.Write(raw); e != nil {
		return nil, e
	}
	if e = f.Chmod(0400); e != nil {
		return nil, e
	}
	if _, e = unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_SEAL|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_WRITE); e != nil {
		return nil, e
	}
	return os.Open(fmt.Sprintf("/proc/self/fd/%d", fd))
}
func restrictedCommandV2(p phaseV2, cgroupFD int, report, contract *os.File) *exec.Cmd {
	c := restrictedCommand("/init", []string{"--child", string(p)}, envV2, cgroupFD)
	// Explicit nil FD3 slot keeps the sealed contract on FD4 for every phase.
	c.ExtraFiles = []*os.File{report, contract}
	return c
}
func phaseResourcesLinuxV2(group string) error {
	for file, keys := range map[string][]string{"memory.events": {"oom", "oom_kill"}, "pids.events": {"max"}} {
		b, e := os.ReadFile(filepath.Join(group, file))
		if e != nil {
			return e
		}
		for _, key := range keys {
			if e = checkCounterV2(b, key); e != nil {
				return e
			}
		}
	}
	return nil
}
func reapAdoptedV2() {
	for {
		var status unix.WaitStatus
		pid, _ := unix.Wait4(-1, &status, unix.WNOHANG, nil)
		if pid <= 0 {
			return
		}
	}
}
func (m *linuxMachineV2) Run(ctx context.Context, r phaseRequestV2, stdout, stderr, report io.Writer) phaseOutcomeV2 {
	if !r.Contract.valid(r.Phase) {
		return phaseOutcomeV2{Process: frameworkreport.Result{Err: errGuestV2}}
	}
	if e := ctx.Err(); e != nil {
		return phaseOutcomeV2{Process: frameworkreport.Result{Err: e, DeadlineExceeded: e == context.DeadlineExceeded, Canceled: e == context.Canceled}, CleanupConfirmed: true, ResourcesOK: true}
	}
	// Retain the creating OS thread until direct exit and group cleanup, as the
	// existing native lifecycle requires for Linux PDEATHSIG semantics.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	m.sequence++
	group := fmt.Sprintf("%s-v2-%d", cgroupPath, m.sequence)
	if e := createLimitedCgroup(group, os.ReadFile, writeControl, func(p string) error { return os.Mkdir(p, 0700) }); e != nil {
		return phaseOutcomeV2{Process: frameworkreport.Result{Err: e}}
	}
	cg, e := os.Open(group)
	if e != nil {
		return phaseOutcomeV2{Process: frameworkreport.Result{Err: e}}
	}
	defer cg.Close()
	contract, e := sealedContractV2(r.Contract)
	if e != nil {
		return phaseOutcomeV2{Process: frameworkreport.Result{Err: e}}
	}
	defer contract.Close()
	outR, outW, e := os.Pipe()
	if e != nil {
		return phaseOutcomeV2{Process: frameworkreport.Result{Err: e}}
	}
	defer outR.Close()
	defer outW.Close()
	errR, errW, e := os.Pipe()
	if e != nil {
		return phaseOutcomeV2{Process: frameworkreport.Result{Err: e}}
	}
	defer errR.Close()
	defer errW.Close()
	var reportR, reportW *os.File
	if r.Phase.test() {
		reportR, reportW, e = os.Pipe()
		if e != nil {
			return phaseOutcomeV2{Process: frameworkreport.Result{Err: e}}
		}
		defer reportR.Close()
		defer reportW.Close()
	}
	cmd := restrictedCommandV2(r.Phase, int(cg.Fd()), reportW, contract)
	cmd.Stdout = outW
	cmd.Stderr = errW
	null, e := os.Open("/dev/null")
	if e != nil {
		return phaseOutcomeV2{Process: frameworkreport.Result{Err: e}}
	}
	defer null.Close()
	cmd.Stdin = null
	cleanup := func() error {
		// Direct child is reaped before adopted descendants, avoiding Wait4 races.
		// cgroup.kill/populated is checked here; the reaper runs after cmd.Wait.
		if e := cleanupCgroup(group, os.ReadFile, writeControl, func() {}, func() { time.Sleep(10 * time.Millisecond) }); e != nil {
			return e
		}
		events, e := os.ReadFile(filepath.Join(group, "cgroup.events"))
		if e != nil {
			return e
		}
		return checkCounterV2(events, "populated")
	}
	if e = cmd.Start(); e != nil {
		cleaned := cleanup() == nil
		return phaseOutcomeV2{Process: frameworkreport.Result{Err: e}, CleanupConfirmed: cleaned, ResourcesOK: phaseResourcesLinuxV2(group) == nil}
	}
	outW.Close()
	errW.Close()
	if reportW != nil {
		reportW.Close()
	}
	contract.Close()
	null.Close()
	wait := func() frameworkreport.Result {
		e := cmd.Wait()
		result := frameworkreport.Result{Exited: true}
		if e == nil {
			return result
		}
		if exit, ok := e.(*exec.ExitError); ok {
			result.ExitCode = exit.ExitCode()
			if status, ok := exit.Sys().(syscall.WaitStatus); !ok || status.Signaled() {
				result.Err = errGuestV2
				result.ExitCode = -1
			}
			return result
		}
		result.Err = errGuestV2
		result.Exited = false
		return result
	}
	streams := phaseStreamsV2{stdout: outR, stderr: errR}
	if reportR != nil {
		streams.report = reportR
	}
	outcome := superviseV2(ctx, streams, wait, cleanup, func() error { return phaseResourcesLinuxV2(group) }, stdout, stderr, report, time.Second)
	if outcome.Process.Exited {
		reapAdoptedV2()
	}
	return outcome
}
