import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  c4CandidateOperation,
  c4Mission,
  c4Operation,
  c4Profile,
} from "../test/c4Fixtures";
import { C4Decision, C4Verification } from "./C4Verification";
import { DecisionPanel } from "./DecisionPanel";
import { DecisionQueue } from "./DecisionQueue";
import { MissionWorkspace } from "./MissionWorkspace";
import { views } from "./MissionViews";
import { api, APIError } from "../api";
import type { Mission } from "../types";

const m: Mission = {
  ...c4Mission,
  state: "draft",
  spec: { ...c4Mission.spec, c4: c4Profile },
  c4_operations: [
    {
      ...c4Operation,
      state: "prepared",
      approval: undefined,
      dispatch: undefined,
      created: undefined,
      started: undefined,
      result: undefined,
      cleanup: [],
      container_id: undefined,
    },
  ],
};

afterEach(() => vi.restoreAllMocks());
function status(current = m) {
  return {
    mission_id: current.id,
    revision: current.revision,
    profile: current.spec.c4!,
    operations: current.c4_operations!,
    read_only: false,
    simulation: true,
    real_execution: false,
  };
}

const matchingCandidateApproval = {
  kind: "execute",
  plan_revision: c4CandidateOperation.plan_revision,
  digest: c4CandidateOperation.approval!.digest,
  revision: c4CandidateOperation.approval!.revision,
  created_at: c4CandidateOperation.approval!.at,
};

function candidateMission(changes: Partial<Mission> = {}): Mission {
  return {
    ...c4Mission,
    state: "blocked",
    candidate_digest: c4CandidateOperation.candidate_digest!,
    candidate_generation: c4CandidateOperation.candidate_generation!,
    c4_operations: [c4Operation, c4CandidateOperation],
    approvals: [...c4Mission.approvals, matchingCandidateApproval],
    ...changes,
  };
}
it("requires separate explicit approval and execution, displays immutable permissions, never acts on mount", async () => {
  vi.spyOn(api, "c4").mockResolvedValue(status());
  const approved = {
    ...m,
    revision: m.revision + 1,
    c4_operations: [
      {
        ...m.c4_operations![0],
        state: "approved",
        approval: {
          digest: c4Operation.material_digest,
          revision: m.revision,
          at: c4Operation.prepared_at,
        },
      },
    ],
  };
  const action = vi
    .spyOn(api, "c4Action")
    .mockResolvedValue({ mission: approved });
  const update = vi.fn();
  const { rerender } = render(
    <C4Decision mission={m} busy={false} onUpdate={update} />,
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "批准基线材料" })).toBeEnabled(),
  );
  expect(action).not.toHaveBeenCalled();
  expect(screen.getByText(/256 MiB.*exec/)).toBeInTheDocument();
  expect(screen.getByText(/C4-C 未执行/)).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /启用真实/ }),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "批准基线材料" }));
  await waitFor(() => expect(update).toHaveBeenCalledWith(approved));
  expect(action).toHaveBeenCalledExactlyOnceWith(m.id, {
    action: "approve",
    revision: m.revision,
    operation_id: "baseline-1",
    material_digest: c4Operation.material_digest,
  });
  vi.mocked(api.c4).mockResolvedValue(status(approved));
  rerender(<C4Decision mission={approved} busy={false} onUpdate={update} />);
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "执行已批准基线（模拟）" }),
    ).toBeEnabled(),
  );
  expect(action).toHaveBeenCalledTimes(1);
  expect(screen.getByText(/已批准但尚未执行/)).toBeInTheDocument();
});
it("blocks busy and stale data, and performs GET-only refresh after an uncertain action", async () => {
  vi.spyOn(api, "c4").mockResolvedValue(status());
  vi.spyOn(api, "mission").mockResolvedValue({ mission: m });
  const action = vi
    .spyOn(api, "c4Action")
    .mockRejectedValue(new APIError(409, "c4_not_passed", "cleanup failed"));
  const { rerender } = render(
    <C4Decision mission={m} busy onUpdate={vi.fn()} />,
  );
  expect(screen.getByRole("button", { name: "批准基线材料" })).toBeDisabled();
  rerender(<C4Decision mission={m} busy={false} onUpdate={vi.fn()} />);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "批准基线材料" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "批准基线材料" }));
  await screen.findByText(/未自动重试/);
  expect(action).toHaveBeenCalledTimes(1);
  expect(api.mission).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "批准基线材料" })).toBeDisabled();
  vi.mocked(api.c4).mockResolvedValue({
    ...status(),
    revision: m.revision - 1,
  });
  fireEvent.click(screen.getByRole("button", { name: "刷新 C4 事实" }));
  await screen.findByText(/版本不一致/);
  expect(screen.getByRole("button", { name: "批准基线材料" })).toBeDisabled();
});
it("separates baseline/candidate and does not call failed cleanup a success", () => {
  render(
    <C4Verification
      mission={{
        ...m,
        approvals: [...m.approvals, matchingCandidateApproval],
        c4_operations: [
          c4Operation,
          {
            ...c4CandidateOperation,
            cleanup: [{ ...c4Operation.cleanup![0], state: "failed" }],
          },
        ],
      }}
    />,
  );
  expect(
    screen.getByRole("region", { name: "C4 基线记录" }),
  ).toBeInTheDocument();
  expect(screen.getByRole("region", { name: "C4 候选记录" })).toHaveTextContent(
    "清理未确认",
  );
  expect(
    screen.getByRole("region", { name: "C4 候选记录" }),
  ).not.toHaveTextContent("模拟验证通过");
});

it("labels only approval-bound candidate results as passing", () => {
  const invalid = candidateMission({ approvals: c4Mission.approvals });
  const { rerender } = render(<C4Verification mission={invalid} />);

  expect(
    screen.getByRole("heading", {
      name: "candidate-1 · 审批绑定缺失或不匹配 · 不能确认通过",
    }),
  ).toBeVisible();
  expect(
    screen.getByRole("region", { name: "C4 候选记录" }),
  ).not.toHaveTextContent("模拟验证通过");
  expect(screen.getByRole("region", { name: "C4 基线记录" })).toHaveTextContent(
    "模拟验证通过 · 非真实执行",
  );

  rerender(<C4Verification mission={candidateMission()} />);
  expect(
    screen.getByRole("heading", {
      name: "candidate-1 · 模拟验证通过 · 非真实执行",
    }),
  ).toBeVisible();
});

it("blocks delivery when a passing-shaped candidate lacks its copied approval", () => {
  const evidence = {
    id: "verification-current",
    candidate_digest: c4CandidateOperation.candidate_digest!,
    image_id: c4Mission.spec.image_id,
    acceptance_revision: c4Mission.plan!.revision,
    passed: true,
    created_at: "2026-10-21T00:00:07Z",
    results: [],
  };
  const reviews = (["qa", "auditor"] as const).map((role) => ({
    role,
    passed: true,
    summary: `${role} passed`,
    findings: [],
    candidate_digest: c4CandidateOperation.candidate_digest!,
  }));
  const deliverable = candidateMission({
    state: "awaiting_delivery",
    evidence: [evidence],
    reviews,
  });
  const { rerender } = render(
    <DecisionPanel
      mission={deliverable}
      busy={false}
      c4Available
      onAction={vi.fn()}
    />,
  );
  expect(screen.getByRole("button", { name: "批准并导出交付" })).toBeEnabled();

  rerender(
    <DecisionPanel
      mission={{ ...deliverable, approvals: c4Mission.approvals }}
      busy={false}
      c4Available
      onAction={vi.fn()}
    />,
  );
  expect(screen.getByRole("button", { name: "批准并导出交付" })).toBeDisabled();
});

it("shows the missing candidate approval in the latest-candidate queue fact", () => {
  render(
    <DecisionQueue
      missions={[
        candidateMission({
          approvals: c4Mission.approvals,
          error: "candidate approval binding unavailable",
        }),
      ]}
      loading={false}
      error=""
      onOpenMission={vi.fn()}
    />,
  );

  const queue = screen.getByRole("region", { name: "需要处理" });
  expect(queue).toHaveTextContent("审批绑定缺失或不匹配 · 不能确认通过");
  expect(queue).toHaveTextContent("基线模拟验证通过 · 非真实执行");
  expect(queue).not.toHaveTextContent("最新候选记录模拟验证通过 · 非真实执行");
});

it("shows baseline failure in the existing attention queue without offering plan approval", () => {
  const failed = {
    ...m,
    c4_operations: [
      {
        ...c4Operation,
        result: { ...c4Operation.result!, class: "infrastructure_failed" },
        cleanup: [{ ...c4Operation.cleanup![0], state: "failed" }],
      },
    ],
  };
  render(
    <DecisionQueue
      missions={[failed]}
      loading={false}
      error=""
      onOpenMission={vi.fn()}
    />,
  );
  expect(screen.getByRole("region", { name: "需要处理" })).toHaveTextContent(
    "infrastructure_failed",
  );
  expect(screen.getByText(/精确清理需单独点击/)).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /批准并执行计划/ }),
  ).not.toBeInTheDocument();
});
it("does not infer synthetic provenance or passing readiness from an unsupported profile", () => {
  render(
    <C4Verification
      mission={{
        ...m,
        spec: { ...m.spec, c4: { ...m.spec.c4!, schema_version: 2 } },
        c4_operations: [c4Operation],
      }}
    />,
  );
  expect(screen.getByText(/来源未确认/)).toBeInTheDocument();
  expect(screen.queryByText(/模拟验证通过/)).not.toBeInTheDocument();
});
it("keeps a partial schema1 profile unknown in the read-only verification display", () => {
  render(
    <C4Verification
      mission={{
        ...m,
        spec: { ...m.spec, c4: { ...m.spec.c4!, verifier: "" } },
        c4_operations: [c4Operation],
      }}
    />,
  );
  expect(screen.getByText(/来源未确认/)).toBeInTheDocument();
  expect(screen.getByRole("region", { name: "C4 基线记录" })).toHaveTextContent(
    "配置不支持 · 仅显示原始事实",
  );
  expect(screen.queryByText(/模拟验证通过/)).not.toBeInTheDocument();
});
it("keeps unsupported passing baseline facts unknown in the decision panel", async () => {
  const unsupported: Mission = {
    ...m,
    spec: { ...m.spec, c4: { ...m.spec.c4!, schema_version: 2 } },
    c4_operations: [c4Operation],
  };
  vi.spyOn(api, "c4").mockResolvedValue({
    ...status(unsupported),
    read_only: true,
    simulation: false,
  });
  render(<C4Decision mission={unsupported} busy={false} onUpdate={vi.fn()} />);
  await waitFor(() =>
    expect(screen.queryByText(/正在读取 C4/)).not.toBeInTheDocument(),
  );
  expect(screen.getByText("配置不支持 · 仅显示原始事实")).toBeInTheDocument();
  expect(screen.queryByText(/模拟验证通过/)).not.toBeInTheDocument();
});
it("keeps a passing-shaped wrong-mission operation unknown in the decision panel", async () => {
  const invalid: Mission = {
    ...m,
    c4_operations: [{ ...c4Operation, mission_id: "other" }],
  };
  vi.spyOn(api, "c4").mockResolvedValue({
    ...status(invalid),
    read_only: true,
    simulation: false,
  });
  render(<C4Decision mission={invalid} busy={false} onUpdate={vi.fn()} />);
  await waitFor(() =>
    expect(screen.queryByText(/正在读取 C4/)).not.toBeInTheDocument(),
  );
  expect(screen.getByText("配置不支持 · 仅显示原始事实")).toBeInTheDocument();
  expect(screen.queryByText(/模拟验证通过/)).not.toBeInTheDocument();
});
it("keeps unsupported passing facts unknown in the attention queue", () => {
  render(
    <DecisionQueue
      missions={[
        {
          ...m,
          state: "blocked",
          spec: { ...m.spec, c4: { ...m.spec.c4!, schema_version: 2 } },
          c4_operations: [c4Operation, c4CandidateOperation],
        },
      ]}
      loading={false}
      error=""
      onOpenMission={vi.fn()}
    />,
  );
  expect(screen.getByRole("region", { name: "需要处理" })).toHaveTextContent(
    "配置不支持 · 仅显示原始事实",
  );
  expect(screen.queryByText(/模拟验证通过/)).not.toBeInTheDocument();
});
it("keeps missing operation bindings unknown in the attention queue", () => {
  render(
    <DecisionQueue
      missions={[
        {
          ...m,
          state: "blocked",
          c4_operations: [{ ...c4Operation, profile_digest: "" }],
        },
      ]}
      loading={false}
      error=""
      onOpenMission={vi.fn()}
    />,
  );
  expect(screen.getByRole("region", { name: "需要处理" })).toHaveTextContent(
    "配置不支持 · 仅显示原始事实",
  );
  expect(screen.queryByText(/模拟验证通过/)).not.toBeInTheDocument();
});
it("retains latest candidate stop facts and mission error separately from a passing baseline", () => {
  render(
    <DecisionQueue
      missions={[
        {
          ...m,
          state: "blocked",
          base_commit: c4Operation.base_commit,
          error: "candidate cleanup requires explicit recovery",
          approvals: [...m.approvals, matchingCandidateApproval],
          c4_operations: [
            c4Operation,
            { ...c4CandidateOperation, id: "older-candidate" },
            {
              ...c4CandidateOperation,
              id: "stopped-candidate",
              material_digest: "5".repeat(64),
              result: {
                ...c4Operation.result!,
                class: "infrastructure_failed",
              },
              cleanup: [{ ...c4Operation.cleanup![0], state: "failed" }],
            },
          ],
        },
      ]}
      loading={false}
      error=""
      onOpenMission={vi.fn()}
    />,
  );
  const queue = screen.getByRole("region", { name: "需要处理" });
  expect(queue).toHaveTextContent("infrastructure_failed");
  expect(queue).toHaveTextContent("清理未确认，不能继续");
  expect(queue).toHaveTextContent(
    "candidate cleanup requires explicit recovery",
  );
  expect(queue).toHaveTextContent("stopped-candidate");
  expect(queue).toHaveTextContent("5".repeat(64));
  expect(queue).not.toHaveTextContent("older-candidate");
  expect(queue).toHaveTextContent("基线模拟验证通过 · 非真实执行");
});
it("keeps independent context approval disabled until clean baseline facts exist", () => {
  render(<DecisionPanel mission={m} busy={false} onAction={vi.fn()} />);
  fireEvent.click(screen.getByRole("checkbox", { name: /我已检查目标/ }));
  expect(
    screen.getByRole("button", { name: "授权上下文并生成计划" }),
  ).toBeDisabled();
});

it("preparation is explicit and real/read-only/unsupported capabilities never enable a mutation", async () => {
  const empty = { ...m, c4_operations: [] };
  const read = vi.spyOn(api, "c4").mockResolvedValue(status(empty));
  const action = vi.spyOn(api, "c4Action").mockResolvedValue({ mission: m });
  const update = vi.fn();
  const { rerender } = render(
    <C4Decision mission={empty} busy={false} onUpdate={update} />,
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "准备基线材料" })).toBeEnabled(),
  );
  expect(action).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "准备基线材料" }));
  await waitFor(() => expect(update).toHaveBeenCalledWith(m));
  expect(action).toHaveBeenCalledExactlyOnceWith(m.id, {
    action: "prepare",
    revision: m.revision,
  });
  for (const changed of [
    { read_only: true },
    { simulation: false },
    { real_execution: true },
  ]) {
    read.mockResolvedValue({ ...status(empty), ...changed });
    fireEvent.click(screen.getByRole("button", { name: "刷新 C4 事实" }));
    await waitFor(() =>
      expect(screen.queryByText(/正在读取 C4/)).not.toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: "准备基线材料" })).toBeDisabled();
  }
  rerender(
    <C4Decision
      mission={{
        ...empty,
        spec: { ...empty.spec, c4: { ...empty.spec.c4!, schema_version: 2 } },
      }}
      busy={false}
      onUpdate={update}
    />,
  );
  expect(screen.getByRole("button", { name: "准备基线材料" })).toBeDisabled();
});

it("known exact-owned cleanup is an explicit cleanup-only request, never execute", async () => {
  const failed = {
    ...m,
    c4_operations: [
      {
        ...c4Operation,
        result: { ...c4Operation.result!, class: "outcome_unknown" },
        cleanup: [{ ...c4Operation.cleanup![0], state: "failed" }],
      },
    ],
  };
  vi.spyOn(api, "c4").mockResolvedValue(status(failed));
  const action = vi
    .spyOn(api, "c4Action")
    .mockResolvedValue({ mission: failed });
  render(<C4Decision mission={failed} busy={false} onUpdate={vi.fn()} />);
  const button = screen.getByRole("button", { name: /仅清理已记录对象/ });
  await waitFor(() => expect(button).toBeEnabled());
  expect(action).not.toHaveBeenCalled();
  expect(
    screen.queryByRole("button", { name: /执行已批准/ }),
  ).not.toBeInTheDocument();
  fireEvent.click(button);
  await waitFor(() =>
    expect(action).toHaveBeenCalledExactlyOnceWith(m.id, {
      action: "cleanup",
      revision: m.revision,
      operation_id: "baseline-1",
      material_digest: c4Operation.material_digest,
      container_id: c4Operation.container_id,
    }),
  );
});

it("stops approved stale material and keeps Main chat from invalidating pending baseline", async () => {
  const stale = {
    ...m,
    state: "paused",
    approvals: [
      {
        kind: "context",
        revision: 1,
        plan_revision: 0,
        digest: "consent",
        created_at: "today",
      },
    ],
    c4_operations: [
      {
        ...m.c4_operations![0],
        state: "approved",
        approval: {
          digest: c4Operation.material_digest,
          revision: m.revision - 2,
          at: c4Operation.prepared_at,
        },
      },
    ],
  };
  vi.spyOn(api, "c4").mockResolvedValue(status(stale));
  vi.spyOn(api, "planRevisions").mockResolvedValue({ plan_revisions: [] });
  vi.spyOn(api, "localOperations").mockResolvedValue({ local_operations: [] });
  render(<MissionWorkspace mission={stale} onUpdate={vi.fn()} />);
  await screen.findByText(/已批准材料已过期/);
  expect(
    screen.getByRole("button", { name: "执行已批准基线（模拟）" }),
  ).toBeDisabled();
  expect(screen.getByRole("textbox", { name: "向 Main 提问" })).toBeDisabled();
});

it("does not retry failed cleanup, but permits a new explicit cleanup at a new durable revision", async () => {
  const failed = {
    ...m,
    c4_operations: [
      {
        ...c4Operation,
        result: { ...c4Operation.result!, class: "infrastructure_failed" },
        cleanup: [{ ...c4Operation.cleanup![0], state: "failed" }],
      },
    ],
  };
  const next = { ...failed, revision: failed.revision + 1 };
  const read = vi.spyOn(api, "c4").mockResolvedValue(status(failed));
  const action = vi
    .spyOn(api, "c4Action")
    .mockRejectedValue(new APIError(409, "c4_not_passed", "cleanup failed"));
  vi.spyOn(api, "mission").mockResolvedValue({ mission: next });
  const { rerender } = render(
    <C4Decision mission={failed} busy={false} onUpdate={vi.fn()} />,
  );
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: /仅清理已记录对象/ }),
    ).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: /仅清理已记录对象/ }));
  await screen.findByText(/未自动重试/);
  expect(
    screen.getByRole("button", { name: /仅清理已记录对象/ }),
  ).toBeDisabled();
  read.mockResolvedValue(status(next));
  rerender(<C4Decision mission={next} busy={false} onUpdate={vi.fn()} />);
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: /仅清理已记录对象/ }),
    ).toBeEnabled(),
  );
  expect(action).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: /仅清理已记录对象/ }));
  await waitFor(() => expect(action).toHaveBeenCalledTimes(2));
  expect(action.mock.calls[1][1]).toMatchObject({
    action: "cleanup",
    revision: next.revision,
  });
});

it("uses the existing workspace tabs and persisted C4 event labels, no new navigation", async () => {
  vi.spyOn(api, "c4").mockResolvedValue(status());
  vi.spyOn(api, "c4Action");
  vi.spyOn(api, "planRevisions").mockResolvedValue({ plan_revisions: [] });
  vi.spyOn(api, "localOperations").mockResolvedValue({ local_operations: [] });
  render(
    <MissionWorkspace
      mission={m}
      onUpdate={vi.fn()}
      events={[
        {
          seq: 1,
          mission_id: m.id,
          revision: m.revision,
          kind: "c4.prepared",
          created_at: "today",
        },
      ]}
    />,
  );
  expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual([
    ...views,
  ]);
  expect(screen.getByText(/C4 材料已准备 · c4.prepared/)).toBeInTheDocument();
  await waitFor(() => expect(api.c4).toHaveBeenCalledTimes(1));
  expect(api.c4Action).not.toHaveBeenCalled();
});
