import { useEffect, useState } from "react";
import {
  c4AdvanceAllowed,
  c4CandidatePassed,
  hasC4,
  isModelBackedC4,
} from "../c4";
import {
  activeStates,
  isControlledMCPReadMission,
  isControlledReadMission,
  isAccountWorkspaceMode,
  roleNames,
  roles,
  type Mission,
  type MissionAction,
} from "../types";
import { isMCPDecisionPending, missionStateName } from "../deliveryStatus";
import {
  verificationOutcome,
  verificationSourceLabel,
} from "../verificationSource";
import { ControlledTask } from "./ControlledTask";
import { ModelSnapshot } from "./ModelSnapshot";
import { codingReviewsCurrent } from "./VerificationView";
import {
  accountActionBlocked,
  accountActionBlockReason,
  invalidModelSnapshot,
} from "../modelSnapshot";
export function DecisionPanel({
  mission,
  busy,
  demo = false,
  onAction,
  onOpenMCPApproval,
  planApprovalBlocked = false,
  unsupportedWorkspaceMode = false,
  c4Decision,
  c4Available = false,
  c4ExecutionAvailable = c4Available,
}: {
  mission: Mission;
  busy: boolean;
  demo?: boolean;
  onAction: (action: MissionAction, controlledConsent?: true) => void;
  onOpenMCPApproval?: () => void;
  planApprovalBlocked?: boolean;
  unsupportedWorkspaceMode?: boolean;
  c4Decision?: React.ReactNode;
  c4Available?: boolean;
  c4ExecutionAvailable?: boolean;
}) {
  const [consent, setConsent] = useState(false);
  const [controlledConsent, setControlledConsent] = useState(false);
  useEffect(() => {
    setConsent(false);
    setControlledConsent(false);
  }, [mission.id, mission.revision]);
  const controlled = isControlledReadMission(mission);
  const mcpControlled = isControlledMCPReadMission(mission);
  const mcpDecisionPending = isMCPDecisionPending(mission);
  const invalidSnapshot = invalidModelSnapshot(mission);
  const planAccountBlocked = accountActionBlocked(mission, "plan");
  const executionAccountBlocked = accountActionBlocked(mission, "approve");
  unsupportedWorkspaceMode ||= invalidSnapshot;
  const planning = ["draft", "paused", "blocked"].includes(mission.state);
  const approval = mission.state === "awaiting_approval" && !!mission.plan;
  const replan =
    mission.coding_version === 1 &&
    planning &&
    (!!mission.replan_context ||
      mission.coding_attempts?.some((a) => a.status !== "succeeded"));
  const unresolved = mission.plan?.adjustments?.some((a) => a.unresolved);
  const evidence = mission.evidence?.at(-1);
  const c4Blocked =
    hasC4(mission) && (!c4Available || !c4AdvanceAllowed(mission));
  const modelBacked = isModelBackedC4(mission);
  const deliverable =
    mission.state === "awaiting_delivery" &&
    (!hasC4(mission) ||
      (c4Available &&
        c4AdvanceAllowed(mission) &&
        c4CandidatePassed(mission))) &&
    codingReviewsCurrent(mission) &&
    !!evidence?.passed &&
    evidence.candidate_digest === mission.candidate_digest &&
    ["qa", "auditor"].every((role) =>
      mission.reviews?.some(
        (review) =>
          review.role === role &&
          review.passed &&
          review.candidate_digest === mission.candidate_digest,
      ),
    );
  return (
    <section className="decision-panel">
      <p className="eyebrow">当前决策</p>
      <h2>
        {demo
          ? "演示中的决策视角"
          : planning && c4Blocked
            ? "检查 C4 基线与清理"
            : planning
              ? "检查共享上下文"
              : approval
                ? "请审阅执行计划"
                : mission.state === "awaiting_delivery"
                  ? "请审阅交付证据"
                  : missionStateName(mission)}
      </h2>
      <p className="muted small">
        任务 r{mission.revision}
        {mission.plan && ` · 计划 v${mission.plan.revision}`}
      </p>
      {demo ? (
        <div className="note">
          这里只展示工作产物的组织方式。演示不会调用模型、执行命令或生成真实交付。
        </div>
      ) : (
        <>
          {c4Decision}
          {c4Blocked && (
            <p className="warning-copy">
              {modelBacked
                ? `C4 尚无可继续的完整 ${mission.spec.c4?.mode === "embedded_vm" ? "内置 VM" : "Docker"} 基线与清理事实，或当前就绪未确认。模型路由配置不改变此门禁；上下文、执行与交付仍需各自批准。`
                : "C4 尚无可继续的完整基线与清理事实，或模拟能力未确认。上下文、执行与交付仍需各自批准；请先检查 C4 记录。"}
            </p>
          )}
          {unsupportedWorkspaceMode && (
            <div className="note warning-note">
              此任务使用当前版本不支持的工作区模式。任务保持可读，但规划、计划修订、执行、暂停、交付和模型调用均已禁用。
            </div>
          )}
          {controlled && <ControlledTask mission={mission} />}
          {(mission.model_snapshot ||
            mission.model_snapshot_status ||
            invalidSnapshot ||
            isAccountWorkspaceMode(mission.workspace_mode)) && (
            <div aria-label="本次审批模型依据">
              <ModelSnapshot mission={mission} compact />
            </div>
          )}
          {((planning && planAccountBlocked) ||
            (approval && executionAccountBlocked)) && (
            <p className="warning-copy">
              本次动作涉及账户绑定角色。
              {accountActionBlockReason(mission, planning ? "plan" : "approve")}
              此动作已禁用；不会替换模型或自动付费降级。
            </p>
          )}
          {mcpDecisionPending && onOpenMCPApproval && (
            <div className="note warning-note">
              <p>
                当前调研正在等待一次明确的 MCP
                调用决定。批准或拒绝都必须在任务审批视角完成。
              </p>
              <button type="button" onClick={onOpenMCPApproval}>
                转到当前 MCP 调用审批
              </button>
            </div>
          )}
          {planning && (
            <>
              <div className="note warning-note">
                {modelBacked ? (
                  "上下文批准会绑定冻结的账户、模型、消费类别与调用预算，并允许后端按角色发起受控模型调用。它不批准 readiness、baseline、执行或交付，也不保证远程额度与结果。"
                ) : hasC4(mission) ? (
                  "C4 上下文仅交给专用本地模拟模型；下方端点是合成声明，不联系远程服务。上下文同意仍独立于基线、计划与交付批准。"
                ) : (
                  <>
                    已批准的源码、目标与命令可能包含嵌入的秘密。文件名过滤不等于秘密扫描。授权后，这些上下文会发送至下方冻结的模型
                    {mcpControlled ? "接收连接" : "端点"}。
                  </>
                )}
              </div>
              {mission.state !== "draft" && (
                <p className="warning-copy">
                  重新规划将归档已有调研、计划、代码变更、验证、审查与交付记录，并重置当前候选；历史证据不能批准新交付，已消耗的调用预算保留。
                </p>
              )}
              <details open className="disclosure">
                <summary>
                  上下文文件清单（{mission.context_manifest?.length ?? 0}）
                </summary>
                <ul className="manifest">
                  {(mission.context_manifest ?? []).map((file) => (
                    <li key={file.path}>
                      <code>{file.path}</code>
                      <small>
                        {file.bytes} bytes · SHA-256 <code>{file.sha256}</code>
                      </small>
                    </li>
                  ))}
                </ul>
              </details>
              {!mission.model_snapshot && !mission.model_snapshot_status && (
                <details open className="disclosure">
                  <summary>
                    {mcpControlled
                      ? "冻结的角色与接收连接标识"
                      : "冻结的角色与发送端点"}
                  </summary>
                  <p className="muted small">
                    来自本任务快照；修改全局设置不会替换这些连接。
                    {mcpControlled &&
                      " 安全任务视图显示连接名称与 ID，不返回原始端点。"}
                  </p>
                  {roles.map((role) => {
                    const profile = mission.profiles?.[role];
                    const endpoint =
                      profile && "base_url" in profile
                        ? profile.base_url
                        : undefined;
                    return (
                      <div className="frozen-profile" key={role}>
                        <strong>
                          {roleNames[role]}
                          {role === "researcher" && !mission.spec.research
                            ? "（本任务未启用）"
                            : ""}
                        </strong>
                        <span>
                          {mcpControlled
                            ? profile
                              ? `${profile.name} · ${profile.id}`
                              : "未配置连接"
                            : (profile?.model ?? "未配置")}
                        </span>
                        <code>
                          {mcpControlled
                            ? "原始端点未在安全任务视图提供"
                            : typeof endpoint === "string"
                              ? endpoint
                              : "端点由安全任务视图隐藏"}
                        </code>
                        <small>
                          {mcpControlled && profile?.model
                            ? `${profile.model} · ${profile.protocol}`
                            : (profile?.protocol ?? "")}
                        </small>
                      </div>
                    );
                  })}
                </details>
              )}
            </>
          )}
          {(planning || approval || mission.state === "awaiting_delivery") && (
            <dl className="decision-facts">
              <dt>修改范围</dt>
              <dd>{(mission.spec.scope ?? []).join(" · ")}</dd>
              <dt>验收命令</dt>
              <dd>
                {(mission.spec.acceptance ?? []).map((command, i) => (
                  <code className="block-code" key={i}>
                    {command}
                  </code>
                ))}
              </dd>
              <dt>调用预算</dt>
              <dd>
                {mission.budget.used_calls} / {mission.budget.max_calls} 已使用
              </dd>
              <dt>基线</dt>
              <dd>
                <code>{mission.base_commit}</code>
              </dd>
            </dl>
          )}
          {planning && (
            <>
              <label className="check-label">
                <input
                  type="checkbox"
                  checked={consent}
                  onChange={(e) => setConsent(e.target.checked)}
                />
                {mcpControlled
                  ? "我已检查目标、命令、文件清单及接收连接标识；安全任务视图不显示原始端点，并授权共享"
                  : "我已检查目标、命令、文件清单及发送端点，并授权共享"}
              </label>
              {controlled && (
                <label className="check-label">
                  <input
                    type="checkbox"
                    checked={controlledConsent}
                    onChange={(e) => setControlledConsent(e.target.checked)}
                  />
                  {mcpControlled
                    ? "我授权 Researcher 按固定预算读取冻结仓库；每次本地测试工具调用仍需单独审批，观察不构成验证或审批"
                    : "我授权 Researcher 在本次规划中按固定预算读取冻结清单；观察不构成验证或审批"}
                </label>
              )}
              <button
                className="primary full"
                disabled={
                  busy ||
                  !consent ||
                  (controlled &&
                    (!controlledConsent || !mission.task_bindings)) ||
                  unsupportedWorkspaceMode ||
                  planAccountBlocked ||
                  c4Blocked
                }
                onClick={() =>
                  onAction("plan", controlled || modelBacked ? true : undefined)
                }
              >
                {mcpControlled
                  ? "授权上下文并开始受控调研"
                  : controlled
                    ? "授权上下文与本地读取并生成计划"
                    : replan
                      ? "基于失败重新规划"
                      : "授权上下文并生成计划"}
              </button>
            </>
          )}
          {approval && (
            <>
              <div className="note">
                {modelBacked
                  ? `批准后，Worker 按当前计划在独立候选副本工作；Worker 模型输出以调用回执为准，测试通过只以 C4 ${mission.spec.c4?.mode === "embedded_vm" ? "内置 VM" : "Docker"} 操作记录为准。批准计划不跳过候选验证、QA、Auditor 或交付审批。`
                  : "批准后，Worker 将按当前计划在独立候选副本执行，不会修改原仓库。验证来源以控制器记录为准；模拟检查不执行命令，来源未记录不代表已执行。"}
              </div>
              <button
                className="primary full"
                disabled={
                  busy ||
                  planApprovalBlocked ||
                  unresolved ||
                  unsupportedWorkspaceMode ||
                  executionAccountBlocked ||
                  (hasC4(mission) && !c4ExecutionAvailable) ||
                  c4Blocked
                }
                onClick={() => onAction("approve")}
              >
                批准并执行计划
              </button>
              {mission.spec.c4?.control_version === 1 &&
                !c4ExecutionAvailable && (
                  <p className="warning-copy">
                    新的执行需要未过期的激活与匹配就绪结果；历史证据不会自动授权再次执行。
                  </p>
                )}
              {unresolved && (
                <p className="warning-copy">
                  未解决的失败阻止执行审批；请在计划修订中明确处理。
                </p>
              )}
              {planApprovalBlocked && (
                <p className="warning-copy">
                  本地计划草稿尚未保存、丢弃或已过期。请先在“计划链路”明确处理；系统不会自动保存并批准。
                </p>
              )}
            </>
          )}
          {mission.state === "awaiting_delivery" && (
            <>
              {!!mission.coding_version && (
                <p className="note">
                  清单完整性：QA 逐项验收，Auditor
                  核对范围、保护、审批与完整性。当前计划与引用必须匹配；模型审查不是命令执行。控制器将复核全部摘要。
                </p>
              )}
              <p className="note">
                最新验证：
                {evidence
                  ? `${verificationOutcome(evidence)} · ${verificationSourceLabel(evidence)}`
                  : "未记录 · 来源未记录"}
                。 只导出当前候选的 patch 和报告，不自动 apply、push 或
                deploy。控制器会再次核对摘要。
              </p>
              <button
                className="primary full"
                disabled={busy || !deliverable || unsupportedWorkspaceMode}
                onClick={() => onAction("deliver")}
              >
                批准并导出交付
              </button>
              {!deliverable && (
                <p className="warning-copy">
                  缺少当前候选匹配的通过证据或审查，请刷新检查。
                </p>
              )}
            </>
          )}
          {activeStates.includes(mission.state) && (
            <>
              <p className="note">
                暂停会取消当前请求并阻止后续调用。只有后端确认停止后才显示“已暂停”。
              </p>
              <button
                className="full"
                disabled={
                  busy ||
                  mission.state === "pausing" ||
                  unsupportedWorkspaceMode
                }
                onClick={() => onAction("pause")}
              >
                {mission.state === "pausing" ? "正在等待停止…" : "请求暂停"}
              </button>
            </>
          )}
          {mission.state === "delivered" && (
            <p className="note">交付产物已生成，可在“交付报告”视角下载。</p>
          )}
        </>
      )}
    </section>
  );
}
