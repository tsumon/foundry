package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// validateJSON rejects duplicate keys and excessive nesting before unmarshalling.
func validateJSON(raw []byte) error {
	if !utf8.Valid(raw) || !validJSONUnicodeEscapes(raw) {
		return failure("invalid_response", false)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return failure("budget_exceeded", false)
		}
		token, err := dec.Token()
		if err != nil {
			return failure("invalid_response", false)
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				key, e := dec.Token()
				if e != nil {
					return failure("invalid_response", false)
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return failure("invalid_response", false)
				}
				seen[s] = true
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
		case '[':
			for dec.More() {
				if e := walk(depth + 1); e != nil {
					return e
				}
			}
		default:
			return failure("invalid_response", false)
		}
		if _, err = dec.Token(); err != nil {
			return failure("invalid_response", false)
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return failure("invalid_response", false)
	}
	return nil
}

// encoding/json intentionally repairs lone UTF-16 surrogate escapes. Authority
// bytes must instead be lossless, while valid surrogate pairs remain accepted.
func validJSONUnicodeEscapes(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		for i++; i < len(raw); i++ {
			switch raw[i] {
			case '"':
				goto stringDone
			case '\\':
				i++
				if i >= len(raw) {
					return false
				}
				if raw[i] != 'u' {
					continue
				}
				value, ok := jsonHex4(raw, i+1)
				if !ok {
					return false
				}
				i += 4
				if value >= 0xd800 && value <= 0xdbff {
					if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
						return false
					}
					low, valid := jsonHex4(raw, i+3)
					if !valid || low < 0xdc00 || low > 0xdfff {
						return false
					}
					i += 6
				} else if value >= 0xdc00 && value <= 0xdfff {
					return false
				}
			}
		}
		return false
	stringDone:
	}
	return true
}

func jsonHex4(raw []byte, start int) (uint16, bool) {
	if start+4 > len(raw) {
		return 0, false
	}
	var value uint16
	for _, b := range raw[start : start+4] {
		value <<= 4
		switch {
		case b >= '0' && b <= '9':
			value += uint16(b - '0')
		case b >= 'a' && b <= 'f':
			value += uint16(b-'a') + 10
		case b >= 'A' && b <= 'F':
			value += uint16(b-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func (d *discovery) message(raw []byte, id int) (json.RawMessage, bool, error) {
	if err := validateJSON(raw); err != nil {
		return nil, false, err
	}
	var envelope map[string]json.RawMessage
	if !object(raw) || json.Unmarshal(raw, &envelope) != nil || string(envelope["jsonrpc"]) != `"2.0"` {
		return nil, false, failure("invalid_response", false)
	}
	if method, ok := envelope["method"]; ok {
		var name string
		if _, hasID := envelope["id"]; hasID || json.Unmarshal(method, &name) != nil || !strings.HasPrefix(name, "notifications/") || envelope["result"] != nil || envelope["error"] != nil {
			return nil, false, failure("protocol_error", false)
		}
		d.notifications++
		if d.notifications > 64 {
			return nil, false, failure("budget_exceeded", false)
		}
		return nil, true, nil
	}
	if string(envelope["id"]) != strconv.Itoa(id) {
		return nil, false, failure("protocol_error", false)
	}
	result, hasResult := envelope["result"]
	remoteError, hasError := envelope["error"]
	if hasResult == hasError {
		return nil, false, failure("invalid_response", false)
	}
	if hasError {
		if !object(remoteError) {
			return nil, false, failure("invalid_response", false)
		}
		if d.callStarted {
			return nil, false, failure("remote_error", false)
		}
		return nil, false, failure("protocol_error", false)
	}
	if !object(result) {
		return nil, false, failure("invalid_response", false)
	}
	if d.credential != "" {
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(result))
		// Preserve numeric lexemes exactly: float64 would round large keys or
		// reject valid exponents before literal-reflection checks can see them.
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return nil, false, failure("invalid_response", false)
		}
		if containsCredential(decoded, d.credential) {
			return nil, false, failure("invalid_response", false)
		}
	}
	return result, false, nil
}

func (d *discovery) readSSE(reader io.Reader, id int) (json.RawMessage, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), (1<<20)+1)
	var data strings.Builder
	event := ""
	eventBytes := 0
	for scanner.Scan() {
		line := scanner.Text()
		eventBytes += len(line) + 1
		if eventBytes > 1<<20 {
			return nil, failure("budget_exceeded", false)
		}
		if line == "" {
			if strings.TrimSpace(data.String()) != "" {
				if event != "" && event != "message" {
					return nil, failure("protocol_error", false)
				}
				result, notification, err := d.message([]byte(strings.TrimSuffix(data.String(), "\n")), id)
				if err != nil {
					return nil, err
				}
				if !notification {
					return result, nil
				}
			}
			data.Reset()
			event = ""
			eventBytes = 0
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, hasColon := strings.Cut(line, ":")
		if hasColon {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		case "event":
			event = value
		case "id", "retry": // Retained stream metadata never causes reconnect/resumption.
		default:
			return nil, failure("invalid_response", false)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, bodyError(err)
	}
	return nil, failure("transport_error", true)
}

func containsCredential(v any, secret string) bool {
	switch x := v.(type) {
	case string:
		return strings.Contains(x, secret)
	case json.Number:
		return strings.Contains(x.String(), secret)
	case bool:
		return strings.Contains(strconv.FormatBool(x), secret)
	case nil:
		return strings.Contains("null", secret)
	case map[string]any:
		for k, v := range x {
			if strings.Contains(k, secret) || containsCredential(v, secret) {
				return true
			}
		}
	case []any:
		for _, v := range x {
			if containsCredential(v, secret) {
				return true
			}
		}
	}
	return false
}
