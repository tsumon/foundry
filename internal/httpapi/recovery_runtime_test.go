package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func TestRuntimeMismatchDiagnosticKeepsHistoryAndDeniesEveryMutation(t *testing.T) {
	f := newFixture(t)
	m, err := f.store.CreateMission(domain.Mission{ID: "retained", Title: "Owned offline history", State: "blocked", Budget: domain.Budget{MaxCalls: 7, UsedCalls: 1, UsageUnknown: true}})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(m)
	srv := f.handler.(*server)
	f.handler = New(f.store, srv.engine, srv.client, f.secrets, Options{Address: "127.0.0.1:4317", CodingWorkbench: true, RecoveryReadOnly: true, RecoveryError: "c4_runtime_mismatch"})
	f.authenticate()
	rr := f.request(http.MethodGet, "/api/health", "", true)
	var health struct {
		ReadOnly     bool            `json:"read_only"`
		C4Only       bool            `json:"c4_controlled_only"`
		Error        string          `json:"recovery_error"`
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err = json.Unmarshal(rr.Body.Bytes(), &health); err != nil || rr.Code != 200 || !health.ReadOnly || health.C4Only || health.Error != "c4_runtime_mismatch" {
		t.Fatal("incorrect Mac diagnostic", rr.Body.String(), err)
	}
	for key, enabled := range health.Capabilities {
		if enabled && key != "model_snapshots" && key != "server_sent_events" && key != "catalog" {
			t.Fatal("mutation capability enabled", key)
		}
	}
	for _, path := range []string{"/api/missions", "/api/missions/retained", "/api/missions/retained/invocations"} {
		if rr := f.request(http.MethodGet, path, "", true); rr.Code != 200 {
			t.Fatal(path, rr.Code)
		}
	}
	for _, path := range []string{"/api/missions/retained/plan", "/api/missions/retained/model-continuations", "/api/model-authorizations/start", "/api/coding-tasks", "/api/future-mutation"} {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			rr := f.request(method, path, `{}`, true)
			if rr.Code != 503 || !strings.Contains(rr.Body.String(), `"code":"recovery_read_only"`) {
				t.Fatal(method, path, rr.Code, rr.Body.String())
			}
		}
	}
	m, err = f.store.GetMission(m.ID)
	after, _ := json.Marshal(m)
	if err != nil || string(before) != string(after) {
		t.Fatal("history or 1/7 budget changed", err)
	}
}
