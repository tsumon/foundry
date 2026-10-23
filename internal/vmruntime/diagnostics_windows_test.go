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
	fixtures := []struct{ name, output, status, code string }{
		{"whpx", "WHPX: Failed to create partition PRIVATE_SENTINEL\n", "valid", "whpx_partition"},
		{"kernel", "qemu: could not open kernel file 'PRIVATE_SENTINEL': No such file or directory\n", "valid", "kernel_load"},
		{"unknown", "PRIVATE_SENTINEL\n", "unrecognized", ""},
	}
	if child := os.Getenv("FOUNDRY_QEMU_DIAGNOSTIC_CHILD"); child != "" {
		for _, tc := range fixtures {
			if child == tc.name {
				_, _ = os.Stderr.WriteString(tc.output)
				os.Exit(1)
			}
		}
		os.Exit(1)
	}
	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			exe, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			p, e := launchNative(ctx, command{Path: exe, Dir: filepath.Dir(exe), Args: []string{"-test.run=^TestWindowsQEMUDiagnosticOwnedChild$"}, Env: []string{"FOUNDRY_QEMU_DIAGNOSTIC_CHILD=" + tc.name}})
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
			if !f.TerminationConfirmed || f.ExitCode == nil || *f.ExitCode != 1 || f.DiagnosticStatus != tc.status {
				t.Fatalf("missing isolated failure facts: %#v", f)
			}
			if tc.code == "" {
				if f.QEMU != nil {
					t.Fatal("unknown text retained")
				}
			} else if f.QEMU == nil || len(f.QEMU.Codes) != 1 || f.QEMU.Codes[0] != tc.code {
				t.Fatal("startup category lost")
			}
		})
	}
}
