//go:build windows

package vmruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Only our test binary executes: no VM, repository code or network. This
// exercises the real suspended-start, Job admission and separate stderr pipe.
func TestWindowsQEMUDiagnosticOwnedChild(t *testing.T) {
	if os.Getenv("FOUNDRY_QEMU_DIAGNOSTIC_CHILD") == "1" {
		_, _ = os.Stderr.WriteString("WHPX: Failed to create partition PRIVATE_SENTINEL\n")
		os.Exit(1)
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, e := launchNative(ctx, command{Path: exe, Dir: filepath.Dir(exe), Args: []string{"-test.run=^TestWindowsQEMUDiagnosticOwnedChild$"}, Env: []string{"FOUNDRY_QEMU_DIAGNOSTIC_CHILD=1"}})
	if p != nil {
		defer func() {
			_ = p.Stop()
			bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_ = p.(*windowsProcess).observeTermination(bounded)
			p.Input().Close()
			p.Output().Close()
		}()
	}
	if e != nil {
		t.Fatal(e)
	}
	_ = p.(*windowsProcess).observeTermination(ctx)
	f := processDiagnostics(p)
	if !f.TerminationConfirmed || f.ExitCode == nil || *f.ExitCode != 1 || f.DiagnosticStatus != "valid" || f.QEMU == nil || len(f.QEMU.Codes) != 1 || f.QEMU.Codes[0] != "whpx_partition" {
		t.Fatalf("missing isolated failure facts: %#v", f)
	}
}
