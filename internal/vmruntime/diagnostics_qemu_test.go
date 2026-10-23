package vmruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestQEMUDiagnosticsProjectOnlyFixedCategories(t *testing.T) {
	cases := []struct{ input, code, status string }{
		{"qemu: cannot initialize crypto: Unable to create cryptographic provider: PRIVATE_SENTINEL", "crypto_initialization", "valid"},
		{"qemu-system-x86_64.exe: Property 'pc-q35-machine.usb' not found", "machine_property", "valid"},
		{"WHPX: Failed to create partition, hr=0x80370301", "whpx_partition", "valid"},
		{"qemu: could not load PC BIOS 'PRIVATE_SENTINEL'", "firmware", "valid"},
		{"qemu: -chardev stdio: could not open PRIVATE_SENTINEL", "character_device", "valid"},
		{"qemu: unable to find CPU model 'PRIVATE_SENTINEL'", "cpu_model", "valid"},
		{"qemu: failed to allocate memory PRIVATE_SENTINEL", "memory_allocation", "valid"},
		{"qemu: invalid option 'PRIVATE_SENTINEL'", "option", "valid"},
		{"qemu: could not open kernel file 'PRIVATE_SENTINEL': No such file or directory", "kernel_load", "valid"},
		{"qemu: error reading initrd PRIVATE_SENTINEL: Failed to open file", "initrd_load", "valid"},
		{"qemu: failed to find romfile PRIVATE_SENTINEL", "rom_load", "valid"},
		{"qemu: 'PRIVATE_SENTINEL' is not a valid device model name", "device_configuration", "valid"},
		{"qemu: qemu aio context not ready PRIVATE_SENTINEL", "event_loop", "valid"},
		{"qemu: Failed to CreateFileMapping PRIVATE_SENTINEL", "memory_mapping", "valid"},
		{"qemu: failed to WSAEventSelect() PRIVATE_SENTINEL", "host_socket", "valid"},
		{"qemu: Could not open 'PRIVATE_SENTINEL'", "resource_access", "valid"},
		{"PRIVATE_SENTINEL", "", "unrecognized"},
		{"", "", "absent"},
		{strings.Repeat("PRIVATE_SENTINEL", 200), "", "overflow"},
	}
	for _, tc := range cases {
		b := &diagnosticBuffer{}
		_, _ = io.WriteString(b, tc.input)
		d, status := b.parseQEMU()
		if status != tc.status {
			t.Fatalf("status %s want %s", status, tc.status)
		}
		if tc.code != "" && (d == nil || len(d.Codes) != 1 || d.Codes[0] != tc.code) {
			t.Fatalf("bad category: %#v", d)
		}
		if tc.code == "" && d != nil {
			t.Fatal("untrusted metadata retained")
		}
		raw, _ := json.Marshal(d)
		if strings.Contains(string(raw), "PRIVATE_SENTINEL") {
			t.Fatal("raw diagnostic leak")
		}
		b.discard()
		if len(b.data) != 0 {
			t.Fatal("raw bytes retained")
		}
	}
}
func TestQEMUPipeDrainAndCancellationAreBounded(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		r, w, e := os.Pipe()
		if e != nil {
			t.Fatal(e)
		}
		capture := newQEMUCapture(r)
		input := "WHPX: Failed to create partition"
		if oversized {
			input = strings.Repeat("PRIVATE_SENTINEL", 100000)
		}
		done := make(chan struct{})
		go func() { _, _ = io.WriteString(w, input); w.Close(); close(done) }()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		d, status := capture.finish(ctx)
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("stderr writer blocked")
		}
		if oversized {
			if d != nil || status != "overflow" {
				t.Fatal("overflow admitted")
			}
		} else if d == nil || status != "valid" {
			t.Fatal("lost bounded diagnostic")
		}
	}
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	capture := newQEMUCapture(r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if d, status := capture.finish(ctx); d != nil || status != "incomplete" {
		t.Fatal("unclosed stderr treated as complete")
	}
	select {
	case <-capture.done:
	case <-time.After(time.Second):
		t.Fatal("owned pipe did not close")
	}
}

type qemuFailedReader struct{}

func (qemuFailedReader) Read([]byte) (int, error) { return 0, errors.New("PRIVATE_SENTINEL") }
func (qemuFailedReader) Close() error             { return nil }

func TestQEMUCaptureDistinguishesTransportAndUnrecognizedFailures(t *testing.T) {
	for _, tc := range []struct {
		reader io.ReadCloser
		want   string
	}{
		{qemuFailedReader{}, "read_failed"},
		{io.NopCloser(strings.NewReader("PRIVATE_SENTINEL")), "unrecognized"},
	} {
		capture := newQEMUCapture(tc.reader)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		d, status := capture.finish(ctx)
		cancel()
		if d != nil || status != tc.want {
			t.Fatalf("status=%s want=%s", status, tc.want)
		}
		f := safeNativeFacts(nativeFacts{DiagnosticStatus: status})
		if f.DiagnosticStatus != tc.want || f.QEMU != nil {
			t.Fatal("capture failure classification lost")
		}
	}
}
func TestQEMUCodesCannotAuthorizeSuccessOrRetainUnknownText(t *testing.T) {
	failed, passed := int64(1), int64(0)
	valid := &qemuDiagnostic{Codes: []string{"whpx_partition"}}
	f := safeNativeFacts(nativeFacts{TerminationConfirmed: true, ExitCode: &failed, DiagnosticStatus: "valid", QEMU: valid})
	if f.QEMU == nil {
		t.Fatal("typed failure lost")
	}
	for _, f := range []nativeFacts{
		{TerminationConfirmed: true, ExitCode: &passed, DiagnosticStatus: "valid", QEMU: valid},
		{ExitCode: &failed, DiagnosticStatus: "valid", QEMU: valid},
		{TerminationConfirmed: true, ExitCode: &failed, DiagnosticStatus: "valid", QEMU: &qemuDiagnostic{Codes: []string{"PRIVATE_SENTINEL"}}},
	} {
		if safeNativeFacts(f).QEMU != nil {
			t.Fatal("invalid failure metadata retained")
		}
	}
}
