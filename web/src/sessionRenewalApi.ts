import { request } from "./api";
import type { ModelAuthorizationBinding } from "./types";
export interface RenewalMaterial {
  id: string;
  account_id: string;
  account_revision: number;
  revision: number;
  binding: ModelAuthorizationBinding;
  resource: string;
  token_endpoint: string;
  expires_at: string;
  credential_store: string;
  approval_scope: string;
  schedule: string;
  failure_behavior: string;
}
export interface RenewalStatus {
  account_id: string;
  account_revision: number;
  revision: number;
  enabled: boolean;
  registration_retained: boolean;
  access_state: string;
  state: string;
  due_at?: string;
  material?: RenewalMaterial;
  receipt?: {
    stage: string;
    category: string;
    certainty: string;
    old_generation: number;
    new_generation: number;
  };
}
export type RenewalAction =
  | { action: "prepare"; expected_account_revision: number }
  | { action: "approve"; material_id: string; expected_revision: number }
  | { action: "disable"; expected_revision: number };
const path = (id: string) =>
  `/api/model-accounts/${encodeURIComponent(id)}/session-renewal`;
export const sessionRenewalApi = {
  read: (id: string) => request<RenewalStatus>(path(id)),
  mutate: (id: string, action: RenewalAction) =>
    request<RenewalStatus>(path(id), {
      method: "POST",
      body: JSON.stringify(action),
    }),
};
