package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
)

func TestProductionModelsHaveNoLiveOrSimulationBypass(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	secrets := diagnosticSecrets{}
	runtime := newProductionModels(s, model.NewClient(secrets), secrets)
	defer func() {
		if err := runtime.siwc.Close(); err != nil {
			t.Errorf("close SIWC service: %v", err)
		}
	}()
	if runtime.router.SimulationEnabled() || runtime.siwc.SimulationEnabled() {
		t.Fatal("production model runtime enabled a simulation origin")
	}
	selection := domain.RoleModelSelection{
		Profile: domain.Profile{ID: "profile", AccountID: "account", RouteID: "route", Protocol: "responses", BaseURL: "https://api.openai.com/v1", Model: "gpt-test"},
		Account: &domain.ModelAccount{ID: "account", Revision: 1, Name: "Account", Provider: "openai", BaseURL: "https://api.openai.com/v1", AuthMethod: "api_key", BillingMode: "api_credits", Enabled: true},
		Route:   &domain.ModelRoute{ID: "route", AccountID: "account", Adapter: "openai_api_responses", Protocol: "responses", BaseURL: "https://api.openai.com/v1", Environment: "live", SpendClass: "explicit_api", ExtraUsage: "off"},
	}
	err = runtime.router.PreflightModelSelection(context.Background(), 2, selection)
	var provider *model.ProviderError
	if !errors.As(err, &provider) || provider.Code != "live_provider_access_pending_confirmation" || provider.Certainty != "not_sent" {
		t.Fatalf("live gate=%v", err)
	}
}

func TestLoadConfigRejectsNonLoopbackAddress(t *testing.T) {
	t.Setenv("FOUNDRY_DATA_DIR", t.TempDir())
	for _, address := range []string{"0.0.0.0:4317", ":4317", "localhost:4317", "127.0.0.2:4317", "127.0.0.1"} {
		t.Run(address, func(t *testing.T) {
			t.Setenv("FOUNDRY_ADDR", address)
			if _, err := loadConfig(); err == nil {
				t.Fatalf("address %q accepted", address)
			}
		})
	}
}

func TestLoadConfigAcceptsConfiguredLoopbackAndPaths(t *testing.T) {
	dataDir := t.TempDir()
	webDir := t.TempDir()
	t.Setenv("FOUNDRY_DATA_DIR", dataDir)
	t.Setenv("FOUNDRY_ADDR", "127.0.0.1:9876")
	t.Setenv("FOUNDRY_WEB_DIR", webDir)
	t.Setenv("FOUNDRY_DOCKER", "/opt/local/bin/docker")
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.address != "127.0.0.1:9876" || config.dataDir != filepath.Clean(dataDir) || config.webDir != filepath.Clean(webDir) || config.dockerPath != "/opt/local/bin/docker" {
		t.Fatalf("config=%+v", config)
	}
}

func TestControlledCreationSwitchRejectsMalformedValues(t *testing.T) {
	t.Setenv("FOUNDRY_DATA_DIR", t.TempDir())
	t.Setenv("FOUNDRY_WEB_DIR", "")
	t.Setenv("FOUNDRY_ADDR", "127.0.0.1:4317")
	for _, value := range []string{"true", "false", "", "invalid", " true ", "yes"} {
		t.Setenv("FOUNDRY_DISABLE_CONTROLLED_READ_CREATION", value)
		cfg, err := loadConfig()
		if value == "true" || value == "false" || value == "" {
			if err != nil || cfg.disableControlledReadCreation != (value == "true") {
				t.Fatal(value, cfg, err)
			}
		} else if err == nil {
			t.Fatal("malformed creation switch accepted", value)
		}
	}
}

func TestDataDirectoryLockIsExclusiveAndReleasable(t *testing.T) {
	directory := t.TempDir()
	first, err := acquireDataLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireDataLock(directory); err == nil {
		t.Fatal("second controller acquired the data directory")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := acquireDataLock(directory)
	if err != nil {
		t.Fatalf("released lock was not reacquired: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFoundryProcessHelper(t *testing.T) {
	if os.Getenv("FOUNDRY_PROCESS_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestSIGINTClosesAuthenticatedEventStreamWithoutShutdownTimeout(t *testing.T) {
	address := unusedAddress(t)
	command := exec.Command(os.Args[0], "-test.run=^TestFoundryProcessHelper$")
	command.Env = append(os.Environ(),
		"FOUNDRY_PROCESS_HELPER=1",
		"FOUNDRY_ADDR="+address,
		"FOUNDRY_DATA_DIR="+t.TempDir(),
		"FOUNDRY_WEB_DIR=",
		"FOUNDRY_DOCKER=docker",
	)
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	})

	origin := "http://" + address
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var cookie *http.Cookie
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, origin+"/api/session", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		response, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK && len(response.Cookies()) == 1 {
				cookie = response.Cookies()[0]
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if cookie == nil {
		t.Fatalf("server did not start: %s", output.String())
	}
	healthRequest, err := http.NewRequest(http.MethodGet, origin+"/api/model-routes", nil)
	if err != nil {
		t.Fatal(err)
	}
	healthRequest.AddCookie(cookie)
	healthResponse, err := (&http.Client{Timeout: time.Second}).Do(healthRequest)
	if err != nil {
		t.Fatal(err)
	}
	runtimeBody, err := io.ReadAll(healthResponse.Body)
	healthResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if healthResponse.StatusCode != http.StatusOK || !strings.Contains(string(runtimeBody), `"live_authorization":false`) || !strings.Contains(string(runtimeBody), `"live_discovery":false`) || !strings.Contains(string(runtimeBody), `"live_inference":false`) || !strings.Contains(string(runtimeBody), `"simulation_inference":false`) || !strings.Contains(string(runtimeBody), `"simulation_authorization":false`) || !strings.Contains(string(runtimeBody), `"automatic_paid_fallback":false`) {
		t.Fatalf("unsafe production model runtime status=%d body=%s", healthResponse.StatusCode, runtimeBody)
	}

	streamContext, cancelStream := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStream()
	streamRequest, err := http.NewRequestWithContext(streamContext, http.MethodGet, origin+"/api/events?after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	streamRequest.AddCookie(cookie)
	streamClient := &http.Client{}
	streamResponse, err := streamClient.Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer streamResponse.Body.Close()
	if streamResponse.StatusCode != http.StatusOK || streamResponse.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream status=%d content-type=%q", streamResponse.StatusCode, streamResponse.Header.Get("Content-Type"))
	}

	started := time.Now()
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server exited with error after %s: %v\n%s", time.Since(started), err, output.String())
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("server needed %s to close an active stream", elapsed)
		}
	case <-time.After(3 * time.Second):
		_ = command.Process.Kill()
		<-done
		t.Fatalf("server did not close an active stream before the shutdown deadline\n%s", output.String())
	}
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func TestStartupRecoversDiscoveriesBeforeServing(t *testing.T) {
	directory := t.TempDir()
	db, err := store.Open(filepath.Join(directory, "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pending", "dispatching"} {
		c, err := db.SaveMCPConnection(domain.MCPConnection{ID: id, Name: id, Endpoint: "http://127.0.0.1:9/mcp", Protocol: domain.MCPProtocol, Enabled: true}, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = db.BeginDiscovery(domain.DiscoveryOperation{ID: id, ConnectionID: id, ConnectionRevision: c.Revision, Endpoint: c.Endpoint, Consent: true, AuthorizedAt: time.Now().UTC().Format(time.RFC3339Nano), Budget: domain.FixedDiscoveryBudget()})
		if err != nil {
			t.Fatal(err)
		}
		if id == "dispatching" {
			if _, err = db.ClaimDiscovery(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	address := unusedAddress(t)
	origin := "http://" + address
	command := exec.Command(os.Args[0], "-test.run=^TestFoundryProcessHelper$")
	command.Env = append(os.Environ(), "FOUNDRY_PROCESS_HELPER=1", "FOUNDRY_ADDR="+address, "FOUNDRY_DATA_DIR="+directory, "FOUNDRY_WEB_DIR=", "FOUNDRY_DOCKER=docker")
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	})
	client := &http.Client{Timeout: 200 * time.Millisecond}
	ready := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("POST", origin+"/api/session", strings.NewReader(`{}`))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("server did not become ready")
	}
	db, err = store.Open(filepath.Join(directory, "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"pending", "dispatching"} {
		op, err := db.GetDiscovery(id)
		if err != nil || op.Status != "outcome_unknown" || op.ErrorCode != "interrupted" || op.Requests != nil || op.ResponseBytes != nil {
			t.Fatalf("%#v %v", op, err)
		}
	}
	if err = command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err = command.Wait(); err != nil {
		t.Fatal(err)
	}
}
