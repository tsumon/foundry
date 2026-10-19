package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"foundry/internal/domain"
)

const siwcHostMetadataKey = "openai_siwc_host_id"

var siwcUUIDHostPattern = regexp.MustCompile(`^urn:uuid:[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// SIWCRemoteRevocation records only whether the provider acknowledged the
// remote revocation. Local authorization state remains the execution guard.
type SIWCRemoteRevocation struct {
	AuthorizationID string `json:"authorization_id"`
	Outcome         string `json:"outcome"`
	UpdatedAt       string `json:"updated_at"`
}

func (s *Store) initializeSIWCMetadata() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS siwc_installation_metadata (
 key TEXT PRIMARY KEY,
 value TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS siwc_installation_metadata_immutable
BEFORE UPDATE ON siwc_installation_metadata BEGIN SELECT RAISE(ABORT,'immutable SIWC installation metadata'); END;
CREATE TRIGGER IF NOT EXISTS siwc_installation_metadata_no_delete
BEFORE DELETE ON siwc_installation_metadata BEGIN SELECT RAISE(ABORT,'immutable SIWC installation metadata'); END;
CREATE TABLE IF NOT EXISTS siwc_remote_revocations (
 authorization_id TEXT PRIMARY KEY,
 outcome TEXT NOT NULL CHECK(outcome IN ('confirmed','unconfirmed')),
 updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS siwc_remote_revocation_claims (
 authorization_id TEXT PRIMARY KEY,
 claimed_at TEXT NOT NULL
);`)
	return err
}

// ClaimSIWCRemoteRevocation elects the only caller allowed to send a remote
// revocation request. Claims are durable and intentionally never replayed.
func (s *Store) ClaimSIWCRemoteRevocation(authorizationID string) (bool, error) {
	if domain.ValidateModelCredentialOperation(authorizationID) != nil {
		return false, domain.ErrInvalidModelAuthorization
	}
	if err := s.initializeSIWCMetadata(); err != nil {
		return false, err
	}
	result, err := s.db.Exec(`INSERT OR IGNORE INTO siwc_remote_revocation_claims(authorization_id,claimed_at) VALUES(?,?)`, authorizationID, timestamp())
	if err != nil {
		return false, modelStoreError(err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// ClaimAndRevokeSIWCRemoteAuthorization atomically elects the only remote
// sender and makes the authorization locally non-callable. Secret cleanup is
// intentionally left to the lifecycle after the winner has copied the token.
func (s *Store) ClaimAndRevokeSIWCRemoteAuthorization(authorizationID string) (domain.ModelAuthorization, bool, error) {
	var zero domain.ModelAuthorization
	if domain.ValidateModelCredentialOperation(authorizationID) != nil {
		return zero, false, domain.ErrInvalidModelAuthorization
	}
	if err := s.initializeSIWCMetadata(); err != nil {
		return zero, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, false, err
	}
	defer tx.Rollback()
	authorization, err := modelAuthorizationTx(tx, authorizationID)
	if err != nil {
		return zero, false, err
	}
	claim, err := tx.Exec(`INSERT OR IGNORE INTO siwc_remote_revocation_claims(authorization_id,claimed_at) VALUES(?,?)`, authorizationID, timestamp())
	if err != nil {
		return zero, false, modelStoreError(err)
	}
	rows, err := claim.RowsAffected()
	if err != nil {
		return zero, false, err
	}
	winner := rows == 1
	authorization, err = revokeModelAuthorizationTx(tx, authorization)
	if err != nil {
		return zero, false, err
	}
	if err = tx.Commit(); err != nil {
		return zero, false, err
	}
	return authorization, winner, nil
}

func newSIWCUUIDHostID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("urn:uuid:%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// GetOrCreateSIWCHostID lazily creates non-secret per-installation metadata.
// INSERT OR IGNORE makes concurrent Store instances converge on one value.
func (s *Store) GetOrCreateSIWCHostID() (string, error) {
	if err := s.initializeSIWCMetadata(); err != nil {
		return "", err
	}
	candidate, err := newSIWCUUIDHostID()
	if err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT OR IGNORE INTO siwc_installation_metadata(key,value) VALUES(?,?)`, siwcHostMetadataKey, candidate); err != nil {
		return "", err
	}
	var id string
	if err = tx.QueryRow(`SELECT value FROM siwc_installation_metadata WHERE key=?`, siwcHostMetadataKey).Scan(&id); err != nil {
		return "", err
	}
	if !siwcUUIDHostPattern.MatchString(id) {
		return "", errors.New("invalid SIWC host metadata")
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) RecordSIWCRemoteRevocation(authorizationID, outcome string) (SIWCRemoteRevocation, error) {
	var zero SIWCRemoteRevocation
	if domain.ValidateModelCredentialOperation(authorizationID) != nil || outcome != "confirmed" && outcome != "unconfirmed" {
		return zero, domain.ErrInvalidModelAuthorization
	}
	if err := s.initializeSIWCMetadata(); err != nil {
		return zero, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	record := SIWCRemoteRevocation{AuthorizationID: authorizationID, Outcome: outcome, UpdatedAt: timestamp()}
	_, err = tx.Exec(`INSERT INTO siwc_remote_revocations(authorization_id,outcome,updated_at) VALUES(?,?,?)
ON CONFLICT(authorization_id) DO UPDATE SET outcome=excluded.outcome,updated_at=excluded.updated_at
WHERE siwc_remote_revocations.outcome<>'confirmed'`, record.AuthorizationID, record.Outcome, record.UpdatedAt)
	if err != nil {
		return zero, modelStoreError(err)
	}
	if err = tx.QueryRow(`SELECT authorization_id,outcome,updated_at FROM siwc_remote_revocations WHERE authorization_id=?`, authorizationID).Scan(&record.AuthorizationID, &record.Outcome, &record.UpdatedAt); err != nil {
		return zero, modelStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return record, nil
}

func (s *Store) GetSIWCRemoteRevocation(authorizationID string) (SIWCRemoteRevocation, error) {
	var record SIWCRemoteRevocation
	if domain.ValidateModelCredentialOperation(authorizationID) != nil {
		return record, domain.ErrInvalidModelAuthorization
	}
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='siwc_remote_revocations' LIMIT 1`).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return record, ErrNotFound
	}
	if err != nil {
		return record, err
	}
	err = s.db.QueryRow(`SELECT authorization_id,outcome,updated_at FROM siwc_remote_revocations WHERE authorization_id=?`, authorizationID).Scan(&record.AuthorizationID, &record.Outcome, &record.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return record, ErrNotFound
	}
	return record, err
}
