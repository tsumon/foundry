package vmruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFixedNativeCommandsExcludeInheritedConfiguration(t *testing.T) {
	t.Setenv("PATH", "/attacker")
	t.Setenv("DYLD_INSERT_LIBRARIES", "/attacker.dylib")
	t.Setenv("FOUNDRY_VM_HELPER", "/attacker")
	for _, host := range []string{"darwin/arm64", "windows/amd64"} {
		m := Manifest{HostPlatform: host, Runtime: "runtime/foundry-vm-helper", Kernel: "kernel", RootFS: "initramfs.cpio.gz"}
		if strings.HasPrefix(host, "windows") {
			m.Runtime = "runtime/qemu-system-x86_64.exe"
		}
		c := nativeCommand(filepath.Join(t.TempDir(), "assets"), m)
		if len(c.Env) != 2 || strings.Contains(strings.Join(c.Env, " "), "attacker") {
			t.Fatal("inherited environment")
		}
		a := strings.Join(c.Args, " ")
		if strings.HasPrefix(host, "windows") && !strings.Contains(a, "-machine q35,sata=off,usb=off") {
			t.Fatal("implicit SATA/USB controller enabled")
		}
		if strings.HasPrefix(host, "windows") {
			if !strings.Contains(a, "-accel whpx") || !strings.Contains(a, "-nic none") || !strings.Contains(a, "-monitor none") {
				t.Fatal("missing VM restrictions")
			}
			for _, bad := range []string{"tcg", "virtfs", "netdev", "hostfwd", "shell", "cmd.exe"} {
				if strings.Contains(a, bad) {
					t.Fatal("unsafe invocation")
				}
			}
		}
		if !filepath.IsAbs(c.Path) {
			t.Fatal("executable search permitted")
		}
	}
}

// Linux6.18.58 drivers/tty/hvc/hvc_console.c intercepts selected-console input
// before tty termios: ^O arms SysRq, the next key is consumed even when the
// handler is disabled, and a second ^O forwards one literal ^O. Keep state
// across polls. This model never invokes a kernel command or starts a VM.
type hvcInputTest struct {
	selected, pending bool
	keys              int
}

func (f *hvcInputTest) read(raw []byte) []byte {
	var out []byte
	for _, b := range raw {
		if f.selected {
			if b == 0x0f {
				f.pending = !f.pending
				if f.pending {
					continue
				}
			} else if f.pending {
				f.keys++
				f.pending = false
				continue
			}
		}
		out = append(out, b)
	}
	return out
}

func controlBearingProbesTest(t *testing.T) []struct {
	request WireRequestV2
	raw     []byte
} {
	t.Helper()
	var found [2]bool
	frames := make([]struct {
		request WireRequestV2
		raw     []byte
	}, 2)
	for n := 0; n < 4096 && (!found[0] || !found[1]); n++ {
		q := probeV2Test("c4_node_model_v2")
		q.Nonce = fmt.Sprintf("%064x", n)
		var wire bytes.Buffer
		if err := WriteRequestV2(context.Background(), &wire, q, nil, ""); err != nil {
			t.Fatal(err)
		}
		raw := wire.Bytes()
		if bytes.Count(raw, []byte{0x0f}) != 1 {
			continue
		}
		i := 0
		if raw[len(raw)-1] == 0x0f {
			i = 1
		}
		if !found[i] {
			frames[i].request, frames[i].raw = q, bytes.Clone(raw)
			found[i] = true
		}
	}
	if !found[0] || !found[1] {
		t.Fatal("inert probe fixtures lack control/key and trailing-control digests")
	}
	return frames
}

func macKernelArgumentsTest(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "desktop", "sandbox", "macos", "main.swift"))
	if err != nil {
		t.Fatal(err)
	}
	var arguments []string
	for _, line := range strings.Split(string(raw), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "loader.commandLine = "); ok {
			value, err = strconv.Unquote(value)
			if err != nil {
				t.Fatal("Mac helper must supply one fixed kernel command line")
			}
			arguments = append(arguments, value)
		}
	}
	if len(arguments) != 1 {
		t.Fatal("Mac helper kernel policy is missing or ambiguous")
	}
	return arguments[0]
}

func TestNativeBootConsolePolicyPreservesBinaryFrames(t *testing.T) {
	policies := map[string]string{"mac_shared_v1_v2": macKernelArgumentsTest(t)}
	for _, schema := range []int{1, 2} {
		assets := bootAssets{HostPlatform: "windows/amd64", Runtime: "runtime/qemu-system-x86_64.exe", Kernel: "kernel", RootFS: "initramfs.cpio.gz"}
		if schema == 1 {
			assets = (Manifest{HostPlatform: assets.HostPlatform, Runtime: assets.Runtime, Kernel: assets.Kernel, RootFS: assets.RootFS}).boot()
		} else {
			assets = (ManifestV2{HostPlatform: assets.HostPlatform, Runtime: assets.Runtime, Kernel: assets.Kernel, RootFS: assets.RootFS}).boot()
		}
		c := nativeBootCommand(t.TempDir(), assets)
		for i := 0; i+1 < len(c.Args); i++ {
			if c.Args[i] == "-append" {
				policies[fmt.Sprintf("windows_v%d", schema)] = c.Args[i+1]
			}
		}
	}
	if len(policies) != 3 {
		t.Fatal("a production platform lacks a kernel policy")
	}
	frames := controlBearingProbesTest(t)
	for name, policy := range policies {
		t.Run(name, func(t *testing.T) {
			var consoles []string
			for _, arg := range strings.Fields(policy) {
				if value, ok := strings.CutPrefix(arg, "console="); ok {
					consoles = append(consoles, value)
				}
			}
			// An explicit preferred console prevents automatic HVC selection. A
			// ttyS console does not select HVC; Windows's own policy is checked
			// below and by the complete native-command golden.
			selectedHVC := len(consoles) == 0
			for _, console := range consoles {
				selectedHVC = selectedHVC || console == "hvc0"
			}
			for _, frame := range frames {
				input := (&hvcInputTest{selected: selectedHVC}).read(frame.raw)
				q, err := ReadRequestV2(context.Background(), bytes.NewReader(input), drainV2Test)
				if err != nil || q != frame.request || !bytes.Equal(input, frame.raw) {
					t.Fatal("production boot policy corrupts a valid binary request")
				}
				v := responseV2Test(q)
				var output bytes.Buffer
				if err = WriteResponseV2(&output, q, v.Checks, v.Result, v.Phases); err != nil {
					t.Fatal(err)
				}
				r := bytes.NewReader(output.Bytes())
				got, err := ReadResponseV2(r, q)
				if err != nil || got.Result.Class != "probe_passed" || !got.Checks.Valid() {
					t.Fatal("control-bearing request lost its authenticated response")
				}
				var extra [1]byte
				if n, err := r.Read(extra[:]); n != 0 || err != io.EOF {
					t.Fatal("authenticated frame lacks exact EOF")
				}
			}
			if len(consoles) != 1 || consoles[0] != "null" {
				t.Fatal("protocol device must be separate from the kernel console")
			}
		})
	}
}

func TestSelectedHVCConsoleConsumesBinaryControls(t *testing.T) {
	frames := controlBearingProbesTest(t)
	for i, frame := range frames {
		f := &hvcInputTest{selected: true}
		input := f.read(frame.raw)
		if _, err := ReadRequestV2(context.Background(), bytes.NewReader(input), drainV2Test); !errors.Is(err, ErrProtocolV2) {
			t.Fatal("kernel-filtered binary request was admitted")
		}
		wantLost, wantKeys := 2, 1
		if i == 1 {
			wantLost, wantKeys = 1, 0
		}
		if len(frame.raw)-len(input) != wantLost || f.keys != wantKeys {
			t.Fatal("selected-console model differs from pinned HVC filter")
		}
	}
	f := &hvcInputTest{selected: true}
	if len(f.read([]byte{0x0f})) != 0 || !bytes.Equal(f.read([]byte{0x0f}), []byte{0x0f}) || f.pending {
		t.Fatal("double-control handling changed")
	}
	if len(f.read([]byte{0x0f})) != 0 || len(f.read([]byte{'b'})) != 0 || f.keys != 1 || f.pending {
		t.Fatal("split-poll SysRq state changed")
	}
	// sysrq=0 cannot restore bytes consumed before handle_sysrq. A dropped
	// trailing control leaves the exact terminal read pending on live input.
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	done, written := make(chan error, 1), make(chan struct{})
	go func() { _, err := ReadRequestV2(context.Background(), input, drainV2Test); done <- err }()
	go func() { _, _ = writer.Write((&hvcInputTest{selected: true}).read(frames[1].raw)); close(written) }()
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("inert request bytes were not consumed")
	}
	select {
	case <-done:
		t.Fatal("lost terminal byte completed a live transaction")
	case <-time.After(20 * time.Millisecond):
	}
	writer.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrProtocolV2) {
			t.Fatal("incomplete transaction became valid")
		}
	case <-time.After(time.Second):
		t.Fatal("inert terminal read did not join")
	}
}
