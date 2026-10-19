import { request } from "./api";
import type { Settings } from "./types";
import type {
  RoleModelPolicy,
  RolePolicyDecision,
  RolePolicyInput,
  RolePolicyProposal,
  RolePolicyState,
} from "./rolePolicyTypes";

const json = (method: string, body: unknown): RequestInit => ({
  method,
  body: JSON.stringify(body),
});

export const rolePolicyApi = {
  state: () => request<{ state: RolePolicyState }>("/api/role-model-policy"),
  save: (
    expectedRevision: number,
    profilesDigest: string,
    input: RolePolicyInput,
  ) =>
    request<{ policy: RoleModelPolicy }>(
      "/api/role-model-policy",
      json("PUT", {
        expected_revision: expectedRevision,
        profiles_digest: profilesDigest,
        roles: input.roles,
      }),
    ),
  propose: () =>
    request<{ proposal: RolePolicyProposal }>(
      "/api/role-model-policy/proposals",
      json("POST", {}),
    ),
  confirm: (proposal: RolePolicyProposal) =>
    request<{
      decision: RolePolicyDecision;
      settings: Settings;
      applied: boolean;
    }>(
      `/api/role-model-policy/proposals/${encodeURIComponent(proposal.id)}/confirm`,
      json("POST", { digest: proposal.digest, confirm: true }),
    ),
};
