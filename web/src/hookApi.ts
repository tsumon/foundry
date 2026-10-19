import { request } from "./api";
import type {
  HookRecordResponse,
  HookRule,
  HookRuleInput,
  HookSnapshot,
} from "./hookTypes";

export const hookApi = {
  snapshot: (signal?: AbortSignal) =>
    request<HookSnapshot>("/api/hooks", { signal }),
  saveRule: (input: HookRuleInput, signal?: AbortSignal) =>
    request<{ rule: HookRule }>("/api/hooks/rules", {
      method: "POST",
      body: JSON.stringify(input),
      signal,
    }),
  records: (
    missionID: string,
    before: number,
    limit = 50,
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams();
    if (missionID) query.set("mission_id", missionID);
    if (before > 0) query.set("before", String(before));
    query.set("limit", String(limit));
    return request<HookRecordResponse>(`/api/hooks/records?${query}`, {
      signal,
    });
  },
};
