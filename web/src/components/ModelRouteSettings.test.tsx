import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { response } from "../test/fixtures";
import { ModelRouteSettings } from "./ModelRouteSettings";

const account = {
  id: "account-openai",
  revision: 4,
  name: "OpenAI API",
  provider: "openai",
  base_url: "https://api.openai.com/v1",
  auth_method: "api_key" as const,
  billing_mode: "metered_api" as const,
  enabled: true,
  current_credential_version: 2,
  created_at: "2026-10-19T00:00:00Z",
  updated_at: "2026-10-19T00:00:00Z",
  identity_status: "unverified" as const,
  usage_status: "unknown" as const,
  inference_enabled: false as const,
};

const runtime = {
  live_authorization: false,
  live_discovery: false,
  live_inference: false,
  simulation_inference: false,
  simulation_authorization: false,
  automatic_paid_fallback: false,
  adapters: [
    {
      id: "openai_api_chat",
      provider: "openai",
      auth_method: "api_key",
      protocol: "chat_completions",
      spend_class: "explicit_api",
      implemented: true,
      live_enabled: false,
    },
    {
      id: "openai_api_responses",
      provider: "openai",
      auth_method: "api_key",
      protocol: "responses",
      spend_class: "explicit_api",
      implemented: true,
      live_enabled: false,
    },
  ],
};

it("reads route metadata only and keeps every production live control visibly closed", async () => {
  const storage = vi.spyOn(Storage.prototype, "setItem");
  const fetcher = vi.fn(async (_path: string) =>
    response({ routes: [], runtime }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<ModelRouteSettings accounts={[account]} onSnapshot={vi.fn()} />);
  expect(await screen.findByText("生产真实接入关闭")).toBeVisible();
  expect(
    screen.getByRole("button", { name: "OpenAI 真实登录（未开放）" }),
  ).toBeDisabled();
  expect(
    screen.getByText(/Claude 订阅与智谱 Coding Plan 未开放/),
  ).toBeVisible();
  expect(screen.getByText(/本进程未启用本地模拟推理/)).toBeVisible();
  expect(screen.getByText(/不会自动切换到付费 API/)).toBeVisible();
  expect(
    screen.getByText(/全局账户路由.*账户级订阅连接另由独立验收审批处理/),
  ).toBeVisible();
  expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
    "/api/model-routes",
  ]);
  expect(storage).not.toHaveBeenCalled();
});

it("creates an immutable route with the exact account revision and explicit usage declaration", async () => {
  const created = {
    id: "route-created",
    account_id: account.id,
    adapter: "openai_api_responses",
    protocol: "responses",
    base_url: account.base_url,
    environment: "live",
    spend_class: "explicit_api",
    extra_usage: "off",
    created_at: "2026-10-19T01:00:00Z",
  };
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) =>
    init?.method === "POST"
      ? response({ route: created })
      : response({ routes: [], runtime }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<ModelRouteSettings accounts={[account]} onSnapshot={vi.fn()} />);
  await screen.findByText("尚未创建模型路由");
  fireEvent.change(screen.getByLabelText("路由账户"), {
    target: { value: account.id },
  });
  fireEvent.change(screen.getByLabelText("适配器"), {
    target: { value: "openai_api_responses" },
  });
  expect(screen.getByText(account.base_url)).toBeVisible();
  expect(screen.getByText(/按量 API（用户声明）/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "创建不可变路由" }));
  await screen.findByText("route-created");
  const post = fetcher.mock.calls.find(([, init]) => init?.method === "POST")!;
  expect(post[0]).toBe("/api/model-routes");
  expect(JSON.parse(String(post[1]?.body))).toEqual({
    account_id: account.id,
    expected_account_revision: 4,
    adapter: "openai_api_responses",
    environment: "live",
    extra_usage: "off",
  });
});

it("treats a failed read as unknown and ignores a late response after unmount", async () => {
  let resolve!: (value: Response) => void;
  const pending = new Promise<Response>((done) => {
    resolve = done;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(() => pending),
  );
  const view = render(
    <ModelRouteSettings accounts={[account]} onSnapshot={vi.fn()} />,
  );
  expect(screen.getByText(/路由与运行状态尚未读取/)).toBeVisible();
  view.unmount();
  await act(async () => resolve(response({ routes: [], runtime })));
});

it("locks route mutations while the original request outcome is pending", async () => {
  let finish!: (value: Response) => void;
  const pending = new Promise<Response>((done) => {
    finish = done;
  });
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) =>
    init?.method === "POST" ? pending : response({ routes: [], runtime }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<ModelRouteSettings accounts={[account]} onSnapshot={vi.fn()} />);
  await screen.findByText("尚未创建模型路由");
  fireEvent.change(screen.getByLabelText("路由账户"), {
    target: { value: account.id },
  });
  fireEvent.change(screen.getByLabelText("适配器"), {
    target: { value: "openai_api_chat" },
  });
  const submit = screen.getByRole("button", { name: "创建不可变路由" });
  fireEvent.click(submit);
  fireEvent.click(submit);
  await waitFor(() => expect(submit).toBeDisabled());
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
  await act(async () =>
    finish(
      response({
        route: {
          id: "route-one",
          account_id: account.id,
          adapter: "openai_api_chat",
          protocol: "chat_completions",
          base_url: account.base_url,
          environment: "live",
          spend_class: "explicit_api",
          extra_usage: "off",
          created_at: "now",
        },
      }),
    ),
  );
});
