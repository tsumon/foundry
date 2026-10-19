import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { mission, response } from "../test/fixtures";
import { MCPInvocationActivity } from "./MCPInvocationActivity";

const call = {
  id: "call-1",
  proposal_id: "proposal-1",
  status: "succeeded",
  phase: "result_sealed",
  tool: "reference.lookup",
  arguments: { topic: "mcp-workflow" },
  result_digest: "result-digest",
  requests: 3,
  response_bytes: 144,
  duration_millis: 18,
  created_at: "2026-10-18T01:00:00Z",
  finished_at: "2026-10-18T01:00:01Z",
  test_only: true,
};
const decisions = [
  {
    mission_id: mission.id,
    request_id: "reject-1",
    proposal_id: "proposal-0",
    proposal_digest: "digest-0",
    expected_revision: 7,
    approved: false,
  },
  {
    mission_id: mission.id,
    request_id: "approve-1",
    proposal_id: "proposal-1",
    proposal_digest: "digest-1",
    expected_revision: 8,
    approved: true,
    invocation_id: "call-1",
    grant_id: "grant-1",
  },
];

it("retries a failed detail GET only after explicit collapse and reopen at the same version", async () => {
  let detailReads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    expect(init?.method ?? "GET").toBe("GET");
    if (path.endsWith("/mcp-invocations"))
      return response({ invocations: [call], decisions: [] });
    if (path.endsWith("/mcp-invocations/call-1")) {
      if (++detailReads === 1) throw new TypeError("temporary read failure");
      return response({
        invocation: { ...call, result: { text: "verified after retry" } },
      });
    }
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const { rerender } = render(
    <MCPInvocationActivity mission={mission} visible eventSequence={10} />,
  );
  await screen.findByText("调用成功并封存结果");
  fireEvent.click(screen.getByRole("button", { name: /查看结果详情/ }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "temporary read failure",
  );
  rerender(
    <MCPInvocationActivity mission={mission} visible eventSequence={10} />,
  );
  expect(detailReads).toBe(1);
  fireEvent.click(screen.getByRole("button", { name: /收起结果详情/ }));
  fireEvent.click(screen.getByRole("button", { name: /查看结果详情/ }));
  expect(await screen.findByText(/verified after retry/)).toBeVisible();
  expect(detailReads).toBe(2);
  expect(fetcher).toHaveBeenCalledTimes(3);
});

it("renders durable decisions separately from calls and lazily shows escaped verified result detail", async () => {
  const fetcher = vi.fn(async (path: string) => {
    if (path.endsWith("/mcp-invocations"))
      return response({ invocations: [call], decisions });
    if (path.endsWith("/mcp-invocations/call-1"))
      return response({
        invocation: {
          ...call,
          result: { text: "<img src=x onerror=alert(1)>" },
        },
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <MCPInvocationActivity mission={mission} visible eventSequence={10} />,
  );
  const history = await screen.findByRole("region", {
    name: "本地 MCP 决策历史",
  });
  expect(within(history).getByText(/已拒绝/)).toBeVisible();
  expect(within(history).getByText(/已批准/)).toBeVisible();
  expect(within(history).getByText(/任务 r7/)).toBeVisible();
  expect(await screen.findByText("调用成功并封存结果")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: /查看结果详情/ }));
  expect(
    await screen.findByText(/<img src=x onerror=alert\(1\)>/),
  ).toBeVisible();
  expect(document.querySelector("img")).toBeNull();
  expect(screen.getByText(/合成本地测试数据不是验证证据/)).toBeVisible();
});

it("refreshes same-revision MCP transitions by event sequence and keeps honest empty history", async () => {
  let reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response(
        ++reads === 1
          ? { invocations: [], decisions: [] }
          : {
              invocations: [
                { ...call, status: "dispatching", phase: "call_started" },
              ],
              decisions: [],
            },
      ),
    ),
  );
  const { rerender } = render(
    <MCPInvocationActivity mission={mission} visible eventSequence={40} />,
  );
  expect(await screen.findByText("已读取，尚无 MCP 决策记录。")).toBeVisible();
  expect(screen.getByText("已读取，尚无 MCP 调用记录。")).toBeVisible();
  rerender(
    <MCPInvocationActivity mission={mission} visible eventSequence={41} />,
  );
  expect(await screen.findByText("正在调用本地测试工具")).toBeVisible();
  await waitFor(() => expect(reads).toBe(2));
});

it("reloads an expanded pending detail after same-revision completion", async () => {
  let historyReads = 0;
  let detailReads = 0;
  const pending = {
    ...call,
    status: "dispatching",
    phase: "call_started",
    result_digest: undefined,
    finished_at: undefined,
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/mcp-invocations")) {
        historyReads++;
        return response({
          invocations: [historyReads === 1 ? pending : call],
          decisions: [],
        });
      }
      if (path.endsWith("/mcp-invocations/call-1")) {
        detailReads++;
        return response({
          invocation:
            detailReads === 1
              ? pending
              : { ...call, result: { text: "sealed result" } },
        });
      }
      throw new Error(`unexpected ${path}`);
    }),
  );
  const { rerender } = render(
    <MCPInvocationActivity mission={mission} visible eventSequence={20} />,
  );
  await screen.findByText("正在调用本地测试工具");
  fireEvent.click(screen.getByRole("button", { name: /查看结果详情/ }));
  expect(await screen.findByText(/结果尚未封存/)).toBeVisible();
  rerender(
    <MCPInvocationActivity mission={mission} visible eventSequence={21} />,
  );
  expect(await screen.findByText(/sealed result/)).toBeVisible();
  expect(detailReads).toBe(2);
});

it("labels a terminal invocation without a sealed result instead of loading forever", async () => {
  const failed = {
    ...call,
    status: "failed",
    phase: "finished",
    result_digest: undefined,
    error_code: "tool_error",
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/mcp-invocations"))
        return response({ invocations: [failed], decisions: [] });
      if (path.endsWith("/mcp-invocations/call-1"))
        return response({ invocation: failed });
      throw new Error(`unexpected ${path}`);
    }),
  );
  render(
    <MCPInvocationActivity mission={mission} visible eventSequence={30} />,
  );
  await screen.findByText("调用失败");
  fireEvent.click(screen.getByRole("button", { name: /查看结果详情/ }));
  expect(await screen.findByText("调用已结束，没有封存结果。")).toBeVisible();
  expect(screen.queryByText("正在核对结果…")).not.toBeInTheDocument();
});
