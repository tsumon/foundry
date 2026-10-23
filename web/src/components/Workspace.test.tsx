import {
  render,
  screen,
  fireEvent,
  waitFor,
  within,
} from "@testing-library/react";
import { useState } from "react";
import { vi, it, expect } from "vitest";
import userEvent from "@testing-library/user-event";
import { MissionWorkspace } from "./MissionWorkspace";
import { DemoWorkspace } from "./DemoWorkspace";
import { mission, profile, response } from "../test/fixtures";
import { roles, type Mission } from "../types";

const reliabilityRead = (url: string) => {
  if (url.endsWith("/plan-revisions")) return response({ plan_revisions: [] });
  if (url.endsWith("/local-operations"))
    return response({ local_operations: [] });
  throw new Error(`unexpected request ${url}`);
};

const openPlanEditor = () =>
  fireEvent.click(
    screen.getByRole("button", { name: "修订计划（保存不审批）" }),
  );

it("opens the existing decision drawer from diagnosis without executing an action", async () => {
  const fetcher = vi.fn(async (path: string, _init?: RequestInit) =>
    path.endsWith("/recovery-diagnostic")
      ? response({
          diagnostic: {
            schema_version: 1,
            mission_id: mission.id,
            revision: mission.revision,
            epoch: mission.epoch,
            checked_at: "now",
            stale: false,
            checks: [],
            next_actions: ["replan"],
          },
        })
      : reliabilityRead(path),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <MissionWorkspace
      mission={mission}
      onUpdate={vi.fn()}
      initialView="总览"
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "核对恢复状态" }));
  fireEvent.click(
    await screen.findByRole("link", { name: /返回原决策面板核对/ }),
  );
  expect(
    screen.getByRole("button", { name: /决策与 Main Agent/ }),
  ).toHaveAttribute("aria-expanded", "true");
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
    false,
  );
});

const controlledBindings = {
  git: {
    repository_id: "repo",
    repository_revision: 1,
    observation_id: "obs",
    root: "/tmp/source",
    base_commit: "a".repeat(40),
  },
  skills: [],
  policy_version: "repo_read_v1",
  tools: ["repo.list", "repo.read", "repo.search"],
  budget: {
    max_turns: 4,
    max_calls: 3,
    max_argument_bytes: 8192,
    max_result_bytes: 65536,
    deadline_seconds: 5,
    max_total_result_bytes: 196608,
  },
};

const ordinarySchema2Mission = (): Mission => {
  const routedProfile = {
    ...profile,
    route_id: "ordinary-route",
    account_id: "ordinary-account",
    account_revision: 1,
    credential_version: 1,
  };
  const account = {
    id: "ordinary-account",
    revision: 1,
    name: "Ordinary simulation account",
    provider: "openai",
    billing_mode: "api_credits" as const,
    enabled: true,
    identity_status: "unverified" as const,
    usage_status: "unknown" as const,
    inference_enabled: false as const,
  };
  const route = {
    id: "ordinary-route",
    account_id: account.id,
    adapter: "openai_api_responses",
    protocol: "responses",
    base_url: "http://127.0.0.1:9812/v1",
    environment: "simulation" as const,
    spend_class: "explicit_api" as const,
    extra_usage: "off" as const,
    created_at: "then",
  };
  const profiles = {} as Mission["profiles"];
  const snapshotRoles = {} as NonNullable<Mission["model_snapshot"]>["roles"];
  for (const role of roles) {
    profiles[role] = { ...routedProfile };
    snapshotRoles[role] = {
      profile: { ...routedProfile },
      account: { ...account },
      route: { ...route },
    };
  }
  return {
    ...mission,
    workspace_mode: "generations_account_snapshot_v2",
    model_snapshot_status: "valid",
    profiles,
    model_snapshot: {
      schema_version: 2,
      roles: snapshotRoles,
      digest: "a".repeat(64),
    },
    model_runtime: {
      live_authorization: false,
      live_discovery: false,
      live_inference: false,
      simulation_inference: true,
      simulation_authorization: false,
      automatic_paid_fallback: false,
      adapters: [],
    },
  };
};

it("refreshes untouched drafts through first planning and chat-only mission updates", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input) => reliabilityRead(String(input))),
  );
  const { rerender } = render(
    <MissionWorkspace
      mission={{ ...mission, state: "draft", revision: 1, plan: null }}
      onUpdate={() => {}}
    />,
  );
  rerender(
    <MissionWorkspace
      mission={{ ...mission, state: "planning", revision: 2, plan: null }}
      onUpdate={() => {}}
    />,
  );
  rerender(<MissionWorkspace mission={mission} onUpdate={() => {}} />);
  openPlanEditor();
  expect(screen.getByLabelText("计划摘要")).toHaveValue("先验证，再调整");
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeEnabled();
  rerender(
    <MissionWorkspace
      mission={{ ...mission, revision: 10 }}
      onUpdate={() => {}}
    />,
  );
  expect(
    screen.queryByText(/服务端任务或计划版本已变化/),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeEnabled();
  rerender(
    <MissionWorkspace
      mission={{
        ...mission,
        revision: 11,
        plan: { ...mission.plan!, revision: 4, summary: "最新计划" },
      }}
      onUpdate={() => {}}
    />,
  );
  expect(screen.getByLabelText("计划摘要")).toHaveValue("最新计划");
});

it("labels both legacy and explicit proposal revisions as plan versions, never mission versions", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input) => reliabilityRead(String(input))),
  );
  render(
    <MissionWorkspace
      mission={{
        ...mission,
        revision: 10,
        plan: { ...mission.plan!, revision: 7 },
        approvals: [
          {
            kind: "plan_proposal",
            revision: 6,
            digest: "legacy",
            created_at: "",
          },
          {
            kind: "plan_proposal",
            revision: 7,
            plan_revision: 7,
            digest: "new",
            created_at: "",
          },
          { kind: "execute", revision: 9, digest: "execute", created_at: "" },
        ],
      }}
      onUpdate={() => {}}
    />,
  );
  fireEvent.click(screen.getByRole("tab", { name: "审批" }));
  const rows = screen.getAllByRole("article");
  expect(rows[0]).toHaveTextContent("计划 v6");
  expect(rows[1]).toHaveTextContent("计划 v7");
  expect(rows[0]).not.toHaveTextContent("任务 r6");
  expect(rows[1]).not.toHaveTextContent("任务 r7");
  expect(rows[2]).toHaveTextContent("任务 r9");
  expect(rows[2]).toHaveTextContent("计划版本未记录（旧记录）");
});

it("does not stale an untouched plan when a chat snapshot arrives before its request settles", async () => {
  let finish!: (value: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: string) =>
      input.endsWith("/chat")
        ? new Promise<Response>((resolve) => {
            finish = resolve;
          })
        : Promise.resolve(reliabilityRead(input)),
    ),
  );
  const { rerender } = render(
    <MissionWorkspace mission={mission} onUpdate={() => {}} />,
  );
  fireEvent.change(screen.getByLabelText("向 Main 提问"), {
    target: { value: "解释计划" },
  });
  fireEvent.click(screen.getByRole("button", { name: "发送消息" }));
  const chatted = { ...mission, revision: 10 };
  rerender(<MissionWorkspace mission={chatted} onUpdate={() => {}} />);
  finish(response({ mission: chatted }));
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "批准并执行计划" }),
    ).toBeEnabled(),
  );
  expect(
    screen.queryByText(/服务端任务或计划版本已变化/),
  ).not.toBeInTheDocument();
});

it("uses the newest authoritative plan when a successful save response arrives after a newer snapshot", async () => {
  let finish!: (value: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: string, init?: RequestInit) =>
      init?.method === "POST"
        ? new Promise<Response>((resolve) => {
            finish = resolve;
          })
        : Promise.resolve(reliabilityRead(input)),
    ),
  );
  const { rerender } = render(
    <MissionWorkspace mission={mission} onUpdate={() => {}} />,
  );
  openPlanEditor();
  fireEvent.change(screen.getByLabelText("计划摘要"), {
    target: { value: "本次保存" },
  });
  fireEvent.change(screen.getByLabelText("修订原因"), {
    target: { value: "明确步骤" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存修订（不审批）" }));
  const latest = {
    ...mission,
    revision: 11,
    plan: { ...mission.plan!, revision: 5, summary: "更新的权威计划" },
  };
  rerender(<MissionWorkspace mission={latest} onUpdate={() => {}} />);
  const saved = {
    ...mission,
    revision: 10,
    plan: { ...mission.plan!, revision: 4, summary: "本次保存" },
  };
  finish(
    response({
      mission: saved,
      plan_revision: { epoch: 1, plan_revision: 4, plan: saved.plan },
    }),
  );
  await waitFor(() =>
    expect(screen.getByLabelText("计划摘要")).toHaveValue("更新的权威计划"),
  );
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeEnabled();
});

it("keeps demonstration separate from execution", () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  render(<DemoWorkspace onClose={() => {}} />);
  expect(screen.getByText(/演示数据/)).toBeVisible();
  expect(
    screen.queryByRole("button", { name: /批准并执行/ }),
  ).not.toBeInTheDocument();
  expect(fetcher).not.toHaveBeenCalled();
});

it("lazily reads exact-mission observation records only after running activity expands", async () => {
  const sourceEvent = {
    seq: 20,
    mission_id: mission.id,
    kind: "plan.ready",
    revision: mission.revision,
    created_at: "2026-10-18T01:00:00Z",
  };
  const fetcher = vi.fn(async (path: string) => {
    if (path.endsWith("/plan-revisions"))
      return response({ plan_revisions: [] });
    if (path.endsWith("/local-operations"))
      return response({ local_operations: [] });
    if (path === "/api/hooks/records?mission_id=m1&limit=50")
      return response({
        records: [
          {
            id: 3,
            event_seq: 20,
            mission_id: "m1",
            mission_revision: 9,
            event_kind: "plan.ready",
            event_created_at: "2026-10-18T01:00:00Z",
            rule_id: "a".repeat(32),
            rule_revision: 2,
            rule_name: "任务活动观察",
            action: "record_activity",
            handler_version: "record_activity/v1",
            status: "recorded",
            processed_at: "2026-10-18T01:00:01Z",
          },
        ],
        next_before: 0,
        processor: {
          initialized_after_seq: 1,
          cursor: 20,
          latest_event_seq: 20,
          pending_events: 0,
          last_processed_at: "2026-10-18T01:00:01Z",
        },
        runtime: {
          state: "running",
          last_attempt_at: "2026-10-18T01:00:01Z",
          last_success_at: "2026-10-18T01:00:01Z",
          error_code: "",
        },
      });
    throw new Error(`unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <MissionWorkspace
      mission={mission}
      onUpdate={() => {}}
      events={[sourceEvent]}
      hooksEnabled
    />,
  );
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([path]) =>
        String(path).includes("/api/hooks/records"),
      ),
    ).toHaveLength(0),
  );
  fireEvent.click(screen.getByText("⌁ 运行活动"));
  expect(
    await screen.findByRole("heading", { name: "观察处理记录" }),
  ).toBeVisible();
  expect(await screen.findByText("任务活动观察")).toBeVisible();
  expect(screen.getAllByText("plan.ready")).toHaveLength(2);
  expect(
    fetcher.mock.calls.filter(
      ([path]) => path === "/api/hooks/records?mission_id=m1&limit=50",
    ),
  ).toHaveLength(1);
});
it("changes seven views without changing lifecycle and shows research citations and unknowns", () => {
  render(<MissionWorkspace mission={mission} onUpdate={() => {}} />);
  expect(screen.getAllByRole("tab")).toHaveLength(7);
  fireEvent.click(screen.getByRole("tab", { name: "计划链路" }));
  expect(screen.getByText("尚未确认编码边界")).toBeVisible();
  expect(screen.getAllByText("src/import.ts").length).toBeGreaterThan(0);
  const stages = screen.getByRole("list", { name: "任务阶段" });
  expect(within(stages).getByText("计划").closest("li")).toHaveAttribute(
    "aria-current",
    "step",
  );
  fireEvent.click(screen.getByRole("tab", { name: "验证" }));
  expect(screen.getByText("尚无验证记录")).toBeVisible();
  expect(within(stages).getByText("计划").closest("li")).toHaveAttribute(
    "aria-current",
    "step",
  );
});
it("keeps approval notes honest for simulation and unknown verification sources", () => {
  const { rerender } = render(
    <MissionWorkspace mission={mission} onUpdate={() => {}} />,
  );
  expect(screen.queryByText(/验证仅在受限/)).not.toBeInTheDocument();
  expect(screen.getByText(/验证来源以控制器记录为准/)).toBeVisible();
  rerender(
    <MissionWorkspace
      mission={{
        ...mission,
        state: "awaiting_delivery",
        evidence: [
          {
            id: "simulation",
            candidate_digest: mission.candidate_digest,
            image_id: "image",
            acceptance_revision: 3,
            created_at: "today",
            passed: true,
            results: [],
            source_version: 1,
            source_kind: "synthetic_stub",
          },
        ],
      }}
      onUpdate={() => {}}
    />,
  );
  expect(
    screen.getByText(/模拟验证通过 · 流程模拟（未执行命令）/),
  ).toBeVisible();
});
it("approves mission revision, suppresses duplicate submits and surfaces conflict without retry", async () => {
  let resolve!: (v: Response) => void;
  const fetcher = vi.fn((input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith("/approve") && init?.method === "POST")
      return new Promise<Response>((r) => {
        resolve = r;
      });
    if (url === "/api/missions/m1")
      return Promise.resolve(
        response({ mission: { ...mission, revision: 10, state: "running" } }),
      );
    return Promise.resolve(reliabilityRead(url));
  });
  vi.stubGlobal("fetch", fetcher);
  const update = vi.fn();
  render(<MissionWorkspace mission={mission} onUpdate={update} />);
  const approve = screen.getByRole("button", { name: "批准并执行计划" });
  fireEvent.click(approve);
  fireEvent.click(approve);
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).endsWith("/approve")),
  ).toHaveLength(1);
  const approvalCall = fetcher.mock.calls.find(([url]) =>
    String(url).endsWith("/approve"),
  );
  expect(JSON.parse(String(approvalCall?.[1]?.body))).toEqual({ revision: 9 });
  resolve(
    response(
      { error: { code: "conflict", message: "revision conflict" } },
      409,
    ),
  );
  await waitFor(() =>
    expect(update).toHaveBeenCalledWith(
      expect.objectContaining({ revision: 10 }),
    ),
  );
  expect(screen.getByRole("alert")).toHaveTextContent(/版本已变化/);
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).endsWith("/approve")),
  ).toHaveLength(1);
});
it("requires frozen destinations and source disclosure before planning and explains archival replan", () => {
  render(
    <MissionWorkspace
      mission={{ ...mission, state: "blocked" }}
      onUpdate={() => {}}
    />,
  );
  expect(screen.getByText(/重新规划将归档/)).toBeVisible();
  expect(screen.getByText(/文件名过滤不等于秘密扫描/)).toBeVisible();
  expect(screen.getAllByText(profileURL).length).toBe(7);
  const action = screen.getByRole("button", { name: "授权上下文并生成计划" });
  expect(action).toBeDisabled();
  fireEvent.click(screen.getByLabelText(/我已检查/));
  expect(action).toBeEnabled();
});

it("requires two independent unchecked consents and sends controlled consent only for the exact controlled pair", async () => {
  const controlledMission = {
    ...mission,
    state: "draft",
    plan: null,
    tool_mode: "controlled_read_v1" as const,
    workspace_mode: "generations_controlled_read_v1" as const,
    task_bindings: controlledBindings,
  };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith("/read-grants")) return response({ read_grants: [] });
    if (path.endsWith("/plan-revisions"))
      return response({ plan_revisions: [] });
    if (path.endsWith("/local-operations"))
      return response({ local_operations: [] });
    if (path.endsWith("/plan") && init?.method === "POST")
      return response({
        mission: { ...controlledMission, state: "planning", revision: 10 },
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const { rerender } = render(
    <MissionWorkspace mission={controlledMission} onUpdate={() => {}} />,
  );
  const action = screen.getByRole("button", {
    name: "授权上下文与本地读取并生成计划",
  });
  const context = screen.getByLabelText(/我已检查目标/);
  const local = screen.getByLabelText(/我授权 Researcher 在本次规划中/);
  expect(context).not.toBeChecked();
  expect(local).not.toBeChecked();
  expect(action).toBeDisabled();
  fireEvent.click(context);
  expect(action).toBeDisabled();
  fireEvent.click(local);
  expect(action).toBeEnabled();
  fireEvent.click(action);
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(([path]) => String(path).endsWith("/plan")),
    ).toBe(true),
  );
  const planCall = fetcher.mock.calls.find(([path]) =>
    String(path).endsWith("/plan"),
  )!;
  expect(JSON.parse(String(planCall[1]?.body))).toEqual({
    revision: 9,
    controlled_consent: true,
  });
  rerender(
    <MissionWorkspace
      mission={{ ...controlledMission, revision: 10 }}
      onUpdate={() => {}}
    />,
  );
  expect(screen.getByLabelText(/我已检查目标/)).not.toBeChecked();
  expect(
    screen.getByLabelText(/我授权 Researcher 在本次规划中/),
  ).not.toBeChecked();
});

it("wires same-revision invocation events into the controlled activity refresh", async () => {
  const controlledMission = {
    ...mission,
    tool_mode: "controlled_read_v1" as const,
    workspace_mode: "generations_controlled_read_v1" as const,
    task_bindings: controlledBindings,
  };
  let invocationReads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/read-grants")) return response({ read_grants: [] });
      if (path.endsWith("/plan-revisions"))
        return response({ plan_revisions: [] });
      if (path.endsWith("/local-operations"))
        return response({ local_operations: [] });
      if (path.includes("/invocations?")) {
        invocationReads++;
        return response({ invocations: [] });
      }
      throw new Error(`unexpected ${path}`);
    }),
  );
  const firstEvent = {
    seq: 40,
    mission_id: controlledMission.id,
    kind: "invocation.authorized",
    created_at: "2026-10-18T00:00:00Z",
    revision: controlledMission.revision,
  };
  const { rerender } = render(
    <MissionWorkspace
      mission={controlledMission}
      onUpdate={() => {}}
      events={[firstEvent]}
    />,
  );
  fireEvent.click(screen.getByText("⌁ 运行活动"));
  await waitFor(() => expect(invocationReads).toBe(1));
  rerender(
    <MissionWorkspace
      mission={controlledMission}
      onUpdate={() => {}}
      events={[
        firstEvent,
        { ...firstEvent, seq: 41, kind: "invocation.dispatching" },
      ]}
    />,
  );
  await waitFor(() => expect(invocationReads).toBe(2));
});

it("shows invocation receipts for ordinary schema2 missions without enabling them for legacy ordinary missions", async () => {
  const routed = ordinarySchema2Mission();
  const invocation = {
    id: "ordinary-invocation",
    mission_id: routed.id,
    epoch: 1,
    session_id: "ordinary-session",
    ordinal: 1,
    kind: "model",
    role: "worker",
    name: "model.complete",
    binding_digest: "b".repeat(64),
    authority_digest: "c".repeat(64),
    input: {
      schema_version: 1,
      kind: "model_input",
      invocation_id: "ordinary-invocation",
      mission_id: routed.id,
      epoch: 1,
      producer: "worker",
      captured_at: "now",
      digest: "d".repeat(64),
      bytes: 20,
      content_type: "application/json",
      binding_digest: "b".repeat(64),
      truncated: false,
    },
    status: "failed",
    error_code: "quota_exhausted",
    reserved_result_bytes: 0,
    created_at: "now",
    finished_at: "later",
    usage_known: false,
    input_tokens: 0,
    output_tokens: 0,
    model_receipt: {
      request_id: "ordinary-request",
      certainty: "rejected",
    },
  };
  const fetcher = vi.fn(async (path: string) => {
    if (path.endsWith("/plan-revisions"))
      return response({ plan_revisions: [] });
    if (path.endsWith("/local-operations"))
      return response({ local_operations: [] });
    if (path.includes("/invocations?"))
      return response({ invocations: [invocation] });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);

  const view = render(
    <MissionWorkspace mission={routed} onUpdate={() => {}} />,
  );
  fireEvent.click(screen.getByText("⌁ 运行活动"));
  expect(await screen.findByText("当前路由额度受限")).toBeVisible();
  expect(screen.getByText("ordinary-request")).toBeVisible();
  expect(
    fetcher.mock.calls.filter(([path]) =>
      String(path).includes("/invocations?"),
    ),
  ).toHaveLength(1);

  view.unmount();
  render(<MissionWorkspace mission={mission} onUpdate={() => {}} />);
  fireEvent.click(screen.getByText("⌁ 运行活动"));
  expect(
    screen.queryByRole("region", { name: "模型与受控调用记录" }),
  ).not.toBeInTheDocument();
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([path]) =>
        String(path).endsWith("/plan-revisions"),
      ),
    ).toHaveLength(2),
  );
  expect(
    fetcher.mock.calls.filter(([path]) =>
      String(path).includes("/invocations?"),
    ),
  ).toHaveLength(1);
});

it("wires same-revision MCP and research events into task MCP history refresh", async () => {
  const controlledMission = {
    ...mission,
    state: "planning",
    stage: "mcp_call",
    plan: null,
    tool_mode: "controlled_mcp_read_v2" as const,
    workspace_mode: "generations_controlled_mcp_read_v2" as const,
    task_bindings: controlledBindings,
    research_status: {
      current: true,
      can_decide: false,
      can_continue: false,
    },
  };
  let historyReads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/read-grants")) return response({ read_grants: [] });
      if (path.endsWith("/plan-revisions"))
        return response({ plan_revisions: [] });
      if (path.endsWith("/local-operations"))
        return response({ local_operations: [] });
      if (path.includes("/invocations?")) return response({ invocations: [] });
      if (path.endsWith("/mcp-invocations")) {
        historyReads++;
        return response({ invocations: [], decisions: [] });
      }
      throw new Error(`unexpected ${path}`);
    }),
  );
  const firstEvent = {
    seq: 40,
    mission_id: controlledMission.id,
    kind: "mcp.call_started",
    created_at: "2026-10-18T00:00:00Z",
    revision: controlledMission.revision,
  };
  const { rerender } = render(
    <MissionWorkspace
      mission={controlledMission}
      onUpdate={() => {}}
      events={[firstEvent]}
    />,
  );
  fireEvent.click(screen.getByText("⌁ 运行活动"));
  await waitFor(() => expect(historyReads).toBe(1));
  rerender(
    <MissionWorkspace
      mission={controlledMission}
      onUpdate={() => {}}
      events={[
        firstEvent,
        { ...firstEvent, seq: 41, kind: "research.turn_applied" },
      ]}
    />,
  );
  await waitFor(() => expect(historyReads).toBe(2));
});

it("shows safe v2 recipient connection identities without claiming raw endpoints", () => {
  const safeProfiles = Object.fromEntries(
    Object.keys(mission.profiles).map((role) => [
      role,
      {
        id: `safe-${role}`,
        name: `安全 ${role}`,
        protocol: "responses",
        model: "fixture-model",
      },
    ]),
  ) as typeof mission.profiles;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/read-grants")) return response({ read_grants: [] });
      if (path.endsWith("/plan-revisions"))
        return response({ plan_revisions: [] });
      if (path.endsWith("/local-operations"))
        return response({ local_operations: [] });
      throw new Error(`unexpected ${path}`);
    }),
  );

  render(
    <MissionWorkspace
      mission={{
        ...mission,
        state: "draft",
        plan: null,
        tool_mode: "controlled_mcp_read_v2",
        workspace_mode: "generations_controlled_mcp_read_v2",
        task_bindings: controlledBindings,
        profiles: safeProfiles,
      }}
      onUpdate={() => {}}
    />,
  );

  expect(screen.getByText("安全 researcher · safe-researcher")).toBeVisible();
  expect(screen.getAllByText("原始端点未在安全任务视图提供")).toHaveLength(7);
  expect(
    screen.getByLabelText(
      /我已检查目标、命令、文件清单及接收连接标识；安全任务视图不显示原始端点/,
    ),
  ).not.toBeChecked();
  expect(screen.queryByText(profileURL)).not.toBeInTheDocument();
});

it("opens the live MCP decision from the persistent decision panel in the approval view", async () => {
  const budget = {
    max_calls: 3,
    max_argument_bytes: 8192,
    max_result_bytes: 65536,
    max_total_result_bytes: 196608,
    deadline_seconds: 30,
    max_requests: 14,
    max_pages: 10,
    max_tools: 500,
    max_response_bytes: 1048576,
    max_total_response_bytes: 4194304,
    max_event_bytes: 1048576,
    max_notifications: 64,
    request_timeout_seconds: 5,
    cleanup_timeout_seconds: 1,
  };
  const controlledMission = {
    ...mission,
    state: "planning",
    stage: "awaiting_tool_approval",
    revision: 8,
    plan: null,
    tool_mode: "controlled_mcp_read_v2" as const,
    workspace_mode: "generations_controlled_mcp_read_v2" as const,
    task_bindings: controlledBindings,
    mcp_selection: {
      connection_id: "local-test",
      connection_revision: 4,
      endpoint: "http://127.0.0.1:43123/mcp",
      protocol: "streamable_http",
      snapshot_id: "snapshot-4",
      snapshot_digest: "snapshot-digest",
      tools: [
        {
          name: "reference.lookup",
          definition_digest: "definition-digest",
          input_schema_digest: "input-digest",
          policy: { type: "object" },
        },
      ],
      budget,
      test_only: true,
    },
    research_status: {
      current: true,
      can_decide: true,
      can_continue: false,
    },
  };
  const pendingDecision = {
    current: true,
    can_decide: true,
    can_continue: false,
    proposal_id: "proposal-1",
    proposal_digest: "proposal-digest-1",
    revision: 8,
    purpose: "核对任务内工具审批流程",
    endpoint: "http://127.0.0.1:43123/mcp",
    tool: "reference.lookup",
    arguments: { topic: "mcp-workflow" },
    policy: { type: "object" },
    source_invocation_id: "source-1",
    source_digest: "source-digest-1",
    test_only: true,
    used_calls: 1,
    remaining_calls: 2,
    budget,
    recipients: [],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/read-grants")) return response({ read_grants: [] });
      if (path.endsWith("/plan-revisions"))
        return response({ plan_revisions: [] });
      if (path.endsWith("/local-operations"))
        return response({ local_operations: [] });
      if (path.endsWith("/mcp-decision"))
        return response({ decision: pendingDecision });
      throw new Error(`unexpected ${path}`);
    }),
  );

  render(<MissionWorkspace mission={controlledMission} onUpdate={() => {}} />);

  expect(screen.getByRole("tab", { name: "计划链路" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  expect(
    screen.queryByRole("region", { name: "本次 MCP 调用审批" }),
  ).not.toBeInTheDocument();
  expect(screen.getByText(/仓库读取与 MCP 合计最多 3 次调用/)).toBeVisible();
  expect(screen.queryByText(/3 次仓库调用/)).not.toBeInTheDocument();
  expect(screen.queryByText("共享 MCP 预算")).not.toBeInTheDocument();

  fireEvent.click(
    screen.getByRole("button", { name: "转到当前 MCP 调用审批" }),
  );

  expect(screen.getByRole("tab", { name: "审批" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  expect(
    await screen.findByRole("region", { name: "本次 MCP 调用审批" }),
  ).toBeVisible();
  expect(await screen.findByText("核对任务内工具审批流程")).toBeVisible();
  expect(
    screen.getAllByRole("region", { name: "本次 MCP 调用审批" }),
  ).toHaveLength(1);
});

it("keeps an unresolved MCP decision locked when leaving and re-entering overview", async () => {
  const controlledMission = {
    ...mission,
    state: "planning",
    stage: "awaiting_tool_approval",
    revision: 8,
    plan: null,
    tool_mode: "controlled_mcp_read_v2" as const,
    workspace_mode: "generations_controlled_mcp_read_v2" as const,
    task_bindings: controlledBindings,
    research_status: {
      current: true,
      can_decide: true,
      can_continue: false,
    },
  };
  const pendingDecision = {
    current: true,
    can_decide: true,
    can_continue: false,
    proposal_id: "proposal-1",
    proposal_digest: "proposal-digest-1",
    revision: 8,
    purpose: "核对任务内工具审批流程",
    endpoint: "http://127.0.0.1:43123/mcp",
    tool: "reference.lookup",
    arguments: { topic: "mcp-workflow" },
    policy: { type: "object" },
    source_invocation_id: "source-1",
    source_digest: "source-digest-1",
    test_only: true,
    used_calls: 1,
    remaining_calls: 2,
    budget: {
      max_calls: 3,
      max_argument_bytes: 8192,
      max_result_bytes: 65536,
      max_total_result_bytes: 196608,
      deadline_seconds: 30,
      max_requests: 14,
      max_pages: 10,
      max_tools: 500,
      max_response_bytes: 1048576,
      max_total_response_bytes: 4194304,
      max_event_bytes: 1048576,
      max_notifications: 64,
      request_timeout_seconds: 5,
      cleanup_timeout_seconds: 1,
    },
    recipients: [],
  };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith("/read-grants")) return response({ read_grants: [] });
    if (path.endsWith("/plan-revisions"))
      return response({ plan_revisions: [] });
    if (path.endsWith("/local-operations"))
      return response({ local_operations: [] });
    if (path.endsWith("/mcp-decision"))
      return response({ decision: pendingDecision });
    if (path.includes("/mcp-proposals/") && init?.method === "POST")
      throw new TypeError("connection lost");
    if (path.includes("/mcp-decisions/"))
      throw new TypeError("receipt unavailable");
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <MissionWorkspace
      mission={controlledMission}
      onUpdate={() => {}}
      initialView="总览"
    />,
  );
  await screen.findByText("核对任务内工具审批流程");
  fireEvent.click(screen.getByRole("button", { name: "拒绝本次调用" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/响应未确认/);
  fireEvent.click(screen.getByRole("tab", { name: "计划链路" }));
  fireEvent.click(screen.getByRole("tab", { name: "总览" }));
  expect(
    await screen.findByRole("button", { name: "仅查询原请求结果" }),
  ).toBeVisible();
  expect(screen.getByRole("button", { name: "拒绝本次调用" })).toBeDisabled();
  expect(
    fetcher.mock.calls.filter(([, request]) => request?.method === "POST"),
  ).toHaveLength(1);
});
const profileURL = "https://models.example/v1";
it("distinguishes archived verification from current delivery evidence", () => {
  const previous = {
    epoch: 2,
    plan_revision: 9,
    revision: 15,
    archived_at: "2026-10-18",
    state: "blocked",
    error: "prior failed run",
    image_id: "old-image",
    candidate_digest: "old-digest",
    plan: mission.plan,
    research: null,
    changes: [],
    delivery: null,
    evidence: [
      {
        id: "old",
        candidate_digest: "old-digest",
        image_id: "old-image",
        created_at: "2026-10-18",
        acceptance_revision: 9,
        results: [
          {
            command: "sh scripts/check.sh",
            output: "historical failure output",
            exit_code: 1,
            timed_out: false,
          },
        ],
        passed: false,
      },
    ],
    reviews: [
      {
        role: "qa" as const,
        passed: false,
        summary: "historical explanation",
        findings: [],
        candidate_digest: "old-digest",
      },
    ],
  };
  render(
    <MissionWorkspace
      mission={{ ...mission, run_archives: [previous] }}
      onUpdate={() => {}}
    />,
  );
  fireEvent.click(screen.getByRole("tab", { name: "验证" }));
  expect(screen.getByText(/历史归档 · 不用于当前交付审批/)).toBeVisible();
  expect(screen.getByText(/运行 2 · 计划 r9/)).toBeVisible();
  expect(screen.getByText("historical failure output")).toBeInTheDocument();
  expect(screen.getByText("historical explanation")).toBeInTheDocument();
  expect(screen.getByText("尚无验证记录")).toBeVisible();
});
it("keeps command attempts and model reviews distinct, handles repeated change paths and only offers actual delivery", () => {
  const evidence = {
    id: "e1",
    candidate_digest: "digest",
    image_id: "image",
    created_at: "2026-10-18",
    acceptance_revision: 9,
    results: [
      {
        command: "npm test",
        output: "failed assertion",
        exit_code: 1,
        timed_out: false,
      },
    ],
    passed: false,
  };
  render(
    <MissionWorkspace
      mission={{
        ...mission,
        evidence: [
          evidence,
          {
            ...evidence,
            id: "e2",
            passed: true,
            results: [{ ...evidence.results[0], exit_code: 0, output: "ok" }],
          },
        ],
        reviews: [
          {
            role: "qa",
            passed: true,
            summary: "模型判断",
            findings: [],
            candidate_digest: "digest",
          },
        ],
        changes: [
          { path: "a.ts", before: "a", after: "b" },
          { path: "a.ts", before: "b", after: "c" },
        ],
      }}
      onUpdate={() => {}}
    />,
  );
  fireEvent.click(screen.getByRole("tab", { name: "验证" }));
  expect(screen.getByText("failed assertion")).toBeVisible();
  expect(screen.getByText("ok")).toBeVisible();
  expect(screen.getByText(/模型审查 · 非命令执行证据/)).toBeVisible();
  fireEvent.click(screen.getByRole("tab", { name: "代码变更" }));
  expect(screen.getAllByText("a.ts")).toHaveLength(2);
  fireEvent.click(screen.getByRole("tab", { name: "交付报告" }));
  expect(screen.queryByRole("link", { name: /下载/ })).not.toBeInTheDocument();
});
it("sends bounded UTF-8 Main messages without treating conversation as approval", async () => {
  const fetcher = vi.fn(async (input) => reliabilityRead(String(input)));
  vi.stubGlobal("fetch", fetcher);
  render(<MissionWorkspace mission={mission} onUpdate={() => {}} />);
  fireEvent.change(screen.getByLabelText("向 Main 提问"), {
    target: { value: "汉".repeat(2700) },
  });
  fireEvent.click(screen.getByRole("button", { name: "发送消息" }));
  expect(
    fetcher.mock.calls.some(([url]) => String(url).endsWith("/chat")),
  ).toBe(false);
  expect(screen.getByRole("alert")).toHaveTextContent(/8000/);
});
it("sends Main questions with the current mission revision and clears only a successful submission", async () => {
  const fetcher = vi.fn(
    async (input: string | URL | Request, _init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/chat"))
        return response({ mission: { ...mission, revision: 10 } });
      return reliabilityRead(url);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  render(<MissionWorkspace mission={mission} onUpdate={() => {}} />);
  const input = screen.getByLabelText("向 Main 提问");
  fireEvent.change(input, { target: { value: "可以，请解释计划" } });
  fireEvent.click(screen.getByRole("button", { name: "发送消息" }));
  await waitFor(() => expect(input).toHaveValue(""));
  const chatCall = fetcher.mock.calls.find(([url]) =>
    String(url).endsWith("/chat"),
  );
  expect(chatCall?.[0]).toBe("/api/missions/m1/chat");
  expect(JSON.parse(String(chatCall?.[1]?.body))).toEqual({
    revision: 9,
    message: "可以，请解释计划",
  });
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).endsWith("/chat")),
  ).toHaveLength(1);
});
it("waits for backend pause confirmation and labels internal seals separately from user approvals", async () => {
  const fetcher = vi.fn(async (input: string | URL | Request) => {
    const url = String(input);
    if (url.endsWith("/pause"))
      return response({ mission: { ...mission, state: "pausing" } });
    return reliabilityRead(url);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <MissionWorkspace
      mission={{
        ...mission,
        state: "running",
        approvals: [
          { kind: "snapshot", revision: 1, created_at: "", digest: "seal" },
          { kind: "execute", revision: 9, created_at: "", digest: "user" },
        ],
      }}
      onUpdate={() => {}}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "请求暂停" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([url]) => String(url).endsWith("/pause")),
    ).toHaveLength(1),
  );
  expect(screen.queryByText("已暂停")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("tab", { name: "审批" }));
  expect(screen.getByText("内部封存 · 仓库快照")).toBeVisible();
  expect(screen.getByText("用户审批 · 执行计划")).toBeVisible();
});
it("only links controller download routes once the mission is actually delivered", () => {
  render(
    <MissionWorkspace
      mission={{
        ...mission,
        state: "delivered",
        delivery: {
          candidate_digest: "sha",
          created_at: "2026-10-18",
          patch_path: "/private/no-browser/changes.patch",
          report_path: "/private/no-browser/report.md",
        },
      }}
      onUpdate={() => {}}
    />,
  );
  fireEvent.click(screen.getByRole("tab", { name: "交付报告" }));
  expect(screen.getByRole("link", { name: "下载 patch" })).toHaveAttribute(
    "href",
    "/api/missions/m1/patch",
  );
  expect(
    screen.getByRole("link", { name: "下载 Markdown 报告" }),
  ).toHaveAttribute("href", "/api/missions/m1/report");
  expect(document.body.textContent).not.toContain("/private/no-browser");
});
it("closes model-backed Main chat after delivery while retaining report downloads", async () => {
  const delivered = {
    ...mission,
    state: "delivered",
    spec: {
      ...mission.spec,
      c4: {
        schema_version: 1,
        control_version: 1,
        id: "c4_go_model_v1",
        mode: "docker",
        image_id: `sha256:${"a".repeat(64)}`,
        platform: "linux/arm64",
        docker_path: "/opt/docker/bin/docker",
        endpoint: "unix:///opt/docker/run/docker.sock",
        verifier: "/opt/foundry/verify-go-v1",
      },
    },
    c4_operations: [],
    c4_controls: [],
    delivery: {
      candidate_digest: "sha",
      created_at: "2026-10-23",
      patch_path: "/private/no-browser/model.patch",
      report_path: "/private/no-browser/model.md",
    },
  } satisfies Mission;
  const fetcher = vi.fn(
    async (input: string | URL | Request, _init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/c4"))
        return response({
          mission_id: delivered.id,
          revision: delivered.revision,
          profile: delivered.spec.c4,
          operations: [],
          controls: [],
          read_only: false,
          simulation: false,
          real_execution: true,
          control: {
            available: true,
            structurally_available: true,
            source_kind: "docker_c4_control",
            expires_at: "2026-10-23T01:00:00Z",
            expired: false,
          },
        });
      return reliabilityRead(url);
    },
  );
  vi.stubGlobal("fetch", fetcher);

  render(<MissionWorkspace mission={delivered} onUpdate={vi.fn()} />);

  expect(screen.getByLabelText("向 Main 提问")).toBeDisabled();
  expect(
    screen.getAllByText("已交付；如需继续执行请新建任务，历史证据保持不变")
      .length,
  ).toBeGreaterThan(0);
  fireEvent.click(screen.getByRole("tab", { name: "交付报告" }));
  expect(screen.getByRole("link", { name: "下载 patch" })).toHaveAttribute(
    "href",
    "/api/missions/m1/patch",
  );
  expect(
    screen.getByRole("link", { name: "下载 Markdown 报告" }),
  ).toHaveAttribute("href", "/api/missions/m1/report");
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
    false,
  );
});
it("uses arrow keys to switch views without moving the lifecycle stage", () => {
  render(<MissionWorkspace mission={mission} onUpdate={() => {}} />);
  const tab = screen.getByRole("tab", { name: "计划链路" });
  tab.focus();
  fireEvent.keyDown(tab, { key: "ArrowRight" });
  expect(screen.getByRole("tab", { name: "代码变更" })).toHaveFocus();
  expect(screen.getByRole("tab", { name: "代码变更" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
});
it("opens the narrow nonmodal decision region from the keyboard and returns focus on Escape and close", async () => {
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({ matches: true })),
  );
  const user = userEvent.setup();
  render(<MissionWorkspace mission={mission} onUpdate={() => {}} />);
  const trigger = screen.getByRole("button", { name: /决策与 Main Agent/ });
  trigger.focus();
  await user.keyboard("{Enter}");
  const region = screen.getByRole("region", { name: "决策与 Main Agent" });
  expect(region).toHaveFocus();
  expect(region).not.toHaveAttribute("aria-modal");
  expect(trigger).toHaveAttribute("aria-expanded", "true");
  await user.keyboard("{Escape}");
  expect(trigger).toHaveFocus();
  expect(trigger).toHaveAttribute("aria-expanded", "false");
  await user.keyboard("{Enter}");
  expect(region).toHaveFocus();
  await user.click(trigger);
  expect(trigger).toHaveFocus();
  expect(trigger).toHaveAttribute("aria-expanded", "false");
});
it("does not move desktop focus on render or mission updates", () => {
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({ matches: false })),
  );
  const { rerender } = render(
    <MissionWorkspace mission={mission} onUpdate={() => {}} />,
  );
  const tab = screen.getByRole("tab", { name: "计划链路" });
  tab.focus();
  rerender(
    <MissionWorkspace
      mission={{ ...mission, revision: 10 }}
      onUpdate={() => {}}
    />,
  );
  expect(tab).toHaveFocus();
});
it("opens and focuses the decision region on narrow queue navigation", () => {
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({ matches: true })),
  );
  render(
    <MissionWorkspace
      mission={mission}
      onUpdate={() => {}}
      initialDecisionOpen
    />,
  );
  expect(screen.getByRole("region", { name: "决策与 Main Agent" })).toHaveClass(
    "drawer-open",
  );
  expect(
    screen.getByRole("region", { name: "决策与 Main Agent" }),
  ).toHaveFocus();
});

it("saves the exact dual-revision plan body without dispatching approval", async () => {
  const revised = {
    mission_id: "m1",
    plan_revision: 4,
    parent_plan_revision: 3,
    epoch: 1,
    mission_revision: 10,
    source: "user",
    reason: "拆分实现与验证关注点",
    plan: { ...mission.plan!, revision: 4, summary: "当前交付步骤" },
    plan_digest: "body-digest",
    created_at: "2026-10-18T09:00:00Z",
  };
  const fetcher = vi.fn(
    async (input: string | URL | Request, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST" && url.endsWith("/plan-revisions"))
        return response({
          mission: { ...mission, revision: 10, plan: revised.plan },
          plan_revision: revised,
        });
      return reliabilityRead(url);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  const update = vi.fn();
  render(<MissionWorkspace mission={mission} onUpdate={update} />);
  openPlanEditor();
  fireEvent.change(screen.getByLabelText("计划摘要"), {
    target: { value: "当前交付步骤" },
  });
  fireEvent.change(screen.getByLabelText("修订原因"), {
    target: { value: "拆分实现与验证关注点" },
  });
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
  const save = screen.getByRole("button", { name: "保存修订（不审批）" });
  fireEvent.click(save);
  fireEvent.click(save);
  await waitFor(() => expect(update).toHaveBeenCalled());
  const saveCall = fetcher.mock.calls.find(
    ([url, init]) =>
      String(url).endsWith("/plan-revisions") && init?.method === "POST",
  );
  expect(JSON.parse(String(saveCall?.[1]?.body))).toEqual({
    expected_mission_revision: 9,
    expected_plan_revision: 3,
    reason: "拆分实现与验证关注点",
    plan: {
      summary: "当前交付步骤",
      steps: [
        {
          id: "s1",
          title: "检查校验边界",
          description: "保留既有接口",
          depends_on: [],
          files: ["src/import.ts"],
        },
      ],
    },
  });
  expect(
    fetcher.mock.calls.filter(
      ([url, init]) =>
        String(url).endsWith("/plan-revisions") && init?.method === "POST",
    ),
  ).toHaveLength(1);
  expect(
    fetcher.mock.calls.some(([url]) => String(url).endsWith("/approve")),
  ).toBe(false);
});

it("retains a conflicting draft, consumes the newer snapshot, and requires explicit rebase", async () => {
  const current = {
    ...mission,
    revision: 10,
    plan: { ...mission.plan!, revision: 4, summary: "服务端新计划" },
  };
  const fetcher = vi.fn(
    async (input: string | URL | Request, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST" && url.endsWith("/plan-revisions"))
        return response(
          { error: { code: "conflict", message: "revision conflict" } },
          409,
        );
      if (url === "/api/missions/m1") return response({ mission: current });
      return reliabilityRead(url);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  function Controlled() {
    const [value, setValue] = useState(mission);
    return <MissionWorkspace mission={value} onUpdate={setValue} />;
  }
  render(<Controlled />);
  openPlanEditor();
  fireEvent.change(screen.getByLabelText("计划摘要"), {
    target: { value: "我的未保存计划" },
  });
  fireEvent.change(screen.getByLabelText("修订原因"), {
    target: { value: "需要保留的理由" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存修订（不审批）" }));
  await screen.findByText(/草稿仍保留/);
  expect(screen.getByLabelText("计划摘要")).toHaveValue("我的未保存计划");
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "基于当前版本重新编辑" }));
  expect(screen.queryByText(/草稿仍保留/)).not.toBeInTheDocument();
  expect(screen.getByLabelText("计划摘要")).toHaveValue("我的未保存计划");
});

it("retains a draft when saving fails without pretending it was approved", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST" && url.endsWith("/plan-revisions"))
        return response({ error: { message: "计划保存暂时不可用" } }, 500);
      return reliabilityRead(url);
    }),
  );
  render(<MissionWorkspace mission={mission} onUpdate={() => {}} />);
  openPlanEditor();
  fireEvent.change(screen.getByLabelText("计划摘要"), {
    target: { value: "失败也要保留" },
  });
  fireEvent.change(screen.getByLabelText("修订原因"), {
    target: { value: "验证失败保留" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存修订（不审批）" }));
  expect(await screen.findByText("计划保存暂时不可用")).toBeVisible();
  expect(screen.getByLabelText("计划摘要")).toHaveValue("失败也要保留");
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
});

it("freezes a draft while its save is pending and restores the same draft after failure", async () => {
  const user = userEvent.setup();
  let finishSave!: (value: Response) => void;
  const fetcher = vi.fn(
    async (input: string | URL | Request, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST" && url.endsWith("/plan-revisions"))
        return new Promise<Response>((resolve) => {
          finishSave = resolve;
        });
      return reliabilityRead(url);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  render(<MissionWorkspace mission={mission} onUpdate={() => {}} />);
  openPlanEditor();
  const summary = screen.getByLabelText("计划摘要");
  fireEvent.change(summary, { target: { value: "提交时冻结的草稿" } });
  fireEvent.change(screen.getByLabelText("修订原因"), {
    target: { value: "验证保存期间不会丢失修改" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存修订（不审批）" }));

  expect(summary).toHaveAttribute("readonly");
  expect(screen.getByRole("button", { name: "添加步骤" })).toBeDisabled();
  await user.type(summary, "不应写入的并发修改");
  expect(summary).toHaveValue("提交时冻结的草稿");
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();

  finishSave(response({ error: { message: "保存失败" } }, 500));
  expect(await screen.findByText("保存失败")).toBeVisible();
  expect(summary).not.toHaveAttribute("readonly");
  expect(summary).toHaveValue("提交时冻结的草稿");
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
  expect(
    fetcher.mock.calls.some(([url]) => String(url).endsWith("/approve")),
  ).toBe(false);
});

it("keeps dirty drafts across task tabs and discard restores the separate approval action", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input) => reliabilityRead(String(input))),
  );
  render(<MissionWorkspace mission={mission} onUpdate={() => {}} />);
  openPlanEditor();
  fireEvent.change(screen.getByLabelText("计划摘要"), {
    target: { value: "跨视角保留" },
  });
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
  fireEvent.click(screen.getByRole("tab", { name: "代码变更" }));
  fireEvent.click(screen.getByRole("tab", { name: "计划链路" }));
  expect(screen.getByLabelText("计划摘要")).toHaveValue("跨视角保留");
  fireEvent.click(screen.getByRole("button", { name: "丢弃草稿" }));
  expect(screen.getByLabelText("计划摘要")).toHaveValue("先验证，再调整");
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeEnabled();
});

it("marks a local draft stale when a newer mission snapshot arrives", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input) => reliabilityRead(String(input))),
  );
  const { rerender } = render(
    <MissionWorkspace mission={mission} onUpdate={() => {}} />,
  );
  openPlanEditor();
  fireEvent.change(screen.getByLabelText("计划摘要"), {
    target: { value: "保留这个草稿" },
  });
  rerender(
    <MissionWorkspace
      mission={{
        ...mission,
        revision: 10,
        plan: { ...mission.plan!, revision: 4, summary: "服务端正文" },
      }}
      onUpdate={() => {}}
    />,
  );
  expect(screen.getByText(/服务端任务或计划版本已变化/)).toBeVisible();
  expect(screen.getByLabelText("计划摘要")).toHaveValue("保留这个草稿");
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
});

it("keeps the mission visible when history fails and shows recorded operation recovery facts", async () => {
  const operation = {
    id: "0123456789abcdef0123456789abcdef",
    mission_id: "m1",
    kind: "apply",
    status: "interrupted",
    expected_revision: 9,
    epoch: 1,
    plan_revision: 3,
    before: { id: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", digest: "old-digest" },
    base_digest: "base-digest",
    intent_digest: "intent-digest",
    approval_digest: "approval-digest",
    result: {
      generation: {
        id: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        digest: "retained-unselected-digest",
      },
      changes: [],
    },
    error: "publish stage interrupted",
    created_at: "2026-10-18T08:00:00Z",
    updated_at: "2026-10-18T08:01:00Z",
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input) => {
      const url = String(input);
      if (url.endsWith("/plan-revisions"))
        return response({ error: { message: "修订历史暂时不可用" } }, 500);
      if (url.endsWith("/local-operations"))
        return response({ local_operations: [operation] });
      throw new Error(`unexpected ${url}`);
    }),
  );
  render(
    <MissionWorkspace
      mission={{
        ...mission,
        workspace_mode: "generations_v1",
        candidate_generation: {
          id: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          digest: "old-digest",
        },
      }}
      onUpdate={() => {}}
    />,
  );
  expect(screen.getByRole("heading", { name: mission.title })).toBeVisible();
  expect(await screen.findByText("修订历史暂时不可用")).toBeVisible();
  fireEvent.click(screen.getByRole("tab", { name: "总览" }));
  expect(
    await screen.findByRole("heading", { name: "apply · interrupted" }),
  ).toBeVisible();
  expect(screen.getByText("publish stage interrupted")).toBeVisible();
  expect(document.body).toHaveTextContent(
    "本次操作未提交；请核对错误与后续操作记录",
  );
  expect(document.body).not.toHaveTextContent("已恢复执行");
  expect(
    screen.queryByRole("button", { name: /^(恢复|回滚|自动恢复)/ }),
  ).not.toBeInTheDocument();
});

it("labels operation generations and next actions as historical facts after later commits", async () => {
  const previousResult = {
    id: "11111111111111111111111111111111",
    mission_id: "m1",
    kind: "apply",
    status: "interrupted",
    expected_revision: 9,
    epoch: 1,
    plan_revision: 3,
    before: { id: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", digest: "old-digest" },
    base_digest: "base-digest",
    intent_digest: "first-intent",
    approval_digest: "first-approval",
    result: {
      generation: {
        id: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        digest: "uncommitted-digest",
      },
      changes: [],
    },
    error: "publish interrupted",
    created_at: "2026-10-18T08:00:00Z",
    updated_at: "2026-10-18T08:01:00Z",
  };
  const committedResult = {
    ...previousResult,
    id: "22222222222222222222222222222222",
    status: "committed",
    expected_revision: 10,
    intent_digest: "second-intent",
    approval_digest: "second-approval",
    result: {
      generation: {
        id: "cccccccccccccccccccccccccccccccc",
        digest: "current-digest",
      },
      changes: [],
    },
    error: "",
    created_at: "2026-10-18T09:00:00Z",
    updated_at: "2026-10-18T09:01:00Z",
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input) => {
      const url = String(input);
      if (url.endsWith("/plan-revisions"))
        return response({ plan_revisions: [] });
      if (url.endsWith("/local-operations"))
        return response({
          local_operations: [previousResult, committedResult],
        });
      throw new Error(`unexpected ${url}`);
    }),
  );
  render(
    <MissionWorkspace
      mission={{
        ...mission,
        workspace_mode: "generations_v1",
        candidate_generation: committedResult.result.generation,
        candidate_digest: committedResult.result.generation.digest,
      }}
      onUpdate={() => {}}
      initialView="总览"
    />,
  );

  expect(
    await screen.findByRole("heading", { name: "apply · committed" }),
  ).toBeVisible();
  expect(document.body).toHaveTextContent(
    "操作结果 generation（当前记录的选择）：",
  );
  expect(document.body).toHaveTextContent(
    "操作结果 generation（本次操作未提交）：",
  );
  expect(
    screen.getByText(/本次操作未提交；请核对错误与后续操作记录/),
  ).toBeVisible();
  expect(document.body).not.toHaveTextContent("仍选择原候选");
});

it("labels new ordinary generation mode and unchanged legacy mode truthfully", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input) => reliabilityRead(String(input))),
  );
  const { rerender } = render(
    <MissionWorkspace
      mission={{
        ...mission,
        workspace_mode: "generations_v1",
        approvals: [
          {
            kind: "execute",
            revision: 9,
            plan_revision: 3,
            created_at: "2026-10-18",
            digest: "approval",
          },
        ],
      }}
      onUpdate={() => {}}
    />,
  );
  fireEvent.click(screen.getByRole("tab", { name: "总览" }));
  expect(await screen.findByText("不可变候选模式")).toBeVisible();
  expect(screen.queryByText("旧任务兼容模式")).not.toBeInTheDocument();
  rerender(
    <MissionWorkspace
      mission={{
        ...mission,
        approvals: [
          {
            kind: "execute",
            revision: 9,
            plan_revision: 3,
            created_at: "2026-10-18",
            digest: "approval",
          },
        ],
      }}
      onUpdate={() => {}}
    />,
  );
  expect(await screen.findByText("旧任务兼容模式")).toBeVisible();
  fireEvent.click(screen.getByRole("tab", { name: "审批" }));
  expect(screen.getAllByText(/任务 r9 · 计划 v3/).length).toBeGreaterThan(0);
});

it("keeps unknown workspace modes readable but disables every runtime and edit action", async () => {
  const fetcher = vi.fn(
    async (input: string | URL | Request, _init?: RequestInit) =>
      reliabilityRead(String(input)),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <MissionWorkspace
      mission={{ ...mission, workspace_mode: "future_v2" }}
      onUpdate={() => {}}
      initialDecisionOpen
    />,
  );

  expect(screen.getByText(/当前版本不支持的工作区模式/)).toBeVisible();
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
  expect(screen.getByLabelText("向 Main 提问")).toBeDisabled();
  openPlanEditor();
  expect(screen.getByText("不支持的只读模式：future_v2")).toBeVisible();
  expect(screen.getByLabelText("计划摘要")).toHaveAttribute("readonly");
  expect(
    screen.getByRole("button", { name: "保存修订（不审批）" }),
  ).toBeDisabled();

  fireEvent.click(screen.getByRole("tab", { name: "总览" }));
  expect(await screen.findByText("未知模式：future_v2")).toBeVisible();
  expect(screen.getByText(/当前界面不声称能够执行或恢复/)).toBeVisible();
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
    false,
  );
});

it("retains plan revision and immutable candidate affordances for the exact controlled mode pair", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input) => {
      const url = String(input);
      if (url.endsWith("/read-grants")) return response({ read_grants: [] });
      return reliabilityRead(url);
    }),
  );
  render(
    <MissionWorkspace
      mission={{
        ...mission,
        workspace_mode: "generations_controlled_read_v1",
        tool_mode: "controlled_read_v1",
        task_bindings: controlledBindings,
        candidate_generation: {
          id: "controlled-generation",
          digest: "controlled-generation-digest",
        },
      }}
      onUpdate={() => {}}
    />,
  );

  openPlanEditor();
  expect(screen.getByText("受控只读 · 可恢复候选模式")).toBeVisible();
  expect(screen.getByLabelText("计划摘要")).not.toHaveAttribute("readonly");
  fireEvent.click(screen.getByRole("tab", { name: "总览" }));
  expect(await screen.findByText("受控只读 · 不可变候选模式")).toBeVisible();
  expect(screen.getByText("controlled-generation")).toBeVisible();
});

it("keeps mismatched workspace and tool modes readable but non-editable", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input) => reliabilityRead(String(input))),
  );
  render(
    <MissionWorkspace
      mission={{
        ...mission,
        workspace_mode: "generations_v1",
        tool_mode: "controlled_read_v1",
      }}
      onUpdate={() => {}}
      initialDecisionOpen
    />,
  );

  expect(screen.getByText(/当前版本不支持的工作区模式/)).toBeVisible();
  openPlanEditor();
  expect(screen.getByText(/不支持的模式组合/)).toBeVisible();
  expect(screen.getByLabelText("计划摘要")).toHaveAttribute("readonly");
});
