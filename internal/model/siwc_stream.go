package model

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// decodeSIWCStream never exposes partial deltas as successful output. The full
// completed response is authoritative; unknown event payloads stay inert data.
func decodeSIWCStream(reader io.Reader, header http.Header) (Result, error) {
	limited := &io.LimitedReader{R: reader, N: maxResponseBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxResponseBytes)
	var data strings.Builder
	eventName := ""
	for scanner.Scan() {
		line := scanner.Text()
		if limited.N <= 0 {
			return Result{}, providerFailure("response_incomplete", "unknown")
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
		if json.Unmarshal([]byte(data.String()), &event) != nil || event.Type == "" || (eventName != "" && eventName != event.Type) {
			return Result{}, providerFailure("response_incomplete", "unknown")
		}
		data.Reset()
		eventName = ""
		switch event.Type {
		case "response.completed":
			return decodeRoutedResponses(event.Response)
		case "response.failed":
			p := classifyProviderError(http.StatusOK, header, event.Response)
			p.Certainty = "rejected"
			return Result{}, p
		case "response.incomplete":
			return Result{}, providerFailure("response_incomplete", "unknown")
		case "error":
			if len(event.Error) == 0 {
				event.Error, _ = json.Marshal(map[string]string{"code": event.Code, "param": event.Param})
			}
			raw, _ := json.Marshal(map[string]json.RawMessage{"error": event.Error})
			return Result{}, classifyProviderError(http.StatusOK, header, raw)
		}
	}
	return Result{}, providerFailure("response_incomplete", "unknown")
}
