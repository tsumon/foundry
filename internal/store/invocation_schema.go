package store

import "fmt"

func (s *Store) initializeInvocations() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS read_grants (
 id TEXT PRIMARY KEY CHECK(length(id)>0), mission_id TEXT NOT NULL REFERENCES missions(id), epoch INTEGER NOT NULL CHECK(epoch>0),
 session_id TEXT NOT NULL CHECK(length(session_id)>0), role TEXT NOT NULL, required_state TEXT NOT NULL,
 binding_digest TEXT NOT NULL CHECK(length(binding_digest)=64), authority_digest TEXT NOT NULL CHECK(length(authority_digest)=64),
 data BLOB NOT NULL CHECK(json_valid(data)), created_at TEXT NOT NULL,
 UNIQUE(mission_id,epoch))`,
		`CREATE TABLE IF NOT EXISTS invocations (
 id TEXT PRIMARY KEY CHECK(length(id)>0), mission_id TEXT NOT NULL REFERENCES missions(id), epoch INTEGER NOT NULL CHECK(epoch>0),
 session_id TEXT NOT NULL CHECK(length(session_id)>0), ordinal INTEGER NOT NULL CHECK(ordinal>0), kind TEXT NOT NULL CHECK(kind IN ('model','repository')),
 role TEXT NOT NULL, name TEXT NOT NULL, binding_digest TEXT NOT NULL CHECK(length(binding_digest)=64), authority_digest TEXT NOT NULL CHECK(length(authority_digest)=64),
 grant_id TEXT NOT NULL DEFAULT '', intent BLOB NOT NULL CHECK(json_valid(intent)),
 status TEXT NOT NULL CHECK(status IN ('authorized_pending','dispatching','succeeded','failed','outcome_unknown','interrupted_before_dispatch')),
 result BLOB CHECK(result IS NULL OR json_valid(result)), error_code TEXT NOT NULL DEFAULT '', reserved_result_bytes INTEGER NOT NULL CHECK(reserved_result_bytes>=0),
 created_at TEXT NOT NULL, dispatched_at TEXT NOT NULL DEFAULT '', finished_at TEXT NOT NULL DEFAULT '',
 usage_known INTEGER NOT NULL DEFAULT 0 CHECK(usage_known IN (0,1)), input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(input_tokens>=0), output_tokens INTEGER NOT NULL DEFAULT 0 CHECK(output_tokens>=0),
 UNIQUE(mission_id,ordinal))`,
		`CREATE UNIQUE INDEX IF NOT EXISTS one_active_invocation ON invocations(mission_id) WHERE status IN ('authorized_pending','dispatching')`,
		`CREATE INDEX IF NOT EXISTS invocations_mission_ordinal ON invocations(mission_id,ordinal DESC)`,
		`CREATE TABLE IF NOT EXISTS invocation_transitions (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, invocation_id TEXT NOT NULL REFERENCES invocations(id), mission_id TEXT NOT NULL REFERENCES missions(id),
 from_status TEXT NOT NULL, to_status TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TRIGGER IF NOT EXISTS immutable_task_tool_mode BEFORE UPDATE OF data ON missions
 WHEN COALESCE(json_extract(OLD.data,'$.tool_mode'),'') IS NOT COALESCE(json_extract(NEW.data,'$.tool_mode'),'')
 BEGIN SELECT RAISE(ABORT,'tool mode is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS immutable_task_bindings BEFORE UPDATE OF data ON missions
 WHEN COALESCE(json_extract(OLD.data,'$.task_bindings'),'') IS NOT COALESCE(json_extract(NEW.data,'$.task_bindings'),'')
 BEGIN SELECT RAISE(ABORT,'task bindings are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS read_grant_no_update BEFORE UPDATE ON read_grants BEGIN SELECT RAISE(ABORT,'read grants are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS read_grant_no_delete BEFORE DELETE ON read_grants BEGIN SELECT RAISE(ABORT,'read grants are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS invocation_initial_state BEFORE INSERT ON invocations
 WHEN NEW.status!='authorized_pending' OR NEW.result IS NOT NULL OR NEW.error_code!='' OR NEW.dispatched_at!='' OR NEW.finished_at!='' OR NEW.usage_known!=0 OR NEW.input_tokens!=0 OR NEW.output_tokens!=0
 BEGIN SELECT RAISE(ABORT,'invalid invocation reservation'); END`,
		`CREATE TRIGGER IF NOT EXISTS invocation_immutable_intent BEFORE UPDATE ON invocations
 WHEN NEW.id IS NOT OLD.id OR NEW.mission_id IS NOT OLD.mission_id OR NEW.epoch IS NOT OLD.epoch OR NEW.session_id IS NOT OLD.session_id
 OR NEW.ordinal IS NOT OLD.ordinal OR NEW.kind IS NOT OLD.kind OR NEW.role IS NOT OLD.role OR NEW.name IS NOT OLD.name
 OR NEW.binding_digest IS NOT OLD.binding_digest OR NEW.authority_digest IS NOT OLD.authority_digest OR NEW.grant_id IS NOT OLD.grant_id
 OR NEW.intent IS NOT OLD.intent OR NEW.reserved_result_bytes IS NOT OLD.reserved_result_bytes OR NEW.created_at IS NOT OLD.created_at
 BEGIN SELECT RAISE(ABORT,'immutable invocation intent'); END`,
		`CREATE TRIGGER IF NOT EXISTS invocation_transition BEFORE UPDATE ON invocations
 WHEN NOT ((OLD.status='authorized_pending' AND NEW.status IN ('dispatching','interrupted_before_dispatch'))
 OR (OLD.status='dispatching' AND NEW.status IN ('succeeded','failed','outcome_unknown')))
 OR (NEW.status='dispatching' AND (NEW.dispatched_at='' OR NEW.finished_at!='' OR NEW.result IS NOT NULL))
 OR (NEW.status IN ('succeeded','failed','outcome_unknown','interrupted_before_dispatch') AND NEW.finished_at='')
 OR (NEW.status='succeeded' AND NEW.result IS NULL) OR (NEW.status!='succeeded' AND NEW.result IS NOT NULL)
 OR (NEW.status='succeeded' AND NEW.error_code!='') OR (NEW.status IN ('failed','outcome_unknown','interrupted_before_dispatch') AND NEW.error_code='')
 OR (NEW.usage_known=0 AND (NEW.input_tokens!=0 OR NEW.output_tokens!=0))
 BEGIN SELECT RAISE(ABORT,'invalid invocation transition'); END`,
		`CREATE TRIGGER IF NOT EXISTS invocation_no_delete BEFORE DELETE ON invocations BEGIN SELECT RAISE(ABORT,'invocations are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS invocation_transition_no_update BEFORE UPDATE ON invocation_transitions BEGIN SELECT RAISE(ABORT,'invocation transitions are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS invocation_transition_no_delete BEFORE DELETE ON invocation_transitions BEGIN SELECT RAISE(ABORT,'invocation transitions are append-only'); END`,
	}
	for _, statement := range statements {
		if _, err = tx.Exec(statement); err != nil {
			return fmt.Errorf("initialize invocations: %w", err)
		}
	}
	var hasReceipt bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('invocations') WHERE name='model_receipt')`).Scan(&hasReceipt); err != nil {
		return err
	}
	if !hasReceipt {
		if _, err = tx.Exec(`ALTER TABLE invocations ADD COLUMN model_receipt BLOB CHECK(model_receipt IS NULL OR json_valid(model_receipt))`); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE TRIGGER IF NOT EXISTS invocation_no_initial_receipt BEFORE INSERT ON invocations WHEN NEW.model_receipt IS NOT NULL BEGIN SELECT RAISE(ABORT,'receipt is outcome only'); END`,
		`CREATE TRIGGER IF NOT EXISTS invocation_receipt_outcome BEFORE UPDATE ON invocations WHEN NEW.model_receipt IS NOT NULL AND (NEW.kind!='model' OR NEW.status NOT IN ('succeeded','failed','outcome_unknown','interrupted_before_dispatch')) BEGIN SELECT RAISE(ABORT,'receipt is outcome only'); END`,
	} {
		if _, err = tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}
