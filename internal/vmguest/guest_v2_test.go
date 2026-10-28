package vmguest

import (
	"bytes"
	"context"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/frameworkreport"
	"foundry/internal/vmruntime"
	"io"
	"os"
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
		var input bytes.Buffer
		var output memoryWireV2
		if e := vmruntime.WriteRequestV2(context.Background(), &input, r, nil, ""); e != nil {
			t.Fatal(e)
		}
		m := &fixtureV2{outcome: completedV2()}
		if e := ServeV2(context.Background(), finiteReceiveV2{&input}, &output, "linux/amd64", m); e != nil {
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
		var out memoryWireV2
		if ServeV2(context.Background(), finiteReceiveV2{bytes.NewReader(raw)}, &out, "linux/amd64", m) == nil || m.prepares != 0 {
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
	go func() { var out memoryWireV2; done <- ServeV2(context.Background(), reader, &out, "linux/amd64", m) }()
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
		var out memoryWireV2
		done <- ServeV2(context.Background(), io.NopCloser(&noProgressAfterV2{bytes.NewReader(b.Bytes())}), &out, "linux/amd64", m)
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

// memoryWireV2 is an owned in-memory output endpoint. Close prevents further
// writes; captured bytes are read only after Serve returns.
type memoryWireV2 struct {
	bytes.Buffer
	closed   bool
	closeErr error
}

func (w *memoryWireV2) Write(b []byte) (int, error) {
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	return w.Buffer.Write(b)
}
func (w *memoryWireV2) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *memoryWireV2) Close() error                      { w.closed = true; return w.closeErr }

type blockedWireV2 struct {
	memoryWireV2
	entered, release chan struct{}
}

func (w *blockedWireV2) Write(b []byte) (int, error) {
	if bytes.HasPrefix(b, []byte(vmruntime.HeaderV2)) {
		close(w.entered)
		<-w.release
	}
	return w.memoryWireV2.Write(b)
}
func hostAcceptsV2(q vmruntime.WireRequestV2, raw []byte) bool {
	r := bytes.NewReader(bytes.TrimPrefix(raw, []byte(vmruntime.ReadyMarkerV2)))
	v, e := vmruntime.ReadResponseV2(r, q)
	if e != nil {
		return false
	}
	var extra [1]byte
	n, e := r.Read(extra[:])
	return (v.Result.Class == "probe_passed" || v.Result.Class == "passed") && n == 0 && e == io.EOF
}
func TestV2LateInputDuringBlockedPublicationLeavesNoHostResponse(t *testing.T) {
	q := probeV2(t, "c4_node_model_v2")
	var request bytes.Buffer
	if e := vmruntime.WriteRequestV2(context.Background(), &request, q, nil, ""); e != nil {
		t.Fatal(e)
	}
	input, send := io.Pipe()
	defer input.Close()
	defer send.Close()
	output := &blockedWireV2{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- ServeV2(context.Background(), input, output, "linux/amd64", &fixtureV2{outcome: completedV2()})
	}()
	if _, e := send.Write(request.Bytes()); e != nil {
		t.Fatal(e)
	}
	<-output.entered
	// Write returns only after the actual monitor consumed the forbidden byte.
	// Finalization must join that observer even if its latch update is pending.
	if _, e := send.Write([]byte("x")); e != nil {
		t.Fatal(e)
	}
	close(output.release)
	select {
	case e := <-done:
		if hostAcceptsV2(q, output.Bytes()) {
			t.Fatal("late byte left an authenticated successful frame and exact EOF", e)
		}
		if e == nil {
			t.Fatal("late publication succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("publication did not finish")
	}
}

// This endpoint has a permanent finite EOF; joining its observer is still
// mandatory. Preserve Len so buffered extra-byte admission remains covered.
type finiteBytesV2 interface {
	io.Reader
	Len() int
}
type finiteReceiveV2 struct{ finiteBytesV2 }

func (finiteReceiveV2) Close() error { return nil }

type finalizationReceiveV2 struct {
	request                     *bytes.Reader
	started, stop, releaseClose chan struct{}
	observation                 receiveObservationV2
	closeErr                    error
	holdRead                    bool
}

func (r *finalizationReceiveV2) Read(b []byte) (int, error) {
	if r.request.Len() != 0 {
		return r.request.Read(b)
	}
	close(r.started)
	<-r.stop
	if r.holdRead {
		<-r.releaseClose
	}
	if r.observation.n != 0 {
		b[0] = 'x'
	}
	return r.observation.n, r.observation.err
}
func (r *finalizationReceiveV2) Close() error {
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
	return r.closeErr
}
func TestV2PublicationRequiresReceiveCloseAndObserverJoin(t *testing.T) {
	for _, tc := range []struct {
		name         string
		observation  receiveObservationV2
		closeErr     error
		holdRead, ok bool
	}{
		{name: "close-wakeup", observation: receiveObservationV2{err: io.ErrClosedPipe}, ok: true},
		{name: "EOF", observation: receiveObservationV2{err: io.EOF}, ok: true},
		{name: "late-during-finalization", observation: receiveObservationV2{n: 1}},
		{name: "pending-error", observation: receiveObservationV2{err: errors.New("pending monitor failure")}},
		{name: "close-failure", observation: receiveObservationV2{err: io.ErrClosedPipe}, closeErr: errors.New("receive close failed")},
		{name: "join-failure", observation: receiveObservationV2{err: io.ErrClosedPipe}, holdRead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := probeV2(t, "c4_node_model_v2")
			var raw bytes.Buffer
			if e := vmruntime.WriteRequestV2(context.Background(), &raw, q, nil, ""); e != nil {
				t.Fatal(e)
			}
			input := &finalizationReceiveV2{request: bytes.NewReader(raw.Bytes()), started: make(chan struct{}), stop: make(chan struct{}), releaseClose: make(chan struct{}), observation: tc.observation, closeErr: tc.closeErr, holdRead: tc.holdRead}
			defer close(input.releaseClose)
			output := &blockedWireV2{entered: make(chan struct{}), release: make(chan struct{})}
			done := make(chan error, 1)
			go func() {
				done <- ServeV2(context.Background(), input, output, "linux/amd64", &fixtureV2{outcome: completedV2()})
			}()
			<-input.started
			<-output.entered
			close(output.release)
			e := <-done
			if (e == nil) != tc.ok {
				t.Fatal("serve result", e)
			}
			if hostAcceptsV2(q, output.Bytes()) != tc.ok {
				t.Fatal("host parser/EOF disagrees with receive authority", e)
			}
			if !output.closed {
				t.Fatal("output left open")
			}
			if !tc.ok {
				r := bytes.NewReader(bytes.TrimPrefix(output.Bytes(), []byte(vmruntime.ReadyMarkerV2)))
				if _, e := vmruntime.ReadResponseV2(r, q); e == nil {
					t.Fatal("failed precommit operation left a complete authenticated frame")
				}
			}
		})
	}
}
func TestV2BufferedPrefixCannotAuthenticateBeforeReceiveShutdown(t *testing.T) {
	q := probeV2(t, "c4_node_model_v2")
	var frame bytes.Buffer
	if e := vmruntime.WriteResponseV2(&frame, q, goodChecksV2(), c4verify.Result{Class: "probe_passed"}, []vmruntime.PhaseEvidenceV2{{Phase: "probe-node", Exited: true, ResourceLimitsOK: true, CleanupConfirmed: true}}); e != nil {
		t.Fatal(e)
	}
	b := frame.Bytes()
	if _, e := vmruntime.ReadResponseV2(bytes.NewReader(b[:len(b)-1]), q); e == nil {
		t.Fatal("withheld-byte prefix authenticated")
	}
	if _, e := vmruntime.ReadResponseV2(bytes.NewReader(b), q); e != nil {
		t.Fatal(e)
	}
}
func TestV2PublicationReportsOutputCloseAfterCommit(t *testing.T) {
	q := probeV2(t, "c4_node_model_v2")
	var request bytes.Buffer
	_ = vmruntime.WriteRequestV2(context.Background(), &request, q, nil, "")
	output := &memoryWireV2{closeErr: errors.New("output close failed")}
	e := ServeV2(context.Background(), finiteReceiveV2{bytes.NewReader(request.Bytes())}, output, "linux/amd64", &fixtureV2{outcome: completedV2()})
	if e == nil {
		t.Fatal("output close failure hidden")
	}
	if !hostAcceptsV2(q, output.Bytes()) {
		t.Fatal("test must demonstrate that local postcommit failure cannot retract frame")
	}
}

type errorReceiveV2 struct{}

func (errorReceiveV2) Read([]byte) (int, error) { return 0, errors.New("owned monitor error") }
func (errorReceiveV2) Close() error             { return nil }
func TestV2ReceiveShutdownJoinsObserverAfterObservation(t *testing.T) {
	m := &receiveMonitorV2{input: errorReceiveV2{}}
	entered, release := make(chan struct{}), make(chan struct{})
	m.start(func() { close(entered); <-release })
	<-entered
	done := make(chan error, 1)
	go func() { done <- m.shutdown() }()
	select {
	case <-done:
		close(release)
		t.Fatal("returned before observer completed")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if e := <-done; e == nil {
		t.Fatal("pending error hidden")
	}
}
func TestV2PublicationClosesOwnedNativeReceivePipe(t *testing.T) {
	input, send, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer input.Close()
	defer send.Close()
	if e = input.SetReadDeadline(time.Now().Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	q := probeV2(t, "c4_node_model_v2")
	var request bytes.Buffer
	if e = vmruntime.WriteRequestV2(context.Background(), &request, q, nil, ""); e != nil {
		t.Fatal(e)
	}
	sent := make(chan error, 1)
	go func() { _, e := send.Write(request.Bytes()); sent <- e }()
	output := &memoryWireV2{}
	e = ServeV2(context.Background(), input, output, "linux/amd64", &fixtureV2{outcome: completedV2()})
	if e != nil {
		t.Fatal(e)
	}
	if e = <-sent; e != nil {
		t.Fatal(e)
	}
	if !hostAcceptsV2(q, output.Bytes()) || !output.closed {
		t.Fatal("native pipe shutdown did not commit exact transcript")
	}
	if _, e = send.Write([]byte("after receive shutdown")); e == nil {
		t.Fatal("receive pipe remained open")
	}
}
func TestV2PendingCloseErrorCannotBeIgnored(t *testing.T) {
	for _, e := range []error{io.ErrClosedPipe, os.ErrClosed} {
		m := &receiveMonitorV2{input: &readErrorV2{err: e}}
		m.start(func() {})
		<-m.joined
		if m.shutdown() == nil {
			t.Fatal("preexisting close error ignored", e)
		}
	}
}

type readErrorV2 struct{ err error }

func (r *readErrorV2) Read([]byte) (int, error) { return 0, r.err }
func (r *readErrorV2) Close() error             { return nil }

type blockingCloseReceiveV2 struct {
	*bytes.Reader
	release chan struct{}
}

func (r *blockingCloseReceiveV2) Close() error { <-r.release; return nil }
func TestV2ReceiveCloseDeadlineLeavesFrameIncomplete(t *testing.T) {
	q := probeV2(t, "c4_node_model_v2")
	var request bytes.Buffer
	_ = vmruntime.WriteRequestV2(context.Background(), &request, q, nil, "")
	input := &blockingCloseReceiveV2{Reader: bytes.NewReader(request.Bytes()), release: make(chan struct{})}
	defer close(input.release)
	output := &memoryWireV2{}
	started := time.Now()
	e := ServeV2(context.Background(), input, output, "linux/amd64", &fixtureV2{outcome: completedV2()})
	if e == nil || hostAcceptsV2(q, output.Bytes()) || time.Since(started) > 2*transportShutdownV2 {
		t.Fatal("close deadline did not fail incomplete", e)
	}
}
