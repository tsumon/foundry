import type { MissionRolePolicySnapshot } from "./rolePolicyTypes";

export const roles = [
  "main",
  "researcher",
  "planner",
  "worker",
  "debugger",
  "qa",
  "auditor",
] as const;
export type Role = (typeof roles)[number];
export const roleNames: Record<Role, string> = {
  main: "Main",
  researcher: "Researcher",
  planner: "Planner",
  worker: "Worker",
  debugger: "Debugger",
  qa: "QA",
  auditor: "Auditor",
};
export interface DisplayProfile {
  id: string;
  name: string;
  protocol: string;
  model: string;
  route_id?: string;
  account_id?: string;
  account_revision?: number;
  credential_version?: number;
}
export type BillingMode =
  "subscription" | "api_credits" | "metered_api" | "unknown";
export interface SnapshotAccount {
  id: string;
  revision: number;
  name: string;
  provider: string;
  billing_mode: BillingMode;
  enabled: boolean;
  identity_status: "unverified";
  usage_status: "unknown";
  inference_enabled: false;
}
export interface ModelAccount extends SnapshotAccount {
  base_url: string;
  auth_method: "api_key" | "openai_siwc";
  current_credential_version: number;
  created_at: string;
  updated_at: string;
}
export interface ModelCredential {
  account_id: string;
  version: number;
  operation_id: string;
  state: "pending" | "available" | "failed" | "interrupted" | "revoked";
  cleanup_pending: boolean;
  created_at: string;
  updated_at: string;
}
export interface RoleModelSelection {
  profile: DisplayProfile;
  account?: SnapshotAccount;
  route?: ModelRoute;
  authorization?: ModelAuthorizationBinding;
}
export type ModelAcceptanceReason =
  | "live_provider_access_pending_confirmation"
  | "account_disabled"
  | "route_missing"
  | "route_invalid"
  | "credential_metadata_unavailable"
  | "authorization_unavailable"
  | "authorization_expired"
  | "inference_scope_missing"
  | "extra_usage_local_setting_unknown"
  | "remote_billing_unverified"
  | "task_approval_required";
export interface OpenAISubscriptionAdapterPreparation {
  state: "implemented_disabled";
  validation: "local_simulation_only";
}
export interface OpenAIProductionAdmissionPreparation {
  state: "implemented_disabled";
  validation: "local_simulation_only";
  native_keychain: "not_checked";
  operator_activation: "unavailable" | "explicit_startup";
}
export interface OpenAIPreparation {
  scope: "openai_subscription_stage0";
  endpoint_policy: "exact_official" | "owned_simulation_required" | "invalid";
  credential_storage: "not_checked";
  remote_billing: "unverified";
  quota: "unknown";
  real_connection: "blocked";
  real_catalog: "blocked";
  real_inference: "blocked";
  subscription_adapter?: OpenAISubscriptionAdapterPreparation;
  production_admission?: OpenAIProductionAdmissionPreparation;
}
export interface ModelAccountAcceptance {
  account_id: string;
  account_revision: number;
  scope: "account_routes_only";
  live_access: "blocked";
  quota: "unknown";
  checks: {
    phase: "local_metadata" | "login" | "catalog" | "inference" | "task";
    state: "ready" | "blocked" | "not_evaluated";
    reason_codes: ModelAcceptanceReason[];
  }[];
  routes: {
    id: string;
    adapter: string;
    environment: "live" | "simulation" | "unknown";
    local_metadata: "ready" | "blocked";
    local_extra_usage: "off" | "unknown";
    remote_extra_usage: "unverified";
    reason_codes: ModelAcceptanceReason[];
  }[];
  openai_preparation?: OpenAIPreparation;
}

export type ModelAcceptancePhase = "connection" | "catalog" | "inference";
export type ModelAcceptanceState =
  | "pending"
  | "approved"
  | "running"
  | "succeeded"
  | "failed"
  | "unknown"
  | "rejected"
  | "expired"
  | "invalidated";
export interface ModelAcceptanceRoute {
  id: string;
  adapter: string;
  environment: "simulation" | "live";
  spend_class: string;
  base_url: string;
  connection_purpose?: "identity_only" | "subscription_only";
  connection_mode?: "browser_handoff";
}
export interface ModelAcceptanceAccount {
  id: string;
  revision: number;
  name: string;
  provider: string;
  base_url: string;
  auth_method: ModelAccount["auth_method"];
  billing_mode: BillingMode;
  enabled: boolean;
  current_credential_version: number;
  created_at: string;
  updated_at: string;
}
export interface ModelAcceptanceAuthorization {
  binding: ModelAuthorizationBinding;
  state: string;
  generation: number;
  expires_at: string;
}
export interface ModelAcceptanceMaterial {
  production_admission?: ModelProductionAdmission;
  phase: ModelAcceptancePhase;
  connection_purpose?: "identity_only" | "subscription_only";
  connection_mode?: "browser_handoff";
  login_timeout_seconds?: 600;
  requested_scopes?:
    | ["openid", "profile", "email"]
    | [
        "openid",
        "profile",
        "email",
        "offline_access",
        "resource.invoke",
        "chatgpt.tokens.use.direct",
      ];
  requested_resource?: "https://api.openai.com/v1";
  account: ModelAcceptanceAccount;
  route: ModelRoute;
  credential_version: number;
  authorization?: ModelAcceptanceAuthorization;
  prerequisite_id?: string;
  connection_id?: string;
  model?: string;
  prompt_digest?: string;
  data_summary: string;
  created_at: string;
  expires_at: string;
  deadline_seconds: 30;
  max_attempts: 1;
}
export interface ModelAcceptanceCatalogEntry {
  id: string;
  display_name: string;
}
export interface ModelAcceptanceOperation {
  id: string;
  account_id: string;
  revision: number;
  phase: ModelAcceptancePhase;
  state: ModelAcceptanceState;
  material: ModelAcceptanceMaterial;
  material_digest: string;
  attempts: number;
  claim_sequence: number;
  source: "local_simulation" | "production_connection";
  certainty: "not_sent" | "completed" | "rejected" | "unknown";
  code?: string;
  started_at?: string;
  finished_at?: string;
  result_account_revision?: number;
  result_snapshot_digest?: string;
  catalog: ModelAcceptanceCatalogEntry[];
  input_tokens?: number;
  output_tokens?: number;
  usage_known: boolean;
  quota: "unknown";
  remote_billing: "unverified";
  live_access: "blocked" | "connection_only";
}
export interface ModelProductionAdmission {
  generation: string;
  profile: "openai_siwc_subscription_v1";
}
export interface ModelAcceptanceHistory {
  operations: ModelAcceptanceOperation[];
  simulation_enabled: boolean;
  legacy_siwc_simulation_enabled?: boolean;
  production_connection_enabled?: boolean;
  production_admission?: ModelProductionAdmission;
  routes: ModelAcceptanceRoute[];
}
export type ConnectionSessionState =
  | "waiting"
  | "exchanging"
  | "available"
  | "declined"
  | "failed"
  | "canceled"
  | "expired";
export interface ConnectionSession {
  operation_id: string;
  account_id: string;
  state: ConnectionSessionState;
  expires_at: string;
  handoff_url?: string;
}
export interface ModelAcceptanceOperationResponse {
  operation: ModelAcceptanceOperation;
  connection_session?: ConnectionSession;
}
export interface ModelAcceptanceProposal {
  phase: ModelAcceptancePhase;
  connection_purpose?: "identity_only" | "subscription_only";
  route_id: string;
  expected_account_revision: number;
  model?: string;
  prerequisite_id?: string;
}

export type ModelRouteEnvironment = "live" | "simulation";
export type ModelRouteSpendClass =
  "explicit_api" | "interactive_package" | "subscription_only";
export interface ModelRoute {
  id: string;
  account_id: string;
  adapter: string;
  protocol: string;
  base_url: string;
  environment: ModelRouteEnvironment;
  spend_class: ModelRouteSpendClass;
  extra_usage: "off" | "unknown";
  created_at: string;
}
export interface ModelAuthorizationBinding {
  id: string;
  account_id: string;
  issuer: string;
  subject: string;
  client_id: string;
  spend_class: string;
  scopes: string[];
}
export interface ModelAuthorization extends ModelAuthorizationBinding {
  state: string;
  generation: number;
  expires_at: string;
}
export interface ModelAuthorizationFlow {
  operation_id: string;
  state: string;
  handoff_url: string;
  expires_at: string;
}
export interface ModelRemoteRevocation {
  authorization_id: string;
  outcome: "confirmed" | "unconfirmed";
  updated_at: string;
}
export interface ModelRevocation {
  authorization_id: string;
  local_state: string;
  remote_state: "confirmed" | "unconfirmed";
  updated_at: string;
}
export interface ModelAdapterRuntime {
  id: string;
  provider: string;
  auth_method: ModelAccount["auth_method"];
  protocol: string;
  spend_class: ModelRouteSpendClass;
  implemented: boolean;
  live_enabled: boolean;
}
export interface ModelRuntime {
  live_authorization: boolean;
  live_discovery: boolean;
  live_inference: boolean;
  simulation_inference: boolean;
  simulation_authorization: boolean;
  automatic_paid_fallback: boolean;
  adapters: ModelAdapterRuntime[];
}
export interface MissionModelSnapshot {
  schema_version: number;
  roles: Record<Role, RoleModelSelection>;
  digest: string;
}
export interface Profile extends DisplayProfile {
  kind: "direct" | "compatible" | "magpie";
  protocol: "chat_completions" | "responses" | "messages";
  base_url: string;
  has_key: boolean;
  account?: ModelAccount;
}
export interface ProfileInput {
  id?: string;
  name: string;
  kind: Profile["kind"];
  protocol: Profile["protocol"];
  base_url: string;
  model: string;
  route_id?: string;
  account_id?: string;
  api_key?: string;
}
export interface Settings {
  default_profile_id: string;
  role_bindings: Partial<Record<Role, string>>;
  model_policy_decision_id?: string;
}
export interface Spec {
  scope: string[];
  protected: string[];
  acceptance: string[];
  image: string;
  image_id: string;
  profile_id: string;
  research: boolean;
  max_calls: number;
}
export interface FileMeta {
  path: string;
  sha256: string;
  bytes: number;
}
export interface Step {
  id: string;
  title: string;
  description: string;
  depends_on: string[];
  files: string[];
  status: string;
}
export interface Plan {
  revision: number;
  summary: string;
  steps: Step[];
}
export interface Finding {
  severity: string;
  summary: string;
  paths: string[];
  sources?: ResearchSource[];
}
export interface ResearchSource {
  kind: "mcp";
  invocation_id: string;
  digest: string;
}
export interface Research {
  summary: string;
  findings: Finding[];
  unknowns: string[];
}
export interface Change {
  path: string;
  before: string;
  after: string;
}
export interface CommandResult {
  command: string;
  output: string;
  exit_code: number;
  timed_out: boolean;
}
export interface Verification {
  id: string;
  candidate_digest: string;
  image_id: string;
  created_at: string;
  acceptance_revision: number;
  results: CommandResult[];
  passed: boolean;
}
export interface Review {
  role: Role;
  passed: boolean;
  summary: string;
  findings: Finding[];
  candidate_digest: string;
}
export interface Message {
  role: string;
  content: string;
  created_at: string;
}
export interface Approval {
  kind: string;
  digest: string;
  created_at: string;
  revision: number;
  plan_revision?: number;
}
export interface Budget {
  max_calls: number;
  used_calls: number;
  input_tokens: number;
  output_tokens: number;
  usage_unknown: boolean;
}
export type ToolMode = "controlled_read_v1" | "controlled_mcp_read_v2";
export interface SkillBinding {
  id: string;
  revision: number;
  digest: string;
  name: string;
  role: Role;
}
export interface GitSourceBinding {
  repository_id: string;
  repository_revision: number;
  observation_id: string;
  root: string;
  base_commit: string;
}
export interface ReadBudget {
  max_turns: number;
  max_calls: number;
  max_argument_bytes: number;
  max_result_bytes: number;
  deadline_seconds: number;
  max_total_result_bytes: number;
}
export interface TaskBindings {
  git: GitSourceBinding;
  skills: SkillBinding[];
  policy_version: string;
  tools: string[];
  budget: ReadBudget;
}
export interface CapabilitySelection {
  repository_id: string;
  repository_revision: number;
  observation_id: string;
  skills: SkillBinding[];
}
export interface ReadGrant {
  id: string;
  role: Role;
  created_at: string;
  mission_id?: string;
  epoch?: number;
  session_id?: string;
  required_state?: string;
  binding_digest?: string;
  authority_digest?: string;
}
export interface EvidenceEnvelope {
  schema_version: number;
  kind: string;
  invocation_id: string;
  mission_id: string;
  epoch: number;
  producer: string;
  captured_at: string;
  digest: string;
  bytes: number;
  content_type: string;
  binding_digest: string;
  truncated: boolean;
}
export interface Invocation {
  id: string;
  mission_id: string;
  epoch: number;
  session_id: string;
  ordinal: number;
  kind: string;
  role: Role;
  name: string;
  binding_digest: string;
  authority_digest: string;
  grant_id?: string;
  input: EvidenceEnvelope;
  result?: EvidenceEnvelope;
  status: string;
  error_code?: string;
  reserved_result_bytes: number;
  created_at: string;
  dispatched_at?: string;
  finished_at?: string;
  usage_known: boolean;
  input_tokens: number;
  output_tokens: number;
  model_receipt?: ModelCallReceipt;
}
export interface ModelCallReceipt {
  request_id?: string;
  resolved_model?: string;
  credential_generation?: number;
  http_status?: number;
  provider_code?: string;
  provider_param?: string;
  body_shape?: string;
  retry_after_seconds?: number;
  certainty: "completed" | "not_sent" | "rejected" | "unknown";
}
export interface InvocationDetail {
  invocation: Invocation;
  input_preview: string;
  result_preview?: string;
  input_truncated: boolean;
  result_truncated: boolean;
  input_redacted?: boolean;
  result_redacted?: boolean;
}

export interface MCPBudget {
  max_calls: number;
  max_argument_bytes: number;
  max_result_bytes: number;
  max_total_result_bytes: number;
  deadline_seconds: number;
  max_requests: number;
  max_pages: number;
  max_tools: number;
  max_response_bytes: number;
  max_total_response_bytes: number;
  max_event_bytes: number;
  max_notifications: number;
  request_timeout_seconds: number;
  cleanup_timeout_seconds: number;
}
export interface MCPSelectedTool {
  name: string;
  definition_digest: string;
  policy_digest: string;
}
export interface MCPSelection {
  connection_id: string;
  connection_revision: number;
  snapshot_id: string;
  snapshot_digest: string;
  tools: MCPSelectedTool[];
}
export interface LocalMCPTest {
  enabled: boolean;
  eligible: boolean;
  reason?: string;
  test_only: true;
  endpoint?: string;
  selection?: MCPSelection;
  budget: MCPBudget;
}
export interface MissionMCPTool {
  name: string;
  definition_digest: string;
  input_schema_digest: string;
  output_schema_digest?: string;
  policy: unknown;
}
export interface MissionMCPSelection {
  connection_id: string;
  connection_revision: number;
  endpoint: string;
  protocol: string;
  snapshot_id: string;
  snapshot_digest: string;
  tools: MissionMCPTool[];
  budget: MCPBudget;
  test_only: boolean;
}
export interface ResearchStatus {
  current: boolean;
  can_decide: boolean;
  can_continue: boolean;
  why_not?: string;
}
export interface MCPRecipient {
  role: Role;
  name: string;
  protocol: string;
  model: string;
}
export interface MCPDecisionView extends ResearchStatus {
  proposal_id: string;
  proposal_digest: string;
  revision: number;
  purpose: string;
  endpoint: string;
  tool: string;
  arguments: unknown;
  policy: unknown;
  source_invocation_id: string;
  source_digest: string;
  test_only: true;
  used_calls: number;
  remaining_calls: number;
  budget: MCPBudget;
  recipients: MCPRecipient[];
}
export interface MCPDecisionReceipt {
  mission_id: string;
  request_id: string;
  proposal_id: string;
  proposal_digest: string;
  expected_revision: number;
  approved: boolean;
  invocation_id?: string;
  grant_id?: string;
}
export interface MCPInvocationView {
  id: string;
  proposal_id: string;
  status: string;
  phase: string;
  error_code?: string;
  tool: string;
  arguments: unknown;
  result_digest?: string;
  result?: unknown;
  requests: number;
  response_bytes: number;
  duration_millis: number;
  created_at: string;
  finished_at?: string;
  test_only: true;
}
export interface Delivery {
  operation_id?: string;
  patch_sha256?: string;
  report_sha256?: string;
  patch_path: string;
  report_path: string;
  candidate_digest: string;
  created_at: string;
}
export interface Mission {
  model_snapshot?: MissionModelSnapshot;
  model_snapshot_status?: "valid" | "invalid_or_unsupported";
  model_policy_snapshot?: MissionRolePolicySnapshot;
  model_policy_snapshot_status?: "valid" | "invalid_or_unsupported";
  model_runtime?: ModelRuntime;
  workspace_mode?: string;
  tool_mode?: string;
  task_bindings?: TaskBindings;
  mcp_selection?: MissionMCPSelection;
  research_status?: ResearchStatus;
  candidate_generation?: GenerationRef;
  id: string;
  title: string;
  goal: string;
  repo_path: string;
  base_commit: string;
  state: string;
  stage: string;
  revision: number;
  epoch: number;
  spec: Spec;
  profiles: Record<Role, DisplayProfile>;
  context_manifest: FileMeta[];
  research: Research | null;
  plan: Plan | null;
  messages: Message[];
  changes: Change[];
  evidence: Verification[];
  reviews: Review[];
  approvals: Approval[];
  budget: Budget;
  candidate_digest: string;
  error: string;
  created_at: string;
  updated_at: string;
  delivery: Delivery | null;
  run_archives?: RunArchive[];
}
const accountModes: Record<string, string> = {
  account_snapshot_v1: "",
  generations_account_snapshot_v1: "generations_v1",
  generations_controlled_read_account_snapshot_v1:
    "generations_controlled_read_v1",
  generations_controlled_mcp_read_account_snapshot_v1:
    "generations_controlled_mcp_read_v2",
  account_snapshot_v2: "",
  generations_account_snapshot_v2: "generations_v1",
  generations_controlled_read_account_snapshot_v2:
    "generations_controlled_read_v1",
  generations_controlled_mcp_read_account_snapshot_v2:
    "generations_controlled_mcp_read_v2",
};
export const baseWorkspaceMode = (mode?: string) =>
  Object.hasOwn(accountModes, mode ?? "") ? accountModes[mode!] : (mode ?? "");
export const isAccountWorkspaceMode = (mode?: string) =>
  Object.hasOwn(accountModes, mode ?? "");
export const isControlledReadMission = (
  mission: Pick<Mission, "workspace_mode" | "tool_mode">,
) =>
  (baseWorkspaceMode(mission.workspace_mode) ===
    "generations_controlled_read_v1" &&
    mission.tool_mode === "controlled_read_v1") ||
  isControlledMCPReadMission(mission);
export const isControlledMCPReadMission = (
  mission: Pick<Mission, "workspace_mode" | "tool_mode">,
) =>
  baseWorkspaceMode(mission.workspace_mode) ===
    "generations_controlled_mcp_read_v2" &&
  mission.tool_mode === "controlled_mcp_read_v2";
export const usesGenerationWorkspace = (
  mission: Pick<Mission, "workspace_mode" | "tool_mode">,
) =>
  (baseWorkspaceMode(mission.workspace_mode) === "generations_v1" &&
    !mission.tool_mode) ||
  isControlledReadMission(mission);
export const isSupportedMissionMode = (
  mission: Pick<Mission, "workspace_mode" | "tool_mode">,
) =>
  (!baseWorkspaceMode(mission.workspace_mode) && !mission.tool_mode) ||
  usesGenerationWorkspace(mission);
export interface RunArchive {
  candidate_generation?: GenerationRef;
  epoch: number;
  plan_revision: number;
  revision: number;
  archived_at: string;
  state: string;
  error: string;
  image_id: string;
  candidate_digest: string;
  plan: Plan | null;
  research: Research | null;
  changes: Change[];
  evidence: Verification[];
  reviews: Review[];
  delivery: Delivery | null;
}
export interface RunEvent {
  seq: number;
  mission_id: string;
  kind: string;
  created_at: string;
  revision: number;
}
export interface CreateRequest {
  title: string;
  goal: string;
  repo_path: string;
  spec: Spec;
  workspace_mode?:
    | "generations_v1"
    | "generations_controlled_read_v1"
    | "generations_controlled_mcp_read_v2";
  tool_mode?: ToolMode;
  capability_selection?: CapabilitySelection;
  mcp_selection?: MCPSelection;
}
export interface GenerationRef {
  id: string;
  digest: string;
}
export interface PreparedExport {
  patch_path: string;
  report_path: string;
  patch_sha256: string;
  report_sha256: string;
  report_input: string;
}
export interface LocalOperationResult {
  generation?: GenerationRef;
  changes?: Change[];
  export?: PreparedExport;
}
export interface LocalOperation {
  id: string;
  mission_id: string;
  kind: string;
  status: string;
  expected_revision: number;
  epoch: number;
  plan_revision: number;
  before: GenerationRef;
  base_digest: string;
  intent_digest: string;
  approval_digest: string;
  export_report?: string;
  result?: LocalOperationResult;
  error?: string;
  created_at: string;
  updated_at: string;
}
export interface PlanRevision {
  mission_id: string;
  plan_revision: number;
  parent_plan_revision: number;
  epoch: number;
  mission_revision: number;
  source: string;
  reason: string;
  plan: Plan;
  plan_digest: string;
  created_at: string;
}
export type MissionAction = "plan" | "approve" | "pause" | "deliver" | "chat";
export const stateNames: Record<string, string> = {
  draft: "待授权上下文",
  planning: "正在规划",
  awaiting_approval: "等待计划审批",
  running: "正在构建",
  verifying: "正在验证",
  awaiting_delivery: "等待交付审批",
  delivered: "已交付",
  pausing: "正在暂停",
  paused: "已暂停",
  blocked: "已阻塞",
};
export const activeStates = ["planning", "running", "verifying", "pausing"];
