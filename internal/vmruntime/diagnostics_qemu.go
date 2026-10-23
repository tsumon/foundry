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
	case "machine_property", "whpx_partition", "whpx_initialization", "firmware", "character_device", "cpu_model", "memory_allocation", "option":
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
	}
	if code == "" {
		return nil, "rejected"
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
			c.status = "rejected"
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
		return nil, "rejected"
	}
}
