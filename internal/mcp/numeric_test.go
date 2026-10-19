package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestValidateExecutionNumbersRejectsUnsupportedLexemes(t *testing.T) {
	for name, literal := range map[string]string{
		"positive exponent": "1e1025",
		"negative exponent": "1e-1025",
		"long mantissa":     strings.Repeat("9", 257),
		"leading zero":      "01",
		"missing exponent":  "1e",
		"not JSON":          "NaN",
	} {
		t.Run(name, func(t *testing.T) {
			value := map[string]any{"nested": []any{json.Number(literal)}}
			if validateExecutionNumbers(value) == nil {
				t.Fatalf("unsupported execution number accepted: %s", literal)
			}
		})
	}
}

func TestValidateExecutionNumbersAcceptsBoundedExactLexemes(t *testing.T) {
	for name, literal := range map[string]string{
		"large integer":     strings.Repeat("9", 256),
		"negative zero":     "-0",
		"negative fraction": "-0.125",
		"positive boundary": "1e+1024",
		"negative boundary": "1e-1024",
	} {
		t.Run(name, func(t *testing.T) {
			value := map[string]any{"nested": []any{json.Number(literal)}}
			if err := validateExecutionNumbers(value); err != nil {
				t.Fatalf("bounded execution number rejected: %s: %v", literal, err)
			}
		})
	}
}

func TestValidateArgumentsChecksSchemaNumbersBeforeCompiler(t *testing.T) {
	policy := reviewedPolicy(`{"type":"object","properties":{},"additionalProperties":false}`)
	for name, literal := range map[string]string{
		"exponent":     "1e1025",
		"long literal": strings.Repeat("9", 257),
	} {
		t.Run(name, func(t *testing.T) {
			tool := argumentTool(`{"type":"object","minimum":` + literal + `}`)
			if _, err := ValidateArguments(tool, policy, json.RawMessage(`{}`)); err == nil {
				t.Fatal("unsupported schema number accepted for execution")
			}
		})
	}
}

func TestValidateArgumentsAcceptsExecutionNumberBoundaries(t *testing.T) {
	tool := argumentTool(`{"type":"object","properties":{"n":{"type":"number","minimum":-1e1024,"maximum":1e1024}},"required":["n"]}`)
	policy := reviewedPolicy(`{"type":"object","properties":{"n":{"type":"number"}},"required":["n"],"additionalProperties":false}`)
	for _, literal := range []string{"1e1024", "-1e1024", "-0.0", "9007199254740993", "0.125"} {
		t.Run(literal, func(t *testing.T) {
			if _, err := ValidateArguments(tool, policy, json.RawMessage(`{"n":`+literal+`}`)); err != nil {
				t.Fatalf("bounded exact number rejected: %v", err)
			}
		})
	}
}

func TestValidateArgumentsRejectsUnsupportedExponentBeforeValidator(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("unsupported exponent reached validator panic: %v", recovered)
		}
	}()
	tool := argumentTool(`{"type":"object","properties":{"n":{"type":"number","minimum":0}},"required":["n"]}`)
	policy := reviewedPolicy(`{"type":"object","properties":{"n":{"type":"number"}},"required":["n"],"additionalProperties":false}`)
	if _, err := ValidateArguments(tool, policy, json.RawMessage(`{"n":1e10000001}`)); err == nil {
		t.Fatal("unsupported exponent reached validator")
	}
}

func binaryReferenceDAG(levels int) string {
	var definitions strings.Builder
	for level := 0; level <= levels; level++ {
		if level > 0 {
			definitions.WriteByte(',')
		}
		if level == levels {
			fmt.Fprintf(&definitions, `"level%d":{"type":"object"}`, level)
			continue
		}
		fmt.Fprintf(&definitions, `"level%d":{"allOf":[{"$ref":"#/$defs/level%d"},{"$ref":"#/$defs/level%d"}]}`, level, level+1, level+1)
	}
	return `{"type":"object","$ref":"#/$defs/level0","$defs":{` + definitions.String() + `}}`
}

func TestValidateArgumentsRejectsExpandedReferenceWork(t *testing.T) {
	policy := reviewedPolicy(`{"type":"object","properties":{},"additionalProperties":false}`)
	if _, err := ValidateArguments(argumentTool(binaryReferenceDAG(9)), policy, json.RawMessage(`{}`)); err == nil {
		t.Fatal("exponentially expanded reference work bypassed execution bound")
	}
}

func TestValidateArgumentsRejectsRepeatedReferenceEntryEdges(t *testing.T) {
	rootReferences := strings.TrimSuffix(strings.Repeat(`{"$ref":"#/$defs/level0"},`, 4), ",")
	schema := strings.Replace(binaryReferenceDAG(5), `"$ref":"#/$defs/level0"`, `"allOf":[`+rootReferences+`]`, 1)
	policy := reviewedPolicy(`{"type":"object","properties":{},"additionalProperties":false}`)
	if _, err := ValidateArguments(argumentTool(schema), policy, json.RawMessage(`{}`)); err == nil {
		t.Fatal("repeated entry edges to shared reference target bypassed execution bound")
	}
}

func TestValidateArgumentsRejectsRepeatedSharedCompositionWork(t *testing.T) {
	rootReferences := strings.TrimSuffix(strings.Repeat(`{"$ref":"#/$defs/shared"},`, 3), ",")
	branches := strings.TrimSuffix(strings.Repeat(`{"type":"object"},`, 120), ",")
	schema := `{"type":"object","allOf":[` + rootReferences + `],"$defs":{"shared":{"type":"object","allOf":[` + branches + `]}}}`
	policy := reviewedPolicy(`{"type":"object","properties":{},"additionalProperties":false}`)
	if _, err := ValidateArguments(argumentTool(schema), policy, json.RawMessage(`{}`)); err == nil {
		t.Fatal("repeated composition work through a shared target bypassed execution bound")
	}
}

func TestValidateArgumentsAcceptsBoundedReferenceWork(t *testing.T) {
	policy := reviewedPolicy(`{"type":"object","properties":{},"additionalProperties":false}`)
	if _, err := ValidateArguments(argumentTool(binaryReferenceDAG(5)), policy, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("bounded acyclic reference work rejected: %v", err)
	}
}
