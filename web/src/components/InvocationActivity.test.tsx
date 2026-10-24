import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { mission, response } from "../test/fixtures";
import { InvocationActivity } from "./InvocationActivity";

const invocation = {
  id: "inv-1",
  mission_id: mission.id,
  epoch: 1,
  session_id: "session-long",
  ordinal: 2,
  kind: "model",
  role: "researcher",
  name: "model.complete",
  binding_digest: "a".repeat(64),
  authority_digest: "b".repeat(64),
  input: {
    schema_version: 1,
    kind: "model_input",
    invocation_id: "inv-1",
    mission_id: mission.id,
    epoch: 1,
    producer: "researcher",
    captured_at: "now",
    digest: "c".repeat(64),
    bytes: 20,
    content_type: "application/json",
    binding_digest: "a".repeat(64),
    truncated: false,
  },
  result: {
    schema_version: 1,
    kind: "model_output",
    invocation_id: "inv-1",
    mission_id: mission.id,
    epoch: 1,
    producer: "researcher",
    captured_at: "now",
    digest: "d".repeat(64),
    bytes: 20,
    content_type: "application/json",
    binding_digest: "a".repeat(64),
    truncated: false,
  },
  status: "succeeded",
  reserved_result_bytes: 0,
  created_at: "now",
  finished_at: "later",
  usage_known: false,
  input_tokens: 0,
  output_tokens: 0,
};

it.each([
  [undefined, "未记录分阶段诊断"],
  [
    {
      version: 1,
      stage: "stream_read",
      reason: "missing_terminal",
      media_type: "sse",
      events_seen: 2,
      bytes_read: 180,
    },
    "流结束时未收到终止事件",
  ],
  [
    {
      version: 1,
      stage: "provider_terminal",
      reason: "provider_incomplete",
      media_type: "sse",
      terminal_event: "response.incomplete",
      events_seen: 2,
      bytes_read: 180,
    },
    "已收到提供方 incomplete 事件",
  ],
  [
    {
      version: 1,
      stage: "completed_response",
      reason: "invalid_output",
      media_type: "sse",
      terminal_event: "response.completed",
      events_seen: 2,
      bytes_read: 180,
    },
    "完成响应的消息类型、角色或状态未通过校验",
  ],
  [
    {
      version: 1,
      stage: "stream_read",
      reason: "PRIVATE",
      media_type: "sse",
      events_seen: 1,
      bytes_read: 40,
    },
    "分阶段诊断不可验证",
  ],
])(
  "renders read-only diagnostic observations without guessing or replaying %j",
  async (diagnostic, expected) => {
    const fetcher = vi.fn(async () =>
      response({
        invocations: [
          {
            ...invocation,
            status: "outcome_unknown",
            result: undefined,
            error_code: "response_incomplete",
            model_receipt: {
              http_status: 200,
              certainty: "unknown",
              response_diagnostic: diagnostic,
            },
          },
        ],
      }),
    );
    vi.stubGlobal("fetch", fetcher);
    render(<InvocationActivity mission={mission} visible />);
    expect(await screen.findByText(new RegExp(expected))).toBeVisible();
    expect(screen.getByText(/预算已消耗，不会自动重试/)).toBeVisible();
    expect(document.body.textContent).not.toContain("PRIVATE");
    expect(
      screen.queryByRole("button", { name: /重试|重新调用|切换|批准/ }),
    ).toBeNull();
    expect(fetcher).toHaveBeenCalledTimes(1);
  },
);

it("does not read while hidden, then lists metadata and lazily renders escaped detail text", async () => {
  const fetcher = vi.fn(async (path: string) => {
    if (path === "/api/missions/m1/invocations?before=0&limit=30")
      return response({ invocations: [invocation] });
    if (path === "/api/missions/m1/invocations/inv-1")
      return response({
        invocation: {
          invocation,
          input_preview: "<img src=x onerror=alert(1)>",
          result_preview: "sealed",
          input_truncated: false,
          result_truncated: false,
        },
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const { rerender } = render(
    <InvocationActivity mission={mission} visible={false} />,
  );
  expect(fetcher).not.toHaveBeenCalled();
  rerender(<InvocationActivity mission={mission} visible />);
  expect(await screen.findByText("模型输出已封存")).toBeVisible();
  expect(screen.getByText(/用量未知/)).toBeVisible();
  expect(fetcher).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: /查看调用内容/ }));
  expect(await screen.findByText("<img src=x onerror=alert(1)>")).toBeVisible();
  expect(document.querySelector("img")).toBeNull();
  expect(fetcher).toHaveBeenCalledTimes(2);
  fireEvent.click(screen.getByRole("button", { name: /收起调用内容/ }));
  fireEvent.click(screen.getByRole("button", { name: /查看调用内容/ }));
  expect(fetcher).toHaveBeenCalledTimes(2);
});

it("retains last good rows when a refresh fails and keeps unknown/interrupted statuses distinct", async () => {
  const second = {
    ...invocation,
    id: "inv-2",
    ordinal: 1,
    status: "interrupted_before_dispatch",
    result: undefined,
  };
  let calls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      ++calls === 1
        ? response({ invocations: [second] })
        : response({ error: { message: "offline" } }, 500),
    ),
  );
  const { rerender } = render(<InvocationActivity mission={mission} visible />);
  expect(await screen.findByText("派发前中断")).toBeVisible();
  rerender(
    <InvocationActivity
      mission={{ ...mission, revision: mission.revision + 1 }}
      visible
    />,
  );
  await waitFor(() =>
    expect(screen.getByRole("alert")).toHaveTextContent(/保留上次数据/),
  );
  expect(screen.getByText("派发前中断")).toBeVisible();
});

it("runs a trailing newest-page refresh when the final revision arrives during an in-flight read", async () => {
  const pending = { ...invocation, status: "dispatching", result: undefined };
  let finishFirst!: (value: Response) => void;
  let listCalls = 0;
  const fetcher = vi.fn(async (path: string) => {
    if (!path.endsWith("/invocations?before=0&limit=30"))
      throw new Error(`unexpected ${path}`);
    listCalls++;
    if (listCalls === 1)
      return new Promise<Response>((resolve) => {
        finishFirst = resolve;
      });
    return response({ invocations: [invocation] });
  });
  vi.stubGlobal("fetch", fetcher);
  const { rerender } = render(<InvocationActivity mission={mission} visible />);
  await waitFor(() => expect(listCalls).toBe(1));
  rerender(
    <InvocationActivity
      mission={{ ...mission, revision: mission.revision + 1 }}
      visible
    />,
  );
  expect(listCalls).toBe(1);
  finishFirst(response({ invocations: [pending] }));
  expect(await screen.findByText("模型输出已封存")).toBeVisible();
  expect(listCalls).toBe(2);
});

it("refreshes a same-revision claim transition when its invocation event sequence advances", async () => {
  const pending = {
    ...invocation,
    status: "authorized_pending",
    result: undefined,
  };
  const dispatching = { ...pending, status: "dispatching" };
  let listCalls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({ invocations: [++listCalls === 1 ? pending : dispatching] }),
    ),
  );
  const { rerender } = render(
    <InvocationActivity mission={mission} visible eventSequence={40} />,
  );
  expect(await screen.findByText("已授权，等待派发")).toBeVisible();
  rerender(<InvocationActivity mission={mission} visible eventSequence={41} />);
  expect(await screen.findByText("正在派发")).toBeVisible();
  expect(listCalls).toBe(2);
});

it("refetches an expanded pending detail when refreshed metadata seals a result", async () => {
  const pending = { ...invocation, status: "dispatching", result: undefined };
  let listCalls = 0;
  let detailCalls = 0;
  const fetcher = vi.fn(async (path: string) => {
    if (path.endsWith("/invocations?before=0&limit=30"))
      return response({
        invocations: [++listCalls === 1 ? pending : invocation],
      });
    if (path.endsWith("/invocations/inv-1")) {
      detailCalls++;
      return response({
        invocation: {
          invocation: detailCalls === 1 ? pending : invocation,
          input_preview: "input",
          ...(detailCalls === 1 ? {} : { result_preview: "sealed-result" }),
          input_truncated: false,
          result_truncated: false,
        },
      });
    }
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const { rerender } = render(<InvocationActivity mission={mission} visible />);
  fireEvent.click(await screen.findByRole("button", { name: /查看调用内容/ }));
  expect(await screen.findByText("没有封存结果正文。")).toBeVisible();
  rerender(
    <InvocationActivity
      mission={{ ...mission, revision: mission.revision + 1 }}
      visible
    />,
  );
  expect(await screen.findByText("sealed-result")).toBeVisible();
  expect(detailCalls).toBe(2);
});

it("discards a late list response after switching missions", async () => {
  let finishFirst!: (value: Response) => void;
  const secondMission = { ...mission, id: "m2", revision: 1 };
  const secondInvocation = {
    ...invocation,
    id: "inv-m2",
    mission_id: "m2",
    name: "repo.search",
    input: {
      ...invocation.input,
      invocation_id: "inv-m2",
      mission_id: "m2",
    },
    result: undefined,
    status: "outcome_unknown",
  };
  const fetcher = vi.fn(async (path: string) => {
    if (path.startsWith("/api/missions/m1/"))
      return new Promise<Response>((resolve) => {
        finishFirst = resolve;
      });
    if (path.startsWith("/api/missions/m2/"))
      return response({ invocations: [secondInvocation] });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const { rerender } = render(<InvocationActivity mission={mission} visible />);
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  rerender(<InvocationActivity mission={secondMission} visible />);
  expect(await screen.findByText("结果未知")).toBeVisible();
  finishFirst(response({ invocations: [invocation] }));
  await waitFor(() => expect(screen.queryByText("模型输出已封存")).toBeNull());
  expect(screen.getByText(/repo.search/)).toBeVisible();
});

it("pages from the oldest ordinal and de-duplicates stable invocation IDs", async () => {
  const page = Array.from({ length: 30 }, (_, index) => ({
    ...invocation,
    id: `inv-${60 - index}`,
    ordinal: 60 - index,
    input: { ...invocation.input, invocation_id: `inv-${60 - index}` },
    result: undefined,
  }));
  const fetcher = vi.fn(async (path: string) => {
    if (path.endsWith("?before=0&limit=30"))
      return response({ invocations: page });
    if (path.endsWith("?before=31&limit=30"))
      return response({
        invocations: [
          page[29],
          {
            ...invocation,
            id: "inv-30",
            ordinal: 30,
            result: undefined,
          },
        ],
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<InvocationActivity mission={mission} visible />);
  fireEvent.click(await screen.findByRole("button", { name: "读取更早记录" }));
  expect(
    await screen.findByText("#30 · model · researcher · model.complete"),
  ).toBeVisible();
  expect(fetcher.mock.calls.map(([path]) => path)).toContain(
    "/api/missions/m1/invocations?before=31&limit=30",
  );
  expect(
    screen.getAllByText("#31 · model · researcher · model.complete"),
  ).toHaveLength(1);
});

it("renders typed model receipts without inventing plan quota or a retry/fallback action", async () => {
  const quota = {
    ...invocation,
    id: "inv-quota",
    ordinal: 4,
    status: "failed",
    error_code: "quota_exhausted",
    result: undefined,
    model_receipt: {
      request_id: "req-bounded",
      resolved_model: "model-resolved",
      credential_generation: 7,
      http_status: 403,
      provider_code: "subscription_sharing_usage_limit_exceeded",
      provider_param: "model",
      body_shape: "error_envelope",
      retry_after_seconds: 12,
      certainty: "rejected",
    },
  };
  const unknown = {
    ...invocation,
    id: "inv-unknown",
    ordinal: 3,
    status: "outcome_unknown",
    error_code: "transport_unknown",
    result: undefined,
    model_receipt: {
      request_id: "req-unknown",
      certainty: "unknown",
    },
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response({ invocations: [quota, unknown] })),
  );
  render(<InvocationActivity mission={mission} visible />);
  expect(await screen.findByText("当前路由额度受限")).toBeVisible();
  expect(screen.getByText(/不代表整个套餐余额或重置时间/)).toBeVisible();
  expect(screen.getByText(/已拒绝/)).toBeVisible();
  expect(screen.getByText(/req-bounded/)).toBeVisible();
  expect(screen.getByText(/model-resolved/)).toBeVisible();
  expect(screen.getByText(/凭据代数 7/)).toBeVisible();
  expect(screen.getByText(/HTTP 403/)).toBeVisible();
  expect(
    screen.getByText(/subscription_sharing_usage_limit_exceeded/),
  ).toBeVisible();
  expect(screen.getByText(/结果未知；预算已消耗，不会自动重试/)).toBeVisible();
  expect(screen.queryByRole("button", { name: /付费|重试|切换/ })).toBeNull();
  expect(document.body.textContent).not.toContain("套餐已耗尽");
});
