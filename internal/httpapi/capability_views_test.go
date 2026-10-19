package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"foundry/internal/domain"
)

func TestCapabilityRoutesProjectLosslessJSONFromLiveAndPersistedSnapshots(t *testing.T) {
	const input = `{"type":"object","minimum":9007199254740993,"maximum":1e400}`
	const output = `{"type":"object","maximum":1e400,"const":9007199254740993}`
	const annotations = `{"value":9007199254740993,"large":1e400,"text":"<script>inert</script>"}`
	wantText := map[string]string{
		"input_schema": input, "output_schema": output,
		"annotations": `{"value":9007199254740993,"large":1e400,"text":"\u003cscript\u003einert\u003c/script\u003e"}`,
	}
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("persisted=%t", legacy), func(t *testing.T) {
			f := newFixture(t)
			f.authenticate()
			var hits atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				var q struct {
					ID     int
					Method string
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch q.Method {
				case "initialize":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`, q.ID)
				case "notifications/initialized":
					w.WriteHeader(202)
				case "tools/list":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"precise","inputSchema":%s,"outputSchema":%s,"annotations":%s},{"name":"optional-absent","inputSchema":{"type":"object"}}]}}`, q.ID, input, output, annotations)
				default:
					t.Errorf("unexpected method: %s", q.Method)
				}
			}))
			defer remote.Close()
			c, err := f.store.SaveMCPConnection(domain.MCPConnection{ID: "precise", Name: "fixture", Endpoint: remote.URL, Protocol: domain.MCPProtocol, Enabled: true}, 0)
			if err != nil {
				t.Fatal(err)
			}
			var baseline []byte
			if legacy {
				intent := domain.DiscoveryOperation{ID: "precise-request", ConnectionID: c.ID, ConnectionRevision: c.Revision, Endpoint: c.Endpoint, Consent: true, AuthorizedAt: "2026-10-18T00:00:00Z", Budget: domain.FixedDiscoveryBudget()}
				if _, _, err := f.store.BeginDiscovery(intent); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.ClaimDiscovery(intent.ID); err != nil {
					t.Fatal(err)
				}
				// Old snapshots have only raw domain fields, never HTTP display fields.
				snapshot := &domain.MCPToolSnapshot{ID: "legacy", ConnectionID: c.ID, ConnectionRevision: c.Revision, Protocol: c.Protocol, ServerName: "fixture", ServerVersion: "1", Digest: strings.Repeat("a", 64), ObservedAt: intent.AuthorizedAt, Tools: []domain.MCPTool{
					{Name: "precise", InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output), Annotations: json.RawMessage(annotations), DefinitionDigest: strings.Repeat("b", 64)},
					{Name: "optional-absent", InputSchema: json.RawMessage(`{"type":"object"}`), DefinitionDigest: strings.Repeat("c", 64)},
				}}
				if _, err := f.store.FinishDiscovery(intent.ID, "succeeded", snapshot, 3, 100, ""); err != nil {
					t.Fatal(err)
				}
				baseline, err = json.Marshal([]domain.MCPToolSnapshot{*snapshot})
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, route := range []struct{ method, path, body string }{
				{"POST", "/api/capabilities/mcp/precise/discover", `{"expected_revision":1,"request_id":"precise-request","consent":true}`},
				{"GET", "/api/capabilities", ""},
				{"GET", "/api/capabilities/discoveries/precise-request", ""},
			} {
				r := f.request(route.method, route.path, route.body, true)
				if r.Code != 200 {
					t.Fatalf("%s: %d %s", route.path, r.Code, r.Body.String())
				}
				var body struct {
					Snapshot  json.RawMessage   `json:"snapshot"`
					Snapshots []json.RawMessage `json:"snapshots"`
				}
				if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if route.path == "/api/capabilities" {
					if len(body.Snapshots) != 1 {
						t.Fatalf("missing snapshot: %s", r.Body.String())
					}
					body.Snapshot = body.Snapshots[0]
				}
				var view struct {
					Tools []map[string]json.RawMessage `json:"tools"`
				}
				if err := json.Unmarshal(body.Snapshot, &view); err != nil {
					t.Fatalf("missing snapshot: %s (%v)", r.Body.String(), err)
				}
				if len(view.Tools) != 2 {
					t.Fatalf("missing tools: %s", body.Snapshot)
				}
				stored, err := f.store.Capabilities()
				if err != nil {
					t.Fatal(err)
				}
				current, err := json.Marshal(stored.Snapshots)
				if err != nil {
					t.Fatal(err)
				}
				if baseline == nil {
					baseline = current
				} else if string(current) != string(baseline) {
					t.Fatal("HTTP projection changed persisted snapshot/digests")
				}
				tool := stored.Snapshots[0].Tools[0]
				for field, raw := range map[string]json.RawMessage{"input_schema": tool.InputSchema, "output_schema": tool.OutputSchema, "annotations": tool.Annotations} {
					var text string
					if err := json.Unmarshal(view.Tools[0][field+"_json"], &text); err != nil || text != string(raw) {
						t.Errorf("%s %s: lossless text got %q want %s (%v)", route.path, field, text, raw, err)
					}
					if text != wantText[field] {
						t.Errorf("%s %s: numeric lexemes changed: %s", route.path, field, text)
					}
					if string(view.Tools[0][field]) != string(raw) {
						t.Errorf("compatibility raw field changed: %s", field)
					}
				}
				for _, field := range []string{"output_schema_json", "annotations_json"} {
					if value, present := view.Tools[1][field]; present && string(value) != `""` && string(value) != "null" {
						t.Errorf("fabricated absent raw text: %s", value)
					}
				}
			}
			wantHits := int32(3)
			if legacy {
				wantHits = 0
			}
			if hits.Load() != wantHits {
				t.Fatalf("unexpected dispatch: %d", hits.Load())
			}
		})
	}
}
