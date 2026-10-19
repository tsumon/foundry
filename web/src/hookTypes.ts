export interface HookPoint {
  kind: string;
  label: string;
  description: string;
}

export interface HookRule {
  id: string;
  revision: number;
  name: string;
  enabled: boolean;
  event_kinds: string[];
  mission_id: string;
  action: "record_activity";
  effective_after_seq: number;
  created_at: string;
  updated_at: string;
}

export interface HookProcessorState {
  initialized_after_seq: number;
  cursor: number;
  latest_event_seq: number;
  pending_events: number;
  last_processed_at: string;
}

export interface HookRuntimeStatus {
  state: "not_started" | "running" | "degraded" | "stopped";
  last_attempt_at: string;
  last_success_at: string;
  error_code: string;
}

export interface HookSnapshot {
  hook_points: HookPoint[];
  rules: HookRule[];
  processor: HookProcessorState;
  runtime: HookRuntimeStatus;
}

export interface HookRecord {
  id: number;
  event_seq: number;
  mission_id: string;
  mission_revision: number;
  event_kind: string;
  event_created_at: string;
  rule_id: string;
  rule_revision: number;
  rule_name: string;
  action: "record_activity";
  handler_version: "record_activity/v1";
  status: "recorded";
  processed_at: string;
}

export interface HookRecordResponse {
  records: HookRecord[];
  next_before: number;
  processor: HookProcessorState;
  runtime: HookRuntimeStatus;
}

export interface HookRuleInput {
  id?: string;
  expected_revision: number;
  name: string;
  enabled: boolean;
  event_kinds: string[];
  mission_id: string;
  action: "record_activity";
}

export interface HookMissionChoice {
  id: string;
  title: string;
}
