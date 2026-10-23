import { request } from "./api";
import type { C4Profile } from "./c4";
import type { CreateRequest, Mission } from "./types";

export type CodingTaskApproval =
  "readiness" | "baseline" | "context" | "execute" | "delivery";

export interface CodingTaskCapability {
  enabled: boolean;
  available: boolean;
  reason: string;
  profile: C4Profile;
  limits: { max_calls: number; max_repairs: number };
  provider_families: string[];
  approval_required: CodingTaskApproval[];
}

const approvals: CodingTaskApproval[] = [
  "readiness",
  "baseline",
  "context",
  "execute",
  "delivery",
];

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function parseCodingTaskCapability(
  value: unknown,
): CodingTaskCapability | null {
  if (!isRecord(value) || !isRecord(value.profile) || !isRecord(value.limits))
    return null;
  const profile = value.profile;
  const limits = value.limits;
  const required = value.approval_required;
  if (
    typeof value.enabled !== "boolean" ||
    typeof value.available !== "boolean" ||
    typeof value.reason !== "string" ||
    profile.schema_version !== 1 ||
    profile.control_version !== 1 ||
    profile.id !== "c4_go_model_v1" ||
    profile.mode !== "docker" ||
    profile.platform !== "linux/arm64" ||
    profile.verifier !== "/opt/foundry/verify-go-v1" ||
    typeof profile.docker_path !== "string" ||
    typeof profile.endpoint !== "string" ||
    typeof profile.image_id !== "string" ||
    !Number.isSafeInteger(limits.max_calls) ||
    (limits.max_calls as number) < 1 ||
    !Number.isSafeInteger(limits.max_repairs) ||
    (limits.max_repairs as number) < 0 ||
    !Array.isArray(value.provider_families) ||
    !value.provider_families.every((item) => typeof item === "string") ||
    !Array.isArray(required) ||
    required.length !== approvals.length ||
    !approvals.every((item) => required.includes(item))
  )
    return null;
  return value as unknown as CodingTaskCapability;
}

export const codingTaskApi = {
  capability: async () => {
    const value = await request<unknown>("/api/coding-tasks");
    const capability = parseCodingTaskCapability(value);
    if (!capability) throw new Error("编码能力元数据缺失或格式不受支持");
    return capability;
  },
  create: (body: CreateRequest) =>
    request<{ mission: Mission }>("/api/coding-tasks", {
      method: "POST",
      body: JSON.stringify(body),
    }),
};
