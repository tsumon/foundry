package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLocalMCPRejectsArbitraryEndpointAndUnexpectedArgs(t *testing.T) {
	t.Setenv("FOUNDRY_DATA_DIR", t.TempDir())
	t.Setenv("FOUNDRY_WEB_DIR", "")
	t.Setenv("FOUNDRY_ADDR", "127.0.0.1:4317")
	for _, args := range [][]string{{"--mcp-url=http://127.0.0.1:9"}, {"--local-mcp-test", "unexpected"}, {"--local-mcp-test=invalid"}} {
		if err := runWithArgs(args); err == nil {
			t.Fatalf("unsupported options accepted: %v", args)
		}
	}
}

func TestLocalMCPExplicitFlagAndOwnedShutdown(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "foundry")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin/go"), "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default_off", true: "explicit_on"}[enabled], func(t *testing.T) {
			address := unusedAddress(t)
			origin := "http://" + address
			args := []string{}
			if enabled {
				args = append(args, "--local-mcp-test")
			}
			cmd := exec.Command(binary, args...)
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			cmd.Env = append(os.Environ(), "FOUNDRY_ADDR="+address, "FOUNDRY_DATA_DIR="+t.TempDir(), "FOUNDRY_WEB_DIR=", "FOUNDRY_DOCKER=docker")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if cmd.ProcessState == nil {
					cmd.Process.Kill()
					cmd.Wait()
				}
			})
			transport := http.DefaultTransport.(*http.Transport).Clone()
			defer transport.CloseIdleConnections()
			client := &http.Client{Timeout: 500 * time.Millisecond, Transport: transport}
			var cookie *http.Cookie
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				req, _ := http.NewRequest("POST", origin+"/api/session", strings.NewReader(`{}`))
				req.Header.Set("Origin", origin)
				req.Header.Set("Content-Type", "application/json")
				response, err := client.Do(req)
				if err == nil {
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
					if response.StatusCode == 200 && len(response.Cookies()) == 1 {
						cookie = response.Cookies()[0]
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if cookie == nil {
				t.Fatal("server did not start")
			}
			req, _ := http.NewRequest("GET", origin+"/api/mcp-local-test", nil)
			req.AddCookie(cookie)
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(response.Body)
			response.Body.Close()
			var view struct {
				Enabled  bool   `json:"enabled"`
				Eligible bool   `json:"eligible"`
				Endpoint string `json:"endpoint"`
			}
			if err = json.Unmarshal(raw, &view); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 || view.Enabled != enabled || view.Eligible != enabled {
				t.Fatalf("enabled=%v: %d %s", enabled, response.StatusCode, raw)
			}
			if enabled {
				response, err = client.Get(view.Endpoint)
				if err != nil {
					t.Fatal("owned listener unavailable", err)
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
			}
			transport.CloseIdleConnections()
			if err = cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err = <-done:
				if err != nil {
					t.Fatal(err)
				}
			// Lifecycle ownership, not a latency benchmark: allow the server's
			// configured 10s graceful deadline plus process cleanup. The separate
			// active-SSE regression continues to enforce its tighter 2s contract.
			case <-time.After(12 * time.Second):
				cmd.Process.Signal(syscall.SIGQUIT)
				<-done
				t.Fatalf("shutdown timed out\n%s", output.String())
			}
			if enabled {
				response, err = client.Get(view.Endpoint)
				if err == nil {
					response.Body.Close()
					t.Fatal("owned MCP listener survived shutdown")
				}
			}
		})
	}
}
