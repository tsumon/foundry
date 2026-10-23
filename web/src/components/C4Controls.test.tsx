import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useState } from "react";
import { api } from "../api";
import {
  c4Mission,
  c4Operation,
  c4CandidateOperation,
} from "../test/c4Fixtures";
import type { Mission } from "../types";
import type { C4Control, C4Status } from "../c4";
import { C4Decision, C4Verification } from "./C4Verification";
import { c4BaselinePassed, c4OperationLabel } from "../c4";
import { decisionMaterial } from "../decisionMaterials";
import { MissionWorkspace } from "./MissionWorkspace";
import { RecoveryNotice } from "./RecoveryNotice";
import { hookApi } from "../hookApi";

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});
const future = () => new Date(Date.now() + 600000).toISOString();
const initial = (): Mission => ({
  ...c4Mission,
  state: "draft",
  plan: null,
  revision: 10,
  approvals: [],
  spec: {
    ...c4Mission.spec,
    c4: { ...c4Mission.spec.c4!, control_version: 1 },
  },
  c4_operations: [],
  c4_controls: [],
});
function prepared(m: Mission, changes: Partial<C4Control> = {}): C4Control {
  return {
    schema_version: 1,
    id: "readiness-1",
    kind: "readiness",
    mission_id: m.id,
    epoch: m.epoch,
    plan_revision: 0,
    base_commit: m.base_commit,
    base_digest: m.repository_import!.base_digest,
    profile_digest: "c".repeat(64),
    activation_digest: "f".repeat(64),
    activation_expires_at: future(),
    source_kind: "synthetic_c4_control",
    prepared_revision: m.revision - 1,
    prepared_at: new Date().toISOString(),
    expires_at: future(),
    material_digest: "a".repeat(64),
    state: "prepared",
    ...changes,
  };
}
function status(m: Mission): C4Status {
  return {
    mission_id: m.id,
    revision: m.revision,
    profile: m.spec.c4!,
    operations: m.c4_operations!,
    controls: m.c4_controls!,
    read_only: false,
    simulation: false,
    real_execution: false,
    control: {
      available: true,
      structurally_available: true,
      source_kind: "synthetic_c4_control",
      activation_digest: "f".repeat(64),
      expires_at: m.c4_controls?.at(-1)?.activation_expires_at ?? future(),
      expired: false,
    },
  };
}
function Harness({ start }: { start: Mission }) {
  const [m, setM] = useState(start);
  return <C4Decision mission={m} busy={false} onUpdate={setM} />;
}
it("requires three explicit readiness actions; readiness cannot approve or execute baseline", async () => {
  let m = initial();
  vi.spyOn(api, "c4").mockImplementation(async () => status(m));
  const post = vi.spyOn(api, "c4Action").mockImplementation(async (_, body) => {
    m = { ...m, revision: m.revision + 1 };
    if (body.action === "prepare_readiness") m.c4_controls = [prepared(m)];
    if (body.action === "approve_control")
      m.c4_controls = [
        {
          ...m.c4_controls![0],
          state: "approved",
          approval: {
            digest: m.c4_controls![0].material_digest,
            revision: m.revision - 1,
            at: new Date().toISOString(),
          },
        },
      ];
    if (body.action === "execute_readiness")
      m.c4_controls = [
        {
          ...m.c4_controls![0],
          state: "completed",
          dispatch: { revision: m.revision - 1, at: new Date().toISOString() },
          result: {
            status: "ready",
            revision: m.revision,
            at: new Date().toISOString(),
            valid_until: future(),
            fingerprint: "e".repeat(64),
          },
        },
      ];
    return { mission: m };
  });
  render(<Harness start={m} />);
  expect(
    screen.getByText(/本任务仅本地模拟，未执行真实 Docker/),
  ).toBeInTheDocument();
  expect(screen.getByText(/模拟就绪检查 · 非真实 Docker/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "执行就绪检查" })).toBeDisabled();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "准备就绪材料" })).toBeEnabled(),
  );
  expect(screen.getByRole("button", { name: "准备基线材料" })).toBeDisabled();
  for (const [label, n] of [
    ["准备就绪材料", 1],
    ["批准就绪材料", 2],
    ["执行就绪检查", 3],
  ] as const) {
    await waitFor(() =>
      expect(screen.getByRole("button", { name: label })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: label }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(n));
  }
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "准备基线材料" })).toBeEnabled(),
  );
  expect(post.mock.calls.map((x) => x[1].action)).toEqual([
    "prepare_readiness",
    "approve_control",
    "execute_readiness",
  ]);
  expect(screen.getByText(/就绪不是基线或候选通过/)).toBeInTheDocument();
  expect(m.approvals).toEqual([]);
});

it.each([
  "readiness_expired",
  "activation_expired",
  "mismatch",
  "readonly",
  "timer_expiry",
])(
  "keeps separate historical delivery authority accurate for %s",
  async (mode) => {
    if (mode === "timer_expiry") vi.useFakeTimers();
    const candidate = c4CandidateOperation;
    const m: Mission = {
      ...c4Mission,
      revision: 12,
      state: "awaiting_delivery",
      spec: {
        ...c4Mission.spec,
        c4: { ...c4Mission.spec.c4!, control_version: 1 },
      },
      candidate_digest: candidate.candidate_digest!,
      candidate_generation: candidate.candidate_generation!,
      approvals: [
        ...c4Mission.approvals,
        {
          kind: "execute",
          plan_revision: candidate.plan_revision,
          digest: candidate.approval!.digest,
          revision: candidate.approval!.revision,
          created_at: candidate.approval!.at,
        },
      ],
      evidence: [
        {
          id: "v",
          candidate_digest: candidate.candidate_digest!,
          image_id: c4Mission.spec.image_id,
          acceptance_revision: candidate.plan_revision,
          passed: true,
          created_at: c4Operation.prepared_at,
          results: [],
        },
      ],
      reviews: (["qa", "auditor"] as const).map((role) => ({
        role,
        passed: true,
        summary: "passed",
        findings: [],
        candidate_digest: candidate.candidate_digest!,
      })),
    };
    const expired = new Date(Date.now() - 1000).toISOString();
    const nextExpiry = new Date(Date.now() + 1000).toISOString();
    const receipt = prepared(m, {
      state: "completed",
      activation_expires_at: mode === "activation_expired" ? expired : future(),
      approval: {
        digest: "a".repeat(64),
        revision: 1,
        at: c4Operation.prepared_at,
      },
      dispatch: { revision: 2, at: c4Operation.prepared_at },
      result: {
        status: "ready",
        revision: 3,
        at: c4Operation.prepared_at,
        valid_until: expired,
        fingerprint: "e".repeat(64),
      },
    });
    m.c4_controls = [receipt];
    m.c4_operations = [c4Operation, candidate].map((o) => ({
      ...o,
      readiness_id: receipt.id,
      readiness_digest: receipt.material_digest,
    }));
    if (mode === "timer_expiry") {
      receipt.activation_expires_at = nextExpiry;
      receipt.result!.valid_until = nextExpiry;
    }
    const view = status(m);
    view.read_only = mode === "readonly";
    view.control = {
      ...view.control!,
      available: mode !== "activation_expired" && mode !== "mismatch",
      structurally_available: mode !== "mismatch",
      expired: mode === "activation_expired",
      expires_at: receipt.activation_expires_at,
    };
    vi.spyOn(api, "c4").mockResolvedValue(view);
    vi.spyOn(api, "planRevisions").mockResolvedValue({ plan_revisions: [] });
    vi.spyOn(api, "localOperations").mockResolvedValue({
      local_operations: [],
    });
    const post = vi.spyOn(api, "action").mockResolvedValue({ mission: m });
    const { rerender } = render(
      <MissionWorkspace mission={m} onUpdate={vi.fn()} />,
    );
    await act(async () => {});
    const deliver = screen.getByRole("button", { name: "批准并导出交付" });
    if (mode === "timer_expiry") {
      expect(deliver).toBeEnabled();
      rerender(
        <MissionWorkspace
          mission={{ ...m, state: "awaiting_approval" }}
          onUpdate={vi.fn()}
        />,
      );
      await act(async () => {});
      expect(
        screen.getByRole("button", { name: "批准并执行计划" }),
      ).toBeEnabled();
      await act(async () => vi.advanceTimersByTime(1001));
      expect(
        screen.getByRole("button", { name: "批准并执行计划" }),
      ).toBeDisabled();
      rerender(<MissionWorkspace mission={m} onUpdate={vi.fn()} />);
      await act(async () => {});
      expect(
        screen.getByRole("button", { name: "批准并导出交付" }),
      ).toBeEnabled();
      expect(post).not.toHaveBeenCalled();
      expect(api.c4).toHaveBeenCalledTimes(1);
      return;
    }
    if (mode === "mismatch" || mode === "readonly")
      expect(deliver).toBeDisabled();
    else {
      expect(deliver).toBeEnabled();
      expect(post).not.toHaveBeenCalled();
      fireEvent.click(deliver);
      await waitFor(() =>
        expect(post).toHaveBeenCalledExactlyOnceWith(
          m.id,
          "deliver",
          m.revision,
          undefined,
        ),
      );
    }
    rerender(
      <MissionWorkspace
        mission={{ ...m, state: "awaiting_approval" }}
        onUpdate={vi.fn()}
      />,
    );
    await act(async () => {});
    expect(
      screen.getByRole("button", { name: "批准并执行计划" }),
    ).toBeDisabled();
  },
);

it.each(["expired", "stale", "consumed", "mismatch", "readonly", "busy"])(
  "disables %s control authority",
  async (reason) => {
    const m = initial();
    let c = prepared(m, {
      state: "approved",
      approval: {
        digest: "a".repeat(64),
        revision: 9,
        at: new Date().toISOString(),
      },
    });
    if (reason === "expired")
      c = { ...c, expires_at: new Date(Date.now() - 1).toISOString() };
    if (reason === "stale")
      c = { ...c, approval: { ...c.approval!, revision: 8 } };
    if (reason === "consumed")
      c = {
        ...c,
        state: "completed",
        dispatch: { revision: 9, at: new Date().toISOString() },
      };
    if (reason === "mismatch") c = { ...c, base_digest: "0".repeat(64) };
    m.c4_controls = [c];
    vi.spyOn(api, "c4").mockResolvedValue({
      ...status(m),
      read_only: reason === "readonly",
    });
    const post = vi.spyOn(api, "c4Action");
    render(
      <C4Decision mission={m} busy={reason === "busy"} onUpdate={vi.fn()} />,
    );
    await screen.findByText(/激活到期/);
    expect(screen.getByRole("button", { name: "执行就绪检查" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "执行就绪检查" }));
    expect(post).not.toHaveBeenCalled();
  },
);

it("refreshes mission once after network failure without replaying mutation", async () => {
  const m = initial();
  m.c4_controls = [
    prepared(m, {
      state: "approved",
      approval: {
        digest: "a".repeat(64),
        revision: 9,
        at: new Date().toISOString(),
      },
    }),
  ];
  vi.spyOn(api, "c4").mockResolvedValue(status(m));
  const post = vi
    .spyOn(api, "c4Action")
    .mockRejectedValue(new Error("network lost"));
  const get = vi.spyOn(api, "mission").mockResolvedValue({ mission: m });
  render(<Harness start={m} />);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "执行就绪检查" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "执行就绪检查" }));
  await screen.findByText(/未自动重试/);
  expect(get).toHaveBeenCalledTimes(1);
  expect(post).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "执行就绪检查" })).toBeDisabled();
});

it.each([
  "image_command_failed",
  "info_protocol_failed",
  "version_trust_failed",
  "failed",
])(
  "shows persisted diagnostic %s without re-enabling execution",
  async (code) => {
    const m = initial();
    m.c4_controls = [
      prepared(m, {
        state: "completed",
        approval: {
          digest: "a".repeat(64),
          revision: 7,
          at: new Date().toISOString(),
        },
        dispatch: { revision: 8, at: new Date().toISOString() },
        result: {
          status: "failed",
          failure_code: code,
          revision: 9,
          at: new Date().toISOString(),
        },
      }),
    ];
    vi.spyOn(api, "c4").mockResolvedValue(status(m));
    const post = vi.spyOn(api, "c4Action");
    render(<C4Decision mission={m} busy={false} onUpdate={vi.fn()} />);
    await screen.findByText(new RegExp(`failed · ${code}`));
    expect(screen.getByRole("button", { name: "执行就绪检查" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "准备基线材料" })).toBeDisabled();
    expect(post).not.toHaveBeenCalled();
  },
);

it("prepares cleanup with only the durable exact ID and requires its own approval", async () => {
  const m = initial();
  m.c4_operations = [
    {
      ...c4Operation,
      result: { ...c4Operation.result!, class: "infrastructure_failed" },
      cleanup: [{ ...c4Operation.cleanup![0], state: "failed" }],
    },
  ];
  vi.spyOn(api, "c4").mockResolvedValue(status(m));
  const post = vi.spyOn(api, "c4Action").mockResolvedValue({ mission: m });
  render(<C4Decision mission={m} busy={false} onUpdate={vi.fn()} />);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: /准备清理材料/ })).toBeEnabled(),
  );
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /准备清理材料/ }));
  await waitFor(() =>
    expect(post).toHaveBeenCalledExactlyOnceWith(m.id, {
      action: "prepare_cleanup",
      revision: m.revision,
      operation_id: c4Operation.id,
      material_digest: c4Operation.material_digest,
      container_id: c4Operation.container_id,
    }),
  );
  expect(screen.getByRole("button", { name: /执行精确清理/ })).toBeDisabled();
});

it("does not invoke unrelated hook services in controlled task activity", async () => {
  const m = initial();
  vi.spyOn(api, "c4").mockResolvedValue(status(m));
  vi.spyOn(api, "planRevisions").mockResolvedValue({ plan_revisions: [] });
  vi.spyOn(api, "localOperations").mockResolvedValue({ local_operations: [] });
  const read = vi.spyOn(hookApi, "records");
  render(<MissionWorkspace mission={m} onUpdate={vi.fn()} hooksEnabled />);
  const details = screen.getByText(/⌁ 运行活动/).closest("details")!;
  details.open = true;
  fireEvent(details, new Event("toggle"));
  await act(async () => {});
  expect(read).not.toHaveBeenCalled();
});

it("explains C4 recovery restriction without claiming model snapshot corruption", async () => {
  vi.spyOn(api, "health").mockResolvedValue({
    status: "recovery_limited",
    version: "test",
    read_only: true,
    c4_controlled_only: true,
    capabilities: {},
  });
  render(<RecoveryNotice enabled />);
  expect(await screen.findByRole("alert")).toHaveTextContent("C4");
  expect(screen.getByRole("alert")).not.toHaveTextContent("任务模型快照损坏");
});

it("does not mislabel configured Docker history as synthetic or closed", () => {
  const m = initial();
  m.spec.c4 = { ...m.spec.c4!, mode: "docker" };
  const receipt = prepared(m, {
    source_kind: "docker_c4_control",
    state: "completed",
    approval: {
      digest: "a".repeat(64),
      revision: 2,
      at: c4Operation.prepared_at,
    },
    dispatch: { revision: 3, at: c4Operation.prepared_at },
    result: {
      status: "ready",
      revision: 4,
      at: c4Operation.prepared_at,
      valid_until: future(),
      fingerprint: "e".repeat(64),
    },
  });
  m.c4_controls = [receipt];
  const op = {
    ...c4Operation,
    readiness_id: receipt.id,
    readiness_digest: receipt.material_digest,
  };
  m.c4_operations = [op];
  expect(c4OperationLabel(m, op)).toBe("Docker 验证通过 · 已记录隔离执行");
  render(<C4Verification mission={m} />);
  expect(screen.queryByText(/真实 C4 入口关闭/)).not.toBeInTheDocument();
  expect(
    decisionMaterial({ ...m, c4_operations: [] }).facts.find(
      (x) => x.label === "来源",
    )?.value,
  ).toContain("受控 Docker 声明");
  m.c4_controls = [{ ...receipt, source_kind: "synthetic_c4_control" }];
  expect(c4BaselinePassed(m)).toBe(false);
});

it("does not label the model-backed Docker control path as simulated inference", async () => {
  const m = initial();
  m.spec.c4 = {
    ...m.spec.c4!,
    id: "c4_go_model_v1",
    mode: "docker",
    docker_path: "/opt/docker/bin/docker",
    endpoint: "unix:///opt/docker/run/docker.sock",
  };
  vi.spyOn(api, "c4").mockResolvedValue({
    ...status(m),
    profile: m.spec.c4,
    real_execution: true,
    control: {
      ...status(m).control!,
      source_kind: "docker_c4_control",
    },
  });

  render(<C4Decision mission={m} busy={false} onUpdate={vi.fn()} />);
  await screen.findByText(/C4 就绪检查 · 受控 Docker 配置/);
  expect(screen.queryByText(/模型推理仍为本地模拟/)).not.toBeInTheDocument();
  expect(screen.getByText(/模型调用仍需独立上下文授权/)).toBeVisible();
});

it("closes model-backed readiness and cleanup controls after committed delivery while retaining facts", async () => {
  const m = initial();
  m.state = "delivered";
  m.delivery = {
    candidate_digest: "delivered-candidate",
    created_at: "2026-10-23T00:00:00Z",
    patch_path: "/private/delivery.patch",
    report_path: "/private/report.md",
  };
  m.spec.c4 = {
    ...m.spec.c4!,
    id: "c4_go_model_v1",
    mode: "docker",
    docker_path: "/opt/docker/bin/docker",
    endpoint: "unix:///opt/docker/run/docker.sock",
  };
  const dirty = {
    ...c4Operation,
    cleanup: [
      {
        ...c4Operation.cleanup![0],
        state: "failed",
      },
    ],
  };
  const readiness = prepared(m, {
    source_kind: "docker_c4_control",
  });
  const cleanup = prepared(m, {
    id: "cleanup-1",
    kind: "cleanup",
    source_kind: "docker_c4_control",
    operation_id: dirty.id,
    operation_digest: dirty.material_digest,
    container_id: dirty.container_id,
  });
  m.c4_operations = [dirty];
  m.c4_controls = [readiness, cleanup];
  vi.spyOn(api, "c4").mockResolvedValue({
    ...status(m),
    profile: m.spec.c4,
    real_execution: true,
    control: {
      ...status(m).control!,
      source_kind: "docker_c4_control",
    },
  });
  const post = vi.spyOn(api, "c4Action");

  render(<C4Decision mission={m} busy={false} onUpdate={vi.fn()} />);

  expect(
    await screen.findByText("已交付；如需继续执行请新建任务，历史证据保持不变"),
  ).toBeVisible();
  for (const name of [
    "准备就绪材料",
    "批准就绪材料",
    "执行就绪检查",
    `准备清理材料 · ${dirty.id}`,
    "批准清理材料",
    "执行精确清理",
  ]) {
    const action = screen.getByRole("button", { name });
    expect(action).toBeDisabled();
    fireEvent.click(action);
  }
  expect(post).not.toHaveBeenCalled();
  expect(screen.getByText("清理材料 · prepared")).toBeVisible();
  expect(screen.getByRole("button", { name: "刷新 C4 事实" })).toBeEnabled();
});

it("does not apply the post-delivery terminal guard to legacy C4 fixtures", async () => {
  const m = initial();
  m.state = "delivered";
  m.delivery = {
    candidate_digest: "legacy-candidate",
    created_at: "2026-10-23T00:00:00Z",
    patch_path: "/private/legacy.patch",
    report_path: "/private/legacy.md",
  };
  m.c4_controls = [];
  vi.spyOn(api, "c4").mockResolvedValue(status(m));

  render(<C4Decision mission={m} busy={false} onUpdate={vi.fn()} />);

  await waitFor(() =>
    expect(screen.getByRole("button", { name: "准备就绪材料" })).toBeEnabled(),
  );
  expect(
    screen.queryByText("已交付；如需继续执行请新建任务，历史证据保持不变"),
  ).not.toBeInTheDocument();
});

it.each(["prepared", "approved"])(
  "prevents readiness preparation from stranding a %s baseline",
  async (state) => {
    const m = initial();
    const receipt = prepared(m, {
      state: "completed",
      approval: {
        digest: "a".repeat(64),
        revision: 3,
        at: new Date().toISOString(),
      },
      dispatch: { revision: 4, at: new Date().toISOString() },
      result: {
        status: "ready",
        revision: 5,
        at: new Date().toISOString(),
        valid_until: future(),
        fingerprint: "e".repeat(64),
      },
    });
    m.c4_controls = [receipt];
    m.c4_operations = [
      {
        ...c4Operation,
        id: "9-pending",
        state,
        approval:
          state === "approved"
            ? { ...c4Operation.approval!, revision: 9 }
            : undefined,
        dispatch: undefined,
        result: undefined,
        cleanup: [],
        readiness_id: receipt.id,
        readiness_digest: receipt.material_digest,
      },
    ];
    vi.spyOn(api, "c4").mockResolvedValue(status(m));
    const post = vi.spyOn(api, "c4Action");
    const view = render(
      <C4Decision mission={m} busy={false} onUpdate={vi.fn()} />,
    );
    const baselineLabel =
      state === "prepared" ? "批准基线材料" : "执行已批准基线（模拟）";
    await waitFor(() =>
      expect(screen.getByRole("button", { name: baselineLabel })).toBeEnabled(),
    );
    const readiness = screen.getByRole("button", { name: "准备就绪材料" });
    expect(readiness).toBeDisabled();
    fireEvent.click(readiness);
    expect(post).not.toHaveBeenCalled();
    // Stale baseline authority stays blocked; readiness cannot silently replace it.
    const stale = {
      ...m,
      c4_operations: [
        {
          ...m.c4_operations![0],
          id: "7-stale",
          approval:
            state === "approved"
              ? { ...c4Operation.approval!, revision: 7 }
              : undefined,
        },
      ],
    };
    view.rerender(
      <C4Decision mission={stale} busy={false} onUpdate={vi.fn()} />,
    );
    expect(screen.getByRole("button", { name: baselineLabel })).toBeDisabled();
    expect(screen.getByRole("button", { name: "准备就绪材料" })).toBeDisabled();
    expect(
      screen.getByText(/若授权已过期或失配，请新建任务/),
    ).toBeInTheDocument();
  },
);

it.each(["digest", "expiry"])(
  "disables live actions after current activation %s renewal",
  async (binding) => {
    const m = initial();
    const receipt = prepared(m, {
      state: "completed",
      approval: {
        digest: "a".repeat(64),
        revision: 3,
        at: new Date().toISOString(),
      },
      dispatch: { revision: 4, at: new Date().toISOString() },
      result: {
        status: "ready",
        revision: 5,
        at: new Date().toISOString(),
        valid_until: future(),
      },
    });
    m.c4_controls = [receipt];
    const s = status(m);
    if (binding === "digest") s.control!.activation_digest = "b".repeat(64);
    else s.control!.expires_at = new Date(Date.now() + 1200000).toISOString();
    vi.spyOn(api, "c4").mockResolvedValue(s);
    const post = vi.spyOn(api, "c4Action");
    const structural = vi.fn();
    const execution = vi.fn();
    const view = render(
      <C4Decision
        mission={m}
        busy={false}
        onUpdate={vi.fn()}
        onAvailabilityChange={structural}
        onExecutionAvailabilityChange={execution}
      />,
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "准备就绪材料" }),
      ).toBeEnabled(),
    );
    expect(screen.getByRole("button", { name: "准备基线材料" })).toBeDisabled();
    await waitFor(() => expect(structural).toHaveBeenLastCalledWith(true));
    expect(execution).toHaveBeenLastCalledWith(false);
    for (const kind of ["readiness", "cleanup"] as const) {
      for (const state of ["prepared", "approved"] as const) {
        const pending = {
          ...m,
          c4_operations:
            kind === "cleanup"
              ? [{ ...c4Operation, cleanup: [], state: "cleanup_failed" }]
              : [],
          c4_controls: [
            prepared(m, {
              kind,
              state,
              activation_expires_at: receipt.activation_expires_at,
              operation_id: kind === "cleanup" ? c4Operation.id : undefined,
              operation_digest:
                kind === "cleanup" ? c4Operation.material_digest : undefined,
              container_id:
                kind === "cleanup" ? c4Operation.container_id : undefined,
              approval:
                state === "approved"
                  ? {
                      digest: "a".repeat(64),
                      revision: 9,
                      at: new Date().toISOString(),
                    }
                  : undefined,
            }),
          ],
        };
        view.rerender(
          <C4Decision mission={pending} busy={false} onUpdate={vi.fn()} />,
        );
        expect(
          screen.getByRole("button", {
            name: kind === "cleanup" ? "批准清理材料" : "批准就绪材料",
          }),
        ).toBeDisabled();
        expect(
          screen.getByRole("button", {
            name: kind === "cleanup" ? "执行精确清理" : "执行就绪检查",
          }),
        ).toBeDisabled();
      }
    }
    expect(post).not.toHaveBeenCalled();
  },
);

it("expires controls locally without another GET or POST", async () => {
  vi.useFakeTimers();
  const m = initial();
  m.c4_controls = [
    prepared(m, {
      expires_at: new Date(Date.now() + 100).toISOString(),
      state: "approved",
      approval: {
        digest: "a".repeat(64),
        revision: 9,
        at: new Date().toISOString(),
      },
    }),
  ];
  const get = vi.spyOn(api, "c4").mockResolvedValue(status(m));
  const post = vi.spyOn(api, "c4Action");
  render(<C4Decision mission={m} busy={false} onUpdate={vi.fn()} />);
  await act(async () => {});
  expect(screen.getByRole("button", { name: "执行就绪检查" })).toBeEnabled();
  await act(async () => vi.advanceTimersByTime(101));
  expect(screen.getByRole("button", { name: "执行就绪检查" })).toBeDisabled();
  expect(get).toHaveBeenCalledTimes(1);
  expect(post).not.toHaveBeenCalled();
});
