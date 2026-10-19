package store

import "fmt"

func (s *Store) initializeRolePolicy() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS role_model_policies (
 revision INTEGER PRIMARY KEY CHECK(revision>0), created_at TEXT NOT NULL,
 data BLOB NOT NULL CHECK(json_valid(data)))`,
		`CREATE TABLE IF NOT EXISTS role_model_proposals (
 id TEXT PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 256),
 policy_revision INTEGER NOT NULL REFERENCES role_model_policies(revision),
 created_at TEXT NOT NULL, settings_digest TEXT NOT NULL CHECK(length(settings_digest)=64),
 inputs_digest TEXT NOT NULL CHECK(length(inputs_digest)=64), digest TEXT NOT NULL CHECK(length(digest)=64),
 data BLOB NOT NULL CHECK(json_valid(data)))`,
		`CREATE TABLE IF NOT EXISTS role_model_decisions (
 id TEXT PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 256),
 proposal_id TEXT NOT NULL UNIQUE REFERENCES role_model_proposals(id),
 confirmed_at TEXT NOT NULL, data BLOB NOT NULL CHECK(json_valid(data)))`,
		`CREATE TRIGGER IF NOT EXISTS role_model_policy_no_update BEFORE UPDATE ON role_model_policies BEGIN SELECT RAISE(ABORT,'role model policies are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS role_model_policy_no_delete BEFORE DELETE ON role_model_policies BEGIN SELECT RAISE(ABORT,'role model policies are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS role_model_proposal_no_update BEFORE UPDATE ON role_model_proposals BEGIN SELECT RAISE(ABORT,'role model proposals are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS role_model_proposal_no_delete BEFORE DELETE ON role_model_proposals BEGIN SELECT RAISE(ABORT,'role model proposals are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS role_model_decision_no_update BEFORE UPDATE ON role_model_decisions BEGIN SELECT RAISE(ABORT,'role model decisions are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS role_model_decision_no_delete BEFORE DELETE ON role_model_decisions BEGIN SELECT RAISE(ABORT,'role model decisions are append-only'); END`,
	}
	for _, statement := range statements {
		if _, err = tx.Exec(statement); err != nil {
			return fmt.Errorf("initialize role policy: %w", err)
		}
	}
	return tx.Commit()
}
