package httpapi

import (
	"errors"
	"foundry/internal/acceptance"
	"foundry/internal/domain"
	"foundry/internal/store"
	"net/http"
)

type acceptanceRouteView struct {
	ID                string `json:"id"`
	Adapter           string `json:"adapter"`
	Environment       string `json:"environment"`
	SpendClass        string `json:"spend_class"`
	BaseURL           string `json:"base_url"`
	ConnectionPurpose string `json:"connection_purpose,omitempty"`
	ConnectionMode    string `json:"connection_mode,omitempty"`
}

// legacySIWCSimulationEligible is deliberately independent from acceptance
// readiness: non-gate synthetic SIWC fixtures retain their existing local
// controls, while owned acceptance accounts can never bypass their gate.
func (s *server) legacySIWCSimulationEligible(accountID string) bool {
	if s.recoveryReadOnly || s.acceptance.OwnsAccount(accountID) {
		return false
	}
	account, err := s.store.GetModelAccount(accountID)
	return err == nil && s.siwc.LegacySimulationEligible(account)
}

func (s *server) rejectAcceptanceBypass(w http.ResponseWriter, account string) bool {
	if s.acceptance.OwnsAccount(account) {
		writeError(w, 403, "acceptance_gate_required", "this account requires a separate acceptance approval")
		return true
	}
	return false
}
func writeAcceptanceError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, acceptance.ErrDisabled):
		writeError(w, 403, "acceptance_simulation_disabled", "acceptance is unavailable for this account or phase")
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
				routes = append(routes, acceptanceRouteView{v.ID, v.Adapter, v.Environment, v.SpendClass, v.BaseURL, s.acceptance.ConnectionPurpose(v.ID), s.acceptance.ConnectionMode(v.ID)})
			}
			production := s.acceptance.ProductionConnectionStatus(account)
			payload := map[string]any{"operations": items, "simulation_enabled": s.acceptance.SimulationEligible(account) && !s.recoveryReadOnly, "legacy_siwc_simulation_enabled": s.legacySIWCSimulationEligible(account), "routes": routes, "production_connection_enabled": production.Enabled && !s.recoveryReadOnly}
			if production.Admission != nil {
				payload["production_admission"] = production.Admission
			}
			writeJSON(w, 200, payload)
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
		o, session, e := s.acceptance.Read(account, parts[2])
		if e != nil {
			writeAcceptanceError(w, e)
			return
		}
		writeAcceptanceOperation(w, o, session)
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
		case "execute", "cancel":
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
			if parts[3] == "cancel" {
				o, e = s.acceptance.Cancel(account, parts[2], q.ExpectedRevision, q.MaterialDigest)
			} else {
				o, e = s.acceptance.Execute(r.Context(), account, parts[2], q.ExpectedRevision, q.MaterialDigest)
			}
		default:
			writeError(w, 404, "not_found", "route not found")
			return
		}
		if e != nil {
			writeAcceptanceError(w, e)
			return
		}
		// Re-read the pair under the controller's completion lock so a fast
		// callback cannot combine a terminal receipt with a usable link.
		o, session, e := s.acceptance.Read(account, o.ID)
		if e != nil {
			writeAcceptanceError(w, e)
			return
		}
		writeAcceptanceOperation(w, o, session)
		return
	}
	writeError(w, 404, "not_found", "route not found")
}

func writeAcceptanceOperation(w http.ResponseWriter, o domain.ModelAcceptanceOperation, session *acceptance.ConnectionSession) {
	payload := map[string]any{"operation": o}
	if session != nil {
		payload["connection_session"] = session
	}
	writeJSON(w, 200, payload)
}
