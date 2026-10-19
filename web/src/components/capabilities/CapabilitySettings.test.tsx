import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it } from "vitest";
import userEvent from "@testing-library/user-event";
import { CapabilitySettings } from "./CapabilitySettings";
import {
  catalogState,
  connection,
  deferred,
  emptyCapabilities,
  mockServer,
} from "./testFixtures";
import { response } from "../../test/fixtures";

it("gates all settings reads on session readiness and lazily reads capabilities without another session/catalog", async () => {
  const fetcher = mockServer();
  const { rerender } = render(
    <CapabilitySettings enabled={false} catalog={catalogState} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "工具与 MCP" }));
  expect(fetcher).not.toHaveBeenCalled();
  expect(screen.queryByText("尚未登记 MCP 连接")).not.toBeInTheDocument();
  rerender(<CapabilitySettings enabled catalog={catalogState} />);
  expect(await screen.findByText("尚未登记 MCP 连接")).toBeVisible();
  await waitFor(() =>
    expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
      "/api/capabilities",
      "/api/mcp-local-test",
    ]),
  );
  fireEvent.click(screen.getByRole("button", { name: "Skills" }));
  expect(screen.getByText("尚未登记 Skill")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Git" }));
  expect(screen.getByText("尚未登记 Git 仓库")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "执行工具" }),
  ).not.toBeInTheDocument();
});

it("keeps original model settings as the default without requesting capabilities", async () => {
  const fetcher = mockServer();
  render(<CapabilitySettings enabled catalog={catalogState} />);
  expect(
    await screen.findByRole("heading", { name: "模型与连接" }),
  ).toBeVisible();
  await waitFor(() => expect(screen.getByLabelText("连接名称")).toBeEnabled());
  expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
    "/api/model-routes",
    "/api/model-accounts",
    "/api/profiles",
    "/api/role-model-policy",
  ]);
});

it("shows unavailable data rather than fake empty states and explicitly retries the read", async () => {
  let fail = true;
  mockServer(emptyCapabilities, (path) =>
    path === "/api/capabilities" && fail
      ? Promise.reject(new Error("raw secret-bearing system error"))
      : undefined,
  );
  render(<CapabilitySettings enabled catalog={catalogState} />);
  fireEvent.click(screen.getByRole("button", { name: "工具与 MCP" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("请求未完成");
  expect(document.body.textContent).not.toContain("raw secret");
  expect(screen.queryByText("尚未登记 MCP 连接")).not.toBeInTheDocument();
  fail = false;
  fireEvent.click(screen.getByRole("button", { name: "重新读取能力设置" }));
  expect(await screen.findByText("尚未登记 MCP 连接")).toBeVisible();
});

it("ignores a disabled epoch's late snapshot", async () => {
  const old = deferred<Response>();
  let reads = 0;
  mockServer(emptyCapabilities, (path) =>
    path === "/api/capabilities" && ++reads === 1 ? old.promise : undefined,
  );
  const view = render(<CapabilitySettings enabled catalog={catalogState} />);
  fireEvent.click(screen.getByRole("button", { name: "工具与 MCP" }));
  view.rerender(<CapabilitySettings enabled={false} catalog={catalogState} />);
  view.rerender(<CapabilitySettings enabled catalog={catalogState} />);
  expect(await screen.findByText("尚未登记 MCP 连接")).toBeVisible();
  await act(async () =>
    old.resolve(
      response({
        ...emptyCapabilities,
        connections: [{ id: "late", name: "旧请求不应出现" }],
      }),
    ),
  );
  expect(screen.queryByText("旧请求不应出现")).not.toBeInTheDocument();
});

it("does not lose a successful save when a settings refresh was requested while it was pending", async () => {
  const save = deferred<Response>();
  const refresh = deferred<Response>();
  let reads = 0;
  mockServer(emptyCapabilities, (path) => {
    if (path === "/api/capabilities/mcp") return save.promise;
    if (path === "/api/capabilities" && ++reads > 1) return refresh.promise;
  });
  render(<CapabilitySettings enabled catalog={catalogState} />);
  fireEvent.click(screen.getByRole("button", { name: "工具与 MCP" }));
  await screen.findByRole("button", { name: "保存 MCP 配置" });
  fireEvent.change(screen.getByLabelText("MCP 连接名称"), {
    target: { value: connection.name },
  });
  fireEvent.change(screen.getByLabelText("MCP 端点"), {
    target: { value: connection.endpoint },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存 MCP 配置" }));
  fireEvent.click(screen.getByRole("button", { name: "重新读取能力设置" }));
  await act(async () => save.resolve(response({ connection })));
  await act(async () => refresh.resolve(response(emptyCapabilities)));
  expect(
    await screen.findByRole("button", { name: "编辑 MCP 本机发现 fixture" }),
  ).toBeVisible();
  expect(screen.queryByText("尚未登记 MCP 连接")).not.toBeInTheDocument();
});

it("supports keyboard-only subnavigation without reading capabilities until activation", async () => {
  const user = userEvent.setup();
  const fetcher = mockServer();
  render(<CapabilitySettings enabled catalog={catalogState} />);
  await screen.findByRole("heading", { name: "模型与连接" });
  screen.getByRole("button", { name: "模型" }).focus();
  await user.tab();
  expect(screen.getByRole("button", { name: "工具与 MCP" })).toHaveFocus();
  expect(
    fetcher.mock.calls.some(([path]) => path === "/api/capabilities"),
  ).toBe(false);
  await user.keyboard("{Enter}");
  expect(await screen.findByText("尚未登记 MCP 连接")).toBeVisible();
  expect(screen.getByRole("button", { name: "工具与 MCP" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
});

it("opens event hooks through an independent lazy route without changing the model default or reading capabilities", async () => {
  const hookSnapshot = {
    hook_points: [],
    rules: [],
    processor: {
      initialized_after_seq: 0,
      cursor: 0,
      latest_event_seq: 0,
      pending_events: 0,
      last_processed_at: "",
    },
    runtime: {
      state: "not_started",
      last_attempt_at: "",
      last_success_at: "",
      error_code: "",
    },
  };
  const fetcher = mockServer(emptyCapabilities, (path) => {
    if (path === "/api/hooks") return response(hookSnapshot);
    if (path === "/api/hooks/records?limit=50")
      return response({
        records: [],
        next_before: 0,
        processor: hookSnapshot.processor,
        runtime: hookSnapshot.runtime,
      });
  });
  render(<CapabilitySettings enabled catalog={catalogState} />);
  expect(
    await screen.findByRole("heading", { name: "模型与连接" }),
  ).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "事件钩子" }));
  expect(
    await screen.findByRole("heading", { name: "事件钩子" }),
  ).toBeVisible();
  await waitFor(() =>
    expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
      "/api/model-routes",
      "/api/model-accounts",
      "/api/profiles",
      "/api/role-model-policy",
      "/api/hooks",
      "/api/hooks/records?limit=50",
    ]),
  );
  expect(fetcher.mock.calls.map(([path]) => path)).not.toContain(
    "/api/capabilities",
  );
  expect(
    screen.queryByRole("button", { name: "运行脚本" }),
  ).not.toBeInTheDocument();
});
