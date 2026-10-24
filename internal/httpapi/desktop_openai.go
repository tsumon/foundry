package httpapi

import (
	"foundry/internal/domain"
	"net/http"
)

func (s *server) desktopOwnsSubscription(account string) bool {
	if s.desktopAccounts == nil {
		return false
	}
	a, e := s.store.GetModelAccount(account)
	return e == nil && a.Provider == "openai" && a.AuthMethod == "openai_siwc"
}

func (s *server) desktopPreparation(w http.ResponseWriter, r *http.Request, account string) {
	if s.desktopAccounts == nil || !s.codingWorkbench || s.recoveryReadOnly {
		writeError(w, 403, "desktop_subscription_unavailable", "Mac coding desktop preparation unavailable")
		return
	}
	if domain.ValidateModelCredentialOperation(account) != nil || r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "validation", "invalid preparation route")
		return
	}
	if r.Method == http.MethodPost {
		var q struct {
			Phase    string `json:"phase"`
			RouteID  string `json:"route_id"`
			Revision int64  `json:"expected_account_revision"`
		}
		if e := decodeAccountJSON(w, r, &q); e != nil {
			writeDecodeError(w, e)
			return
		}
		if q.Revision < 1 || domain.ValidateModelCredentialOperation(q.RouteID) != nil || (q.Phase != "connection" && q.Phase != "catalog") {
			writeError(w, 400, "validation", "invalid preparation")
			return
		}
		if e := s.desktopAccounts.Prepare(account, q.Phase, q.RouteID, q.Revision); e != nil {
			writeAcceptanceError(w, e)
			return
		}
	} else if r.Method != http.MethodGet {
		writeError(w, 404, "not_found", "route not found")
		return
	}
	choices, e := s.store.DesktopCatalogChoices(account)
	if e != nil {
		writeAccountError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"preparation": s.desktopAccounts.Status(account), "catalog_choices": choices})
}

// The Electron main process validates the proposed link using this authenticated
// metadata-only endpoint. It is not a generic localhost external-link grant.
func (s *server) desktopHandoff(w http.ResponseWriter, r *http.Request) {
	if s.desktopAccounts == nil || !s.codingWorkbench || s.recoveryReadOnly {
		writeError(w, 403, "desktop_subscription_unavailable", "handoff unavailable")
		return
	}
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "validation", "invalid handoff")
		return
	}
	var q struct {
		URL string `json:"url"`
	}
	if e := decodeAccountJSON(w, r, &q); e != nil {
		writeDecodeError(w, e)
		return
	}
	if !s.desktopAccounts.Handoff(q.URL) {
		writeError(w, 403, "handoff_unavailable", "no current waiting session")
		return
	}
	writeJSON(w, 200, map[string]bool{"allowed": true})
}
