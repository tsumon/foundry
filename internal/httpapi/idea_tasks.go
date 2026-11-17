package httpapi

import (
	"errors"
	"foundry/internal/engine"
	"foundry/internal/workspace"
	"net/http"
)

func (s *server) ideaCapability() (bool, string) {
	available, reason := s.engine.IdeaAvailable()
	if !s.codingWorkbench {
		available = false
		reason = "coding_workbench_disabled"
	}
	if s.recoveryReadOnly {
		available = false
		reason = "recovery_read_only"
	}
	return available, reason
}

func (s *server) ideaTasks(w http.ResponseWriter, r *http.Request) {
	available, reason := s.ideaCapability()
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"available": available, "reason": reason})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 404, "not_found", "route not found")
		return
	}
	if !available {
		writeError(w, 409, "idea_tasks_unavailable", reason)
		return
	}
	var q engine.IdeaCreateRequest
	if err := decodeCodingJSON(w, r, &q); err != nil {
		writeDecodeError(w, err)
		return
	}
	m, err := s.engine.CreateIdea(r.Context(), q)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	s.writeMission(w, m, nil)
}

func (s *server) ideaContinueRoute(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) != 2 || parts[1] != "idea-continue" {
		return false
	}
	if r.Method != http.MethodPost {
		writeError(w, 404, "not_found", "route not found")
		return true
	}
	available, reason := s.ideaCapability()
	if !available {
		writeError(w, 409, "idea_tasks_unavailable", reason)
		return true
	}
	var q engine.IdeaCreateRequest
	if err := decodeCodingJSON(w, r, &q); err != nil {
		writeDecodeError(w, err)
		return true
	}
	m, err := s.engine.ContinueIdea(r.Context(), parts[0], q)
	if errors.Is(err, workspace.ErrBrowserAppAsset) {
		writeBrowserAssetError(w, err)
		return true
	}
	if err != nil {
		writeMappedError(w, err)
		return true
	}
	s.writeMission(w, m, nil)
	return true
}

func (s *server) ideaArtifactRoute(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 2 || (parts[1] != "app-preview" && parts[1] != "source" && parts[1] != "app-html") {
		return false
	}
	if r.Method != http.MethodGet || len(parts) > 3 || len(parts) == 3 && (parts[1] != "app-preview" || parts[2] != "frame") {
		writeError(w, 404, "not_found", "route not found")
		return true
	}
	if parts[1] == "source" {
		b, err := s.engine.AppSource(parts[0])
		if err != nil {
			writeMappedError(w, err)
			return true
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="application-source.zip"`)
		w.Write(b)
		return true
	}
	if parts[1] == "app-html" {
		b, err := s.engine.AppStandaloneHTML(parts[0])
		if err != nil {
			writeMappedError(w, err)
			return true
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="application.html"`)
		w.Write(b)
		return true
	}
	preview, err := s.engine.AppPreview(parts[0])
	if errors.Is(err, workspace.ErrBrowserAppAsset) {
		writeBrowserAssetError(w, err)
		return true
	}
	if err != nil {
		writeMappedError(w, err)
		return true
	}
	if len(parts) == 2 {
		writeJSON(w, 200, preview)
		return true
	}
	if r.URL.Query().Get("digest") != preview.CandidateDigest {
		writeError(w, 409, "stale_candidate", "preview candidate changed")
		return true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", workspace.BrowserAppCSP+"; frame-ancestors 'self'; sandbox allow-scripts")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Write([]byte(preview.HTML))
	return true
}

func writeBrowserAssetError(w http.ResponseWriter, err error) {
	detail := "Preview requires a complete HTML document with local classic JavaScript and CSS assets; external assets, frames and modules are unsupported"
	var assetErr *workspace.BrowserAppAssetError
	if errors.As(err, &assetErr) && assetErr.Reason != "" {
		detail = "Unsupported browser application asset: " + assetErr.Reason + ". " + detail
	}
	writeError(w, 409, "unsupported_app_preview", detail)
}
