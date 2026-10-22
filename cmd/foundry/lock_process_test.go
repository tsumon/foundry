package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLockProcessCrashReacquire(t *testing.T) {
	if root := os.Getenv("FOUNDRY_LOCK_TEST_ROOT"); root != "" {
		lock, e := acquireDataLock(root)
		if e != nil {
			os.Exit(3)
		}
		defer lock.Close()
		os.Stdout.WriteString("locked\n")
		bufio.NewReader(os.Stdin).ReadByte()
		return
	}
	root := t.TempDir()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	child := exec.Command(exe, "-test.run=^TestLockProcessCrashReacquire$")
	child.Env = append(os.Environ(), "FOUNDRY_LOCK_TEST_ROOT="+root)
	out, e := child.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	in, e := child.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	defer child.Process.Kill()
	line, e := bufio.NewReader(out).ReadString('\n')
	if e != nil || line != "locked\n" {
		t.Fatalf("child lock readiness %q %v", line, e)
	}
	before, e := os.Stat(filepath.Join(root, "controller.lock"))
	if e != nil {
		t.Fatal(e)
	}
	if lock, e := acquireDataLock(root); e == nil {
		lock.Close()
		t.Fatal("concurrent owner admitted")
	}
	after, e := os.Stat(filepath.Join(root, "controller.lock"))
	if e != nil || !os.SameFile(before, after) || after.Size() != before.Size() {
		t.Fatal("contender truncated owner PID")
	}
	if e := child.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	child.Wait()
	pid, e := os.ReadFile(filepath.Join(root, "controller.lock"))
	if e != nil || string(pid) != strconv.Itoa(child.Process.Pid)+"\n" {
		t.Fatal("contender changed owner PID", e)
	}
	lock, e := acquireDataLock(root)
	if e != nil {
		t.Fatal(e)
	}
	lock.Close()
	if _, e := os.Stat(filepath.Join(root, "controller.lock")); e != nil {
		t.Fatal("release unlinked lock", e)
	}
}
