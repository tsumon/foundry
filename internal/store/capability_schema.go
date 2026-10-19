package store

import "fmt"

func (s *Store) initializeCapabilities() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS mcp_connections (id TEXT PRIMARY KEY CHECK(length(id)>0), revision INTEGER NOT NULL CHECK(revision>0), data BLOB NOT NULL CHECK(json_valid(data) AND json_extract(data,'$.id')=id AND json_extract(data,'$.revision')=revision))`,
		`CREATE TABLE IF NOT EXISTS discovery_operations (
 id TEXT PRIMARY KEY CHECK(length(id)>0),connection_id TEXT NOT NULL REFERENCES mcp_connections(id),
 intent BLOB NOT NULL CHECK(json_valid(intent)),
 status TEXT NOT NULL CHECK(status IN ('authorized_pending','dispatching','succeeded','failed','outcome_unknown')),
 requests INTEGER CHECK(requests>=0),response_bytes INTEGER CHECK(response_bytes>=0),snapshot_id TEXT NOT NULL DEFAULT '',error_code TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,finished_at TEXT NOT NULL DEFAULT '')`,
		`CREATE UNIQUE INDEX IF NOT EXISTS one_active_discovery ON discovery_operations(connection_id) WHERE status IN ('authorized_pending','dispatching')`,
		`CREATE TABLE IF NOT EXISTS mcp_tool_snapshots (id TEXT PRIMARY KEY CHECK(length(id)>0),connection_id TEXT NOT NULL REFERENCES mcp_connections(id),connection_revision INTEGER NOT NULL CHECK(connection_revision>0),operation_id TEXT NOT NULL UNIQUE REFERENCES discovery_operations(id),data BLOB NOT NULL CHECK(json_valid(data)))`,
		`CREATE TABLE IF NOT EXISTS skills (id TEXT PRIMARY KEY CHECK(length(id)>0),revision INTEGER NOT NULL CHECK(revision>0),name TEXT NOT NULL UNIQUE COLLATE NOCASE CHECK(length(name)>0))`,
		`CREATE TABLE IF NOT EXISTS skill_revisions (id TEXT NOT NULL REFERENCES skills(id),revision INTEGER NOT NULL CHECK(revision>0),data BLOB NOT NULL CHECK(json_valid(data)),PRIMARY KEY(id,revision))`,
		`CREATE TABLE IF NOT EXISTS git_repositories (id TEXT PRIMARY KEY CHECK(length(id)>0),revision INTEGER NOT NULL CHECK(revision>0),data BLOB NOT NULL CHECK(json_valid(data) AND json_extract(data,'$.id')=id AND json_extract(data,'$.revision')=revision))`,
		`CREATE TABLE IF NOT EXISTS git_observations (id TEXT PRIMARY KEY CHECK(length(id)>0),repository_id TEXT NOT NULL REFERENCES git_repositories(id),repository_revision INTEGER NOT NULL CHECK(repository_revision>0),data BLOB NOT NULL CHECK(json_valid(data) AND json_extract(data,'$.working_tree_status')='not_checked' AND json_extract(data,'$.status') IN ('succeeded','failed')))`,
		`CREATE TRIGGER IF NOT EXISTS mcp_active_config_update BEFORE UPDATE ON mcp_connections WHEN EXISTS (SELECT 1 FROM discovery_operations WHERE connection_id=OLD.id AND status IN ('authorized_pending','dispatching')) BEGIN SELECT RAISE(ABORT,'active discovery'); END`,
		`CREATE TRIGGER IF NOT EXISTS discovery_initial_state BEFORE INSERT ON discovery_operations WHEN NEW.status!='authorized_pending' OR NEW.requests IS NOT NULL OR NEW.response_bytes IS NOT NULL OR NEW.snapshot_id!='' OR NEW.error_code!='' OR NEW.finished_at!='' OR json_extract(NEW.intent,'$.consent') IS NOT 1 OR json_extract(NEW.intent,'$.id') IS NOT NEW.id OR json_extract(NEW.intent,'$.connection_id') IS NOT NEW.connection_id BEGIN SELECT RAISE(ABORT,'invalid discovery reservation'); END`,
		`CREATE TRIGGER IF NOT EXISTS discovery_immutable_intent BEFORE UPDATE ON discovery_operations WHEN NEW.id IS NOT OLD.id OR NEW.connection_id IS NOT OLD.connection_id OR NEW.intent IS NOT OLD.intent OR NEW.created_at IS NOT OLD.created_at BEGIN SELECT RAISE(ABORT,'immutable discovery intent'); END`,
		`CREATE TRIGGER IF NOT EXISTS discovery_transition BEFORE UPDATE ON discovery_operations WHEN NOT ((OLD.status='authorized_pending' AND NEW.status IN ('dispatching','outcome_unknown')) OR (OLD.status='dispatching' AND NEW.status IN ('succeeded','failed','outcome_unknown'))) OR (NEW.status='succeeded' AND NOT EXISTS (SELECT 1 FROM mcp_tool_snapshots WHERE id=NEW.snapshot_id AND operation_id=NEW.id)) OR (NEW.status!='succeeded' AND NEW.snapshot_id!='') OR (NEW.status IN ('succeeded','failed','outcome_unknown') AND NEW.finished_at='') BEGIN SELECT RAISE(ABORT,'invalid discovery transition'); END`,
		`CREATE TRIGGER IF NOT EXISTS discovery_no_delete BEFORE DELETE ON discovery_operations BEGIN SELECT RAISE(ABORT,'discovery history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS snapshot_version BEFORE INSERT ON mcp_tool_snapshots WHEN NOT EXISTS (SELECT 1 FROM mcp_connections c JOIN discovery_operations o ON o.connection_id=c.id WHERE c.id=NEW.connection_id AND c.revision=NEW.connection_revision AND o.id=NEW.operation_id AND o.status='dispatching' AND json_extract(o.intent,'$.connection_revision')=NEW.connection_revision) BEGIN SELECT RAISE(ABORT,'stale snapshot'); END`,
		`CREATE TRIGGER IF NOT EXISTS snapshot_no_update BEFORE UPDATE ON mcp_tool_snapshots BEGIN SELECT RAISE(ABORT,'immutable snapshot'); END`,
		`CREATE TRIGGER IF NOT EXISTS snapshot_no_delete BEFORE DELETE ON mcp_tool_snapshots BEGIN SELECT RAISE(ABORT,'snapshot history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS skill_revision_no_update BEFORE UPDATE ON skill_revisions BEGIN SELECT RAISE(ABORT,'immutable skill revision'); END`,
		`CREATE TRIGGER IF NOT EXISTS skill_revision_no_delete BEFORE DELETE ON skill_revisions BEGIN SELECT RAISE(ABORT,'skill history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS git_observation_version BEFORE INSERT ON git_observations WHEN NOT EXISTS (SELECT 1 FROM git_repositories WHERE id=NEW.repository_id AND revision=NEW.repository_revision) BEGIN SELECT RAISE(ABORT,'stale git observation'); END`,
		`CREATE TRIGGER IF NOT EXISTS git_observation_no_update BEFORE UPDATE ON git_observations BEGIN SELECT RAISE(ABORT,'immutable git observation'); END`,
		`CREATE TRIGGER IF NOT EXISTS git_observation_no_delete BEFORE DELETE ON git_observations BEGIN SELECT RAISE(ABORT,'git history is append-only'); END`,
	}
	for _, statement := range statements {
		if _, err = tx.Exec(statement); err != nil {
			return fmt.Errorf("initialize capabilities: %w", err)
		}
	}
	return tx.Commit()
}
