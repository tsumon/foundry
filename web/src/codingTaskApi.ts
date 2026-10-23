import { request } from "./api";
import { c4ProfileSupported, type C4Profile } from "./c4";
import type { CreateRequest, Mission } from "./types";

export type CodingTaskApproval =
  "readiness" | "baseline" | "context" | "execute" | "delivery";

export interface CodingTaskCapability {
  enabled: boolean;
  available: boolean;
  reason: string;
  profile: C4Profile;
  default_backend?: "embedded_vm" | "docker";
  backends?: Array<{
    mode: "embedded_vm" | "docker";
    available: boolean;
    profile: C4Profile;
    prerequisite: string;
  }>;
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

function capabilityProfile(value: unknown, available: boolean): boolean {
  if (
    !isRecord(value) ||
    value.schema_version !== 1 ||
    value.control_version !== 1 ||
    value.id !== "c4_go_model_v1" ||
    value.verifier !== "/opt/foundry/verify-go-v1"
  )
    return false;
  if (value.mode === "embedded_vm") {
    if (
      value.image_id !== "" ||
      value.docker_path !== "" ||
      value.endpoint !== ""
    )
      return false;
    if (!available && value.embedded_vm === undefined && value.platform === "")
      return true;
    return c4ProfileSupported(value as unknown as C4Profile);
  }
  return (
    value.mode === "docker" &&
    value.embedded_vm === undefined &&
    value.platform === "linux/arm64" &&
    typeof value.docker_path === "string" &&
    typeof value.endpoint === "string" &&
    typeof value.image_id === "string"
  );
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
    !capabilityProfile(profile, value.available === true) ||
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
  if (
    value.default_backend !== undefined ||
    value.backends !== undefined ||
    profile.mode === "embedded_vm"
  ) {
    if (
      value.default_backend !== "embedded_vm" ||
      !Array.isArray(value.backends) ||
      value.backends.length !== 2 ||
      !value.backends.every(
        (backend) =>
          isRecord(backend) &&
          ["embedded_vm", "docker"].includes(backend.mode as string) &&
          typeof backend.available === "boolean" &&
          typeof backend.prerequisite === "string" &&
          capabilityProfile(backend.profile, backend.available) &&
          isRecord(backend.profile) &&
          backend.profile.mode === backend.mode,
      ) ||
      new Set(value.backends.map((b) => b.mode)).size !== 2
    )
      return null;
    const primary = value.backends.find(
      (b) => b.mode === value.default_backend,
    );
    if (
      !primary ||
      primary.available !== value.available ||
      JSON.stringify(primary.profile) !== JSON.stringify(profile)
    )
      return null;
  }
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
