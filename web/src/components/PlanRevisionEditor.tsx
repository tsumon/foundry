import { useState } from "react";
import { invalidModelSnapshot } from "../modelSnapshot";
import {
  baseWorkspaceMode,
  isControlledReadMission,
  isSupportedMissionMode,
  type Mission,
  type Plan,
  type PlanAdjustment,
  type PlanRevision,
  type Step,
} from "../types";

type EditableStep = Omit<Step, "status">;
type EditablePlan = Omit<Plan, "revision" | "steps" | "adjustments"> & {
  steps: EditableStep[];
  adjustments?: (Omit<PlanAdjustment, "step_ids"> & { step_ids: string[] })[];
};

export interface PlanDraft {
  key: string;
  missionID: string;
  expectedMissionRevision: number;
  expectedPlanRevision: number;
  originalPlan: EditablePlan;
  plan: EditablePlan;
  reason: string;
  dirty: boolean;
  stale: boolean;
}

const editablePlan = (plan: Plan): EditablePlan => ({
  ...(plan.adjustments
    ? {
        adjustments: plan.adjustments.map((a) => ({
          ...a,
          step_ids: [...(a.step_ids ?? [])],
        })),
      }
    : {}),
  summary: plan.summary,
  steps: plan.steps.map(({ status: _status, ...step }) => ({
    ...step,
    depends_on: [...(step.depends_on ?? [])],
    files: [...(step.files ?? [])],
  })),
});

export function createPlanDraft(mission: Mission): PlanDraft {
  const plan = mission.plan
    ? editablePlan(mission.plan)
    : { summary: "", steps: [] };
  const expectedPlanRevision = mission.plan?.revision ?? 0;
  return {
    key: `${mission.id}:${mission.revision}:${expectedPlanRevision}`,
    missionID: mission.id,
    expectedMissionRevision: mission.revision,
    expectedPlanRevision,
    originalPlan: structuredClone(plan),
    plan: structuredClone(plan),
    reason: "",
    dirty: false,
    stale: false,
  };
}

export const planListLines = (value: string) =>
  value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);

const rawLines = (value: string) => value.replace(/\r\n/g, "\n").split("\n");
const utf8Bytes = (value: string) => new TextEncoder().encode(value).byteLength;

const samePlan = (left: EditablePlan, right: EditablePlan) =>
  JSON.stringify(left) === JSON.stringify(right);

function describeDiff(draft: PlanDraft): string[] {
  const result: string[] = [];
  const before = draft.originalPlan;
  const after = draft.plan;
  if (JSON.stringify(before.adjustments) !== JSON.stringify(after.adjustments))
    result.push("失败映射、调整原因或未解决状态已变更。");
  if (before.summary !== after.summary)
    result.push(
      `摘要：${before.summary || "（空）"} → ${after.summary || "（空）"}`,
    );
  const beforeByID = new Map(before.steps.map((step) => [step.id, step]));
  const afterByID = new Map(after.steps.map((step) => [step.id, step]));
  const describeList = (
    prefix: string,
    label: string,
    previous: string[],
    current: string[],
  ) => {
    const oldItems = planListLines(previous.join("\n"));
    const newItems = planListLines(current.join("\n"));
    const added = newItems.filter((item) => !oldItems.includes(item));
    const removed = oldItems.filter((item) => !newItems.includes(item));
    if (added.length) result.push(`${prefix}${label}新增：${added.join("、")}`);
    if (removed.length)
      result.push(`${prefix}${label}删除：${removed.join("、")}`);
    if (
      !added.length &&
      !removed.length &&
      oldItems.join("\n") !== newItems.join("\n")
    )
      result.push(
        `${prefix}${label}顺序：${oldItems.join("、")} → ${newItems.join("、")}`,
      );
  };
  for (const step of after.steps) {
    if (!beforeByID.has(step.id)) {
      result.push(
        `新增步骤 ${step.id || "（未填写 ID）"}：${step.title || "（未填写标题）"}`,
      );
      const dependencies = planListLines(step.depends_on.join("\n"));
      const files = planListLines(step.files.join("\n"));
      if (dependencies.length)
        result.push(
          `新增步骤 ${step.id || "（未填写 ID）"}依赖：${dependencies.join("、")}`,
        );
      if (files.length)
        result.push(
          `新增步骤 ${step.id || "（未填写 ID）"}文件：${files.join("、")}`,
        );
      continue;
    }
    const old = beforeByID.get(step.id)!;
    if (old.title !== step.title)
      result.push(`步骤 ${step.id}标题：${old.title} → ${step.title}`);
    if (old.description !== step.description)
      result.push(
        `步骤 ${step.id}说明：${old.description || "（空）"} → ${step.description || "（空）"}`,
      );
    describeList(`步骤 ${step.id}`, "依赖", old.depends_on, step.depends_on);
    describeList(`步骤 ${step.id}`, "文件", old.files, step.files);
  }
  for (const step of before.steps)
    if (!afterByID.has(step.id))
      result.push(`删除步骤 ${step.id}：${step.title}`);
  const oldOrder = before.steps.map((step) => step.id).join(" → ");
  const newOrder = after.steps.map((step) => step.id).join(" → ");
  if (oldOrder !== newOrder && before.steps.length && after.steps.length)
    result.push(`步骤顺序：${oldOrder} → ${newOrder}`);
  return result;
}

function validateDraft(draft: PlanDraft): string {
  if (!draft.plan.summary.trim()) return "计划摘要不能为空。";
  if (utf8Bytes(draft.plan.summary) > 4000)
    return "计划摘要不能超过 4000 UTF-8 字节。";
  if (!draft.reason.trim()) return "修订原因不能为空。";
  if (utf8Bytes(draft.reason) > 1000)
    return "修订原因不能超过 1000 UTF-8 字节。";
  if (!draft.plan.steps.length || draft.plan.steps.length > 6)
    return "计划必须包含 1 至 6 个步骤。";
  const seen = new Set<string>();
  for (const [index, step] of draft.plan.steps.entries()) {
    const label = `步骤 ${index + 1}`;
    const id = step.id.trim();
    if (!id || utf8Bytes(step.id) > 64)
      return `${label} ID 必填且不能超过 64 UTF-8 字节。`;
    if (seen.has(id)) return `步骤 ID ${id} 重复。`;
    seen.add(id);
    if (!step.title.trim() || utf8Bytes(step.title) > 200)
      return `${label}标题必填且不能超过 200 UTF-8 字节。`;
    if (!step.description.trim() || utf8Bytes(step.description) > 8000)
      return `${label}说明必填且不能超过 8000 UTF-8 字节。`;
    const files = planListLines(step.files.join("\n"));
    if (!files.length) return `${label}至少需要一个文件。`;
    if (files.length > 30) return "每个步骤最多 30 个文件。";
  }
  for (const a of draft.plan.adjustments ?? []) {
    if (!a.reason.trim()) return "每项失败必须填写调整原因。";
    if (utf8Bytes(a.reason) > 4000) return "调整原因不能超过 4000 UTF-8 字节。";
    if (!a.unresolved && !a.step_ids.length)
      return "已解决失败必须映射当前步骤。";
    if (
      new Set(a.step_ids).size !== a.step_ids.length ||
      a.step_ids.some((id) => !seen.has(id))
    )
      return "失败映射包含未知或重复步骤；请更新映射或标记未解决。";
  }
  return "";
}

const sourceName = (source: string) =>
  source === "user"
    ? "用户修订"
    : source === "planner"
      ? "Planner 原始计划"
      : source;

export function PlanRevisionEditor({
  mission,
  draft,
  history,
  historyLoading,
  historyError,
  busy,
  saveError,
  onDraftChange,
  onSave,
  onDiscard,
  onRebase,
}: {
  mission: Mission;
  draft: PlanDraft;
  history: PlanRevision[];
  historyLoading: boolean;
  historyError: string;
  busy: boolean;
  saveError: string;
  onDraftChange: (draft: PlanDraft) => void;
  onSave: () => void;
  onDiscard: () => void;
  onRebase: () => void;
}) {
  const [editorOpen, setEditorOpen] = useState(draft.dirty || draft.stale);
  const updatePlan = (plan: EditablePlan) =>
    onDraftChange({ ...draft, plan, dirty: true });
  const updateStep = (index: number, patch: Partial<EditableStep>) =>
    updatePlan({
      ...draft.plan,
      steps: draft.plan.steps.map((step, stepIndex) =>
        stepIndex === index ? { ...step, ...patch } : step,
      ),
    });
  const moveStep = (index: number, offset: number) => {
    const next = index + offset;
    if (next < 0 || next >= draft.plan.steps.length) return;
    const steps = [...draft.plan.steps];
    [steps[index], steps[next]] = [steps[next], steps[index]];
    updatePlan({ ...draft.plan, steps });
  };
  const diff = describeDiff(draft);
  const controlled = isControlledReadMission(mission);
  const modeSupported =
    isSupportedMissionMode(mission) && !invalidModelSnapshot(mission);
  const canEdit =
    modeSupported && mission.state === "awaiting_approval" && !busy;
  const validationError = draft.dirty ? validateDraft(draft) : "";
  const canSave =
    canEdit &&
    !busy &&
    !draft.stale &&
    draft.reason.trim().length > 0 &&
    !validationError &&
    !samePlan(draft.originalPlan, draft.plan);
  return (
    <section className="plan-editor" aria-label="计划修订编辑器">
      <button
        type="button"
        className="plan-editor-toggle"
        aria-expanded={editorOpen}
        aria-controls="plan-revision-editor-fields"
        onClick={() => setEditorOpen((open) => !open)}
      >
        修订计划（保存不审批）
      </button>
      <div
        id="plan-revision-editor-fields"
        className="plan-editor-disclosure"
        hidden={!editorOpen}
      >
        <div className="section-heading plan-heading">
          <div>
            <h2>编辑待审批计划</h2>
            <p className="muted small">
              基于任务 r{draft.expectedMissionRevision} · 计划 v
              {draft.expectedPlanRevision}
            </p>
          </div>
          <span className="tag">
            {controlled
              ? "受控只读 · 可恢复候选模式"
              : baseWorkspaceMode(mission.workspace_mode) ===
                    "generations_v1" && !mission.tool_mode
                ? "可恢复候选模式"
                : mission.tool_mode
                  ? `不支持的模式组合：${mission.workspace_mode || "legacy"} + ${mission.tool_mode}`
                  : baseWorkspaceMode(mission.workspace_mode)
                    ? `不支持的只读模式：${mission.workspace_mode}`
                    : "旧任务兼容模式"}
          </span>
        </div>
        <p className="note">
          保存修订只更新待审批计划，不会批准、执行或恢复任何本地操作。
        </p>
        {draft.stale && (
          <div className="note warning-note" role="alert">
            服务端任务或计划版本已变化。草稿仍保留，且不会自动合并或重送。
            <div className="actions compact-actions">
              <button
                type="button"
                onClick={onRebase}
                disabled={busy || !modeSupported}
              >
                基于当前版本重新编辑
              </button>
              <button
                type="button"
                onClick={onDiscard}
                disabled={busy || !modeSupported}
              >
                丢弃草稿
              </button>
            </div>
          </div>
        )}
        <label htmlFor="plan-summary">计划摘要</label>
        <textarea
          id="plan-summary"
          rows={3}
          maxLength={4000}
          readOnly={!canEdit}
          value={draft.plan.summary}
          onChange={(event) =>
            updatePlan({ ...draft.plan, summary: event.target.value })
          }
        />
        <div className="plan-step-editors">
          {draft.plan.adjustments?.map((adjustment, index) => (
            <fieldset key={adjustment.failure_id}>
              <legend>失败调整 {adjustment.failure_id}</legend>
              <label>
                调整原因 {adjustment.failure_id}
                <textarea
                  value={adjustment.reason}
                  readOnly={!canEdit}
                  onChange={(event) =>
                    updatePlan({
                      ...draft.plan,
                      adjustments: draft.plan.adjustments!.map((a, i) =>
                        i === index ? { ...a, reason: event.target.value } : a,
                      ),
                    })
                  }
                />
              </label>
              <label>
                映射步骤 {adjustment.failure_id}（每行一项）
                <textarea
                  value={adjustment.step_ids.join("\n")}
                  readOnly={!canEdit}
                  onChange={(event) =>
                    updatePlan({
                      ...draft.plan,
                      adjustments: draft.plan.adjustments!.map((a, i) =>
                        i === index
                          ? {
                              ...a,
                              step_ids: planListLines(event.target.value),
                            }
                          : a,
                      ),
                    })
                  }
                />
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={adjustment.unresolved}
                  disabled={!canEdit}
                  onChange={(event) =>
                    updatePlan({
                      ...draft.plan,
                      adjustments: draft.plan.adjustments!.map((a, i) =>
                        i === index
                          ? { ...a, unresolved: event.target.checked }
                          : a,
                      ),
                    })
                  }
                />
                未解决 {adjustment.failure_id}
              </label>
            </fieldset>
          ))}
          {draft.plan.steps.map((step, index) => (
            <fieldset
              key={index}
              className="plan-step-editor"
              aria-label={`计划步骤 ${index + 1}`}
            >
              <legend>步骤 {index + 1}</legend>
              <div className="plan-step-actions">
                <button
                  type="button"
                  aria-label="上移"
                  disabled={!canEdit || index === 0}
                  onClick={() => moveStep(index, -1)}
                >
                  上移
                </button>
                <button
                  type="button"
                  aria-label="下移"
                  disabled={!canEdit || index === draft.plan.steps.length - 1}
                  onClick={() => moveStep(index, 1)}
                >
                  下移
                </button>
                <button
                  type="button"
                  aria-label="删除步骤"
                  disabled={!canEdit}
                  onClick={() =>
                    updatePlan({
                      ...draft.plan,
                      steps: draft.plan.steps.filter((_, i) => i !== index),
                    })
                  }
                >
                  删除步骤
                </button>
              </div>
              <div className="form-columns">
                <div>
                  <label htmlFor={`plan-step-${index}-id`}>步骤 ID</label>
                  <input
                    id={`plan-step-${index}-id`}
                    maxLength={64}
                    readOnly={!canEdit}
                    value={step.id}
                    onChange={(event) =>
                      updateStep(index, { id: event.target.value })
                    }
                  />
                </div>
                <div>
                  <label htmlFor={`plan-step-${index}-title`}>步骤标题</label>
                  <input
                    id={`plan-step-${index}-title`}
                    maxLength={200}
                    readOnly={!canEdit}
                    value={step.title}
                    onChange={(event) =>
                      updateStep(index, { title: event.target.value })
                    }
                  />
                </div>
              </div>
              <label htmlFor={`plan-step-${index}-description`}>步骤说明</label>
              <textarea
                id={`plan-step-${index}-description`}
                rows={3}
                maxLength={8000}
                readOnly={!canEdit}
                value={step.description}
                onChange={(event) =>
                  updateStep(index, { description: event.target.value })
                }
              />
              <div className="form-columns">
                <div>
                  <label htmlFor={`plan-step-${index}-dependencies`}>
                    依赖（每行一项）
                  </label>
                  <textarea
                    id={`plan-step-${index}-dependencies`}
                    rows={3}
                    maxLength={6000}
                    readOnly={!canEdit}
                    value={step.depends_on.join("\n")}
                    onChange={(event) =>
                      updateStep(index, {
                        depends_on: rawLines(event.target.value),
                      })
                    }
                  />
                </div>
                <div>
                  <label htmlFor={`plan-step-${index}-files`}>
                    文件（每行一项）
                  </label>
                  <textarea
                    id={`plan-step-${index}-files`}
                    rows={3}
                    maxLength={12000}
                    readOnly={!canEdit}
                    value={step.files.join("\n")}
                    onChange={(event) =>
                      updateStep(index, { files: rawLines(event.target.value) })
                    }
                  />
                </div>
              </div>
            </fieldset>
          ))}
        </div>
        <button
          type="button"
          onClick={() =>
            updatePlan({
              ...draft.plan,
              steps: [
                ...draft.plan.steps,
                {
                  id: "",
                  title: "",
                  description: "",
                  depends_on: [],
                  files: [],
                },
              ],
            })
          }
          disabled={!canEdit || busy || draft.plan.steps.length >= 6}
        >
          添加步骤
        </button>
        <section className="plan-diff" role="region" aria-label="修订差异">
          <h3>修订差异</h3>
          {diff.length ? (
            <ul>
              {diff.map((item, index) => (
                <li key={index}>{item}</li>
              ))}
            </ul>
          ) : (
            <p className="muted small">计划正文尚无变化。</p>
          )}
        </section>
        <label htmlFor="plan-reason">修订原因</label>
        <textarea
          id="plan-reason"
          rows={2}
          maxLength={1000}
          required
          readOnly={!canEdit}
          value={draft.reason}
          onChange={(event) =>
            onDraftChange({ ...draft, reason: event.target.value, dirty: true })
          }
        />
        {validationError && (
          <p className="form-error" role="alert">
            {validationError}
          </p>
        )}
        {saveError && (
          <p className="form-error" role="alert">
            {saveError}
          </p>
        )}
        <div className="actions">
          <button
            className="primary"
            type="button"
            disabled={!canSave}
            onClick={onSave}
          >
            保存修订（不审批）
          </button>
          {draft.dirty && !draft.stale && (
            <button
              type="button"
              onClick={onDiscard}
              disabled={busy || !modeSupported}
            >
              丢弃草稿
            </button>
          )}
        </div>
      </div>
      <section className="plan-history" aria-label="计划修订历史">
        <div className="section-heading">
          <h2>修订历史</h2>
          <span className="muted small">仅供核对，不作为执行授权</span>
        </div>
        {historyLoading && (
          <p className="muted small" role="status">
            正在读取修订历史…
          </p>
        )}
        {historyError && (
          <p className="warning-copy" role="status">
            {historyError}
          </p>
        )}
        {!historyLoading && !historyError && !history.length && (
          <p className="quiet-state">尚无已保存的修订历史</p>
        )}
        {history.map((record) => (
          <article
            className="plan-history-row"
            key={`${record.epoch}:${record.plan_revision}`}
          >
            <div className="section-heading">
              <h3>
                计划 v{record.plan_revision} · {sourceName(record.source)}
              </h3>
              <time>{record.created_at}</time>
            </div>
            <p className="muted small">
              运行 {record.epoch} · 任务 r{record.mission_revision}
              {record.parent_plan_revision
                ? ` · 父计划 v${record.parent_plan_revision}`
                : " · 无父计划记录"}
            </p>
            <p>{record.reason || "原始 Planner 计划（无用户修订原因）"}</p>
            <details>
              <summary>查看历史计划正文</summary>
              <p>
                <strong>摘要：</strong>
                {record.plan.summary}
              </p>
              <ol className="plan-history-steps">
                {record.plan.steps.map((step, index) => (
                  <li key={`${index}:${step.id}`}>
                    <strong>
                      {step.id} · {step.title}
                    </strong>
                    <p>{step.description}</p>
                    <p className="muted small">
                      依赖：{(step.depends_on ?? []).join("、") || "无"}
                    </p>
                    <p className="muted small">
                      文件：{(step.files ?? []).join("、") || "无"}
                    </p>
                  </li>
                ))}
              </ol>
              <p className="muted small">
                正文 SHA-256：<code>{record.plan_digest}</code>
              </p>
            </details>
          </article>
        ))}
      </section>
    </section>
  );
}
