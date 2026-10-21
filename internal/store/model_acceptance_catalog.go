package store

import (
	"database/sql"
	"foundry/internal/domain"
	"reflect"
	"time"
)

func catalogPrerequisite(tx *sql.Tx, m domain.ModelAcceptanceMaterial) error {
	p, e := acceptanceTx(tx, m.Account.ID, m.PrerequisiteID)
	if e != nil || !validAcceptanceMaterial(p) || domain.AcceptanceDigest(p.Material) != p.MaterialDigest || p.Source != "production_connection" || p.Phase != "connection" || p.State != "succeeded" || p.Certainty != "completed" || p.Attempts != 1 || p.ClaimSequence < 1 || p.Material.Route != m.Route || p.ResultAccountRevision != m.Account.Revision || m.ConnectionID != p.ID {
		return ErrConflict
	}
	// Compare today's exact credential/account snapshot with the connection's
	// result using its own provenance. Its old process generation is not authority.
	comparison := m
	comparison.ProductionAdmission = p.Material.ProductionAdmission
	comparison.ConnectionPurpose = p.Material.ConnectionPurpose
	comparison.RequestedScopes = p.Material.RequestedScopes
	comparison.RequestedResource = p.Material.RequestedResource
	if p.ResultSnapshotDigest != acceptanceSnapshotDigest(comparison) {
		return ErrConflict
	}
	var latest string
	if e = tx.QueryRow(`SELECT o.id FROM model_acceptance_claims c JOIN model_acceptance_operations o ON o.id=c.operation_id WHERE o.account_id=? AND json_extract(o.data,'$.phase')='connection' ORDER BY c.sequence DESC LIMIT 1`, m.Account.ID).Scan(&latest); e != nil || latest != p.ID {
		return ErrConflict
	}
	var sequence int64
	if e = tx.QueryRow(`SELECT claim_sequence FROM model_acceptance_dispatches WHERE operation_id=?`, p.ID).Scan(&sequence); e != nil || sequence != p.ClaimSequence {
		return ErrConflict
	}
	return nil
}

func (s *Store) ProposeProductionCatalog(account string, q domain.ModelAcceptanceProposal, admission domain.ProductionAdmission, now time.Time) (domain.ModelAcceptanceOperation, error) {
	if !admission.Valid() || admission.Profile != "openai_siwc_catalog_v1" || q.Phase != "catalog" || q.ConnectionPurpose != "" || q.Model != "" || q.PrerequisiteID == "" {
		return domain.ModelAcceptanceOperation{}, ErrConflict
	}
	return s.proposeModelAcceptance(account, q, "", &admission, now)
}

// ProductionCatalogReadiness reads only metadata, including current lineage.
func (s *Store) ProductionCatalogReadiness(account, route string, admission domain.ProductionAdmission, now time.Time) string {
	if !admission.Valid() || admission.Profile != "openai_siwc_catalog_v1" {
		return "admission_unavailable"
	}
	tx, e := s.db.Begin()
	if e != nil {
		return "admission_unavailable"
	}
	defer tx.Rollback()
	var expiry, state string
	if e = tx.QueryRow(`SELECT expires_at,state FROM model_authorizations WHERE account_id=? ORDER BY rowid DESC LIMIT 1`, account).Scan(&expiry, &state); e != nil || state != "available" {
		return "authorization_unavailable"
	}
	expiration, e := time.Parse(time.RFC3339Nano, expiry)
	if e != nil {
		return "authorization_unavailable"
	}
	if !expiration.After(now) {
		return "authorization_expired"
	}
	m, e := acceptanceGateSnapshot(tx, account, route, true, "", &admission, now)
	if e != nil {
		return "authorization_unavailable"
	}
	if e = tx.QueryRow(`SELECT o.id FROM model_acceptance_claims c JOIN model_acceptance_operations o ON o.id=c.operation_id WHERE o.account_id=? AND json_extract(o.data,'$.phase')='connection' ORDER BY c.sequence DESC LIMIT 1`, account).Scan(&m.PrerequisiteID); e != nil {
		return "prerequisite_unavailable"
	}
	m.ConnectionID = m.PrerequisiteID
	if catalogPrerequisite(tx, m) != nil {
		return "prerequisite_unavailable"
	}
	return ""
}

// validateSingleRequestClaim is shared by the independently admitted catalog and
// inference controllers; each public entry point still pins its own profile.
func validateSingleRequestClaim(tx *sql.Tx, claim domain.ModelAcceptanceOperation, admission domain.ProductionAdmission, now time.Time) error {
	o, e := acceptanceTx(tx, claim.AccountID, claim.ID)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(o, claim) || !validAcceptanceMaterial(o) || (o.Source != "production_catalog" && o.Source != "production_inference") || o.State != "running" || o.Attempts != 1 || o.ClaimSequence < 1 || o.Material.ProductionAdmission == nil || *o.Material.ProductionAdmission != admission || domain.AcceptanceDigest(o.Material) != o.MaterialDigest {
		return ErrConflict
	}
	expiry, e := time.Parse(time.RFC3339Nano, o.Material.ExpiresAt)
	if e != nil || !expiry.After(now) {
		return ErrConflict
	}
	started, e := time.Parse(time.RFC3339Nano, o.StartedAt)
	if e != nil || now.Before(started) || !started.Add(30*time.Second).After(now) {
		return ErrConflict
	}
	var sequence int64
	if e = tx.QueryRow(`SELECT sequence FROM model_acceptance_claims WHERE operation_id=?`, o.ID).Scan(&sequence); e != nil || sequence != o.ClaimSequence {
		return ErrConflict
	}
	current, e := acceptanceGateSnapshot(tx, o.AccountID, o.Material.Route.ID, true, "", &admission, now)
	if e != nil {
		return e
	}
	if acceptanceSnapshotDigest(current) != acceptanceSnapshotDigest(o.Material) {
		return ErrConflict
	}
	return acceptancePrerequisite(tx, o.Material)
}

func (s *Store) ReserveProductionCatalogDispatch(claim domain.ModelAcceptanceOperation, admission domain.ProductionAdmission, now time.Time) error {
	if claim.Source != "production_catalog" || admission.Profile != "openai_siwc_catalog_v1" {
		return ErrConflict
	}
	return s.reserveProductionDispatch(claim, admission, now)
}

func (s *Store) reserveProductionDispatch(claim domain.ModelAcceptanceOperation, admission domain.ProductionAdmission, now time.Time) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`UPDATE model_acceptance_operations SET revision=revision WHERE id=?`, claim.ID); e != nil {
		return e
	}
	if e = validateSingleRequestClaim(tx, claim, admission, now); e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO model_acceptance_dispatches(operation_id,claim_sequence) VALUES(?,?)`, claim.ID, claim.ClaimSequence); e != nil {
		return ErrConflict
	}
	return tx.Commit()
}

func (s *Store) ValidateProductionCatalogDispatch(claim domain.ModelAcceptanceOperation, admission domain.ProductionAdmission, now time.Time) error {
	if claim.Source != "production_catalog" || admission.Profile != "openai_siwc_catalog_v1" {
		return ErrConflict
	}
	return s.validateProductionDispatch(claim, admission, now)
}

func (s *Store) validateProductionDispatch(claim domain.ModelAcceptanceOperation, admission domain.ProductionAdmission, now time.Time) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = validateSingleRequestClaim(tx, claim, admission, now); e != nil {
		return e
	}
	var sequence int64
	if e = tx.QueryRow(`SELECT claim_sequence FROM model_acceptance_dispatches WHERE operation_id=?`, claim.ID).Scan(&sequence); e != nil || sequence != claim.ClaimSequence {
		return ErrConflict
	}
	return nil
}

func (s *Store) FinishProductionCatalog(claim domain.ModelAcceptanceOperation, state, certainty, code string, catalog []domain.ModelAcceptanceCatalogEntry, now time.Time) (domain.ModelAcceptanceOperation, error) {
	if claim.Source != "production_catalog" || !validAcceptanceMaterial(claim) {
		return domain.ModelAcceptanceOperation{}, ErrConflict
	}
	return s.finishModelAcceptance(claim.AccountID, claim.ID, claim.Revision, state, certainty, code, catalog, 0, 0, false, nil, now, &claim)
}
