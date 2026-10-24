package model

import (
	"bufio"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"io"
	"net/http"
	"strings"
)

// decodeSIWCStream never exposes partial deltas as successful output. The full
// completed response is authoritative; unknown event payloads stay inert data.
func decodeSIWCStream(reader io.Reader, header http.Header) (result Result, err error) {
	return decodeSIWCStreamObserved(reader, header, nil)
}

func decodeSIWCStreamObserved(reader io.Reader, header http.Header, observed *domain.ResponseDiagnostic) (result Result, err error) {
	limited := &io.LimitedReader{R: reader, N: maxResponseBytes + 1}
	diagnostic := domain.ResponseDiagnostic{Version: 1, MediaType: "sse"}
	fail := func(stage, reason string) (Result, error) {
		diagnostic.Stage, diagnostic.Reason = stage, reason
		return Result{}, providerFailure("response_incomplete", "unknown")
	}
	defer func() {
		diagnostic.BytesRead = maxResponseBytes + 1 - limited.N
		if observed != nil {
			*observed = diagnostic
		}
		if p, ok := err.(*ProviderError); ok {
			p.ResponseDiagnostic = diagnostic
		}
	}()
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxResponseBytes)
	var data strings.Builder
	eventName := ""
	for scanner.Scan() {
		line := scanner.Text()
		if limited.N <= 0 {
			return fail("stream_read", "body_limit")
		}
		if line != "" {
			if strings.HasPrefix(line, "data:") {
				value := strings.TrimPrefix(line, "data:")
				value = strings.TrimPrefix(value, " ")
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(value)
			} else if strings.HasPrefix(line, "event:") {
				eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
			continue
		}
		if data.Len() == 0 {
			eventName = ""
			continue
		}
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
			Error    json.RawMessage `json:"error"`
			Code     string          `json:"code"`
			Param    string          `json:"param"`
		}
		diagnostic.EventsSeen++
		if json.Unmarshal([]byte(data.String()), &event) != nil {
			return fail("sse_decode", "invalid_json")
		}
		if event.Type == "" {
			return fail("sse_decode", "missing_event_type")
		}
		if eventName != "" && eventName != event.Type {
			return fail("sse_decode", "event_type_mismatch")
		}
		data.Reset()
		eventName = ""
		switch event.Type {
		case "response.completed":
			diagnostic.Stage, diagnostic.TerminalEvent = "completed_response", "response.completed"
			result, err = decodeSIWCCompletedResponse(event.Response)
			if p, ok := err.(*ProviderError); ok {
				diagnostic.Reason = p.ResponseDiagnostic.Reason
			}
			return result, err
		case "response.failed":
			diagnostic.Stage, diagnostic.Reason, diagnostic.TerminalEvent = "provider_terminal", "provider_failed", "response.failed"
			p := classifyProviderError(http.StatusOK, header, event.Response)
			p.Certainty = "rejected"
			return Result{}, p
		case "response.incomplete":
			diagnostic.TerminalEvent = "response.incomplete"
			return fail("provider_terminal", "provider_incomplete")
		case "error":
			diagnostic.Stage, diagnostic.Reason, diagnostic.TerminalEvent = "provider_terminal", "provider_error", "error"
			if len(event.Error) == 0 {
				event.Error, _ = json.Marshal(map[string]string{"code": event.Code, "param": event.Param})
			}
			raw, _ := json.Marshal(map[string]json.RawMessage{"error": event.Error})
			return Result{}, classifyProviderError(http.StatusOK, header, raw)
		}
	}
	if errors.Is(scanner.Err(), bufio.ErrTooLong) {
		return fail("stream_read", "line_limit")
	}
	if limited.N <= 0 {
		return fail("stream_read", "body_limit")
	}
	if scanner.Err() != nil {
		return fail("stream_read", "read_error")
	}
	if data.Len() > 0 || eventName != "" {
		return fail("stream_read", "unterminated_frame")
	}
	return fail("stream_read", "missing_terminal")
}
