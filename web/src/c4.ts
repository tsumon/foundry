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
const c4Opaque = /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/;
const c4SHA = /^[a-f0-9]{64}$/;
const c4Commit = /^([a-f0-9]{40}|[a-f0-9]{64})$/;
const c4Timestamp =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;
const c4OpaqueValue = (value: unknown): value is string =>
  typeof value === "string" && c4Opaque.test(value);

const c4Absolute = (value: unknown) => {
  if (
    typeof value !== "string" ||
    !value.startsWith("/") ||
    value === "/" ||
    value.endsWith("/") ||
    /[\\\0\r\n\t ]/.test(value)
  )
    return false;
  return value
    .slice(1)
    .split("/")
    .every((part) => part !== "" && part !== "." && part !== "..");
};

const c4Time = (value: unknown) =>
  typeof value === "string" &&
  c4Timestamp.test(value) &&
  !Number.isNaN(Date.parse(value));

const c4Fact = (value?: C4Fact): value is C4Fact =>
  !!value &&
  Number.isInteger(value.revision) &&
  value.revision > 0 &&
  c4Time(value.at);

const c4Facts = (o: C4Operation): Array<C4Fact | undefined> => [
  o.approval,
  o.dispatch,
  o.created,
  o.started,
  o.result,
  ...(o.cleanup ?? []),
];

function c4LifecycleOrdered(o: C4Operation): boolean {
  let lastRevision = 0;
  let lastTime = Date.parse(o.prepared_at);
  for (const [index, fact] of c4Facts(o).entries()) {
    if (!c4Fact(fact) || fact.revision < lastRevision) return false;
    lastRevision = fact.revision;
    // Candidate approval is copied from earlier execute consent. All other
    // stage times are ordered from preparation, matching the domain contract.
    if (o.kind === "candidate" && index === 0) continue;
    const current = Date.parse(fact.at);
    if (current < lastTime) return false;
    lastTime = current;
  }
  return true;
}

export const c4Supported = (m: Mission) => {
  const p = m.spec.c4;
  if (!p) return false;
  const socketPath = p.endpoint?.startsWith("unix://")
    ? p.endpoint.slice("unix://".length)
    : "";
  return (
    p.schema_version === 1 &&
    p.id === "c4_go_v1" &&
    p.mode === "synthetic" &&
    /^sha256:[a-f0-9]{64}$/.test(p.image_id) &&
    p.platform === "linux/arm64" &&
    c4Absolute(p.docker_path) &&
    p.endpoint === `unix://${socketPath}` &&
    !/[%?#]/.test(socketPath) &&
    c4Absolute(socketPath) &&
    p.verifier === "/opt/foundry/verify-go-v1"
  );
};
export const c4CleanupState = (o: C4Operation) =>
  o.cleanup?.at(-1)?.state ?? "pending";

function c4OperationBindings(o: C4Operation): boolean {
  if (
    o.schema_version !== 1 ||
    !c4OpaqueValue(o.id) ||
    !c4OpaqueValue(o.mission_id) ||
    !Number.isInteger(o.epoch) ||
    o.epoch < 0 ||
    !Number.isInteger(o.plan_revision) ||
    o.plan_revision < 0 ||
    !c4Commit.test(o.base_commit) ||
    !c4SHA.test(o.base_digest) ||
    !c4SHA.test(o.profile_digest) ||
    !c4SHA.test(o.material_digest) ||
    !c4OpaqueValue(o.container_name) ||
    !c4OpaqueValue(o.owner) ||
    !c4Time(o.prepared_at)
  )
    return false;
  if (o.kind === "baseline")
    return !o.candidate_generation && !o.candidate_digest;
  return (
    o.kind === "candidate" &&
    o.plan_revision > 0 &&
    !!o.candidate_generation &&
    c4OpaqueValue(o.candidate_generation.id) &&
    c4SHA.test(o.candidate_generation.digest) &&
    o.candidate_generation.digest === o.candidate_digest
  );
}

function c4MissionOperationBindings(m: Mission, o: C4Operation): boolean {
  // These are display guards over returned facts, not a replacement for the
  // backend's material/profile digest recomputation or execution authority.
  return (
    c4Supported(m) &&
    c4OperationBindings(o) &&
    o.mission_id === m.id &&
    o.epoch <= m.epoch &&
    o.base_commit === m.base_commit &&
    !!m.repository_import &&
    o.base_digest === m.repository_import.base_digest &&
    c4Facts(o).every(
      (fact) => !fact || (c4Fact(fact) && fact.revision <= m.revision),
    )
  );
}

export function c4Passed(o?: C4Operation): boolean {
  if (!o) return false;
  const cleanup = o.cleanup?.at(-1);
  const approvalDigest = o.approval?.digest;
  const cleanupValid =
    !!o.cleanup?.length &&
    o.cleanup.length <= 64 &&
    o.cleanup.every(
      (fact, index) =>
        c4Fact(fact) &&
        (fact.state === "failed" || fact.state === "confirmed") &&
        fact.container_id === o.container_id &&
        fact.owner === o.owner &&
        (fact.state !== "confirmed" || index === o.cleanup!.length - 1),
    );
  return (
    c4OperationBindings(o) &&
    c4LifecycleOrdered(o) &&
    o.state === "finished" &&
    c4SHA.test(approvalDigest ?? "") &&
    (o.kind !== "baseline" || approvalDigest === o.material_digest) &&
    c4Fact(o.approval) &&
    c4Fact(o.dispatch) &&
    c4Fact(o.created) &&
    c4Fact(o.started) &&
    c4Fact(o.result) &&
    o.result?.class === "passed" &&
    o.result.exit_code === 0 &&
    Number.isInteger(o.result.tests) &&
    o.result.tests > 0 &&
    typeof o.result.output === "string" &&
    typeof o.result.truncated === "boolean" &&
    c4OpaqueValue(o.container_id) &&
    cleanupValid &&
    cleanup?.state === "confirmed" &&
    cleanup.container_id === o.container_id &&
    cleanup.owner === o.owner
  );
}
export function c4BaselinePassed(m: Mission): boolean {
  const o = m.c4_operations?.filter((o) => o.kind === "baseline").at(-1);
  return (
    !!o &&
    c4MissionOperationBindings(m, o) &&
    c4Passed(o) &&
    o.kind === "baseline"
  );
}
export const c4AdvanceAllowed = (m: Mission) =>
  c4Supported(m) &&
  c4BaselinePassed(m) &&
  (m.c4_operations ?? []).every(
    (o) =>
      c4MissionOperationBindings(m, o) &&
      o.state === "finished" &&
      c4CleanupState(o) === "confirmed",
  );

function c4CandidateApprovalBound(m: Mission, o: C4Operation): boolean {
  return (
    !!o.approval &&
    m.approvals.some(
      (approval) =>
        approval.kind === "execute" &&
        approval.plan_revision === o.plan_revision &&
        approval.digest === o.approval!.digest &&
        approval.revision === o.approval!.revision &&
        approval.created_at === o.approval!.at,
    )
  );
}

export function c4CandidatePassed(m: Mission): boolean {
  const o = m.c4_operations?.filter((o) => o.kind === "candidate").at(-1);
  return (
    !!o &&
    c4MissionOperationBindings(m, o) &&
    c4Passed(o) &&
    o.kind === "candidate" &&
    c4CandidateApprovalBound(m, o) &&
    o.epoch === m.epoch &&
    o.plan_revision === m.plan?.revision &&
    !!m.candidate_digest &&
    o.candidate_digest === m.candidate_digest &&
    !!m.candidate_generation &&
    o.candidate_generation?.id === m.candidate_generation.id &&
    o.candidate_generation.digest === m.candidate_generation.digest
  );
}
export function c4OperationLabel(m: Mission, o: C4Operation): string {
  if (!c4MissionOperationBindings(m, o)) return "配置不支持 · 仅显示原始事实";
  if (o.kind === "candidate" && !c4CandidateApprovalBound(m, o))
    return "审批绑定缺失或不匹配 · 不能确认通过";
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
