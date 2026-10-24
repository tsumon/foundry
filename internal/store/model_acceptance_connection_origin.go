package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"time"
)

func (s *Store) initializeConnectionOrigins() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS model_acceptance_connection_origins (
 operation_id TEXT PRIMARY KEY REFERENCES model_acceptance_operations(id), authorization BLOB NOT NULL);
 CREATE TRIGGER IF NOT EXISTS acceptance_origin_no_update BEFORE UPDATE ON model_acceptance_connection_origins BEGIN SELECT RAISE(ABORT,'immutable connection origin'); END;
 CREATE TRIGGER IF NOT EXISTS acceptance_origin_no_delete BEFORE DELETE ON model_acceptance_connection_origins BEGIN SELECT RAISE(ABORT,'immutable connection origin'); END;`)
	return err
}

// This side record retains only the exact nonsecret result authorization. It
// neither changes the connection receipt nor establishes authority on its own.
func recordConnectionOrigin(tx *sql.Tx, id string, a *domain.ModelAuthorization) error {
	if a == nil || a.State != "available" || a.Generation < 1 || a.Binding.SpendClass != "subscription_only" || domain.ValidateModelAuthorizationBinding(a.Binding) != nil {
		return ErrConflict
	}
	raw, err := json.Marshal(a) // SecretRef is explicitly excluded by the domain type.
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO model_acceptance_connection_origins(operation_id,authorization) VALUES(?,?) ON CONFLICT(operation_id) DO NOTHING`, id, raw)
	return err
}

// A legacy connection can be anchored only before rotation and only when its
// complete result digest is still reproducible. Unverifiable history stays
// unanchored; it must not prevent otherwise authorized credential maintenance.
func anchorCurrentConnection(tx *sql.Tx, account string) error {
	var id string
	err := tx.QueryRow(`SELECT o.id FROM model_acceptance_claims c JOIN model_acceptance_operations o ON o.id=c.operation_id WHERE o.account_id=? AND json_extract(o.data,'$.phase')='connection' ORDER BY c.sequence DESC LIMIT 1`, account).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	p, err := acceptanceTx(tx, account, id)
	if err != nil {
		return err
	}
	if !validAcceptanceMaterial(p) || domain.AcceptanceDigest(p.Material) != p.MaterialDigest || p.Source != "production_connection" || p.State != "succeeded" || p.Certainty != "completed" || p.Code != "" || p.Attempts != 1 || p.ClaimSequence < 1 {
		return nil
	}
	// This is historical proof, not a catalog admission. Access expiry does not
	// erase exact metadata; do not invent a time to pass a freshness gate.
	m := p.Material
	m.Account, err = modelAccountTx(tx, account)
	if err != nil {
		return err
	}
	m.Route, err = modelRouteTx(tx, p.Material.Route.ID)
	if err != nil {
		return err
	}
	a, err := currentModelAuthorizationTx(tx, account)
	if err != nil {
		return err
	}
	m.CredentialVersion, m.Authorization = m.Account.CurrentCredentialVersion, &a
	if p.ResultSnapshotDigest != acceptanceSnapshotDigest(m) {
		return nil
	}
	var sequence int64
	if err = tx.QueryRow(`SELECT claim_sequence FROM model_acceptance_dispatches WHERE operation_id=?`, id).Scan(&sequence); err != nil || sequence != p.ClaimSequence {
		return nil
	}
	return recordConnectionOrigin(tx, id, m.Authorization)
}

// Only the connection prerequisite may traverse renewal lineage. Catalog
// approvals, catalog results and task snapshots still pin one exact generation.
func renewedConnectionOrigin(tx *sql.Tx, p domain.ModelAcceptanceOperation, comparison domain.ModelAcceptanceMaterial) error {
	current := comparison.Authorization
	if current == nil || current.State != "available" || current.Binding.SpendClass != "subscription_only" {
		return ErrConflict
	}
	var raw []byte
	if err := tx.QueryRow(`SELECT authorization FROM model_acceptance_connection_origins WHERE operation_id=?`, p.ID).Scan(&raw); err != nil {
		return ErrConflict
	}
	var origin domain.ModelAuthorization
	if json.Unmarshal(raw, &origin) != nil || origin.State != "available" || origin.Generation < 1 || origin.Generation >= current.Generation || !domain.SameModelAuthorizationBinding(origin.Binding, current.Binding) || domain.ValidateModelAuthorizationBinding(origin.Binding) != nil {
		return ErrConflict
	}
	comparison.Authorization = &origin
	if p.ResultSnapshotDigest != acceptanceSnapshotDigest(comparison) {
		return ErrConflict
	}
	var pending int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM model_authorization_operations WHERE account_id=? AND (cleanup_pending=1 OR write_claimed=1 OR state='pending')`, p.AccountID).Scan(&pending); err != nil || pending != 0 {
		return ErrConflict
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM session_renewal_dispatches WHERE account_id=? AND (stage='dispatched' OR (authorization_id=? AND stage NOT IN('succeeded','waiting')))`, p.AccountID, origin.Binding.ID).Scan(&pending); err != nil || pending != 0 {
		return ErrConflict
	}
	// Count first to bound work by actual persisted history, not an untrusted
	// generation delta. Every successor must be a committed refresh, with every
	// retired predecessor clean and revoked and only the tip still available.
	var count int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM session_renewal_dispatches WHERE authorization_id=? AND generation>=? AND generation<?`, origin.Binding.ID, origin.Generation, current.Generation).Scan(&count); err != nil || count != current.Generation-origin.Generation {
		return ErrConflict
	}
	for generation := origin.Generation; generation <= current.Generation; generation++ {
		op, err := scanAuthorizationOperation(tx.QueryRow(`SELECT `+authorizationOperationColumns+` FROM model_authorization_operations WHERE authorization_id=? AND generation=?`, origin.Binding.ID, generation))
		if err != nil || op.AccountID != p.AccountID || op.CleanupPending || op.WriteClaimed || (generation > origin.Generation && op.Kind != "refresh") || (generation == origin.Generation && op.Kind != "sign_in" && op.Kind != "refresh") {
			return ErrConflict
		}
		if generation == current.Generation {
			var secretRef string
			if err = tx.QueryRow(`SELECT secret_ref FROM model_authorizations WHERE id=? AND account_id=? AND generation=? AND state='available'`, origin.Binding.ID, p.AccountID, generation).Scan(&secretRef); err != nil || op.State != "available" || op.SecretRef != secretRef {
				return ErrConflict
			}
			break
		}
		if op.State != "revoked" {
			return ErrConflict
		}
		var account, stage, category, certainty, started, finished string
		var successor, revision int64
		if err = tx.QueryRow(`SELECT account_id,stage,category,certainty,new_generation,policy_revision,started_at,finished_at FROM session_renewal_dispatches WHERE authorization_id=? AND generation=?`, origin.Binding.ID, generation).Scan(&account, &stage, &category, &certainty, &successor, &revision, &started, &finished); err != nil || account != p.AccountID || stage != "succeeded" || category != "" || certainty != "confirmed" || successor != generation+1 || revision < 1 {
			return ErrConflict
		}
		start, se := time.Parse(time.RFC3339Nano, started)
		finish, fe := time.Parse(time.RFC3339Nano, finished)
		if se != nil || fe != nil || finish.Before(start) {
			return ErrConflict
		}
	}
	return nil
}
