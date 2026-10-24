package domain

// ResponseDiagnostic contains observations, never provider prose or authority.
// BytesRead includes scanner read-ahead; neither counter is token/billing usage.
type ResponseDiagnostic struct {
	Version       int    `json:"version"`
	Stage         string `json:"stage"`
	Reason        string `json:"reason"`
	MediaType     string `json:"media_type,omitempty"`
	TerminalEvent string `json:"terminal_event,omitempty"`
	EventsSeen    int    `json:"events_seen"`
	BytesRead     int64  `json:"bytes_read"`
}

func (d ResponseDiagnostic) Valid() bool {
	if d.Version != 1 || d.EventsSeen < 0 || d.EventsSeen > 2<<20 || d.BytesRead < 0 || d.BytesRead > (2<<20)+1 {
		return false
	}
	switch d.MediaType {
	case "", "sse", "json", "other", "invalid", "missing":
	default:
		return false
	}
	terminal := ""
	valid := false
	switch d.Stage {
	case "http_media":
		valid = d.Reason == "missing_media_type" || d.Reason == "invalid_media_type" || d.Reason == "unexpected_media_type"
	case "stream_read":
		valid = d.Reason == "body_limit" || d.Reason == "line_limit" || d.Reason == "read_error" || d.Reason == "missing_terminal" || d.Reason == "unterminated_frame"
	case "sse_decode":
		valid = d.Reason == "invalid_json" || d.Reason == "missing_event_type" || d.Reason == "event_type_mismatch"
	case "provider_terminal":
		switch d.Reason {
		case "provider_incomplete":
			terminal, valid = "response.incomplete", true
		case "provider_failed":
			terminal, valid = "response.failed", true
		case "provider_error":
			terminal, valid = "error", true
		}
	case "completed_response":
		terminal = "response.completed"
		valid = d.Reason == "invalid_envelope_json" || d.Reason == "invalid_envelope" || d.Reason == "invalid_output" || d.Reason == "invalid_content" || d.Reason == "empty_output"
	case "output_guard":
		valid = d.Reason == "credential_echo"
		terminal = "response.completed"
	}
	if !valid || d.TerminalEvent != terminal {
		return false
	}
	if d.Stage == "http_media" {
		if d.BytesRead != 0 || d.EventsSeen != 0 {
			return false
		}
		switch d.Reason {
		case "missing_media_type":
			return d.MediaType == "missing"
		case "invalid_media_type":
			return d.MediaType == "invalid"
		default:
			return d.MediaType == "json" || d.MediaType == "other"
		}
	}
	if d.MediaType != "sse" {
		return false
	}
	return int64(d.EventsSeen) <= d.BytesRead && (d.Stage == "stream_read" || d.EventsSeen > 0)
}
