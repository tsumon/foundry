package vmguest

import (
	"bytes"
	"context"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/frameworkreport"
	"foundry/internal/vmruntime"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestV2ResourceCountersRequiredAndNumeric(t *testing.T) {
	for _, b := range []string{"", "other 0\n", "oom_kill nope\n", "oom_kill -1\n", "oom_kill 0\noom_kill 0\n", "oom_kill 1\n", "oom_kill 18446744073709551616\n"} {
		if checkCounterV2([]byte(b), "oom_kill") == nil {
			t.Fatal("bad counter", b)
		}
	}
	if checkCounterV2([]byte("low 0\noom_kill 0\n"), "oom_kill") != nil {
		t.Fatal("good counter")
	}
}
func TestV2DirectExitPrecedesHeldPipeAndCleanup(t *testing.T) {
	out, w := io.Pipe()
	var cleaned atomic.Bool
	o := superviseV2(context.Background(), phaseStreamsV2{stdout: out}, func() frameworkreport.Result { return frameworkreport.Result{Exited: true} }, func() error { cleaned.Store(true); return w.Close() }, func() error { return nil }, io.Discard, io.Discard, io.Discard, 100*time.Millisecond)
	if !o.CleanupConfirmed || !o.ResourcesOK || o.Process.Err != nil || !o.Process.Exited || !cleaned.Load() {
		t.Fatal(o)
	}
}
func TestV2ParserOverflowKillsGroupAndBoundedDrain(t *testing.T) {
	report, w := io.Pipe()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.WriteString(w, strings.Repeat("x", 4096)); _ = w.Close() }()
	var cleanup atomic.Bool
	p := frameworkreport.New(frameworkreport.NodeTestV2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	exited := make(chan struct{})
	o := superviseV2(ctx, phaseStreamsV2{report: report}, func() frameworkreport.Result { <-exited; return frameworkreport.Result{Exited: true, ExitCode: 1} }, func() error { cleanup.Store(true); close(exited); return w.Close() }, func() error { return nil }, io.Discard, io.Discard, p, 100*time.Millisecond)
	<-done
	if !cleanup.Load() || o.Process.Err == nil || !o.CleanupConfirmed {
		t.Fatal(o)
	}
}
func TestV2UnknownCleanupResourcesOrDrainCannotPass(t *testing.T) {
	for _, bad := range []string{"cleanup", "resources", "drain", "deadline"} {
		t.Run(bad, func(t *testing.T) {
			r, w := io.Pipe()
			defer w.Close()
			ctx, cancel := context.WithCancel(context.Background())
			if bad == "deadline" {
				var c context.CancelFunc
				ctx, c = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer c()
			}
			defer cancel()
			exit := make(chan struct{})
			if bad != "deadline" {
				close(exit)
			}
			o := superviseV2(ctx, phaseStreamsV2{stdout: r}, func() frameworkreport.Result { <-exit; return frameworkreport.Result{Exited: true} }, func() error {
				if bad == "deadline" {
					close(exit)
				}
				if bad != "drain" {
					_ = w.Close()
				}
				if bad == "cleanup" {
					return errors.New("unknown")
				}
				return nil
			}, func() error {
				if bad == "resources" {
					return errors.New("missing")
				}
				return nil
			}, io.Discard, io.Discard, io.Discard, 10*time.Millisecond)
			if bad == "deadline" {
				if !o.Process.DeadlineExceeded || o.Process.Canceled {
					t.Fatal(o)
				}
			} else if o.Process.Err == nil {
				t.Fatal("failure passed", o)
			}
		})
	}
}
func TestV2ReportGateSealsBeforeVerdict(t *testing.T) {
	p := frameworkreport.New(frameworkreport.NodeTestV2)
	g := &reportGateV2{parser: p}
	if _, e := io.WriteString(g, oneReportV2); e != nil {
		t.Fatal(e)
	}
	v := g.finish(frameworkreport.Result{Exited: true})
	if v.Class != "passed" {
		t.Fatal(v)
	}
	if _, e := g.Write([]byte("late drain")); e == nil {
		t.Fatal("late drain could mutate finished parser")
	}
	if p.Finish(frameworkreport.Result{Exited: true}).Class != "passed" {
		t.Fatal("late writer reached parser")
	}
}
func TestV2DeadlineDuringDrainIsObserved(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	o := superviseV2(ctx, phaseStreamsV2{stdout: r}, func() frameworkreport.Result { return frameworkreport.Result{Exited: true} }, func() error { return nil }, func() error { return nil }, io.Discard, io.Discard, io.Discard, 20*time.Millisecond)
	if !o.Process.DeadlineExceeded || o.Process.Canceled {
		t.Fatal("drain deadline hidden", o)
	}
}

// Only the external control hooks are inert: request admission, supervision,
// report verdict, response authentication and exact EOF remain the real path.
type supervisedFixtureV2 struct {
	fixtureV2
	run     func(context.Context, phaseRequestV2, io.Writer, io.Writer, io.Writer) phaseOutcomeV2
	outcome phaseOutcomeV2
}

func (m *supervisedFixtureV2) Prepare(vmruntime.WireRequestV2) (vmruntime.SelfCheckV2, phaseExecutorV2, error) {
	return goodChecksV2(), m, nil
}
func (m *supervisedFixtureV2) Run(ctx context.Context, r phaseRequestV2, out, errOut, report io.Writer) phaseOutcomeV2 {
	m.outcome = m.run(ctx, r, out, errOut, report)
	return m.outcome
}
func joinFinalizationFixtureV2(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("released finalization fixture did not join")
	}
}
func finalizationResponseV2(t *testing.T, m *supervisedFixtureV2) vmruntime.WireResponseV2 {
	t.Helper()
	q, files, store := transactionFixtureV2(t)
	var input bytes.Buffer
	if e := vmruntime.WriteRequestV2(context.Background(), &input, q, files, store); e != nil {
		t.Fatal(e)
	}
	var output memoryWireV2
	done := make(chan error, 1)
	go func() { done <- ServeV2(context.Background(), finiteReceiveV2{&input}, &output, "linux/amd64", m) }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(300 * time.Millisecond):
		// The caller owns release of its deliberately blocked hook during teardown.
		t.Cleanup(func() {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("fixture did not join after release")
			}
		})
		t.Fatal("finalization blocked past its fixed allowance")
	}
	reader := bytes.NewReader(bytes.TrimPrefix(output.Bytes(), []byte(vmruntime.ReadyMarkerV2)))
	got, e := vmruntime.ReadResponseV2(reader, q)
	if e != nil {
		t.Fatal(e)
	}
	var extra [1]byte
	n, e := reader.Read(extra[:])
	if n != 0 || e != io.EOF || !output.closed {
		t.Fatal("missing closed exact EOF", n, e)
	}
	return got
}
func TestV2FinalizationBlockedControlsPublishTruthfulNegative(t *testing.T) {
	for _, blocked := range []string{"cleanup", "resources"} {
		t.Run(blocked, func(t *testing.T) {
			release, joined := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }); joinFinalizationFixtureV2(t, joined) }
			defer unblock()
			m := &supervisedFixtureV2{}
			m.run = func(ctx context.Context, _ phaseRequestV2, out, errOut, report io.Writer) phaseOutcomeV2 {
				hook := func() error { defer close(joined); <-release; return nil }
				clean, resources := func() error { return nil }, func() error { return nil }
				if blocked == "cleanup" {
					clean = hook
				} else {
					resources = hook
				}
				return superviseV2(ctx, phaseStreamsV2{report: io.NopCloser(strings.NewReader(strings.SplitAfter(oneReportV2, "\n")[0]))}, func() frameworkreport.Result {
					return frameworkreport.Result{Exited: true, ExitCode: -1, Err: errGuestV2}
				}, clean, resources, out, errOut, report, 20*time.Millisecond)
			}
			got := finalizationResponseV2(t, m)
			want := "[foundry-v2 phase=test-node stage=" + blocked + "_deadline]"
			if got.Result.Class != "infrastructure_failed" || got.Result.Tests != 0 || !strings.Contains(got.Result.Output, want) || got.Phases[0].ResourceLimitsOK || got.Phases[0].CleanupConfirmed != (blocked == "resources") || got.Checks.PhaseCleanupConfirmed != (blocked == "resources") {
				t.Fatalf("unsafe or unannotated negative: %+v", got)
			}
			frozen := m.outcome
			// A late nil result must not upgrade the outcome; join before comparing.
			unblock()
			if !reflect.DeepEqual(m.outcome, frozen) {
				t.Fatal("late control upgraded frozen outcome")
			}
		})
	}
}
func TestV2FinalizationSharesOneBudgetAcrossControlsAndDrains(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	const budget = 80 * time.Millisecond
	joined := make(chan struct{})
	defer joinFinalizationFixtureV2(t, joined)
	start := time.Now()
	o := superviseV2(context.Background(), phaseStreamsV2{stdout: r}, func() frameworkreport.Result { return frameworkreport.Result{Exited: true} }, func() error { time.Sleep(50 * time.Millisecond); return nil }, func() error { defer close(joined); time.Sleep(50 * time.Millisecond); return nil }, io.Discard, io.Discard, io.Discard, budget)
	if elapsed := time.Since(start); elapsed >= 140*time.Millisecond || o.ResourcesOK || !o.CleanupConfirmed || o.Process.Err == nil {
		t.Fatalf("stages reset finalization allowance: elapsed=%s outcome=%+v", elapsed, o)
	}
}
func TestV2FinalizationFailureAnnotationSharesDiagnosticBudget(t *testing.T) {
	q := probeV2(t, "c4_node_model_v2")
	q.Action = "verify"
	q.Verify = &vmruntime.VerifyRequestV2{}
	q.Verify.Inputs.SourceRoots = []string{"."}
	q.Verify.Inputs.TestFiles = []string{"tests/a.test.js"}
	m := &supervisedFixtureV2{}
	m.run = func(ctx context.Context, _ phaseRequestV2, out, errOut, report io.Writer) phaseOutcomeV2 {
		_, _ = io.WriteString(out, strings.Repeat("界", c4verify.OutputLimit))
		return superviseV2(ctx, phaseStreamsV2{}, func() frameworkreport.Result { return frameworkreport.Result{Exited: true} }, func() error { return errors.New("do not emit /secret/path nonce=payload") }, func() error { return nil }, out, errOut, report, time.Second)
	}
	checks := goodChecksV2()
	got, _ := runPhasesV2(context.Background(), q, m, m, &checks)
	if len(got.Output) > c4verify.OutputLimit || !utf8.ValidString(got.Output) || !got.Truncated || !strings.HasSuffix(got.Output, "[foundry-v2 phase=test-node stage=cleanup_failed]\n") || strings.Contains(got.Output, "secret") {
		t.Fatalf("invalid failure diagnostic: bytes=%d truncated=%v suffix=%q", len(got.Output), got.Truncated, got.Output[max(0, len(got.Output)-100):])
	}
}

func TestV2FinalizationPromptOutcomesRetainAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, stage                          string
		launch, signaled, cleanup, resources bool
	}{
		{"positive", "", false, false, true, true},
		{"cleanup", "cleanup_failed", false, false, false, true},
		{"resources", "resources_failed", false, false, true, false},
		{"signal", "child_wait_failed", false, true, true, false},
		{"launch", "child_wait_failed", true, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launchErr := errors.New("owned launch error; /secret/path nonce=payload")
			m := &supervisedFixtureV2{}
			m.run = func(ctx context.Context, _ phaseRequestV2, out, errOut, report io.Writer) phaseOutcomeV2 {
				clean := func() error {
					if !tc.cleanup {
						return errors.New("/secret/cleanup")
					}
					return nil
				}
				resources := func() error {
					if !tc.resources {
						return errors.New("/secret/resources")
					}
					return nil
				}
				if tc.launch {
					return finalizeStartFailureV2(ctx, launchErr, clean, resources)
				}
				raw := oneReportV2
				if tc.signaled {
					raw = strings.SplitAfter(raw, "\n")[0]
				}
				return superviseV2(ctx, phaseStreamsV2{stdout: io.NopCloser(strings.NewReader("owned diagnostic\n")), report: io.NopCloser(strings.NewReader(raw))}, func() frameworkreport.Result {
					if tc.signaled {
						return frameworkreport.Result{Exited: true, ExitCode: -1, Err: errGuestV2}
					}
					return frameworkreport.Result{Exited: true}
				}, clean, resources, out, errOut, report, time.Second)
			}
			got := finalizationResponseV2(t, m)
			if got.Phases[0].Exited == tc.launch || got.Phases[0].CleanupConfirmed != tc.cleanup || got.Phases[0].ResourceLimitsOK != tc.resources {
				t.Fatalf("changed observed facts %+v", got)
			}
			if tc.stage == "" {
				if got.Result.Class != "passed" || got.Result.Tests != 1 || got.Result.Output != "owned diagnostic\n" {
					t.Fatalf("changed positive payload %+v", got)
				}
			} else if got.Result.Class != "infrastructure_failed" || !strings.HasSuffix(got.Result.Output, "[foundry-v2 phase=test-node stage="+tc.stage+"]\n") || strings.Contains(got.Result.Output, "secret") {
				t.Fatalf("unsafe failure annotation %+v", got)
			}
			if tc.launch && (!errors.Is(m.outcome.Process.Err, launchErr) || got.Result.Tests != 0 || got.Result.ExitCode != 0) {
				t.Fatalf("invented child/exit or lost launch error %+v", got)
			}
			if tc.signaled && (got.Result.ExitCode != -1 || got.Result.Tests != 0) {
				t.Fatalf("invented completed signal test %+v", got)
			}
		})
	}
}
func TestV2FinalizationCanceledWorkStillAttemptsCleanupAndBoundsMissingExit(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "deadline"}[deadline], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			cancel()
			exit, joined := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(exit) }); joinFinalizationFixtureV2(t, joined) }
			defer unblock()
			var cleaned atomic.Bool
			o := superviseV2(ctx, phaseStreamsV2{}, func() frameworkreport.Result {
				defer close(joined)
				<-exit
				return frameworkreport.Result{Exited: true}
			}, func() error { cleaned.Store(true); return nil }, func() error { return nil }, io.Discard, io.Discard, io.Discard, 10*time.Millisecond)
			if !cleaned.Load() || !o.CleanupConfirmed || o.ResourcesOK || o.Process.Exited || o.Process.Err == nil || o.Process.DeadlineExceeded != deadline || o.Process.Canceled == deadline {
				t.Fatalf("cancellation skipped cleanup or invented exit %+v", o)
			}
			frozen := o
			unblock()
			if !reflect.DeepEqual(o, frozen) {
				t.Fatal("late wait upgraded frozen outcome")
			}
		})
	}
}
func TestV2FinalizationStartFailureBoundsCleanup(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "canceled"}[canceled], func(t *testing.T) {
			release, joined := make(chan struct{}), make(chan struct{})
			defer func() { close(release); joinFinalizationFixtureV2(t, joined) }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
			}
			launchErr := errors.New("owned launch failure")
			started := time.Now()
			o := finalizeStartFailureV2(ctx, launchErr, func() error { defer close(joined); <-release; return nil }, func() error { return nil })
			if time.Since(started) > 2*time.Second || o.Process.Exited || !errors.Is(o.Process.Err, launchErr) || o.CleanupConfirmed || o.ResourcesOK || o.Process.Canceled != canceled {
				t.Fatalf("unbounded or invented failed-start facts %+v", o)
			}
		})
	}
}

type lateStreamV2 struct {
	release, joined chan struct{}
	r               *strings.Reader
	once            sync.Once
}

func (r *lateStreamV2) Read(b []byte) (int, error) {
	<-r.release
	n, e := r.r.Read(b)
	if e == io.EOF {
		r.once.Do(func() { close(r.joined) })
	}
	return n, e
}
func (r *lateStreamV2) Close() error { return nil }
func TestV2FinalizationLateStreamsCannotChangeFrozenDiagnosticsOrVerdict(t *testing.T) {
	release := make(chan struct{})
	stdout := &lateStreamV2{release: release, joined: make(chan struct{}), r: strings.NewReader("late stdout")}
	reportStream := &lateStreamV2{release: release, joined: make(chan struct{}), r: strings.NewReader(oneReportV2)}
	var once sync.Once
	unblock := func() {
		once.Do(func() { close(release) })
		joinFinalizationFixtureV2(t, stdout.joined)
		joinFinalizationFixtureV2(t, reportStream.joined)
	}
	defer unblock()
	var budget *diagnosticsV2
	m := &supervisedFixtureV2{}
	m.run = func(ctx context.Context, _ phaseRequestV2, out, errOut, report io.Writer) phaseOutcomeV2 {
		budget = out.(diagnosticWriterV2).budget
		return superviseV2(ctx, phaseStreamsV2{stdout: stdout, report: reportStream}, func() frameworkreport.Result { return frameworkreport.Result{Exited: true} }, func() error { return nil }, func() error { return nil }, out, errOut, report, 10*time.Millisecond)
	}
	got := finalizationResponseV2(t, m)
	if got.Result.Class != "infrastructure_failed" || got.Result.Tests != 0 || !got.Result.Truncated || !strings.Contains(got.Result.Output, "stage=stream_deadline") {
		t.Fatalf("incomplete EOF became success %+v", got)
	}
	before, outTrunc, errTrunc := budget.facts()
	frozen := m.outcome
	unblock()
	after, lateOut, lateErr := budget.facts()
	if before != after || outTrunc != lateOut || errTrunc != lateErr || !reflect.DeepEqual(m.outcome, frozen) {
		t.Fatal("late stream mutated frozen facts")
	}
}

func TestV2FinalizationPythonPartialReportAndResourcesRemainNegative(t *testing.T) {
	q := probeV2(t, "c4_python_model_v2")
	q.Action = "verify"
	q.Verify = &vmruntime.VerifyRequestV2{}
	q.Verify.Inputs.SourceRoots = []string{"src"}
	q.Verify.Inputs.TestFiles = []string{"tests/test_triple.py"}
	m := &supervisedFixtureV2{}
	m.run = func(ctx context.Context, _ phaseRequestV2, out, errOut, report io.Writer) phaseOutcomeV2 {
		partial := "{\"type\":\"suite_start\",\"seq\":1,\"schema_version\":1,\"runner\":\"python-unittest-v2\"}\n{\"type\":\"case_start\",\"seq\":2,\"id\":1}\n"
		return superviseV2(ctx, phaseStreamsV2{report: io.NopCloser(strings.NewReader(partial))}, func() frameworkreport.Result {
			return frameworkreport.Result{Exited: true, ExitCode: -1, Err: errGuestV2}
		}, func() error { return nil }, func() error { return errGuestV2 }, out, errOut, report, time.Second)
	}
	checks := goodChecksV2()
	got, phases := runPhasesV2(context.Background(), q, m, m, &checks)
	if got.Class != "infrastructure_failed" || got.Tests != 0 || got.ExitCode != -1 || len(phases) != 1 || phases[0].Phase != "test-python" || phases[0].ResourceLimitsOK || !phases[0].CleanupConfirmed || !checks.PhaseCleanupConfirmed {
		t.Fatalf("invented pressure/partial-report authority %+v %+v", got, phases)
	}
	for _, stage := range []failureStageV2{"", "cleanup_failed]\nnonce=secret"} {
		f := &fixtureV2{outcome: phaseOutcomeV2{Process: frameworkreport.Result{Err: errGuestV2}, CleanupConfirmed: true, ResourcesOK: true, FailureStage: stage}}
		checks = goodChecksV2()
		result, _ := runPhasesV2(context.Background(), q, f, f, &checks)
		if result.Output != "" {
			t.Fatal("unrecognized failure stage escaped into diagnostics", result.Output)
		}
	}
}
