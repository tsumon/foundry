import type { CatalogSnapshot } from "./catalogTypes";

export interface MCPConnection {
  id: string;
  revision: number;
  name: string;
  endpoint: string;
  protocol: string;
  enabled: boolean;
  has_credential: boolean;
  credential_revision: number;
  created_at: string;
  updated_at: string;
}
export interface MCPTool {
  name: string;
  title: string;
  description: string;
  input_schema: unknown;
  output_schema: unknown;
  annotations: unknown;
  // Server-generated text preserves JSON numbers beyond JavaScript precision.
  // Optional for older responses; never reconstruct it from parsed raw fields.
  input_schema_json?: string;
  output_schema_json?: string;
  annotations_json?: string;
  definition_digest: string;
}
export interface MCPToolSnapshot {
  id: string;
  connection_id: string;
  connection_revision: number;
  protocol: string;
  server_name: string;
  server_version: string;
  tools: MCPTool[];
  digest: string;
  observed_at: string;
}
export interface DiscoveryBudget {
  deadline_seconds: number;
  request_timeout_seconds: number;
  cleanup_timeout_seconds: number;
  max_requests: number;
  max_pages: number;
  max_response_bytes: number;
  max_total_response_bytes: number;
  max_event_bytes: number;
  max_notifications: number;
  max_tools: number;
  max_schema_bytes: number;
  max_json_depth: number;
  max_cursor_bytes: number;
}
export interface DiscoveryOperation {
  id: string;
  connection_id: string;
  connection_revision: number;
  endpoint: string;
  credential_revision: number;
  consent: boolean;
  authorized_at: string;
  status: string;
  budget: DiscoveryBudget;
  requests: number | null;
  response_bytes: number | null;
  snapshot_id: string;
  error_code: string;
  created_at: string;
  finished_at: string;
}
export interface DiscoveryResult {
  operation: DiscoveryOperation;
  snapshot?: MCPToolSnapshot;
}
export interface SkillFile {
  path: string;
  content: string;
}
export interface SkillRevision {
  id: string;
  revision: number;
  name: string;
  description: string;
  source: string;
  files: SkillFile[];
  digest: string;
  created_at: string;
}
export interface GitRepository {
  id: string;
  revision: number;
  name: string;
  path: string;
  project_id: string;
  created_at: string;
  updated_at: string;
}
export interface GitObservation {
  id: string;
  repository_id: string;
  repository_revision: number;
  root: string;
  branch: string;
  head: string;
  detached: boolean;
  unborn: boolean;
  checked_at: string;
  status: string;
  error_code: string;
  working_tree_status: string;
}
export interface CapabilitySnapshot {
  connections: MCPConnection[];
  snapshots: MCPToolSnapshot[];
  discoveries: DiscoveryOperation[];
  skills: SkillRevision[];
  repositories: GitRepository[];
  git_observations: GitObservation[];
  builtin_capabilities: {
    id: string;
    name: string;
    description: string;
    agent_callable: boolean;
  }[];
  tool_execution: boolean;
  skill_execution: boolean;
  git_write: boolean;
}
export interface MCPInput {
  id?: string;
  expected_revision: number;
  name: string;
  endpoint: string;
  enabled: boolean;
  api_key?: string;
  clear_key: boolean;
}
export interface SkillInput {
  id?: string;
  expected_revision: number;
  source: string;
  files: SkillFile[];
}
export interface GitInput {
  id?: string;
  expected_revision: number;
  name: string;
  path: string;
  project_id: string;
}
export interface ProjectChoices {
  snapshot: CatalogSnapshot | null;
  ready: boolean;
  loading: boolean;
  error: string;
}
export interface CapabilityPanelProps {
  enabled: boolean;
  snapshot: CapabilitySnapshot;
  update: (
    change: (previous: CapabilitySnapshot) => CapabilitySnapshot,
  ) => void;
}
