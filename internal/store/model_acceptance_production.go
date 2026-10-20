package store

import (
	"foundry/internal/domain"
	"reflect"
	"time"
)

func (s *Store) initializeProductionConnectionDispatch() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS model_acceptance_dispatches(operation_id TEXT PRIMARY KEY REFERENCES model_acceptance_operations(id),claim_sequence INTEGER NOT NULL UNIQUE REFERENCES model_acceptance_claims(sequence));
CREATE TRIGGER IF NOT EXISTS acceptance_dispatch_no_update BEFORE UPDATE ON model_acceptance_dispatches BEGIN SELECT RAISE(ABORT,'immutable acceptance dispatch'); END;
CREATE TRIGGER IF NOT EXISTS acceptance_dispatch_no_delete BEFORE DELETE ON model_acceptance_dispatches BEGIN SELECT RAISE(ABORT,'immutable acceptance dispatch'); END;`)
	return err
}

// ProposeProductionConnection is the dedicated trusted-controller entry point.
// Admission metadata is provenance, never an independently dispatchable permit.
// Generic simulation proposal helpers cannot construct live material.
func (s *Store) ProposeProductionConnection(account string, q domain.ModelAcceptanceProposal, admission domain.ProductionAdmission, now time.Time) (domain.ModelAcceptanceOperation, error) {
	if !admission.Valid() || q.Phase != "connection" || q.ConnectionPurpose != "subscription_only" {
		return domain.ModelAcceptanceOperation{}, ErrConflict
	}
	return s.proposeModelAcceptance(account, q, "browser_handoff", &admission, now)
}

// FinishProductionConnection binds the receipt to the original running claim,
// including its process generation and digest, in the receipt transaction.
func (s *Store) FinishProductionConnection(claim domain.ModelAcceptanceOperation, state, certainty, code string, now time.Time) (domain.ModelAcceptanceOperation, error) {
	if claim.Material.ProductionAdmission == nil || !validAcceptanceMaterial(claim) {
		return domain.ModelAcceptanceOperation{}, ErrConflict
	}
	return s.finishModelAcceptance(claim.AccountID, claim.ID, claim.Revision, state, certainty, code, nil, 0, 0, false, now, &claim)
}

// ReserveProductionConnectionDispatch independently checks the current durable
// claim and snapshot in the transaction that elects its sole adapter dispatch.
// No replay is possible even if a process dies before the first protocol effect.
func (s *Store) ReserveProductionConnectionDispatch(claim domain.ModelAcceptanceOperation, admission domain.ProductionAdmission, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE model_acceptance_operations SET revision=revision WHERE id=? AND account_id=?`, claim.ID, claim.AccountID); err != nil {
		return err
	}
	o, err := acceptanceTx(tx, claim.AccountID, claim.ID)
	if err != nil {
		return err
	}
	if !admission.Valid() || !reflect.DeepEqual(o, claim) || o.State != "running" || o.Attempts != 1 || o.ClaimSequence < 1 ||
		!validAcceptanceMaterial(o) || domain.AcceptanceDigest(o.Material) != o.MaterialDigest || o.Material.ProductionAdmission == nil || *o.Material.ProductionAdmission != admission {
		return ErrConflict
	}
	started, err := time.Parse(time.RFC3339Nano, o.StartedAt)
	if err != nil || now.Before(started) || !started.Add(600*time.Second).After(now) {
		return ErrConflict
	}
	var sequence int64
	if err = tx.QueryRow(`SELECT sequence FROM model_acceptance_claims WHERE operation_id=?`, o.ID).Scan(&sequence); err != nil {
		return err
	}
	if sequence != o.ClaimSequence {
		return ErrConflict
	}
	current, err := acceptanceGateSnapshot(tx, o.AccountID, o.Material.Route.ID, false, o.Material.ConnectionPurpose, o.Material.ProductionAdmission, now)
	if err != nil {
		return err
	}
	if acceptanceSnapshotDigest(current) != acceptanceSnapshotDigest(o.Material) {
		return ErrConflict
	}
	if _, err = tx.Exec(`INSERT INTO model_acceptance_dispatches(operation_id,claim_sequence) VALUES(?,?)`, o.ID, o.ClaimSequence); err != nil {
		return ErrConflict
	}
	return tx.Commit()
}
