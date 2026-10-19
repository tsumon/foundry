package httpapi

import (
	"encoding/json"
	"foundry/internal/engine"
	"net/http"
	"testing"
)

func TestMCPInternalOptionDoesNotExposeHTTPAuthority(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	e := engine.New(f.store, nil, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}, EnableLocalMCP: true})
	defer e.Close()
	f.handler.(*server).engine = e
	for _, path := range []string{"/api/mcp/call", "/mcp/call", "/api/missions/fixture/mcp/approve", "/api/missions/fixture/mcp-proposals/proposal/decide", "/api/missions/fixture/mcp/call"} {
		rr := f.request(http.MethodPost, path, `{"revision":1,"approve":true}`, true)
		if rr.Code != http.StatusNotFound {
			t.Errorf("route exposed %s: %d", path, rr.Code)
		}
	}
	rr := f.request(http.MethodGet, "/api/health", "", true)
	var health struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mcp_tool_call", "tool_execution", "skill_execution", "git_write", "hook_scripts"} {
		value, ok := health.Capabilities[key]
		if !ok || value {
			t.Errorf("capability %s enabled/missing", key)
		}
	}
	rr = f.request(http.MethodPost, "/api/missions", `{"title":"R4","goal":"deny","repo_path":"/tmp","workspace_mode":"generations_controlled_mcp_read_v1","tool_mode":"controlled_mcp_read_v1","spec":{}}`, true)
	if rr.Code < 400 {
		t.Fatalf("R4 public creation: %d %s", rr.Code, rr.Body.String())
	}
	missions, err := f.store.ListMissions()
	if err != nil || len(missions) != 0 {
		t.Fatal("denied creation persisted a mission", missions, err)
	}
}
