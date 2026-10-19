import { request } from "./api";
import type {
  CapabilitySnapshot,
  DiscoveryResult,
  GitInput,
  GitObservation,
  GitRepository,
  MCPConnection,
  MCPInput,
  SkillInput,
  SkillRevision,
} from "./capabilityTypes";

const post = (body: unknown): RequestInit => ({
  method: "POST",
  body: JSON.stringify(body),
});
const idPath = (id: string) => encodeURIComponent(id);
export const capabilityApi = {
  snapshot: () => request<CapabilitySnapshot>("/api/capabilities"),
  saveMCP: (input: MCPInput) =>
    request<{ connection: MCPConnection }>(
      "/api/capabilities/mcp",
      post(input),
    ),
  discover: (id: string, revision: number, requestID: string) =>
    request<DiscoveryResult>(
      `/api/capabilities/mcp/${idPath(id)}/discover`,
      post({
        expected_revision: revision,
        request_id: requestID,
        consent: true,
      }),
    ),
  discovery: (requestID: string) =>
    request<DiscoveryResult>(
      `/api/capabilities/discoveries/${idPath(requestID)}`,
    ),
  saveSkill: (input: SkillInput) =>
    request<{ revision: SkillRevision }>(
      "/api/capabilities/skills",
      post(input),
    ),
  skillRevision: (id: string, revision: number) =>
    request<{ revision: SkillRevision }>(
      `/api/capabilities/skills/${idPath(id)}/revisions/${revision}`,
    ),
  saveGit: (input: GitInput) =>
    request<{ repository: GitRepository }>(
      "/api/capabilities/git",
      post(input),
    ),
  inspectGit: (id: string, revision: number) =>
    request<{ observation: GitObservation }>(
      `/api/capabilities/git/${idPath(id)}/inspect`,
      post({ expected_revision: revision, consent: true }),
    ),
};
