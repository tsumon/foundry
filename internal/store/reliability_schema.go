package store

import "fmt"

func (s *Store) initializeReliability() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS local_operations (
		 id TEXT PRIMARY KEY, mission_id TEXT NOT NULL REFERENCES missions(id),
		 intent BLOB NOT NULL, status TEXT NOT NULL CHECK(status IN ('pending','prepared','committed','interrupted')),
		 result BLOB, error TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS one_active_local_operation ON local_operations(mission_id) WHERE status IN ('pending','prepared')`,
		`CREATE TABLE IF NOT EXISTS plan_revisions (mission_id TEXT NOT NULL REFERENCES missions(id), plan_revision INTEGER NOT NULL, data BLOB NOT NULL, PRIMARY KEY(mission_id,plan_revision))`,
		`CREATE TRIGGER IF NOT EXISTS immutable_workspace_mode BEFORE UPDATE OF data ON missions
		 WHEN COALESCE(json_extract(OLD.data,'$.workspace_mode'),'') IS NOT COALESCE(json_extract(NEW.data,'$.workspace_mode'),'')
		 BEGIN SELECT RAISE(ABORT,'workspace mode is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS immutable_operation_intent BEFORE UPDATE ON local_operations
		 WHEN NEW.id IS NOT OLD.id OR NEW.mission_id IS NOT OLD.mission_id OR NEW.intent IS NOT OLD.intent
		 BEGIN SELECT RAISE(ABORT,'operation intent is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS operation_transition BEFORE UPDATE ON local_operations
		 WHEN NOT ((OLD.status='pending' AND NEW.status IN ('prepared','interrupted')) OR (OLD.status='prepared' AND NEW.status IN ('committed','interrupted')) OR (OLD.status='interrupted' AND NEW.status='prepared' AND json_extract(OLD.intent,'$.kind')='export'))
		 OR (OLD.result IS NOT NULL AND NEW.result IS NOT OLD.result)
		 OR (NEW.status='prepared' AND NEW.result IS NULL)
		 BEGIN SELECT RAISE(ABORT,'invalid operation transition'); END`,
		`CREATE TRIGGER IF NOT EXISTS immutable_operation_delete BEFORE DELETE ON local_operations BEGIN SELECT RAISE(ABORT,'operations are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS immutable_plan_revision_update BEFORE UPDATE ON plan_revisions BEGIN SELECT RAISE(ABORT,'plan revisions are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS immutable_plan_revision_delete BEFORE DELETE ON plan_revisions BEGIN SELECT RAISE(ABORT,'plan revisions are append-only'); END`,
	}
	for _, statement := range statements {
		if _, err = tx.Exec(statement); err != nil {
			return fmt.Errorf("initialize reliability: %w", err)
		}
	}
	return tx.Commit()
}
