package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func TestValidateToolResultNormalizesAllowedContentLosslessly(t *testing.T) {
	tool := callTool(t, `{"name":"lookup","inputSchema":{"type":"object"},"outputSchema":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}}`)
	raw := json.RawMessage(`{"unknown":"ignored","isError":false,"structuredContent":{"n":9007199254740993},"content":[{"type":"text","text":"ok","unknown":"ignored"},{"type":"resource_link","uri":"https://example.invalid/item","name":"item","description":"desc","mimeType":"text/plain","unknown":"ignored"}]}`)
	normalized, err := ValidateToolResult(tool, raw)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"content":[{"type":"text","text":"ok"},{"type":"resource_link","uri":"https://example.invalid/item","name":"item","description":"desc","mimeType":"text/plain"}],"structuredContent":{"n":9007199254740993},"isError":false}`
	if string(normalized) != want {
		t.Fatalf("normalized result\n got: %s\nwant: %s", normalized, want)
	}
}

func TestValidateToolResultRetainsToolErrorWithoutSuccessSchema(t *testing.T) {
	tool := callTool(t, `{"name":"lookup","inputSchema":{"type":"object"},"outputSchema":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}}`)
	normalized, err := ValidateToolResult(tool, json.RawMessage(`{"content":[{"type":"text","text":"remote refusal"}],"isError":true}`))
	if err != nil || string(normalized) != `{"content":[{"type":"text","text":"remote refusal"}],"isError":true}` {
		t.Fatalf("normalized=%s err=%v", normalized, err)
	}
}

func TestValidateToolResultRejectsInvalidOrUnsupportedContent(t *testing.T) {
	tool := callTool(t, `{"name":"lookup","inputSchema":{"type":"object"},"outputSchema":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}}`)
	cases := map[string]string{
		"missing content":          `{"structuredContent":{"n":1}}`,
		"missing structured":       `{"content":[]}`,
		"output mismatch":          `{"content":[],"structuredContent":{"n":"one"}}`,
		"duplicate":                `{"content":[],"content":[]}`,
		"image":                    `{"content":[{"type":"image","data":"AA==","mimeType":"image/png"}]}`,
		"audio":                    `{"content":[{"type":"audio","data":"AA==","mimeType":"audio/wav"}]}`,
		"embedded resource":        `{"content":[{"type":"resource","resource":{"uri":"file:///x","text":"x"}}]}`,
		"unknown block":            `{"content":[{"type":"future"}]}`,
		"malformed resource link":  `{"content":[{"type":"resource_link","uri":"https://example.invalid"}]}`,
		"non-object structured":    `{"content":[],"structuredContent":[]}`,
		"non-boolean isError":      `{"content":[],"isError":"true"}`,
		"null isError":             `{"content":[],"structuredContent":{"n":1},"isError":null}`,
		"null text":                `{"content":[{"type":"text","text":null}],"structuredContent":{"n":1}}`,
		"null optional link field": `{"content":[{"type":"resource_link","uri":"https://example.invalid","name":"item","title":null}],"structuredContent":{"n":1}}`,
		"oversized":                `{"content":[{"type":"text","text":"` + strings.Repeat("x", (64<<10)+1) + `"}]}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if normalized, err := ValidateToolResult(tool, json.RawMessage(raw)); err == nil || normalized != nil {
				t.Fatalf("accepted invalid result: %s", normalized)
			}
		})
	}
}

func TestValidateToolResultRejectsUnsupportedNumberBeforeSchemaValidator(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("unsupported result number reached validator panic: %v", recovered)
		}
	}()
	for name, tc := range map[string]struct {
		tool   domain.MCPTool
		result json.RawMessage
	}{
		"success schema": {
			tool:   callTool(t, `{"name":"lookup","inputSchema":{"type":"object"},"outputSchema":{"type":"object","properties":{"n":{"type":"number","minimum":0}},"required":["n"]}}`),
			result: json.RawMessage(`{"content":[],"structuredContent":{"n":1e10000001}}`),
		},
		"tool error without schema": {
			tool:   callTool(t, `{"name":"lookup","inputSchema":{"type":"object"}}`),
			result: json.RawMessage(`{"content":[],"structuredContent":{"n":1e10000001},"isError":true}`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if normalized, err := ValidateToolResult(tc.tool, tc.result); err == nil || normalized != nil {
				t.Fatalf("unsupported result number accepted: %s", normalized)
			}
		})
	}
}
