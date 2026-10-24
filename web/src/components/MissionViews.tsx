import type { ReactNode } from "react";
import {
  isControlledReadMission,
  isControlledMCPReadMission,
  usesGenerationWorkspace,
  baseWorkspaceMode,
  type LocalOperation,
  type Mission,
} from "../types";
import { ResearchBrief } from "./ResearchBrief";
import {
  VerificationView,
  CodingAttempts,
  codingAttemptLabel,
} from "./VerificationView";
import { ModelSnapshot } from "./ModelSnapshot";
import { ModelContinuation } from "./ModelContinuation";
import { RecoveryDiagnostic, RepositorySource } from "./RecoveryDiagnostic";
export const views = [
  "总览",
  "会话",
  "计划链路",
  "代码变更",
  "验证",
  "审批",
  "交付报告",
] as const;
export type MissionView = (typeof views)[number];
export function MissionViews({
  mission,
  view,
  demo = false,
  operations = [],
  operationsLoading = false,
  operationsError = "",
  planEditor,
  mcpDecision,
  onOpenDecision,
  onChanged,
  busy = false,
}: {
  mission: Mission;
  view: MissionView;
  demo?: boolean;
  operations?: LocalOperation[];
  operationsLoading?: boolean;
  operationsError?: string;
  planEditor?: ReactNode;
  mcpDecision?: ReactNode;
  onOpenDecision?: () => void;
  onChanged?: (mission: Mission) => void;
  busy?: boolean;
}) {
  if (view === "总览")
    return (
      <>
        <div className="section-heading">
          <h2>任务简报</h2>
          <span className="muted small">任务 r{mission.revision}</span>
        </div>
        <p className="goal">{mission.goal}</p>
        <RepositorySource mission={mission} />
        {mcpDecision}
        <dl className="metadata">
          <dt>仓库</dt>
          <dd>
            <code>{mission.repo_path}</code>
          </dd>
          <dt>基线 commit</dt>
          <dd>
            <code>{mission.base_commit}</code>
          </dd>
          <dt>允许修改</dt>
          <dd>{(mission.spec.scope ?? []).join(" · ")}</dd>
          <dt>受保护文件</dt>
          <dd>
            {(mission.spec.protected ?? []).join(" · ") ||
              "由控制器应用默认保护"}
          </dd>
          <dt>验收命令</dt>
          <dd>
            {(mission.spec.acceptance ?? []).map((command, i) => (
              <code className="block-code" key={i}>
                {command}
              </code>
            ))}
          </dd>
          <dt>验证镜像</dt>
          <dd>
            <code>{mission.spec.image}</code>
            <br />
            <code>{mission.spec.image_id || "待解析本地镜像 ID"}</code>
          </dd>
          <dt>模型调用</dt>
          <dd>
            {mission.budget.used_calls} / {mission.budget.max_calls}
          </dd>
          <dt>Token 使用量</dt>
          <dd>
            {mission.budget.usage_unknown
              ? "未知（提供方未完整返回）"
              : `输入 ${mission.budget.input_tokens} · 输出 ${mission.budget.output_tokens}`}
          </dd>
        </dl>
        <ModelSnapshot
          mission={mission}
          compact={mission.coding_version === 1}
        />
        {!demo &&
          onChanged &&
          mission.coding_version === 1 &&
          mission.model_snapshot && (
            <ModelContinuation
              key={`${mission.id}:${mission.revision}`}
              mission={mission}
              onChanged={onChanged}
              disabled={busy}
            />
          )}
        {mission.coding_version !== undefined && (
          <CodingAttempts mission={mission} />
        )}
        <RecoveryStatus
          mission={mission}
          onOpenDecision={onOpenDecision}
          operations={operations}
          loading={operationsLoading}
          error={operationsError}
        />
        <ResearchBrief mission={mission} />
      </>
    );
  if (view === "会话")
    return (
      <>
        <div className="section-heading">
          <h2>任务会话</h2>
          <span className="muted small">Main · 只读问答</span>
        </div>
        <p className="muted">
          在右侧 Main Agent 中提问。聊天内容不能代替审批，也不能修改批准的范围。
        </p>
        {!mission.messages?.length && (
          <p className="quiet-state">尚无会话消息</p>
        )}
        {(mission.messages ?? []).map((message, i) => (
          <article key={i} className="conversation-message">
            <div className="section-heading">
              <strong>{message.role === "user" ? "你" : message.role}</strong>
              <time>{message.created_at}</time>
            </div>
            <p>{message.content}</p>
          </article>
        ))}
      </>
    );
  if (view === "计划链路")
    return (
      <>
        <ResearchBrief mission={mission} />
        <div className="section-heading plan-heading">
          <h2>执行计划</h2>
          <span className="muted small">
            {mission.plan ? `计划 v${mission.plan.revision}` : "尚未生成"}
          </span>
        </div>
        <p className="muted">
          {mission.plan?.summary ?? "检查上下文并授权后，由 Planner 生成计划。"}
        </p>
        <ol className="plan-list">
          {(mission.plan?.steps ?? []).map((step, i) => (
            <li key={step.id}>
              <span className="step-index">{i + 1}</span>
              <details open={i === 0}>
                <summary>
                  <span>
                    <strong>{step.title}</strong>
                    <small>{step.description}</small>
                  </span>
                  <span className="step-status">
                    Worker ·{" "}
                    {mission.coding_version === 1
                      ? codingAttemptLabel(
                          [...(mission.coding_attempts ?? [])]
                            .reverse()
                            .find(
                              (a) =>
                                a.phase === "worker" &&
                                a.step_id === step.id &&
                                a.plan_revision === mission.plan?.revision,
                            ),
                        )
                      : ((
                          {
                            pending: "待执行",
                            running: "执行中",
                            done: "已完成",
                            completed: "已完成",
                            failed: "失败",
                          } as Record<string, string>
                        )[step.status] ?? step.status)}
                  </span>
                </summary>
                <div className="step-details">
                  <p>
                    范围 <code>{(step.files ?? []).join(" · ")}</code>
                  </p>
                  <p>
                    依赖 {(step.depends_on ?? []).join(" · ") || "无前置步骤"}
                  </p>
                  <p className="muted small">
                    完成情况以控制器记录和后续验证证据为准。
                  </p>
                  {mission.coding_version === 1 &&
                    (mission.coding_attempts ?? [])
                      .filter(
                        (a) =>
                          a.phase === "worker" &&
                          a.step_id === step.id &&
                          a.plan_revision === mission.plan?.revision,
                      )
                      .map((a) => (
                        <p className="muted small" key={a.id}>
                          本地尝试 <code>{a.id}</code>
                          {a.failure_code ? ` · ${a.failure_code}` : ""}
                          {a.after
                            ? ` · 候选 ${a.after.id} · ${a.after.digest}`
                            : ""}
                        </p>
                      ))}
                </div>
              </details>
            </li>
          ))}
        </ol>
        {planEditor}
      </>
    );
  if (view === "代码变更")
    return (
      <>
        <div className="section-heading">
          <h2>代码变更记录</h2>
          <span className="muted small">按产生顺序，保留修复历史</span>
        </div>
        <p className="muted small">
          候选摘要 <code>{mission.candidate_digest || "尚未生成"}</code>
        </p>
        {!mission.changes?.length && (
          <p className="quiet-state">尚无代码变更</p>
        )}
        {(mission.changes ?? []).map((change, i) => (
          <details className="change-file" key={`${i}:${change.path}`} open>
            <summary>
              <code>{change.path}</code>
              <span className="muted small">变更 {i + 1}</span>
            </summary>
            <div className="code-pair">
              <section>
                <h3>变更前</h3>
                <pre>{change.before || "（空文件 / 新文件）"}</pre>
              </section>
              <section>
                <h3>变更后</h3>
                <pre>{change.after || "（空内容）"}</pre>
              </section>
            </div>
          </details>
        ))}
      </>
    );
  if (view === "验证") return <VerificationView mission={mission} />;
  if (view === "审批")
    return (
      <>
        <h2>授权与审批记录</h2>
        <p className="muted">
          用户授权与控制器内部封存分开标注。每条记录绑定具体版本与摘要。
        </p>
        {mcpDecision}
        {!mission.approvals?.length && (
          <p className="quiet-state">尚无审批记录</p>
        )}
        {(mission.approvals ?? []).map((approval, i) => (
          <article className="approval-row" key={i}>
            <h3>
              {(
                {
                  context: "用户授权 · 共享上下文",
                  execute: "用户审批 · 执行计划",
                  deliver: "用户审批 · 导出交付",
                  snapshot: "内部封存 · 仓库快照",
                  plan_proposal: "内部封存 · 计划提案",
                } as Record<string, string>
              )[approval.kind] ?? `记录 · ${approval.kind}`}
            </h3>
            <p className="muted small">
              {approval.kind === "plan_proposal"
                ? `计划 v${approval.plan_revision ?? approval.revision} · 任务版本未记录`
                : `任务 r${approval.revision} · ${approval.plan_revision ? `计划 v${approval.plan_revision}` : "计划版本未记录（旧记录）"}`}{" "}
              · {approval.created_at}
            </p>
            <code>{approval.digest}</code>
          </article>
        ))}
      </>
    );
  return (
    <>
      <h2>交付报告</h2>
      {mission.coding_version === 1 && (
        <p className="muted">
          C2
          报告包含执行轨迹、失败归档、计划调整与角色审查清单；下载时控制器复核绑定。内容检查模拟不等于命令执行。
        </p>
      )}
      {mission.delivery && mission.state === "delivered" ? (
        <>
          <p>已生成本地 patch 与报告；未修改原仓库，也未推送或部署。</p>
          <dl className="metadata">
            <dt>基线 commit</dt>
            <dd>
              <code>{mission.base_commit}</code>
            </dd>
            <dt>交付摘要</dt>
            <dd>
              <code>{mission.delivery.candidate_digest}</code>
            </dd>
            <dt>交付时间</dt>
            <dd>{mission.delivery.created_at}</dd>
          </dl>
          {!demo && (
            <div className="actions">
              <a
                className="button primary"
                href={`/api/missions/${encodeURIComponent(mission.id)}/patch`}
                download
              >
                下载 patch
              </a>
              <a
                className="button"
                href={`/api/missions/${encodeURIComponent(mission.id)}/report`}
                download
              >
                下载 Markdown 报告
              </a>
            </div>
          )}
        </>
      ) : (
        <div className="quiet-state">
          <h3>尚未生成交付报告</h3>
          <p>
            当前候选通过验证与审查，并由你批准交付后，才会生成可下载的产物。
          </p>
        </div>
      )}
    </>
  );
}

function operationNextAction(operation: LocalOperation) {
  if (operation.status === "committed")
    return "操作已提交；以当前任务与候选记录为准。";
  if (operation.status === "interrupted")
    return "本次操作未提交；请核对错误与后续操作记录。需要继续时，通过原决策面板重新规划或重新审批。";
  if (operation.status === "prepared")
    return "准备结果已登记但未显示已提交；刷新任务并核对权威记录，不会自动继续。";
  if (operation.status === "failed")
    return "本次操作失败且未提交；请核对错误与后续操作记录。解决问题后，通过原决策面板发起新的明确操作。";
  return "操作尚未提交；不会自动继续、恢复或回滚。";
}

function operationGenerationLabel(mission: Mission, operation: LocalOperation) {
  const result = operation.result?.generation;
  const current = mission.candidate_generation;
  if (
    result &&
    current &&
    result.id === current.id &&
    result.digest === current.digest
  )
    return "操作结果 generation（当前记录的选择）：";
  if (operation.status === "committed")
    return "操作结果 generation（已提交记录）：";
  return "操作结果 generation（本次操作未提交）：";
}

function RecoveryStatus({
  mission,
  onOpenDecision,
  operations,
  loading,
  error,
}: {
  mission: Mission;
  onOpenDecision?: () => void;
  operations: LocalOperation[];
  loading: boolean;
  error: string;
}) {
  const mode = baseWorkspaceMode(mission.workspace_mode);
  const controlled = isControlledReadMission(mission);
  const mcpControlled = isControlledMCPReadMission(mission);
  const generations = usesGenerationWorkspace(mission);
  return (
    <section className="recovery-status" aria-label="候选与本地操作">
      <RecoveryDiagnostic mission={mission} onOpenDecision={onOpenDecision} />
      <div className="section-heading">
        <h2>候选与本地操作</h2>
        <span className="tag">
          {mcpControlled
            ? "任务内 MCP · 受控只读 · 不可变候选模式"
            : controlled
              ? "受控只读 · 不可变候选模式"
              : mode === "generations_v1" && !mission.tool_mode
                ? "不可变候选模式"
                : mission.tool_mode
                  ? `未知模式组合：${mode || "legacy"} + ${mission.tool_mode}`
                  : mode
                    ? `未知模式：${mode}`
                    : "旧任务兼容模式"}
        </span>
      </div>
      {generations ? (
        <dl className="metadata compact-metadata">
          <dt>记录的当前 generation</dt>
          <dd>
            {mission.candidate_generation ? (
              <>
                <code>{mission.candidate_generation.id}</code>
                <br />
                <code>{mission.candidate_generation.digest}</code>
              </>
            ) : (
              "尚未记录当前 generation"
            )}
          </dd>
          <dt>候选摘要</dt>
          <dd>
            <code>{mission.candidate_digest || "尚未生成"}</code>
          </dd>
        </dl>
      ) : (
        <p className="muted small">
          {mode
            ? "此模式可查看，但当前界面不声称能够执行或恢复它。"
            : "此任务沿用旧候选路径，没有不可变 generation 或自动恢复声明。"}
        </p>
      )}
      {generations && mission.candidate_generation && (
        <p className="muted small">
          此处显示控制器记录的选择；磁盘完整性仍以服务端核对结果为准。
        </p>
      )}
      {loading && (
        <p className="muted small" role="status">
          正在读取本地操作记录…
        </p>
      )}
      {error && (
        <p className="warning-copy" role="status">
          本地操作记录读取失败：{error}
        </p>
      )}
      {!loading && !error && !operations.length && (
        <p className="quiet-state">尚无本地操作记录</p>
      )}
      {operations.map((operation) => (
        <article className="operation-row" key={operation.id}>
          <div className="section-heading">
            <h3>
              {operation.kind} · {operation.status}
            </h3>
            <time>{operation.updated_at}</time>
          </div>
          <p className="muted small">
            操作 <code>{operation.id}</code> · 任务 r
            {operation.expected_revision} · 计划 v{operation.plan_revision}
          </p>
          {operation.error && <p className="warning-copy">{operation.error}</p>}
          {operation.result?.generation && (
            <p className="small">
              {operationGenerationLabel(mission, operation)}
              <code>{operation.result.generation.id}</code> ·{" "}
              <code>{operation.result.generation.digest}</code>
            </p>
          )}
          <p className="small">
            <strong>下一步：</strong>
            {operationNextAction(operation)}
          </p>
        </article>
      ))}
    </section>
  );
}
