package store

import (
	"database/sql"
	"errors"
	"math"
	"strings"

	"foundry/internal/domain"
)

const modelAccountColumns = `id,revision,name,provider,base_url,auth_method,billing_mode,enabled,current_credential_version,created_at,updated_at`
const modelCredentialColumns = `account_id,version,operation_id,state,secret_ref,cleanup_pending,created_at,updated_at`

type modelRowScanner interface{ Scan(...any) error }

func scanModelAccount(row modelRowScanner) (domain.ModelAccount, error) {
	var a domain.ModelAccount
	err := row.Scan(&a.ID, &a.Revision, &a.Name, &a.Provider, &a.BaseURL, &a.AuthMethod, &a.BillingMode, &a.Enabled, &a.CurrentCredentialVersion, &a.CreatedAt, &a.UpdatedAt)
	return a, modelStoreError(err)
}
func scanModelCredential(row modelRowScanner) (domain.ModelCredentialVersion, error) {
	var v domain.ModelCredentialVersion
	err := row.Scan(&v.AccountID, &v.Version, &v.OperationID, &v.State, &v.SecretRef, &v.CleanupPending, &v.CreatedAt, &v.UpdatedAt)
	return v, modelStoreError(err)
}
func modelStoreError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil && isConstraint(err) {
		return ErrConflict
	}
	return err
}
func modelAccountTx(tx *sql.Tx, id string) (domain.ModelAccount, error) {
	return scanModelAccount(tx.QueryRow(`SELECT `+modelAccountColumns+` FROM model_accounts WHERE id=?`, id))
}
func modelCredentialOperationTx(tx *sql.Tx, id string) (domain.ModelCredentialVersion, error) {
	return scanModelCredential(tx.QueryRow(`SELECT `+modelCredentialColumns+` FROM model_credential_versions WHERE operation_id=?`, id))
}
func noPendingModelCredential(tx *sql.Tx, id string) error {
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM model_credential_versions WHERE account_id=? AND state='pending'`, id).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return ErrConflict
	}
	return nil
}

func (s *Store) SaveModelAccount(a domain.ModelAccount, expectedRevision int64) (domain.ModelAccount, error) {
	if err := domain.ValidateModelAccount(a); err != nil {
		return domain.ModelAccount{}, err
	}
	if a.ID == "" || len(a.ID) > 256 || expectedRevision < 0 {
		return domain.ModelAccount{}, domain.ErrInvalidModelAccount
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ModelAccount{}, err
	}
	defer tx.Rollback()
	now := timestamp()
	if expectedRevision == 0 {
		a.Revision = 1
		a.CurrentCredentialVersion = 0
		a.CreatedAt = now
		a.UpdatedAt = now
		_, err = tx.Exec(`INSERT INTO model_accounts (`+modelAccountColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, a.ID, a.Revision, a.Name, a.Provider, a.BaseURL, a.AuthMethod, a.BillingMode, a.Enabled, a.CurrentCredentialVersion, a.CreatedAt, a.UpdatedAt)
	} else {
		old, readErr := modelAccountTx(tx, a.ID)
		if readErr != nil {
			return domain.ModelAccount{}, readErr
		}
		if old.Revision != expectedRevision || old.Revision == math.MaxInt64 {
			return domain.ModelAccount{}, ErrConflict
		}
		if old.Provider != a.Provider || old.BaseURL != a.BaseURL || old.AuthMethod != a.AuthMethod || old.BillingMode != a.BillingMode {
			return domain.ModelAccount{}, ErrConflict
		}
		if err := noPendingModelCredential(tx, a.ID); err != nil {
			return domain.ModelAccount{}, err
		}
		a.Revision = old.Revision + 1
		a.CurrentCredentialVersion = old.CurrentCredentialVersion
		a.CreatedAt = old.CreatedAt
		a.UpdatedAt = now
		_, err = tx.Exec(`UPDATE model_accounts SET revision=?,name=?,enabled=?,updated_at=? WHERE id=? AND revision=?`, a.Revision, a.Name, a.Enabled, a.UpdatedAt, a.ID, expectedRevision)
	}
	if err != nil {
		return domain.ModelAccount{}, modelStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return domain.ModelAccount{}, err
	}
	return a, nil
}
func (s *Store) GetModelAccount(id string) (domain.ModelAccount, error) {
	return scanModelAccount(s.db.QueryRow(`SELECT `+modelAccountColumns+` FROM model_accounts WHERE id=?`, id))
}
func (s *Store) ListModelAccounts() ([]domain.ModelAccount, error) {
	rows, err := s.db.Query(`SELECT ` + modelAccountColumns + ` FROM model_accounts ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.ModelAccount, 0)
	for rows.Next() {
		a, err := scanModelAccount(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// BeginModelCredential returns the original durable operation before checking
// revision. Its SecretRef identifies the sole writer that created this intent.
func (s *Store) BeginModelCredential(v domain.ModelCredentialVersion, expectedRevision int64) (domain.ModelCredentialVersion, error) {
	if err := domain.ValidateModelCredentialOperation(v.OperationID); err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	defer tx.Rollback()
	old, err := modelCredentialOperationTx(tx, v.OperationID)
	if err == nil {
		if old.AccountID != v.AccountID {
			return domain.ModelCredentialVersion{}, ErrConflict
		}
		return old, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return domain.ModelCredentialVersion{}, err
	}
	if !strings.HasPrefix(v.SecretRef, "foundry-model-account-") || len(v.SecretRef) > 256 || (v.State != "" && v.State != "pending") || v.Version != 0 {
		return domain.ModelCredentialVersion{}, domain.ErrInvalidModelAccount
	}
	a, err := modelAccountTx(tx, v.AccountID)
	if err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	if a.Revision != expectedRevision || a.Revision == math.MaxInt64 {
		return domain.ModelCredentialVersion{}, ErrConflict
	}
	if a.AuthMethod != "api_key" {
		return domain.ModelCredentialVersion{}, domain.ErrInvalidModelAccount
	}
	if err := noPendingModelCredential(tx, a.ID); err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	var last int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(version),0) FROM model_credential_versions WHERE account_id=?`, a.ID).Scan(&last); err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	if last == math.MaxInt64 {
		return domain.ModelCredentialVersion{}, ErrConflict
	}
	v.Version = last + 1
	v.State = "pending"
	v.CleanupPending = false
	v.CreatedAt = timestamp()
	v.UpdatedAt = v.CreatedAt
	_, err = tx.Exec(`INSERT INTO model_credential_versions (`+modelCredentialColumns+`) VALUES(?,?,?,?,?,?,?,?)`, v.AccountID, v.Version, v.OperationID, v.State, v.SecretRef, v.CleanupPending, v.CreatedAt, v.UpdatedAt)
	if err != nil {
		return domain.ModelCredentialVersion{}, modelStoreError(err)
	}
	if _, err := tx.Exec(`UPDATE model_accounts SET revision=revision+1,updated_at=? WHERE id=?`, v.UpdatedAt, a.ID); err != nil {
		return domain.ModelCredentialVersion{}, modelStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	return v, nil
}

func (s *Store) FinishModelCredential(operationID, state string) (domain.ModelCredentialVersion, error) {
	if state != "available" && state != "failed" && state != "interrupted" {
		return domain.ModelCredentialVersion{}, domain.ErrInvalidModelAccount
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	defer tx.Rollback()
	v, err := modelCredentialOperationTx(tx, operationID)
	if err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	if v.State == state {
		return v, nil
	}
	if v.State != "pending" {
		return domain.ModelCredentialVersion{}, ErrConflict
	}
	a, err := modelAccountTx(tx, v.AccountID)
	if err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	if a.Revision == math.MaxInt64 {
		return domain.ModelCredentialVersion{}, ErrConflict
	}
	v.State = state
	v.CleanupPending = state != "available"
	v.UpdatedAt = timestamp()
	_, err = tx.Exec(`UPDATE model_credential_versions SET state=?,cleanup_pending=?,updated_at=? WHERE operation_id=?`, v.State, v.CleanupPending, v.UpdatedAt, v.OperationID)
	if err != nil {
		return domain.ModelCredentialVersion{}, modelStoreError(err)
	}
	current := a.CurrentCredentialVersion
	if state == "available" {
		current = v.Version
	}
	_, err = tx.Exec(`UPDATE model_accounts SET revision=revision+1,current_credential_version=?,updated_at=? WHERE id=?`, current, v.UpdatedAt, a.ID)
	if err != nil {
		return domain.ModelCredentialVersion{}, modelStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	return v, nil
}
func (s *Store) GetModelCredential(accountID string, version int64) (domain.ModelCredentialVersion, error) {
	return scanModelCredential(s.db.QueryRow(`SELECT `+modelCredentialColumns+` FROM model_credential_versions WHERE account_id=? AND version=?`, accountID, version))
}
func (s *Store) GetModelCredentialOperation(operationID string) (domain.ModelCredentialVersion, error) {
	return scanModelCredential(s.db.QueryRow(`SELECT `+modelCredentialColumns+` FROM model_credential_versions WHERE operation_id=?`, operationID))
}
func (s *Store) ListModelCredentials(accountID string) ([]domain.ModelCredentialVersion, error) {
	return s.listModelCredentials(`WHERE account_id=? ORDER BY version`, accountID)
}
func (s *Store) listModelCredentials(clause string, args ...any) ([]domain.ModelCredentialVersion, error) {
	rows, err := s.db.Query(`SELECT `+modelCredentialColumns+` FROM model_credential_versions `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.ModelCredentialVersion, 0)
	for rows.Next() {
		v, err := scanModelCredential(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func (s *Store) RevokeModelCredential(accountID string, version, expectedRevision int64) (domain.ModelCredentialVersion, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	defer tx.Rollback()
	a, err := modelAccountTx(tx, accountID)
	if err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	if a.Revision != expectedRevision || a.Revision == math.MaxInt64 {
		return domain.ModelCredentialVersion{}, ErrConflict
	}
	if err := noPendingModelCredential(tx, a.ID); err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	v, err := scanModelCredential(tx.QueryRow(`SELECT `+modelCredentialColumns+` FROM model_credential_versions WHERE account_id=? AND version=?`, accountID, version))
	if err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	if v.State == "revoked" {
		return v, nil
	}
	v.State = "revoked"
	v.CleanupPending = true
	v.UpdatedAt = timestamp()
	// Clear the pointer before revoking its target inside the same transaction.
	current := a.CurrentCredentialVersion
	if current == version {
		current = 0
	}
	_, err = tx.Exec(`UPDATE model_accounts SET revision=revision+1,current_credential_version=?,updated_at=? WHERE id=?`, current, v.UpdatedAt, a.ID)
	if err != nil {
		return domain.ModelCredentialVersion{}, modelStoreError(err)
	}
	_, err = tx.Exec(`UPDATE model_credential_versions SET state='revoked',cleanup_pending=1,updated_at=? WHERE account_id=? AND version=?`, v.UpdatedAt, accountID, version)
	if err != nil {
		return domain.ModelCredentialVersion{}, modelStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	return v, nil
}
func (s *Store) PendingModelCredentialCleanup() ([]domain.ModelCredentialVersion, error) {
	return s.listModelCredentials(`WHERE state='pending' OR cleanup_pending=1 ORDER BY account_id,version`)
}
func (s *Store) CompleteModelCredentialCleanup(accountID string, version int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	v, err := scanModelCredential(tx.QueryRow(`SELECT `+modelCredentialColumns+` FROM model_credential_versions WHERE account_id=? AND version=?`, accountID, version))
	if err != nil {
		return err
	}
	if v.State == "pending" || v.State == "available" {
		return ErrConflict
	}
	if !v.CleanupPending {
		return nil
	}
	_, err = tx.Exec(`UPDATE model_credential_versions SET cleanup_pending=0,updated_at=? WHERE account_id=? AND version=?`, timestamp(), accountID, version)
	if err != nil {
		return modelStoreError(err)
	}
	return tx.Commit()
}
