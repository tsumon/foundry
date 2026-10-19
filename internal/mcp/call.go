package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"foundry/internal/domain"
)

type CallStage string

const (
	CallStagePreflight        CallStage = "preflight"
	CallStageCallStarted      CallStage = "call_started"
	CallStageResponseReceived CallStage = "response_received"
)

type CallResult struct {
	Result          json.RawMessage
	Stage           CallStage
	Dispatched      bool
	RemoteToolError bool
	RequestBytes    int64
	ResponseBytes   int64
	Requests        int
	Duration        time.Duration
}

func Call(ctx context.Context, connection domain.MCPConnection, tool domain.MCPTool, policy, arguments json.RawMessage, credential string, beforeDispatch func() error) (result CallResult, err error) {
	started := time.Now()
	defer func() { result.Duration = time.Since(started) }()
	callContext, cancel := context.WithDeadline(ctx, started.Add(30*time.Second))
	defer cancel()
	result.Stage = CallStagePreflight
	tool = cloneTool(tool)
	policy = bytes.Clone(policy)
	arguments = bytes.Clone(arguments)
	if !connection.Enabled || connection.Protocol != domain.MCPProtocol || validateCallEndpoint(connection.Endpoint) != nil {
		return result, callFailure("invalid_connection", false, false)
	}
	if len(credential) > 8192 || strings.ContainsFunc(credential, unicode.IsControl) {
		return result, callFailure("invalid_connection", false, false)
	}
	if !validFrozenTool(tool) {
		return result, callFailure("invalid_tool", false, false)
	}
	if output := bytes.TrimSpace(tool.OutputSchema); len(output) > 0 && !bytes.Equal(output, []byte("null")) {
		if _, schemaErr := compileExecutionSchema(tool.OutputSchema, false); schemaErr != nil {
			return result, callFailure("invalid_tool", false, false)
		}
	}
	if !supportsSynchronousCall(tool.RawDefinition) {
		return result, callFailure("task_unsupported", false, false)
	}
	canonicalArguments, validationErr := ValidateArguments(tool, policy, arguments)
	if validationErr != nil {
		return result, callFailure("invalid_arguments", false, false)
	}
	client, transport, clientErr := newCallHTTPClient(connection.Endpoint)
	if clientErr != nil {
		return result, callFailure("invalid_connection", false, false)
	}
	defer transport.CloseIdleConnections()
	d := &discovery{ctx: callContext, client: client, endpoint: connection.Endpoint, credential: credential, maxRequests: 14, result: Result{Tools: []domain.MCPTool{}}}
	defer func() {
		d.cleanup()
		result.Requests = d.result.Requests
		result.RequestBytes = d.requestBytes
		result.ResponseBytes = d.result.ResponseBytes
	}()
	if err = d.initialize(); err != nil {
		return result, preflightError(err)
	}
	if err = d.listTools(); err != nil {
		return result, preflightError(err)
	}
	live, found := selectedTool(d.result.Tools, tool.Name)
	if !found || !bytes.Equal(live.RawDefinition, tool.RawDefinition) || live.DefinitionDigest != tool.DefinitionDigest {
		return result, callFailure("tool_changed", false, false)
	}
	if !supportsSynchronousCall(live.RawDefinition) {
		return result, callFailure("task_unsupported", false, false)
	}
	canonicalArguments, err = ValidateArguments(live, policy, canonicalArguments)
	if err != nil {
		return result, callFailure("invalid_arguments", false, false)
	}
	if beforeDispatch == nil || beforeDispatch() != nil {
		return result, callFailure("dispatch_checkpoint_failed", false, false)
	}
	result.Stage = CallStageCallStarted
	result.Dispatched = true
	d.callStarted = true
	raw, postErr := d.postWithin(12, "tools/call", map[string]any{"name": tool.Name, "arguments": json.RawMessage(canonicalArguments)}, 30*time.Second)
	if postErr != nil {
		var protocolError *Error
		if errors.As(postErr, &protocolError) && !protocolError.Unknown && d.callResponseReceived {
			result.Stage = CallStageResponseReceived
		}
		return result, sentError(postErr)
	}
	result.Stage = CallStageResponseReceived
	normalized, validationErr := validateToolResultWithCredential(tool, raw, credential)
	if validationErr != nil {
		return result, callFailure("invalid_result", false, true)
	}
	result.Result = normalized
	var decoded struct {
		IsError bool `json:"isError"`
	}
	_ = json.Unmarshal(normalized, &decoded)
	result.RemoteToolError = decoded.IsError
	return result, nil
}

func cloneTool(tool domain.MCPTool) domain.MCPTool {
	tool.InputSchema = bytes.Clone(tool.InputSchema)
	tool.OutputSchema = bytes.Clone(tool.OutputSchema)
	tool.Annotations = bytes.Clone(tool.Annotations)
	tool.RawDefinition = bytes.Clone(tool.RawDefinition)
	return tool
}

func validFrozenTool(tool domain.MCPTool) bool {
	projected, err := parseToolDefinition(tool.RawDefinition)
	return err == nil && tool.Name == projected.Name && tool.Title == projected.Title && tool.Description == projected.Description && equivalentProjection(tool.InputSchema, projected.InputSchema, false) && equivalentProjection(tool.OutputSchema, projected.OutputSchema, true) && equivalentProjection(tool.Annotations, projected.Annotations, true) && bytes.Equal(tool.RawDefinition, projected.RawDefinition) && tool.DefinitionDigest == projected.DefinitionDigest
}

func supportsSynchronousCall(definition json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(definition, &fields) != nil {
		return false
	}
	raw, exists := fields["execution"]
	if !exists {
		return true
	}
	var execution map[string]json.RawMessage
	if !object(raw) || json.Unmarshal(raw, &execution) != nil {
		return false
	}
	taskSupport := "forbidden"
	if value, exists := execution["taskSupport"]; exists {
		if !decodeJSONString(value, &taskSupport) {
			return false
		}
	}
	return taskSupport == "forbidden" || taskSupport == "optional"
}

func selectedTool(tools []domain.MCPTool, name string) (domain.MCPTool, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return domain.MCPTool{}, false
}

func validateToolResultWithCredential(tool domain.MCPTool, raw json.RawMessage, credential string) (json.RawMessage, error) {
	if credential != "" {
		_, decoded, err := canonicalJSON(raw, maxToolResultBytes)
		if err != nil || containsCredential(decoded, credential) {
			return nil, errInvalidToolResult
		}
	}
	return ValidateToolResult(tool, raw)
}

func callFailure(code string, unknown, mayHaveBeenSent bool) error {
	return &Error{Code: code, Unknown: unknown, CallMayHaveBeenSent: mayHaveBeenSent}
}

func preflightError(err error) error {
	var protocolError *Error
	if !errors.As(err, &protocolError) {
		return callFailure("transport_error", false, false)
	}
	return callFailure(protocolError.Code, false, false)
}

func sentError(err error) error {
	var protocolError *Error
	if !errors.As(err, &protocolError) {
		return callFailure("transport_error", true, true)
	}
	code := protocolError.Code
	if code == "invalid_response" {
		code = "invalid_result"
	}
	return callFailure(code, protocolError.Unknown, true)
}
