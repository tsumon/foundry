package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/engine"
)

// Catches accidental reuse of persistent data, missing synthetic labels,
// incorrect harness wiring or bypassed normal context/tool/plan boundaries.
func TestAcceptanceRuntimeFreshIsolatedNormalHTTPFlow(t *testing.T) {
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("acceptance static fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime, err := startAcceptance(web)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close(); os.RemoveAll(runtime.Root) })
	second, err := startAcceptance(web)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close(); os.RemoveAll(second.Root) })
	if runtime.Root == second.Root || runtime.Repo == second.Repo || runtime.URL == second.URL || strings.HasSuffix(runtime.URL, ":4337") || !strings.HasPrefix(runtime.URL, "http://127.0.0.1:") {
		t.Fatal("harness is not separately isolated", runtime.Root, second.Root)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	var cookie *http.Cookie
	request := func(method, path string, body any) []byte {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, runtime.URL+path, strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		if method != "GET" {
			req.Header.Set("Origin", runtime.URL)
			req.Header.Set("Content-Type", "application/json")
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err = io.ReadAll(response.Body)
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("%s %s: %d %s %v", method, path, response.StatusCode, raw, err)
		}
		if path == "/api/session" {
			if len(response.Cookies()) != 1 {
				t.Fatal("missing production session cookie")
			}
			cookie = response.Cookies()[0]
		}
		return raw
	}
	decode := func(raw []byte, value any) {
		t.Helper()
		if err := json.Unmarshal(raw, value); err != nil {
			t.Fatal(err)
		}
	}
	if raw := request("GET", "/", nil); string(raw) != "acceptance static fixture" {
		t.Fatalf("static serving: %s", raw)
	}
	request("POST", "/api/session", map[string]any{})
	var empty struct {
		Missions []domain.Mission `json:"missions"`
	}
	decode(request("GET", "/api/missions", nil), &empty)
	var frozen domain.Mission
	for _, mission := range empty.Missions {
		if mission.ID == runtime.AccountMissionID {
			frozen = mission
			break
		}
	}
	if len(empty.Missions) < 6 || frozen.ID == "" || frozen.ModelSnapshot == nil || frozen.ModelSnapshot.SchemaVersion != 1 || frozen.Profiles[domain.Worker].CredentialVersion != 1 || frozen.State != "awaiting_approval" || !strings.Contains(frozen.Title, "SYNTHETIC") {
		t.Fatal("harness must retain the clearly synthetic, normally planned frozen V1 compatibility task")
	}
	frozenID := frozen.ID
	var profiles struct {
		Profiles []domain.Profile `json:"profiles"`
	}
	decode(request("GET", "/api/profiles", nil), &profiles)
	if len(profiles.Profiles) < 2 {
		t.Fatal("expected legacy, account and protocol synthetic profiles")
	}
	var legacy domain.Profile
	for _, p := range profiles.Profiles {
		if p.ID == "synthetic-stub" {
			legacy = p
		}
	}
	if legacy.ID == "" || !strings.Contains(legacy.Name, "SYNTHETIC") || legacy.HasKey {
		t.Fatal("profile is not explicitly synthetic and credential-free", profiles)
	}
	var accounts struct {
		Accounts []domain.ModelAccount `json:"accounts"`
	}
	decode(request("GET", "/api/model-accounts", nil), &accounts)
	var a domain.ModelAccount
	for _, account := range accounts.Accounts {
		if account.ID == runtime.AccountID {
			a = account
			break
		}
	}
	if a.ID == "" || a.CurrentCredentialVersion != 2 {
		t.Fatal("synthetic compatibility account should demonstrate rotation after frozen V1")
	}
	var detail struct {
		Credentials []struct {
			Version int64  `json:"version"`
			State   string `json:"state"`
		} `json:"credentials"`
	}
	decode(request("GET", "/api/model-accounts/"+a.ID, nil), &detail)
	if len(detail.Credentials) != 2 || detail.Credentials[0].State != "available" || detail.Credentials[1].State != "available" {
		t.Fatal("missing published synthetic versions")
	}
	var historical struct {
		Mission domain.Mission `json:"mission"`
	}
	decode(request("GET", "/api/missions/"+frozenID, nil), &historical)
	if historical.Mission.Profiles[domain.Worker].CredentialVersion != 1 {
		t.Fatal("rotation rewrote frozen task")
	}
	request("PUT", "/api/model-accounts/"+a.ID, map[string]any{"name": "SYNTHETIC renamed", "enabled": false, "expected_revision": a.Revision})
	decode(request("GET", "/api/missions/"+frozenID, nil), &historical)
	if historical.Mission.ModelSnapshot.Roles[domain.Worker].Account.Name != "SYNTHETIC R5A account" || !historical.Mission.ModelSnapshot.Roles[domain.Worker].Account.Enabled {
		t.Fatal("live metadata replaced frozen history")
	}
	var selection engine.LocalMCPSelectionView
	decode(request("GET", "/api/mcp-local-test", nil), &selection)
	if !selection.Eligible || !selection.TestOnly || selection.Selection == nil {
		t.Fatal("owned local MCP is not available", selection)
	}
	var capabilities struct {
		Repositories []domain.GitRepository  `json:"repositories"`
		Observations []domain.GitObservation `json:"git_observations"`
	}
	decode(request("GET", "/api/capabilities", nil), &capabilities)
	if len(capabilities.Repositories) != 1 || len(capabilities.Observations) != 1 {
		t.Fatalf("missing synthetic repository catalog %+v", capabilities)
	}
	repository := capabilities.Repositories[0]
	if err := runtime.store.PutSettings(allRoleBindings(legacy.ID, nil)); err != nil {
		t.Fatal(err)
	}
	create := engine.CreateRequest{Title: "SYNTHETIC browser acceptance", Goal: "Inspect synthetic reference, then update synthetic text", RepoPath: runtime.Repo, WorkspaceMode: domain.WorkspaceModeControlledMCPReadV2, ToolMode: domain.ToolModeControlledMCPReadV2, Spec: domain.Spec{Scope: []string{"src/"}, Acceptance: []string{"test -f /workspace/src/main.txt"}, Image: "synthetic:stub", ProfileID: legacy.ID, Research: true, MaxCalls: 16}, MCPSelection: selection.Selection, CapabilitySelection: &domain.CapabilitySelection{RepositoryID: repository.ID, RepositoryRevision: repository.Revision, ObservationID: capabilities.Observations[0].ID}}
	var result struct {
		Mission domain.Mission `json:"mission"`
	}
	decode(request("POST", "/api/missions", create), &result)
	m := result.Mission
	base := "/api/missions/" + m.ID
	request("POST", base+"/plan", map[string]any{"revision": m.Revision, "controlled_consent": true})
	wait := func(state, stage string) domain.Mission {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			decode(request("GET", base, nil), &result)
			if result.Mission.State == state && (stage == "" || result.Mission.Stage == stage) {
				return result.Mission
			}
			if result.Mission.State == "blocked" {
				t.Fatal("harness flow blocked", result.Mission.Error)
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("harness checkpoint timeout", result.Mission.State)
		return domain.Mission{}
	}
	m = wait("planning", "awaiting_tool_approval")
	var calls struct {
		Invocations []engine.ResearchMCPInvocationView `json:"invocations"`
	}
	decode(request("GET", base+"/mcp-invocations", nil), &calls)
	if len(calls.Invocations) != 0 {
		t.Fatal("harness bypassed tool approval")
	}
	var card struct {
		Decision engine.ResearchMCPDecisionView `json:"decision"`
	}
	decode(request("GET", base+"/mcp-decision", nil), &card)
	request("POST", base+"/mcp-proposals/"+card.Decision.ProposalID+"/decision", map[string]any{"expected_revision": m.Revision, "proposal_digest": card.Decision.ProposalDigest, "decision": "approve", "request_id": "harness-approval"})
	m = wait("awaiting_approval", "")
	decode(request("GET", base+"/mcp-invocations", nil), &calls)
	if len(calls.Invocations) != 1 || calls.Invocations[0].Status != "succeeded" || m.Research == nil || len(m.Research.Findings) != 1 || len(m.Research.Findings[0].Sources) != 1 || m.Research.Findings[0].Sources[0].InvocationID != calls.Invocations[0].ID || len(m.Evidence) != 0 || len(m.Changes) != 0 || m.Delivery != nil {
		t.Fatalf("normal harness handoff failed: %+v %+v", m, calls)
	}
	request("POST", base+"/approve", map[string]any{"revision": m.Revision})
	m = wait("awaiting_delivery", "")
	if len(m.Evidence) != 1 || len(m.Evidence[0].Results) != 1 || !strings.Contains(m.Evidence[0].Results[0].Output, "SYNTHETIC STUB") || m.Delivery != nil {
		t.Fatal("stub runner is not labeled or bypassed delivery", m.Evidence)
	}
	runtime.Close()
	for _, endpoint := range []string{runtime.URL, selection.Endpoint} {
		response, err := client.Get(endpoint)
		if response != nil {
			response.Body.Close()
		}
		if err == nil {
			t.Fatal("owned listener survived harness shutdown", endpoint)
		}
	}
	if _, err := os.Stat(filepath.Join(runtime.Root, "foundry.db")); err != nil {
		t.Fatal("shutdown did not retain acceptance artifacts", err)
	}
	response, err := client.Get(second.URL)
	if err != nil {
		t.Fatal("shutdown affected another isolated harness", err)
	}
	response.Body.Close()
}
