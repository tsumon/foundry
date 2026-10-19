package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"foundry/internal/domain"
)

func executionSnapshotFixture(t *testing.T) domain.MCPToolSnapshot {
	t.Helper()
	raw := json.RawMessage(`{"annotations":{"readOnlyHint":true},"description":"reads","inputSchema":{"additionalProperties":false,"properties":{"n":{"type":"integer"}},"type":"object"},"name":"lookup","outputSchema":{"type":"object"},"title":"Lookup","x-extension":{"precise":9007199254740993}}`)
	h := sha256.Sum256(raw)
	tool := domain.MCPTool{
		Name:             "lookup",
		Title:            "Lookup",
		Description:      "reads",
		InputSchema:      json.RawMessage(`{"additionalProperties":false,"properties":{"n":{"type":"integer"}},"type":"object"}`),
		OutputSchema:     json.RawMessage(`{"type":"object"}`),
		Annotations:      json.RawMessage(`{"readOnlyHint":true}`),
		RawDefinition:    raw,
		DefinitionDigest: hex.EncodeToString(h[:]),
	}
	tools, err := json.Marshal([]domain.MCPTool{tool})
	if err != nil {
		t.Fatal(err)
	}
	snapshotHash := sha256.Sum256(tools)
	return domain.MCPToolSnapshot{
		ID:                 "snapshot",
		ConnectionID:       "connection",
		ConnectionRevision: 1,
		Protocol:           domain.MCPProtocol,
		ServerName:         "fixture",
		ServerVersion:      "1",
		Tools:              []domain.MCPTool{tool},
		Digest:             hex.EncodeToString(snapshotHash[:]),
		ObservedAt:         "2026-10-18T00:00:00Z",
		ExecutionVersion:   1,
	}
}

func TestLegacyCatalogJSONBytesDoNotGainExecutionFields(t *testing.T) {
	legacy := domain.MCPToolSnapshot{ID: "legacy", Tools: []domain.MCPTool{{Name: "lookup"}}}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("raw_definition")) || bytes.Contains(raw, []byte("execution_version")) {
		t.Fatalf("legacy catalog bytes changed: %s", raw)
	}
}

func TestToolDefinitionCanonicalRoundTripPreservesExtensionsAndNumbers(t *testing.T) {
	tool, err := parseTool(json.RawMessage(`{"x-extension":{"precise":9007199254740993},"name":"lookup","title":"Lookup","description":"reads","inputSchema":{"type":"object","properties":{"n":{"type":"integer"}},"additionalProperties":false},"outputSchema":{"type":"object"},"annotations":{"readOnlyHint":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"annotations":{"readOnlyHint":true},"description":"reads","inputSchema":{"additionalProperties":false,"properties":{"n":{"type":"integer"}},"type":"object"},"name":"lookup","outputSchema":{"type":"object"},"title":"Lookup","x-extension":{"precise":9007199254740993}}`
	if string(tool.RawDefinition) != want {
		t.Fatalf("full definition lost or changed: %s", tool.RawDefinition)
	}
	if err := VerifyExecutionSnapshot(executionSnapshotFixture(t)); err != nil {
		t.Fatalf("valid execution snapshot rejected: %v", err)
	}
}

func TestExecutionSnapshotRejectsLegacyCatalog(t *testing.T) {
	legacy := domain.MCPToolSnapshot{Tools: []domain.MCPTool{}}
	if VerifyExecutionSnapshot(legacy) == nil {
		t.Fatal("catalog became authority")
	}
}

func TestExecutionSnapshotRejectsDefinitionProjectionAndDigestTamper(t *testing.T) {
	cases := map[string]func(*domain.MCPToolSnapshot){
		"definition": func(s *domain.MCPToolSnapshot) {
			s.Tools[0].RawDefinition = json.RawMessage(`{"name":"different","inputSchema":{"type":"object"}}`)
		},
		"name projection": func(s *domain.MCPToolSnapshot) { s.Tools[0].Name = "different" },
		"input projection": func(s *domain.MCPToolSnapshot) {
			s.Tools[0].InputSchema = json.RawMessage(`{"type":"object"}`)
		},
		"annotations projection": func(s *domain.MCPToolSnapshot) {
			s.Tools[0].Annotations = json.RawMessage(`{"readOnlyHint":false}`)
		},
		"definition digest": func(s *domain.MCPToolSnapshot) { s.Tools[0].DefinitionDigest = "00" },
		"snapshot digest":   func(s *domain.MCPToolSnapshot) { s.Digest = "00" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot := executionSnapshotFixture(t)
			mutate(&snapshot)
			if VerifyExecutionSnapshot(snapshot) == nil {
				t.Fatal("tampered execution snapshot accepted")
			}
		})
	}
}

func TestToolDefinitionRejectsDuplicateKeysAndUnicodeRepair(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"duplicate":      json.RawMessage(`{"name":"one","name":"two","inputSchema":{"type":"object"}}`),
		"lone surrogate": json.RawMessage(`{"name":"\ud800","inputSchema":{"type":"object"}}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTool(raw); err == nil {
				t.Fatal("lossy definition accepted")
			}
		})
	}
}

func TestToolDefinitionAcceptsValidUnicodeSurrogatePair(t *testing.T) {
	tool, err := parseTool(json.RawMessage(`{"name":"emoji-\ud83d\ude00","inputSchema":{"type":"object"}}`))
	if err != nil || tool.Name != "emoji-😀" {
		t.Fatalf("valid surrogate pair rejected: %q %v", tool.Name, err)
	}
}

func TestToolDefinitionRejectsExplicitNullOptionalFields(t *testing.T) {
	for _, field := range []string{"title", "description", "outputSchema", "annotations"} {
		t.Run(field, func(t *testing.T) {
			raw := json.RawMessage(`{"name":"lookup","inputSchema":{"type":"object"},"` + field + `":null}`)
			if _, err := parseTool(raw); err == nil {
				t.Fatalf("explicit null %s accepted", field)
			}
		})
	}
}

func TestToolDefinitionAcceptsAbsentOptionalFields(t *testing.T) {
	if _, err := parseTool(json.RawMessage(`{"name":"lookup","inputSchema":{"type":"object"}}`)); err != nil {
		t.Fatalf("absent optional fields rejected: %v", err)
	}
}

func TestUnsupportedExecutionSchemaRemainsVisibleInDiscovery(t *testing.T) {
	tool, err := parseTool(json.RawMessage(`{"name":"visible","inputSchema":{"type":"object","$ref":"https://example.com/schema"}}`))
	if err != nil || tool.Name != "visible" || len(tool.RawDefinition) == 0 {
		t.Fatalf("unsupported execution schema disappeared from catalog: %+v %v", tool, err)
	}
}
