import { request } from "./api";
import type { Mission } from "./types";

export interface IdeaRequest {
  title: string;
  goal: string;
  acceptance: string[];
  profile_id: string;
  max_calls: number;
}

export const ideaApi = {
  capability: () =>
    request<{ available: boolean; reason: string }>("/api/idea-tasks"),
  create: (body: IdeaRequest) =>
    request<{ mission: Mission }>("/api/idea-tasks", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  preview: (id: string, signal?: AbortSignal) =>
    request<{ html: string; candidate_digest: string }>(
      `/api/missions/${encodeURIComponent(id)}/app-preview`,
      { signal },
    ),
};
