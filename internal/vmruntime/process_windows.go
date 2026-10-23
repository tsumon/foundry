//go:build windows

package vmruntime

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Both native handles remain owned until Wait observes termination. A PID is
// never used for cleanup or recovery, and the job is not inherited by QEMU.
type windowsProcess struct {
	mu           sync.Mutex
	process, job windows.Handle
	in, out      *os.File
	waited       bool
}

func (p *windowsProcess) Input() io.WriteCloser { return p.in }
func (p *windowsProcess) Output() io.ReadCloser { return p.out }
func (p *windowsProcess) Terminated() bool      { p.mu.Lock(); defer p.mu.Unlock(); return p.waited }
func (p *windowsProcess) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.in.Close()
	if p.waited {
		return nil
	}
	return windows.TerminateJobObject(p.job, 1)
}
func (p *windowsProcess) Wait() error {
	event, e := windows.WaitForSingleObject(p.process, windows.INFINITE)
	if e != nil || event != windows.WAIT_OBJECT_0 {
		return errors.New("native process termination unconfirmed")
	}
	var code uint32
	e = windows.GetExitCodeProcess(p.process, &code)
	p.mu.Lock()
	p.waited = true
	windows.CloseHandle(p.process)
	windows.CloseHandle(p.job)
	p.mu.Unlock()
	if e != nil || code != 0 {
		return errors.New("native process failed")
	}
	return nil
}
func launchNative(ctx context.Context, c command) (nativeProcess, error) {
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
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY | windows.JOB_OBJECT_LIMIT_PROCESS_TIME
	limits.BasicLimitInformation.ActiveProcessLimit = 1
	limits.BasicLimitInformation.PerProcessUserTimeLimit = 180 * 10000000
	limits.ProcessMemoryLimit = 3 * 1024 * 1024 * 1024
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		return nil, e
	}
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
	null, e := os.OpenFile("NUL", os.O_WRONLY, 0)
	if e != nil {
		parentIn.Close()
		parentOut.Close()
		return nil, e
	}
	defer null.Close()
	defer func() {
		if !owned {
			parentIn.Close()
			parentOut.Close()
		}
	}()
	handles := []windows.Handle{windows.Handle(childIn.Fd()), windows.Handle(childOut.Fd()), windows.Handle(null.Fd())}
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
	env := []uint16{}
	for _, v := range c.Env {
		env = append(env, utf16.Encode([]rune(v))...)
		env = append(env, 0)
	}
	env = append(env, 0)
	pi := windows.ProcessInformation{}
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	if e = windows.CreateProcess(application, args, nil, nil, true, flags, &env[0], dir, &si.StartupInfo, &pi); e != nil {
		return nil, e
	}
	defer windows.CloseHandle(pi.Thread)
	runtime.KeepAlive(handles)
	runtime.KeepAlive(env)
	// Crucially, no untrusted guest instruction executes before this succeeds.
	if e = windows.AssignProcessToJobObject(job, pi.Process); e != nil {
		windows.TerminateProcess(pi.Process, 1)
		windows.WaitForSingleObject(pi.Process, 5000)
		windows.CloseHandle(pi.Process)
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		windows.TerminateJobObject(job, 1)
		windows.WaitForSingleObject(pi.Process, 5000)
		windows.CloseHandle(pi.Process)
		return nil, e
	}
	if _, e = windows.ResumeThread(pi.Thread); e != nil {
		windows.TerminateJobObject(job, 1)
		windows.WaitForSingleObject(pi.Process, 5000)
		windows.CloseHandle(pi.Process)
		return nil, e
	}
	owned = true
	return &windowsProcess{process: pi.Process, job: job, in: parentIn, out: parentOut}, nil
}
