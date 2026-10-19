import type { Role } from "./types";

export const rolePolicyCapabilities = [
  "structured_output",
  "reasoning",
  "coding",
  "long_context",
  "review",
  "concise_explanation",
] as const;
export type RolePolicyCapability = (typeof rolePolicyCapabilities)[number];

export const rolePolicySpendClasses = [
  "subscription_only",
  "interactive_package",
  "explicit_api",
  "unknown",
] as const;
export type RolePolicySpendClass = (typeof rolePolicySpendClasses)[number];

export type RolePolicyPreference = "candidate_order" | "subscription_first";
export type RolePolicyReason =
  | "profile_missing"
  | "profile_changed"
  | "required_capability_missing"
  | "spend_class_not_allowed"
  | "account_disabled"
  | "route_invalid"
  | "credential_unavailable"
  | "authorization_unavailable"
  | "extra_usage_not_off"
  | "evidence_invalid"
  | "selection_invalid";
export type RolePolicyExecutionNotice =
  "production_live_gate_closed" | "credentials_and_quota_unknown";
export type RolePolicyEvidenceStatus =
  "completed" | "failed" | "outcome_unknown" | "interrupted_before_dispatch";
export type RolePolicyEvidenceEnvironment = "live" | "simulation" | "unknown";
export type RolePolicyEvidenceErrorCode =
  | "auth_invalid"
  | "entitlement_denied"
  | "quota_exhausted"
  | "rate_limited"
  | "request_rejected"
  | "server_error"
  | "transport_unknown"
  | "model_transport_unknown"
  | "response_incomplete"
  | "live_provider_access_pending_confirmation"
  | "dispatch_interrupted"
  | "stale_dispatch"
  | "result_seal_failed"
  | "interrupted_before_dispatch"
  | "other_failure";

export interface RolePolicyCandidateInput {
  profile_id: string;
  capabilities: RolePolicyCapability[];
  rationale: string;
  evidence_invocation_id?: string;
}
export interface RolePolicyCandidate extends RolePolicyCandidateInput {
  profile_digest: string;
}
export interface RolePolicyRoleInput {
  candidates: RolePolicyCandidateInput[];
  required_capabilities: RolePolicyCapability[];
  allowed_spend_classes: RolePolicySpendClass[];
  preference: RolePolicyPreference;
}
export interface RolePolicyRole extends Omit<
  RolePolicyRoleInput,
  "candidates"
> {
  candidates: RolePolicyCandidate[];
}
export interface RolePolicyInput {
  roles: Record<Role, RolePolicyRoleInput>;
}
export interface RoleModelPolicy {
  revision: number;
  created_at: string;
  roles: Record<Role, RolePolicyRole>;
}
export interface RolePolicyEvidence {
  invocation_id: string;
  mission_id: string;
  role: Role;
  status: RolePolicyEvidenceStatus;
  error_code?: RolePolicyEvidenceErrorCode;
  environment: RolePolicyEvidenceEnvironment;
  finished_at: string;
}
export interface RolePolicyCandidateResult {
  profile_id: string;
  profile_digest: string;
  spend_class: RolePolicySpendClass;
  eligible: boolean;
  reasons: RolePolicyReason[];
  capabilities: RolePolicyCapability[];
  rationale: string;
  evidence?: RolePolicyEvidence;
  execution_notice: RolePolicyExecutionNotice;
}
export interface RolePolicyRoleResult {
  selected_profile_id: string;
  required_capabilities: RolePolicyCapability[];
  candidates: RolePolicyCandidateResult[];
}
export interface RolePolicyProposal {
  id: string;
  policy_revision: number;
  created_at: string;
  settings_digest: string;
  inputs_digest: string;
  digest: string;
  confirmable: boolean;
  roles: Record<Role, RolePolicyRoleResult>;
}
export interface RolePolicyDecision {
  id: string;
  proposal_id: string;
  confirmed_at: string;
  proposal: RolePolicyProposal;
}
export interface RolePolicyState {
  policy: RoleModelPolicy;
  profiles_digest: string;
  latest_proposal?: RolePolicyProposal;
  active_decision?: RolePolicyDecision;
}
export interface MissionRolePolicySnapshot {
  schema_version: number;
  decision_id: string;
  policy_revision: number;
  recommendation_digest: string;
  confirmed_at: string;
  digest: string;
  roles: Record<Role, RolePolicyRoleResult>;
}
