package vmruntime

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
)

const diagnosticLimit = 1024

// Helper diagnostics describe failure only. They never establish readiness,
// test evidence, or native termination and never contain arbitrary messages.
type helperDiagnostic struct {
	SchemaVersion int    `json:"schema_version"`
	Stage         string `json:"stage"`
	ExitCode      int64  `json:"exit_code"`
	ErrorDomain   string `json:"error_domain,omitempty"`
	ErrorCode     *int64 `json:"error_code,omitempty"`
}
type nativeFacts struct {
	TerminationConfirmed bool              `json:"termination_confirmed"`
	StopRequested        *bool             `json:"stop_requested,omitempty"`
	Readiness            string            `json:"readiness,omitempty"`
	ReadinessPrefix      string            `json:"readiness_prefix,omitempty"`
	ExitCode             *int64            `json:"exit_code,omitempty"`
	Signal               *int              `json:"signal,omitempty"`
	AdmissionStage       string            `json:"admission_stage,omitempty"`
	NativeErrorCode      *int64            `json:"native_error_code,omitempty"`
	DiagnosticStatus     string            `json:"diagnostic_status"`
	Helper               *helperDiagnostic `json:"helper,omitempty"`
	QEMU                 *qemuDiagnostic   `json:"qemu,omitempty"`
}
type diagnosticBuffer struct {
	mu       sync.Mutex
	data     []byte
	overflow bool
}

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := diagnosticLimit - len(b.data)
	if len(p) > left {
		b.overflow = true
		p = p[:left]
	}
	b.data = append(b.data, p...)
	return n, nil
}
func (b *diagnosticBuffer) discard() { b.mu.Lock(); defer b.mu.Unlock(); clear(b.data); b.data = nil }
func (b *diagnosticBuffer) parse(exit *int64) (*helperDiagnostic, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.overflow {
		return nil, "overflow"
	}
	if len(b.data) == 0 {
		return nil, "absent"
	}
	var d helperDiagnostic
	if strictJSON(b.data, &d) != nil || !validHelperReport(d, exit) {
		return nil, "rejected"
	}
	return &d, "valid"
}
func validHelperReport(d helperDiagnostic, exit *int64) bool {
	expected := map[string]int64{"invalid_arguments": 64, "invalid_input": 65, "platform_unsupported": 69, "configuration_failed": 70, "start_failed": 70, "stop_failed": 71, "owner_stopped": 71, "stop_deadline": 72, "guest_failed": 73}
	code, ok := expected[d.Stage]
	if !ok || d.SchemaVersion != 1 || d.ExitCode != code || exit == nil || d.ExitCode != *exit {
		return false
	}
	if (d.ErrorDomain == "") != (d.ErrorCode == nil) {
		return false
	}
	switch d.ErrorDomain {
	case "", "VZErrorDomain", "NSPOSIXErrorDomain", "NSCocoaErrorDomain", "other":
	default:
		return false
	}
	return true
}
func admissionStageAllowed(s string) bool {
	switch s {
	case "", "helper_spawn", "running", "create_job", "job_limits", "stdio", "handle_list", "process_arguments", "create_process", "created_suspended", "assign_job", "context_admission", "resume_thread":
		return true
	}
	return false
}
func safeNativeFacts(f nativeFacts) nativeFacts {
	switch f.Readiness {
	case "", "matched", "eof", "short_read", "read_failed", "deadline", "canceled", "marker_mismatch":
	default:
		f.Readiness = ""
	}
	if f.Readiness != "marker_mismatch" || (f.ReadinessPrefix != "unknown" && f.ReadinessPrefix != "whpx_clock_query_prefix" && f.ReadinessPrefix != "ready_crlf") {
		f.ReadinessPrefix = ""
	}
	if !admissionStageAllowed(f.AdmissionStage) {
		f.AdmissionStage = ""
	}
	if f.ExitCode != nil && (*f.ExitCode < 0 || *f.ExitCode > 4294967295) {
		f.ExitCode = nil
	}
	if f.Signal != nil && (*f.Signal <= 0 || *f.Signal > 127 || f.ExitCode != nil) {
		f.Signal = nil
	}
	if f.NativeErrorCode != nil && (*f.NativeErrorCode < 0 || *f.NativeErrorCode > 4294967295) {
		f.NativeErrorCode = nil
	}
	switch f.DiagnosticStatus {
	case "absent", "valid", "rejected", "overflow", "unrecognized", "read_failed", "incomplete":
	default:
		f.DiagnosticStatus = "rejected"
	}
	if f.Helper != nil && (!validHelperReport(*f.Helper, f.ExitCode) || f.DiagnosticStatus != "valid") {
		f.Helper = nil
		f.DiagnosticStatus = "rejected"
	}
	if f.QEMU != nil && (!f.TerminationConfirmed || f.ExitCode == nil || *f.ExitCode == 0 || f.DiagnosticStatus != "valid" || !validQEMUDiagnostic(*f.QEMU)) {
		f.QEMU = nil
		f.DiagnosticStatus = "rejected"
	}
	return f
}
func processDiagnostics(p nativeProcess) nativeFacts {
	f := nativeFacts{DiagnosticStatus: "absent"}
	if provider, ok := p.(interface{ diagnostics() nativeFacts }); ok {
		f = provider.diagnostics()
	}
	f.TerminationConfirmed = p.Terminated()
	return safeNativeFacts(f)
}
func withNativeFacts(e error, f nativeFacts) error {
	if e == nil {
		return nil
	}
	f = safeNativeFacts(f)
	parts := []string{fmt.Sprintf("native_terminated=%t", f.TerminationConfirmed)}
	if f.StopRequested != nil {
		parts = append(parts, fmt.Sprintf("stop_requested=%t", *f.StopRequested))
	}
	if f.Readiness != "" {
		parts = append(parts, "readiness="+f.Readiness)
	}
	if f.ReadinessPrefix != "" {
		parts = append(parts, "readiness_prefix="+f.ReadinessPrefix)
	}
	if f.AdmissionStage != "" {
		parts = append(parts, "native_stage="+f.AdmissionStage)
	}
	if f.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit=%d", *f.ExitCode))
	}
	if f.Signal != nil {
		parts = append(parts, fmt.Sprintf("signal=%d", *f.Signal))
	}
	if f.NativeErrorCode != nil {
		parts = append(parts, fmt.Sprintf("native_error=%d", *f.NativeErrorCode))
	}
	if f.Helper != nil {
		parts = append(parts, "helper_stage="+f.Helper.Stage)
		if f.Helper.ErrorCode != nil {
			parts = append(parts, fmt.Sprintf("domain=%s code=%d", f.Helper.ErrorDomain, *f.Helper.ErrorCode))
		}
	}
	if f.DiagnosticStatus != "absent" {
		if f.QEMU != nil {
			parts = append(parts, "qemu="+strings.Join(f.QEMU.Codes, ","))
		}
		parts = append(parts, "diagnostics="+f.DiagnosticStatus)
	}
	return fmt.Errorf("%w; %s", e, strings.Join(parts, " "))
}

type nativeAdmissionError struct{ facts nativeFacts }

func (e *nativeAdmissionError) Error() string { return "native process admission failed" }
func admissionFailure(stage string, e error) error {
	f := nativeFacts{AdmissionStage: stage, DiagnosticStatus: "absent"}
	var number syscall.Errno
	if errors.As(e, &number) {
		n := int64(number)
		f.NativeErrorCode = &n
	}
	return &nativeAdmissionError{facts: safeNativeFacts(f)}
}
func admissionFacts(e error) nativeFacts {
	var failure *nativeAdmissionError
	if errors.As(e, &failure) {
		return failure.facts
	}
	return nativeFacts{DiagnosticStatus: "absent"}
}
