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
	"testing"
	"time"
)

func probeV2(t *testing.T, id string) vmruntime.WireRequestV2 {
	t.Helper()
	return vmruntime.WireRequestV2{SchemaVersion: 2, Action: "probe", Nonce: strings.Repeat("a", 64), Secret: strings.Repeat("b", 64), Target: vmruntime.ProbeTargetV2{ProfileID: id, RuntimeInventoryDigest: strings.Repeat("c", 64), Identity: identityV2()}}
}
func goodChecksV2() vmruntime.SelfCheckV2 {
	return vmruntime.SelfCheckV2{ReadOnlyWorkspace: true, ReadOnlyToolchain: true, NoNetwork: true, PrivateControl: true, Unprivileged: true, NoNewPrivileges: true, CgroupLimits: true, TmpfsLimits: true, ReadOnlyDependencies: true, RuntimeInventoryMatches: true, InputBindingValid: true, PhaseCleanupConfirmed: true}
}

type fixtureV2 struct {
	stages, prepares, freezes int
	phases                    []phaseV2
	bad                       bool
	outcome                   phaseOutcomeV2
	report                    string
	deadlines                 []time.Time
}

func (m *fixtureV2) Stage(_ uint8, _ uint32, _ vmruntime.SourceFileV2, r io.Reader) error {
	m.stages++
	_, e := io.Copy(io.Discard, r)
	return e
}
func (m *fixtureV2) Prepare(vmruntime.WireRequestV2) (vmruntime.SelfCheckV2, phaseExecutorV2, error) {
	m.prepares++
	if m.bad {
		return vmruntime.SelfCheckV2{}, nil, errors.New("unavailable")
	}
	return goodChecksV2(), m, nil
}
func (m *fixtureV2) FreezeCompiled(vmruntime.WireRequestV2) (string, error) {
	m.freezes++
	if m.bad {
		return "", errors.New("not frozen")
	}
	return strings.Repeat("d", 64), nil
}
func (m *fixtureV2) Run(ctx context.Context, r phaseRequestV2, out, errOut, report io.Writer) phaseOutcomeV2 {
	m.phases = append(m.phases, r.Phase)
	d, _ := ctx.Deadline()
	m.deadlines = append(m.deadlines, d)
	io.WriteString(report, m.report)
	return m.outcome
}
func completedV2() phaseOutcomeV2 {
	return phaseOutcomeV2{Process: frameworkreport.Result{Exited: true}, CleanupConfirmed: true, ResourcesOK: true}
}
func TestV2ProbeAndExactTerminal(t *testing.T) {
	for _, id := range []string{"c4_node_model_v2", "c4_python_model_v2", "c4_typescript_model_v2"} {
		r := probeV2(t, id)
		var input, output bytes.Buffer
		if e := vmruntime.WriteRequestV2(context.Background(), &input, r, nil, ""); e != nil {
			t.Fatal(e)
		}
		m := &fixtureV2{outcome: completedV2()}
		if e := ServeV2(context.Background(), &input, &output, "linux/amd64", m); e != nil {
			t.Fatal(e)
		}
		got, e := vmruntime.ReadResponseV2(bytes.NewReader(bytes.TrimPrefix(output.Bytes(), []byte(vmruntime.ReadyMarkerV2))), r)
		if e != nil || got.Result.Class != "probe_passed" || got.Result.Tests != 0 || m.stages != 0 || len(m.phases) != 1 {
			t.Fatalf("%+v %v %+v", got, e, m)
		}
	}
	r := probeV2(t, "c4_node_model_v2")
	var b bytes.Buffer
	_ = vmruntime.WriteRequestV2(context.Background(), &b, r, nil, "")
	valid := bytes.Clone(b.Bytes())
	for _, raw := range [][]byte{valid[:len(valid)-1], append(bytes.Clone(valid), 'x'), []byte(vmruntime.ReadyMarker)} {
		m := &fixtureV2{}
		var out bytes.Buffer
		if ServeV2(context.Background(), bytes.NewReader(raw), &out, "linux/amd64", m) == nil || m.prepares != 0 {
			t.Fatal("incomplete/mixed/extra transaction prepared")
		}
	}
}
func TestV2LateInputCancelsWork(t *testing.T) {
	r := probeV2(t, "c4_node_model_v2")
	var b bytes.Buffer
	_ = vmruntime.WriteRequestV2(context.Background(), &b, r, nil, "")
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	m := &lateMachineV2{fixtureV2: fixtureV2{outcome: completedV2()}, running: make(chan struct{})}
	done := make(chan error, 1)
	go func() { var out bytes.Buffer; done <- ServeV2(context.Background(), reader, &out, "linux/amd64", m) }()
	go func() { _, _ = writer.Write(b.Bytes()); <-m.running; _, _ = writer.Write([]byte("late")) }()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("late input accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("late input did not cancel")
	}
}

type lateMachineV2 struct {
	fixtureV2
	running chan struct{}
}

func (m *lateMachineV2) Prepare(vmruntime.WireRequestV2) (vmruntime.SelfCheckV2, phaseExecutorV2, error) {
	return goodChecksV2(), m, nil
}
func (m *lateMachineV2) Run(ctx context.Context, _ phaseRequestV2, _, _, _ io.Writer) phaseOutcomeV2 {
	close(m.running)
	<-ctx.Done()
	return phaseOutcomeV2{Process: frameworkreport.Result{Canceled: true}, CleanupConfirmed: true, ResourcesOK: true}
}
func TestV2PhasesShareDeadlineAndCompileGate(t *testing.T) {
	r := probeV2(t, "c4_typescript_model_v2")
	r.Action = "verify"
	r.Verify = &vmruntime.VerifyRequestV2{}
	r.Verify.Inputs.SourceRoots = []string{"."}
	r.Verify.Inputs.TestFiles = []string{"tests/a.test.ts"}
	m := &fixtureV2{outcome: completedV2(), report: oneReportV2}
	checks := goodChecksV2()
	result, phases := runPhasesV2(context.Background(), r, m, m, &checks)
	if result.Class != "passed" || len(phases) != 2 || m.freezes != 1 || !m.deadlines[0].Equal(m.deadlines[1]) || phases[0].CompiledOutputDigest == "" {
		t.Fatalf("%+v %+v %+v", result, phases, m)
	}
	for _, o := range []phaseOutcomeV2{{Process: frameworkreport.Result{Exited: true, ExitCode: 1}, CleanupConfirmed: true, ResourcesOK: true}, {Process: frameworkreport.Result{Exited: true}, ResourcesOK: true}, {Process: frameworkreport.Result{Exited: true}, CleanupConfirmed: true}, {Process: frameworkreport.Result{DeadlineExceeded: true}, CleanupConfirmed: true, ResourcesOK: true}} {
		m := &fixtureV2{outcome: o}
		checks := goodChecksV2()
		got, p := runPhasesV2(context.Background(), r, m, m, &checks)
		if got.Class == "passed" || len(p) != 1 || m.freezes != 0 {
			t.Fatal("failed compiler launched tests", got, p)
		}
	}
	m = &fixtureV2{outcome: completedV2(), bad: true}
	checks = goodChecksV2()
	got, p := runPhasesV2(context.Background(), r, m, m, &checks)
	if got.Class != "infrastructure_failed" || len(p) != 1 || p[0].CompiledOutputDigest != "" {
		t.Fatal(got, p)
	}
}

const oneReportV2 = "{\"type\":\"suite_start\",\"seq\":1,\"schema_version\":1,\"runner\":\"node-test-v2\"}\n{\"type\":\"case_start\",\"seq\":2,\"id\":1}\n{\"type\":\"case_end\",\"seq\":3,\"id\":1,\"status\":\"passed\"}\n{\"type\":\"suite_end\",\"seq\":4,\"started\":1,\"passed\":1,\"failed\":0,\"skipped\":0,\"expected_failed\":0,\"unexpected_passed\":0,\"discovery_errors\":0}\n"

var _ c4verify.Result

func TestV2TestVerdictRequiresEOFResourcesAndCleanup(t *testing.T) {
	q := probeV2(t, "c4_node_model_v2")
	q.Action = "verify"
	q.Verify = &vmruntime.VerifyRequestV2{}
	q.Verify.Inputs.SourceRoots = []string{"."}
	q.Verify.Inputs.TestFiles = []string{"tests/a.test.js"}
	for _, bad := range []string{"", "extra-report", "cleanup", "resources", "exit", "deadline", "cancel", "diagnostics"} {
		t.Run(bad, func(t *testing.T) {
			m := &fixtureV2{outcome: completedV2(), report: oneReportV2}
			switch bad {
			case "extra-report":
				m.report += " "
			case "cleanup":
				m.outcome.CleanupConfirmed = false
			case "resources":
				m.outcome.ResourcesOK = false
			case "exit":
				m.outcome.Process.ExitCode = 1
			case "deadline":
				m.outcome.Process.DeadlineExceeded = true
			case "cancel":
				m.outcome.Process.Canceled = true
			case "diagnostics":
				m.outcome.Process.StdoutTruncated = true
			}
			checks := goodChecksV2()
			r, _ := runPhasesV2(context.Background(), q, m, m, &checks)
			if (r.Class == "passed") != (bad == "") {
				t.Fatal(r)
			}
			if bad == "deadline" && r.Class != "timeout" {
				t.Fatal(r)
			}
			if bad == "cleanup" && checks.PhaseCleanupConfirmed {
				t.Fatal("aggregate lied")
			}
		})
	}
}
func TestV2DiagnosticBudgetSharedAcrossPhasesAndStreams(t *testing.T) {
	d := &diagnosticsV2{}
	stdout, stderr := diagnosticWriterV2{budget: d}, diagnosticWriterV2{budget: d, stderr: true}
	_, _ = stdout.Write([]byte(strings.Repeat("a", 40000)))
	_, _ = stderr.Write([]byte(strings.Repeat("b", 40000)))
	_, _ = stdout.Write([]byte("late"))
	s, out, err := d.facts()
	if len(s) != 64<<10 || !out || !err {
		t.Fatal(len(s), out, err)
	}
}

type noProgressAfterV2 struct{ r *bytes.Reader }

func (r *noProgressAfterV2) Read(b []byte) (int, error) {
	if r.r.Len() != 0 {
		return r.r.Read(b)
	}
	return 0, nil
}
func TestV2PostTerminalMonitorDoesNotSilentlyStopOnNoProgress(t *testing.T) {
	q := probeV2(t, "c4_node_model_v2")
	var b bytes.Buffer
	_ = vmruntime.WriteRequestV2(context.Background(), &b, q, nil, "")
	m := &lateMachineV2{running: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		var out bytes.Buffer
		done <- ServeV2(context.Background(), &noProgressAfterV2{bytes.NewReader(b.Bytes())}, &out, "linux/amd64", m)
	}()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("monitor stopped")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("monitor stopped after empty read")
	}
}

type invalidFreezerV2 struct{}

func (invalidFreezerV2) FreezeCompiled(vmruntime.WireRequestV2) (string, error) {
	return strings.Repeat("z", 64), nil
}
func TestV2CompilerDigestMustBeCanonicalBeforeTests(t *testing.T) {
	q := probeV2(t, "c4_typescript_model_v2")
	q.Action = "verify"
	q.Verify = &vmruntime.VerifyRequestV2{}
	q.Verify.Inputs.SourceRoots = []string{"."}
	q.Verify.Inputs.TestFiles = []string{"tests/a.test.ts"}
	m := &fixtureV2{outcome: completedV2(), report: oneReportV2}
	checks := goodChecksV2()
	r, p := runPhasesV2(context.Background(), q, m, invalidFreezerV2{}, &checks)
	if r.Class != "infrastructure_failed" || len(p) != 1 || len(m.phases) != 1 {
		t.Fatal("invalid digest launched tests", r, p)
	}
}
