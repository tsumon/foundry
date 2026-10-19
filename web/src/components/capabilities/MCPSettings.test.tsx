import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { CapabilitySettings } from "./CapabilitySettings";
import {
  catalogState,
  connection,
  deferred,
  emptyCapabilities,
  mockServer,
  operation,
  toolSnapshot,
} from "./testFixtures";
import { response } from "../../test/fixtures";

async function openMCP() {
  render(<CapabilitySettings enabled catalog={catalogState} />);
  fireEvent.click(screen.getByRole("button", { name: "工具与 MCP" }));
  await screen.findByRole("button", { name: "保存 MCP 配置" });
}

it("distinguishes controlled callable built-ins from closed MCP execution", async () => {
  mockServer({
    ...emptyCapabilities,
    builtin_capabilities: [
      {
        id: "repo.read",
        name: "Controlled repository read",
        description: "Researcher against the frozen manifest",
        agent_callable: true,
      },
      {
        id: "snapshot",
        name: "Snapshot",
        description: "Controller flow",
        agent_callable: false,
      },
    ],
  });
  await openMCP();
  expect(screen.getByText("仅在显式受控任务中按冻结权限可调用")).toBeVisible();
  expect(
    screen.getByText("Agent 不可调用（controller 固定流程）"),
  ).toBeVisible();
  expect(screen.getByText(/不表示 MCP tools\/call 已开放/)).toBeVisible();
  expect(screen.getByText(/通用 MCP tools\/call 仍关闭/)).toBeVisible();
  expect(
    await screen.findByRole("heading", {
      name: "本地合成测试服务当前不可用于新任务",
    }),
  ).toBeVisible();
});

it("shows owned local-test eligibility separately from discovered external connections", async () => {
  mockServer(emptyCapabilities, (path) =>
    path === "/api/mcp-local-test"
      ? response({
          enabled: true,
          eligible: true,
          test_only: true,
          endpoint: "http://127.0.0.1:43123/mcp",
          selection: {
            connection_id: "owned-local",
            connection_revision: 3,
            snapshot_id: "snapshot-3",
            snapshot_digest: "snapshot-digest",
            tools: [
              {
                name: "reference.lookup",
                definition_digest: "definition-digest",
                policy_digest: "policy-digest",
              },
            ],
          },
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
        })
      : undefined,
  );
  await openMCP();
  expect(
    await screen.findByRole("heading", {
      name: "本地合成测试服务可用于新任务",
    }),
  ).toBeVisible();
  expect(
    screen.getByText("reference.lookup", { selector: "code" }),
  ).toBeVisible();
  expect(screen.getByText(/通用 MCP tools\/call 仍关闭/)).toBeVisible();
});

it("displays lossless server JSON text without reparsing numbers or rendering markup", async () => {
  const input = '{"type":"object","minimum":9007199254740993,"maximum":1e400}';
  const output = '{"type":"object","const":9007199254740993,"maximum":1e400}';
  const annotations =
    '{"value":9007199254740993,"large":1e400,"text":"<img src=x onerror=alert(1)>"}';
  mockServer({
    ...emptyCapabilities,
    connections: [connection],
    snapshots: [
      {
        ...toolSnapshot,
        tools: [
          {
            ...toolSnapshot.tools[0],
            input_schema: JSON.parse(input),
            output_schema: JSON.parse(output),
            annotations: JSON.parse(annotations),
            input_schema_json: input,
            output_schema_json: output,
            annotations_json: annotations,
          },
        ],
      },
    ],
  });
  await openMCP();
  fireEvent.click(screen.getByText("文本定义"));
  for (const text of [input, output, annotations]) {
    expect(screen.getByText(text, { exact: true }).tagName).toBe("PRE");
  }
  expect(document.querySelector(".capability-tool img")).toBeNull();
});

it("does not fabricate lossless JSON from parsed values when server text is missing", async () => {
  mockServer({
    ...emptyCapabilities,
    connections: [connection],
    snapshots: [
      {
        ...toolSnapshot,
        tools: [
          {
            ...toolSnapshot.tools[0],
            input_schema: { type: "object", minimum: 9007199254740992 },
            output_schema: { type: "object", maximum: null },
            annotations: { value: 9007199254740992 },
            input_schema_json: undefined,
            output_schema_json: undefined,
            annotations_json: undefined,
          },
        ],
      },
    ],
  });
  await openMCP();
  fireEvent.click(screen.getByText("文本定义"));
  const details = screen.getByText("文本定义").closest("details")!;
  expect(details).not.toHaveTextContent("9007199254740992");
  expect(details).not.toHaveTextContent('"maximum": null');
  expect(
    within(details).getAllByText("原始 JSON 文本不可用；未从解析值重建。"),
  ).toHaveLength(3);
});

it("moves keyboard focus to confirmation review and restores its exact trigger on cancel without dispatch", async () => {
  const user = userEvent.setup();
  const fetcher = mockServer({
    ...emptyCapabilities,
    connections: [
      connection,
      { ...connection, id: "mcp-2", name: "第二个 MCP" },
    ],
  });
  await openMCP();
  const trigger = screen.getByRole("button", {
    name: "发现工具目录 第二个 MCP",
  });
  trigger.focus();
  await user.keyboard("{Enter}");
  expect(screen.getByRole("heading", { name: "确认 MCP 发现" })).toHaveFocus();
  await user.keyboard("{Enter}");
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
  await user.tab();
  expect(screen.getByRole("button", { name: "确认并发现" })).toHaveFocus();
  await user.tab();
  expect(screen.getByRole("button", { name: "取消发现" })).toHaveFocus();
  await user.keyboard("{Enter}");
  expect(
    screen.queryByRole("region", { name: "确认 MCP 发现" }),
  ).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
});

it("saves config without discovery, clears only the write-only secret and emits the exact CAS DTO", async () => {
  const storage = vi.spyOn(Storage.prototype, "setItem");
  const fetcher = mockServer(emptyCapabilities, (path) =>
    path === "/api/capabilities/mcp" ? response({ connection }) : undefined,
  );
  await openMCP();
  fireEvent.change(screen.getByLabelText("MCP 连接名称"), {
    target: { value: "本机发现 fixture" },
  });
  fireEvent.change(screen.getByLabelText("MCP 端点"), {
    target: { value: connection.endpoint },
  });
  fireEvent.change(screen.getByLabelText("MCP API Key（只写）"), {
    target: { value: "fake-r2-key" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存 MCP 配置" }));
  await waitFor(() =>
    expect(screen.getByLabelText("MCP API Key（只写）")).toHaveValue(""),
  );
  expect(screen.getByLabelText("MCP 连接名称")).toHaveValue("本机发现 fixture");
  const mutations = fetcher.mock.calls.filter(
    ([, init]) => init?.method === "POST",
  );
  expect(mutations.map(([path]) => path)).toEqual(["/api/capabilities/mcp"]);
  expect(JSON.parse(mutations[0][1]!.body as string)).toEqual({
    expected_revision: 0,
    name: "本机发现 fixture",
    endpoint: connection.endpoint,
    enabled: true,
    api_key: "fake-r2-key",
    clear_key: false,
  });
  expect(storage).not.toHaveBeenCalled();
});

it("requires explicit consent for saved endpoint discovery and blocks duplicate dispatch while busy", async () => {
  const pending = deferred<Response>();
  const fetcher = mockServer(
    { ...emptyCapabilities, connections: [connection] },
    (path) => (path.endsWith("/discover") ? pending.promise : undefined),
  );
  await openMCP();
  fireEvent.click(
    screen.getByRole("button", { name: "发现工具目录 本机发现 fixture" }),
  );
  const confirmation = screen.getByRole("region", { name: "确认 MCP 发现" });
  expect(confirmation).toHaveTextContent(connection.endpoint);
  expect(confirmation).toHaveTextContent("不发送任务源码");
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
  const consent = within(confirmation).getByRole("button", {
    name: "确认并发现",
  });
  fireEvent.click(consent);
  fireEvent.click(consent);
  expect(consent).toBeDisabled();
  const post = fetcher.mock.calls.filter(([path]) =>
    path.endsWith("/discover"),
  );
  expect(post).toHaveLength(1);
  const input = JSON.parse(post[0][1]!.body as string);
  expect(input).toEqual({
    expected_revision: 2,
    request_id: expect.any(String),
    consent: true,
  });
  await act(async () =>
    pending.resolve(
      response({ operation: { ...operation, id: input.request_id } }),
    ),
  );
  expect(await screen.findByText("结果未知")).toBeVisible();
});

it("reconciles an uncertain POST using only its original request ID and never automatically retries", async () => {
  let posted = "";
  const fetcher = mockServer(
    { ...emptyCapabilities, connections: [connection] },
    (path, init) => {
      if (path.endsWith("/discover")) {
        posted = JSON.parse(init!.body as string).request_id;
        return Promise.reject(new Error("transport"));
      }
      if (path.startsWith("/api/capabilities/discoveries/"))
        return response({ operation: { ...operation, id: posted } });
    },
  );
  await openMCP();
  fireEvent.click(
    screen.getByRole("button", { name: "发现工具目录 本机发现 fixture" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "确认并发现" }));
  expect(await screen.findByText("结果未知")).toBeVisible();
  expect(
    fetcher.mock.calls
      .filter(([path]) => path.includes("/discover"))
      .map(([path]) => path),
  ).toEqual([
    "/api/capabilities/mcp/mcp-1/discover",
    `/api/capabilities/discoveries/${posted}`,
  ]);
});

it("retains an unresolved original request for read-only manual reconciliation", async () => {
  let posted = "";
  let reconcile = false;
  const fetcher = mockServer(
    { ...emptyCapabilities, connections: [connection] },
    (path, init) => {
      if (path.endsWith("/discover")) {
        posted = JSON.parse(init!.body as string).request_id;
        return Promise.reject(new Error("transport"));
      }
      if (path.startsWith("/api/capabilities/discoveries/"))
        return reconcile
          ? response({ operation: { ...operation, id: posted } })
          : Promise.reject(new Error("offline"));
    },
  );
  await openMCP();
  fireEvent.click(
    screen.getByRole("button", { name: "发现工具目录 本机发现 fixture" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "确认并发现" }));
  const query = await screen.findByRole("button", { name: "查询本次结果" });
  expect(
    screen.getByRole("button", { name: "发现工具目录 本机发现 fixture" }),
  ).toBeDisabled();
  reconcile = true;
  fireEvent.click(query);
  expect(await screen.findByText("结果未知")).toBeVisible();
  expect(
    fetcher.mock.calls.filter(([path]) => path.endsWith("/discover")),
  ).toHaveLength(1);
  expect(
    fetcher.mock.calls
      .filter(([path]) => path.startsWith("/api/capabilities/discoveries/"))
      .map(([path]) => path),
  ).toEqual([
    `/api/capabilities/discoveries/${posted}`,
    `/api/capabilities/discoveries/${posted}`,
  ]);
});

it("separates stale last-known tools from the current config and unknown latest attempt counters", async () => {
  mockServer({
    ...emptyCapabilities,
    connections: [connection],
    snapshots: [toolSnapshot],
    discoveries: [operation],
  });
  await openMCP();
  expect(screen.getByText(/旧配置快照/)).toBeVisible();
  expect(screen.getByText(/当前配置 v2/)).toBeVisible();
  expect(screen.getByText(/请求次数：未知/)).toBeVisible();
  expect(screen.getByText(/读取字节：未知/)).toBeVisible();
  expect(screen.getByText(/2026-10-18T01:10:00Z/)).toBeVisible();
  fireEvent.click(screen.getByText("文本定义"));
  expect(screen.getByText("<img src=x onerror=alert(1)>")).toBeVisible();
  expect(document.querySelector("img")).toBeNull();
});

it("preserves nonsecret conflict drafts through refresh/subnav but clears keys when leaving MCP", async () => {
  mockServer({ ...emptyCapabilities, connections: [connection] }, (path) =>
    path === "/api/capabilities/mcp"
      ? response({ error: { code: "conflict", message: "unsafe detail" } }, 409)
      : undefined,
  );
  await openMCP();
  fireEvent.click(
    screen.getByRole("button", { name: "编辑 MCP 本机发现 fixture" }),
  );
  fireEvent.change(screen.getByLabelText("MCP 连接名称"), {
    target: { value: "保留的草稿" },
  });
  fireEvent.change(screen.getByLabelText("MCP API Key（只写）"), {
    target: { value: "fake-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存 MCP 配置" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("草稿已保留");
  fireEvent.click(screen.getByRole("button", { name: "重新读取能力设置" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "保存 MCP 配置" })).toBeEnabled(),
  );
  expect(screen.getByLabelText("MCP 连接名称")).toHaveValue("保留的草稿");
  fireEvent.click(screen.getByRole("button", { name: "Git" }));
  fireEvent.click(screen.getByRole("button", { name: "工具与 MCP" }));
  expect(screen.getByLabelText("MCP 连接名称")).toHaveValue("保留的草稿");
  expect(screen.getByLabelText("MCP API Key（只写）")).toHaveValue("");
  expect(document.body.textContent).not.toContain("unsafe detail");
});

it("keeps newer drafts after a disabled lifecycle's late successful save", async () => {
  const pending = deferred<Response>();
  mockServer(emptyCapabilities, (path) =>
    path === "/api/capabilities/mcp" ? pending.promise : undefined,
  );
  const view = render(<CapabilitySettings enabled catalog={catalogState} />);
  fireEvent.click(screen.getByRole("button", { name: "工具与 MCP" }));
  await screen.findByRole("button", { name: "保存 MCP 配置" });
  fireEvent.change(screen.getByLabelText("MCP 连接名称"), {
    target: { value: "旧草稿" },
  });
  fireEvent.change(screen.getByLabelText("MCP 端点"), {
    target: { value: connection.endpoint },
  });
  fireEvent.change(screen.getByLabelText("MCP API Key（只写）"), {
    target: { value: "fake-old-key" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存 MCP 配置" }));
  view.rerender(<CapabilitySettings enabled={false} catalog={catalogState} />);
  expect(screen.getByLabelText("MCP API Key（只写）")).toHaveValue("");
  view.rerender(<CapabilitySettings enabled catalog={catalogState} />);
  await waitFor(() =>
    expect(screen.getByLabelText("MCP 连接名称")).toBeEnabled(),
  );
  fireEvent.change(screen.getByLabelText("MCP 连接名称"), {
    target: { value: "新会话草稿" },
  });
  await act(async () => pending.resolve(response({ connection })));
  expect(screen.getByLabelText("MCP 连接名称")).toHaveValue("新会话草稿");
  expect(
    screen.queryByRole("button", { name: "编辑 MCP 本机发现 fixture" }),
  ).not.toBeInTheDocument();
});

it("sends explicit clear-key with endpoint updates and does not echo unknown response fields", async () => {
  const fetcher = mockServer(
    {
      ...emptyCapabilities,
      connections: [{ ...connection, api_key: "fake-server-secret" }],
    },
    (path) =>
      path === "/api/capabilities/mcp" ? response({ connection }) : undefined,
  );
  await openMCP();
  fireEvent.click(
    screen.getByRole("button", { name: "编辑 MCP 本机发现 fixture" }),
  );
  fireEvent.change(screen.getByLabelText("MCP 端点"), {
    target: { value: "http://127.0.0.1:7777/mcp" },
  });
  fireEvent.change(screen.getByLabelText("MCP API Key（只写）"), {
    target: { value: "fake-discarded" },
  });
  fireEvent.click(screen.getByLabelText("明确清空凭据"));
  expect(screen.getByLabelText("MCP API Key（只写）")).toHaveValue("");
  expect(screen.getByLabelText("MCP API Key（只写）")).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "保存 MCP 配置" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "保存 MCP 配置" })).toBeEnabled(),
  );
  const call = fetcher.mock.calls.find(
    ([path]) => path === "/api/capabilities/mcp",
  )!;
  expect(JSON.parse(call[1]!.body as string)).toEqual({
    id: "mcp-1",
    expected_revision: 2,
    name: connection.name,
    endpoint: "http://127.0.0.1:7777/mcp",
    enabled: true,
    clear_key: true,
  });
  expect(document.body.textContent).not.toContain("fake-server-secret");
});

it("keeps the last valid snapshot after a failed discovery and treats an empty successful snapshot as real", async () => {
  let attempts = 0;
  mockServer(
    {
      ...emptyCapabilities,
      connections: [connection],
      snapshots: [toolSnapshot],
    },
    (path, init) => {
      if (!path.endsWith("/discover")) return;
      attempts++;
      const requestID = JSON.parse(init!.body as string).request_id;
      return response(
        attempts === 1
          ? {
              operation: {
                ...operation,
                id: requestID,
                status: "failed",
                error_code: "invalid_response",
                requests: 2,
                response_bytes: 20,
              },
            }
          : {
              operation: {
                ...operation,
                id: requestID,
                status: "succeeded",
                error_code: "",
                snapshot_id: "new-empty",
                requests: 4,
                response_bytes: 100,
              },
              snapshot: {
                ...toolSnapshot,
                id: "new-empty",
                connection_revision: 2,
                tools: [],
              },
            },
      );
    },
  );
  await openMCP();
  fireEvent.click(
    screen.getByRole("button", { name: "发现工具目录 本机发现 fixture" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "确认并发现" }));
  expect(await screen.findByText("发现失败")).toBeVisible();
  expect(screen.getByText("文本定义")).toBeVisible();
  fireEvent.click(
    screen.getByRole("button", { name: "发现工具目录 本机发现 fixture" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "确认并发现" }));
  expect(await screen.findByText(/返回 0 个工具/)).toBeVisible();
  expect(screen.queryByText("文本定义")).not.toBeInTheDocument();
});

it("does not replace a newer last-known snapshot with an older request reconciliation", async () => {
  const reconciled = deferred<Response>();
  let reads = 0;
  const newerSnapshot = {
    ...toolSnapshot,
    id: "newer-snapshot",
    connection_revision: 2,
    observed_at: "2026-10-18T05:00:00Z",
    server_name: "NEWER SERVER",
  };
  const newerOperation = {
    ...operation,
    id: "newer-request",
    status: "succeeded",
    requests: 4,
    response_bytes: 100,
    created_at: "2026-10-18T05:00:00Z",
    finished_at: "2026-10-18T05:00:01Z",
    snapshot_id: newerSnapshot.id,
  };
  mockServer({ ...emptyCapabilities, connections: [connection] }, (path) => {
    if (path === "/api/capabilities" && ++reads > 1)
      return response({
        ...emptyCapabilities,
        connections: [connection],
        snapshots: [newerSnapshot],
        discoveries: [newerOperation],
      });
    if (path.endsWith("/discover"))
      return Promise.reject(new Error("uncertain"));
    if (path.startsWith("/api/capabilities/discoveries/"))
      return reconciled.promise;
  });
  await openMCP();
  fireEvent.click(
    screen.getByRole("button", { name: "发现工具目录 本机发现 fixture" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "确认并发现" }));
  fireEvent.click(screen.getByRole("button", { name: "重新读取能力设置" }));
  expect(await screen.findByText(/NEWER SERVER/)).toBeVisible();
  await act(async () =>
    reconciled.resolve(
      response({
        operation: {
          ...operation,
          status: "succeeded",
          snapshot_id: toolSnapshot.id,
        },
        snapshot: toolSnapshot,
      }),
    ),
  );
  expect(screen.getByText(/NEWER SERVER/)).toBeVisible();
  expect(screen.getByText(/请求次数：4/)).toBeVisible();
});

it("does not roll back a newer config read when an older save response arrives later", async () => {
  const saved = deferred<Response>();
  let reads = 0;
  const newer = { ...connection, revision: 3, name: "更新配置 v3" };
  mockServer({ ...emptyCapabilities, connections: [connection] }, (path) => {
    if (path === "/api/capabilities" && ++reads > 1)
      return response({ ...emptyCapabilities, connections: [newer] });
    if (path === "/api/capabilities/mcp") return saved.promise;
  });
  await openMCP();
  fireEvent.click(
    screen.getByRole("button", { name: "编辑 MCP 本机发现 fixture" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "保存 MCP 配置" }));
  fireEvent.click(screen.getByRole("button", { name: "重新读取能力设置" }));
  expect(
    await screen.findByRole("button", { name: "编辑 MCP 更新配置 v3" }),
  ).toBeDisabled();
  await act(async () => saved.resolve(response({ connection })));
  expect(
    screen.getByRole("button", { name: "编辑 MCP 更新配置 v3" }),
  ).toBeEnabled();
});

it("orders variable-precision UTC discovery timestamps chronologically, including submillisecond records", async () => {
  mockServer({
    ...emptyCapabilities,
    connections: [connection],
    discoveries: [
      {
        ...operation,
        id: "whole-second",
        status: "failed",
        created_at: "2026-10-18T03:00:00Z",
      },
      {
        ...operation,
        id: "one-nanosecond-later",
        status: "outcome_unknown",
        created_at: "2026-10-18T03:00:00.000000001Z",
      },
    ],
  });
  await openMCP();
  expect(screen.getByText("结果未知")).toBeVisible();
  expect(screen.queryByText("发现失败")).not.toBeInTheDocument();
});
