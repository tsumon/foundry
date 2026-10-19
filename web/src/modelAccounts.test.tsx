import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ModelSettings } from "./components/ModelSettings";
import { CapabilitySettings } from "./components/capabilities/CapabilitySettings";
import { response } from "./test/fixtures";
import { profile } from "./test/fixtures";
import {
  isSupportedMissionMode,
  isControlledReadMission,
  usesGenerationWorkspace,
} from "./types";

export const accountFixture = {
  id: "synthetic-account",
  revision: 1,
  name: "SYNTHETIC account",
  provider: "custom",
  base_url: "https://example.invalid",
  auth_method: "api_key",
  billing_mode: "unknown",
  enabled: true,
  current_credential_version: 0,
  created_at: "2026-10-18T00:00:00Z",
  updated_at: "2026-10-18T00:00:00Z",
  identity_status: "unverified",
  usage_status: "unknown",
  inference_enabled: false,
};
const settings = {
  profiles: [],
  settings: { default_profile_id: "", role_bindings: {} },
};

it("can explicitly query the original ID after an old unmounted POST settles before the remounted query", async () => {
  const account = { ...accountFixture, id: "late-before-query" };
  let finish!: (value: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  let id = "";
  const receipt = () =>
    response({
      operation: {
        account_id: account.id,
        version: 1,
        operation_id: id,
        state: "available",
        cleanup_pending: false,
        created_at: "now",
        updated_at: "now",
      },
    });
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/profiles") return response(settings);
    if (path === "/api/model-accounts")
      return response({ accounts: [account] });
    if (init?.method === "POST") {
      id = JSON.parse(String(init.body)).operation_id;
      return pending;
    }
    if (path.includes("/operations/")) return receipt();
    return response({ account, credentials: [] });
  });
  vi.stubGlobal("fetch", fetcher);
  const first = render(<ModelSettings />);
  fireEvent.click(
    await screen.findByRole("button", { name: "管理账户 SYNTHETIC account" }),
  );
  fireEvent.change(await screen.findByLabelText("凭据"), {
    target: { value: "synthetic" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存新版本" }));
  await waitFor(() => expect(id).not.toBe(""));
  first.unmount();
  render(<ModelSettings />);
  fireEvent.click(
    await screen.findByRole("button", { name: "管理账户 SYNTHETIC account" }),
  );
  await screen.findByRole("button", { name: "仅查询原操作结果" });
  await act(async () => finish(receipt()));
  fireEvent.click(screen.getByRole("button", { name: "仅查询原操作结果" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(([path]) => path.endsWith(`/operations/${id}`)),
    ).toBe(true),
  );
  await waitFor(() => expect(screen.getByLabelText("凭据")).toBeEnabled());
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it.each(["success", "client-error", "query-success"] as const)(
  "keeps newer credential intent B locked when remounted intent A settles late: %s",
  async (outcome) => {
    const account = { ...accountFixture, id: `late-${outcome}` };
    let finishA!: (value: Response) => void;
    let failB!: (cause: Error) => void;
    const pendingA = new Promise<Response>((resolve) => {
      finishA = resolve;
    });
    const pendingB = new Promise<Response>((_, reject) => {
      failB = reject;
    });
    const ids: string[] = [];
    let queries = 0;
    const receipt = (id: string, state = "available") =>
      response({
        operation: {
          account_id: account.id,
          version: 1,
          operation_id: id,
          state,
          cleanup_pending: false,
          created_at: "now",
          updated_at: "now",
        },
      });
    const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
      if (path === "/api/profiles") return response(settings);
      if (path === "/api/model-accounts")
        return response({ accounts: [account] });
      if (init?.method === "POST") {
        ids.push(JSON.parse(String(init.body)).operation_id);
        if (ids.length === 1) {
          if (outcome === "query-success")
            throw new Error("synthetic A transport uncertainty");
          return pendingA;
        }
        return pendingB;
      }
      if (path.includes("/operations/")) {
        queries++;
        if (outcome === "query-success" && queries === 1) return pendingA;
        return receipt(path.split("/").at(-1)!);
      }
      return response({ account, credentials: [] });
    });
    vi.stubGlobal("fetch", fetcher);
    const first = render(<ModelSettings />);
    fireEvent.click(
      await screen.findByRole("button", { name: "管理账户 SYNTHETIC account" }),
    );
    fireEvent.change(await screen.findByLabelText("凭据"), {
      target: { value: "synthetic-A" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存新版本" }));
    await waitFor(() => expect(ids).toHaveLength(1));
    if (outcome === "query-success") {
      await waitFor(() =>
        expect(
          screen.getByRole("button", { name: "仅查询原操作结果" }),
        ).toBeEnabled(),
      );
      fireEvent.click(screen.getByRole("button", { name: "仅查询原操作结果" }));
      await waitFor(() => expect(queries).toBe(1));
    }
    first.unmount();
    render(<ModelSettings />);
    fireEvent.click(
      await screen.findByRole("button", { name: "管理账户 SYNTHETIC account" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "仅查询原操作结果" }),
    );
    await waitFor(() => expect(screen.getByLabelText("凭据")).toBeEnabled());
    fireEvent.change(screen.getByLabelText("凭据"), {
      target: { value: "synthetic-B" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存新版本" }));
    await waitFor(() => expect(ids).toHaveLength(2));
    expect(ids[0]).not.toBe(ids[1]);
    await act(async () =>
      finishA(
        outcome === "client-error"
          ? response(
              {
                error: {
                  code: "invalid_model_account",
                  message: "untrusted old failure",
                },
              },
              400,
            )
          : receipt(ids[0]),
      ),
    );
    await act(async () =>
      failB(new Error("synthetic B transport uncertainty")),
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "仅查询原操作结果" }),
      ).toBeEnabled(),
    );
    expect(screen.getByLabelText("凭据")).toBeDisabled();
    expect(screen.getByRole("button", { name: "保存新版本" })).toBeDisabled();
    expect(screen.getByText(ids[1])).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "仅查询原操作结果" }));
    await waitFor(() =>
      expect(
        fetcher.mock.calls.some(([path]) =>
          path.endsWith(`/operations/${ids[1]}`),
        ),
      ).toBe(true),
    );
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
    ).toHaveLength(2);
  },
);

it("recognizes only the four exact account wrappers and preserves exact tool pairs", () => {
  for (const [workspace_mode, tool_mode, generation, controlled] of [
    ["account_snapshot_v1", "", false, false],
    ["generations_account_snapshot_v1", "", true, false],
    [
      "generations_controlled_read_account_snapshot_v1",
      "controlled_read_v1",
      true,
      true,
    ],
    [
      "generations_controlled_mcp_read_account_snapshot_v1",
      "controlled_mcp_read_v2",
      true,
      true,
    ],
  ] as const) {
    const m = { workspace_mode, tool_mode };
    expect(isSupportedMissionMode(m)).toBe(true);
    expect(usesGenerationWorkspace(m)).toBe(generation);
    expect(isControlledReadMission(m)).toBe(controlled);
  }
  expect(
    isSupportedMissionMode({
      workspace_mode: "generations_controlled_mcp_read_account_snapshot_v1",
      tool_mode: "controlled_read_v1",
    }),
  ).toBe(false);
  expect(
    isSupportedMissionMode({ workspace_mode: "future_account_snapshot_v1" }),
  ).toBe(false);
});

it("account-bound profiles hide inline secrets and probes while legacy credentials are immutable", async () => {
  const bound = {
    ...profile,
    id: "bound",
    name: "Bound profile",
    has_key: false,
    account_id: accountFixture.id,
    account: accountFixture,
  };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/profiles")
      return response({ ...settings, profiles: [profile, bound] });
    if (path === "/api/model-accounts")
      return response({ accounts: [accountFixture] });
    return response({ profile: bound });
  });
  vi.stubGlobal("fetch", fetcher);
  render(<ModelSettings />);
  fireEvent.click(await screen.findByRole("button", { name: "编辑 测试连接" }));
  expect(screen.queryByLabelText("API Key（只写）")).not.toBeInTheDocument();
  expect(screen.getByText(/旧连接已有凭据，不能原地覆盖/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "编辑 Bound profile" }));
  expect(screen.queryByLabelText("API Key（只写）")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "探测模型目录" }),
  ).not.toBeInTheDocument();
  expect(screen.getByLabelText("API 地址")).toHaveAttribute("readonly");
  fireEvent.click(screen.getByRole("button", { name: "保存连接" }));
  await waitFor(() =>
    expect(fetcher.mock.calls.some(([, i]) => i?.method === "POST")).toBe(true),
  );
  const body = JSON.parse(
    String(fetcher.mock.calls.find(([, i]) => i?.method === "POST")?.[1]?.body),
  );
  expect(body.account_id).toBe(accountFixture.id);
  expect(body.account).toBeUndefined();
  expect(body.account_revision).toBeUndefined();
  expect(body.has_key).toBeUndefined();
});

it("shows the live renamed account in saved connection labels without rewriting the embedded profile snapshot", async () => {
  let live = { ...accountFixture, id: "rename-label" };
  const embedded = { ...live };
  const bound = {
    ...profile,
    id: "bound-renamed",
    name: "Bound rename label",
    account_id: live.id,
    account: embedded,
    has_key: false,
  };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/profiles")
      return response({ ...settings, profiles: [bound] });
    if (path === "/api/model-accounts") return response({ accounts: [live] });
    if (init?.method === "PUT") {
      const q = JSON.parse(String(init.body));
      live = { ...live, name: q.name, revision: 2 };
      return response({ account: live });
    }
    return response({ account: live, credentials: [] });
  });
  vi.stubGlobal("fetch", fetcher);
  render(<ModelSettings />);
  expect(
    await screen.findByText("账户绑定 · SYNTHETIC account · 真实调用未开放"),
  ).toBeVisible();
  fireEvent.click(
    screen.getByRole("button", { name: "管理账户 SYNTHETIC account" }),
  );
  fireEvent.change(await screen.findByLabelText("账户新名称"), {
    target: { value: "Renamed live label" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存账户变更" }));
  expect(
    await screen.findByText("账户绑定 · Renamed live label · 真实调用未开放"),
  ).toBeVisible();
  expect(embedded.name).toBe("SYNTHETIC account");
});

it("creates unverified metadata with strict create fields and known-zero credentials versus unknown usage", async () => {
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/profiles") return response(settings);
    if (path === "/api/model-accounts" && init?.method === "POST")
      return response({ account: accountFixture });
    if (path === "/api/model-accounts") return response({ accounts: [] });
    return response({ account: accountFixture, credentials: [] });
  });
  vi.stubGlobal("fetch", fetcher);
  render(<ModelSettings />);
  expect(await screen.findByText("尚无模型账户")).toBeVisible();
  fireEvent.change(screen.getByLabelText("账户名称"), {
    target: { value: "SYNTHETIC account" },
  });
  fireEvent.change(screen.getByLabelText("账户固定端点"), {
    target: { value: "https://example.invalid" },
  });
  fireEvent.click(screen.getByRole("button", { name: "创建账户" }));
  await screen.findByRole("button", { name: "管理账户 SYNTHETIC account" });
  const posted = fetcher.mock.calls.find(([, init]) => init?.method === "POST");
  expect(JSON.parse(String(posted?.[1]?.body))).toEqual({
    name: "SYNTHETIC account",
    provider: "custom",
    base_url: "https://example.invalid",
    auth_method: "api_key",
    billing_mode: "unknown",
  });
  expect(screen.getByText(/当前凭据：0（未配置）/)).toBeVisible();
  expect(screen.getAllByText(/额度未知/).length).toBeGreaterThan(0);
  expect(
    screen.getByRole("button", { name: "OpenAI 真实登录（未开放）" }),
  ).toBeDisabled();
});

it("creates OpenAI SIWC metadata without exposing API-key or live-login operations", async () => {
  const siwc = {
    ...accountFixture,
    id: "siwc-account",
    name: "OpenAI subscription",
    provider: "openai",
    base_url: "https://api.openai.com/v1",
    auth_method: "openai_siwc",
    billing_mode: "subscription",
  };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/profiles") return response(settings);
    if (path === "/api/model-routes")
      return response({
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
      });
    if (path === "/api/model-authorizations")
      return response({ authorizations: [], remote_revocations: [] });
    if (path === "/api/model-accounts" && init?.method === "POST")
      return response({ account: siwc });
    if (path === "/api/model-accounts") return response({ accounts: [] });
    return response({ account: siwc, credentials: [] });
  });
  vi.stubGlobal("fetch", fetcher);
  render(<ModelSettings />);
  await screen.findByText("尚无模型账户");
  fireEvent.change(screen.getByLabelText("声明厂商"), {
    target: { value: "openai" },
  });
  fireEvent.change(screen.getByLabelText("认证方式"), {
    target: { value: "openai_siwc" },
  });
  fireEvent.change(screen.getByLabelText("账户名称"), {
    target: { value: "OpenAI subscription" },
  });
  fireEvent.change(screen.getByLabelText("账户固定端点"), {
    target: { value: "https://api.openai.com/v1" },
  });
  expect(screen.getByLabelText("声明计费渠道")).toHaveValue("subscription");
  fireEvent.click(screen.getByRole("button", { name: "创建账户" }));
  const manage = await screen.findByRole("button", {
    name: "管理账户 OpenAI subscription",
  });
  await waitFor(() => expect(manage).toBeEnabled());
  fireEvent.click(manage);
  expect(screen.queryByLabelText("凭据")).not.toBeInTheDocument();
  expect(
    await screen.findByRole("button", { name: "真实登录（未开放）" }),
  ).toBeDisabled();
  expect(screen.getByText(/生产真实登录不会发起请求/)).toBeVisible();
  const post = fetcher.mock.calls.find(
    ([path, init]) => path === "/api/model-accounts" && init?.method === "POST",
  )!;
  expect(JSON.parse(String(post[1]?.body))).toEqual({
    name: "OpenAI subscription",
    provider: "openai",
    base_url: "https://api.openai.com/v1",
    auth_method: "openai_siwc",
    billing_mode: "subscription",
  });
});

it("clears uncertain secrets, suppresses duplicates and keeps original GET-only receipt across settings unmount", async () => {
  let operationID = "";
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/profiles") return response(settings);
    if (path === "/api/model-accounts")
      return response({ accounts: [accountFixture] });
    if (init?.method === "POST") {
      operationID = JSON.parse(String(init.body)).operation_id;
      throw new Error("SYNTHETIC-ONLY-KEY");
    }
    if (path.includes("/operations/"))
      return response({
        operation: {
          account_id: accountFixture.id,
          version: 1,
          operation_id: operationID,
          state: "available",
          cleanup_pending: false,
        },
      });
    return response({ account: accountFixture, credentials: [] });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <CapabilitySettings
      enabled
      catalog={{ snapshot: null, ready: false, loading: false, error: "" }}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "管理账户 SYNTHETIC account" }),
  );
  const secret = await screen.findByLabelText("凭据");
  fireEvent.change(secret, { target: { value: "SYNTHETIC-ONLY-KEY" } });
  const save = screen.getByRole("button", { name: "保存新版本" });
  fireEvent.click(save);
  fireEvent.click(save);
  await screen.findByRole("button", { name: "仅查询原操作结果" });
  expect(secret).toHaveValue("");
  expect(document.body.textContent).not.toContain("SYNTHETIC-ONLY-KEY");
  fireEvent.click(screen.getByRole("button", { name: "事件钩子" }));
  fireEvent.click(screen.getByRole("button", { name: "模型" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "管理账户 SYNTHETIC account" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "仅查询原操作结果" }),
  );
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(([p]) =>
        p.endsWith(`/operations/${operationID}`),
      ),
    ).toBe(true),
  );
  expect(
    fetcher.mock.calls.filter(([, i]) => i?.method === "POST"),
  ).toHaveLength(1);
});
