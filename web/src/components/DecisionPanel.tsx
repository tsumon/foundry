import { useEffect, useState } from "react";
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
import { ControlledTask } from "./ControlledTask";
import { ModelSnapshot } from "./ModelSnapshot";
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
}: {
  mission: Mission;
  busy: boolean;
  demo?: boolean;
  onAction: (action: MissionAction, controlledConsent?: true) => void;
  onOpenMCPApproval?: () => void;
  planApprovalBlocked?: boolean;
  unsupportedWorkspaceMode?: boolean;
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
  const evidence = mission.evidence?.at(-1);
  const deliverable =
    mission.state === "awaiting_delivery" &&
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
            <ModelSnapshot mission={mission} compact />
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
                已批准的源码、目标与命令可能包含嵌入的秘密。文件名过滤不等于秘密扫描。授权后，这些上下文会发送至下方冻结的模型
                {mcpControlled ? "接收连接" : "端点"}。
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
                  planAccountBlocked
                }
                onClick={() => onAction("plan", controlled ? true : undefined)}
              >
                {mcpControlled
                  ? "授权上下文并开始受控调研"
                  : controlled
                    ? "授权上下文与本地读取并生成计划"
                    : "授权上下文并生成计划"}
              </button>
            </>
          )}
          {approval && (
            <>
              <div className="note">
                批准后，Worker 将按当前计划在独立候选副本执行。验证仅在受限
                Docker 容器中进行；不会修改原仓库。
              </div>
              <button
                className="primary full"
                disabled={
                  busy ||
                  planApprovalBlocked ||
                  unsupportedWorkspaceMode ||
                  executionAccountBlocked
                }
                onClick={() => onAction("approve")}
              >
                批准并执行计划
              </button>
              {planApprovalBlocked && (
                <p className="warning-copy">
                  本地计划草稿尚未保存、丢弃或已过期。请先在“计划链路”明确处理；系统不会自动保存并批准。
                </p>
              )}
            </>
          )}
          {mission.state === "awaiting_delivery" && (
            <>
              <p className="note">
                只导出当前候选的 patch 和报告，不自动 apply、push 或
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
