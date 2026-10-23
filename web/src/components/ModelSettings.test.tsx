import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { vi, it, expect } from "vitest";
import { ModelSettings } from "./ModelSettings";
import { profile as savedProfile, response } from "../test/fixtures";
const profile = { ...savedProfile, has_key: false };
const stubProfileFetch = (
  fetcher: (path: string, init?: RequestInit) => unknown,
) =>
  vi.stubGlobal("fetch", (path: string, init?: RequestInit) =>
    path === "/api/model-accounts"
      ? Promise.resolve(response({ accounts: [] }))
      : path === "/api/model-routes"
        ? Promise.resolve(
            response({
              routes: [],
              runtime: {
                live_authorization: false,
                live_discovery: false,
                live_inference: false,
                simulation_inference: false,
                simulation_authorization: false,
                automatic_paid_fallback: false,
                adapters: [],
              },
            }),
          )
        : path === "/api/role-model-policy" && !init?.method
          ? Promise.resolve(
              response({
                state: {
                  policy: {
                    revision: 0,
                    created_at: "",
                    roles: Object.fromEntries(
                      [
                        "main",
                        "researcher",
                        "planner",
                        "worker",
                        "debugger",
                        "qa",
                        "auditor",
                      ].map((role) => [
                        role,
                        {
                          candidates: [],
                          required_capabilities: [],
                          allowed_spend_classes: [],
                          preference: "candidate_order",
                        },
                      ]),
                    ),
                  },
                  profiles_digest: "0".repeat(64),
                },
              }),
            )
          : fetcher(path, init),
  );

it("labels live coding account, profile, and role setup as approval-bound without granting authority", async () => {
  const account = {
    id: "coding-account",
    revision: 1,
    name: "Coding API account",
    provider: "openai",
    base_url: "https://api.openai.com/v1",
    auth_method: "api_key",
    billing_mode: "api_credits",
    enabled: true,
    current_credential_version: 1,
    created_at: "2026-10-23T00:00:00Z",
    updated_at: "2026-10-23T00:00:00Z",
    identity_status: "unverified",
    usage_status: "unknown",
    inference_enabled: false,
  };
  const route = {
    id: "coding-route",
    account_id: account.id,
    adapter: "openai_api_responses",
    protocol: "responses",
    base_url: account.base_url,
    environment: "live",
    spend_class: "explicit_api",
    extra_usage: "off",
    created_at: "2026-10-23T00:00:00Z",
  };
  const bound = {
    ...profile,
    id: "coding-profile",
    name: "Coding profile",
    account_id: account.id,
    route_id: route.id,
    account,
  };
  const fetcher = vi.fn(async (path: string) => {
    if (path === "/api/profiles")
      return response({
        profiles: [bound],
        settings: {
          default_profile_id: bound.id,
          role_bindings: {},
        },
      });
    if (path === "/api/model-accounts")
      return response({ accounts: [account] });
    if (path === "/api/model-routes")
      return response({
        routes: [route],
        runtime: {
          live_authorization: false,
          live_discovery: false,
          live_inference: true,
          simulation_inference: false,
          simulation_authorization: false,
          automatic_paid_fallback: false,
          adapters: [
            {
              id: route.adapter,
              provider: "openai",
              auth_method: "api_key",
              protocol: "responses",
              spend_class: "explicit_api",
              implemented: true,
              live_enabled: true,
            },
          ],
        },
      });
    return response({ error: { message: "not configured" } }, 503);
  });
  vi.stubGlobal("fetch", fetcher);

  render(<ModelSettings />);

  expect(
    await screen.findByText(
      "账户绑定 · Coding API account · 仅受支持的账户编码任务按独立审批",
    ),
  ).toBeVisible();
  expect(
    within(screen.getByRole("region", { name: "模型账户" })).getByText(
      /仅受支持的账户编码任务按独立审批.*不代表已连接、已授权或可调用/,
    ),
  ).toBeVisible();
  expect(screen.getByRole("row", { name: /Main/ })).toHaveTextContent(
    "仅受支持的账户编码任务按独立审批",
  );
  expect(screen.queryByText(/状态冲突/)).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "OpenAI 真实登录（未开放）" }),
  ).toBeDisabled();
});

it("never displays returned secrets and saves an allowlisted DTO then clears the write-only key", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(
      response({
        profiles: [{ ...profile, api_key: "server-secret" }],
        settings: { default_profile_id: "p1", role_bindings: {} },
      }),
    )
    .mockResolvedValue(response({ profile }));
  stubProfileFetch(fetcher);
  render(<ModelSettings />);
  fireEvent.click(await screen.findByRole("button", { name: "编辑 测试连接" }));
  const key = screen.getByLabelText("API Key（只写）");
  expect(key).toHaveValue("");
  expect(document.body.textContent).not.toContain("server-secret");
  fireEvent.change(key, { target: { value: "fake-key" } });
  fireEvent.click(screen.getByRole("button", { name: "保存连接" }));
  await waitFor(() => expect(key).toHaveValue(""));
  const sent = JSON.parse(fetcher.mock.calls[1][1].body);
  expect(sent.api_key).toBe("fake-key");
  expect(sent.has_key).toBeUndefined();
  expect(sent.id).toBe("p1");
});
it("saves default and all role bindings explicitly including Researcher and describes probe honestly", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(
      response({
        profiles: [profile],
        settings: { default_profile_id: "", role_bindings: {} },
      }),
    )
    .mockResolvedValue(
      response({
        settings: {
          default_profile_id: "p1",
          role_bindings: { researcher: "p1" },
        },
      }),
    );
  stubProfileFetch(fetcher);
  render(<ModelSettings />);
  await screen.findByRole("button", { name: "编辑 测试连接" });
  fireEvent.change(screen.getByLabelText("默认连接"), {
    target: { value: "p1" },
  });
  fireEvent.change(screen.getByLabelText("Researcher 连接"), {
    target: { value: "p1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存角色分配" }));
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  const sent = JSON.parse(fetcher.mock.calls[1][1].body);
  expect(sent.default_profile_id).toBe("p1");
  expect(sent.role_bindings.researcher).toBe("p1");
  expect(screen.getByText(/目录连通不代表推理可用/)).toBeVisible();
});
it("treats the policy decision marker as readonly and strips it from a manual settings save", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(
      response({
        profiles: [profile],
        settings: {
          default_profile_id: "p1",
          role_bindings: { main: "p1" },
          model_policy_decision_id: "decision-readonly",
        },
      }),
    )
    .mockResolvedValueOnce(
      response({
        settings: { default_profile_id: "p1", role_bindings: { main: "p1" } },
      }),
    );
  stubProfileFetch(fetcher);
  render(<ModelSettings />);
  const save = await screen.findByRole("button", { name: "保存角色分配" });
  expect(screen.getByText(/保存手动分配会退出活动策略/)).toBeVisible();
  fireEvent.click(save);
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  const sent = JSON.parse(String(fetcher.mock.calls[1][1].body));
  expect(sent).toEqual({
    default_profile_id: "p1",
    role_bindings: expect.objectContaining({ main: "p1" }),
  });
  expect(sent.model_policy_decision_id).toBeUndefined();
});
it("creates a keyless loopback gateway without server-only or preassigned identity fields", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(
      response({
        profiles: [],
        settings: { default_profile_id: "", role_bindings: {} },
      }),
    )
    .mockResolvedValue(response({ profile }));
  stubProfileFetch(fetcher);
  render(<ModelSettings />);
  await waitFor(() => expect(screen.getByLabelText("连接名称")).toBeEnabled());
  fireEvent.change(screen.getByLabelText("接入方式"), {
    target: { value: "compatible" },
  });
  fireEvent.change(screen.getByLabelText("连接名称"), {
    target: { value: "本地假模型" },
  });
  fireEvent.change(screen.getByLabelText("API 地址"), {
    target: { value: "http://127.0.0.1:9999/v1" },
  });
  fireEvent.change(screen.getByLabelText("模型 ID"), {
    target: { value: "fixture-model" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存连接" }));
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({
    name: "本地假模型",
    kind: "compatible",
    protocol: "responses",
    base_url: "http://127.0.0.1:9999/v1",
    model: "fixture-model",
  });
});
it("keeps role settings locked after a failed read until explicit retry restores actual assignments", async () => {
  const actual = {
    default_profile_id: "p1",
    role_bindings: { researcher: "p1", auditor: "p1" },
  };
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new Error("settings unavailable"))
    .mockResolvedValueOnce(response({ profiles: [profile], settings: actual }))
    .mockResolvedValueOnce(response({ settings: actual }));
  stubProfileFetch(fetcher);
  render(<ModelSettings />);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "settings unavailable",
  );
  expect(
    screen.queryByRole("button", { name: "保存角色分配" }),
  ).not.toBeInTheDocument();
  expect(screen.queryByLabelText("默认连接")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("Researcher 连接")).not.toBeInTheDocument();
  expect(fetcher).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "重新读取模型设置" }));
  const save = await screen.findByRole("button", { name: "保存角色分配" });
  await waitFor(() => expect(save).toBeEnabled());
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(screen.getByLabelText("默认连接")).toHaveValue("p1");
  expect(screen.getByLabelText("Researcher 连接")).toHaveValue("p1");
  expect(screen.getByLabelText("Auditor 连接")).toHaveValue("p1");
  fireEvent.click(save);
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(3));
  expect(fetcher.mock.calls[2][0]).toBe("/api/settings");
  expect(JSON.parse(fetcher.mock.calls[2][1].body)).toMatchObject(actual);
});

it("does not present empty connections or default bindings before settings are received", async () => {
  stubProfileFetch(vi.fn().mockRejectedValue(new Error("offline")));
  render(<ModelSettings />);
  expect(await screen.findByRole("alert")).toHaveTextContent("offline");
  expect(screen.queryByText("未配置")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("默认连接")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("Researcher 连接")).not.toBeInTheDocument();
  expect(screen.getByText(/尚未读取现有设置/)).toBeVisible();
});

it("derives a native Messages profile from an explicit route and sends the exact route binding", async () => {
  const account = {
    id: "anthropic-account",
    revision: 3,
    name: "Anthropic API",
    provider: "anthropic",
    base_url: "https://api.anthropic.com/v1",
    auth_method: "api_key",
    billing_mode: "metered_api",
    enabled: true,
    current_credential_version: 1,
    created_at: "now",
    updated_at: "now",
    identity_status: "unverified",
    usage_status: "unknown",
    inference_enabled: false,
  };
  const route = {
    id: "anthropic-route",
    account_id: account.id,
    adapter: "anthropic_api_messages",
    protocol: "messages",
    base_url: account.base_url,
    environment: "live",
    spend_class: "explicit_api",
    extra_usage: "off",
    created_at: "now",
  };
  const runtime = {
    live_authorization: false,
    live_discovery: false,
    live_inference: false,
    simulation_inference: false,
    simulation_authorization: false,
    automatic_paid_fallback: false,
    adapters: [],
  };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/model-accounts")
      return response({ accounts: [account] });
    if (path === "/api/model-routes")
      return response({ routes: [route], runtime });
    if (path === "/api/profiles" && !init?.method)
      return response({
        profiles: [],
        settings: { default_profile_id: "", role_bindings: {} },
      });
    if (path === "/api/profiles" && init?.method === "POST")
      return response({
        profile: {
          ...profile,
          id: "route-profile",
          name: "Native Claude",
          protocol: "messages",
          model: "claude-fixture",
          base_url: account.base_url,
          account_id: account.id,
          route_id: route.id,
        },
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<ModelSettings />);
  await screen.findByText("anthropic-route");
  fireEvent.change(screen.getByLabelText("模型路由"), {
    target: { value: route.id },
  });
  expect(screen.getByLabelText("凭据来源")).toHaveValue(account.id);
  expect(screen.getByLabelText("协议")).toHaveValue("messages");
  expect(screen.getByLabelText("API 地址")).toHaveValue(account.base_url);
  fireEvent.change(screen.getByLabelText("连接名称"), {
    target: { value: "Native Claude" },
  });
  fireEvent.change(screen.getByLabelText("模型 ID"), {
    target: { value: "claude-fixture" },
  });
  expect(
    screen.getByRole("button", { name: "探测模型目录（真实环境未开放）" }),
  ).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "保存连接" }));
  await screen.findByText(/连接已保存/);
  const post = fetcher.mock.calls.find(
    ([path, init]) => path === "/api/profiles" && init?.method === "POST",
  )!;
  expect(JSON.parse(String(post[1]?.body))).toEqual({
    name: "Native Claude",
    kind: "direct",
    protocol: "messages",
    base_url: account.base_url,
    model: "claude-fixture",
    account_id: account.id,
    route_id: route.id,
  });
  expect(
    fetcher.mock.calls.filter(([path]) => String(path).endsWith("/probe")),
  ).toHaveLength(0);
});
