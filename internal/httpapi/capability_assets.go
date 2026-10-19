package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"foundry/internal/capability"
	"foundry/internal/domain"
	"foundry/internal/jsontext"
)

func (s *server) saveSkill(w http.ResponseWriter, r *http.Request) {
	var in capability.SkillInput
	if err := decodeSkillJSON(w, r, &in); err != nil {
		writeDecodeError(w, err)
		return
	}
	revision, err := s.capability.SaveSkill(in)
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"revision": revision})
}

// encoding/json repairs invalid UTF-8 and unpaired UTF-16 escapes with U+FFFD.
// Reject those encodings before decoding so an uploaded static package is never
// silently changed. Other endpoints retain their existing JSON behavior.
func decodeSkillJSON(w http.ResponseWriter, r *http.Request, target *capability.SkillInput) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if !losslessSkillJSON(raw) {
		return errors.New("invalid skill JSON text encoding")
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return decodeJSON(w, r, target)
}

func losslessSkillJSON(raw []byte) bool {
	return jsontext.Lossless(raw)
}

func (s *server) readSkillRevision(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/capabilities/skills/"), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "revisions" {
		writeError(w, 404, "not_found", "route not found")
		return
	}
	number, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || number < 1 {
		writeCapabilityError(w, domain.ErrInvalidCapability)
		return
	}
	revision, err := s.capability.SkillRevision(parts[0], number)
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"revision": revision})
}

func (s *server) saveGit(w http.ResponseWriter, r *http.Request) {
	var in capability.GitInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeDecodeError(w, err)
		return
	}
	repository, err := s.capability.SaveGit(in)
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"repository": repository})
}

func (s *server) inspectGit(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/capabilities/git/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "inspect" {
		writeError(w, 404, "not_found", "route not found")
		return
	}
	var in struct {
		ExpectedRevision int64 `json:"expected_revision"`
		Consent          bool  `json:"consent"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeDecodeError(w, err)
		return
	}
	observation, err := s.capability.InspectGit(r.Context(), parts[0], in.ExpectedRevision, in.Consent)
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"observation": observation})
}
