package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s *Store) initializeModelAccounts() (result error) {
	// SQLite requires FK enforcement to be disabled outside the transaction for
	// its documented parent-table rebuild. Pin the connection and always restore
	// it before returning; no application writes can observe this connection.
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var legacyAlter int
	if err = conn.QueryRowContext(ctx, `PRAGMA legacy_alter_table`).Scan(&legacyAlter); err != nil {
		return err
	}
	defer func() {
		_, fkErr := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
		alterSetting := "OFF"
		if legacyAlter != 0 {
			alterSetting = "ON"
		}
		_, alterErr := conn.ExecContext(ctx, `PRAGMA legacy_alter_table=`+alterSetting)
		result = errors.Join(result, fkErr, alterErr)
	}()
	if _, err = conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, `PRAGMA legacy_alter_table=ON`); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	migrated, err := migrateModelAccountAuth(tx)
	if err != nil {
		return err
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS model_accounts (
   id TEXT PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 256), revision INTEGER NOT NULL CHECK(revision>0),
   name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 200),
   provider TEXT NOT NULL CHECK(provider IN ('openai','anthropic','zhipu','kimi','custom')),
   base_url TEXT NOT NULL, auth_method TEXT NOT NULL CHECK(auth_method IN ('api_key','openai_siwc') AND (auth_method!='openai_siwc' OR provider='openai')),
   billing_mode TEXT NOT NULL CHECK(billing_mode IN ('subscription','api_credits','metered_api','unknown')),
   enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), current_credential_version INTEGER NOT NULL DEFAULT 0 CHECK(current_credential_version>=0),
   created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS model_credential_versions (
   account_id TEXT NOT NULL REFERENCES model_accounts(id), version INTEGER NOT NULL CHECK(version>0),
   operation_id TEXT NOT NULL UNIQUE CHECK(length(operation_id) BETWEEN 1 AND 256 AND operation_id NOT GLOB '*[^A-Za-z0-9_-]*'),
   state TEXT NOT NULL CHECK(state IN ('pending','available','failed','interrupted','revoked')),
   secret_ref TEXT NOT NULL UNIQUE CHECK(length(secret_ref)>0),
   cleanup_pending INTEGER NOT NULL CHECK(cleanup_pending IN (0,1) AND (cleanup_pending=0 OR state IN ('failed','interrupted','revoked'))),
   created_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(account_id,version))`,
		`CREATE UNIQUE INDEX IF NOT EXISTS model_one_pending_credential ON model_credential_versions(account_id) WHERE state='pending'`,
		`CREATE TRIGGER IF NOT EXISTS model_account_initial BEFORE INSERT ON model_accounts WHEN NEW.current_credential_version!=0 OR NEW.revision!=1 BEGIN SELECT RAISE(ABORT,'invalid account creation'); END`,
		`CREATE TRIGGER IF NOT EXISTS model_account_identity BEFORE UPDATE ON model_accounts WHEN NEW.id IS NOT OLD.id OR NEW.provider IS NOT OLD.provider OR NEW.base_url IS NOT OLD.base_url OR NEW.auth_method IS NOT OLD.auth_method OR NEW.billing_mode IS NOT OLD.billing_mode OR NEW.created_at IS NOT OLD.created_at BEGIN SELECT RAISE(ABORT,'immutable model account'); END`,
		`CREATE TRIGGER IF NOT EXISTS model_account_pending BEFORE UPDATE ON model_accounts WHEN (NEW.name IS NOT OLD.name OR NEW.enabled IS NOT OLD.enabled) AND EXISTS(SELECT 1 FROM model_credential_versions WHERE account_id=OLD.id AND state='pending') BEGIN SELECT RAISE(ABORT,'pending credential mutation'); END`,
		`CREATE TRIGGER IF NOT EXISTS model_account_current BEFORE UPDATE ON model_accounts WHEN NEW.current_credential_version!=0 AND NOT EXISTS(SELECT 1 FROM model_credential_versions WHERE account_id=NEW.id AND version=NEW.current_credential_version AND state='available') BEGIN SELECT RAISE(ABORT,'invalid current credential'); END`,
		`CREATE TRIGGER IF NOT EXISTS model_credential_initial BEFORE INSERT ON model_credential_versions WHEN NEW.state!='pending' OR NEW.cleanup_pending!=0 OR NEW.version!=(SELECT COALESCE(MAX(version),0)+1 FROM model_credential_versions WHERE account_id=NEW.account_id) BEGIN SELECT RAISE(ABORT,'invalid credential reservation'); END`,
		`CREATE TRIGGER IF NOT EXISTS model_credential_identity BEFORE UPDATE ON model_credential_versions WHEN NEW.account_id IS NOT OLD.account_id OR NEW.version IS NOT OLD.version OR NEW.operation_id IS NOT OLD.operation_id OR NEW.secret_ref IS NOT OLD.secret_ref OR NEW.created_at IS NOT OLD.created_at BEGIN SELECT RAISE(ABORT,'immutable credential identity'); END`,
		`CREATE TRIGGER IF NOT EXISTS model_credential_transition BEFORE UPDATE ON model_credential_versions WHEN NOT (
   (OLD.state='pending' AND ((NEW.state='available' AND NEW.cleanup_pending=0) OR (NEW.state IN ('failed','interrupted') AND NEW.cleanup_pending=1))) OR
   (OLD.state IN ('available','failed','interrupted') AND NEW.state='revoked' AND NEW.cleanup_pending=1) OR
   (OLD.state IN ('failed','interrupted','revoked') AND NEW.state=OLD.state AND OLD.cleanup_pending=1 AND NEW.cleanup_pending=0))
   BEGIN SELECT RAISE(ABORT,'invalid credential transition'); END`,
		`CREATE TRIGGER IF NOT EXISTS model_credential_revoke_guard BEFORE UPDATE ON model_credential_versions WHEN NEW.state='revoked' AND OLD.state!='revoked' AND (EXISTS(SELECT 1 FROM model_accounts WHERE id=OLD.account_id AND current_credential_version=OLD.version) OR EXISTS(SELECT 1 FROM model_credential_versions WHERE account_id=OLD.account_id AND state='pending')) BEGIN SELECT RAISE(ABORT,'unsafe credential revocation'); END`,
		`CREATE TRIGGER IF NOT EXISTS model_credential_no_delete BEFORE DELETE ON model_credential_versions BEGIN SELECT RAISE(ABORT,'credential history is append-only'); END`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	if migrated {
		rows, err := tx.Query(`PRAGMA foreign_key_check`)
		if err != nil {
			return err
		}
		violated := rows.Next()
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return rowErr
		}
		if violated {
			return errors.New("model account migration foreign key violation")
		}
	}
	return tx.Commit()
}

func migrateModelAccountAuth(tx *sql.Tx) (bool, error) {
	var schema string
	err := tx.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='model_accounts'`).Scan(&schema)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if strings.Contains(schema, "'openai_siwc'") {
		return false, nil
	}
	const oldCheck = "auth_method='api_key'"
	const newCheck = "auth_method IN ('api_key','openai_siwc') AND (auth_method!='openai_siwc' OR provider='openai')"
	if strings.Count(schema, oldCheck) != 1 {
		return false, errors.New("unsupported model account schema; migration refused")
	}
	// Retain every original constraint, index and trigger, including locally
	// installed safety triggers, rather than reconstructing a partial schema.
	rows, err := tx.Query(`SELECT sql FROM sqlite_master WHERE type IN ('trigger','index') AND tbl_name='model_accounts' AND sql IS NOT NULL ORDER BY type,name`)
	if err != nil {
		return false, err
	}
	var dependents []string
	for rows.Next() {
		var statement string
		if err = rows.Scan(&statement); err != nil {
			rows.Close()
			return false, err
		}
		dependents = append(dependents, statement)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	schema = strings.Replace(schema, oldCheck, newCheck, 1)
	schema = strings.Replace(schema, "model_accounts", "model_accounts_r5b_migration", 1)
	for _, statement := range []string{schema,
		`INSERT INTO model_accounts_r5b_migration (` + modelAccountColumns + `) SELECT ` + modelAccountColumns + ` FROM model_accounts`,
		`DROP TABLE model_accounts`,
		`ALTER TABLE model_accounts_r5b_migration RENAME TO model_accounts`,
	} {
		if _, err = tx.Exec(statement); err != nil {
			return false, err
		}
	}
	for _, statement := range dependents {
		if _, err = tx.Exec(statement); err != nil {
			return false, err
		}
	}
	return true, nil
}
