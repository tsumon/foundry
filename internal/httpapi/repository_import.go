package httpapi

import (
	"errors"
	"foundry/internal/domain"
	"net/http"
)

func (s *server) repositoryInspections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := s.store.ListRepositoryInspections()
		if err != nil {
			writeError(w, 500, "storage_failed", "inspection history unavailable")
			return
		}
		writeJSON(w, 200, rows)
	case http.MethodPost:
		var request domain.RepositoryInspectionRequest
		if err := decodeJSON(w, r, &request); err != nil {
			writeDecodeError(w, err)
			return
		}
		v, err := s.engine.InspectRepository(r.Context(), request)
		if err != nil {
			status := 400
			code := domain.ImportInvalidRequest
			switch {
			case errors.Is(err, domain.ImportConsentRequired):
				code = domain.ImportConsentRequired
			case errors.Is(err, domain.ImportSourceChanged):
				status = 409
				code = domain.ImportSourceChanged
			case errors.Is(err, domain.ImportStorageFailed):
				status = 500
				code = domain.ImportStorageFailed
			case errors.Is(err, domain.ImportTimeout):
				status = 408
				code = domain.ImportTimeout
			case errors.Is(err, domain.ImportInspectionFailed):
				status = 500
				code = domain.ImportInspectionFailed
			}
			writeError(w, status, string(code), "repository inspection request refused")
			return
		}
		writeJSON(w, 200, v)
	default:
		writeError(w, 404, "not_found", "route not found")
	}
}
