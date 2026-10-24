package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"time"
)

func (s *Store) initializeSessionRenewal() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS session_renewal_policies (
 account_id TEXT PRIMARY KEY REFERENCES model_accounts(id), revision INTEGER NOT NULL, enabled INTEGER NOT NULL CHECK(enabled IN(0,1)), material BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS session_renewal_dispatches (
 authorization_id TEXT NOT NULL, generation INTEGER NOT NULL, account_id TEXT NOT NULL, policy_revision INTEGER NOT NULL,
 stage TEXT NOT NULL CHECK(stage IN('dispatched','waiting','succeeded','recovery_required','login_required')), category TEXT NOT NULL, certainty TEXT NOT NULL,
 started_at TEXT NOT NULL, finished_at TEXT NOT NULL, new_generation INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(authorization_id,generation));
 CREATE TABLE IF NOT EXISTS session_renewal_schedule (authorization_id TEXT NOT NULL,generation INTEGER NOT NULL,earliest_refresh_at TEXT NOT NULL,PRIMARY KEY(authorization_id,generation));`)
	return err
}

// Called only by startup recovery once old owners have stopped. The journal,
// not the absence of a receipt, determines whether a generation may be used.
func (s *Store) RecoverSessionRenewals() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE model_authorizations SET state='refresh_unknown' WHERE state='available' AND EXISTS(SELECT 1 FROM session_renewal_dispatches d WHERE d.authorization_id=model_authorizations.id AND d.generation=model_authorizations.generation AND d.stage='dispatched')`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE session_renewal_dispatches SET stage='recovery_required',category='renewal_failed',
 certainty=CASE WHEN EXISTS(SELECT 1 FROM model_authorizations a WHERE a.id=session_renewal_dispatches.authorization_id AND a.generation>session_renewal_dispatches.generation) THEN 'confirmed' ELSE 'unknown' END,
 new_generation=COALESCE((SELECT a.generation FROM model_authorizations a WHERE a.id=session_renewal_dispatches.authorization_id AND a.generation>session_renewal_dispatches.generation),0),
 finished_at=? WHERE stage='dispatched'`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func renewalPolicy(row modelRowScanner, accountID string) (domain.SessionRenewalPolicy, error) {
	p := domain.SessionRenewalPolicy{AccountID: accountID}
	var raw []byte
	err := row.Scan(&p.Revision, &p.Enabled, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(raw, &p.Material)
	return p, err
}
func (s *Store) SessionRenewalPolicy(id string) (domain.SessionRenewalPolicy, error) {
	return renewalPolicy(s.db.QueryRow(`SELECT revision,enabled,material FROM session_renewal_policies WHERE account_id=?`, id), id)
}
func renewalPolicyTx(tx *sql.Tx, id string) (domain.SessionRenewalPolicy, error) {
	return renewalPolicy(tx.QueryRow(`SELECT revision,enabled,material FROM session_renewal_policies WHERE account_id=?`, id), id)
}
func validateRenewalTx(tx *sql.Tx, m domain.SessionRenewalMaterial) (domain.ModelAuthorization, error) {
	a, err := modelAccountTx(tx, m.AccountID)
	if err != nil {
		return domain.ModelAuthorization{}, err
	}
	b, err := currentModelAuthorizationTx(tx, m.AccountID)
	if err != nil {
		return b, err
	}
	if a.Revision != m.AccountRevision || !a.Enabled || a.Provider != "openai" || a.AuthMethod != "openai_siwc" || a.BillingMode != "subscription" || a.BaseURL != m.Resource || b.State != "available" || b.Binding.SpendClass != "subscription_only" || !domain.SameModelAuthorizationBinding(b.Binding, m.Binding) {
		return b, domain.ErrSessionRenewal
	}
	offline := false
	for _, scope := range b.Binding.Scopes {
		if scope == "offline_access" {
			offline = true
		}
	}
	if !offline {
		return b, domain.ErrSessionRenewal
	}
	var pending int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM model_authorization_operations WHERE account_id=? AND (cleanup_pending=1 OR state='pending')`, a.ID).Scan(&pending); err != nil {
		return b, err
	}
	if pending != 0 {
		return b, domain.ErrSessionRenewal
	}
	return b, nil
}
func (s *Store) PrepareSessionRenewal(m domain.SessionRenewalMaterial) (domain.SessionRenewalMaterial, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	if _, err = validateRenewalTx(tx, m); err != nil {
		return m, err
	}
	p, err := renewalPolicyTx(tx, m.AccountID)
	if err != nil {
		return m, err
	}
	if p.Enabled {
		return m, ErrConflict
	}
	var n int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM session_renewal_dispatches WHERE account_id=? AND stage='dispatched'`, m.AccountID).Scan(&n); err != nil {
		return m, err
	}
	if n > 0 {
		return m, ErrConflict
	}
	m.Revision = p.Revision + 1
	raw, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	_, err = tx.Exec(`INSERT INTO session_renewal_policies VALUES(?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET revision=excluded.revision,material=excluded.material`, m.AccountID, m.Revision, p.Enabled, raw)
	if err != nil {
		return m, err
	}
	return m, tx.Commit()
}
func (s *Store) ApproveSessionRenewal(id, materialID string, revision int64, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := renewalPolicyTx(tx, id)
	if err != nil {
		return err
	}
	expiry, e := time.Parse(time.RFC3339Nano, p.Material.ExpiresAt)
	if p.Enabled || p.Material.Revision != p.Revision || p.Revision != revision || p.Material.ID != materialID || e != nil || !now.Before(expiry) {
		return ErrConflict
	}
	if _, err = validateRenewalTx(tx, p.Material); err != nil {
		return err
	}
	var n int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM session_renewal_dispatches WHERE account_id=? AND stage NOT IN('succeeded','waiting')`, id).Scan(&n); err != nil {
		return err
	}
	// A fresh sign-in is explicit recovery; unresolved old claims still block.
	if n > 0 {
		var blocked int
		err = tx.QueryRow(`SELECT COUNT(*) FROM session_renewal_dispatches WHERE account_id=? AND (stage='dispatched' OR (authorization_id=? AND stage NOT IN('succeeded','waiting')))`, id, p.Material.Binding.ID).Scan(&blocked)
		if err != nil {
			return err
		}
		if blocked > 0 {
			return ErrConflict
		}
	}
	_, err = tx.Exec(`UPDATE session_renewal_policies SET enabled=1,revision=revision+1 WHERE account_id=?`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) DisableSessionRenewal(id string, revision int64) error {
	result, err := s.db.Exec(`UPDATE session_renewal_policies SET enabled=0,revision=revision+1 WHERE account_id=? AND revision=?`, id, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}
func (s *Store) EnabledSessionRenewals() ([]domain.SessionRenewalPolicy, error) {
	rows, err := s.db.Query(`SELECT account_id,revision,enabled,material FROM session_renewal_policies WHERE enabled=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SessionRenewalPolicy
	for rows.Next() {
		var p domain.SessionRenewalPolicy
		var raw []byte
		if err = rows.Scan(&p.AccountID, &p.Revision, &p.Enabled, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &p.Material); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) ClaimSessionRenewal(p domain.SessionRenewalPolicy, generation int64, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	latest, err := renewalPolicyTx(tx, p.AccountID)
	if err != nil {
		return err
	}
	if !latest.Enabled || latest.Revision != p.Revision {
		return ErrConflict
	}
	a, err := validateRenewalTx(tx, latest.Material)
	if err != nil {
		return err
	}
	if a.Generation != generation {
		return ErrConflict
	}
	var pending int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM session_renewal_dispatches WHERE account_id=? AND (stage='dispatched' OR (authorization_id=? AND stage NOT IN('succeeded','waiting')))`, p.AccountID, p.Material.Binding.ID).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return ErrConflict
	}
	if err = anchorCurrentConnection(tx, p.AccountID); err != nil {
		return err
	}
	result, err := tx.Exec(`INSERT INTO session_renewal_dispatches(authorization_id,generation,account_id,policy_revision,stage,category,certainty,started_at,finished_at) VALUES(?,?,?,?,'dispatched','','unknown',?,'') ON CONFLICT(authorization_id,generation) DO UPDATE SET policy_revision=excluded.policy_revision,stage='dispatched',category='',certainty='unknown',started_at=excluded.started_at,finished_at='' WHERE session_renewal_dispatches.stage='waiting' AND session_renewal_dispatches.certainty='not_sent'`, a.Binding.ID, generation, p.AccountID, p.Revision, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return modelStoreError(err)
	}
	if n, e := result.RowsAffected(); e != nil {
		return e
	} else if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}
func (s *Store) SessionRenewalConsentCurrent(id string, revision int64) error {
	p, err := s.SessionRenewalPolicy(id)
	if err != nil {
		return err
	}
	if !p.Enabled || p.Revision != revision {
		return ErrConflict
	}
	return nil
}
func (s *Store) FinishSessionRenewal(id string, generation, newGeneration int64, stage, category, certainty string, now time.Time) error {
	if stage != "succeeded" && stage != "recovery_required" && stage != "login_required" && stage != "waiting" {
		return domain.ErrSessionRenewal
	}
	if category != "" && category != "renewal_failed" && category != "auth_invalid" && category != "refresh_not_ready" {
		return domain.ErrSessionRenewal
	}
	if certainty != "confirmed" && certainty != "unknown" && certainty != "not_sent" && certainty != "rejected" {
		return domain.ErrSessionRenewal
	}
	if stage == "waiting" && (certainty != "not_sent" || category != "refresh_not_ready" || newGeneration != 0) {
		return domain.ErrSessionRenewal
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if stage == "recovery_required" {
		_, err = tx.Exec(`UPDATE model_authorizations SET state='refresh_unknown' WHERE id=? AND generation=? AND state='available'`, id, generation)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE session_renewal_dispatches SET stage=?,category=?,certainty=?,new_generation=?,finished_at=? WHERE authorization_id=? AND generation=? AND stage='dispatched'`, stage, category, certainty, newGeneration, now.UTC().Format(time.RFC3339Nano), id, generation)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Backfill only an old generation's nonsecret earliest schedule after exact
// bundle validation. This never adopts a credential or changes authority.
func (s *Store) RecordSessionRenewalEarliest(id string, generation int64, earliest string) error {
	if _, err := time.Parse(time.RFC3339, earliest); err != nil {
		return domain.ErrSessionRenewal
	}
	_, err := s.db.Exec(`INSERT INTO session_renewal_schedule VALUES(?,?,?) ON CONFLICT(authorization_id,generation) DO UPDATE SET earliest_refresh_at=excluded.earliest_refresh_at WHERE session_renewal_schedule.earliest_refresh_at=''`, id, generation, earliest)
	return err
}
func (s *Store) SessionRenewalReceipt(id string) (*domain.SessionRenewalReceipt, error) {
	var r domain.SessionRenewalReceipt
	err := s.db.QueryRow(`SELECT authorization_id,generation,new_generation,stage,category,certainty,started_at,finished_at FROM session_renewal_dispatches WHERE account_id=? ORDER BY rowid DESC LIMIT 1`, id).Scan(&r.AuthorizationID, &r.OldGeneration, &r.NewGeneration, &r.Stage, &r.Category, &r.Certainty, &r.StartedAt, &r.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}
func (s *Store) SessionRenewalEarliest(id string, generation int64) (string, error) {
	var earliest string
	err := s.db.QueryRow(`SELECT earliest_refresh_at FROM session_renewal_schedule WHERE authorization_id=? AND generation=?`, id, generation).Scan(&earliest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return earliest, err
}
