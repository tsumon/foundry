import { request } from "./api";
import type {
  LocalMCPTest,
  MCPDecisionReceipt,
  MCPDecisionView,
  MCPInvocationView,
  Mission,
} from "./types";

const json = (body: unknown): RequestInit => ({
  method: "POST",
  body: JSON.stringify(body),
});
const id = (value: string) => encodeURIComponent(value);

export const mcpTaskApi = {
  localTest: () => request<LocalMCPTest>("/api/mcp-local-test"),
  decision: (missionID: string, signal?: AbortSignal) =>
    request<{ decision: MCPDecisionView | null }>(
      `/api/missions/${id(missionID)}/mcp-decision`,
      { signal },
    ),
  decide: (
    missionID: string,
    proposalID: string,
    body: {
      expected_revision: number;
      proposal_digest: string;
      decision: "approve" | "reject";
      request_id: string;
    },
  ) =>
    request<{ mission: Mission }>(
      `/api/missions/${id(missionID)}/mcp-proposals/${id(proposalID)}/decision`,
      json(body),
    ),
  receipt: (missionID: string, requestID: string) =>
    request<{ decision: MCPDecisionReceipt }>(
      `/api/missions/${id(missionID)}/mcp-decisions/${id(requestID)}`,
    ),
  history: (missionID: string, signal?: AbortSignal) =>
    request<{
      invocations: MCPInvocationView[];
      decisions: MCPDecisionReceipt[];
    }>(`/api/missions/${id(missionID)}/mcp-invocations`, { signal }),
  invocation: (missionID: string, invocationID: string, signal?: AbortSignal) =>
    request<{ invocation: MCPInvocationView }>(
      `/api/missions/${id(missionID)}/mcp-invocations/${id(invocationID)}`,
      { signal },
    ),
  continueResearch: (
    missionID: string,
    expectedRevision: number,
    intentID: string,
  ) =>
    request<{ mission: Mission }>(
      `/api/missions/${id(missionID)}/research-continue`,
      json({ expected_revision: expectedRevision, intent_id: intentID }),
    ),
};
