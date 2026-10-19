package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"foundry/internal/domain"
	"foundry/internal/engine"
)

type revisionRequest struct {
	Revision int64 `json:"revision"`
}

type planRequest struct {
	Revision          int64 `json:"revision"`
	ControlledConsent bool  `json:"controlled_consent,omitempty"`
}

func (s *server) listMissions(w http.ResponseWriter) {
	missions, err := s.store.ListMissions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not list missions")
		return
	}
	views := make([]any, 0, len(missions))
	for _, m := range missions {
		view, err := s.missionView(m)
		if err != nil {
			writeMappedError(w, err)
			return
		}
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"missions": views})
}

func (s *server) createMission(w http.ResponseWriter, r *http.Request) {
	var request engine.CreateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	if _, bound, err := domain.BaseWorkspaceMode(request.WorkspaceMode); err != nil || bound {
		writeError(w, 400, "validation", "workspace format is selected by the server")
		return
	}
	if domain.IsResearchFlowV2(request.WorkspaceMode, request.ToolMode) {
		eligibility, err := s.engine.LocalMCPTestSelection()
		if err != nil {
			writeMappedError(w, err)
			return
		}
		if !eligibility.Eligible {
			writeError(w, 409, "conflict", "owned local test creation unavailable")
			return
		}
	}
	mission, err := s.engine.Create(r.Context(), request)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	s.writeMission(w, mission, nil)
}

func (s *server) missionRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/missions/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	id := parts[0]
	if s.mcpTaskRoute(w, r, parts) {
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		mission, err := s.store.GetMission(id)
		if err != nil {
			writeMappedError(w, err)
			return
		}
		s.writeMission(w, mission, nil)
		return
	}
	if len(parts) == 2 && parts[1] == "invocations" {
		if r.Method == http.MethodGet {
			s.invocationList(w, r, id)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if len(parts) == 2 && parts[1] == "read-grants" {
		if r.Method == http.MethodGet {
			s.readGrants(w, r, id)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if len(parts) == 3 && parts[1] == "invocations" {
		if r.Method == http.MethodGet {
			s.invocationDetail(w, r, id, parts[2])
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if parts[1] == "plan-revisions" && (r.Method == http.MethodGet || r.Method == http.MethodPost) {
		s.planRevisions(w, r, id)
		return
	}
	if r.Method == http.MethodGet && parts[1] == "local-operations" {
		operations, err := s.store.ListLocalOperations(id)
		if err != nil {
			writeMappedError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"local_operations": operations})
		return
	}
	if r.Method == http.MethodGet && (parts[1] == "patch" || parts[1] == "report") {
		s.download(w, r, id, parts[1])
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if parts[1] == "chat" {
		var request struct {
			Revision int64  `json:"revision"`
			Message  string `json:"message"`
		}
		if err := decodeJSON(w, r, &request); err != nil {
			writeDecodeError(w, err)
			return
		}
		mission, err := s.engine.Chat(r.Context(), id, request.Revision, request.Message)
		if err != nil {
			writeMappedError(w, err)
			return
		}
		s.writeMission(w, mission, nil)
		return
	}
	if parts[1] == "plan" {
		var request planRequest
		if err := decodeJSON(w, r, &request); err != nil {
			writeDecodeError(w, err)
			return
		}
		var (
			mission domain.Mission
			err     error
		)
		if request.ControlledConsent {
			mission, err = s.engine.PlanControlled(r.Context(), id, request.Revision, true)
		} else {
			mission, err = s.engine.Plan(r.Context(), id, request.Revision)
		}
		if err != nil {
			writeMappedError(w, err)
			return
		}
		s.writeMission(w, mission, nil)
		return
	}

	var request revisionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	var (
		mission domain.Mission
		err     error
	)
	switch parts[1] {
	case "approve":
		mission, err = s.engine.Approve(r.Context(), id, request.Revision)
	case "pause":
		mission, err = s.engine.Pause(r.Context(), id, request.Revision)
	case "deliver":
		mission, err = s.engine.Deliver(r.Context(), id, request.Revision)
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if err != nil {
		writeMappedError(w, err)
		return
	}
	s.writeMission(w, mission, nil)
}

func (s *server) planRevisions(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method == http.MethodGet {
		revisions, err := s.store.ListPlanRevisions(id)
		if err != nil {
			writeMappedError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plan_revisions": revisions})
		return
	}
	// encoding/json replaces invalid raw UTF-8 with U+FFFD. Check the bounded
	// source bytes first so a malformed revision is rejected, not silently edited.
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeDecodeError(w, err)
		return
	}
	if !utf8.Valid(data) {
		writeDecodeError(w, errors.New("invalid UTF-8"))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	var request engine.PlanRevisionRequest
	if err = decodeJSON(w, r, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	mission, revision, err := s.engine.RevisePlan(r.Context(), id, request)
	if err != nil {
		if errors.Is(err, engine.ErrPlanRevisionInput) {
			writeError(w, http.StatusBadRequest, "validation", "invalid plan revision input; check plan fields, reason, dependencies and allowed unprotected files; the plan must change")
		} else if errors.Is(err, engine.ErrInvalidPlanRevision) {
			writeError(w, http.StatusBadRequest, "validation", "plan revision unavailable; refresh the mission and check awaiting-approval state, pending operations and snapshot/candidate integrity before retrying")
		} else {
			writeMappedError(w, err)
		}
		return
	}
	s.writeMission(w, mission, map[string]any{"plan_revision": revision})
}

func (s *server) download(w http.ResponseWriter, r *http.Request, id, kind string) {
	data, err := s.engine.ReadDelivery(id, kind)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "delivery artifact not found")
		return
	}
	wantedName := "changes.patch"
	contentType := "text/plain; charset=utf-8"
	if kind == "report" {
		wantedName = "report.md"
		contentType = "text/markdown; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, wantedName))
	http.ServeContent(w, r, wantedName, time.Time{}, bytes.NewReader(data))
}
