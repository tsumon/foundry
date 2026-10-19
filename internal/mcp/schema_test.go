package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func argumentTool(schema string) domain.MCPTool {
	return domain.MCPTool{Name: "lookup", InputSchema: json.RawMessage(schema)}
}

func reviewedPolicy(schema string) json.RawMessage {
	return json.RawMessage(`{"policy_version":"mcp_read_v1","reviewed":true,"schema":` + schema + `}`)
}

func TestValidateArgumentsCanonicalizesWithoutLosingLargeIntegers(t *testing.T) {
	tool := argumentTool(`{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}`)
	got, err := ValidateArguments(tool, reviewedPolicy(`{"type":"object","properties":{"n":{"minimum":9007199254740993}},"required":["n"],"additionalProperties":false}`), json.RawMessage(`{ "n" : 9007199254740993 }`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"n":9007199254740993}` {
		t.Fatalf("arguments were not exact canonical JSON: %s", got)
	}
}

func TestValidateArgumentsEnforcesServerAndLocalSchemas(t *testing.T) {
	tool := argumentTool(`{"type":"object","properties":{"kind":{"enum":["issue"]},"count":{"type":"integer","minimum":1,"maximum":3}},"required":["kind","count"],"additionalProperties":false}`)
	policy := reviewedPolicy(`{"type":"object","properties":{"kind":{"const":"issue"},"count":{"maximum":2}},"required":["kind","count"],"additionalProperties":false}`)
	for name, arguments := range map[string]string{
		"required":              `{"kind":"issue"}`,
		"enum":                  `{"kind":"pull","count":1}`,
		"additional properties": `{"kind":"issue","count":1,"secret":true}`,
		"server bound":          `{"kind":"issue","count":4}`,
		"policy bound":          `{"kind":"issue","count":3}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateArguments(tool, policy, json.RawMessage(arguments)); err == nil {
				t.Fatal("invalid arguments accepted")
			}
		})
	}
	if _, err := ValidateArguments(tool, policy, json.RawMessage(`{"kind":"issue","count":2}`)); err != nil {
		t.Fatalf("valid arguments rejected: %v", err)
	}
}

func TestValidateArgumentsEnforcesPolicyPattern(t *testing.T) {
	tool := argumentTool(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
	policy := reviewedPolicy(`{"type":"object","properties":{"path":{"pattern":"^public/"}},"required":["path"],"additionalProperties":false}`)
	if _, err := ValidateArguments(tool, policy, json.RawMessage(`{"path":"private/data"}`)); err == nil {
		t.Fatal("policy pattern did not narrow arguments")
	}
	if _, err := ValidateArguments(tool, policy, json.RawMessage(`{"path":"public/data"}`)); err != nil {
		t.Fatalf("policy pattern rejected matching arguments: %v", err)
	}
}

func TestValidateArgumentsRejectsDuplicateKeysUnicodeRepairAndNonObject(t *testing.T) {
	tool := argumentTool(`{"type":"object"}`)
	policy := reviewedPolicy(`{"type":"object","properties":{"name":{"type":"string"}},"additionalProperties":false}`)
	for name, arguments := range map[string]string{
		"duplicate":      `{"name":"one","name":"two"}`,
		"unicode repair": `{"name":"\ud800"}`,
		"array":          `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateArguments(tool, policy, json.RawMessage(arguments)); err == nil {
				t.Fatal("invalid JSON arguments accepted")
			}
		})
	}
}

func TestValidateArgumentsRejectsInvalidLocalPolicy(t *testing.T) {
	tool := argumentTool(`{"type":"object"}`)
	policies := map[string]string{
		"not reviewed":             `{"policy_version":"mcp_read_v1","reviewed":false,"schema":{"type":"object","properties":{},"additionalProperties":false}}`,
		"wrong version":            `{"policy_version":"other","reviewed":true,"schema":{"type":"object","properties":{},"additionalProperties":false}}`,
		"missing explicit props":   `{"policy_version":"mcp_read_v1","reviewed":true,"schema":{"type":"object","additionalProperties":false}}`,
		"additional props allowed": `{"policy_version":"mcp_read_v1","reviewed":true,"schema":{"type":"object","properties":{},"additionalProperties":true}}`,
		"unknown metadata":         `{"policy_version":"mcp_read_v1","reviewed":true,"schema":{"type":"object","properties":{},"additionalProperties":false},"approval":"model"}`,
	}
	for name, policy := range policies {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateArguments(tool, json.RawMessage(policy), json.RawMessage(`{}`)); err == nil {
				t.Fatal("invalid local policy accepted")
			}
		})
	}
}

func TestValidateArgumentsRejectsUnsupportedSchemaReferencesAndDialects(t *testing.T) {
	policy := reviewedPolicy(`{"type":"object","properties":{},"additionalProperties":false}`)
	for name, schema := range map[string]string{
		"remote ref":        `{"type":"object","$ref":"https://example.com/schema"}`,
		"file ref":          `{"type":"object","$ref":"file:///tmp/schema.json"}`,
		"custom dialect":    `{"$schema":"https://example.com/custom","type":"object"}`,
		"custom vocabulary": `{"$vocabulary":{"https://example.com/custom":true},"type":"object"}`,
		"dynamic ref":       `{"type":"object","$dynamicRef":"#node"}`,
		"recursive ref":     `{"type":"object","$defs":{"node":{"$ref":"#/$defs/node"}},"$ref":"#/$defs/node"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateArguments(argumentTool(schema), policy, json.RawMessage(`{}`)); err == nil {
				t.Fatal("unsupported execution schema accepted")
			}
		})
	}
}

func TestValidateArgumentsAllowsBoundedAcyclicLocalReference(t *testing.T) {
	tool := argumentTool(`{"type":"object","properties":{"kind":{"$ref":"#/$defs/kind"}},"required":["kind"],"additionalProperties":false,"$defs":{"kind":{"enum":["issue"]}}}`)
	policy := reviewedPolicy(`{"type":"object","properties":{"kind":{"const":"issue"}},"required":["kind"],"additionalProperties":false}`)
	if _, err := ValidateArguments(tool, policy, json.RawMessage(`{"kind":"issue"}`)); err != nil {
		t.Fatalf("bounded local reference rejected: %v", err)
	}
}

func TestValidateArgumentsRejectsSchemaComplexityAndBounds(t *testing.T) {
	policy := reviewedPolicy(`{"type":"object","properties":{},"additionalProperties":false}`)
	branches := strings.Repeat(`{"type":"object"},`, 300) + `{"type":"object"}`
	references := strings.Repeat(`{"$ref":"#/$defs/object"},`, 257) + `{"type":"object"}`
	nodes := strings.Repeat(`0,`, 2050) + `0`
	for name, schema := range map[string]string{
		"bytes":       `{"type":"object","description":"` + strings.Repeat("x", 65536) + `"}`,
		"depth":       `{"type":"object","allOf":` + strings.Repeat(`[`, 34) + `{}` + strings.Repeat(`]`, 34) + `}`,
		"composition": `{"type":"object","anyOf":[` + branches + `]}`,
		"references":  `{"type":"object","allOf":[` + references + `],"$defs":{"object":{"type":"object"}}}`,
		"nodes":       `{"type":"object","properties":{"n":{"enum":[` + nodes + `]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateArguments(argumentTool(schema), policy, json.RawMessage(`{}`)); err == nil {
				t.Fatal("schema resource bound was not enforced")
			}
		})
	}
}

func schemaWithVariableComposition(keyword string, count int) string {
	var values strings.Builder
	if keyword == "dependentSchemas" {
		values.WriteByte('{')
		for i := 0; i < count; i++ {
			if i > 0 {
				values.WriteByte(',')
			}
			fmt.Fprintf(&values, `"p%d":{}`, i)
		}
		values.WriteByte('}')
		return `{"type":"object","dependentSchemas":` + values.String() + `}`
	}
	values.WriteByte('[')
	for i := 0; i < count; i++ {
		if i > 0 {
			values.WriteByte(',')
		}
		values.WriteString(`{}`)
	}
	values.WriteByte(']')
	return `{"type":"object","properties":{"items":{"type":"array","prefixItems":` + values.String() + `}}}`
}

func TestValidateArgumentsCountsVariableWidthCompositionWork(t *testing.T) {
	policy := reviewedPolicy(`{"type":"object","properties":{},"additionalProperties":false}`)
	for _, keyword := range []string{"dependentSchemas", "prefixItems"} {
		t.Run(keyword+" boundary", func(t *testing.T) {
			tool := argumentTool(schemaWithVariableComposition(keyword, maxSchemaWork))
			if _, err := ValidateArguments(tool, policy, json.RawMessage(`{}`)); err != nil {
				t.Fatalf("%d contained schemas should fit work budget: %v", maxSchemaWork, err)
			}
		})
		t.Run(keyword+" overflow", func(t *testing.T) {
			tool := argumentTool(schemaWithVariableComposition(keyword, maxSchemaWork+1))
			if _, err := ValidateArguments(tool, policy, json.RawMessage(`{}`)); err == nil {
				t.Fatalf("%d contained schemas bypassed work budget", maxSchemaWork+1)
			}
		})
	}
}

func TestValidateArgumentsRejectsMalformedVariableWidthComposition(t *testing.T) {
	policy := reviewedPolicy(`{"type":"object","properties":{},"additionalProperties":false}`)
	for name, schema := range map[string]string{
		"dependentSchemas array": `{"type":"object","dependentSchemas":[]}`,
		"prefixItems object":     `{"type":"object","prefixItems":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateArguments(argumentTool(schema), policy, json.RawMessage(`{}`)); err == nil {
				t.Fatal("malformed composition shape accepted")
			}
		})
	}
}

func TestValidateArgumentsRejectsOversizedArguments(t *testing.T) {
	tool := argumentTool(`{"type":"object"}`)
	policy := reviewedPolicy(`{"type":"object","properties":{"value":{"type":"string"}},"additionalProperties":false}`)
	arguments := json.RawMessage(`{"value":"` + strings.Repeat("x", maxArgumentBytes) + `"}`)
	if _, err := ValidateArguments(tool, policy, arguments); err == nil {
		t.Fatal("argument byte budget was not enforced")
	}
}
