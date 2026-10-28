package httpapi

import (
	"errors"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"net/http"
)

func (s *server) codingDependencies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 404, "not_found", "route not found")
		return
	}
	if !s.codingWorkbench || !s.engine.CodingWorkbenchAvailable() {
		writeError(w, 403, "coding_workbench_unavailable", "controlled coding workbench is unavailable")
		return
	}
	var q engine.CodingDependencyImportRequest
	if err := decodeCodingJSON(w, r, &q); err != nil {
		writeDecodeError(w, err)
		return
	}
	result, err := s.engine.ImportCodingDependencies(r.Context(), q)
	if err != nil {
		status := 400
		code := domain.ImportDependencies
		for _, c := range []domain.ImportError{domain.ImportConsentRequired, domain.ImportInvalidRequest, domain.ImportSourceChanged, domain.ImportLocation, domain.ImportUnavailable, domain.ImportStorageFailed} {
			if errors.Is(err, c) {
				code = c
			}
		}
		if code == domain.ImportSourceChanged {
			status = 409
		}
		if code == domain.ImportStorageFailed {
			status = 500
		}
		writeError(w, status, string(code), "local dependency import refused")
		return
	}
	writeJSON(w, 200, result)
}
