package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"math"
	"time"
)

const authorizationColumns = `binding,state,generation,expires_at,secret_ref`
const authorizationOperationColumns = `id,account_id,kind,authorization_id,state,generation,secret_ref,cleanup_pending,write_claimed`

func (s *Store) initializeModelAuthorizations() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS model_authorizations (
 id TEXT PRIMARY KEY, account_id TEXT NOT NULL REFERENCES model_accounts(id), binding BLOB NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','available','refreshing','refresh_unknown','revoked','failed')),
 generation INTEGER NOT NULL CHECK(generation>0), expires_at TEXT NOT NULL, secret_ref TEXT NOT NULL UNIQUE);
 CREATE TABLE IF NOT EXISTS model_authorization_operations (
 id TEXT PRIMARY KEY, account_id TEXT NOT NULL REFERENCES model_accounts(id), kind TEXT NOT NULL CHECK(kind IN ('sign_in','refresh','revoke')),
 authorization_id TEXT NOT NULL, state TEXT NOT NULL CHECK(state IN ('pending','available','refresh_unknown','revoked','failed')),
 generation INTEGER NOT NULL CHECK(generation>0), secret_ref TEXT NOT NULL UNIQUE, cleanup_pending INTEGER NOT NULL CHECK(cleanup_pending IN (0,1)), write_claimed INTEGER NOT NULL DEFAULT 0 CHECK(write_claimed IN (0,1)),
 UNIQUE(authorization_id,generation));
 CREATE UNIQUE INDEX IF NOT EXISTS model_authorization_one_pending ON model_authorization_operations(account_id) WHERE state='pending';
 CREATE TRIGGER IF NOT EXISTS model_authorization_binding_immutable BEFORE UPDATE OF id,account_id,binding ON model_authorizations BEGIN SELECT RAISE(ABORT,'immutable authorization binding'); END;
 CREATE TRIGGER IF NOT EXISTS model_authorization_generation_guard BEFORE UPDATE ON model_authorizations WHEN
 NEW.generation<OLD.generation OR
 ((NEW.secret_ref IS NOT OLD.secret_ref OR NEW.expires_at IS NOT OLD.expires_at OR (NEW.state='available' AND OLD.state!='available')) AND NEW.generation<=OLD.generation) OR
 (NEW.generation>OLD.generation AND NOT EXISTS(SELECT 1 FROM model_authorization_operations o WHERE o.authorization_id=OLD.id AND o.account_id=OLD.account_id AND o.state='pending' AND o.generation=NEW.generation AND o.secret_ref=NEW.secret_ref))
 BEGIN SELECT RAISE(ABORT,'invalid authorization generation'); END;
 CREATE TRIGGER IF NOT EXISTS model_authorization_no_delete BEFORE DELETE ON model_authorizations BEGIN SELECT RAISE(ABORT,'immutable authorization history'); END;
 CREATE TRIGGER IF NOT EXISTS model_authorization_operation_identity BEFORE UPDATE OF id,account_id,kind,authorization_id,generation,secret_ref ON model_authorization_operations BEGIN SELECT RAISE(ABORT,'immutable authorization intent'); END;
 CREATE TRIGGER IF NOT EXISTS model_authorization_operation_no_delete BEFORE DELETE ON model_authorization_operations BEGIN SELECT RAISE(ABORT,'immutable authorization intent'); END;`)
	return err
}

func scanModelAuthorization(row modelRowScanner) (domain.ModelAuthorization, error) {
	var a domain.ModelAuthorization
	var raw []byte
	err := row.Scan(&raw, &a.State, &a.Generation, &a.ExpiresAt, &a.SecretRef)
	if err != nil {
		return a, modelStoreError(err)
	}
	if err = json.Unmarshal(raw, &a.Binding); err != nil {
		return a, err
	}
	return a, domain.ValidateModelAuthorizationBinding(a.Binding)
}
func scanAuthorizationOperation(row modelRowScanner) (domain.AuthorizationOperation, error) {
	var o domain.AuthorizationOperation
	err := row.Scan(&o.ID, &o.AccountID, &o.Kind, &o.AuthorizationID, &o.State, &o.Generation, &o.SecretRef, &o.CleanupPending, &o.WriteClaimed)
	return o, modelStoreError(err)
}
func modelAuthorizationTx(tx *sql.Tx, id string) (domain.ModelAuthorization, error) {
	return scanModelAuthorization(tx.QueryRow(`SELECT `+authorizationColumns+` FROM model_authorizations WHERE id=?`, id))
}
func currentModelAuthorizationTx(tx *sql.Tx, id string) (domain.ModelAuthorization, error) {
	return scanModelAuthorization(tx.QueryRow(`SELECT `+authorizationColumns+` FROM model_authorizations WHERE account_id=? ORDER BY rowid DESC LIMIT 1`, id))
}
func authorizationOperationTx(tx *sql.Tx, id string) (domain.AuthorizationOperation, error) {
	return scanAuthorizationOperation(tx.QueryRow(`SELECT `+authorizationOperationColumns+` FROM model_authorization_operations WHERE id=?`, id))
}
func (s *Store) GetModelAuthorization(id string) (domain.ModelAuthorization, error) {
	return scanModelAuthorization(s.db.QueryRow(`SELECT `+authorizationColumns+` FROM model_authorizations WHERE id=?`, id))
}
func (s *Store) CurrentModelAuthorization(accountID string) (domain.ModelAuthorization, error) {
	return scanModelAuthorization(s.db.QueryRow(`SELECT `+authorizationColumns+` FROM model_authorizations WHERE account_id=? ORDER BY rowid DESC LIMIT 1`, accountID))
}

func (s *Store) ListModelAuthorizations() ([]domain.ModelAuthorization, error) {
	rows, err := s.db.Query(`SELECT ` + authorizationColumns + ` FROM model_authorizations ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.ModelAuthorization, 0)
	for rows.Next() {
		a, err := scanModelAuthorization(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
func (s *Store) GetModelAuthorizationOperation(id string) (domain.AuthorizationOperation, error) {
	return scanAuthorizationOperation(s.db.QueryRow(`SELECT `+authorizationOperationColumns+` FROM model_authorization_operations WHERE id=?`, id))
}

// Each successful Begin returns sole ownership. Duplicate operations conflict;
// callers must never replay token exchange or refresh from persisted intents.
func (s *Store) BeginModelAuthorization(accountID, operationID, kind, authorizationID string) (domain.AuthorizationOperation, error) {
	var zero domain.AuthorizationOperation
	if domain.ValidateModelCredentialOperation(operationID) != nil || domain.ValidateModelCredentialOperation(authorizationID) != nil || (kind != "sign_in" && kind != "refresh") {
		return zero, domain.ErrInvalidModelAuthorization
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	a, err := modelAccountTx(tx, accountID)
	if err != nil {
		return zero, err
	}
	if !a.Enabled || a.AuthMethod != "openai_siwc" {
		return zero, ErrModelAccountUnavailable
	}
	old, err := modelAuthorizationTx(tx, authorizationID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return zero, err
	}
	if err == nil && old.Binding.AccountID != accountID {
		return zero, ErrConflict
	}
	if err == nil {
		current, currentErr := currentModelAuthorizationTx(tx, accountID)
		if currentErr != nil {
			return zero, currentErr
		}
		// A superseded immutable grant cannot become current again. A new grant
		// needs a fresh binding ID; reject before reserving or writing a token.
		if current.Binding.ID != authorizationID {
			return zero, ErrConflict
		}
	}
	if kind == "refresh" && (err != nil || old.State != "available" || old.Binding.SpendClass == "identity_only") {
		return zero, ErrConflict
	}
	var last int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(generation),0) FROM model_authorization_operations WHERE authorization_id=?`, authorizationID).Scan(&last); err != nil {
		return zero, err
	}
	if last == math.MaxInt64 {
		return zero, ErrConflict
	}
	var random [24]byte
	if _, err = rand.Read(random[:]); err != nil {
		return zero, err
	}
	o := domain.AuthorizationOperation{ID: operationID, AccountID: accountID, Kind: kind, AuthorizationID: authorizationID, State: "pending", Generation: last + 1, SecretRef: "foundry-model-authorization-" + hex.EncodeToString(random[:])}
	if _, err = tx.Exec(`INSERT INTO model_authorization_operations (`+authorizationOperationColumns+`) VALUES(?,?,?,?,?,?,?,0,0)`, o.ID, o.AccountID, o.Kind, o.AuthorizationID, o.State, o.Generation, o.SecretRef); err != nil {
		return zero, modelStoreError(err)
	}
	state := "pending"
	if kind == "refresh" {
		state = "refreshing"
	}
	if _, err = tx.Exec(`UPDATE model_authorizations SET state=? WHERE id=?`, state, authorizationID); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return o, nil
}

// ClaimModelAuthorizationWrite makes external secret writing single-owner even
// across multiple Service instances. The claim is never reset or replayed.
func (s *Store) ClaimModelAuthorizationWrite(id string) error {
	result, err := s.db.Exec(`UPDATE model_authorization_operations SET write_claimed=1 WHERE id=? AND state='pending' AND write_claimed=0`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) CommitModelAuthorization(operationID string, b domain.ModelAuthorizationBinding, expiresAt string) (domain.ModelAuthorization, error) {
	return s.CommitModelAuthorizationSchedule(operationID, b, expiresAt, "", 0)
}

// Renewal commits fence withdrawn consent in the same transaction as the
// generation pointer. Schedule is nonsecret and advances atomically with it.
func (s *Store) CommitModelAuthorizationSchedule(operationID string, b domain.ModelAuthorizationBinding, expiresAt, earliest string, renewalRevision int64) (domain.ModelAuthorization, error) {
	var zero domain.ModelAuthorization
	if domain.ValidateModelAuthorizationBinding(b) != nil {
		return zero, domain.ErrInvalidModelAuthorization
	}
	if _, err := time.Parse(time.RFC3339, expiresAt); err != nil {
		return zero, domain.ErrInvalidModelAuthorization
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	o, err := authorizationOperationTx(tx, operationID)
	if err != nil {
		return zero, err
	}
	if o.State != "pending" || o.AccountID != b.AccountID || o.AuthorizationID != b.ID {
		return zero, ErrConflict
	}
	if earliest != "" {
		if _, err = time.Parse(time.RFC3339, earliest); err != nil {
			return zero, domain.ErrInvalidModelAuthorization
		}
	}
	if renewalRevision != 0 {
		p, e := renewalPolicyTx(tx, b.AccountID)
		if e != nil {
			return zero, e
		}
		account, e := modelAccountTx(tx, b.AccountID)
		if e != nil {
			return zero, e
		}
		if !p.Enabled || p.Revision != renewalRevision || account.Revision != p.Material.AccountRevision || !domain.SameModelAuthorizationBinding(p.Material.Binding, b) {
			return zero, ErrConflict
		}
		var n int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM session_renewal_dispatches WHERE authorization_id=? AND generation=? AND policy_revision=? AND stage='dispatched'`, b.ID, o.Generation-1, renewalRevision).Scan(&n); err != nil {
			return zero, err
		}
		if n != 1 {
			return zero, ErrConflict
		}
	}
	account, err := modelAccountTx(tx, b.AccountID)
	if err != nil {
		return zero, err
	}
	if !account.Enabled {
		return zero, ErrModelAccountUnavailable
	}
	current, err := currentModelAuthorizationTx(tx, b.AccountID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return zero, err
	}
	if err == nil && (current.Binding.Issuer != b.Issuer || current.Binding.Subject != b.Subject || current.Binding.ClientID != b.ClientID) {
		return zero, ErrConflict
	}
	old, err := modelAuthorizationTx(tx, b.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return zero, err
	}
	exists := err == nil
	if exists && !domain.SameModelAuthorizationBinding(old.Binding, b) {
		return zero, ErrConflict
	}
	if exists && current.Binding.ID != b.ID {
		return zero, ErrConflict
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return zero, err
	}
	// Old generations become exact local cleanup intents in the same commit.
	if _, err = tx.Exec(`UPDATE model_authorization_operations SET cleanup_pending=1 WHERE account_id=? AND state='available' AND secret_ref IN (SELECT secret_ref FROM model_authorizations WHERE account_id=?)`, b.AccountID, b.AccountID); err != nil {
		return zero, err
	}
	if _, err = tx.Exec(`UPDATE model_authorizations SET state='revoked' WHERE account_id=? AND id<>?`, b.AccountID, b.ID); err != nil {
		return zero, err
	}
	if exists {
		_, err = tx.Exec(`UPDATE model_authorizations SET state='available',generation=?,expires_at=?,secret_ref=? WHERE id=?`, o.Generation, expiresAt, o.SecretRef, b.ID)
	} else {
		_, err = tx.Exec(`INSERT INTO model_authorizations(id,account_id,binding,state,generation,expires_at,secret_ref) VALUES(?,?,?,'available',?,?,?)`, b.ID, b.AccountID, raw, o.Generation, expiresAt, o.SecretRef)
	}
	if err != nil {
		return zero, modelStoreError(err)
	}
	if _, err = tx.Exec(`UPDATE model_authorization_operations SET state='available',write_claimed=0 WHERE id=?`, o.ID); err != nil {
		return zero, err
	}
	if _, err = tx.Exec(`INSERT INTO session_renewal_schedule VALUES(?,?,?)`, b.ID, o.Generation, earliest); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return domain.ModelAuthorization{Binding: b, State: "available", Generation: o.Generation, ExpiresAt: expiresAt, SecretRef: o.SecretRef}, nil
}

func failAuthorizationTx(tx *sql.Tx, o domain.AuthorizationOperation, state string, writerFinished bool) error {
	if o.Kind == "refresh" {
		state = "refresh_unknown"
	}
	if o.State != "pending" {
		if o.State != "failed" && o.State != "refresh_unknown" && o.State != "revoked" {
			return ErrConflict
		}
		// Only the owning writer, or startup recovery after all writers are
		// gone, may acknowledge a completed external Set. External failure or
		// cancellation must retain the outstanding marker even when repeated.
		if writerFinished && o.WriteClaimed {
			_, err := tx.Exec(`UPDATE model_authorization_operations SET write_claimed=0,cleanup_pending=1 WHERE id=?`, o.ID)
			return err
		}
		return nil
	}
	if _, err := tx.Exec(`UPDATE model_authorization_operations SET state=?,cleanup_pending=1,write_claimed=? WHERE id=?`, state, o.WriteClaimed && !writerFinished, o.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE model_authorizations SET state=? WHERE id=? AND state<>'revoked'`, state, o.AuthorizationID); err != nil {
		return err
	}
	// Failed refresh or reauthorization cleans only the attempted generation.
	// Retain any previous bundle for explicit revoke or successful replacement;
	// the failed/refresh_unknown authorization denies inference and retry. An
	// initial failed sign-in has no previous bundle to preserve.
	return nil
}
func (s *Store) FailModelAuthorization(operationID, state string) error {
	return s.failModelAuthorization(operationID, state, false)
}

// FinishModelAuthorizationWriteFailure is called only by the Service that won
// ClaimModelAuthorizationWrite, after its external Set has returned (or before
// it started). This is not the cancellation API; external callers must use Fail.
func (s *Store) FinishModelAuthorizationWriteFailure(operationID, state string) error {
	return s.failModelAuthorization(operationID, state, true)
}

func (s *Store) failModelAuthorization(operationID, state string, writerFinished bool) error {
	if state != "failed" && state != "refresh_unknown" {
		return domain.ErrInvalidModelAuthorization
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	o, err := authorizationOperationTx(tx, operationID)
	if err != nil {
		return err
	}
	if err = failAuthorizationTx(tx, o, state, writerFinished); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RevokeModelAuthorization(id string) (domain.ModelAuthorization, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ModelAuthorization{}, err
	}
	defer tx.Rollback()
	a, err := modelAuthorizationTx(tx, id)
	if err != nil {
		return a, err
	}
	a, err = revokeModelAuthorizationTx(tx, a)
	if err != nil {
		return a, err
	}
	if err = tx.Commit(); err != nil {
		return domain.ModelAuthorization{}, err
	}
	return a, nil
}

func revokeModelAuthorizationTx(tx *sql.Tx, a domain.ModelAuthorization) (domain.ModelAuthorization, error) {
	if a.State == "revoked" {
		return a, nil
	}
	id := a.Binding.ID
	var last int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(generation),0) FROM model_authorization_operations WHERE authorization_id=?`, id).Scan(&last); err != nil {
		return a, err
	}
	if last == math.MaxInt64 {
		return a, ErrConflict
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return a, err
	}
	opID := "revoke-" + hex.EncodeToString(random[:])
	if _, err := tx.Exec(`INSERT INTO model_authorization_operations (`+authorizationOperationColumns+`) VALUES(?,?,'revoke',?,'revoked',?,?,0,0)`, opID, a.Binding.AccountID, id, last+1, "foundry-model-authorization-"+hex.EncodeToString(random[:])); err != nil {
		return a, err
	}
	if _, err := tx.Exec(`UPDATE model_authorizations SET state='revoked' WHERE id=?`, id); err != nil {
		return a, err
	}
	if _, err := tx.Exec(`UPDATE model_authorization_operations SET state=CASE WHEN state='pending' THEN 'revoked' ELSE state END,cleanup_pending=1 WHERE authorization_id=? AND (state='pending' OR (state='available' AND secret_ref=?))`, id, a.SecretRef); err != nil {
		return a, err
	}
	a.State = "revoked"
	return a, nil
}

// Recovery has no access to a provider or secret backend and never retries I/O.
func (s *Store) RecoverModelAuthorizations() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT ` + authorizationOperationColumns + ` FROM model_authorization_operations WHERE state='pending' OR (state IN ('failed','refresh_unknown','revoked') AND write_claimed=1)`)
	if err != nil {
		return err
	}
	var pending []domain.AuthorizationOperation
	for rows.Next() {
		o, e := scanAuthorizationOperation(rows)
		if e != nil {
			rows.Close()
			return e
		}
		pending = append(pending, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, o := range pending {
		if err = failAuthorizationTx(tx, o, "failed", true); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) PendingModelAuthorizationCleanup() ([]domain.AuthorizationOperation, error) {
	rows, err := s.db.Query(`SELECT ` + authorizationOperationColumns + ` FROM model_authorization_operations WHERE cleanup_pending=1 ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.AuthorizationOperation, 0)
	for rows.Next() {
		o, err := scanAuthorizationOperation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}
func (s *Store) CompleteModelAuthorizationCleanup(id string) error {
	// An available operation describes an extant token. Once its exact ref is
	// deleted it is terminal revoked, so later reauthorization cannot reopen
	// cleanup merely because the historical authorization still names that ref.
	result, err := s.db.Exec(`UPDATE model_authorization_operations SET cleanup_pending=0,state=CASE WHEN state='available' THEN 'revoked' ELSE state END WHERE id=? AND cleanup_pending=1 AND write_claimed=0 AND NOT EXISTS (SELECT 1 FROM model_authorizations a WHERE a.secret_ref=model_authorization_operations.secret_ref AND a.state='available')`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
