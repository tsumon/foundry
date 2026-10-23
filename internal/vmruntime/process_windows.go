//go:build windows

package vmruntime

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Both native handles remain owned until Wait observes termination. A PID is
// never used for cleanup or recovery, and the job is not inherited by QEMU.
type windowsProcess struct {
	owner   *ownedProcess
	in, out *os.File
	stderr  *qemuCapture
}

func (p *windowsProcess) Input() io.WriteCloser { return p.in }
func (p *windowsProcess) Output() io.ReadCloser { return p.out }
func (p *windowsProcess) Terminated() bool      { return p.owner.terminatedState() }
func (p *windowsProcess) diagnostics() nativeFacts {
	f := p.owner.facts()
	if f.TerminationConfirmed && p.stderr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		d, status := p.stderr.finish(ctx)
		if f.ExitCode != nil && *f.ExitCode != 0 {
			f.QEMU, f.DiagnosticStatus = d, status
		}
	}
	return safeNativeFacts(f)
}
func (p *windowsProcess) Stop() error {
	p.in.Close()
	return p.owner.stop()
}
func (p *windowsProcess) Wait() error {
	return p.owner.waitForTermination(context.Background())
}
func (p *windowsProcess) observeTermination(ctx context.Context) error {
	e := p.owner.waitForTermination(ctx)
	if p.Terminated() {
		return nil
	}
	return e
}
func windowsOwnedCalls() ownedProcessCalls {
	return ownedProcessCalls{
		assign: func(job, process uintptr) error {
			return windows.AssignProcessToJobObject(windows.Handle(job), windows.Handle(process))
		},
		resume:           func(thread uintptr) error { _, e := windows.ResumeThread(windows.Handle(thread)); return e },
		terminateProcess: func(h uintptr) error { return windows.TerminateProcess(windows.Handle(h), 1) },
		terminateJob:     func(h uintptr) error { return windows.TerminateJobObject(windows.Handle(h), 1) },
		wait: func(h uintptr, millis uint32) (bool, error) {
			event, e := windows.WaitForSingleObject(windows.Handle(h), millis)
			if e != nil {
				return false, e
			}
			if event == windows.WAIT_OBJECT_0 {
				return true, nil
			}
			if event == uint32(windows.WAIT_TIMEOUT) {
				return false, nil
			}
			return false, errors.New("unexpected native wait result")
		},
		exitCode: func(h uintptr) (uint32, error) {
			var code uint32
			e := windows.GetExitCodeProcess(windows.Handle(h), &code)
			return code, e
		},
		close: func(h uintptr) error { return windows.CloseHandle(windows.Handle(h)) },
	}
}
func launchNative(ctx context.Context, c command) (process nativeProcess, err error) {
	stage := "create_job"
	defer func() {
		if err != nil {
			if p, ok := process.(*windowsProcess); ok {
				err = &nativeAdmissionError{facts: p.owner.facts()}
			} else {
				err = admissionFailure(stage, err)
			}
		}
	}()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	owned := false
	defer func() {
		if !owned {
			windows.CloseHandle(job)
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	stage = "job_limits"
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY | windows.JOB_OBJECT_LIMIT_PROCESS_TIME
	limits.BasicLimitInformation.ActiveProcessLimit = 1
	limits.BasicLimitInformation.PerProcessUserTimeLimit = 180 * 10000000
	limits.ProcessMemoryLimit = 3 * 1024 * 1024 * 1024
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		return nil, e
	}
	stage = "stdio"
	childIn, parentIn, e := os.Pipe()
	if e != nil {
		return nil, e
	}
	defer childIn.Close()
	parentOut, childOut, e := os.Pipe()
	if e != nil {
		parentIn.Close()
		return nil, e
	}
	defer childOut.Close()
	parentErr, childErr, e := os.Pipe()
	if e != nil {
		parentIn.Close()
		parentOut.Close()
		return nil, e
	}
	defer childErr.Close()
	defer func() {
		if !owned {
			parentIn.Close()
			parentOut.Close()
			parentErr.Close()
		}
	}()
	stage = "handle_list"
	handles := []windows.Handle{windows.Handle(childIn.Fd()), windows.Handle(childOut.Fd()), windows.Handle(childErr.Fd())}
	for _, h := range handles {
		if e = windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); e != nil {
			return nil, e
		}
	}
	attributes, e := windows.NewProcThreadAttributeList(1)
	if e != nil {
		return nil, e
	}
	defer attributes.Delete()
	if e = attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); e != nil {
		return nil, e
	}
	si := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES, StdInput: handles[0], StdOutput: handles[1], StdErr: handles[2]}, ProcThreadAttributeList: attributes.List()}
	stage = "process_arguments"
	application, e := windows.UTF16PtrFromString(c.Path)
	if e != nil {
		return nil, e
	}
	args, e := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{c.Path}, c.Args...)))
	if e != nil {
		return nil, e
	}
	dir, e := windows.UTF16PtrFromString(c.Dir)
	if e != nil {
		return nil, e
	}
	systemRoot, e := windows.GetSystemWindowsDirectory()
	if e != nil {
		return nil, e
	}
	env, e := windowsEnvironment(c.Env, systemRoot)
	if e != nil {
		return nil, e
	}
	pi := windows.ProcessInformation{}
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	stage = "create_process"
	if e = windows.CreateProcess(application, args, nil, nil, true, flags, &env[0], dir, &si.StartupInfo, &pi); e != nil {
		return nil, e
	}
	defer windows.CloseHandle(pi.Thread)
	runtime.KeepAlive(handles)
	runtime.KeepAlive(env)
	// Transfer ownership immediately after CreateProcess, not after admission.
	// Every post-create failure returns this exact owner to Session.Start.
	owned = true
	p := &windowsProcess{owner: &ownedProcess{process: uintptr(pi.Process), job: uintptr(job), calls: windowsOwnedCalls()}, in: parentIn, out: parentOut, stderr: newQEMUCapture(parentErr)}
	// No guest instruction executes before successful Job assignment.
	if e = p.owner.admit(ctx, uintptr(pi.Thread)); e != nil {
		return p, e
	}
	return p, nil
}
