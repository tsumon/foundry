package store

import "fmt"

func (s *Store) initializeResearchFlow() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS research_turn_applications (source_invocation_id TEXT PRIMARY KEY REFERENCES mcp_proposal_sources(model_invocation_id),mission_id TEXT NOT NULL REFERENCES missions(id),version INTEGER NOT NULL,epoch INTEGER NOT NULL,session_id TEXT NOT NULL,kind TEXT NOT NULL,target_id TEXT NOT NULL,data BLOB NOT NULL CHECK(json_valid(data)),digest TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS research_application_target ON research_turn_applications(mission_id,kind,target_id) WHERE kind!='final'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS research_final_session ON research_turn_applications(mission_id,epoch,session_id) WHERE kind='final'`,
		`CREATE TABLE IF NOT EXISTS research_consumptions (consumer_invocation_id TEXT PRIMARY KEY REFERENCES mcp_proposal_sources(model_invocation_id),mission_id TEXT NOT NULL REFERENCES missions(id),version INTEGER NOT NULL,epoch INTEGER NOT NULL,session_id TEXT NOT NULL,observation_kind TEXT NOT NULL,observation_invocation_id TEXT NOT NULL,data BLOB NOT NULL CHECK(json_valid(data)),digest TEXT NOT NULL,UNIQUE(mission_id,observation_kind,observation_invocation_id))`,
		`CREATE TABLE IF NOT EXISTS research_planner_checkpoints (source_invocation_id TEXT PRIMARY KEY REFERENCES research_turn_applications(source_invocation_id),planner_invocation_id TEXT NOT NULL UNIQUE REFERENCES invocations(id),mission_id TEXT NOT NULL REFERENCES missions(id),version INTEGER NOT NULL,epoch INTEGER NOT NULL,session_id TEXT NOT NULL,data BLOB NOT NULL CHECK(json_valid(data)),digest TEXT NOT NULL,UNIQUE(mission_id,epoch,session_id))`,
		`CREATE TABLE IF NOT EXISTS research_plan_applications (planner_invocation_id TEXT PRIMARY KEY REFERENCES research_planner_checkpoints(planner_invocation_id),mission_id TEXT NOT NULL REFERENCES missions(id),version INTEGER NOT NULL,epoch INTEGER NOT NULL,session_id TEXT NOT NULL,data BLOB NOT NULL CHECK(json_valid(data)),digest TEXT NOT NULL)`,
		`CREATE TRIGGER IF NOT EXISTS research_application_owner_v2 BEFORE INSERT ON research_turn_applications WHEN NEW.version=2 AND NOT EXISTS(SELECT 1 FROM mcp_proposal_sources s JOIN invocations i ON i.id=s.model_invocation_id WHERE s.model_invocation_id=NEW.source_invocation_id AND s.mission_id=NEW.mission_id AND s.epoch=NEW.epoch AND s.session_id=NEW.session_id AND s.version=1 AND json_extract(s.data,'$.purpose')='research_turn_v2' AND i.status='succeeded') BEGIN SELECT RAISE(ABORT,'invalid research application owner'); END`,
		`CREATE TRIGGER IF NOT EXISTS research_consumption_owner_v2 BEFORE INSERT ON research_consumptions WHEN NEW.version=2 AND NOT EXISTS(SELECT 1 FROM mcp_proposal_sources s JOIN invocations i ON i.id=s.model_invocation_id WHERE s.model_invocation_id=NEW.consumer_invocation_id AND s.mission_id=NEW.mission_id AND s.epoch=NEW.epoch AND s.session_id=NEW.session_id AND s.version=1 AND json_extract(s.data,'$.purpose')='research_turn_v2' AND i.status='authorized_pending') BEGIN SELECT RAISE(ABORT,'invalid research consumer owner'); END`,
		`CREATE TRIGGER IF NOT EXISTS research_planner_owner_v2 BEFORE INSERT ON research_planner_checkpoints WHEN NEW.version=2 AND NOT EXISTS(SELECT 1 FROM research_turn_applications a JOIN invocations i ON i.id=NEW.planner_invocation_id WHERE a.source_invocation_id=NEW.source_invocation_id AND a.kind='final' AND a.version=2 AND a.mission_id=NEW.mission_id AND a.epoch=NEW.epoch AND a.session_id=NEW.session_id AND i.mission_id=NEW.mission_id AND i.epoch=NEW.epoch AND i.session_id=NEW.session_id AND i.kind='model' AND i.role='planner' AND i.status='authorized_pending') BEGIN SELECT RAISE(ABORT,'invalid research planner owner'); END`,
		`CREATE TRIGGER IF NOT EXISTS research_plan_owner_v2 BEFORE INSERT ON research_plan_applications WHEN NEW.version=2 AND NOT EXISTS(SELECT 1 FROM research_planner_checkpoints c JOIN invocations i ON i.id=c.planner_invocation_id WHERE c.planner_invocation_id=NEW.planner_invocation_id AND c.version=2 AND c.mission_id=NEW.mission_id AND c.epoch=NEW.epoch AND c.session_id=NEW.session_id AND i.status='succeeded') BEGIN SELECT RAISE(ABORT,'invalid research plan owner'); END`,
		// New trigger names are essential for databases which already installed v1.
		`CREATE TRIGGER IF NOT EXISTS generic_mcp_shared_quota_v2 BEFORE INSERT ON invocations WHEN NEW.kind='repository' AND EXISTS(SELECT 1 FROM missions WHERE id=NEW.mission_id AND json_extract(data,'$.tool_mode')='controlled_mcp_read_v2') AND ((SELECT count(*) FROM mcp_invocations WHERE mission_id=NEW.mission_id)+(SELECT count(*) FROM invocations WHERE mission_id=NEW.mission_id AND kind='repository')>=3 OR NEW.reserved_result_bytes+(SELECT COALESCE(sum(reserved_result_bytes),0) FROM mcp_invocations WHERE mission_id=NEW.mission_id)+(SELECT COALESCE(sum(reserved_result_bytes),0) FROM invocations WHERE mission_id=NEW.mission_id AND kind='repository')>196608) BEGIN SELECT RAISE(ABORT,'shared tool quota'); END`,
		`CREATE TRIGGER IF NOT EXISTS mcp_source_claim_v2 BEFORE UPDATE OF status ON invocations WHEN NEW.status='dispatching' AND NEW.kind='model' AND NEW.role='researcher' AND EXISTS(SELECT 1 FROM missions WHERE id=NEW.mission_id AND json_extract(data,'$.tool_mode')='controlled_mcp_read_v2') AND NOT EXISTS(SELECT 1 FROM mcp_proposal_sources WHERE model_invocation_id=NEW.id AND mission_id=NEW.mission_id AND epoch=NEW.epoch AND session_id=NEW.session_id AND version=1 AND json_extract(data,'$.purpose')='research_turn_v2') BEGIN SELECT RAISE(ABORT,'research source checkpoint required'); END`,
		`CREATE TRIGGER IF NOT EXISTS mcp_source_purpose_v2 BEFORE INSERT ON mcp_proposal_sources WHEN NEW.version=1 AND EXISTS(SELECT 1 FROM missions WHERE id=NEW.mission_id AND json_extract(data,'$.tool_mode')='controlled_mcp_read_v2') AND json_extract(NEW.data,'$.purpose') IS NOT 'research_turn_v2' BEGIN SELECT RAISE(ABORT,'invalid v2 source purpose'); END`,
		`CREATE TRIGGER IF NOT EXISTS research_planner_claim_v2 BEFORE UPDATE OF status ON invocations WHEN NEW.status='dispatching' AND NEW.kind='model' AND NEW.role='planner' AND EXISTS(SELECT 1 FROM missions WHERE id=NEW.mission_id AND json_extract(data,'$.tool_mode')='controlled_mcp_read_v2') AND NOT EXISTS(SELECT 1 FROM research_planner_checkpoints WHERE planner_invocation_id=NEW.id AND mission_id=NEW.mission_id AND epoch=NEW.epoch AND session_id=NEW.session_id AND version=2) BEGIN SELECT RAISE(ABORT,'planner checkpoint required'); END`,
	}
	for _, table := range researchTables {
		statements = append(statements, fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s_no_update BEFORE UPDATE ON %s BEGIN SELECT RAISE(ABORT,'immutable research record'); END`, table, table), fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s_no_delete BEFORE DELETE ON %s BEGIN SELECT RAISE(ABORT,'append-only research record'); END`, table, table))
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("initialize research flow: %w", err)
		}
	}
	return tx.Commit()
}

var researchTables = []string{"research_turn_applications", "research_consumptions", "research_planner_checkpoints", "research_plan_applications"}
