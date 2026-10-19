package httpapi

import "net/http"

func (s *server) modelAcceptance(w http.ResponseWriter, r *http.Request, accountID string) {
	report, err := s.store.ReadModelAccountAcceptance(r.Context(), accountID)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"acceptance": report})
}
