-- R5A model-account schema from base 291cae1; no secrets or live accounts.
CREATE TABLE model_accounts (
 id TEXT PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 256), revision INTEGER NOT NULL CHECK(revision>0),
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 200),
 provider TEXT NOT NULL CHECK(provider IN ('openai','anthropic','zhipu','kimi','custom')),
 base_url TEXT NOT NULL, auth_method TEXT NOT NULL CHECK(auth_method='api_key'),
 billing_mode TEXT NOT NULL CHECK(billing_mode IN ('subscription','api_credits','metered_api','unknown')),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), current_credential_version INTEGER NOT NULL DEFAULT 0 CHECK(current_credential_version>=0),
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE model_credential_versions (
 account_id TEXT NOT NULL REFERENCES model_accounts(id), version INTEGER NOT NULL CHECK(version>0),
 operation_id TEXT NOT NULL UNIQUE CHECK(length(operation_id) BETWEEN 1 AND 256 AND operation_id NOT GLOB '*[^A-Za-z0-9_-]*'),
 state TEXT NOT NULL CHECK(state IN ('pending','available','failed','interrupted','revoked')),
 secret_ref TEXT NOT NULL UNIQUE CHECK(length(secret_ref)>0),
 cleanup_pending INTEGER NOT NULL CHECK(cleanup_pending IN (0,1) AND (cleanup_pending=0 OR state IN ('failed','interrupted','revoked'))),
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(account_id,version));
CREATE UNIQUE INDEX model_one_pending_credential ON model_credential_versions(account_id) WHERE state='pending';
CREATE TRIGGER model_account_initial BEFORE INSERT ON model_accounts WHEN NEW.current_credential_version!=0 OR NEW.revision!=1 BEGIN SELECT RAISE(ABORT,'invalid account creation'); END;
CREATE TRIGGER model_account_identity BEFORE UPDATE ON model_accounts WHEN NEW.id IS NOT OLD.id OR NEW.provider IS NOT OLD.provider OR NEW.base_url IS NOT OLD.base_url OR NEW.auth_method IS NOT OLD.auth_method OR NEW.billing_mode IS NOT OLD.billing_mode OR NEW.created_at IS NOT OLD.created_at BEGIN SELECT RAISE(ABORT,'immutable model account'); END;
CREATE TRIGGER model_account_pending BEFORE UPDATE ON model_accounts WHEN (NEW.name IS NOT OLD.name OR NEW.enabled IS NOT OLD.enabled) AND EXISTS(SELECT 1 FROM model_credential_versions WHERE account_id=OLD.id AND state='pending') BEGIN SELECT RAISE(ABORT,'pending credential mutation'); END;
CREATE TRIGGER model_account_current BEFORE UPDATE ON model_accounts WHEN NEW.current_credential_version!=0 AND NOT EXISTS(SELECT 1 FROM model_credential_versions WHERE account_id=NEW.id AND version=NEW.current_credential_version AND state='available') BEGIN SELECT RAISE(ABORT,'invalid current credential'); END;
CREATE TRIGGER model_credential_initial BEFORE INSERT ON model_credential_versions WHEN NEW.state!='pending' OR NEW.cleanup_pending!=0 OR NEW.version!=(SELECT COALESCE(MAX(version),0)+1 FROM model_credential_versions WHERE account_id=NEW.account_id) BEGIN SELECT RAISE(ABORT,'invalid credential reservation'); END;
CREATE TRIGGER model_credential_identity BEFORE UPDATE ON model_credential_versions WHEN NEW.account_id IS NOT OLD.account_id OR NEW.version IS NOT OLD.version OR NEW.operation_id IS NOT OLD.operation_id OR NEW.secret_ref IS NOT OLD.secret_ref OR NEW.created_at IS NOT OLD.created_at BEGIN SELECT RAISE(ABORT,'immutable credential identity'); END;
CREATE TRIGGER model_credential_transition BEFORE UPDATE ON model_credential_versions WHEN NOT (
 (OLD.state='pending' AND ((NEW.state='available' AND NEW.cleanup_pending=0) OR (NEW.state IN ('failed','interrupted') AND NEW.cleanup_pending=1))) OR
 (OLD.state IN ('available','failed','interrupted') AND NEW.state='revoked' AND NEW.cleanup_pending=1) OR
 (OLD.state IN ('failed','interrupted','revoked') AND NEW.state=OLD.state AND OLD.cleanup_pending=1 AND NEW.cleanup_pending=0))
 BEGIN SELECT RAISE(ABORT,'invalid credential transition'); END;
CREATE TRIGGER model_credential_revoke_guard BEFORE UPDATE ON model_credential_versions WHEN NEW.state='revoked' AND OLD.state!='revoked' AND (EXISTS(SELECT 1 FROM model_accounts WHERE id=OLD.account_id AND current_credential_version=OLD.version) OR EXISTS(SELECT 1 FROM model_credential_versions WHERE account_id=OLD.account_id AND state='pending')) BEGIN SELECT RAISE(ABORT,'unsafe credential revocation'); END;
CREATE TRIGGER model_credential_no_delete BEFORE DELETE ON model_credential_versions BEGIN SELECT RAISE(ABORT,'credential history is append-only'); END;
