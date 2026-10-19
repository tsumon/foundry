package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
)

// Repeated diagnostics must not turn the normal HTTP workbench into an
// authorization, directory, inference or approval trigger, even in simulation.
func TestAcceptanceDiagnosticsDoNotDispatchOrChangeTaskAuthority(t *testing.T) {
	runtime := startProtocolAcceptanceForTest(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	request := func(method, path string) []byte {
		t.Helper()
		req, err := http.NewRequest(method, runtime.URL+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", runtime.URL)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("diagnostic HTTP status=%d read=%v", response.StatusCode, err)
		}
		return raw
	}
	request(http.MethodPost, "/api/session")
	state := func() []byte {
		t.Helper()
		missions, err := runtime.store.ListMissions()
		if err != nil {
			t.Fatal(err)
		}
		events, err := runtime.store.Events(0, 1000)
		if err != nil {
			t.Fatal(err)
		}
		accounts, err := runtime.store.ListModelAccounts()
		if err != nil {
			t.Fatal(err)
		}
		runtime.protocol.mu.Lock()
		defer runtime.protocol.mu.Unlock()
		return mustJSON(t, map[string]any{
			"missions": missions, "events": events, "accounts": accounts,
			"authorization_requests": len(runtime.protocol.requests),
			"token_calls":            runtime.protocol.tokenCalls, "revocations": runtime.protocol.revokeCalls,
			"directory_calls": runtime.protocol.directories, "inference_calls": runtime.protocol.modelHits,
		})
	}
	before := state()
	accounts, err := runtime.store.ListModelAccounts()
	if err != nil || len(accounts) == 0 {
		t.Fatal("fixture accounts unavailable", err)
	}
	for pass := 0; pass < 2; pass++ {
		for _, account := range accounts {
			raw := request(http.MethodGet, "/api/model-accounts/"+account.ID+"/acceptance")
			var response struct {
				Acceptance domain.ModelAccountAcceptance `json:"acceptance"`
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				t.Fatal(err)
			}
			report := response.Acceptance
			if report.AccountID != account.ID || report.AccountRevision != account.Revision || report.LiveAccess != "blocked" || report.Quota != "unknown" || report.Scope != "account_routes_only" || report.Checks == nil || report.Routes == nil {
				t.Fatal("diagnostic lost its account binding or closed boundary")
			}
			for _, secret := range runtime.protocol.secretMarkers() {
				if secret != "" && strings.Contains(string(raw), secret) {
					t.Fatal("diagnostic exposed an ephemeral fixture credential")
				}
			}
		}
	}
	if !bytes.Equal(before, state()) {
		t.Fatal("read-only diagnostics dispatched upstream or changed account/task/approval evidence")
	}
}
