import {
  isAccountWorkspaceMode,
  roles,
  type Mission,
  type MissionAction,
  type Role,
} from "./types";
import { isModelBackedC4 } from "./c4";

export function invalidRolePolicySnapshot(mission: Mission): boolean {
  const policyStatus = mission.model_policy_snapshot_status;
  const policy = mission.model_policy_snapshot;
  if (policyStatus === "invalid_or_unsupported") return true;
  if (policyStatus === "valid") {
    if (
      !policy ||
      policy.schema_version !== 1 ||
      !policy.decision_id ||
      !Number.isSafeInteger(policy.policy_revision) ||
      policy.policy_revision < 1 ||
      !/^[a-f0-9]{64}$/.test(policy.recommendation_digest) ||
      !/^[a-f0-9]{64}$/.test(policy.digest) ||
      !policy.confirmed_at ||
      !policy.roles ||
      Object.keys(policy.roles).length !== 7 ||
      roles.some((role) => {
        const result = policy.roles[role];
        const candidate = result?.candidates?.[0];
        return (
          !result?.selected_profile_id ||
          !Array.isArray(result.required_capabilities) ||
          result.candidates?.length !== 1 ||
          candidate?.profile_id !== result.selected_profile_id ||
          candidate.eligible !== true ||
          candidate.reasons?.length !== 0
        );
      })
    )
      return true;
  } else if (policy) {
    return true;
  }
  return false;
}

export function invalidModelSnapshot(mission: Mission): boolean {
  if (invalidRolePolicySnapshot(mission)) return true;
  const bound = isAccountWorkspaceMode(mission.workspace_mode);
  if (mission.model_snapshot_status === "invalid_or_unsupported") return true;
  if (!bound)
    return (
      !!mission.model_snapshot ||
      roles.some((r) => !!mission.profiles?.[r]?.account_id)
    );
  const snapshot = mission.model_snapshot;
  const schema = mission.workspace_mode?.endsWith("account_snapshot_v2")
    ? 2
    : 1;
  if (
    !snapshot ||
    mission.model_snapshot_status !== "valid" ||
    snapshot.schema_version !== schema ||
    !/^[a-f0-9]{64}$/.test(snapshot.digest) ||
    !snapshot.roles ||
    Object.keys(snapshot.roles).length !== 7
  )
    return true;
  return roles.some((role) => {
    const item = snapshot.roles[role],
      profile = mission.profiles?.[role];
    if (
      !item?.profile ||
      !profile ||
      [
        "id",
        "name",
        "protocol",
        "model",
        "route_id",
        "account_id",
        "account_revision",
        "credential_version",
      ].some(
        (k) =>
          item.profile[k as keyof typeof profile] !==
          profile[k as keyof typeof profile],
      )
    )
      return true;
    if (!profile.account_id)
      return (
        !!item.account ||
        !!item.route ||
        !!item.authorization ||
        !!profile.route_id
      );
    if (
      !item.account ||
      item.account.id !== profile.account_id ||
      item.account.revision !== profile.account_revision ||
      !Number.isSafeInteger(profile.credential_version ?? 0) ||
      (profile.credential_version ?? 0) < 0
    )
      return true;
    if (schema === 1)
      return !!profile.route_id || !!item.route || !!item.authorization;
    if (
      !profile.route_id ||
      !item.route ||
      item.route.id !== profile.route_id ||
      item.route.account_id !== profile.account_id ||
      item.route.protocol !== profile.protocol
    )
      return true;
    const oauth = item.route.adapter === "openai_siwc_responses";
    return oauth
      ? !item.authorization ||
          item.authorization.account_id !== profile.account_id ||
          !item.authorization.id
      : !!item.authorization;
  });
}
export function accountActionBlockReason(
  mission: Mission,
  action: MissionAction,
): string {
  if (invalidModelSnapshot(mission)) return "模型快照不完整或不受支持。";
  const relevant: Role[] =
    action === "plan"
      ? ["planner", ...(mission.spec.research ? ["researcher" as const] : [])]
      : action === "chat"
        ? ["main"]
        : action === "approve"
          ? ["worker", "debugger", "qa", "auditor"]
          : [];
  for (const role of relevant) {
    const selection = mission.model_snapshot?.roles?.[role];
    if (!selection?.profile?.account_id) continue;
    if (mission.model_snapshot?.schema_version !== 2)
      return "schema1 账户任务不具备推理执行权。";
    const route = selection.route;
    if (!route) return "冻结路由缺失，不能将账户连接视为可运行。";
    if (route.environment === "live") {
      if (!isModelBackedC4(mission))
        return "冻结路由指向真实环境；本次真实推理门禁保持关闭。";
      if (!mission.model_runtime?.live_inference)
        return "当前编码运行时未提供真实模型推理能力。";
      if (mission.model_runtime.automatic_paid_fallback)
        return "运行时报告自动付费降级，编码任务保持禁用。";
      const adapter = mission.model_runtime.adapters.find(
        (item) => item.id === route.adapter,
      );
      if (!adapter?.implemented || !adapter.live_enabled)
        return "冻结模型适配器当前未获真实推理准入。";
    } else if (route.environment === "simulation") {
      if (!mission.model_runtime?.simulation_inference)
        return "当前进程未提供可用的模拟推理能力。";
    } else return "冻结路由环境不受支持。";
    if (route.extra_usage !== "off")
      return "额外用量未明确关闭，不能视为可运行。";
    if (route.adapter === "openai_siwc_responses") {
      const scopes = selection.authorization?.scopes ?? [];
      if (
        !selection.authorization?.id ||
        !scopes.includes("resource.invoke") ||
        !scopes.includes("chatgpt.tokens.use.direct")
      )
        return "冻结授权绑定或直接调用权限缺失。";
    } else if ((selection.profile.credential_version ?? 0) < 1) {
      return "冻结凭据版本缺失，不能使用当前版本代替。";
    }
  }
  return "";
}
export function accountActionBlocked(
  mission: Mission,
  action: MissionAction,
): boolean {
  return !!accountActionBlockReason(mission, action);
}
