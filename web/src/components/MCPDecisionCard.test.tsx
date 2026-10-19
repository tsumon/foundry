import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import { mission, response } from "../test/fixtures";
import type { Mission } from "../types";
import { MCPDecisionCard, type MCPUnresolvedDecision } from "./MCPDecisionCard";

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
const v2Mission = {
  ...mission,
  state: "planning",
  stage: "awaiting_tool_approval",
  revision: 8,
  plan: null,
  workspace_mode: "generations_controlled_mcp_read_v2",
  tool_mode: "controlled_mcp_read_v2",
  research_status: {
    current: true,
    can_decide: true,
    can_continue: false,
  },
} as Mission;
const decision = {
  current: true,
  can_decide: true,
  can_continue: false,
  proposal_id: "proposal-1",
  proposal_digest: "proposal-digest-1",
  revision: 8,
  purpose: "核对任务内工具审批流程",
  endpoint: "http://127.0.0.1:43123/mcp",
  tool: "reference.lookup",
  arguments: { topic: "mcp-workflow", long: "x".repeat(300) },
  policy: { type: "object", additionalProperties: false },
  source_invocation_id: "source-1",
  source_digest: "source-digest-1",
  test_only: true,
  used_calls: 1,
  remaining_calls: 2,
  budget,
  recipients: [
    {
      role: "researcher",
      name: "Research",
      protocol: "responses",
      model: "fixture-model",
    },
  ],
};

it("explicitly refreshes transient busy authority at the same revision without submitting", async () => {
  let available = false;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith("/mcp-decision"))
      return response({
        decision: available
          ? decision
          : { ...decision, can_decide: false, why_not: "engine_busy" },
      });
    if (path === `/api/missions/${v2Mission.id}`)
      return response({ mission: v2Mission });
    if (path.includes("/mcp-proposals/") && init?.method === "POST")
      return response({
        mission: { ...v2Mission, revision: 9, stage: "mcp_call" },
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <MCPDecisionCard
      mission={{
        ...v2Mission,
        research_status: {
          current: true,
          can_decide: false,
          can_continue: false,
          why_not: "engine_busy",
        },
      }}
      busy={false}
      inFlight={{ current: false }}
      onBusyChange={() => {}}
      onUpdate={() => {}}
    />,
  );
  await screen.findByText("控制器正在处理其他工作");
  const approve = screen.getByRole("button", {
    name: "批准本次调用并继续调研",
  });
  expect(approve).toBeDisabled();
  available = true;
  fireEvent.click(screen.getByRole("button", { name: "刷新当前可用状态" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "拒绝本次调用" })).toBeEnabled(),
  );
  expect(screen.queryByText("控制器正在处理其他工作")).not.toBeInTheDocument();
  expect(approve).toBeDisabled();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
  fireEvent.click(screen.getByLabelText(/确认本次参数/));
  fireEvent.click(approve);
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
    ).toHaveLength(1),
  );
});

it("keeps an original unresolved decision locked after a GET-only availability refresh", async () => {
  let reads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    expect(init?.method ?? "GET").toBe("GET");
    if (path.endsWith("/mcp-decision"))
      return response({
        decision:
          ++reads === 1
            ? { ...decision, can_decide: false, why_not: "engine_busy" }
            : decision,
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <MCPDecisionCard
      mission={v2Mission}
      busy={false}
      inFlight={{ current: false }}
      unresolved={{
        proposal_id: decision.proposal_id,
        proposal_digest: decision.proposal_digest,
        expected_revision: 8,
        decision: "approve",
        request_id: "original-uncertain-request",
      }}
      onBusyChange={() => {}}
      onUpdate={() => {}}
    />,
  );
  await screen.findByText("控制器正在处理其他工作");
  fireEvent.click(screen.getByRole("button", { name: "刷新当前可用状态" }));
  await waitFor(() =>
    expect(
      screen.queryByText("控制器正在处理其他工作"),
    ).not.toBeInTheDocument(),
  );
  expect(screen.getByText("original-uncertain-request")).toBeVisible();
  expect(
    screen.getByRole("button", { name: "仅查询原请求结果" }),
  ).toBeEnabled();
  expect(
    screen.getByRole("button", { name: "批准本次调用并继续调研" }),
  ).toBeDisabled();
  expect(screen.getByRole("button", { name: "拒绝本次调用" })).toBeDisabled();
  expect(screen.getByLabelText(/确认本次参数/)).toBeDisabled();
  expect(fetcher).toHaveBeenCalledTimes(2);
});

it("shows immutable call details, requires acknowledgement only for approval, and shares the click lock", async () => {
  let finish!: (value: Response) => void;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith("/mcp-decision")) return response({ decision });
    if (path.includes("/mcp-proposals/") && init?.method === "POST")
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const inFlight = { current: false };
  const onUpdate = vi.fn();
  render(
    <MCPDecisionCard
      mission={v2Mission}
      busy={false}
      inFlight={inFlight}
      onBusyChange={() => {}}
      onUpdate={onUpdate}
    />,
  );
  expect(await screen.findByText("核对任务内工具审批流程")).toBeVisible();
  expect(screen.getByText(/mcp-workflow/)).toBeVisible();
  expect(screen.getByText(/xxxxxxxxxx/)).toBeVisible();
  const approve = screen.getByRole("button", {
    name: "批准本次调用并继续调研",
  });
  expect(approve).toBeDisabled();
  expect(screen.getByRole("button", { name: "拒绝本次调用" })).toBeEnabled();
  fireEvent.click(screen.getByLabelText(/确认本次参数/));
  fireEvent.click(approve);
  fireEvent.click(approve);
  await waitFor(() => expect(inFlight.current).toBe(true));
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
  finish(
    response({
      mission: {
        ...v2Mission,
        revision: 9,
        stage: "mcp_authorized_pending",
      },
    }),
  );
  await waitFor(() => expect(onUpdate).toHaveBeenCalledOnce());
});

it("reconciles a lost decision response with the same request identity instead of posting again", async () => {
  let postedID = "";
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith("/mcp-decision")) return response({ decision });
    if (path.includes("/mcp-proposals/") && init?.method === "POST") {
      postedID = JSON.parse(String(init.body)).request_id;
      throw new TypeError("connection lost");
    }
    if (path.includes("/mcp-decisions/")) {
      expect(path.endsWith(`/${postedID}`)).toBe(true);
      return response({
        decision: {
          mission_id: v2Mission.id,
          request_id: postedID,
          proposal_id: decision.proposal_id,
          proposal_digest: decision.proposal_digest,
          expected_revision: 8,
          approved: true,
          invocation_id: "call-1",
          grant_id: "grant-1",
        },
      });
    }
    if (path === `/api/missions/${v2Mission.id}`)
      return response({
        mission: {
          ...v2Mission,
          revision: 9,
          stage: "mcp_call",
        },
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <MCPDecisionCard
      mission={v2Mission}
      busy={false}
      inFlight={{ current: false }}
      onBusyChange={() => {}}
      onUpdate={() => {}}
    />,
  );
  await screen.findByText("核对任务内工具审批流程");
  fireEvent.click(screen.getByLabelText(/确认本次参数/));
  fireEvent.click(
    screen.getByRole("button", { name: "批准本次调用并继续调研" }),
  );
  expect(await screen.findByText(/已按原请求编号确认审批记录/)).toBeVisible();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it("locks an unresolved decision to its original request until GET-only reconciliation succeeds", async () => {
  let receiptAvailable = false;
  let postedID = "";
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith("/mcp-decision")) return response({ decision });
    if (path.includes("/mcp-proposals/") && init?.method === "POST") {
      postedID = JSON.parse(String(init.body)).request_id;
      throw new TypeError("connection lost");
    }
    if (path.includes("/mcp-decisions/")) {
      expect(path.endsWith(`/${postedID}`)).toBe(true);
      if (!receiptAvailable) throw new TypeError("receipt unavailable");
      return response({
        decision: {
          mission_id: v2Mission.id,
          request_id: postedID,
          proposal_id: decision.proposal_id,
          proposal_digest: decision.proposal_digest,
          expected_revision: decision.revision,
          approved: false,
        },
      });
    }
    if (path === `/api/missions/${v2Mission.id}`)
      return response({
        mission: { ...v2Mission, revision: 9, stage: "research_turn" },
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);

  function Harness() {
    const [unresolved, setUnresolved] = useState<MCPUnresolvedDecision | null>(
      null,
    );
    return (
      <MCPDecisionCard
        mission={v2Mission}
        busy={false}
        inFlight={{ current: false }}
        unresolved={unresolved}
        onUnresolvedChange={setUnresolved}
        onBusyChange={() => {}}
        onUpdate={() => {}}
      />
    );
  }

  render(<Harness />);
  await screen.findByText("核对任务内工具审批流程");
  fireEvent.click(screen.getByRole("button", { name: "拒绝本次调用" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/响应未确认/);
  expect(screen.getByRole("button", { name: "拒绝本次调用" })).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "批准本次调用并继续调研" }),
  ).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "拒绝本次调用" }));
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);

  fireEvent.click(screen.getByRole("button", { name: "仅查询原请求结果" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/查询仍未确认/);
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);

  receiptAvailable = true;
  fireEvent.click(screen.getByRole("button", { name: "仅查询原请求结果" }));
  expect(await screen.findByText(/已按原请求编号确认拒绝记录/)).toBeVisible();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it("reports a confirmed server rejection without treating it as a lost response", async () => {
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith("/mcp-decision")) return response({ decision });
    if (path.includes("/mcp-proposals/") && init?.method === "POST")
      return response(
        { error: { code: "invalid_action", message: "invalid decision" } },
        400,
      );
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <MCPDecisionCard
      mission={v2Mission}
      busy={false}
      inFlight={{ current: false }}
      onBusyChange={() => {}}
      onUpdate={() => {}}
    />,
  );
  await screen.findByText("核对任务内工具审批流程");
  fireEvent.click(screen.getByRole("button", { name: "拒绝本次调用" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "invalid decision",
  );
  expect(fetcher).toHaveBeenCalledTimes(2);
});

it("keeps a historical card inspectable but disables actions when current authority is unavailable", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        decision: {
          ...decision,
          current: false,
          can_decide: false,
          why_not: "local_test_disabled",
        },
      }),
    ),
  );
  render(
    <MCPDecisionCard
      mission={{
        ...v2Mission,
        research_status: {
          current: false,
          can_decide: false,
          can_continue: false,
          why_not: "local_test_disabled",
        },
      }}
      busy={false}
      inFlight={{ current: false }}
      onBusyChange={() => {}}
      onUpdate={() => {}}
    />,
  );
  expect(await screen.findByText("核对任务内工具审批流程")).toBeVisible();
  expect(screen.getByText(/本地测试服务已停用/)).toBeVisible();
  expect(
    screen.getByRole("button", { name: "批准本次调用并继续调研" }),
  ).toBeDisabled();
  expect(screen.getByRole("button", { name: "拒绝本次调用" })).toBeDisabled();
});

it("refreshes a 409 without resubmitting and resets acknowledgement for the new proposal identity", async () => {
  const nextDecision = {
    ...decision,
    proposal_id: "proposal-2",
    proposal_digest: "proposal-digest-2",
    revision: 9,
  };
  let decisionReads = 0;
  const onUpdate = vi.fn();
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith("/mcp-decision"))
      return response({
        decision: ++decisionReads === 1 ? decision : nextDecision,
      });
    if (path.includes("/mcp-proposals/") && init?.method === "POST")
      return response({ error: { code: "conflict", message: "stale" } }, 409);
    if (path === `/api/missions/${v2Mission.id}`)
      return response({ mission: { ...v2Mission, revision: 9 } });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <MCPDecisionCard
      mission={v2Mission}
      busy={false}
      inFlight={{ current: false }}
      onBusyChange={() => {}}
      onUpdate={onUpdate}
    />,
  );
  await screen.findByText("核对任务内工具审批流程");
  const acknowledgement = screen.getByLabelText(/确认本次参数/);
  fireEvent.click(acknowledgement);
  fireEvent.click(
    screen.getByRole("button", { name: "批准本次调用并继续调研" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(/未自动重送/);
  expect(acknowledgement).not.toBeChecked();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
  expect(onUpdate).toHaveBeenCalledOnce();
  await waitFor(() => expect(decisionReads).toBe(2));
});

it("offers continuation only when the server status explicitly permits it", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response({ decision: null })),
  );
  const { rerender } = render(
    <MCPDecisionCard
      mission={{
        ...v2Mission,
        stage: "mcp_observation_ready",
        research_status: {
          current: true,
          can_decide: false,
          can_continue: false,
        },
      }}
      busy={false}
      inFlight={{ current: false }}
      onBusyChange={() => {}}
      onUpdate={() => {}}
    />,
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "继续调研" }),
    ).not.toBeInTheDocument(),
  );
  rerender(
    <MCPDecisionCard
      mission={{
        ...v2Mission,
        revision: 9,
        stage: "mcp_observation_ready",
        research_status: {
          current: true,
          can_decide: false,
          can_continue: true,
        },
      }}
      busy={false}
      inFlight={{ current: false }}
      onBusyChange={() => {}}
      onUpdate={() => {}}
    />,
  );
  expect(await screen.findByRole("button", { name: "继续调研" })).toBeVisible();
});
