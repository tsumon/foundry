//go:build windows

package vmruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type cryptoEnvironmentResult struct {
	Acquired  bool   `json:"acquired"`
	Released  bool   `json:"released"`
	Win32Code uint32 `json:"win32_code"`
}

// The fixed helper uses no named/persisted key container, private key, account,
// network, VM or repository payload. Match QEMU crypto/random-platform.c.
func TestWindowsCryptoEnvironmentHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "foundry-ephemeral-crypto-diagnostic" {
		return
	}
	var provider windows.Handle
	err := windows.CryptAcquireContext(&provider, nil, nil, windows.PROV_RSA_FULL, windows.CRYPT_VERIFYCONTEXT|windows.CRYPT_SILENT)
	r := cryptoEnvironmentResult{Acquired: err == nil}
	if err == nil {
		r.Released = windows.CryptReleaseContext(provider, 0) == nil
	} else {
		var code syscall.Errno
		if !errors.As(err, &code) || code == 0 {
			os.Exit(2)
		}
		r.Win32Code = uint32(code)
	}
	if json.NewEncoder(os.Stdout).Encode(r) != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

// Characterization, not a success gate for QEMU or a production fallback.
// Catch missing/truncated helper results, leaked extra text, wrong API facts,
// and lost exact process ownership while measuring both controlled inputs.
func TestWindowsCryptoEnvironmentDiagnostic(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("test executable unavailable")
	}
	systemRoot, err := windows.GetSystemWindowsDirectory()
	if err != nil || !filepath.IsAbs(systemRoot) {
		t.Fatal("authoritative system directory unavailable")
	}
	for _, withRoot := range []bool{false, true} {
		t.Run(fmt.Sprintf("systemroot_%t", withRoot), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c := command{Path: executable, Dir: filepath.Dir(executable), Env: []string{"LANG=C", "LC_ALL=C"}, Args: []string{"-test.run=^TestWindowsCryptoEnvironmentHelper$", "--", "foundry-ephemeral-crypto-diagnostic"}}
			if withRoot {
				c.Env = append(c.Env, "SYSTEMROOT="+systemRoot)
			}
			p, err := launchNative(ctx, c)
			if p != nil {
				defer p.Output().Close()
				defer p.Input().Close()
				defer func() {
					if !p.Terminated() {
						_ = p.Stop()
						cleanup, finish := context.WithTimeout(context.Background(), 5*time.Second)
						defer finish()
						_ = p.(*windowsProcess).observeTermination(cleanup)
					}
					if !p.Terminated() {
						t.Error("exact helper termination not confirmed")
					}
				}()
			}
			if err != nil {
				t.Fatal("owned helper admission failed")
			}
			type readResult struct {
				data []byte
				err  error
			}
			read := make(chan readResult, 1)
			go func() {
				data, err := io.ReadAll(io.LimitReader(p.Output(), 257))
				read <- readResult{data, err}
			}()
			var result readResult
			select {
			case result = <-read:
			case <-ctx.Done():
				t.Fatal("owned helper read deadline")
			}
			if result.err != nil || len(result.data) > 256 {
				t.Fatal("invalid bounded helper output")
			}
			var r cryptoEnvironmentResult
			if strictJSON(result.data, &r) != nil || r.Acquired != r.Released || (r.Acquired && r.Win32Code != 0) || (!r.Acquired && r.Win32Code == 0) {
				t.Fatal("invalid crypto provider diagnostic")
			}
			if p.(*windowsProcess).observeTermination(ctx) != nil || !p.Terminated() {
				t.Fatal("helper did not terminate")
			}
			facts := p.(*windowsProcess).diagnostics()
			if facts.ExitCode == nil || *facts.ExitCode != 0 {
				t.Fatal("helper failed")
			}
			t.Logf("ephemeral_crypto systemroot=%t acquired=%t released=%t win32_code=%d", withRoot, r.Acquired, r.Released, r.Win32Code)
		})
	}
}
