package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"foundry/internal/capability"
	"foundry/internal/domain"
	"foundry/internal/store"
)

func (s *server) capabilitiesRoute(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/capabilities":
		snapshot, err := s.capability.Snapshot()
		if err != nil {
			writeCapabilityError(w, err)
			return
		}
		writeJSON(w, 200, capabilitySnapshotView(snapshot))
	case r.Method == http.MethodPost && r.URL.Path == "/api/capabilities/mcp":
		s.saveMCP(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/capabilities/skills":
		s.saveSkill(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/capabilities/skills/"):
		s.readSkillRevision(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/capabilities/git":
		s.saveGit(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/capabilities/git/"):
		s.inspectGit(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/capabilities/mcp/"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/capabilities/mcp/"), "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] != "discover" {
			writeError(w, 404, "not_found", "route not found")
			return
		}
		s.discoverMCP(w, r, parts[0])
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/capabilities/discoveries/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/capabilities/discoveries/")
		if id == "" || strings.Contains(id, "/") {
			writeError(w, 404, "not_found", "route not found")
			return
		}
		op, err := s.capability.Discovery(id)
		if err != nil {
			writeCapabilityError(w, err)
			return
		}
		s.writeDiscovery(w, op)
	default:
		writeError(w, 404, "not_found", "route not found")
	}
}
func (s *server) saveMCP(w http.ResponseWriter, r *http.Request) {
	var in capability.MCPInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeDecodeError(w, err)
		return
	}
	connection, err := s.capability.SaveMCP(in)
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"connection": connectionView(connection)})
}
func (s *server) discoverMCP(w http.ResponseWriter, r *http.Request, id string) {
	var in struct {
		ExpectedRevision int64  `json:"expected_revision"`
		RequestID        string `json:"request_id"`
		Consent          bool   `json:"consent"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeDecodeError(w, err)
		return
	}
	op, err := s.capability.Discover(r.Context(), id, in.ExpectedRevision, in.RequestID, in.Consent)
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	s.writeDiscovery(w, op)
}
func (s *server) writeDiscovery(w http.ResponseWriter, op domain.DiscoveryOperation) {
	snapshot, err := s.capability.SnapshotForOperation(op)
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	response := map[string]any{"operation": op}
	if snapshot != nil {
		response["snapshot"] = mcpSnapshotView(*snapshot)
	}
	writeJSON(w, 200, response)
}
func writeCapabilityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "not_found", "capability resource not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, 409, "conflict", "capability revision or active operation conflicts")
	case errors.Is(err, domain.ErrInvalidCapability):
		writeError(w, 400, "invalid_capability", "capability request is invalid")
	default:
		writeError(w, 500, "internal", "capability operation failed")
	}
}
