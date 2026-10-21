import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { mission } from "../test/fixtures";
import { c4Operation } from "../test/c4Fixtures";
import { C4Decision, C4Verification } from "./C4Verification";
import { DecisionPanel } from "./DecisionPanel";
import { DecisionQueue } from "./DecisionQueue";
import { MissionWorkspace } from "./MissionWorkspace";
import { views } from "./MissionViews";
import { api, APIError } from "../api";
import type { Mission } from "../types";

const m: Mission = {
  ...mission,
  state: "draft",
  spec: {
    ...mission.spec,
    c4: {
      schema_version: 1,
      id: "c4_go_v1",
      mode: "synthetic",
      image_id: "sha256:" + "a".repeat(64),
      platform: "linux/arm64",
      docker_path: "/synthetic/docker",
      endpoint: "unix:///synthetic/docker.sock",
      verifier: "/opt/foundry/verify-go-v1",
    },
  },
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
it("requires separate explicit approval and execution, displays immutable permissions, never acts on mount", async () => {
  vi.spyOn(api, "c4").mockResolvedValue(status());
  const approved = {
    ...m,
    revision: m.revision + 1,
    c4_operations: [
      {
        ...m.c4_operations![0],
        state: "approved",
        approval: { digest: "material", revision: m.revision, at: "today" },
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
    material_digest: "material",
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
        c4_operations: [
          c4Operation,
          {
            ...c4Operation,
            id: "candidate-1",
            kind: "candidate",
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
      material_digest: "material",
      container_id: "b".repeat(64),
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
        approval: { digest: "material", revision: m.revision - 2, at: "today" },
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
