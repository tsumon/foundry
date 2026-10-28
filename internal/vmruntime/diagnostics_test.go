package vmruntime

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"foundry/internal/c4verify"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const validHelperDiagnostic = `{"schema_version":1,"stage":"configuration_failed","exit_code":70,"error_domain":"VZErrorDomain","error_code":2}`

func TestReadinessFailureReceiptClassifiesWithoutRetainingSerial(t *testing.T) {
	for _, tc := range []struct{ name, serial, outcome, prefix string }{
		{"empty", "", "eof", ""},
		{"partial", "FOUNDRY", "short_read", ""},
		{"unknown", "PRIVATE_SENTINEL_SERIAL_DATA", "marker_mismatch", "unknown"},
		{"clock", "WHPX: Failed to query tsc frequency, hr=0xPRIVATE", "marker_mismatch", "whpx_clock_query_prefix"},
		{"crlf", "FOUNDRY_VM_READY_V1\r\n", "marker_mismatch", "ready_crlf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, q := testRuntime(t)
			s, e := r.Create(context.Background(), q)
			if e != nil {
				t.Fatal(e)
			}
			p := &diagnosticNative{failedStartNative: &failedStartNative{}, capture: &diagnosticBuffer{}, code: 1, serial: tc.serial}
			r.launch = func(context.Context, command) (nativeProcess, error) { return p, nil }
			if s.Start(context.Background()) == nil {
				t.Fatal("bad readiness admitted")
			}
			raw, e := os.ReadFile(filepath.Join(s.dir, "terminal.json"))
			if e != nil {
				t.Fatal(e)
			}
			var record struct {
				Native map[string]any `json:"native"`
			}
			if e = json.Unmarshal(raw, &record); e != nil {
				t.Fatal(e)
			}
			if record.Native["exchange"] != nil {
				t.Fatal("teardown overwrote the earlier readiness failure")
			}
			if record.Native["readiness"] != tc.outcome {
				t.Fatalf("readiness=%v want %s", record.Native["readiness"], tc.outcome)
			}
			if tc.prefix != "" && record.Native["readiness_prefix"] != tc.prefix {
				t.Fatalf("prefix=%v want %s", record.Native["readiness_prefix"], tc.prefix)
			}
			if strings.Contains(string(raw), "PRIVATE") {
				t.Fatal("raw serial leaked")
			}
			if _, ok := s.Result(); ok {
				t.Fatal("diagnostics authorized result")
			}
			if e = s.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestBoundedDiagnosticParser(t *testing.T) {
	exit := int64(70)
	for _, tc := range []struct{ name, input, status string }{
		{"valid", validHelperDiagnostic + "\n", "valid"},
		{"absent", "", "absent"},
		{"raw-secret", "password=never-retain-this", "rejected"},
		{"unknown", strings.Replace(validHelperDiagnostic, `"error_code":2`, `"error_code":2,"message":"never-retain-this"`, 1), "rejected"},
		{"duplicate", strings.Replace(validHelperDiagnostic, `"exit_code":70`, `"exit_code":70,"exit_code":0`, 1), "rejected"},
		{"trailing", validHelperDiagnostic + validHelperDiagnostic, "rejected"},
		{"alias", strings.Replace(validHelperDiagnostic, `"stage":`, `"Stage":`, 1), "rejected"},
		{"missing", strings.Replace(validHelperDiagnostic, `"schema_version":1,`, "", 1), "rejected"},
		{"null", strings.Replace(validHelperDiagnostic, `"error_code":2`, `"error_code":null`, 1), "rejected"},
		{"forged-success", strings.Replace(validHelperDiagnostic, `configuration_failed`, `passed`, 1), "rejected"},
		{"exit-mismatch", strings.Replace(validHelperDiagnostic, `"exit_code":70`, `"exit_code":71`, 1), "rejected"},
		{"domain-secret", strings.Replace(validHelperDiagnostic, `VZErrorDomain`, `never-retain-this`, 1), "rejected"},
		{"partial-pair", strings.Replace(validHelperDiagnostic, `,"error_code":2`, "", 1), "rejected"},
		{"overflow", validHelperDiagnostic + strings.Repeat("x", 2048), "overflow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &diagnosticBuffer{}
			n, e := b.Write([]byte(tc.input))
			if e != nil || n != len(tc.input) {
				t.Fatal("diagnostic capture disrupted child output")
			}
			d, status := b.parse(&exit)
			if status != tc.status {
				t.Fatalf("status=%s want %s", status, tc.status)
			}
			if (d != nil) != (tc.status == "valid") {
				t.Fatal("invalid diagnostic retained")
			}
			retained, _ := json.Marshal(d)
			if strings.Contains(string(retained), "never-retain-this") {
				t.Fatal("raw secret retained")
			}
			if len(b.data) > 1024 {
				t.Fatal("diagnostic buffer exceeded bound")
			}
		})
	}
}

type diagnosticNative struct {
	*failedStartNative
	capture      *diagnosticBuffer
	code         int64
	rawWaitError string
	serial       string
	serialReader io.ReadCloser
}

func (p *diagnosticNative) Output() io.ReadCloser {
	if p.serialReader != nil {
		return p.serialReader
	}
	return io.NopCloser(strings.NewReader(p.serial))
}

type failingReadinessReader struct{}

func TestMatchedReadinessIsNotRelabeledByConcurrentCancellation(t *testing.T) {
	r, q := testRuntime(t)
	s, e := r.Create(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	p := &diagnosticNative{failedStartNative: &failedStartNative{}, capture: &diagnosticBuffer{}, code: 1, serial: ReadyMarker}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.exchange(ctx, p, make(chan error, 1))
	raw, e := os.ReadFile(filepath.Join(s.dir, "terminal.json"))
	if e != nil {
		t.Fatal(e)
	}
	var record struct {
		Native nativeFacts `json:"native"`
	}
	if e = json.Unmarshal(raw, &record); e != nil {
		t.Fatal(e)
	}
	if record.Native.Readiness != "matched" {
		t.Fatalf("successful marker read mislabeled: %s", record.Native.Readiness)
	}
	if _, ok := s.Result(); ok {
		t.Fatal("canceled exchange authorized result")
	}
}

func (failingReadinessReader) Read([]byte) (int, error) { return 0, errors.New("PRIVATE_SENTINEL") }

func TestReadinessReadErrorAndCancellationRemainFailed(t *testing.T) {
	for _, outcome := range []string{"read_failed", "canceled", "deadline"} {
		r, q := testRuntime(t)
		s, e := r.Create(context.Background(), q)
		if e != nil {
			t.Fatal(e)
		}
		p := &diagnosticNative{failedStartNative: &failedStartNative{}, capture: &diagnosticBuffer{}, code: 1, serialReader: io.NopCloser(failingReadinessReader{})}
		ctx := context.Background()
		if outcome == "canceled" {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		if outcome == "deadline" {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer cancel()
		}
		started := make(chan error, 1)
		s.exchange(ctx, p, started)
		if e := <-started; e == nil || strings.Contains(e.Error(), "PRIVATE_SENTINEL") {
			t.Fatal("read error accepted or leaked")
		}
		raw, e := os.ReadFile(filepath.Join(s.dir, "terminal.json"))
		if e != nil {
			t.Fatal(e)
		}
		var record struct {
			Native nativeFacts `json:"native"`
		}
		if e = json.Unmarshal(raw, &record); e != nil {
			t.Fatal(e)
		}
		if record.Native.Readiness != outcome || record.Native.ReadinessPrefix != "" {
			t.Fatalf("wrong readiness: %+v", record.Native)
		}
		if _, ok := s.Result(); ok {
			t.Fatal("failed read authorized result")
		}
	}
}

func TestReadinessFactsDiscardUnknownValues(t *testing.T) {
	for _, f := range []nativeFacts{{Readiness: "PRIVATE_SENTINEL", ReadinessPrefix: "PRIVATE_SENTINEL"}, {Readiness: "eof", ReadinessPrefix: "whpx_clock_query_prefix"}, {Readiness: "marker_mismatch", ReadinessPrefix: "PRIVATE_SENTINEL"}} {
		safe := safeNativeFacts(f)
		raw, _ := json.Marshal(safe)
		if safe.ReadinessPrefix != "" || strings.Contains(string(raw), "PRIVATE_SENTINEL") {
			t.Fatal("invalid readiness data retained")
		}
	}
}

func (p *diagnosticNative) Wait() error {
	p.mu.Lock()
	p.terminated = true
	p.mu.Unlock()
	return errors.New(p.rawWaitError)
}
func (p *diagnosticNative) diagnostics() nativeFacts {
	d, status := p.capture.parse(&p.code)
	return nativeFacts{TerminationConfirmed: p.Terminated(), ExitCode: &p.code, AdmissionStage: "running", DiagnosticStatus: status, Helper: d}
}

func TestEarlyNativeFailureRetainsTypedFactsWithoutRawData(t *testing.T) {
	r, q := testRuntime(t)
	s, e := r.Create(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	capture := &diagnosticBuffer{}
	io.WriteString(capture, validHelperDiagnostic)
	p := &diagnosticNative{failedStartNative: &failedStartNative{}, capture: capture, code: 70, rawWaitError: s.wire.Secret + " never-retain-this"}
	r.launch = func(context.Context, command) (nativeProcess, error) { return p, nil }
	e = s.Start(context.Background())
	if e == nil || !strings.Contains(e.Error(), "trusted guest readiness missing") || !strings.Contains(e.Error(), "helper_stage=configuration_failed") || !strings.Contains(e.Error(), "exit=70") {
		t.Fatalf("early failure lacks combined typed facts: %v", e)
	}
	if strings.Contains(e.Error(), s.wire.Secret) || strings.Contains(e.Error(), "never-retain-this") {
		t.Fatal("raw error leaked")
	}
	waitErr := s.Wait(context.Background())
	if waitErr == nil || waitErr.Error() != e.Error() {
		t.Fatal("Wait lost finalized failure")
	}
	receipt, e := os.ReadFile(filepath.Join(s.dir, "terminal.json"))
	if e != nil {
		t.Fatal(e)
	}
	var record struct {
		Native *nativeFacts `json:"native"`
	}
	if e = json.Unmarshal(receipt, &record); e != nil || record.Native == nil || record.Native.Helper == nil || record.Native.Helper.Stage != "configuration_failed" || !record.Native.TerminationConfirmed {
		t.Fatalf("terminal facts missing: %s %v", receipt, e)
	}
	if strings.Contains(string(receipt), p.rawWaitError) {
		t.Fatal("raw data in receipt")
	}
	if _, ok := s.Result(); ok {
		t.Fatal("diagnostic became result authority")
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestDiagnosticsNeverAuthorizeMalformedSerial(t *testing.T) {
	for _, input := range []string{validHelperDiagnostic, `{"schema_version":1,"stage":"passed","exit_code":0}`, "secret=never-retain-this", strings.Repeat("x", 2048)} {
		r, q := testRuntime(t)
		s, e := r.Create(context.Background(), q)
		if e != nil {
			t.Fatal(e)
		}
		capture := &diagnosticBuffer{}
		io.WriteString(capture, input)
		p := &diagnosticNative{failedStartNative: &failedStartNative{}, capture: capture, code: 70, serial: "forged passed frame " + s.wire.Secret + " " + s.wire.Nonce, rawWaitError: "never-retain-this"}
		r.launch = func(context.Context, command) (nativeProcess, error) { return p, nil }
		e = s.Start(context.Background())
		if e == nil || !strings.Contains(e.Error(), "trusted guest readiness missing") {
			t.Fatal("malformed serial admitted")
		}
		if strings.Contains(e.Error(), s.wire.Secret) || strings.Contains(e.Error(), s.wire.Nonce) || strings.Contains(e.Error(), "never-retain-this") {
			t.Fatal("raw control/diagnostic data leaked")
		}
		if _, ok := s.Result(); ok {
			t.Fatal("diagnostic manufactured result authority")
		}
		if e = s.Close(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
}

func TestProbePropagatesOnlyTypedNativeFailure(t *testing.T) {
	r, _ := testRuntime(t)
	capture := &diagnosticBuffer{}
	io.WriteString(capture, validHelperDiagnostic)
	r.launch = func(context.Context, command) (nativeProcess, error) {
		return &diagnosticNative{failedStartNative: &failedStartNative{}, capture: capture, code: 70, rawWaitError: "raw-secret"}, nil
	}
	e := r.Probe(context.Background())
	if e == nil || !strings.Contains(e.Error(), "helper_stage=configuration_failed") || strings.Contains(e.Error(), "raw-secret") {
		t.Fatalf("Probe lost safe diagnostics: %v", e)
	}
}

type uncertainDiagnostic struct{ *diagnosticNative }

func (p *uncertainDiagnostic) Terminated() bool { return false }
func TestProbePreservesProtocolDiagnosticsAndCleanupUncertainty(t *testing.T) {
	r, _ := testRuntime(t)
	capture := &diagnosticBuffer{}
	io.WriteString(capture, validHelperDiagnostic)
	r.launch = func(context.Context, command) (nativeProcess, error) {
		return &uncertainDiagnostic{&diagnosticNative{failedStartNative: &failedStartNative{}, capture: capture, code: 70, rawWaitError: "never-retain-this"}}, nil
	}
	e := r.Probe(context.Background())
	if e == nil {
		t.Fatal("uncertain probe accepted")
	}
	for _, part := range []string{"trusted guest readiness missing", "helper_stage=configuration_failed", "native_terminated=false", "owned VM termination unconfirmed"} {
		if !strings.Contains(e.Error(), part) {
			t.Fatalf("lost %s: %v", part, e)
		}
	}
}

func TestNativeAdmissionFailureRecordsOnlyNumericStage(t *testing.T) {
	r, q := testRuntime(t)
	r.launch = func(context.Context, command) (nativeProcess, error) {
		return nil, admissionFailure("create_process", &os.PathError{Op: "exec", Path: "never-retain-this", Err: syscall.Errno(5)})
	}
	s, e := r.Create(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	e = s.Start(context.Background())
	if e == nil || !strings.Contains(e.Error(), "native_stage=create_process") || !strings.Contains(e.Error(), "native_error=5") || strings.Contains(e.Error(), "never-retain-this") {
		t.Fatalf("unsafe or missing admission facts: %v", e)
	}
	b, e := os.ReadFile(filepath.Join(s.dir, "terminal.json"))
	if e != nil || !strings.Contains(string(b), `"native_error_code":5`) || strings.Contains(string(b), "never-retain-this") {
		t.Fatal("safe numeric admission receipt missing")
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
}

// Only native I/O/process observations are inert; actual admission, request
// framing, Session exchange/Wait/Close and response authority remain production.
type exchangeObservedNative struct {
	*fakeNative
	waitErr     error
	unconfirmed bool
	stopped     chan struct{}
	stopOnce    sync.Once
	afterStop   func()
	afterWait   func()
}

func (p *exchangeObservedNative) Stop() error {
	p.stopOnce.Do(func() {
		p.fakeNative.Stop()
		if p.afterStop != nil {
			p.afterStop()
		}
		close(p.stopped)
	})
	return nil
}
func (p *exchangeObservedNative) Wait() error {
	<-p.done
	if p.afterWait != nil {
		p.afterWait()
	}
	return p.waitErr
}
func (p *exchangeObservedNative) Terminated() bool {
	return !p.unconfirmed && p.fakeNative.Terminated()
}
func exchangeFrameV2Test(q WireRequestV2, kind string) ([]byte, error) {
	r := responseV2Test(q)
	header := func(n uint32) []byte { return binary.BigEndian.AppendUint32([]byte(HeaderV2), n) }
	rawError := errors.New("PRIVATE_SENTINEL raw transport")
	switch kind {
	case "header_eof":
		return nil, nil
	case "header_short":
		return []byte(HeaderV2[:1]), nil
	case "header_read_failed":
		return nil, rawError
	case "header_invalid":
		return append([]byte(strings.Repeat("x", len(HeaderV2))), make([]byte, 4)...), nil
	case "length_rejected":
		return header(0), nil
	case "body_eof":
		return header(4), nil
	case "body_short":
		return append(header(4), '{'), nil
	case "body_read_failed":
		return header(4), rawError
	case "json_rejected":
		return append(header(1), '{'), nil
	case "mac_rejected":
		r.MAC = strings.Repeat("0", 64)
		raw, _ := json.Marshal(r)
		return rawV2Test(raw), nil
	case "semantics_rejected":
		r.Result.Tests = 0
		return signedResponseV2Test(q, r), nil
	case "trailing_data":
		return append(signedResponseV2Test(q, r), 'x'), nil
	case "eof_read_failed":
		return signedResponseV2Test(q, r), rawError
	case "assertion":
		r.Result = c4verify.Result{Class: "test_failed", Tests: 1, ExitCode: 1}
		r.Phases[0].ExitCode = 1
	}
	return signedResponseV2Test(q, r), nil
}
func TestV2ExchangeRetainsFirstHostFailureThroughClosedLifecycle(t *testing.T) {
	cases := []struct{ kind, stage, code string }{
		{"header_eof", "response", "header_eof"}, {"header_short", "response", "header_short"},
		{"header_read_failed", "response", "header_read_failed"}, {"header_invalid", "response", "header_invalid"},
		{"length_rejected", "response", "length_rejected"}, {"body_eof", "response", "body_eof"},
		{"body_short", "response", "body_short"}, {"body_read_failed", "response", "body_read_failed"},
		{"json_rejected", "response", "json_rejected"}, {"mac_rejected", "response", "mac_rejected"},
		{"semantics_rejected", "response", "semantics_rejected"}, {"trailing_data", "transport_eof", "trailing_data"},
		{"eof_read_failed", "transport_eof", "read_failed"}, {"wait", "native_wait", "unsuccessful"},
		{"unconfirmed", "native_wait", "termination_unconfirmed"}, {"assertion", "", ""},
		{"first", "response", "header_invalid"}, {"context", "session_context", "canceled"}, {"canceled", "response", "header_read_failed"}, {"deadline", "response", "header_read_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			r, q := testRuntimeV2(t, "c4_node_model_v2")
			s, e := r.CreateV2(context.Background(), q)
			if e != nil {
				t.Fatal(e)
			}
			parent, cancel := context.WithCancel(context.Background())
			if tc.kind == "deadline" {
				parent, cancel = context.WithTimeout(context.Background(), time.Second)
			}
			defer cancel()
			release := make(chan struct{})
			r.launch = func(ctx context.Context, _ command) (nativeProcess, error) {
				input, hostInput := io.Pipe()
				hostOutput, output := io.Pipe()
				p := &exchangeObservedNative{fakeNative: &fakeNative{in: hostInput, out: hostOutput, done: make(chan struct{})}, stopped: make(chan struct{}), unconfirmed: tc.kind == "unconfirmed"}
				if tc.kind == "wait" || tc.kind == "first" {
					p.waitErr = errors.New("PRIVATE_SENTINEL raw Wait " + s.v2.wire.Secret)
				}
				if tc.kind == "first" {
					p.afterStop = cancel
				}
				if tc.kind == "context" {
					p.afterWait = cancel
				}
				go func() {
					defer close(p.done)
					defer input.Close()
					defer output.Close()
					io.WriteString(output, ReadyMarkerV2)
					wire, e := ReadRequestV2(ctx, input, drainV2Test)
					if e != nil {
						return
					}
					<-release
					if tc.kind == "canceled" || tc.kind == "deadline" {
						<-p.stopped
						return
					}
					kind := tc.kind
					if kind == "first" {
						kind = "header_invalid"
					}
					frame, closeErr := exchangeFrameV2Test(wire, kind)
					output.Write(frame)
					if closeErr != nil {
						output.CloseWithError(closeErr)
					}
				}()
				return p, nil
			}
			if e = s.Start(parent); e != nil {
				close(release)
				t.Fatal(e)
			}
			if tc.kind == "canceled" {
				cancel()
			}
			close(release)
			e = s.Wait(context.Background())
			valid := tc.kind == "assertion"
			if (e == nil) != valid {
				t.Fatal("changed exchange authority", e)
			}
			if tc.stage == "response" && !errors.Is(e, ErrProtocolV2) {
				t.Fatal("public protocol matching changed", e)
			}
			if e != nil && strings.Contains(e.Error(), "PRIVATE_SENTINEL") {
				t.Fatal("raw transport/Wait text leaked")
			}
			if _, ok := s.Result(); ok != valid {
				t.Fatal("invalid result acquired authority")
			}
			if _, ok := s.AuthenticatedEvidenceV2(q); ok {
				t.Fatal("evidence exposed before Close")
			}
			closeErr := s.Close(context.Background())
			if (closeErr == nil) == (tc.kind == "unconfirmed") {
				t.Fatal("changed termination requirement", closeErr)
			}
			if _, ok := s.AuthenticatedEvidenceV2(q); ok != valid {
				t.Fatal("host diagnostic became authenticated evidence")
			}
			raw, e := os.ReadFile(filepath.Join(s.dir, "terminal.json"))
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(raw), "PRIVATE_SENTINEL") {
				t.Fatal("raw data persisted")
			}
			var terminal struct {
				Native struct {
					Exchange *struct {
						Stage   string `json:"stage"`
						Code    string `json:"code"`
						Context string `json:"context"`
						Stop    bool   `json:"exchange_stop_requested"`
					} `json:"exchange"`
				} `json:"native"`
			}
			if json.Unmarshal(raw, &terminal) != nil {
				t.Fatal("invalid terminal")
			}
			if valid {
				if terminal.Native.Exchange != nil {
					t.Fatal("success acquired failure diagnostic")
				}
				return
			}
			got := terminal.Native.Exchange
			contextWant := "live"
			if tc.kind == "canceled" || tc.kind == "deadline" {
				contextWant = tc.kind
			}
			if tc.kind == "context" {
				contextWant = "canceled"
			}
			stop := tc.stage != "native_wait" && tc.stage != "session_context"
			if got == nil || got.Stage != tc.stage || got.Code != tc.code || got.Context != contextWant || got.Stop != stop {
				t.Fatalf("lost first safe exchange failure: %+v want %s/%s/%s stop=%v", got, tc.stage, tc.code, contextWant, stop)
			}
		})
	}
}

type responseReadCounterV2 struct {
	r       *bytes.Reader
	lengths []int
}

func (r *responseReadCounterV2) Read(b []byte) (int, error) {
	r.lengths = append(r.lengths, len(b))
	return r.r.Read(b)
}
func TestV2ExchangeMetadataRetainsExactReadsAndPublicProtocolError(t *testing.T) {
	q, _, _ := fixtureV2Test(t, "c4_node_model_v2")
	raw := signedResponseV2Test(q, responseV2Test(q))
	reader := &responseReadCounterV2{r: bytes.NewReader(append(bytes.Clone(raw), 'x'))}
	if _, e := ReadResponseV2(reader, q); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(reader.lengths, []int{len(HeaderV2) + 4, len(raw) - len(HeaderV2) - 4}) {
		t.Fatal("metadata reads changed", reader.lengths)
	}
	if b, e := reader.r.ReadByte(); e != nil || b != 'x' {
		t.Fatal("response codec read ahead")
	}
	q.Nonce = "invalid"
	reader = &responseReadCounterV2{r: bytes.NewReader(raw)}
	_, e := ReadResponseV2(reader, q)
	var rejected *protocolFailureV2
	if !errors.Is(e, ErrProtocolV2) || !errors.As(e, &rejected) || rejected.code != "request_binding_rejected" || len(reader.lengths) != 0 {
		t.Fatal("invalid request consumed response or changed public error", e, reader.lengths)
	}
}
func TestV2ExchangeFactsRejectUnknownValues(t *testing.T) {
	for _, d := range []exchangeDiagnostic{
		{Stage: "PRIVATE_SENTINEL", Code: "header_eof", Context: "live"},
		{Stage: "response", Code: "PRIVATE_SENTINEL", Context: "live"},
		{Stage: "response", Code: "header_eof", Context: "PRIVATE_SENTINEL"},
		{Stage: "transport_eof", Code: "mac_rejected", Context: "live"},
		{Stage: "session_context", Code: "deadline", Context: "live"},
	} {
		safe := safeNativeFacts(nativeFacts{DiagnosticStatus: "absent", Exchange: &d})
		raw, _ := json.Marshal(safe)
		if safe.Exchange != nil || strings.Contains(string(raw), "PRIVATE_SENTINEL") {
			t.Fatal("unsafe exchange facts persisted", string(raw))
		}
	}
}

type rejectedExchangeInputV2 struct{ io.WriteCloser }

func (rejectedExchangeInputV2) Write([]byte) (int, error) {
	return 0, errors.New("PRIVATE_SENTINEL transfer")
}
func TestV2ExchangePreAcknowledgmentFailureRetainsOnlyClosedHostFacts(t *testing.T) {
	for _, stage := range []string{"request_transfer", "start_record"} {
		t.Run(stage, func(t *testing.T) {
			r, q := testRuntimeV2(t, "c4_node_model_v2")
			s, e := r.CreateV2(context.Background(), q)
			if e != nil {
				t.Fatal(e)
			}
			r.launch = fakeLaunchV2(false, false)
			if stage == "request_transfer" {
				r.launch = func(ctx context.Context, c command) (nativeProcess, error) {
					p, e := fakeLaunchV2(false, false)(ctx, c)
					if e != nil {
						return p, e
					}
					return recordingNativeV2{p, rejectedExchangeInputV2{p.Input()}}, nil
				}
			} else if e = os.WriteFile(filepath.Join(s.dir, "started.json"), []byte("owned collision"), 0600); e != nil {
				t.Fatal(e)
			}
			if e = s.Start(context.Background()); e == nil || strings.Contains(e.Error(), "PRIVATE_SENTINEL") {
				t.Fatal("unsafe or successful failed acknowledgment", e)
			}
			if s.Wait(context.Background()) == nil {
				t.Fatal("failed acknowledgment became valid")
			}
			if e = s.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			if _, ok := s.AuthenticatedEvidenceV2(q); ok {
				t.Fatal("pre-ack diagnostics granted authority")
			}
			raw, e := os.ReadFile(filepath.Join(s.dir, "terminal.json"))
			if e != nil {
				t.Fatal(e)
			}
			var receipt struct {
				Native nativeFacts `json:"native"`
			}
			if e = json.Unmarshal(raw, &receipt); e != nil {
				t.Fatal(e)
			}
			code := "failed"
			if stage == "start_record" {
				code = "persist_failed"
			}
			d := receipt.Native.Exchange
			if d == nil || d.Stage != stage || d.Code != code || d.Context != "live" || !d.StopRequested {
				t.Fatalf("missing first pre-ack diagnostic %+v", d)
			}
		})
	}
}

// Arm at the response EOF, then change Err from its first sampled value to a
// deadline before a possible second read. Stop ends the observation window.
type firstObservationContextV2 struct {
	context.Context
	first          error
	armed, stopped bool
	reads, atStop  int
}

func (c *firstObservationContextV2) Err() error {
	if !c.armed {
		return nil
	}
	if c.stopped {
		return context.DeadlineExceeded
	}
	c.reads++
	if c.reads == 1 {
		return c.first
	}
	return context.DeadlineExceeded
}

type firstObservationNativeV2 struct {
	*failedStartNative
	ctx    *firstObservationContextV2
	output io.ReadCloser
}

func (p *firstObservationNativeV2) Output() io.ReadCloser { return p.output }
func (p *firstObservationNativeV2) Wait() error           { return nil }
func (p *firstObservationNativeV2) Stop() error {
	if !p.ctx.stopped {
		p.ctx.atStop = p.ctx.reads
		p.ctx.stopped = true
	}
	return nil
}

type armObservationReaderV2 struct {
	r   *strings.Reader
	ctx *firstObservationContextV2
}

func (r armObservationReaderV2) Read(b []byte) (int, error) {
	n, e := r.r.Read(b)
	if e == io.EOF {
		r.ctx.armed = true
	}
	return n, e
}
func TestV2ExchangeFirstObservationSamplesContextOnce(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		first      error
	}{
		{"live_then_deadline", "live", nil}, {"deadline", "deadline", context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, q := testRuntimeV2(t, "c4_node_model_v2")
			s, e := r.CreateV2(context.Background(), q)
			if e != nil {
				t.Fatal(e)
			}
			ctx := &firstObservationContextV2{Context: context.Background(), first: tc.first}
			p := &firstObservationNativeV2{failedStartNative: &failedStartNative{terminated: true}, ctx: ctx}
			p.output = io.NopCloser(armObservationReaderV2{r: strings.NewReader(ReadyMarkerV2), ctx: ctx})
			// Use the actual exchange body with already admitted request/owned process
			// state, so the fixture is the context observed by that method directly.
			s.started, s.process = true, p
			started := make(chan error, 1)
			s.exchange(ctx, p, started)
			if e = <-started; e != nil {
				t.Fatal("request was not acknowledged", e)
			}
			if e = s.Wait(context.Background()); !errors.Is(e, ErrProtocolV2) {
				t.Fatal("response rejection changed", e)
			}
			if e = s.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			raw, e := os.ReadFile(filepath.Join(s.dir, "terminal.json"))
			if e != nil {
				t.Fatal(e)
			}
			var record struct {
				Native nativeFacts `json:"native"`
			}
			if e = json.Unmarshal(raw, &record); e != nil {
				t.Fatal(e)
			}
			d := record.Native.Exchange
			if ctx.atStop != 1 || d == nil || d.Context != tc.want || d.Context == "canceled" || d.Stage != "response" || d.Code != "header_eof" {
				t.Fatalf("first observation mixed context reads: reads=%d diagnostic=%+v want=%s", ctx.atStop, d, tc.want)
			}
			if _, valid := s.AuthenticatedEvidenceV2(q); valid {
				t.Fatal("diagnostic granted response authority")
			}
		})
	}
}
