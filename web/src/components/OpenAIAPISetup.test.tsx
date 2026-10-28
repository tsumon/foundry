import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ModelSettings } from "./ModelSettings";
import { response } from "../test/fixtures";

const account = {
  id: "api-account",
  name: "My API",
  revision: 2,
  provider: "openai",
  base_url: "https://api.openai.com/v1",
  auth_method: "api_key",
  billing_mode: "api_credits",
  enabled: true,
  current_credential_version: 1,
  created_at: "now",
  updated_at: "now",
  identity_status: "unverified",
  usage_status: "unknown",
  inference_enabled: false,
};
const route = {
  id: "api-route",
  account_id: account.id,
  adapter: "openai_api_responses",
  protocol: "responses",
  environment: "live",
  base_url: account.base_url,
  spend_class: "explicit_api",
  extra_usage: "off",
  created_at: "now",
};
const profile = {
  id: "api-profile",
  name: "My API · approved-api-model",
  kind: "direct",
  model: "approved-api-model",
  protocol: "responses",
  base_url: account.base_url,
  account_id: account.id,
  route_id: route.id,
  has_key: false,
};
function fixture(
  options: {
    accounts?: unknown[];
    savedRoute?: boolean;
    lose?: "route" | "profile";
    gateway?: boolean;
  } = {},
) {
  const selectedRoute = options.gateway
    ? {
        ...route,
        adapter: "compatible_api_responses",
        base_url: "https://gateway.example.invalid/v1",
      }
    : route;
  let routes = options.savedRoute ? [route] : [];
  let profiles: (typeof profile)[] = [];
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/model-accounts")
      return response({ accounts: options.accounts ?? [account] });
    if (path === "/api/model-routes") {
      if (init?.method === "POST") {
        routes = [selectedRoute];
        if (options.lose === "route") throw new TypeError("lost response");
        return response({ route: selectedRoute });
      }
      return response({
        routes,
        runtime: {
          live_inference: true,
          automatic_paid_fallback: false,
          adapters: [
            {
              id: "openai_api_responses",
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
    }
    if (path === "/api/profiles") {
      if (init?.method === "POST") {
        profiles = [{ ...profile, ...JSON.parse(String(init.body)) }];
        if (options.lose === "profile") throw new TypeError("lost response");
        return response({ profile: profiles[0] });
      }
      return response({
        profiles,
        settings: { default_profile_id: "original-profile", role_bindings: {} },
      });
    }
    if (path === "/api/role-model-policy")
      return response({
        state: {
          policy: { revision: 0, roles: {} },
          profiles_digest: "0".repeat(64),
        },
      });
    throw new Error(`unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
async function fill() {
  const panel = await screen.findByRole("region", {
    name: "API 快速配置",
  });
  await waitFor(() =>
    expect(
      within(panel).getByRole("option", { name: /My API/ }),
    ).toBeInTheDocument(),
  );
  fireEvent.change(within(panel).getByLabelText("API 账户"), {
    target: { value: account.id },
  });
  fireEvent.change(within(panel).getByLabelText("API 模型 ID"), {
    target: { value: "approved-api-model" },
  });
  return within(panel);
}
it("creates an explicit API route and profile without login, discovery, inference or role changes", async () => {
  const fetcher = fixture();
  const storage = vi.spyOn(Storage.prototype, "setItem");
  render(<ModelSettings />);
  const panel = await fill();
  const save = panel.getByRole("button", { name: "保存 API 模型连接" });
  expect(save).toBeDisabled();
  expect(fetcher.mock.calls.every(([, init]) => !init?.method)).toBe(true);
  fireEvent.click(panel.getByRole("checkbox", { name: /按 API 计费/ }));
  fireEvent.click(save);
  fireEvent.click(save);
  await waitFor(() =>
    expect(panel.getByRole("status")).toHaveTextContent("连接已保存"),
  );
  const writes = fetcher.mock.calls.filter(
    ([, init]) => init?.method === "POST",
  );
  expect(writes.map(([p]) => p)).toEqual([
    "/api/model-routes",
    "/api/profiles",
  ]);
  expect(JSON.parse(String(writes[0][1]?.body))).toEqual({
    account_id: "api-account",
    expected_account_revision: 2,
    adapter: "openai_api_responses",
    environment: "live",
    extra_usage: "off",
  });
  expect(JSON.parse(String(writes[1][1]?.body))).toEqual({
    name: profile.name,
    kind: "direct",
    protocol: "responses",
    base_url: account.base_url,
    account_id: account.id,
    route_id: route.id,
    model: "approved-api-model",
  });
  expect(storage).not.toHaveBeenCalled();
  expect(
    fetcher.mock.calls.some(([p]) =>
      /authorization|acceptance|probe|inference|credentials/.test(p),
    ),
  ).toBe(false);
});
it("reuses the exact existing API route without replaying its creation", async () => {
  const fetcher = fixture({ savedRoute: true });
  render(<ModelSettings />);
  const panel = await fill();
  fireEvent.click(panel.getByRole("checkbox", { name: /按 API 计费/ }));
  fireEvent.click(panel.getByRole("button", { name: "保存 API 模型连接" }));
  await waitFor(() =>
    expect(panel.getByRole("status")).toHaveTextContent("连接已保存"),
  );
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method).map(([p]) => p),
  ).toEqual(["/api/profiles"]);
});
it.each(["route", "profile"] as const)(
  "reconciles a lost %s save by reading only, never automatically replaying",
  async (lose) => {
    const fetcher = fixture({ lose });
    render(<ModelSettings />);
    const panel = await fill();
    fireEvent.click(panel.getByRole("checkbox", { name: /按 API 计费/ }));
    fireEvent.click(panel.getByRole("button", { name: "保存 API 模型连接" }));
    await waitFor(() =>
      expect(
        panel.getByRole("button", { name: "仅重新读取保存结果" }),
      ).toBeEnabled(),
    );
    expect(
      panel.getByRole("button", { name: "保存 API 模型连接" }),
    ).toBeDisabled();
    const before = fetcher.mock.calls.filter(([, init]) => init?.method).length;
    fireEvent.click(panel.getByRole("button", { name: "仅重新读取保存结果" }));
    await waitFor(() =>
      expect(
        panel.queryByRole("button", { name: "仅重新读取保存结果" }),
      ).not.toBeInTheDocument(),
    );
    expect(fetcher.mock.calls.filter(([, init]) => init?.method)).toHaveLength(
      before,
    );
    expect(panel.getByRole("status")).toHaveTextContent(
      lose === "profile" ? "已找到保存的连接" : "路由已保存",
    );
  },
);
it("does not offer subscription, arbitrary endpoints, unknown billing or unconfigured keys as API-ready", async () => {
  fixture({
    accounts: [
      {
        ...account,
        id: "subscription",
        auth_method: "openai_siwc",
        billing_mode: "subscription",
      },
      { ...account, id: "gateway", base_url: "https://example.invalid" },
      { ...account, id: "unknown", billing_mode: "unknown" },
      { ...account, id: "no-key", current_credential_version: 0 },
    ],
  });
  render(<ModelSettings />);
  const panel = within(
    await screen.findByRole("region", { name: "API 快速配置" }),
  );
  expect(
    panel.getByRole("button", { name: "保存 API 模型连接" }),
  ).toBeDisabled();
  expect(panel.getAllByRole("option")).toHaveLength(1);
});

it("pins an explicitly selected compatible gateway instead of relabeling it as OpenAI", async () => {
  const gateway = {
    ...account,
    provider: "custom",
    base_url: "https://gateway.example.invalid/v1",
  };
  const fetcher = fixture({ accounts: [gateway], gateway: true });
  render(<ModelSettings />);
  const panel = await fill();
  expect(panel.getByText(gateway.base_url)).toBeVisible();
  fireEvent.click(panel.getByRole("checkbox", { name: /按 API 计费/ }));
  fireEvent.click(panel.getByRole("button", { name: "保存 API 模型连接" }));
  await waitFor(() =>
    expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
      true,
    ),
  );
  const write = fetcher.mock.calls.find(
    ([p, init]) => p === "/api/model-routes" && init?.method === "POST",
  )!;
  expect(JSON.parse(String(write[1]?.body))).toMatchObject({
    account_id: account.id,
    adapter: "compatible_api_responses",
    environment: "live",
    extra_usage: "off",
  });
  await waitFor(() =>
    expect(panel.getByRole("status")).toHaveTextContent("连接已保存"),
  );
  const profileWrite = fetcher.mock.calls.find(
    ([p, init]) => p === "/api/profiles" && init?.method === "POST",
  )!;
  expect(JSON.parse(String(profileWrite[1]?.body))).toMatchObject({
    kind: "compatible",
    base_url: gateway.base_url,
    route_id: route.id,
    account_id: gateway.id,
    protocol: "responses",
  });
});

it.each(["网关".repeat(35), "a".repeat(195), "😀".repeat(60)])(
  "bounds generated profile names in UTF-8 bytes",
  async (name) => {
    const fetcher = fixture({
      accounts: [{ ...account, name: `My API ${name}` }],
    });
    render(<ModelSettings />);
    const panel = await fill();
    fireEvent.click(panel.getByRole("checkbox", { name: /按 API 计费/ }));
    fireEvent.click(panel.getByRole("button", { name: "保存 API 模型连接" }));
    await waitFor(() =>
      expect(panel.getByRole("status")).toHaveTextContent("连接已保存"),
    );
    const write = fetcher.mock.calls.find(
      ([p, init]) => p === "/api/profiles" && init?.method === "POST",
    )!;
    const saved = JSON.parse(String(write[1]?.body)).name;
    expect(new TextEncoder().encode(saved).length).toBeLessThanOrEqual(200);
    expect(saved).not.toContain("\uFFFD");
    expect(`My API ${name} · approved-api-model`.startsWith(saved)).toBe(true);
  },
);
