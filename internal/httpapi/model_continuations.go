package httpapi

import (
	"errors"
	"foundry/internal/domain"
	"foundry/internal/store"
	"net/http"
	"time"
)

// Task-scoped metadata only. Identity-bearing account-management DTOs must not
// enter the continuation renderer, even when it would hide those fields.
type modelContinuationCandidate struct {
	ID         string `json:"id"`
	AccountID  string `json:"account_id"`
	State      string `json:"state"`
	Generation int64  `json:"generation"`
	ExpiresAt  string `json:"expires_at"`
}

func (s *server) modelContinuationCandidates(w http.ResponseWriter, r *http.Request, missionID string) {
	if r.Method != "GET" {
		writeError(w, 404, "not_found", "route not found")
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, 400, "validation", "query parameters and request bodies are not supported")
		return
	}
	m, err := s.store.GetMission(missionID)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	snapshot, err := domain.EffectiveModelSnapshot(m)
	if err != nil || snapshot == nil {
		writeError(w, 409, "conflict", "valid task model authority required")
		return
	}
	views := []modelContinuationCandidate{}
	seen := map[string]bool{}
	for _, role := range domain.RolePolicyRoles() {
		selection := snapshot.Roles[role]
		if selection.Authorization == nil || selection.Route == nil || selection.Route.Adapter != "openai_siwc_responses" || seen[selection.Profile.AccountID] {
			continue
		}
		accountID := selection.Profile.AccountID
		seen[accountID] = true
		grant, err := s.store.CurrentModelAuthorization(accountID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			writeMappedError(w, err)
			return
		}
		views = append(views, modelContinuationCandidate{ID: grant.Binding.ID, AccountID: accountID, State: grant.State, Generation: grant.Generation, ExpiresAt: grant.ExpiresAt})
	}
	writeJSON(w, 200, map[string]any{"authorizations": views})
}

type modelContinuationView struct {
	ID                      string                        `json:"id"`
	Digest                  string                        `json:"digest"`
	State                   string                        `json:"state"`
	PreparedRevision        int64                         `json:"prepared_revision"`
	PredecessorDigest       string                        `json:"predecessor_digest"`
	ReplacementDigest       string                        `json:"replacement_digest"`
	AuthorizationID         string                        `json:"authorization_id"`
	AuthorizationGeneration int64                         `json:"authorization_generation"`
	AuthorizationExpiresAt  string                        `json:"authorization_expires_at"`
	CatalogOperationID      string                        `json:"catalog_operation_id"`
	AffectedRoles           []domain.Role                 `json:"affected_roles"`
	CreatedAt               string                        `json:"created_at"`
	ExpiresAt               string                        `json:"expires_at"`
	Approval                *domain.ModelContinuationFact `json:"approval,omitempty"`
	Application             *domain.ModelContinuationFact `json:"application,omitempty"`
}

func modelContinuationViews(m domain.Mission) []modelContinuationView {
	views := make([]modelContinuationView, 0, len(m.ModelContinuations))
	for _, c := range m.ModelContinuations {
		state := "prepared"
		expected := c.PreparedRevision + 1
		if c.Approval != nil {
			state = "approved"
			expected++
		}
		expiry, err := time.Parse(time.RFC3339Nano, c.ExpiresAt)
		if m.Revision != expected {
			state = "stale"
		}
		if err != nil || !expiry.After(time.Now()) {
			state = "expired"
		}
		if c.Application != nil {
			state = "applied"
		}
		v := modelContinuationView{ID: c.ID, Digest: c.Digest, State: state, PreparedRevision: c.PreparedRevision, PredecessorDigest: c.PredecessorDigest, ReplacementDigest: c.Replacement.Digest, AuthorizationGeneration: c.AuthorizationGeneration, AuthorizationExpiresAt: c.AuthorizationExpiresAt, AffectedRoles: c.AffectedRoles, CreatedAt: c.CreatedAt, ExpiresAt: c.ExpiresAt, Approval: c.Approval, Application: c.Application}
		if len(c.AffectedRoles) > 0 {
			sel := c.Replacement.Roles[c.AffectedRoles[0]]
			if sel.Authorization != nil {
				v.AuthorizationID = sel.Authorization.ID
			}
			if sel.Catalog != nil {
				v.CatalogOperationID = sel.Catalog.OperationID
			}
		}
		views = append(views, v)
	}
	return views
}

func (s *server) modelContinuationRoute(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 2 || parts[1] != "model-continuations" {
		return false
	}
	if len(parts) == 3 && parts[2] == "candidates" {
		s.modelContinuationCandidates(w, r, parts[0])
		return true
	}
	if r.Method != "POST" || (len(parts) != 2 && len(parts) != 4) || (len(parts) == 4 && (parts[2] == "" || (parts[3] != "approve" && parts[3] != "apply"))) {
		writeError(w, 404, "not_found", "route not found")
		return true
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "validation", "query parameters are not supported")
		return true
	}
	var m domain.Mission
	var err error
	if len(parts) == 2 {
		var q struct {
			Revision           int64  `json:"revision"`
			AuthorizationID    string `json:"authorization_id"`
			CatalogOperationID string `json:"catalog_operation_id"`
		}
		if err = decodeMCPAction(w, r, &q, "revision", "authorization_id", "catalog_operation_id"); err != nil {
			writeDecodeError(w, err)
			return true
		}
		if q.Revision < 1 || domain.ValidateModelCredentialOperation(q.AuthorizationID) != nil || domain.ValidateModelCredentialOperation(q.CatalogOperationID) != nil {
			writeError(w, 400, "validation", "exact revision and recorded authorization/catalog IDs required")
			return true
		}
		m, err = s.engine.PrepareModelContinuation(r.Context(), parts[0], q.Revision, q.AuthorizationID, q.CatalogOperationID)
	} else {
		var q struct {
			Revision int64  `json:"revision"`
			Digest   string `json:"digest"`
		}
		if err = decodeMCPAction(w, r, &q, "revision", "digest"); err != nil {
			writeDecodeError(w, err)
			return true
		}
		if q.Revision < 1 || len(q.Digest) != 64 {
			writeError(w, 400, "validation", "exact revision and material digest required")
			return true
		}
		if parts[3] == "approve" {
			m, err = s.engine.ApproveModelContinuation(r.Context(), parts[0], parts[2], q.Revision, q.Digest)
		} else {
			m, err = s.engine.ApplyModelContinuation(r.Context(), parts[0], parts[2], q.Revision, q.Digest)
		}
	}
	if err != nil {
		writeMappedError(w, err)
	} else {
		s.writeMission(w, m, nil)
	}
	return true
}
