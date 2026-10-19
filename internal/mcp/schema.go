package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"foundry/internal/domain"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	maxArgumentBytes  = 8 << 10
	maxSchemaBytes    = 64 << 10
	maxSchemaNodes    = 2048
	maxSchemaWork     = 256
	policyVersion     = "mcp_read_v1"
	draft2020SchemaID = "https://json-schema.org/draft/2020-12/schema"
)

var errInvalidArguments = errors.New("invalid MCP arguments or policy")

type rejectingLoader struct{}

func (rejectingLoader) Load(string) (any, error) {
	return nil, errors.New("external schema resources are disabled")
}

type schemaInspection struct {
	nodes      int
	work       int
	maps       map[string]map[string]any
	references map[string][]string
}

func inspectExecutionSchema(root map[string]any) error {
	inspection := schemaInspection{maps: map[string]map[string]any{}, references: map[string][]string{}}
	var walk func(any, string, int) error
	walk = func(value any, path string, depth int) error {
		inspection.nodes++
		if inspection.nodes > maxSchemaNodes || depth > 32 {
			return errInvalidArguments
		}
		switch node := value.(type) {
		case map[string]any:
			inspection.maps[path] = node
			for key, child := range node {
				switch key {
				case "$schema":
					if id, ok := child.(string); !ok || id != draft2020SchemaID {
						return errInvalidArguments
					}
				case "$vocabulary", "$dynamicRef", "$dynamicAnchor", "$recursiveRef", "$recursiveAnchor", "$anchor", "$id":
					return errInvalidArguments
				case "$ref":
					ref, ok := child.(string)
					if !ok || (ref != "#" && !strings.HasPrefix(ref, "#/")) || strings.Contains(ref, "%") {
						return errInvalidArguments
					}
					inspection.work++
					inspection.references[path] = append(inspection.references[path], strings.TrimPrefix(ref, "#"))
				case "allOf", "anyOf", "oneOf":
					items, ok := child.([]any)
					if !ok {
						return errInvalidArguments
					}
					inspection.work += len(items)
				case "dependentSchemas":
					entries, ok := child.(map[string]any)
					if !ok {
						return errInvalidArguments
					}
					inspection.work += len(entries)
				case "prefixItems":
					items, ok := child.([]any)
					if !ok {
						return errInvalidArguments
					}
					inspection.work += len(items)
				case "not", "if", "then", "else", "contains":
					inspection.work++
				}
				if inspection.work > maxSchemaWork {
					return errInvalidArguments
				}
				if err := walk(child, path+"/"+escapeJSONPointer(key), depth+1); err != nil {
					return err
				}
			}
		case []any:
			for index, child := range node {
				if err := walk(child, fmt.Sprintf("%s/%d", path, index), depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, "", 0); err != nil {
		return err
	}
	for _, refs := range inspection.references {
		for _, ref := range refs {
			if _, ok := inspection.maps[ref]; !ok || invalidPointer(ref) {
				return errInvalidArguments
			}
		}
	}
	// A schema node depends on every reference in its subtree. Detect cycles in
	// that finite graph before handing it to the validator.
	graph := make(map[string][]string, len(inspection.maps))
	for parent := range inspection.maps {
		prefix := parent + "/"
		for source, refs := range inspection.references {
			if source == parent || parent == "" || strings.HasPrefix(source, prefix) {
				graph[parent] = append(graph[parent], refs...)
			}
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(node string) bool {
		if visiting[node] {
			return false
		}
		if visited[node] {
			return true
		}
		visiting[node] = true
		for _, next := range graph[node] {
			if !visit(next) {
				return false
			}
		}
		visiting[node] = false
		visited[node] = true
		return true
	}
	for path := range inspection.maps {
		if !visit(path) {
			return errInvalidArguments
		}
	}
	addWork := func(total, amount int) int {
		if amount > maxSchemaWork-total {
			return maxSchemaWork + 1
		}
		return total + amount
	}
	memo := map[string]int{}
	var schemaWork func(any) int
	var targetWork func(string) int
	targetWork = func(target string) int {
		if cached, ok := memo[target]; ok {
			return cached
		}
		cost := schemaWork(inspection.maps[target])
		memo[target] = cost
		return cost
	}
	schemaWork = func(value any) int {
		schema, ok := value.(map[string]any)
		if !ok {
			return 0
		}
		total := 0
		for keyword, child := range schema {
			switch keyword {
			case "$ref":
				total = addWork(total, 1)
				if ref, ok := child.(string); ok {
					total = addWork(total, targetWork(strings.TrimPrefix(ref, "#")))
				}
			case "allOf", "anyOf", "oneOf", "prefixItems":
				if items, ok := child.([]any); ok {
					total = addWork(total, len(items))
					for _, item := range items {
						total = addWork(total, schemaWork(item))
					}
				}
			case "dependentSchemas":
				if entries, ok := child.(map[string]any); ok {
					total = addWork(total, len(entries))
					for _, entry := range entries {
						total = addWork(total, schemaWork(entry))
					}
				}
			case "properties", "patternProperties":
				if entries, ok := child.(map[string]any); ok {
					for _, entry := range entries {
						total = addWork(total, schemaWork(entry))
					}
				}
			case "dependencies":
				if entries, ok := child.(map[string]any); ok {
					for _, entry := range entries {
						total = addWork(total, schemaWork(entry))
					}
				}
			case "not", "if", "then", "else", "contains":
				total = addWork(total, 1)
				total = addWork(total, schemaWork(child))
			case "additionalProperties", "unevaluatedProperties", "propertyNames", "items", "unevaluatedItems", "contentSchema":
				total = addWork(total, schemaWork(child))
			}
			if total > maxSchemaWork {
				return total
			}
		}
		return total
	}
	var expansionWork func(any) int
	expansionWork = func(value any) int {
		schema, ok := value.(map[string]any)
		if !ok {
			return 0
		}
		total := 0
		for keyword, child := range schema {
			switch keyword {
			case "$ref":
				if ref, ok := child.(string); ok {
					total = addWork(total, targetWork(strings.TrimPrefix(ref, "#")))
				}
			case "allOf", "anyOf", "oneOf", "prefixItems":
				if items, ok := child.([]any); ok {
					for _, item := range items {
						total = addWork(total, expansionWork(item))
					}
				}
			case "properties", "patternProperties", "dependentSchemas", "dependencies":
				if entries, ok := child.(map[string]any); ok {
					for _, entry := range entries {
						total = addWork(total, expansionWork(entry))
					}
				}
			case "not", "if", "then", "else", "contains", "additionalProperties", "unevaluatedProperties", "propertyNames", "items", "unevaluatedItems", "contentSchema":
				total = addWork(total, expansionWork(child))
			}
			if total > maxSchemaWork {
				return total
			}
		}
		return total
	}
	if expansion := expansionWork(root); expansion > maxSchemaWork-inspection.work {
		return errInvalidArguments
	}
	return nil
}

func escapeJSONPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func invalidPointer(pointer string) bool {
	if pointer == "" {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		for i := 0; i < len(part); i++ {
			if part[i] == '~' && (i+1 >= len(part) || (part[i+1] != '0' && part[i+1] != '1')) {
				return true
			}
			if part[i] == '~' {
				i++
			}
		}
	}
	return false
}

func compileExecutionSchema(raw json.RawMessage, localPolicy bool) (*jsonschema.Schema, error) {
	_, value, err := canonicalJSON(raw, maxSchemaBytes)
	if err != nil || validateExecutionNumbers(value) != nil {
		return nil, errInvalidArguments
	}
	root, ok := value.(map[string]any)
	if !ok || root["type"] != "object" {
		return nil, errInvalidArguments
	}
	if localPolicy {
		if _, ok := root["properties"].(map[string]any); !ok || root["additionalProperties"] != false {
			return nil, errInvalidArguments
		}
		if _, extendsProperties := root["patternProperties"]; extendsProperties {
			return nil, errInvalidArguments
		}
	}
	if err := inspectExecutionSchema(root); err != nil {
		return nil, errInvalidArguments
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(rejectingLoader{})
	const resource = "https://foundry.invalid/mcp-schema.json"
	if err := compiler.AddResource(resource, root); err != nil {
		return nil, errInvalidArguments
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		return nil, errInvalidArguments
	}
	return schema, nil
}

func decodePolicy(raw json.RawMessage) (json.RawMessage, error) {
	canonical, _, err := canonicalJSON(raw, maxSchemaBytes+1024)
	if err != nil || !object(canonical) {
		return nil, errInvalidArguments
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(canonical, &fields) != nil || len(fields) != 3 {
		return nil, errInvalidArguments
	}
	var version string
	var reviewed bool
	if json.Unmarshal(fields["policy_version"], &version) != nil || version != policyVersion || json.Unmarshal(fields["reviewed"], &reviewed) != nil || !reviewed || len(fields["schema"]) == 0 || len(fields["schema"]) > maxSchemaBytes {
		return nil, errInvalidArguments
	}
	return fields["schema"], nil
}

// ValidateArguments validates exact JSON arguments against both the server
// input schema and the separately reviewed controller policy, then returns the
// canonical object bytes used for immutable proposal identity.
func ValidateArguments(tool domain.MCPTool, policy, arguments json.RawMessage) (json.RawMessage, error) {
	canonical, value, err := canonicalJSON(arguments, maxArgumentBytes)
	if err != nil || validateExecutionNumbers(value) != nil {
		return nil, errInvalidArguments
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errInvalidArguments
	}
	serverSchema, err := compileExecutionSchema(tool.InputSchema, false)
	if err != nil {
		return nil, errInvalidArguments
	}
	policyRaw, err := decodePolicy(policy)
	if err != nil {
		return nil, errInvalidArguments
	}
	localSchema, err := compileExecutionSchema(policyRaw, true)
	if err != nil {
		return nil, errInvalidArguments
	}
	if err := serverSchema.Validate(value); err != nil {
		return nil, errInvalidArguments
	}
	if err := localSchema.Validate(value); err != nil {
		return nil, errInvalidArguments
	}
	// Keep an owned copy; callers may reuse or mutate their input buffer.
	return bytes.Clone(canonical), nil
}
