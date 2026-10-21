import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { mission } from "../test/fixtures";
import { VerificationView } from "./VerificationView";
import type { Verification } from "../types";
import { MissionViews } from "./MissionViews";

it("shows controller lifecycle without upgrading worker success to tests or provider receipts", () => {
  const traced = {
    ...mission,
    coding_version: 1,
    coding_attempts: [
      {
        id: "local-attempt",
        epoch: 2,
        plan_revision: 1,
        phase: "worker",
        role: "worker" as const,
        step_id: "s1",
        status: "succeeded",
        before: { id: "g0", digest: "d0" },
        after: { id: "g1", digest: "d1" },
        started_at: "now",
        finished_at: "later",
      },
      {
        id: "local-check",
        epoch: 2,
        plan_revision: 1,
        phase: "verification",
        status: "failed",
        failure_code: "verification_failed",
        verification_id: "v-check",
        started_at: "now",
        finished_at: "later",
      },
    ],
  };
  render(<VerificationView mission={traced} />);
  expect(screen.getByText("候选修改已生成")).toBeVisible();
  expect(screen.getByText("verification_failed")).toBeVisible();
  expect(screen.getByText("v-check")).toBeVisible();
  expect(screen.getByText(/本地尝试 ID，不是提供方回执/)).toBeVisible();
  expect(screen.queryByText("测试通过")).not.toBeInTheDocument();
});

it.each([
  ["running", "执行中"],
  ["interrupted", "已中断"],
  ["outcome_unknown", "结果未知"],
])("shows %s lifecycle in existing plan rows", (status, label) => {
  const traced = {
    ...mission,
    coding_version: 1,
    coding_attempts: [
      {
        id: "attempt",
        epoch: mission.epoch,
        plan_revision: mission.plan!.revision,
        phase: "worker",
        role: "worker" as const,
        step_id: mission.plan!.steps[0].id,
        status,
        started_at: "now",
      },
    ],
  };
  render(<MissionViews mission={traced} view="计划链路" />);
  expect(screen.getAllByText(new RegExp(label))).not.toHaveLength(0);
  expect(
    screen.queryByRole("button", { name: /批准/ }),
  ).not.toBeInTheDocument();
});

it("keeps historical absence distinct from an empty new trace", () => {
  const { rerender } = render(<VerificationView mission={mission} />);
  expect(screen.getByText("历史任务未记录执行尝试")).toBeVisible();
  rerender(<VerificationView mission={{ ...mission, coding_version: 1 }} />);
  expect(screen.getByText("尚无执行尝试；执行仍需单独批准")).toBeVisible();
});

it.each([
  [1, "synthetic_stub", "流程模拟（未执行命令）", "模拟验证"],
  [1, "synthetic_content_check", "内容检查模拟（未执行命令）", "模拟验证"],
  [1, "isolated_execution", "隔离命令执行", "验收命令"],
  [1, "unknown", "来源未记录", "验证记录"],
  [1, "future", "来源未记录", "验证记录"],
  [2, "isolated_execution", "来源未记录", "验证记录"],
  [undefined, "isolated_execution", "来源未记录", "验证记录"],
  [undefined, undefined, "来源未记录", "验证记录"],
])(
  "shows source %s/%s without upgrading success or failure",
  (version, kind, label, outcome) => {
    const evidence = [true, false].map(
      (passed, i) =>
        ({
          id: String(i),
          candidate_digest: "same",
          image_id: "sha256:isolated_execution",
          created_at: "today",
          acceptance_revision: 1,
          passed,
          results: [
            {
              command: "test",
              exit_code: passed ? 0 : 1,
              output: "",
              timed_out: false,
            },
          ],
          source_version: version,
          source_kind: kind,
        }) as Verification,
    );
    render(
      <VerificationView mission={{ ...mission, evidence, reviews: [] }} />,
    );
    expect(
      screen.getAllByRole("heading", {
        name: new RegExp(label!.replace(/[（ ）]/g, ".")),
      }),
    ).toHaveLength(2);
    expect(screen.getByText(`${outcome}通过`)).toBeVisible();
    expect(screen.getByText(`${outcome}未通过`)).toBeVisible();
    if (kind !== "isolated_execution" || version !== 1)
      expect(screen.queryByText("验收命令通过")).not.toBeInTheDocument();
  },
);

it("retains individual sources for mixed attempts on the same candidate", () => {
  const evidence = ["synthetic_stub", "isolated_execution"].map(
    (source_kind, i) => ({
      id: String(i),
      candidate_digest: "same",
      image_id: "image",
      created_at: "today",
      acceptance_revision: 1,
      passed: true,
      results: [],
      source_version: 1,
      source_kind,
    }),
  );
  render(<VerificationView mission={{ ...mission, evidence, reviews: [] }} />);
  expect(
    screen.getByRole("heading", { name: /流程模拟（未执行命令）/ }),
  ).toBeVisible();
  expect(screen.getByRole("heading", { name: /隔离命令执行/ })).toBeVisible();
});
