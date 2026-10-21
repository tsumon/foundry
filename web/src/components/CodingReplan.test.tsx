import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { mission } from "../test/fixtures";
import type { Mission } from "../types";
import { DecisionPanel } from "./DecisionPanel";
import { createPlanDraft, PlanRevisionEditor } from "./PlanRevisionEditor";

const replanMission = () =>
  Object.assign(structuredClone(mission), {
    coding_version: 1,
    replan_context: {
      schema_version: 1,
      mission_id: mission.id,
      epoch: 1,
      plan_revision: 2,
      base_commit: "base",
      candidate_generation: { id: "old", digest: "digest" },
      archive_digest: "archive",
      trace_digest: "trace",
      digest: "sealed",
      failures: [
        {
          id: "failure-a",
          code: "verification_failed",
          phase: "verification",
          summary: "内容验证失败",
          attempt_id: "a",
          verification_id: "v",
          source_digest: "source",
        },
      ],
    },
    plan: {
      ...mission.plan!,
      adjustments: [
        {
          failure_id: "failure-a",
          step_ids: [mission.plan!.steps[0].id],
          reason: "修正内容",
          unresolved: false,
        },
      ],
    },
  });

it("replans only on explicit consent and never executes automatically", () => {
  const m = replanMission();
  m.state = "blocked";
  const action = vi.fn();
  render(<DecisionPanel mission={m} busy={false} onAction={action} />);
  const button = screen.getByRole("button", { name: "基于失败重新规划" });
  expect(button).toBeDisabled();
  expect(action).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.click(button);
  expect(action).toHaveBeenCalledExactlyOnceWith("plan", undefined);
  expect(
    screen.queryByRole("button", { name: "批准并执行计划" }),
  ).not.toBeInTheDocument();
});

it("blocks approval for unresolved failure mappings", () => {
  const m = replanMission();
  m.plan.adjustments[0].unresolved = true;
  render(<DecisionPanel mission={m} busy={false} onAction={vi.fn()} />);
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
  expect(screen.getByText(/未解决的失败阻止执行审批/)).toBeVisible();
});

it("retains editable adjustment data and rejects removed mapped steps", () => {
  const m = replanMission();
  const initial = createPlanDraft(m);
  expect(initial.plan).toHaveProperty("adjustments", m.plan.adjustments);
  function Editor() {
    const [draft, setDraft] = useState(initial);
    return (
      <PlanRevisionEditor
        mission={m}
        draft={draft}
        history={[]}
        historyLoading={false}
        historyError=""
        busy={false}
        saveError=""
        onDraftChange={setDraft}
        onSave={vi.fn()}
        onDiscard={vi.fn()}
        onRebase={vi.fn()}
      />
    );
  }
  render(<Editor />);
  fireEvent.click(
    screen.getByRole("button", { name: "修订计划（保存不审批）" }),
  );
  fireEvent.change(screen.getByLabelText("修订原因"), {
    target: { value: "调整" },
  });
  fireEvent.change(screen.getByLabelText("调整原因 failure-a"), {
    target: { value: "修复真实内容" },
  });
  fireEvent.change(screen.getByLabelText("步骤 ID"), {
    target: { value: "changed" },
  });
  expect(screen.getByText(/失败映射包含未知或重复步骤/)).toBeVisible();
  expect(
    screen.getByRole("button", { name: "保存修订（不审批）" }),
  ).toBeDisabled();
});

it.each(["null", "omitted"])(
  "edits unresolved proposals with %s step IDs without changing frozen input",
  (representation) => {
    const source = replanMission();
    const { step_ids: _ids, ...adjustment } = source.plan.adjustments[0];
    const m: Mission = JSON.parse(
      JSON.stringify({
        ...source,
        plan: {
          ...source.plan,
          adjustments: [
            {
              ...adjustment,
              unresolved: true,
              ...(representation === "null" ? { step_ids: null } : {}),
            },
          ],
        },
      }),
    );
    const frozenInput = JSON.stringify(m);
    const initial = createPlanDraft(m);
    expect(initial.plan.adjustments?.[0].step_ids).toEqual([]);
    expect(initial.dirty).toBe(false);
    const save = vi.fn();
    function Editor() {
      const [draft, setDraft] = useState(initial);
      return (
        <PlanRevisionEditor
          mission={m}
          draft={draft}
          history={[]}
          historyLoading={false}
          historyError=""
          busy={false}
          saveError=""
          onDraftChange={setDraft}
          onSave={save}
          onDiscard={vi.fn()}
          onRebase={vi.fn()}
        />
      );
    }
    render(<Editor />);
    fireEvent.click(
      screen.getByRole("button", { name: "修订计划（保存不审批）" }),
    );
    const mapping = screen.getByLabelText("映射步骤 failure-a（每行一项）");
    const saveButton = screen.getByRole("button", {
      name: "保存修订（不审批）",
    });
    expect(mapping).toHaveValue("");
    expect(saveButton).toBeDisabled();
    fireEvent.change(screen.getByLabelText("修订原因"), {
      target: { value: "处理失败" },
    });
    fireEvent.click(screen.getByLabelText("未解决 failure-a"));
    expect(screen.getByText("已解决失败必须映射当前步骤。")).toBeVisible();
    expect(saveButton).toBeDisabled();
    fireEvent.change(mapping, { target: { value: "unknown-step" } });
    expect(screen.getByText(/失败映射包含未知或重复步骤/)).toBeVisible();
    fireEvent.change(mapping, { target: { value: source.plan.steps[0].id } });
    expect(saveButton).toBeEnabled();
    expect(save).not.toHaveBeenCalled();
    expect(JSON.stringify(m)).toBe(frozenInput);
    expect(initial.originalPlan.adjustments?.[0].step_ids).toEqual([]);
  },
);
