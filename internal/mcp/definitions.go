package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"foundry/internal/domain"
)

const executionSnapshotVersion = 1

var errInvalidExecutionSnapshot = errors.New("invalid MCP execution snapshot")

func canonicalJSON(raw json.RawMessage, maxBytes int) (json.RawMessage, any, error) {
	if len(raw) == 0 || (maxBytes > 0 && len(raw) > maxBytes) || validateJSON(raw) != nil {
		return nil, nil, errInvalidExecutionSnapshot
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, errInvalidExecutionSnapshot
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, nil, errInvalidExecutionSnapshot
	}
	return canonical, value, nil
}

func parseToolDefinition(raw json.RawMessage) (domain.MCPTool, error) {
	canonical, _, err := canonicalJSON(raw, 1<<20)
	if err != nil || !object(canonical) {
		return domain.MCPTool{}, errInvalidExecutionSnapshot
	}
	var wire struct {
		Name        string          `json:"name"`
		Title       string          `json:"title"`
		Description string          `json:"description"`
		Input       json.RawMessage `json:"inputSchema"`
		Output      json.RawMessage `json:"outputSchema"`
		Annotations json.RawMessage `json:"annotations"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &wire) != nil || json.Unmarshal(raw, &fields) != nil || !validText(wire.Name, 256, true) || strings.ContainsFunc(wire.Name, unicode.IsControl) || !validText(wire.Title, 4096, false) || !validText(wire.Description, 1<<20, false) {
		return domain.MCPTool{}, errInvalidExecutionSnapshot
	}
	for _, optional := range []string{"title", "description", "outputSchema", "annotations"} {
		if value, present := fields[optional]; present && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return domain.MCPTool{}, errInvalidExecutionSnapshot
		}
	}
	if len(wire.Input) == 0 || len(wire.Input) > 64<<10 || !object(wire.Input) {
		return domain.MCPTool{}, errInvalidExecutionSnapshot
	}
	for _, rawSchema := range []json.RawMessage{wire.Input, wire.Output} {
		if len(rawSchema) == 0 {
			continue
		}
		if len(rawSchema) > 64<<10 || !object(rawSchema) || validateJSON(rawSchema) != nil {
			return domain.MCPTool{}, errInvalidExecutionSnapshot
		}
	}
	var inputRoot map[string]any
	inputDecoder := json.NewDecoder(bytes.NewReader(wire.Input))
	inputDecoder.UseNumber()
	if inputDecoder.Decode(&inputRoot) != nil || inputRoot["type"] != "object" {
		return domain.MCPTool{}, errInvalidExecutionSnapshot
	}
	if len(wire.Output) > 0 {
		var outputRoot map[string]any
		outputDecoder := json.NewDecoder(bytes.NewReader(wire.Output))
		outputDecoder.UseNumber()
		if outputDecoder.Decode(&outputRoot) != nil || outputRoot["type"] != "object" {
			return domain.MCPTool{}, errInvalidExecutionSnapshot
		}
	}
	if len(wire.Annotations) > 0 && (!object(wire.Annotations) || validateJSON(wire.Annotations) != nil) {
		return domain.MCPTool{}, errInvalidExecutionSnapshot
	}
	h := sha256.Sum256(canonical)
	return domain.MCPTool{
		Name:             wire.Name,
		Title:            wire.Title,
		Description:      wire.Description,
		InputSchema:      wire.Input,
		OutputSchema:     wire.Output,
		Annotations:      wire.Annotations,
		RawDefinition:    canonical,
		DefinitionDigest: hex.EncodeToString(h[:]),
	}, nil
}

func toolSnapshotDigest(tools []domain.MCPTool) (string, error) {
	raw, err := json.Marshal(tools)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}

// VerifyExecutionSnapshot verifies the execution-only definition bytes and all
// projections. Legacy catalog snapshots deliberately fail this check.
func VerifyExecutionSnapshot(snapshot domain.MCPToolSnapshot) error {
	if snapshot.ExecutionVersion != executionSnapshotVersion || snapshot.Tools == nil {
		return errInvalidExecutionSnapshot
	}
	names := make(map[string]bool, len(snapshot.Tools))
	for _, stored := range snapshot.Tools {
		if names[stored.Name] {
			return errInvalidExecutionSnapshot
		}
		names[stored.Name] = true
		projected, err := parseToolDefinition(stored.RawDefinition)
		if err != nil || stored.Name != projected.Name || stored.Title != projected.Title || stored.Description != projected.Description || !equivalentProjection(stored.InputSchema, projected.InputSchema, false) || !equivalentProjection(stored.OutputSchema, projected.OutputSchema, true) || !equivalentProjection(stored.Annotations, projected.Annotations, true) || !bytes.Equal(stored.RawDefinition, projected.RawDefinition) || stored.DefinitionDigest != projected.DefinitionDigest {
			return errInvalidExecutionSnapshot
		}
	}
	digest, err := toolSnapshotDigest(snapshot.Tools)
	if err != nil || digest != snapshot.Digest {
		return errInvalidExecutionSnapshot
	}
	return nil
}

func equivalentProjection(left, right json.RawMessage, optional bool) bool {
	leftTrimmed, rightTrimmed := bytes.TrimSpace(left), bytes.TrimSpace(right)
	if optional {
		leftEmpty := len(leftTrimmed) == 0 || bytes.Equal(leftTrimmed, []byte("null"))
		rightEmpty := len(rightTrimmed) == 0 || bytes.Equal(rightTrimmed, []byte("null"))
		if leftEmpty || rightEmpty {
			return leftEmpty && rightEmpty
		}
	}
	leftCanonical, _, leftErr := canonicalJSON(left, maxSchemaBytes)
	rightCanonical, _, rightErr := canonicalJSON(right, maxSchemaBytes)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftCanonical, rightCanonical)
}
