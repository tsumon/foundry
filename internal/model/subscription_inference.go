package model

import (
	"bufio"
	"bytes"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/jsontext"
	"io"
	"net/http"
	"strings"
)

type SubscriptionInferenceFailure struct {
	Failure                   *ProviderError
	TerminalEvent             string
	InputTokens, OutputTokens int64
	UsageKnown                bool
}

func (e *SubscriptionInferenceFailure) Error() string { return e.Failure.Error() }
func (e *SubscriptionInferenceFailure) Unwrap() error { return e.Failure }

func SubscriptionInferenceHTTPFailure(status int, header http.Header, body []byte, secret string) error {
	if len(body) > 1<<20 {
		return providerFailure("response_incomplete", "unknown")
	}
	p := classifyProviderError(status, header, body)
	if strings.Contains(p.RequestID, secret) {
		p.RequestID = ""
	}
	if strings.Contains(p.ProviderCode, secret) {
		p.ProviderCode = ""
	}
	if strings.Contains(p.ProviderParam, secret) {
		p.ProviderParam = ""
	}
	return p
}

// DecodeSubscriptionInference requires a real completed terminal event. Deltas
// remain untrusted data and are never retained, including on a later failure.
func DecodeSubscriptionInference(reader io.Reader, secret string) (Result, error) {
	fail := func() (Result, error) { return Result{}, providerFailure("response_incomplete", "unknown") }
	limited := &io.LimitedReader{R: reader, N: (1 << 20) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data strings.Builder
	eventName := ""
	for scanner.Scan() {
		if limited.N <= 0 {
			return fail()
		}
		line := scanner.Text()
		if line != "" {
			if strings.HasPrefix(line, "data:") {
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			} else if strings.HasPrefix(line, "event:") {
				eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
			continue
		}
		if data.Len() == 0 {
			eventName = ""
			continue
		}
		raw := []byte(data.String())
		if !jsontext.Lossless(raw) {
			return fail()
		}
		if containsCredentialJSON(raw, secret) {
			return fail()
		}
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
			Error    json.RawMessage `json:"error"`
			Code     string          `json:"code"`
			Param    string          `json:"param"`
			Item     json.RawMessage `json:"item"`
			Part     *struct {
				Type string `json:"type"`
			} `json:"part"`
		}
		if json.Unmarshal(raw, &event) != nil || event.Type == "" || eventName != "" && eventName != event.Type {
			return fail()
		}
		data.Reset()
		eventName = ""
		if len(event.Item) > 0 && !inferenceOutputItem(event.Item) {
			return fail()
		}
		if event.Part != nil && event.Part.Type != "output_text" && event.Part.Type != "summary_text" {
			return fail()
		}
		switch event.Type {
		case "response.completed":
			var response struct {
				Output []json.RawMessage `json:"output"`
			}
			if json.Unmarshal(event.Response, &response) != nil {
				return fail()
			}
			for _, item := range response.Output {
				if !inferenceOutputItem(item) {
					return fail()
				}
			}
			result, e := decodeRoutedResponses(event.Response)
			if e != nil || result.ResolvedModel != domain.OpenAIInferenceModel {
				return fail()
			}
			return result, nil
		case "response.failed":
			failure := &SubscriptionInferenceFailure{Failure: classifyProviderError(http.StatusOK, http.Header{}, event.Response), TerminalEvent: event.Type}
			var response struct {
				Usage *responsesUsage `json:"usage"`
			}
			if json.Unmarshal(event.Response, &response) == nil && response.Usage != nil && validUsage(response.Usage.InputTokens, response.Usage.OutputTokens) {
				failure.InputTokens = *response.Usage.InputTokens
				failure.OutputTokens = *response.Usage.OutputTokens
				failure.UsageKnown = true
			}
			return Result{}, failure
		case "response.incomplete":
			return fail()
		case "error":
			if len(event.Error) == 0 {
				event.Error, _ = json.Marshal(map[string]string{"code": event.Code, "param": event.Param})
			}
			body, _ := json.Marshal(map[string]json.RawMessage{"error": event.Error})
			return Result{}, &SubscriptionInferenceFailure{Failure: classifyProviderError(http.StatusOK, http.Header{}, body), TerminalEvent: event.Type}
		case "response.created", "response.in_progress", "response.output_item.added", "response.output_item.done", "response.content_part.added", "response.content_part.done", "response.output_text.delta", "response.output_text.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done":
		default:
			return fail()
		}
	}
	return fail()
}

func containsCredentialJSON(raw []byte, secret string) bool {
	if secret == "" {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		token, e := decoder.Token()
		if e == io.EOF {
			return false
		}
		if e != nil {
			return true
		}
		if text, ok := token.(string); ok && strings.Contains(text, secret) {
			return true
		}
	}
}

func inferenceOutputItem(raw json.RawMessage) bool {
	var item struct {
		Type         string            `json:"type"`
		ToolCalls    []json.RawMessage `json:"tool_calls"`
		FunctionCall json.RawMessage   `json:"function_call"`
	}
	return json.Unmarshal(raw, &item) == nil && (item.Type == "message" || item.Type == "reasoning") && len(item.ToolCalls) == 0 && (len(item.FunctionCall) == 0 || string(item.FunctionCall) == "null")
}
