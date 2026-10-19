import { useState } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { mission } from "../test/fixtures";
import type { PlanRevision } from "../types";
import {
  createPlanDraft,
  PlanRevisionEditor,
  type PlanDraft,
} from "./PlanRevisionEditor";

function Editor({ history = [] }: { history?: PlanRevision[] }) {
  const [draft, setDraft] = useState<PlanDraft>(() => createPlanDraft(mission));
  return (
    <PlanRevisionEditor
      mission={mission}
      draft={draft}
      history={history}
      historyLoading={false}
      historyError=""
      busy={false}
      saveError=""
      onDraftChange={setDraft}
      onSave={vi.fn()}
      onDiscard={() => setDraft(createPlanDraft(mission))}
      onRebase={vi.fn()}
    />
  );
}

const openEditor = () =>
  fireEvent.click(
    screen.getByRole("button", { name: "修订计划（保存不审批）" }),
  );

it("keeps the list-first plan hierarchy with an explicit expandable editor", () => {
  render(<Editor />);
  const entry = screen.getByRole("button", {
    name: "修订计划（保存不审批）",
  });
  expect(entry).toHaveAttribute("aria-expanded", "false");
  fireEvent.click(entry);
  expect(entry).toHaveAttribute("aria-expanded", "true");
  expect(screen.getByLabelText("计划摘要")).toBeVisible();
});

it("edits bounded plan fields and reports field, dependency, file, and order changes", () => {
  render(<Editor />);
  openEditor();
  fireEvent.change(screen.getByLabelText("计划摘要"), {
    target: { value: "先实现，再验证" },
  });
  fireEvent.click(screen.getByRole("button", { name: "添加步骤" }));
  const steps = screen.getAllByRole("group", { name: /计划步骤/ });
  fireEvent.change(within(steps[1]).getByLabelText("步骤 ID"), {
    target: { value: "s2" },
  });
  fireEvent.change(within(steps[1]).getByLabelText("步骤标题"), {
    target: { value: "运行验收" },
  });
  fireEvent.change(within(steps[1]).getByLabelText("步骤说明"), {
    target: { value: "验证实际结果" },
  });
  fireEvent.change(within(steps[1]).getByLabelText("依赖（每行一项）"), {
    target: { value: "s1" },
  });
  fireEvent.change(within(steps[1]).getByLabelText("文件（每行一项）"), {
    target: { value: "src/check.ts\nsrc/report.ts" },
  });
  fireEvent.click(within(steps[1]).getByRole("button", { name: "上移" }));

  const diff = screen.getByRole("region", { name: "修订差异" });
  expect(diff).toHaveTextContent("摘要");
  expect(diff).toHaveTextContent("新增步骤 s2");
  expect(diff).toHaveTextContent("步骤顺序");
  expect(diff).toHaveTextContent("依赖：s1");
  expect(diff).toHaveTextContent("文件：src/check.ts、src/report.ts");
  expect(screen.getByLabelText("修订原因")).toHaveAttribute(
    "maxLength",
    "1000",
  );
  expect(screen.getByLabelText("计划摘要")).toHaveAttribute(
    "maxLength",
    "4000",
  );
});

it("preserves keyboard newlines and explains before/after list and description changes", async () => {
  const user = userEvent.setup();
  render(<Editor />);
  openEditor();
  const step = screen.getByRole("group", { name: "计划步骤 1" });
  const description = within(step).getByLabelText("步骤说明");
  await user.clear(description);
  await user.type(description, "改进说明");
  const dependencies = within(step).getByLabelText("依赖（每行一项）");
  await user.type(dependencies, "s2{Enter}s3");
  const files = within(step).getByLabelText("文件（每行一项）");
  await user.clear(files);
  await user.type(files, "src/check.ts{Enter}src/report.ts");

  expect(dependencies).toHaveValue("s2\ns3");
  expect(files).toHaveValue("src/check.ts\nsrc/report.ts");
  const diff = screen.getByRole("region", { name: "修订差异" });
  expect(diff).toHaveTextContent("说明：保留既有接口 → 改进说明");
  expect(diff).toHaveTextContent("依赖新增：s2、s3");
  expect(diff).toHaveTextContent("文件新增：src/check.ts、src/report.ts");
  expect(diff).toHaveTextContent("文件删除：src/import.ts");
});

it("uses UTF-8 byte and file-count limits before sending a revision", () => {
  render(<Editor />);
  openEditor();
  fireEvent.change(screen.getByLabelText("计划摘要"), {
    target: { value: "汉".repeat(1400) },
  });
  fireEvent.change(screen.getByLabelText("修订原因"), {
    target: { value: "必要原因" },
  });
  expect(screen.getByRole("alert")).toHaveTextContent(
    /摘要不能超过 4000 UTF-8 字节/,
  );
  expect(
    screen.getByRole("button", { name: "保存修订（不审批）" }),
  ).toBeDisabled();

  fireEvent.change(screen.getByLabelText("计划摘要"), {
    target: { value: "有效摘要" },
  });
  fireEvent.change(screen.getByLabelText("文件（每行一项）"), {
    target: {
      value: Array.from({ length: 31 }, (_, index) => `src/${index}.ts`).join(
        "\n",
      ),
    },
  });
  expect(screen.getByRole("alert")).toHaveTextContent(/每个步骤最多 30 个文件/);
});

it("adds, removes, and reorders steps without hiding the reason or save boundary", () => {
  render(<Editor />);
  openEditor();
  fireEvent.click(screen.getByRole("button", { name: "添加步骤" }));
  expect(screen.getAllByRole("group", { name: /计划步骤/ })).toHaveLength(2);
  fireEvent.click(screen.getAllByRole("button", { name: "删除步骤" })[0]);
  expect(screen.getAllByRole("group", { name: /计划步骤/ })).toHaveLength(1);
  expect(screen.getByLabelText("修订原因")).toBeVisible();
  expect(
    screen.getByRole("button", { name: "保存修订（不审批）" }),
  ).toBeDisabled();
  expect(
    screen.queryByRole("button", { name: /批准/ }),
  ).not.toBeInTheDocument();
});

it("renders truthful revision lineage and an independent history error", () => {
  const record: PlanRevision = {
    mission_id: "m1",
    plan_revision: 4,
    parent_plan_revision: 3,
    epoch: 1,
    mission_revision: 10,
    source: "user",
    reason: "拆分实现与验证",
    plan: { ...mission.plan!, revision: 4 },
    plan_digest: "plan-body-sha",
    created_at: "2026-10-18T08:00:00Z",
  };
  const { rerender } = render(<Editor history={[record]} />);
  expect(screen.getByText(/计划 v4/)).toBeVisible();
  expect(screen.getByText(/父计划 v3/)).toBeVisible();
  expect(screen.getByText(/任务 r10/)).toBeVisible();
  expect(screen.getByText("拆分实现与验证")).toBeVisible();
  const historyDisclosure = screen
    .getByText("查看历史计划正文")
    .closest("details")!;
  fireEvent.click(within(historyDisclosure).getByText("查看历史计划正文"));
  expect(
    within(historyDisclosure).getByText("s1 · 检查校验边界"),
  ).toBeVisible();
  expect(within(historyDisclosure).getByText("保留既有接口")).toBeVisible();
  expect(within(historyDisclosure).getByText("依赖：无")).toBeVisible();
  expect(
    within(historyDisclosure).getByText("文件：src/import.ts"),
  ).toBeVisible();

  const draft = createPlanDraft(mission);
  rerender(
    <PlanRevisionEditor
      mission={mission}
      draft={draft}
      history={[]}
      historyLoading={false}
      historyError="修订历史暂时不可用"
      busy={false}
      saveError=""
      onDraftChange={() => {}}
      onSave={() => {}}
      onDiscard={() => {}}
      onRebase={() => {}}
    />,
  );
  expect(screen.getByRole("status")).toHaveTextContent("修订历史暂时不可用");
  expect(
    screen.getByRole("button", { name: "修订计划（保存不审批）" }),
  ).toBeVisible();
});

it("renders nil historical dependency and file slices as empty lists", () => {
  const nullableRecord = {
    mission_id: "m1",
    plan_revision: 3,
    parent_plan_revision: 0,
    epoch: 1,
    mission_revision: 9,
    source: "planner",
    reason: "",
    plan: {
      ...mission.plan!,
      steps: [
        {
          ...mission.plan!.steps[0],
          depends_on: null,
          files: null,
        },
      ],
    },
    plan_digest: "nullable-plan-body-sha",
    created_at: "2026-10-18T07:00:00Z",
  } as unknown as PlanRevision;

  expect(() => render(<Editor history={[nullableRecord]} />)).not.toThrow();
  const historyDisclosure = screen
    .getByText("查看历史计划正文")
    .closest("details")!;
  fireEvent.click(within(historyDisclosure).getByText("查看历史计划正文"));
  expect(within(historyDisclosure).getByText("依赖：无")).toBeVisible();
  expect(within(historyDisclosure).getByText("文件：无")).toBeVisible();
});
