package model

import (
	"encoding/json"
	"strings"
)

func decodeRoutedChat(body []byte) (Result, error) {
	var envelope struct {
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role         string            `json:"role"`
				Content      string            `json:"content"`
				ToolCalls    []json.RawMessage `json:"tool_calls"`
				FunctionCall json.RawMessage   `json:"function_call"`
			} `json:"message"`
		} `json:"choices"`
		Usage *chatUsage `json:"usage"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Object != "chat.completion" || len(envelope.Choices) != 1 {
		return Result{}, providerFailure("response_incomplete", "unknown")
	}
	choice := envelope.Choices[0]
	if choice.FinishReason != "stop" || choice.Message.Role != "assistant" || len(choice.Message.ToolCalls) != 0 || (len(choice.Message.FunctionCall) > 0 && string(choice.Message.FunctionCall) != "null") || strings.TrimSpace(choice.Message.Content) == "" {
		return Result{}, providerFailure("response_incomplete", "unknown")
	}
	result := Result{Text: choice.Message.Content, ResolvedModel: safeIdentifier(envelope.Model)}
	if u := envelope.Usage; u != nil && validUsage(u.PromptTokens, u.CompletionTokens) {
		result.InputTokens = *u.PromptTokens
		result.OutputTokens = *u.CompletionTokens
		result.UsageKnown = true
	}
	return result, nil
}

func decodeRoutedResponses(body []byte) (Result, error) {
	var envelope struct {
		Object string          `json:"object"`
		Status string          `json:"status"`
		Model  string          `json:"model"`
		Error  json.RawMessage `json:"error"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Status  string `json:"status"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage *responsesUsage `json:"usage"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Object != "response" || envelope.Status != "completed" || (len(envelope.Error) > 0 && string(envelope.Error) != "null") {
		return Result{}, providerFailure("response_incomplete", "unknown")
	}
	var text strings.Builder
	for _, output := range envelope.Output {
		// Reasoning items are inert metadata, never executable instructions.
		if output.Type == "reasoning" {
			continue
		}
		if output.Type != "message" || output.Role != "assistant" || output.Status != "completed" {
			return Result{}, providerFailure("response_incomplete", "unknown")
		}
		for _, content := range output.Content {
			if content.Type != "output_text" {
				return Result{}, providerFailure("response_incomplete", "unknown")
			}
			text.WriteString(content.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return Result{}, providerFailure("response_incomplete", "unknown")
	}
	result := Result{Text: text.String(), ResolvedModel: safeIdentifier(envelope.Model)}
	if u := envelope.Usage; u != nil && validUsage(u.InputTokens, u.OutputTokens) {
		result.InputTokens = *u.InputTokens
		result.OutputTokens = *u.OutputTokens
		result.UsageKnown = true
	}
	return result, nil
}

func decodeMessages(body []byte) (Result, error) {
	var envelope struct {
		Type       string `json:"type"`
		Role       string `json:"role"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage *responsesUsage `json:"usage"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Type != "message" || envelope.Role != "assistant" || (envelope.StopReason != "end_turn" && envelope.StopReason != "stop_sequence") {
		return Result{}, providerFailure("response_incomplete", "unknown")
	}
	var text strings.Builder
	for _, content := range envelope.Content {
		if content.Type != "text" {
			return Result{}, providerFailure("response_incomplete", "unknown")
		}
		text.WriteString(content.Text)
	}
	if strings.TrimSpace(text.String()) == "" {
		return Result{}, providerFailure("response_incomplete", "unknown")
	}
	result := Result{Text: text.String(), ResolvedModel: safeIdentifier(envelope.Model)}
	if u := envelope.Usage; u != nil && validUsage(u.InputTokens, u.OutputTokens) {
		result.InputTokens = *u.InputTokens
		result.OutputTokens = *u.OutputTokens
		result.UsageKnown = true
	}
	return result, nil
}
