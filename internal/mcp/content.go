package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"foundry/internal/domain"
)

const maxToolResultBytes = 64 << 10

var errInvalidToolResult = errors.New("invalid MCP tool result")

type normalizedToolResult struct {
	Content           []json.RawMessage `json:"content"`
	StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
	IsError           bool              `json:"isError"`
}

type normalizedTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type normalizedResourceLink struct {
	Type        string `json:"type"`
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ValidateToolResult returns a bounded canonical whitelist of MCP result data.
// Resource links remain inert strings; this package never follows them.
func ValidateToolResult(tool domain.MCPTool, result json.RawMessage) (json.RawMessage, error) {
	if len(result) == 0 || len(result) > maxToolResultBytes || validateJSON(result) != nil || !object(result) {
		return nil, errInvalidToolResult
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(result, &fields) != nil {
		return nil, errInvalidToolResult
	}
	contentRaw, ok := fields["content"]
	if !ok || bytes.Equal(bytes.TrimSpace(contentRaw), []byte("null")) {
		return nil, errInvalidToolResult
	}
	var content []json.RawMessage
	if json.Unmarshal(contentRaw, &content) != nil || content == nil {
		return nil, errInvalidToolResult
	}
	normalized := normalizedToolResult{Content: make([]json.RawMessage, 0, len(content))}
	if raw, exists := fields["isError"]; exists {
		trimmed := bytes.TrimSpace(raw)
		if (!bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false"))) || json.Unmarshal(trimmed, &normalized.IsError) != nil {
			return nil, errInvalidToolResult
		}
	}
	for _, block := range content {
		canonical, err := normalizeContentBlock(block)
		if err != nil {
			return nil, errInvalidToolResult
		}
		normalized.Content = append(normalized.Content, canonical)
	}
	var structuredValue any
	if raw, exists := fields["structuredContent"]; exists {
		canonical, value, err := canonicalJSON(raw, maxToolResultBytes)
		if err != nil || !object(canonical) || validateExecutionNumbers(value) != nil {
			return nil, errInvalidToolResult
		}
		normalized.StructuredContent = canonical
		structuredValue = value
	}
	if !normalized.IsError && len(bytes.TrimSpace(tool.OutputSchema)) > 0 && !bytes.Equal(bytes.TrimSpace(tool.OutputSchema), []byte("null")) {
		if structuredValue == nil {
			return nil, errInvalidToolResult
		}
		schema, err := compileExecutionSchema(tool.OutputSchema, false)
		if err != nil || schema.Validate(structuredValue) != nil {
			return nil, errInvalidToolResult
		}
	}
	canonical, err := json.Marshal(normalized)
	if err != nil || len(canonical) > maxToolResultBytes {
		return nil, errInvalidToolResult
	}
	return canonical, nil
}

func normalizeContentBlock(raw json.RawMessage) (json.RawMessage, error) {
	if !object(raw) || validateJSON(raw) != nil {
		return nil, errInvalidToolResult
	}
	var fields map[string]json.RawMessage
	var kind string
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(fields["type"], &kind) != nil {
		return nil, errInvalidToolResult
	}
	switch kind {
	case "text":
		var text string
		if !decodeJSONString(fields["text"], &text) || !validText(text, maxToolResultBytes, false) {
			return nil, errInvalidToolResult
		}
		return json.Marshal(normalizedTextContent{Type: kind, Text: text})
	case "resource_link":
		link := normalizedResourceLink{Type: kind}
		for key, target := range map[string]*string{
			"uri": &link.URI, "name": &link.Name, "title": &link.Title,
			"description": &link.Description, "mimeType": &link.MIMEType,
		} {
			rawValue, exists := fields[key]
			if !exists {
				continue
			}
			if !decodeJSONString(rawValue, target) || !validText(*target, maxToolResultBytes, false) {
				return nil, errInvalidToolResult
			}
		}
		if strings.TrimSpace(link.URI) == "" || strings.TrimSpace(link.Name) == "" {
			return nil, errInvalidToolResult
		}
		return json.Marshal(link)
	default:
		return nil, errInvalidToolResult
	}
}

func decodeJSONString(raw json.RawMessage, target *string) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) >= 2 && trimmed[0] == '"' && json.Unmarshal(trimmed, target) == nil
}
