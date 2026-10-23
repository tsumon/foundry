package vmruntime

import (
	"context"
	"io"
	"strings"
)

// Categories are diagnostic hints only. Raw stderr is never persisted, and
// neither these hints nor their absence authenticate guest execution.
type qemuDiagnostic struct {
	Codes []string `json:"codes"`
}

func qemuCodeAllowed(code string) bool {
	switch code {
	case "machine_property", "whpx_partition", "whpx_initialization", "firmware", "character_device", "cpu_model", "memory_allocation", "option", "kernel_load", "initrd_load", "rom_load", "device_configuration", "event_loop", "memory_mapping", "host_socket", "resource_access":
		return true
	}
	return false
}
func validQEMUDiagnostic(d qemuDiagnostic) bool {
	if len(d.Codes) == 0 || len(d.Codes) > 8 {
		return false
	}
	seen := map[string]bool{}
	for _, code := range d.Codes {
		if !qemuCodeAllowed(code) || seen[code] {
			return false
		}
		seen[code] = true
	}
	return true
}
func (b *diagnosticBuffer) parseQEMU() (*qemuDiagnostic, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.overflow {
		return nil, "overflow"
	}
	if len(b.data) == 0 {
		return nil, "absent"
	}
	s := strings.ToLower(string(b.data))
	has := func(parts ...string) bool {
		for _, part := range parts {
			if !strings.Contains(s, part) {
				return false
			}
		}
		return true
	}
	code := ""
	// Most specific first; never include arbitrary text, paths or numbers.
	switch {
	case has("property '", "-machine.", "not found"):
		code = "machine_property"
	case has("whpx", "partition"):
		code = "whpx_partition"
	case has("whpx"):
		code = "whpx_initialization"
	case has("could not load", "bios") || has("could not find", "bios"):
		code = "firmware"
	case has("chardev") || has("character device"):
		code = "character_device"
	case has("cpu model"):
		code = "cpu_model"
	case has("allocate memory") || has("memory allocation"):
		code = "memory_allocation"
	case has("invalid option") || has("invalid parameter") || has("unsupported machine type"):
		code = "option"
	case has("could not open kernel file") || has("could not load kernel") || has("error while loading elf kernel") || has("error loading uncompressed kernel") || has("invalid kernel header"):
		code = "kernel_load"
	case has("error reading initrd") || has("initrd is too large"):
		code = "initrd_load"
	case has("failed to find romfile"):
		code = "rom_load"
	case has("not a valid device model name"):
		code = "device_configuration"
	case has("qemu aio context not ready") || has("qemu_add_wait_object") || has("failed to create event"):
		code = "event_loop"
	case has("failed to createfilemapping") || has("failed to mapviewoffile"):
		code = "memory_mapping"
	case has("winsock") || has("socketpair") || has("wsaeventselect"):
		code = "host_socket"
	case has("could not open") || has("failed to open") || has("could not load") || has("fread() failed"):
		code = "resource_access"
	}
	if code == "" {
		return nil, "unrecognized"
	}
	return &qemuDiagnostic{Codes: []string{code}}, "valid"
}

type qemuCapture struct {
	reader io.ReadCloser
	done   chan struct{}
	report *qemuDiagnostic
	status string
}

func newQEMUCapture(r io.ReadCloser) *qemuCapture {
	c := &qemuCapture{reader: r, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		defer r.Close()
		b := &diagnosticBuffer{}
		defer b.discard()
		// Continue draining after the capture limit so a noisy child cannot block.
		if _, err := io.Copy(b, r); err != nil {
			c.status = "read_failed"
			return
		}
		c.report, c.status = b.parseQEMU()
	}()
	return c
}
func (c *qemuCapture) finish(ctx context.Context) (*qemuDiagnostic, string) {
	select {
	case <-c.done:
		return c.report, c.status
	default:
	}
	select {
	case <-c.done:
		return c.report, c.status
	case <-ctx.Done():
		// Only close the exact pipe we created. A partial stream is not accepted.
		_ = c.reader.Close()
		return nil, "incomplete"
	}
}
