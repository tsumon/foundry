package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"foundry/internal/jsontext"
	"io"
	"net/http"
)

func (s *server) localMCPTest(w http.ResponseWriter) {
	view, err := s.engine.LocalMCPTestSelection()
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, 200, view)
}

func validMCPRequestID(id string) bool {
	// Keep reconciliation identities safe as one URL path segment, including
	// generated UUIDs: [A-Za-z0-9_-]{1,256}. No percent-encoding ambiguity.
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// These authority intents are flat exact objects. Reject duplicate, repaired,
// omitted, null and case-aliased fields before the ordinary typed decoder.
func decodeMCPAction(w http.ResponseWriter, r *http.Request, target any, fields ...string) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	invalid := errors.New("exact action object required")
	if !jsontext.Lossless(raw) {
		return invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return invalid
	}
	allowed := map[string]bool{}
	for _, key := range fields {
		allowed[key] = true
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return invalid
		}
		name, ok := key.(string)
		if !ok || !allowed[name] {
			return invalid
		}
		delete(allowed, name)
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid
		}
	}
	if len(allowed) != 0 {
		return invalid
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return decodeJSON(w, r, target)
}

// Only exact task-scoped paths reach the controller. No arguments, endpoint,
// profile or authority object is accepted in a decision or continuation body.
func (s *server) mcpTaskRoute(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	name := parts[1]
	if name != "mcp-decision" && name != "mcp-proposals" && name != "mcp-decisions" && name != "mcp-invocations" && name != "research-continue" {
		return false
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "validation", "task MCP queries are not supported")
		return true
	}
	id := parts[0]
	switch {
	case r.Method == "GET" && len(parts) == 2 && name == "mcp-decision":
		view, err := s.engine.ResearchMCPDecision(id)
		if err != nil {
			writeMappedError(w, err)
		} else {
			writeJSON(w, 200, map[string]any{"decision": view})
		}
	case r.Method == "GET" && len(parts) == 3 && parts[2] != "" && name == "mcp-decisions":
		decision, err := s.engine.ResearchMCPDecisionReceipt(id, parts[2])
		if err != nil {
			writeMappedError(w, err)
			return true
		}
		writeJSON(w, 200, map[string]any{"decision": decision})
	case r.Method == "GET" && len(parts) == 2 && name == "mcp-invocations":
		view, err := s.engine.ResearchMCPHistory(id)
		if err != nil {
			writeMappedError(w, err)
		} else {
			writeJSON(w, 200, view)
		}
	case r.Method == "GET" && len(parts) == 3 && parts[2] != "" && name == "mcp-invocations":
		view, err := s.engine.ResearchMCPInvocation(id, parts[2])
		if err != nil {
			writeMappedError(w, err)
		} else {
			writeJSON(w, 200, map[string]any{"invocation": view})
		}
	case r.Method == "POST" && len(parts) == 4 && name == "mcp-proposals" && parts[2] != "" && parts[3] == "decision":
		var body struct {
			Revision  int64  `json:"expected_revision"`
			Digest    string `json:"proposal_digest"`
			Decision  string `json:"decision"`
			RequestID string `json:"request_id"`
		}
		if err := decodeMCPAction(w, r, &body, "expected_revision", "proposal_digest", "decision", "request_id"); err != nil {
			writeDecodeError(w, err)
			return true
		}
		if (body.Decision != "approve" && body.Decision != "reject") || body.Digest == "" || !validMCPRequestID(body.RequestID) {
			writeError(w, 400, "validation", "exact decision and request identity required")
			return true
		}
		m, err := s.engine.DecideResearchMCP(r.Context(), id, body.Revision, parts[2], body.Digest, body.RequestID, body.Decision == "approve")
		if err != nil {
			writeMappedError(w, err)
		} else {
			s.writeMission(w, m, nil)
		}
	case r.Method == "POST" && len(parts) == 2 && name == "research-continue":
		var body struct {
			Revision int64  `json:"expected_revision"`
			IntentID string `json:"intent_id"`
		}
		if err := decodeMCPAction(w, r, &body, "expected_revision", "intent_id"); err != nil {
			writeDecodeError(w, err)
			return true
		}
		if !validMCPRequestID(body.IntentID) {
			writeError(w, 400, "validation", "stable continuation identity required")
			return true
		}
		m, err := s.engine.ContinueResearch(r.Context(), id, body.Revision, body.IntentID)
		if err != nil {
			writeMappedError(w, err)
		} else {
			s.writeMission(w, m, nil)
		}
	default:
		writeError(w, 404, "not_found", "route not found")
	}
	return true
}
