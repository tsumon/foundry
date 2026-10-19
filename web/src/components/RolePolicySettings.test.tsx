import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { response, profile } from "../test/fixtures";
import { roles, type Settings } from "../types";
import type { RolePolicyProposal, RolePolicyState } from "../rolePolicyTypes";
import { RolePolicySettings } from "./RolePolicySettings";
import { readAppStyles } from "../test/appStyles";

const emptyRole = () => ({
  candidates: [],
  required_capabilities: [],
  allowed_spend_classes: [],
  preference: "candidate_order" as const,
});

const emptyState = (): RolePolicyState => ({
  policy: {
    revision: 0,
    created_at: "",
    roles: Object.fromEntries(
      roles.map((role) => [role, emptyRole()]),
    ) as unknown as RolePolicyState["policy"]["roles"],
  },
  profiles_digest: "0".repeat(64),
});

it("keeps capability checkboxes compact while candidate text controls fill the editor", async () => {
  vi.stubGlobal("fetch", async () => response({ state: emptyState() }));
  // Assert the CSS cascade, not physical layout (which needs real-browser QA).
  const style = document.createElement("style");
  style.textContent = readAppStyles();
  document.head.append(style);
  try {
    render(
      <RolePolicySettings
        profiles={[profile]}
        disabled={false}
        onConfirmed={() => {}}
      />,
    );
    fireEvent.click(await screen.findByLabelText("编辑 Main 策略，0 个候选"));
    fireEvent.click(screen.getByRole("button", { name: "为 Main 添加候选" }));
    expect(
      getComputedStyle(screen.getByLabelText("Main 候选 1 声明能力：推理"))
        .width,
    ).toBe("16px");
    expect(
      getComputedStyle(screen.getByLabelText("Main 要求能力：推理")).width,
    ).toBe("16px");
    expect(
      getComputedStyle(screen.getByLabelText("Main 候选 1 调用记录 ID")).width,
    ).toBe("100%");
  } finally {
    style.remove();
  }
});

const proposal = (confirmable = true): RolePolicyProposal => ({
  id: "proposal-1234567890abcdef",
  policy_revision: 2,
  created_at: "2026-10-19T01:03:00Z",
  settings_digest: "2".repeat(64),
  inputs_digest: "3".repeat(64),
  digest: "4".repeat(64),
  confirmable,
  roles: Object.fromEntries(
    roles.map((role) => [
      role,
      {
        selected_profile_id: confirmable ? profile.id : "",
        required_capabilities: ["reasoning"],
        candidates: [
          {
            profile_id: profile.id,
            profile_digest: "1".repeat(64),
            spend_class: "unknown",
            eligible: confirmable,
            reasons: confirmable
              ? []
              : ["required_capability_missing", "evidence_invalid"],
            capabilities: ["reasoning"],
            rationale: "User declaration only <script>alert(1)</script>",
            evidence: {
              invocation_id: "invocation-long-" + "a".repeat(48),
              mission_id: "mission-1",
              role,
              status: role === "planner" ? "outcome_unknown" : "completed",
              ...(role === "planner"
                ? { error_code: "response_incomplete" as const }
                : {}),
              environment: "simulation",
              finished_at: "2026-10-19T01:02:00Z",
            },
            execution_notice: "production_live_gate_closed",
          },
        ],
      },
    ]),
  ) as RolePolicyProposal["roles"],
});

it("does not generate, probe, or confirm on mount and does not fake an empty policy after load failure", async () => {
  const requests: string[] = [];
  vi.stubGlobal(
    "fetch",
    async (input: RequestInfo | URL, init?: RequestInit) => {
      requests.push(`${init?.method ?? "GET"} ${String(input)}`);
      return response(
        { error: { code: "unavailable", message: "unavailable" } },
        503,
      );
    },
  );
  render(
    <RolePolicySettings
      profiles={[]}
      disabled={false}
      onConfirmed={() => {
        throw new Error("unexpected confirmation");
      }}
    />,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent("unavailable");
  expect(requests).toEqual(["GET /api/role-model-policy"]);
  expect(screen.queryByText("策略版本 0")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "生成建议" }),
  ).not.toBeInTheDocument();
});

it("saves declared inputs with the explicit revision and profile digest without applying settings", async () => {
  const onConfirmed = vi.fn();
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method) return response({ state: emptyState() });
    if (path === "/api/role-model-policy" && init.method === "PUT")
      return response({
        policy: {
          ...emptyState().policy,
          revision: 1,
          created_at: "2026-10-19T01:00:00Z",
          roles: {
            ...emptyState().policy.roles,
            main: {
              candidates: [
                {
                  profile_id: profile.id,
                  profile_digest: "1".repeat(64),
                  capabilities: ["reasoning"],
                  rationale: "适合做有边界的推理",
                  evidence_invocation_id: "invocation-1",
                },
              ],
              required_capabilities: ["reasoning"],
              allowed_spend_classes: ["unknown"],
              preference: "candidate_order",
            },
          },
        },
      });
    throw new Error(`unexpected ${init?.method ?? "GET"} ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <RolePolicySettings
      profiles={[profile]}
      disabled={false}
      onConfirmed={onConfirmed}
    />,
  );
  await screen.findByText(/策略版本 0/);
  fireEvent.click(screen.getByLabelText("编辑 Main 策略，0 个候选"));
  fireEvent.click(screen.getByRole("button", { name: "为 Main 添加候选" }));
  fireEvent.change(screen.getByLabelText("Main 候选 1 连接"), {
    target: { value: profile.id },
  });
  fireEvent.click(screen.getByLabelText("Main 候选 1 声明能力：推理"));
  fireEvent.change(screen.getByLabelText("Main 候选 1 声明依据"), {
    target: { value: "适合做有边界的推理" },
  });
  fireEvent.change(screen.getByLabelText("Main 候选 1 调用记录 ID"), {
    target: { value: "invocation-1" },
  });
  fireEvent.click(screen.getByLabelText("Main 要求能力：推理"));
  fireEvent.click(screen.getByLabelText("Main 允许费用：未知"));
  expect(screen.getByRole("button", { name: "生成建议" })).toBeDisabled();
  expect(screen.getByText(/本地编辑尚未保存/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "保存策略版本" }));

  await screen.findByText(/策略版本 1 已保存/);
  const put = fetcher.mock.calls.find(([, init]) => init?.method === "PUT")!;
  expect(JSON.parse(String(put[1]?.body))).toMatchObject({
    expected_revision: 0,
    profiles_digest: "0".repeat(64),
    roles: {
      main: {
        candidates: [
          {
            profile_id: profile.id,
            capabilities: ["reasoning"],
            rationale: "适合做有边界的推理",
            evidence_invocation_id: "invocation-1",
          },
        ],
        required_capabilities: ["reasoning"],
        allowed_spend_classes: ["unknown"],
        preference: "candidate_order",
      },
    },
  });
  expect(onConfirmed).not.toHaveBeenCalled();
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
    false,
  );
  expect(screen.getByText(/保存不应用角色分配/)).toBeVisible();
});

it("renders finite recommendation reasons and distinguishes declarations, call receipts, and live-closed execution", async () => {
  const state = { ...emptyState(), latest_proposal: proposal(false) };
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response({ state })),
  );
  render(
    <RolePolicySettings
      profiles={[profile]}
      disabled={false}
      onConfirmed={vi.fn()}
    />,
  );
  const preview = await screen.findByRole("region", { name: "角色模型建议" });
  expect(within(preview).getByText(/建议不完整，不能确认/)).toBeVisible();
  expect(within(preview).getAllByText("缺少要求的能力声明")).toHaveLength(7);
  expect(
    within(preview).getAllByText("引用的调用记录无效或已不匹配"),
  ).toHaveLength(7);
  expect(within(preview).getAllByText(/用户声明，未独立验证/)).toHaveLength(7);
  expect(within(preview).getAllByText(/真实运行门禁关闭/)).toHaveLength(7);
  expect(
    within(preview).getAllByText(/调用返回完成，只证明该次调用已返回/),
  ).toHaveLength(6);
  expect(within(preview).getByText(/结果未知 · 响应不完整/)).toBeVisible();
  expect(within(preview).getAllByText(/本地模拟记录/)).toHaveLength(7);
  expect(
    within(preview).getAllByText(/<script>alert\(1\)<\/script>/),
  ).toHaveLength(7);
  expect(document.querySelector("script")).toBeNull();
  expect(
    screen.getByRole("button", { name: "确认并用于未来任务" }),
  ).toBeDisabled();
});

it("invalidates the local proposal after any edit and never autosubmits a replacement", async () => {
  const state = { ...emptyState(), latest_proposal: proposal(true) };
  const fetcher = vi.fn(async () => response({ state }));
  vi.stubGlobal("fetch", fetcher);
  render(
    <RolePolicySettings
      profiles={[profile]}
      disabled={false}
      onConfirmed={vi.fn()}
    />,
  );
  await screen.findByText(/七个角色均有可选建议/);
  fireEvent.click(screen.getByLabelText("编辑 Main 策略，0 个候选"));
  fireEvent.click(screen.getByRole("button", { name: "为 Main 添加候选" }));
  expect(
    screen.queryByRole("region", { name: "角色模型建议" }),
  ).not.toBeInTheDocument();
  expect(screen.getByText(/本地编辑已使旧建议失效/)).toBeVisible();
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("blocks generation and confirmation after an edit until the draft is saved", async () => {
  const state = { ...emptyState(), latest_proposal: proposal(true) };
  const savedPolicy = {
    ...state.policy,
    revision: 1,
    roles: {
      ...state.policy.roles,
      main: {
        ...state.policy.roles.main,
        required_capabilities: ["reasoning" as const],
      },
    },
  };
  const nextProposal = { ...proposal(true), policy_revision: 1 };
  const settings: Settings = {
    default_profile_id: profile.id,
    role_bindings: Object.fromEntries(roles.map((role) => [role, profile.id])),
    model_policy_decision_id: "decision-after-save",
  };
  const onConfirmed = vi.fn();
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method) return response({ state });
    if (path === "/api/role-model-policy" && init.method === "PUT")
      return response({ policy: savedPolicy });
    if (path === "/api/role-model-policy/proposals")
      return response({ proposal: nextProposal });
    if (path.includes("/confirm"))
      return response({
        decision: {
          id: settings.model_policy_decision_id,
          proposal_id: nextProposal.id,
          confirmed_at: "2026-10-19T01:04:00Z",
          proposal: nextProposal,
        },
        settings,
        applied: true,
      });
    throw new Error(`unexpected ${init.method} ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <RolePolicySettings
      profiles={[profile]}
      disabled={false}
      onConfirmed={onConfirmed}
    />,
  );
  await screen.findByText(/七个角色均有可选建议/);
  fireEvent.click(screen.getByLabelText("编辑 Main 策略，0 个候选"));
  fireEvent.click(screen.getByLabelText("Main 要求能力：推理"));

  const generate = screen.getByRole("button", { name: "生成建议" });
  expect(generate).toBeDisabled();
  fireEvent.click(generate);
  expect(
    screen.queryByRole("button", { name: "确认并用于未来任务" }),
  ).not.toBeInTheDocument();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
  expect(screen.getByText(/请先保存策略版本/)).toBeVisible();

  fireEvent.click(screen.getByRole("button", { name: "保存策略版本" }));
  await screen.findByText(/策略版本 1 已保存/);
  expect(generate).toBeEnabled();
  fireEvent.click(generate);
  fireEvent.click(
    await screen.findByRole("button", { name: "确认并用于未来任务" }),
  );
  await waitFor(() => expect(onConfirmed).toHaveBeenCalledWith(settings));
  expect(
    fetcher.mock.calls
      .filter(([, init]) => init?.method)
      .map(([path, init]) => `${init!.method} ${path}`),
  ).toEqual([
    "PUT /api/role-model-policy",
    "POST /api/role-model-policy/proposals",
    `POST /api/role-model-policy/proposals/${nextProposal.id}/confirm`,
  ]);
});

it("locks duplicate proposal clicks while the first request is pending", async () => {
  let release!: (value: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    release = resolve;
  });
  const fetcher = vi.fn((path: string, init?: RequestInit) => {
    if (!init?.method)
      return Promise.resolve(response({ state: emptyState() }));
    if (path === "/api/role-model-policy/proposals") return pending;
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <RolePolicySettings profiles={[]} disabled={false} onConfirmed={vi.fn()} />,
  );
  const generate = await screen.findByRole("button", { name: "生成建议" });
  fireEvent.click(generate);
  fireEvent.click(generate);
  expect(generate).toBeDisabled();
  expect(
    fetcher.mock.calls.filter(
      ([path]) => path === "/api/role-model-policy/proposals",
    ),
  ).toHaveLength(1);
  release(response({ proposal: proposal(false) }));
  await screen.findByRole("region", { name: "角色模型建议" });
});

it("keeps a stale conflict explicit and offers regeneration without automatically retrying", async () => {
  const state = { ...emptyState(), latest_proposal: proposal(true) };
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) =>
    !init?.method
      ? response({ state })
      : response(
          {
            error: {
              code: "role_policy_stale",
              message: "role policy inputs have changed",
            },
          },
          409,
        ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <RolePolicySettings
      profiles={[profile]}
      disabled={false}
      onConfirmed={vi.fn()}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "确认并用于未来任务" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    /配置已变化，请重新生成/,
  );
  expect(screen.getByRole("button", { name: "重新生成建议" })).toBeVisible();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it("reconciles an unknown confirmation once with GETs and never replays the POST", async () => {
  const first = { ...emptyState(), latest_proposal: proposal(true) };
  const decision = {
    id: "decision-1234567890abcdef",
    proposal_id: proposal(true).id,
    confirmed_at: "2026-10-19T01:04:00Z",
    proposal: proposal(true),
  };
  const reconciled = { ...first, active_decision: decision };
  const settings: Settings = {
    default_profile_id: "manual-default",
    role_bindings: Object.fromEntries(roles.map((role) => [role, profile.id])),
    model_policy_decision_id: decision.id,
  };
  let policyReads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.includes("/confirm") && init?.method === "POST")
      throw new TypeError("connection lost after send");
    if (path === "/api/role-model-policy" && !init?.method) {
      policyReads += 1;
      return response({ state: policyReads === 1 ? first : reconciled });
    }
    if (path === "/api/profiles" && !init?.method)
      return response({ profiles: [profile], settings });
    throw new Error(`unexpected ${init?.method ?? "GET"} ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const onConfirmed = vi.fn();
  render(
    <RolePolicySettings
      profiles={[profile]}
      disabled={false}
      onConfirmed={onConfirmed}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "确认并用于未来任务" }),
  );
  await waitFor(() => expect(onConfirmed).toHaveBeenCalledWith(settings));
  expect(screen.getByText(/已通过只读对账确认提交结果/)).toBeVisible();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
  expect(policyReads).toBe(2);
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/api/profiles"),
  ).toHaveLength(1);
});

it("propagates authoritative manual settings after an unmatched unknown confirmation", async () => {
  const attempted = proposal(true);
  const decision = {
    id: "decision-attempted",
    proposal_id: attempted.id,
    confirmed_at: "2026-10-19T01:04:00Z",
    proposal: attempted,
  };
  const initial = { ...emptyState(), latest_proposal: attempted };
  const reconciled = { ...initial, active_decision: decision };
  const manual: Settings = {
    default_profile_id: "manual-default",
    role_bindings: { main: "manual-profile" },
  };
  let policyReads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.includes("/confirm") && init?.method === "POST")
      throw new TypeError("connection lost after send");
    if (path === "/api/role-model-policy" && !init?.method) {
      policyReads += 1;
      return response({ state: policyReads === 1 ? initial : reconciled });
    }
    if (path === "/api/profiles" && !init?.method)
      return response({ profiles: [profile], settings: manual });
    throw new Error(`unexpected ${init?.method ?? "GET"} ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const onConfirmed = vi.fn();
  render(
    <RolePolicySettings
      profiles={[profile]}
      disabled={false}
      onConfirmed={onConfirmed}
    />,
  );

  fireEvent.click(
    await screen.findByRole("button", { name: "确认并用于未来任务" }),
  );

  await waitFor(() => expect(onConfirmed).toHaveBeenCalledWith(manual));
  expect(screen.getByRole("alert")).toHaveTextContent(/未发现匹配决定/);
  expect(screen.queryByText(/当前活动决定/)).not.toBeInTheDocument();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it("propagates a differently active decision after an unmatched unknown confirmation", async () => {
  const attempted = proposal(true);
  const currentProposal = {
    ...proposal(true),
    id: "proposal-current",
    policy_revision: 3,
  };
  const currentDecision = {
    id: "decision-current",
    proposal_id: currentProposal.id,
    confirmed_at: "2026-10-19T02:04:00Z",
    proposal: currentProposal,
  };
  const initial = { ...emptyState(), latest_proposal: attempted };
  const reconciled = { ...initial, active_decision: currentDecision };
  const currentSettings: Settings = {
    default_profile_id: "current-default",
    role_bindings: Object.fromEntries(
      roles.map((role) => [role, `current-${role}`]),
    ),
    model_policy_decision_id: currentDecision.id,
  };
  let policyReads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.includes("/confirm") && init?.method === "POST")
      throw new TypeError("connection lost after send");
    if (path === "/api/role-model-policy" && !init?.method) {
      policyReads += 1;
      return response({ state: policyReads === 1 ? initial : reconciled });
    }
    if (path === "/api/profiles" && !init?.method)
      return response({ profiles: [profile], settings: currentSettings });
    throw new Error(`unexpected ${init?.method ?? "GET"} ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const onConfirmed = vi.fn();
  render(
    <RolePolicySettings
      profiles={[profile]}
      disabled={false}
      onConfirmed={onConfirmed}
    />,
  );

  fireEvent.click(
    await screen.findByRole("button", { name: "确认并用于未来任务" }),
  );

  await waitFor(() =>
    expect(onConfirmed).toHaveBeenCalledWith(currentSettings),
  );
  expect(screen.getByRole("alert")).toHaveTextContent(/未发现匹配决定/);
  expect(screen.getByText(/当前活动决定/)).toHaveTextContent(
    currentDecision.id,
  );
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it("treats a duplicate confirmation as historical when current settings are manual", async () => {
  const oldProposal = proposal(true);
  const decision = {
    id: "decision-original",
    proposal_id: oldProposal.id,
    confirmed_at: "2026-10-19T01:04:00Z",
    proposal: oldProposal,
  };
  const initial = { ...emptyState(), latest_proposal: oldProposal };
  const manual: Settings = {
    default_profile_id: "manual-default",
    role_bindings: { main: "manual-profile" },
  };
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) =>
    !init?.method
      ? response({ state: initial })
      : response({ decision, settings: manual, applied: false }),
  );
  vi.stubGlobal("fetch", fetcher);
  const onConfirmed = vi.fn();
  render(
    <RolePolicySettings
      profiles={[profile]}
      disabled={false}
      onConfirmed={onConfirmed}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "确认并用于未来任务" }),
  );
  await waitFor(() => expect(onConfirmed).toHaveBeenCalledWith(manual));
  expect(screen.getByText(/原决定只作为历史收据/)).toBeVisible();
  expect(screen.queryByText(/当前活动决定/)).not.toBeInTheDocument();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it("retains a different active decision when an older duplicate receipt is returned", async () => {
  const oldProposal = proposal(true);
  const oldDecision = {
    id: "decision-original",
    proposal_id: oldProposal.id,
    confirmed_at: "2026-10-19T01:04:00Z",
    proposal: oldProposal,
  };
  const currentProposal = {
    ...proposal(true),
    id: "proposal-current",
    policy_revision: 3,
  };
  const currentDecision = {
    id: "decision-current",
    proposal_id: currentProposal.id,
    confirmed_at: "2026-10-19T02:04:00Z",
    proposal: currentProposal,
  };
  const initial = {
    ...emptyState(),
    latest_proposal: oldProposal,
    active_decision: currentDecision,
  };
  const currentSettings: Settings = {
    default_profile_id: "p1",
    role_bindings: Object.fromEntries(roles.map((role) => [role, "p1"])),
    model_policy_decision_id: currentDecision.id,
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init?: RequestInit) =>
      !init?.method
        ? response({ state: initial })
        : response({
            decision: oldDecision,
            settings: currentSettings,
            applied: false,
          }),
    ),
  );
  render(
    <RolePolicySettings
      profiles={[profile]}
      disabled={false}
      onConfirmed={vi.fn()}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "确认并用于未来任务" }),
  );
  await screen.findByText(/原决定只作为历史收据/);
  expect(screen.getByText(/当前活动决定/)).toHaveTextContent(
    currentDecision.id,
  );
  expect(screen.getByText(/当前活动决定/)).not.toHaveTextContent(
    oldDecision.id,
  );
});
