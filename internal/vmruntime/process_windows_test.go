//go:build windows

package vmruntime

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"testing"
	"time"
	"unsafe"
)

// This test executable is created suspended and is never resumed. The test
// exercises Windows ownership APIs, not a VM or candidate repository code.
func TestWindowsFailedStartRetainsRealSuspendedHandles(t *testing.T) {
	for _, stage := range []string{"assign", "cancel", "resume"} {
		t.Run(stage, func(t *testing.T) {
			executable, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			application, e := windows.UTF16PtrFromString(executable)
			if e != nil {
				t.Fatal(e)
			}
			argv, e := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{executable, "-test.run=^$"}))
			if e != nil {
				t.Fatal(e)
			}
			job, e := windows.CreateJobObject(nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			var info windows.ProcessInformation
			si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
			env := []uint16{0, 0}
			if e = windows.CreateProcess(application, argv, nil, nil, false, windows.CREATE_SUSPENDED|windows.CREATE_NO_WINDOW|windows.CREATE_UNICODE_ENVIRONMENT, &env[0], nil, &si, &info); e != nil {
				windows.CloseHandle(job)
				t.Fatal(e)
			}
			defer windows.CloseHandle(info.Thread)
			native := windowsOwnedCalls()
			owner := &ownedProcess{process: uintptr(info.Process), job: uintptr(job), calls: native}
			defer func() {
				if !owner.terminatedState() {
					windows.TerminateProcess(info.Process, 1)
					windows.WaitForSingleObject(info.Process, 5000)
					windows.CloseHandle(info.Process)
					windows.CloseHandle(job)
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			owner.calls.assign = func(job, process uintptr) error {
				if stage == "assign" {
					return errors.New("injected assignment failure")
				}
				e := native.assign(job, process)
				if stage == "cancel" {
					cancel()
				}
				return e
			}
			owner.calls.resume = func(uintptr) error { return errors.New("injected resume failure") }
			if owner.admit(ctx, uintptr(info.Thread)) == nil {
				t.Fatal("fault injection accepted")
			}
			owner.calls.terminateProcess = func(uintptr) error { return errors.New("injected terminate failure") }
			owner.calls.terminateJob = owner.calls.terminateProcess
			if owner.stop() == nil {
				t.Fatal("termination failure hidden")
			}
			short, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer stop()
			if owner.waitForTermination(short) == nil || owner.terminatedState() {
				t.Fatal("live suspended process treated as terminated")
			}
			var code uint32
			if e = windows.GetExitCodeProcess(info.Process, &code); e != nil || code != 259 {
				t.Fatalf("exact live handle lost: %v %d", e, code)
			}
			owner.calls.terminateProcess = native.terminateProcess
			owner.calls.terminateJob = native.terminateJob
			if e = owner.stop(); e != nil {
				t.Fatal(e)
			}
			bounded, finish := context.WithTimeout(context.Background(), 5*time.Second)
			defer finish()
			_ = owner.waitForTermination(bounded)
			if !owner.terminatedState() {
				t.Fatal("later exact-owned cleanup not confirmed")
			}
		})
	}
}
