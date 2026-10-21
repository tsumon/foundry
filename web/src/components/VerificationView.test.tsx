import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { mission } from "../test/fixtures";
import { VerificationView } from "./VerificationView";
import type { Verification, Mission } from "../types";
import { MissionViews } from "./MissionViews";
import { DecisionPanel } from "./DecisionPanel";

function reviewedCodingMission(): Mission {
  return {
    ...mission,
    coding_version: 1,
    state: "awaiting_delivery",
    candidate_digest: "current",
    approvals: [
      ...mission.approvals,
      {
        kind: "execute",
        digest: "approved",
        plan_revision: 3,
        revision: 9,
        created_at: "today",
      },
    ],
    evidence: [
      {
        id: "verification-current",
        candidate_digest: "current",
        image_id: mission.spec.image_id,
        acceptance_revision: 3,
        passed: true,
        created_at: "today",
        source_version: 1,
        source_kind: "synthetic_content_check",
        results: [
          {
            command: "npm test",
            output: "SIMULATED",
            exit_code: 0,
            timed_out: false,
          },
        ],
      },
    ],
    reviews: [
      {
        role: "qa",
        passed: true,
        summary: "QA assessment",
        findings: [],
        candidate_digest: "current",
        coding_version: 1,
        plan_revision: 3,
        trace_digest: "trace",
        binding_digest: "binding",
        checks: [
          {
            id: "acceptance:0",
            passed: true,
            summary: "Acceptance simulated result",
            paths: [],
            acceptance_index: 0,
            verification_id: "verification-current",
          },
        ],
      },
      {
        role: "auditor",
        passed: true,
        summary: "Auditor assessment",
        findings: [],
        candidate_digest: "current",
        coding_version: 1,
        plan_revision: 3,
        trace_digest: "trace",
        binding_digest: "binding",
        checks: ["scope", "protected", "approval", "integrity"].map((id) => ({
          id,
          passed: true,
          summary: `Review ${id}`,
          paths: [],
          reference_digest: `ref-${id}`,
        })),
      },
    ],
  } as Mission;
}

it("renders distinct mandatory review references and historical absence truthfully", () => {
  const { rerender } = render(
    <VerificationView mission={reviewedCodingMission()} />,
  );
  expect(screen.getByText("acceptance:0")).toBeVisible();
  expect(screen.getByText("ref-approval")).toBeVisible();
  expect(
    screen.getByRole("link", { name: "verification-current" }),
  ).toHaveAttribute("href", "#verification-verification-current");
  expect(screen.getByText(/模型审查 · 非命令执行证据/)).toBeVisible();
  rerender(
    <VerificationView
      mission={{
        ...mission,
        reviews: [
          {
            role: "qa",
            passed: true,
            summary: "old",
            findings: [],
            candidate_digest: "old",
          },
        ],
      }}
    />,
  );
  expect(
    screen.getByText("历史审查未记录必需清单，不能补推为证据核对"),
  ).toBeVisible();
});

it.each([
  "missing",
  "duplicate",
  "stale",
  "wrong_plan",
  "failed",
  "approval",
  "unknown",
])("blocks C2 delivery UI with %s review material", (kind) => {
  const m = reviewedCodingMission();
  if (kind === "missing") m.reviews[0].checks = [];
  if (kind === "duplicate") m.reviews[1].checks![1] = m.reviews[1].checks![0];
  if (kind === "stale") m.reviews[0].checks![0].verification_id = "old";
  if (kind === "wrong_plan") m.reviews[0].plan_revision = 2;
  if (kind === "failed") m.reviews[0].checks![0].passed = false;
  if (kind === "approval") m.approvals = [];
  if (kind === "unknown") m.reviews[0].coding_version = 9;
  render(<DecisionPanel mission={m} busy={false} onAction={vi.fn()} />);
  expect(screen.getByRole("button", { name: "批准并导出交付" })).toBeDisabled();
});

it("keeps C2 current review and delivery approval separate", () => {
  const action = vi.fn();
  render(
    <DecisionPanel
      mission={reviewedCodingMission()}
      busy={false}
      onAction={action}
    />,
  );
  expect(screen.getByRole("button", { name: "批准并导出交付" })).toBeEnabled();
  expect(action).not.toHaveBeenCalled();
  expect(screen.getByText(/清单完整性/)).toBeVisible();
});

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
