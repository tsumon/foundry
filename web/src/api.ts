import type {
  CreateRequest,
  Invocation,
  InvocationDetail,
  Mission,
  MissionAction,
  LocalOperation,
  PlanRevision,
  Plan,
  Profile,
  ProfileInput,
  Settings,
  ReadGrant,
} from "./types";
import type { C4Action, C4Status } from "./c4";
export interface HealthResponse {
  status: string;
  read_only?: boolean;
  c4_controlled_only?: boolean;
  recovery_error?: string;
  version: string;
  capabilities: Record<string, boolean>;
}
export interface RevisePlanRequest {
  expected_mission_revision: number;
  expected_plan_revision: number;
  reason: string;
  plan: Omit<Plan, "revision" | "steps"> & {
    steps: Array<Omit<Plan["steps"][number], "status">>;
  };
}
export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public data?: unknown,
  ) {
    super(message);
  }
}
export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: {
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  const data = await response.json();
  if (!response.ok)
    throw new APIError(
      response.status,
      data.error?.code ?? "request_failed",
      data.error?.message ?? "请求失败，请检查本地服务",
      data,
    );
  return data as T;
}
const json = (method: string, body: unknown): RequestInit => ({
  method,
  body: JSON.stringify(body),
});
export async function initializeSession(): Promise<void> {
  await request("/api/session", json("POST", {}));
}
export const api = {
  c4: (id: string) =>
    request<C4Status>(`/api/missions/${encodeURIComponent(id)}/c4`),
  c4Action: (id: string, body: C4Action) =>
    request<{ mission: Mission }>(
      `/api/missions/${encodeURIComponent(id)}/c4`,
      json("POST", body),
    ),
  health: () => request<HealthResponse>("/api/health"),
  missions: () => request<{ missions: Mission[] | null }>("/api/missions"),
  mission: (id: string) =>
    request<{ mission: Mission }>(`/api/missions/${encodeURIComponent(id)}`),
  create: (body: CreateRequest) =>
    request<{ mission: Mission }>("/api/missions", json("POST", body)),
  action: (
    id: string,
    action: MissionAction,
    revision: number,
    message?: string,
  ) =>
    request<{ mission: Mission }>(
      `/api/missions/${encodeURIComponent(id)}/${action}`,
      json("POST", { revision, ...(message === undefined ? {} : { message }) }),
    ),
  plan: (id: string, revision: number, controlledConsent?: true) =>
    request<{ mission: Mission }>(
      `/api/missions/${encodeURIComponent(id)}/plan`,
      json("POST", {
        revision,
        ...(controlledConsent ? { controlled_consent: true } : {}),
      }),
    ),
  invocations: (id: string, before = 0, limit = 30, signal?: AbortSignal) =>
    request<{ invocations: Invocation[] | null }>(
      `/api/missions/${encodeURIComponent(id)}/invocations?before=${before}&limit=${limit}`,
      { signal },
    ),
  invocation: (id: string, invocationID: string, signal?: AbortSignal) =>
    request<{ invocation: InvocationDetail }>(
      `/api/missions/${encodeURIComponent(id)}/invocations/${encodeURIComponent(invocationID)}`,
      { signal },
    ),
  readGrants: (id: string, signal?: AbortSignal) =>
    request<{ read_grants: ReadGrant[] | null }>(
      `/api/missions/${encodeURIComponent(id)}/read-grants`,
      { signal },
    ),
  planRevisions: (id: string) =>
    request<{ plan_revisions: PlanRevision[] | null }>(
      `/api/missions/${encodeURIComponent(id)}/plan-revisions`,
    ),
  revisePlan: (id: string, body: RevisePlanRequest) =>
    request<{ mission: Mission; plan_revision: PlanRevision }>(
      `/api/missions/${encodeURIComponent(id)}/plan-revisions`,
      json("POST", body),
    ),
  localOperations: (id: string) =>
    request<{ local_operations: LocalOperation[] | null }>(
      `/api/missions/${encodeURIComponent(id)}/local-operations`,
    ),
  profiles: () =>
    request<{ profiles: Profile[] | null; settings: Settings }>(
      "/api/profiles",
    ),
  saveProfile: (body: ProfileInput) =>
    request<{ profile: Profile }>("/api/profiles", json("POST", body)),
  saveSettings: (
    body: Pick<Settings, "default_profile_id" | "role_bindings">,
  ) => request<{ settings: Settings }>("/api/settings", json("PUT", body)),
  probe: (id: string) =>
    request<{ models: string[] | null; inference_tested: false }>(
      `/api/profiles/${encodeURIComponent(id)}/probe`,
      json("POST", {}),
    ),
  discover: (base_url: string) =>
    request<{ available: boolean; models: string[] | null; message: string }>(
      "/api/magpie/discover",
      json("POST", { base_url }),
    ),
};
export const errorMessage = (error: unknown) =>
  error instanceof Error ? error.message : "请求未完成，请检查本地服务";
