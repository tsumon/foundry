import type { GenerationRef, Mission } from "./types";

export interface C4Profile {
  schema_version: number;
  id: string;
  mode: string;
  image_id: string;
  platform: string;
  docker_path: string;
  endpoint: string;
  verifier: string;
}
export interface C4Fact {
  revision: number;
  at: string;
}
export interface C4Operation {
  schema_version: number;
  id: string;
  kind: string;
  mission_id: string;
  epoch: number;
  plan_revision: number;
  base_commit: string;
  base_digest: string;
  candidate_generation?: GenerationRef;
  candidate_digest?: string;
  profile_digest: string;
  material_digest: string;
  container_name: string;
  container_id?: string;
  owner: string;
  state: string;
  prepared_at: string;
  approval?: C4Fact & { digest: string };
  dispatch?: C4Fact;
  created?: C4Fact;
  started?: C4Fact;
  result?: C4Fact & {
    class: string;
    exit_code?: number;
    tests: number;
    output: string;
    truncated: boolean;
  };
  cleanup?: Array<
    C4Fact & { state: string; container_id: string; owner: string }
  >;
}
export interface C4Status {
  mission_id: string;
  revision: number;
  profile: C4Profile | null;
  operations: C4Operation[] | null;
  read_only: boolean;
  simulation: boolean;
  real_execution: boolean;
}
type Material = {
  revision: number;
  operation_id: string;
  material_digest: string;
};
export type C4Action =
  | { action: "prepare"; revision: number }
  | (Material & { action: "approve" | "execute" })
  | (Material & { action: "cleanup"; container_id: string });

export const hasC4 = (m: Mission) => !!m.spec.c4 || !!m.c4_operations?.length;
export const c4Supported = (m: Mission) =>
  m.spec.c4?.schema_version === 1 &&
  m.spec.c4.id === "c4_go_v1" &&
  m.spec.c4.mode === "synthetic";
export const c4CleanupState = (o: C4Operation) =>
  o.cleanup?.at(-1)?.state ?? "pending";
export function c4Passed(o?: C4Operation): boolean {
  if (!o) return false;
  const cleanup = o.cleanup?.at(-1);
  return (
    o.schema_version === 1 &&
    ["baseline", "candidate"].includes(o.kind) &&
    o.state === "finished" &&
    !!o.owner &&
    !!o.approval?.digest &&
    !!o.dispatch &&
    !!o.created &&
    !!o.started &&
    o.result?.class === "passed" &&
    o.result.exit_code === 0 &&
    o.result.tests > 0 &&
    !!o.container_id &&
    cleanup?.state === "confirmed" &&
    cleanup.container_id === o.container_id &&
    cleanup.owner === o.owner
  );
}
export function c4BaselinePassed(m: Mission): boolean {
  const o = m.c4_operations?.filter((o) => o.kind === "baseline").at(-1);
  return (
    !!o &&
    c4Passed(o) &&
    o.base_commit === m.base_commit &&
    (!m.repository_import || o.base_digest === m.repository_import.base_digest)
  );
}
export const c4AdvanceAllowed = (m: Mission) =>
  c4Supported(m) &&
  c4BaselinePassed(m) &&
  (m.c4_operations ?? []).every(
    (o) =>
      o.schema_version === 1 &&
      o.state === "finished" &&
      c4CleanupState(o) === "confirmed",
  );
export function c4CandidatePassed(m: Mission): boolean {
  const o = m.c4_operations?.filter((o) => o.kind === "candidate").at(-1);
  return (
    !!o &&
    c4Passed(o) &&
    o.epoch === m.epoch &&
    o.plan_revision === m.plan?.revision &&
    !!m.candidate_digest &&
    o.candidate_digest === m.candidate_digest &&
    !!m.candidate_generation &&
    o.candidate_generation?.id === m.candidate_generation.id &&
    o.candidate_generation.digest === m.candidate_generation.digest
  );
}
export function c4OperationLabel(o: C4Operation): string {
  if (o.schema_version !== 1) return "不支持的记录版本 · 不能推断成功";
  if (c4Passed(o)) return "模拟验证通过 · 非真实执行";
  if (o.result)
    return `${o.result.class} · ${c4CleanupState(o) === "confirmed" ? "清理已确认，结果不变" : "清理未确认，不能继续"}`;
  if (o.state === "prepared" && !o.approval) return "待批准基线材料 · 未执行";
  if (o.state === "approved" && o.approval && !o.dispatch)
    return "已批准但尚未执行";
  return `${o.state || "未知状态"} · 尚无完成结果`;
}
const c4EventNames: Record<string, string> = {
  "c4.prepared": "C4 材料已准备",
  "c4.approved": "C4 批准绑定已记录",
  "c4.dispatch": "C4 派发已记录",
  "c4.created": "C4 创建事实已记录",
  "c4.started": "C4 启动事实已记录",
  "c4.result": "C4 结果已记录",
  "c4.cleanup": "C4 清理事实已记录",
  "c4.outcome_unknown": "C4 结果未知（不重放）",
  "c4.recovery_stopped": "C4 恢复已停止（不重放）",
};
export const c4EventLabel = (kind: string) =>
  c4EventNames[kind] ? `${c4EventNames[kind]} · ${kind}` : kind;
