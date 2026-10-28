package vmguest

import (
	"bytes"
	"context"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/frameworkreport"
	"foundry/internal/vmruntime"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type phaseRequestV2 struct {
	Phase    phaseV2
	Contract childContractV2
}
type phaseOutcomeV2 struct {
	Process                       frameworkreport.Result
	CleanupConfirmed, ResourcesOK bool
	FailureStage                  failureStageV2
}
type failureStageV2 string

const (
	cleanupFailedV2     failureStageV2 = "cleanup_failed"
	cleanupDeadlineV2   failureStageV2 = "cleanup_deadline"
	waitFailedV2        failureStageV2 = "child_wait_failed"
	waitDeadlineV2      failureStageV2 = "child_wait_deadline"
	resourcesFailedV2   failureStageV2 = "resources_failed"
	resourcesDeadlineV2 failureStageV2 = "resources_deadline"
	drainFailedV2       failureStageV2 = "stream_failed"
	drainDeadlineV2     failureStageV2 = "stream_deadline"
)

func (s failureStageV2) valid() bool {
	switch s {
	case cleanupFailedV2, cleanupDeadlineV2, waitFailedV2, waitDeadlineV2, resourcesFailedV2, resourcesDeadlineV2, drainFailedV2, drainDeadlineV2:
		return true
	}
	return false
}

type phaseExecutorV2 interface {
	Run(context.Context, phaseRequestV2, io.Writer, io.Writer, io.Writer) phaseOutcomeV2
}
type compiledFreezerV2 interface {
	FreezeCompiled(vmruntime.WireRequestV2) (string, error)
}
type machineV2 interface {
	Stage(uint8, uint32, vmruntime.SourceFileV2, io.Reader) error
	Prepare(vmruntime.WireRequestV2) (vmruntime.SelfCheckV2, phaseExecutorV2, error)
	compiledFreezerV2
}

// ServeV2 consumes exactly one terminal-gated transaction. Stage is provisional;
// neither a sink callback nor a valid metadata header permits execution.
func ServeV2(ctx context.Context, input io.ReadCloser, output io.WriteCloser, platform string, m machineV2) (err error) {
	receive := &receiveMonitorV2{input: input}
	defer func() {
		if e := receive.shutdown(); err == nil {
			err = e
		}
		if e := closeOutputV2(output); err == nil {
			err = e
		}
	}()
	if _, e := io.WriteString(output, vmruntime.ReadyMarkerV2); e != nil {
		return e
	}
	q, e := vmruntime.ReadRequestV2(ctx, input, m.Stage)
	if e != nil {
		return errGuestV2
	}
	if q.Target.Identity.GuestPlatform != platform {
		return errGuestV2
	}
	// Never add a buffered reader: a preexisting buffer remains the caller's
	// responsibility and any available bytes are still observable here.
	if b, ok := input.(interface{ Buffered() int }); ok && b.Buffered() != 0 {
		return errGuestV2
	}
	if b, ok := input.(interface{ Len() int }); ok && b.Len() != 0 {
		return errGuestV2
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	receive.start(cancel)
	checks, exec, e := m.Prepare(q)
	if e != nil || !checks.Valid() || exec == nil {
		return errGuestV2
	}
	if receive.violated() {
		return errGuestV2
	}
	result, phases := runPhasesV2(work, q, exec, m, &checks)
	if receive.violated() {
		return errGuestV2
	}
	var frame bytes.Buffer
	if e = vmruntime.WriteResponseV2(&frame, q, checks, result, phases); e != nil {
		return e
	}
	raw := frame.Bytes()
	// A missing final byte makes the length-delimited JSON/HMAC frame invalid
	// to the unchanged host reader. Prefix publication grants no authority.
	if n, e := output.Write(raw[:len(raw)-1]); e != nil || n != len(raw)-1 {
		return errGuestV2
	}
	if e = receive.shutdown(); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return errGuestV2
	}
	// Receive close and observer join are the explicit linearization boundary.
	// No claim is made about input arriving after this shutdown. Output is closed
	// immediately by the deferred owner after this final fragment is published.
	if n, e := output.Write(raw[len(raw)-1:]); e != nil || n != 1 {
		return errGuestV2
	}
	return nil
}

type diagnosticsV2 struct {
	mu             sync.Mutex
	data           []byte
	stdout, stderr bool
	sealed         bool
}
type diagnosticWriterV2 struct {
	budget *diagnosticsV2
	stderr bool
}

func (w diagnosticWriterV2) Write(b []byte) (int, error) {
	d := w.budget
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(b)
	if d.sealed {
		return n, nil
	}
	left := c4verify.OutputLimit - len(d.data)
	if len(b) > left {
		if w.stderr {
			d.stderr = true
		} else {
			d.stdout = true
		}
		b = b[:left]
	}
	d.data = append(d.data, b...)
	return n, nil
}
func (d *diagnosticsV2) facts() (string, bool, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.ToValidUTF8(string(d.data), "?"), d.stdout, d.stderr
}
func (d *diagnosticsV2) seal() (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sealed = true
	return strings.ToValidUTF8(string(d.data), "?"), d.stdout || d.stderr
}
func phasesV2(q vmruntime.WireRequestV2) []phaseV2 {
	if q.Action == "probe" {
		switch q.Target.ProfileID {
		case "c4_node_model_v2":
			return []phaseV2{probeNodeV2}
		case "c4_python_model_v2":
			return []phaseV2{probePythonV2}
		case "c4_typescript_model_v2":
			return []phaseV2{probeTypeScriptV2}
		}
	}
	switch q.Target.ProfileID {
	case "c4_node_model_v2":
		return []phaseV2{testNodeV2}
	case "c4_python_model_v2":
		return []phaseV2{testPythonV2}
	case "c4_typescript_model_v2":
		return []phaseV2{compileTypeScriptV2, testNodeV2}
	}
	return nil
}
func phaseContractV2(q vmruntime.WireRequestV2, p phaseV2) childContractV2 {
	c := childContractV2{SchemaVersion: 1, Phase: p, SourceRoots: []string{}, TestFiles: []string{}, InputFiles: []string{}, SourceRoot: "/workspace"}
	if p.probe() {
		return c
	}
	c.SourceRoots = append(c.SourceRoots, q.Verify.Inputs.SourceRoots...)
	if p == compileTypeScriptV2 {
		c.OutputRoot = "/foundry-build/compiled"
		if q.Verify.Inputs.TypeScript != nil {
			c.InputFiles = append(c.InputFiles, q.Verify.Inputs.TypeScript.InputFiles...)
		}
	} else {
		c.TestFiles = append(c.TestFiles, q.Verify.Inputs.TestFiles...)
		if q.Target.ProfileID == "c4_typescript_model_v2" {
			c.OutputRoot = "/foundry-build/compiled"
			for i, f := range c.TestFiles {
				c.TestFiles[i] = strings.TrimSuffix(f, ".ts") + ".js"
			}
		}
	}
	return c
}
func runPhasesV2(ctx context.Context, q vmruntime.WireRequestV2, exec phaseExecutorV2, freeze compiledFreezerV2, checks *vmruntime.SelfCheckV2) (c4verify.Result, []vmruntime.PhaseEvidenceV2) {
	// Reserve time for kill/reap/drain before PID1's independent VM deadline.
	deadline := time.Now().Add(120 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Add(-3*time.Second).Before(deadline) {
		deadline = d.Add(-3 * time.Second)
	}
	work, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	d := &diagnosticsV2{}
	out, errOut := diagnosticWriterV2{budget: d}, diagnosticWriterV2{budget: d, stderr: true}
	result := c4verify.Result{Class: "infrastructure_failed"}
	phases := []vmruntime.PhaseEvidenceV2{}
	var failedPhase phaseV2
	var failure failureStageV2
	for _, p := range phasesV2(q) {
		runner := frameworkreport.NodeTestV2
		if p == testPythonV2 {
			runner = frameworkreport.PythonUnittestV2
		}
		parser := &reportGateV2{parser: frameworkreport.New(runner)}
		report := io.Writer(io.Discard)
		if p.test() {
			report = parser
		}
		o := exec.Run(work, phaseRequestV2{p, phaseContractV2(q, p)}, out, errOut, report)
		failedPhase, failure = p, o.FailureStage
		if !o.CleanupConfirmed {
			checks.PhaseCleanupConfirmed = false
		}
		if !o.CleanupConfirmed || !o.ResourcesOK {
			o.Process.Err = errors.New("phase resource or cleanup failure")
		}
		ev := vmruntime.PhaseEvidenceV2{Phase: string(p), Exited: o.Process.Exited, ExitCode: o.Process.ExitCode, DeadlineExceeded: o.Process.DeadlineExceeded, Canceled: o.Process.Canceled, ResourceLimitsOK: o.ResourcesOK, CleanupConfirmed: o.CleanupConfirmed}
		output, stdoutTrunc, stderrTrunc := d.facts()
		o.Process.StdoutTruncated = o.Process.StdoutTruncated || stdoutTrunc
		o.Process.StderrTruncated = o.Process.StderrTruncated || stderrTrunc
		result.Output = output
		result.Truncated = o.Process.StdoutTruncated || o.Process.StderrTruncated || o.Process.ReportTruncated
		result.ExitCode = ev.ExitCode
		success := o.Process.Exited && o.Process.ExitCode == 0 && o.Process.Err == nil && !o.Process.DeadlineExceeded && !o.Process.Canceled && !result.Truncated
		if p.test() {
			v := parser.finish(o.Process)
			result.Class, result.Tests = v.Class, v.Tests
			phases = append(phases, ev)
			break
		}
		if o.Process.DeadlineExceeded {
			result.Class = "timeout"
		}
		if p == compileTypeScriptV2 && success {
			digest, e := freeze.FreezeCompiled(q)
			if e != nil || !canonicalDigestV2(digest) {
				success = false
			} else {
				ev.CompiledOutputDigest = digest
			}
		}
		phases = append(phases, ev)
		if !success {
			break
		}
		if p.probe() {
			result.Class = "probe_passed"
		}
	}
	// Freeze diagnostics after the terminal verdict; late drains cannot alter it.
	output, truncated := d.seal()
	result.Output = output
	result.Truncated = result.Truncated || truncated
	if (result.Class == "infrastructure_failed" || result.Class == "timeout") && failure.valid() && failedPhase.valid() {
		annotation := "\n[foundry-v2 phase=" + string(failedPhase) + " stage=" + string(failure) + "]\n"
		if n := c4verify.OutputLimit - len(annotation); len(result.Output) > n {
			for !utf8.ValidString(result.Output[:n]) {
				n--
			}
			result.Output = result.Output[:n]
			result.Truncated = true
		}
		result.Output += annotation
	}
	return result, phases
}

// A timed-out drain may still be waking from Read after its descriptor closes.
// Seal under the same lock as Write before deriving any parser verdict.
type reportGateV2 struct {
	mu     sync.Mutex
	parser *frameworkreport.Parser
	sealed bool
}

func (g *reportGateV2) Write(b []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sealed {
		return 0, errGuestV2
	}
	return g.parser.Write(b)
}
func (g *reportGateV2) finish(p frameworkreport.Result) frameworkreport.Verdict {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sealed = true
	return g.parser.Finish(p)
}

func canonicalDigestV2(d string) bool {
	if len(d) != 64 {
		return false
	}
	for _, c := range d {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
