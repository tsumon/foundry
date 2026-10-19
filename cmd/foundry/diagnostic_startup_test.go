package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/store"
)

func TestDiagnosticProcessHelper(t *testing.T) {
	if os.Getenv("FOUNDRY_DIAGNOSTIC_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	if err := runWithSecrets([]string{"--local-mcp-test"}, diagnosticSecrets{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// No startup regression can reach the real platform credential backend.
type diagnosticSecrets struct{}

func (diagnosticSecrets) Get(string) (string, error) { panic("unexpected secret read") }
func (diagnosticSecrets) Set(string, string) error   { panic("unexpected secret write") }
func (diagnosticSecrets) Delete(string) error        { return errors.New("synthetic cleanup unavailable") }

func seedPendingCredential(t *testing.T, s *store.Store) {
	t.Helper()
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "synthetic-account", Name: "synthetic", Provider: "custom", BaseURL: "http://127.0.0.1:9", AuthMethod: "api_key", BillingMode: "unknown", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "synthetic-pending", SecretRef: "foundry-model-account-synthetic-pending"}, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
}

func TestStartupCredentialCleanupFailureDoesNotBecomeReady(t *testing.T) {
	directory := t.TempDir()
	s, err := store.Open(filepath.Join(directory, "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	seedPendingCredential(t, s)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FOUNDRY_DATA_DIR", directory)
	t.Setenv("FOUNDRY_ADDR", unusedAddress(t))
	t.Setenv("FOUNDRY_WEB_DIR", "")
	if err = runWithSecrets(nil, diagnosticSecrets{}); err == nil || err.Error() != "recover model accounts failed" {
		t.Fatalf("cleanup failure startup=%v", err)
	}
}

// These go through the production startup entry point, not a manually built
// HTTP handler: a bad snapshot must not remove the history listener or allow
// any of the earlier recovery/observer/MCP startup stages to mutate storage.
func TestDiagnosticStartupPreservesHistoryAndDeniesAllMutations(t *testing.T) {
	for _, snapshot := range []string{`"broken"`, `{"schema_version":2,"roles":{},"digest":"future"}`} {
		t.Run(snapshot, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "foundry.db")
			s, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.CreateMission(domain.Mission{ID: "historical", Title: "historical", State: "planning", Budget: domain.Budget{UsedCalls: 3}})
			if err != nil {
				t.Fatal(err)
			}
			beforeGeneration := domain.GenerationRef{ID: strings.Repeat("2", 32), Digest: strings.Repeat("a", 64)}
			m, err := s.CreateMission(domain.Mission{ID: "generation-history", State: "running", WorkspaceMode: domain.WorkspaceModeGenerationsV1, CandidateGeneration: &beforeGeneration, CandidateDigest: beforeGeneration.Digest, Epoch: 2, Plan: &domain.Plan{Revision: 3}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.BeginLocalOperation(domain.LocalOperation{ID: strings.Repeat("1", 32), MissionID: m.ID, Kind: "apply", ExpectedRevision: m.Revision, Epoch: m.Epoch, PlanRevision: 3, Before: beforeGeneration, BaseDigest: strings.Repeat("b", 64), IntentDigest: strings.Repeat("c", 64), ApprovalDigest: strings.Repeat("d", 64)})
			if err != nil {
				t.Fatal(err)
			}
			c, err := s.SaveMCPConnection(domain.MCPConnection{ID: "pending-discovery", Name: "synthetic", Endpoint: "http://127.0.0.1:9/mcp", Protocol: domain.MCPProtocol, Enabled: true}, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = s.BeginDiscovery(domain.DiscoveryOperation{ID: "pending-discovery", ConnectionID: c.ID, ConnectionRevision: c.Revision, Endpoint: c.Endpoint, Consent: true, AuthorizedAt: time.Now().UTC().Format(time.RFC3339Nano), Budget: domain.FixedDiscoveryBudget()})
			if err != nil {
				t.Fatal(err)
			}
			seedPendingCredential(t, s)
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite3", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			raw := `{"id":"invalid","title":"invalid","revision":1,"state":"planning","budget":{"used_calls":3},"workspace_mode":"account_snapshot_v1","model_snapshot":` + snapshot + `}`
			if _, err = db.Exec(`INSERT INTO missions VALUES('invalid',1,'planning','then',?)`, raw); err != nil {
				t.Fatal(err)
			}
			before := diagnosticRows(t, db)
			origin, client, cookie, stop := startDiagnosticProcess(t, directory)
			request := func(method, path string, authenticated bool) (int, string) {
				t.Helper()
				req, err := http.NewRequest(method, origin+path, strings.NewReader(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Origin", origin)
				req.Header.Set("Content-Type", "application/json")
				if authenticated {
					req.AddCookie(cookie)
				}
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				if err != nil {
					t.Fatal(err)
				}
				return res.StatusCode, string(body)
			}
			if status, _ := request("GET", "/api/missions", false); status != 401 {
				t.Fatalf("unauthenticated history=%d", status)
			}
			for _, path := range []string{"/api/missions", "/api/missions/historical", "/api/missions/generation-history/local-operations", "/api/missions/invalid", "/api/missions/invalid/invocations", "/api/missions/invalid/local-operations", "/api/catalog", "/api/profiles", "/api/model-accounts", "/api/capabilities", "/api/hooks/records"} {
				if status, body := request("GET", path, true); status != 200 {
					t.Fatalf("history %s=%d %s", path, status, body)
				}
			}
			if _, body := request("GET", "/api/missions", true); !strings.Contains(body, `"title":"historical"`) || !strings.Contains(body, `"model_snapshot_status":"invalid_or_unsupported"`) {
				t.Fatalf("history=%s", body)
			}
			status, body := request("GET", "/api/health", true)
			var health struct {
				Status        string          `json:"status"`
				ReadOnly      bool            `json:"read_only"`
				RecoveryError string          `json:"recovery_error"`
				Capabilities  map[string]bool `json:"capabilities"`
			}
			if err = json.Unmarshal([]byte(body), &health); err != nil {
				t.Fatal(err)
			}
			if status != 200 || health.Status != "recovery_limited" || !health.ReadOnly || health.RecoveryError != "invalid_mission_model_snapshot" {
				t.Fatalf("health=%d %s", status, body)
			}
			for _, key := range []string{"planning", "execution", "docker_verification", "local_patch_delivery", "model_directory_probe", "magpie_discovery", "mcp_discovery", "task_local_mcp", "controlled_read_creation", "controlled_builtin_tools", "skill_binding", "observation_hooks"} {
				if health.Capabilities[key] {
					t.Fatalf("diagnostic capability %s still enabled", key)
				}
			}
			if _, body = request("GET", "/api/hooks", true); !strings.Contains(body, `"state":"not_started"`) {
				t.Fatalf("observer started: %s", body)
			}
			if _, body = request("GET", "/api/mcp-local-test", true); !strings.Contains(body, `"enabled":false`) {
				t.Fatalf("owned MCP started: %s", body)
			}
			for _, path := range []string{"/api/missions", "/api/missions/historical/plan", "/api/missions/historical/chat", "/api/missions/historical/pause", "/api/missions/historical/execute", "/api/missions/invalid/plan", "/api/model-accounts", "/api/model-accounts/any/credentials", "/api/profiles", "/api/profiles/any/probe", "/api/settings", "/api/magpie/discover", "/api/capabilities/mcp/any/discover", "/api/catalog/commands", "/api/hooks/rules", "/api/future-route"} {
				for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
					status, body := request(method, path, true)
					if status != 503 || !strings.Contains(body, `"code":"recovery_read_only"`) {
						t.Fatalf("mutation %s %s=%d %s", method, path, status, body)
					}
				}
			}
			stop()
			if after := diagnosticRows(t, db); !reflect.DeepEqual(before, after) {
				t.Fatal("diagnostic startup or reads changed durable rows (missions, events, budgets, ledgers or observer/MCP state)")
			}
		})
	}
}

func startDiagnosticProcess(t *testing.T, directory string) (string, *http.Client, *http.Cookie, func()) {
	t.Helper()
	address := unusedAddress(t)
	origin := "http://" + address
	command := exec.Command(os.Args[0], "-test.run=^TestDiagnosticProcessHelper$")
	command.Env = append(os.Environ(), "FOUNDRY_DIAGNOSTIC_HELPER=1", "FOUNDRY_ADDR="+address, "FOUNDRY_DATA_DIR="+directory, "FOUNDRY_WEB_DIR=", "FOUNDRY_DOCKER="+filepath.Join(directory, "must-not-run-docker"))
	output, err := os.CreateTemp(t.TempDir(), "startup-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	command.Stdout, command.Stderr = output, output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_ = command.Process.Kill()
			<-done
		}
	})
	client := &http.Client{Timeout: 500 * time.Millisecond, Transport: &http.Transport{DisableKeepAlives: true}}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			stopped = true
			log, _ := os.ReadFile(output.Name())
			t.Fatalf("production startup exited before readable listener: %v\n%s", err, log)
		default:
		}
		req, _ := http.NewRequest("POST", origin+"/api/session", strings.NewReader(`{}`))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode == 200 && len(res.Cookies()) == 1 {
				return origin, client, res.Cookies()[0], func() {
					t.Helper()
					if err := command.Process.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
					select {
					case err := <-done:
						stopped = true
						if err != nil {
							t.Fatal(err)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("diagnostic server did not stop")
					}
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("production startup did not expose session handshake")
	return "", nil, nil, nil
}

// Capture exact SQL values, including original JSON BLOB bytes, from every
// durable table so recovery cannot silently change unrelated history/ledgers.
func diagnosticRows(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	result := map[string]string{}
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `" ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var data [][]any
		for rows.Next() {
			values, targets := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			data = append(data, values)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		result[table] = string(raw)
	}
	return result
}
