package store

import "fmt"

func (s *Store) initializeHooks() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS hook_rules (id TEXT PRIMARY KEY,revision INTEGER NOT NULL,data BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS hook_rule_revisions (id TEXT NOT NULL REFERENCES hook_rules(id),revision INTEGER NOT NULL,effective_after_seq INTEGER NOT NULL,data BLOB NOT NULL,PRIMARY KEY(id,revision))`,
		`CREATE INDEX IF NOT EXISTS hook_revision_boundary ON hook_rule_revisions(id,effective_after_seq,revision)`,
		`CREATE TABLE IF NOT EXISTS hook_processor_state (id INTEGER PRIMARY KEY CHECK(id=1),initialized_after_seq INTEGER NOT NULL,cursor INTEGER NOT NULL,last_processed_at TEXT NOT NULL)`,
		`INSERT INTO hook_processor_state(id,initialized_after_seq,cursor,last_processed_at) SELECT 1,COALESCE(MAX(seq),0),COALESCE(MAX(seq),0),'' FROM events WHERE true ON CONFLICT(id) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS hook_records (id INTEGER PRIMARY KEY AUTOINCREMENT,event_seq INTEGER NOT NULL REFERENCES events(seq),mission_id TEXT NOT NULL,mission_revision INTEGER NOT NULL,event_kind TEXT NOT NULL,event_created_at TEXT NOT NULL,rule_id TEXT NOT NULL,rule_revision INTEGER NOT NULL,rule_name TEXT NOT NULL,action TEXT NOT NULL,handler_version TEXT NOT NULL,status TEXT NOT NULL,processed_at TEXT NOT NULL,UNIQUE(event_seq,rule_id),FOREIGN KEY(rule_id,rule_revision) REFERENCES hook_rule_revisions(id,revision))`,
		`CREATE INDEX IF NOT EXISTS hook_records_mission ON hook_records(mission_id,id)`,
		`CREATE TRIGGER IF NOT EXISTS hook_revision_no_update BEFORE UPDATE ON hook_rule_revisions BEGIN SELECT RAISE(ABORT,'immutable hook revision'); END`,
		`CREATE TRIGGER IF NOT EXISTS hook_revision_no_delete BEFORE DELETE ON hook_rule_revisions BEGIN SELECT RAISE(ABORT,'append-only hook revisions'); END`,
		`CREATE TRIGGER IF NOT EXISTS hook_rule_no_delete BEFORE DELETE ON hook_rules BEGIN SELECT RAISE(ABORT,'append-only hook rules'); END`,
		`CREATE TRIGGER IF NOT EXISTS hook_record_no_update BEFORE UPDATE ON hook_records BEGIN SELECT RAISE(ABORT,'immutable hook record'); END`,
		`CREATE TRIGGER IF NOT EXISTS hook_record_no_delete BEFORE DELETE ON hook_records BEGIN SELECT RAISE(ABORT,'append-only hook records'); END`,
	}
	for _, q := range statements {
		if _, err = tx.Exec(q); err != nil {
			return fmt.Errorf("initialize hooks: %w", err)
		}
	}
	return tx.Commit()
}
