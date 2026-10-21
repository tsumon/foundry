package httpapi

import (
	"errors"
	"foundry/internal/store"
	"net/http"
)

func (s *server) recoveryDiagnostic(w http.ResponseWriter, r *http.Request, id string) {
	diagnostic, err := s.engine.DiagnoseRecovery(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "mission not found")
		} else {
			writeError(w, http.StatusInternalServerError, "recovery_diagnostic_unavailable", "mission records could not be read within diagnostic bounds")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"diagnostic": diagnostic})
}
