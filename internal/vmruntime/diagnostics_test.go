package vmruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const validHelperDiagnostic = `{"schema_version":1,"stage":"configuration_failed","exit_code":70,"error_domain":"VZErrorDomain","error_code":2}`

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
}

func (p *diagnosticNative) Output() io.ReadCloser { return io.NopCloser(strings.NewReader(p.serial)) }

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
