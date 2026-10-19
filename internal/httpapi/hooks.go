package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"foundry/internal/domain"
	"foundry/internal/hooks"
	"foundry/internal/store"
)

func (s *server) hooksRoute(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/hooks":
		if r.URL.RawQuery != "" {
			writeHookQueryError(w)
			return
		}
		snapshot, err := s.hooks.Snapshot()
		if err != nil {
			writeHookError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	case r.Method == http.MethodPost && r.URL.Path == "/api/hooks/rules":
		if r.URL.RawQuery != "" {
			writeHookQueryError(w)
			return
		}
		var input hooks.RuleInput
		if err := decodeHookJSON(w, r, &input); err != nil {
			writeDecodeError(w, err)
			return
		}
		rule, err := s.hooks.SaveRule(input)
		if err != nil {
			writeHookError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]domain.HookRule{"rule": rule})
	case r.Method == http.MethodGet && r.URL.Path == "/api/hooks/records":
		missionID, before, limit, err := hookRecordQuery(r.URL.RawQuery)
		if err != nil {
			writeHookQueryError(w)
			return
		}
		response, err := s.hooks.Records(missionID, before, limit)
		if err != nil {
			writeHookError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

// decodeHookJSON prevents encoding/json from silently repairing malformed
// UTF-8 or unpaired surrogate escapes before domain validation sees the name.
func decodeHookJSON(w http.ResponseWriter, r *http.Request, target *hooks.RuleInput) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if !losslessSkillJSON(raw) {
		return errors.New("invalid observation hook JSON text encoding")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return errors.New("invalid observation hook JSON object")
	}
	allowed := map[string]bool{
		"id": true, "expected_revision": true, "name": true, "enabled": true,
		"event_kinds": true, "mission_id": true, "action": true,
	}
	for name, value := range fields {
		if !allowed[name] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("invalid observation hook field")
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return decodeJSON(w, r, target)
}

func hookRecordQuery(raw string) (string, int64, int, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", 0, 0, domain.ErrInvalidHook
	}
	for key, entries := range values {
		if (key != "mission_id" && key != "before" && key != "limit") || len(entries) != 1 {
			return "", 0, 0, domain.ErrInvalidHook
		}
	}
	missionID := ""
	if entries, ok := values["mission_id"]; ok {
		missionID = entries[0]
	}
	var before int64
	if entries, ok := values["before"]; ok {
		before, err = strconv.ParseInt(entries[0], 10, 64)
		if err != nil || before <= 0 {
			return "", 0, 0, domain.ErrInvalidHook
		}
	}
	limit := 50
	if entries, ok := values["limit"]; ok {
		parsed, parseErr := strconv.ParseInt(entries[0], 10, 32)
		if parseErr != nil || parsed < 1 || parsed > 100 {
			return "", 0, 0, domain.ErrInvalidHook
		}
		limit = int(parsed)
	}
	return missionID, before, limit, nil
}

func writeHookQueryError(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "invalid_query", "observation record query is invalid")
}

func writeHookError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "observation hook resource not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "observation hook revision is stale")
	case errors.Is(err, domain.ErrInvalidHook):
		writeError(w, http.StatusBadRequest, "invalid_hook", "observation hook request is invalid")
	default:
		writeError(w, http.StatusInternalServerError, "observation_storage_error", "observation storage is unavailable")
	}
}
