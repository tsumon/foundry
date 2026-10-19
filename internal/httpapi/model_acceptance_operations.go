package httpapi

import (
	"errors"
	"foundry/internal/acceptance"
	"foundry/internal/domain"
	"foundry/internal/store"
	"net/http"
)

type acceptanceRouteView struct {
	ID          string `json:"id"`
	Adapter     string `json:"adapter"`
	Environment string `json:"environment"`
	SpendClass  string `json:"spend_class"`
	BaseURL     string `json:"base_url"`
}

func (s *server) rejectAcceptanceBypass(w http.ResponseWriter, account string) bool {
	if s.acceptance.OwnsAccount(account) {
		writeError(w, 403, "acceptance_gate_required", "this synthetic account requires a separate acceptance approval")
		return true
	}
	return false
}
func writeAcceptanceError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, acceptance.ErrDisabled):
		writeError(w, 403, "acceptance_simulation_disabled", "only registered local simulation is permitted")
	case errors.Is(e, store.ErrConflict):
		writeError(w, 409, "acceptance_conflict", "acceptance material or state is no longer current")
	case errors.Is(e, store.ErrNotFound):
		writeError(w, 404, "not_found", "acceptance operation not found")
	default:
		writeError(w, 503, "acceptance_record_unavailable", "acceptance evidence could not be read or recorded")
	}
}
func (s *server) modelAcceptanceOperations(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < 2 || len(parts) > 4 || r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "validation", "invalid acceptance route")
		return
	}
	for i, p := range parts {
		if i == 1 {
			continue
		}
		if domain.ValidateModelCredentialOperation(p) != nil {
			writeError(w, 400, "validation", "invalid acceptance identifier")
			return
		}
	}
	account := parts[0]
	if len(parts) == 2 {
		if r.Method == http.MethodGet {
			items, e := s.acceptance.List(account)
			if e != nil {
				writeAcceptanceError(w, e)
				return
			}
			routes := []acceptanceRouteView{}
			for _, v := range s.acceptance.Routes(account) {
				routes = append(routes, acceptanceRouteView{v.ID, v.Adapter, v.Environment, v.SpendClass, v.BaseURL})
			}
			writeJSON(w, 200, map[string]any{"operations": items, "simulation_enabled": s.acceptance.SimulationEligible(account) && !s.recoveryReadOnly, "routes": routes})
			return
		}
		if r.Method == http.MethodPost {
			var q domain.ModelAcceptanceProposal
			if e := decodeAccountJSON(w, r, &q); e != nil {
				writeDecodeError(w, e)
				return
			}
			if q.Phase != "connection" && q.Phase != "catalog" && q.Phase != "inference" || domain.ValidateModelCredentialOperation(q.RouteID) != nil || q.ExpectedAccountRevision < 1 || q.PrerequisiteID != "" && domain.ValidateModelCredentialOperation(q.PrerequisiteID) != nil || q.Model != "" && domain.ValidateModelCredentialOperation(q.Model) != nil {
				writeError(w, 400, "validation", "invalid acceptance proposal")
				return
			}
			o, e := s.acceptance.Propose(account, q)
			if e != nil {
				writeAcceptanceError(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"operation": o})
			return
		}
	}
	if len(parts) == 3 && r.Method == http.MethodGet {
		o, e := s.acceptance.Get(account, parts[2])
		if e != nil {
			writeAcceptanceError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"operation": o})
		return
	}
	if len(parts) == 4 && r.Method == http.MethodPost {
		var o domain.ModelAcceptanceOperation
		var e error
		switch parts[3] {
		case "decision":
			var q struct {
				ExpectedRevision int64  `json:"expected_revision"`
				MaterialDigest   string `json:"material_digest"`
				Decision         string `json:"decision"`
			}
			if e = decodeAccountJSON(w, r, &q); e != nil {
				writeDecodeError(w, e)
				return
			}
			if q.ExpectedRevision < 1 || len(q.MaterialDigest) != 64 || (q.Decision != "approve" && q.Decision != "reject") {
				writeError(w, 400, "validation", "invalid acceptance decision")
				return
			}
			o, e = s.acceptance.Decide(account, parts[2], q.ExpectedRevision, q.MaterialDigest, q.Decision)
		case "execute":
			var q struct {
				ExpectedRevision int64  `json:"expected_revision"`
				MaterialDigest   string `json:"material_digest"`
			}
			if e = decodeAccountJSON(w, r, &q); e != nil {
				writeDecodeError(w, e)
				return
			}
			if q.ExpectedRevision < 1 || len(q.MaterialDigest) != 64 {
				writeError(w, 400, "validation", "invalid acceptance execution")
				return
			}
			o, e = s.acceptance.Execute(r.Context(), account, parts[2], q.ExpectedRevision, q.MaterialDigest)
		default:
			writeError(w, 404, "not_found", "route not found")
			return
		}
		if e != nil {
			writeAcceptanceError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"operation": o})
		return
	}
	writeError(w, 404, "not_found", "route not found")
}
