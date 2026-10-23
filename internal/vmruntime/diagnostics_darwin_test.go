//go:build darwin

package vmruntime

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestMacDiagnosticChild(t *testing.T) {
	mode := os.Getenv("FOUNDRY_DIAGNOSTIC_TEST_CHILD")
	if mode == "" {
		return
	}
	switch mode {
	case "valid":
		io.WriteString(os.Stderr, validHelperDiagnostic)
	case "overflow":
		io.WriteString(os.Stderr, validHelperDiagnostic+strings.Repeat("never-retain-this", 10000))
	case "malformed":
		io.WriteString(os.Stderr, "password=never-retain-this")
	}
	os.Exit(70)
}
func TestMacNativeDiagnosticCapture(t *testing.T) {
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"valid", "overflow", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			p, e := launchNative(context.Background(), command{Path: executable, Args: []string{"-test.run=^TestMacDiagnosticChild$"}, Env: []string{"FOUNDRY_DIAGNOSTIC_TEST_CHILD=" + mode}})
			if e != nil {
				t.Fatal(e)
			}
			defer p.Stop()
			out, e := io.ReadAll(p.Output())
			if e != nil || len(out) != 0 {
				t.Fatal("diagnostics contaminated serial output")
			}
			if p.Wait() == nil {
				t.Fatal("native exit70 lost")
			}
			if len(p.(*macProcess).stderr.data) != 0 {
				t.Fatal("raw stderr retained after typed finalization")
			}
			f := processDiagnostics(p)
			if !f.TerminationConfirmed || f.ExitCode == nil || *f.ExitCode != 70 {
				t.Fatalf("native facts missing: %+v", f)
			}
			want := mode
			if mode == "malformed" {
				want = "rejected"
			}
			if f.DiagnosticStatus != want {
				t.Fatalf("capture status=%s want %s", f.DiagnosticStatus, want)
			}
			if (f.Helper != nil) != (mode == "valid") {
				t.Fatal("unsafe helper metadata retained")
			}
		})
	}
}
