package httpapi

import (
	"foundry/internal/domain"
	"net/http"
)

func (s *server) sessionRenewalRoute(w http.ResponseWriter, r *http.Request, id string) {
	if s.sessionRenewal == nil || !s.codingWorkbench {
		writeError(w, 403, "session_renewal_unavailable", "desktop renewal unavailable")
		return
	}
	if domain.ValidateModelCredentialOperation(id) != nil || r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "validation", "invalid renewal route")
		return
	}
	if r.Method == http.MethodGet {
		status, err := s.sessionRenewal.Status(id)
		if err != nil {
			writeAccountError(w, err)
			return
		}
		writeJSON(w, 200, status)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 404, "not_found", "route not found")
		return
	}
	if s.recoveryReadOnly {
		writeError(w, 503, "recovery_read_only", "renewal disabled during recovery")
		return
	}
	var q struct {
		Action          string `json:"action"`
		AccountRevision int64  `json:"expected_account_revision"`
		Revision        int64  `json:"expected_revision"`
		MaterialID      string `json:"material_id"`
	}
	if err := decodeAccountJSON(w, r, &q); err != nil {
		writeDecodeError(w, err)
		return
	}
	var status domain.SessionRenewalStatus
	var err error
	switch q.Action {
	case "prepare":
		if q.AccountRevision < 1 || q.Revision != 0 || q.MaterialID != "" {
			writeError(w, 400, "validation", "invalid renewal preparation")
			return
		}
		_, err = s.sessionRenewal.Prepare(id, q.AccountRevision)
		if err == nil {
			status, err = s.sessionRenewal.Status(id)
		}
	case "approve":
		if q.Revision < 1 || q.AccountRevision != 0 || domain.ValidateModelCredentialOperation(q.MaterialID) != nil {
			writeError(w, 400, "validation", "invalid renewal approval")
			return
		}
		status, err = s.sessionRenewal.Approve(id, q.MaterialID, q.Revision)
	case "disable":
		if q.Revision < 1 || q.AccountRevision != 0 || q.MaterialID != "" {
			writeError(w, 400, "validation", "invalid renewal withdrawal")
			return
		}
		status, err = s.sessionRenewal.Disable(id, q.Revision)
	default:
		writeError(w, 400, "validation", "invalid renewal action")
		return
	}
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, 200, status)
}
