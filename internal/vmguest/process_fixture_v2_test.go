package vmguest

import (
	"context"
	"foundry/internal/frameworkreport"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestV2OwnedPipeChild(t *testing.T) {
	if os.Getenv("FOUNDRY_GUEST_V2_FIXTURE") != "owned-pipes" {
		return
	}
	_, _ = io.WriteString(os.Stdout, "owned diagnostic\n")
	_, _ = io.WriteString(os.Stderr, "owned stderr\n")
	f := os.NewFile(3, "report")
	_, _ = io.WriteString(f, oneReportV2)
	_ = f.Close()
	os.Exit(0)
}
func TestV2OwnedProcessDirectWaitAndThreeConcurrentDrains(t *testing.T) {
	outR, outW, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer outR.Close()
	defer outW.Close()
	errR, errW, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer errR.Close()
	defer errW.Close()
	reportR, reportW, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer reportR.Close()
	defer reportW.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestV2OwnedPipeChild$")
	cmd.Env = []string{"FOUNDRY_GUEST_V2_FIXTURE=owned-pipes"}
	cmd.Stdout = outW
	cmd.Stderr = errW
	cmd.ExtraFiles = []*os.File{reportW}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	reportW.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d := &diagnosticsV2{}
	p := &reportGateV2{parser: frameworkreport.New(frameworkreport.NodeTestV2)}
	cleaned := false
	o := superviseV2(ctx, phaseStreamsV2{outR, errR, reportR}, func() frameworkreport.Result {
		e := cmd.Wait()
		return frameworkreport.Result{Exited: true, Err: e, ExitCode: cmd.ProcessState.ExitCode()}
	}, func() error { cleaned = true; _ = outW.Close(); return errW.Close() }, func() error { return nil }, diagnosticWriterV2{budget: d}, diagnosticWriterV2{budget: d, stderr: true}, p, time.Second)
	v := p.finish(o.Process)
	diagnostics, _, _ := d.facts()
	if !cleaned || !o.CleanupConfirmed || o.Process.Err != nil || v.Class != "passed" || v.Tests != 1 || len(diagnostics) == 0 {
		t.Fatal(o, v, diagnostics)
	}
}
