import {
  act,
  fireEvent,
  render,
  screen,
  within,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { response } from "../test/fixtures";
import { ModelAcceptanceOperations } from "./ModelAcceptanceOperations";

const account = {
  id: "acceptance-gate-openai",
  revision: 1,
  name: "SYNTHETIC acceptance gates — openai",
  provider: "openai",
  base_url: "http://127.0.0.1:43123/v1",
  auth_method: "openai_siwc" as const,
  billing_mode: "subscription" as const,
  enabled: true,
  current_credential_version: 0,
  created_at: "2026-10-19T00:00:00Z",
  updated_at: "2026-10-19T00:00:00Z",
  identity_status: "unverified" as const,
  usage_status: "unknown" as const,
  inference_enabled: false as const,
};
const route = {
  id: "acceptance-gate-openai-route",
  adapter: "openai_siwc_responses",
  environment: "simulation" as const,
  spend_class: "subscription_only",
  base_url: "http://127.0.0.1:43123/v1",
};
const digest = "a".repeat(64);
const material = (phase: "connection" | "catalog" | "inference") => ({
  phase,
  account,
  route: {
    ...route,
    account_id: account.id,
    protocol: "responses",
    extra_usage: "off",
    created_at: "2026-10-19T00:00:00Z",
  },
  credential_version: 0,
  data_summary: "fixed synthetic input; no repository or user data",
  created_at: "2026-10-19T00:01:00Z",
  expires_at: "2026-10-19T00:11:00Z",
  deadline_seconds: 30 as const,
  max_attempts: 1 as const,
});
const operation = (
  id: string,
  phase: "connection" | "catalog" | "inference",
  state: string,
  extra: Record<string, unknown> = {},
) => ({
  id,
  account_id: account.id,
  revision: 1,
  phase,
  state,
  material: material(phase),
  material_digest: digest,
  attempts: 0,
  claim_sequence: 0,
  source: "local_simulation",
  certainty: "not_sent",
  catalog: [] as Array<{ id: string; display_name: string }>,
  usage_known: false,
  quota: "unknown",
  remote_billing: "unverified",
  live_access: "blocked",
  ...extra,
});
const history = (operations: unknown[] = [], enabled = true) =>
  response({
    operations,
    simulation_enabled: enabled,
    routes: enabled ? [route] : [],
  });

const subscriptionAccount = {
  ...account,
  id: "acceptance-gate-openai-subscription",
  name: "SYNTHETIC OpenAI — subscription connection",
};
const subscriptionRoute = {
  ...route,
  id: "acceptance-gate-openai-subscription-route",
  connection_purpose: "subscription_only" as const,
};
const subscriptionScopes = [
  "openid",
  "profile",
  "email",
  "offline_access",
  "resource.invoke",
  "chatgpt.tokens.use.direct",
] as const;
const subscriptionMaterial = () => ({
  ...material("connection"),
  account: subscriptionAccount,
  route: {
    ...subscriptionRoute,
    account_id: subscriptionAccount.id,
    protocol: "responses",
    extra_usage: "off",
    created_at: "2026-10-19T00:00:00Z",
  },
  connection_purpose: "subscription_only" as const,
  requested_scopes: subscriptionScopes,
  requested_resource: "https://api.openai.com/v1" as const,
});
const subscriptionOperation = (
  id: string,
  state: string,
  extra: Record<string, unknown> = {},
) => ({
  ...operation(id, "connection", state, extra),
  account_id: subscriptionAccount.id,
  material: subscriptionMaterial(),
  ...extra,
});

const handoffAccount = {
  ...subscriptionAccount,
  id: "acceptance-gate-openai-handoff",
  name: "SYNTHETIC OpenAI — browser connection",
};
const handoffRoute = {
  ...subscriptionRoute,
  id: "acceptance-gate-openai-handoff-route",
  connection_mode: "browser_handoff" as const,
};
const handoffMaterial = () => ({
  ...subscriptionMaterial(),
  account: handoffAccount,
  route: {
    ...handoffRoute,
    account_id: handoffAccount.id,
    protocol: "responses",
    extra_usage: "off",
    created_at: "2026-10-19T00:00:00Z",
  },
  connection_mode: "browser_handoff" as const,
  login_timeout_seconds: 600 as const,
});
const handoffOperation = (
  id: string,
  state: string,
  extra: Record<string, unknown> = {},
) => ({
  ...subscriptionOperation(id, state, extra),
  account_id: handoffAccount.id,
  material: handoffMaterial(),
  ...extra,
});
const handoffSession = (operationID: string) => ({
  operation_id: operationID,
  account_id: handoffAccount.id,
  state: "waiting",
  expires_at: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
  handoff_url: "http://127.0.0.1:43126/auth/start",
});

it("requires a new reviewed permit for connection, catalog, and inference without automatic dispatch", async () => {
  const refreshAccount = vi.fn(async () => {});
  const posts: Array<{ path: string; body: Record<string, unknown> }> = [];
  let connection: ReturnType<typeof operation> & {
    result_account_revision?: number;
  } = operation("connection-1", "connection", "pending");
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method) return history();
    const body = JSON.parse(String(init.body));
    posts.push({ path, body });
    if (path.endsWith("/acceptance-operations")) {
      if (body.phase === "connection") {
        connection = operation("connection-1", "connection", "pending");
        return response({ operation: connection });
      }
      expect(body).toEqual({
        phase: "catalog",
        route_id: route.id,
        expected_account_revision: 2,
        prerequisite_id: "connection-1",
      });
      return response({
        operation: operation("catalog-1", "catalog", "pending", {
          material: {
            ...material("catalog"),
            account: { ...account, revision: 2 },
            prerequisite_id: "connection-1",
            connection_id: "connection-1",
          },
        }),
      });
    }
    if (path.endsWith("/decision")) {
      expect(body).toEqual({
        expected_revision: 1,
        material_digest: digest,
        decision: "approve",
      });
      connection = { ...connection, revision: 2, state: "approved" };
      return response({ operation: connection });
    }
    if (path.endsWith("/execute")) {
      expect(body).toEqual({ expected_revision: 2, material_digest: digest });
      connection = {
        ...connection,
        revision: 4,
        state: "succeeded",
        attempts: 1,
        claim_sequence: 7,
        certainty: "completed",
        result_account_revision: 2,
      };
      return response({ operation: connection });
    }
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={refreshAccount}
    />,
  );
  await screen.findByText("尚无已保存的验收材料");
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "创建目录材料" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "创建连接材料" }));
  expect((await screen.findAllByText("connection-1"))[0]).toBeVisible();
  expect(screen.getByText(route.base_url)).toBeVisible();
  expect(screen.getByText(/subscription · subscription_only/)).toBeVisible();
  expect(screen.getByText(/fixed synthetic input/)).toBeVisible();
  expect(screen.getByText(/30 秒/)).toBeVisible();
  expect(screen.getByText(/最多 1 次/)).toBeVisible();
  expect(screen.getByText(digest)).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "执行连接" }),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "批准连接材料" }));
  expect(await screen.findByRole("button", { name: "执行连接" })).toBeEnabled();
  expect(posts).toHaveLength(2);
  fireEvent.click(screen.getByRole("button", { name: "执行连接" }));
  await waitFor(() => expect(refreshAccount).toHaveBeenCalledOnce());
  expect(screen.getByText(/连接 · 已成功 · 本地模拟/)).toBeVisible();
  expect(screen.getByRole("button", { name: "创建目录材料" })).toBeEnabled();
  expect(posts).toHaveLength(3);
  fireEvent.click(screen.getByRole("button", { name: "创建目录材料" }));
  await screen.findAllByText("catalog-1");
  expect(posts).toHaveLength(4);
  vi.useFakeTimers();
  try {
    await act(async () => vi.advanceTimersByTimeAsync(120_000));
    expect(fetcher).toHaveBeenCalledTimes(5);
  } finally {
    vi.useRealTimers();
  }
});

it("reviews the exact subscription permission contract and exposes only the independent connection controls", async () => {
  let current: ReturnType<typeof subscriptionOperation> & {
    result_account_revision?: number;
  } = subscriptionOperation("subscription-connection", "pending");
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method)
      return response({
        operations: [],
        simulation_enabled: true,
        routes: [subscriptionRoute],
      });
    const body = JSON.parse(String(init.body));
    if (path.endsWith("/acceptance-operations")) {
      expect(body).toEqual({
        phase: "connection",
        route_id: subscriptionRoute.id,
        expected_account_revision: 1,
        connection_purpose: "subscription_only",
      });
      return response({ operation: current });
    }
    if (path.endsWith("/decision")) {
      expect(body).toEqual({
        expected_revision: 1,
        material_digest: digest,
        decision: "approve",
      });
      current = { ...current, revision: 2, state: "approved" };
      return response({ operation: current });
    }
    if (path.endsWith("/execute")) {
      current = {
        ...current,
        revision: 4,
        state: "succeeded",
        attempts: 1,
        claim_sequence: 1,
        certainty: "completed",
        result_account_revision: 1,
      };
      return response({ operation: current });
    }
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={subscriptionAccount}
      refreshAccount={async () => {}}
    />,
  );

  expect(await screen.findByText(/本地订阅连接模拟/)).toBeVisible();
  expect(screen.getByText(/额度：未知.*真实访问：关闭/)).toBeVisible();
  expect(screen.queryByText("目录")).not.toBeInTheDocument();
  expect(screen.queryByText("推理")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "创建连接材料" }));

  const review = await screen.findByLabelText("连接材料审阅");
  for (const scope of subscriptionScopes)
    expect(within(review).getByText(scope)).toBeVisible();
  expect(within(review).getByText(/用于身份协议登录/)).toBeVisible();
  expect(within(review).getByText(/读取基础个人资料/)).toBeVisible();
  expect(within(review).getByText(/读取账户邮箱声明/)).toBeVisible();
  expect(within(review).getByText(/请求离线授权权限/)).toBeVisible();
  expect(within(review).getByText(/请求资源调用权限/)).toBeVisible();
  expect(within(review).getByText(/请求订阅令牌直接使用权限/)).toBeVisible();
  expect(within(review).getByText("https://api.openai.com/v1")).toBeVisible();
  expect(within(review).getByText(subscriptionRoute.base_url)).toBeVisible();
  expect(
    within(review).getByText(/协议 audience，不是本地传输地址/),
  ).toBeVisible();
  expect(
    within(review).getByText("offline_access").closest("li"),
  ).toHaveTextContent(/offline_access.*应用刷新仍禁用/);
  expect(
    within(review).getByText(
      /目录查询：禁用.*推理：禁用.*应用刷新：禁用.*付费回退：禁用/,
    ),
  ).toBeVisible();
  expect(within(review).getByText(/批准有效期 10 分钟/)).toBeVisible();
  expect(within(review).getByText(/30 秒/)).toBeVisible();
  expect(within(review).getByText(/最多 1 次/)).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "执行连接" }),
  ).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "批准连接材料" }));
  expect(await screen.findByRole("button", { name: "执行连接" })).toBeEnabled();
  expect(
    fetcher.mock.calls.filter(([path]) => String(path).endsWith("/execute")),
  ).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "执行连接" }));
  expect(await screen.findByText(/连接 · 已成功 · 本地模拟/)).toBeVisible();
  expect(screen.queryByText("目录")).not.toBeInTheDocument();
  expect(screen.queryByText("推理")).not.toBeInTheDocument();
});

it("keeps browser handoff proposal, approval, execution, manual open, read, and cancellation separate", async () => {
  let current: ReturnType<typeof handoffOperation> & { code?: string } =
    handoffOperation("browser-handoff", "pending");
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method) {
      if (path.endsWith(`/${current.id}`))
        return response({
          operation: current,
          ...(current.state === "running"
            ? { connection_session: handoffSession(current.id) }
            : {}),
        });
      return response({
        operations: [],
        simulation_enabled: true,
        routes: [handoffRoute],
      });
    }
    const body = JSON.parse(String(init.body));
    if (path.endsWith("/acceptance-operations")) {
      expect(body).toEqual({
        phase: "connection",
        route_id: handoffRoute.id,
        expected_account_revision: 1,
        connection_purpose: "subscription_only",
      });
      return response({ operation: current });
    }
    if (path.endsWith("/decision")) {
      current = { ...current, revision: 2, state: "approved" };
      return response({ operation: current });
    }
    if (path.endsWith("/execute")) {
      current = {
        ...current,
        revision: 3,
        state: "running",
        attempts: 1,
        claim_sequence: 1,
      };
      return response({
        operation: current,
        connection_session: handoffSession(current.id),
      });
    }
    if (path.endsWith("/cancel")) {
      expect(body).toEqual({
        expected_revision: 3,
        material_digest: digest,
      });
      current = {
        ...current,
        revision: 4,
        state: "unknown",
        certainty: "unknown",
        code: "transport_unknown",
      };
      return response({ operation: current });
    }
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={handoffAccount}
      refreshAccount={async () => {}}
    />,
  );

  fireEvent.click(await screen.findByRole("button", { name: "创建连接材料" }));
  const review = await screen.findByLabelText("连接材料审阅");
  expect(within(review).getByText(/批准有效期 10 分钟/)).toBeVisible();
  expect(within(review).getByText(/人工登录窗口 600 秒/)).toBeVisible();
  expect(within(review).getByText(/网络操作期限 30 秒/)).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "打开本地合成授权页" }),
  ).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "批准连接材料" }));
  await screen.findByRole("button", { name: "执行连接" });
  expect(
    fetcher.mock.calls.filter(([path]) => String(path).endsWith("/execute")),
  ).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "执行连接" }));

  const link = await screen.findByRole("link", {
    name: "打开本地合成授权页",
  });
  expect(link).toHaveAttribute("href", handoffSession(current.id).handoff_url);
  expect(fetcher).toHaveBeenCalledTimes(4);
  fireEvent.click(screen.getByRole("button", { name: "读取连接状态" }));
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(5));
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(3);

  fireEvent.click(screen.getByRole("button", { name: "取消连接会话" }));
  expect(
    await screen.findByText(/再次尝试需要创建并独立批准新材料/),
  ).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "打开本地合成授权页" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "取消连接会话" }),
  ).not.toBeInTheDocument();
});

it("reconciles a lost browser execute response with one explicit detail read and never replays execution", async () => {
  const approved = handoffOperation("lost-browser-execute", "approved", {
    revision: 2,
  });
  const running = {
    ...approved,
    revision: 3,
    state: "running",
    attempts: 1,
    claim_sequence: 4,
  };
  let historyReads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (init?.method) throw new Error("synthetic lost execute response");
    if (path.endsWith(`/${approved.id}`))
      return response({
        operation: running,
        connection_session: handoffSession(approved.id),
      });
    historyReads++;
    return response({
      operations: [historyReads === 1 ? approved : running],
      simulation_enabled: true,
      routes: [handoffRoute],
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={handoffAccount}
      refreshAccount={async () => {}}
    />,
  );

  fireEvent.change(await screen.findByLabelText("连接已保存材料"), {
    target: { value: approved.id },
  });
  fireEvent.click(screen.getByRole("button", { name: "执行连接" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "仅读取原验收记录" }),
  );

  expect(
    await screen.findByRole("link", { name: "打开本地合成授权页" }),
  ).toBeVisible();
  expect(
    fetcher.mock.calls.filter(([path]) => String(path).endsWith("/execute")),
  ).toHaveLength(1);
});

it("reconciles a lost browser cancellation response by reading and never repeats cancellation", async () => {
  const running = handoffOperation("lost-browser-cancel", "running", {
    revision: 3,
    attempts: 1,
    claim_sequence: 5,
  });
  const terminal = {
    ...running,
    revision: 4,
    state: "unknown",
    certainty: "unknown",
    code: "transport_unknown",
  };
  let current = running;
  let historyReads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method) {
      if (path.endsWith(`/${running.id}`))
        return response({
          operation: current,
          ...(current.state === "running"
            ? { connection_session: handoffSession(current.id) }
            : {}),
        });
      historyReads++;
      return response({
        operations: [current],
        simulation_enabled: true,
        routes: [handoffRoute],
      });
    }
    if (path.endsWith("/cancel")) {
      current = terminal;
      throw new Error("synthetic lost cancel response");
    }
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={handoffAccount}
      refreshAccount={async () => {}}
    />,
  );

  fireEvent.change(await screen.findByLabelText("连接已保存材料"), {
    target: { value: running.id },
  });
  expect(fetcher).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "读取连接状态" }));
  expect(
    await screen.findByRole("link", { name: "打开本地合成授权页" }),
  ).toBeVisible();
  expect(historyReads).toBe(1);

  fireEvent.click(screen.getByRole("button", { name: "取消连接会话" }));
  expect(
    await screen.findByRole("button", { name: "仅读取原验收记录" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "打开本地合成授权页" }),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "仅读取原验收记录" }));

  expect(
    await screen.findByText(/再次尝试需要创建并独立批准新材料/),
  ).toBeVisible();
  expect(
    fetcher.mock.calls.filter(([path]) => String(path).endsWith("/cancel")),
  ).toHaveLength(1);
  expect(
    screen.queryByRole("link", { name: "打开本地合成授权页" }),
  ).not.toBeInTheDocument();
});

it("fails closed on a mismatched browser session and never renders its URL", async () => {
  const approved = handoffOperation("unsafe-browser-session", "approved", {
    revision: 2,
  });
  const running = {
    ...approved,
    revision: 3,
    state: "running",
    attempts: 1,
    claim_sequence: 3,
  };
  const unsafe = "http://127.0.0.1:43126/auth/start?state=secret";
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init?: RequestInit) =>
      init?.method
        ? response({
            operation: running,
            connection_session: {
              ...handoffSession(running.id),
              account_id: "different-account",
              handoff_url: unsafe,
            },
          })
        : response({
            operations: [approved],
            simulation_enabled: true,
            routes: [handoffRoute],
          }),
    ),
  );
  render(
    <ModelAcceptanceOperations
      account={handoffAccount}
      refreshAccount={async () => {}}
    />,
  );

  fireEvent.change(await screen.findByLabelText("连接已保存材料"), {
    target: { value: approved.id },
  });
  fireEvent.click(screen.getByRole("button", { name: "执行连接" }));

  expect(await screen.findByText("invalid_receipt")).toBeVisible();
  expect(document.body.textContent).not.toContain(unsafe);
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
});

it("removes a previously safe link before failing closed on a malformed status response", async () => {
  const running = handoffOperation("status-turns-malformed", "running", {
    revision: 3,
    attempts: 1,
    claim_sequence: 6,
  });
  let detailReads = 0;
  const fetcher = vi.fn(async (path: string) => {
    if (path.endsWith(`/${running.id}`)) {
      detailReads++;
      return response({
        operation: running,
        connection_session:
          detailReads === 1
            ? handoffSession(running.id)
            : {
                ...handoffSession(running.id),
                handoff_url: "http://127.0.0.1:43126/auth/start?secret=1",
              },
      });
    }
    return response({
      operations: [running],
      simulation_enabled: true,
      routes: [handoffRoute],
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={handoffAccount}
      refreshAccount={async () => {}}
    />,
  );

  fireEvent.change(await screen.findByLabelText("连接已保存材料"), {
    target: { value: running.id },
  });
  fireEvent.click(screen.getByRole("button", { name: "读取连接状态" }));
  expect(
    await screen.findByRole("link", { name: "打开本地合成授权页" }),
  ).toBeVisible();

  fireEvent.click(screen.getByRole("button", { name: "读取连接状态" }));
  expect(await screen.findByText("invalid_receipt")).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "打开本地合成授权页" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
});

it.each([
  {
    name: "browser route attached to identity-only purpose",
    routes: [{ ...handoffRoute, connection_purpose: "identity_only" }],
    operations: [],
  },
  {
    name: "browser material missing its server-owned mode",
    routes: [handoffRoute],
    operations: [
      handoffOperation("handoff-mode-missing", "pending", {
        material: {
          ...handoffMaterial(),
          connection_mode: undefined,
          login_timeout_seconds: undefined,
        },
      }),
    ],
  },
  {
    name: "browser material with a short human-login timeout",
    routes: [handoffRoute],
    operations: [
      handoffOperation("handoff-timeout-short", "pending", {
        material: { ...handoffMaterial(), login_timeout_seconds: 30 },
      }),
    ],
  },
  {
    name: "legacy subscription material carrying a login timeout",
    routes: [subscriptionRoute],
    operations: [
      subscriptionOperation("subscription-timeout-injected", "pending", {
        material: { ...subscriptionMaterial(), login_timeout_seconds: 600 },
      }),
    ],
  },
])("fails closed for $name", async ({ routes, operations }) => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({ operations, simulation_enabled: true, routes }),
    ),
  );
  render(
    <ModelAcceptanceOperations
      account={
        routes[0] &&
        "connection_mode" in routes[0] &&
        routes[0].connection_mode === "browser_handoff"
          ? handoffAccount
          : subscriptionAccount
      }
      refreshAccount={async () => {}}
    />,
  );

  expect(await screen.findByText("invalid_receipt")).toBeVisible();
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
  expect(
    screen.queryByRole("button", { name: "执行连接" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("link", { name: "打开本地合成授权页" }),
  ).not.toBeInTheDocument();
});

it.each([
  ["rejected", "rejected", "rejected", 0, 0],
  ["failed", "failed", "entitlement_denied", 1, 8],
  ["unknown", "unknown", "transport_unknown", 1, 8],
])(
  "keeps a subscription %s receipt terminal without an execute or retry control",
  async (_name, state, code, attempts, claimSequence) => {
    const terminal = subscriptionOperation(`subscription-${state}`, state, {
      attempts,
      claim_sequence: claimSequence,
      certainty: state === "unknown" ? "unknown" : "rejected",
      ...(code === "rejected" ? {} : { code }),
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        response({
          operations: [terminal],
          simulation_enabled: true,
          routes: [subscriptionRoute],
        }),
      ),
    );
    render(
      <ModelAcceptanceOperations
        account={subscriptionAccount}
        refreshAccount={async () => {}}
      />,
    );

    fireEvent.change(await screen.findByLabelText("连接已保存材料"), {
      target: { value: terminal.id },
    });
    if (attempts > 0) expect(screen.getByText(/单次尝试已消耗/)).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "执行连接" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /重试/ }),
    ).not.toBeInTheDocument();
  },
);

it("uses the highest positive connection claim sequence across reverse-ordered receipts and lets a later unknown invalidate success", async () => {
  const latestSuccess = operation(
    "connection-latest",
    "connection",
    "succeeded",
    {
      attempts: 1,
      claim_sequence: 12,
      certainty: "completed",
      result_account_revision: 1,
    },
  );
  const olderUnknown = operation("connection-older", "connection", "unknown", {
    attempts: 1,
    claim_sequence: 9,
    certainty: "unknown",
    code: "transport_unknown",
  });
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) => {
    if (!init?.method) return history([latestSuccess, olderUnknown]);
    return response({
      operation: operation("catalog-next", "catalog", "pending"),
    });
  });
  vi.stubGlobal("fetch", fetcher);
  const view = render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {}}
    />,
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "创建目录材料" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "创建目录材料" }));
  await waitFor(() =>
    expect(JSON.parse(String(fetcher.mock.calls[1][1]?.body))).toMatchObject({
      prerequisite_id: "connection-latest",
    }),
  );

  const laterUnknown = {
    ...olderUnknown,
    id: "connection-later-unknown",
    claim_sequence: 13,
  };
  view.rerender(
    <ModelAcceptanceOperations
      account={{ ...account, id: "second-account" }}
      refreshAccount={async () => {}}
    />,
  );
  fetcher.mockImplementationOnce(async () =>
    history([olderUnknown, latestSuccess, laterUnknown]),
  );
  view.rerender(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {}}
    />,
  );
  expect(await screen.findByText(/最新连接回执为结果未知/)).toBeVisible();
  expect(screen.getByRole("button", { name: "创建目录材料" })).toBeDisabled();
  expect(
    screen.queryByRole("button", { name: /重试/ }),
  ).not.toBeInTheDocument();
});

it("invalidates old downstream authority when the selected account has a newer revision", async () => {
  const freshAccount = { ...account, revision: 5, enabled: true };
  const oldConnection = operation(
    "connection-old-revision",
    "connection",
    "succeeded",
    {
      attempts: 1,
      claim_sequence: 40,
      certainty: "completed",
      result_account_revision: 1,
    },
  );
  const oldCatalog = operation("catalog-old-revision", "catalog", "succeeded", {
    attempts: 1,
    claim_sequence: 41,
    certainty: "completed",
    material: {
      ...material("catalog"),
      prerequisite_id: oldConnection.id,
      connection_id: oldConnection.id,
    },
    catalog: [{ id: "stale-model", display_name: "Stale model" }],
  });
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) => {
    if (!init?.method) return history([oldCatalog, oldConnection]);
    const body = JSON.parse(String(init.body));
    expect(body).toEqual({
      phase: "connection",
      route_id: route.id,
      expected_account_revision: 5,
    });
    return response({
      operation: operation("connection-current", "connection", "pending", {
        material: {
          ...material("connection"),
          account: freshAccount,
        },
      }),
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={freshAccount}
      refreshAccount={async () => {}}
    />,
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "创建连接材料" })).toBeEnabled(),
  );
  expect(screen.getByRole("button", { name: "创建目录材料" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "创建推理材料" })).toBeDisabled();
  expect(
    (screen.getByLabelText("推理模型") as HTMLSelectElement).options,
  ).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "创建连接材料" }));
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
});

it("approves and executes catalog and inference independently, and rejection never executes", async () => {
  const connection = operation("connection-ready", "connection", "succeeded", {
    attempts: 1,
    claim_sequence: 50,
    certainty: "completed",
    result_account_revision: 1,
  });
  let catalog = operation("catalog-flow", "catalog", "pending", {
    material: {
      ...material("catalog"),
      prerequisite_id: connection.id,
      connection_id: connection.id,
    },
  });
  let inferenceCount = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method) return history([connection]);
    const body = JSON.parse(String(init.body));
    if (path.endsWith("/acceptance-operations")) {
      if (body.phase === "catalog") return response({ operation: catalog });
      inferenceCount++;
      return response({
        operation: operation(
          inferenceCount === 1 ? "inference-flow" : "inference-rejected",
          "inference",
          "pending",
          {
            material: {
              ...material("inference"),
              prerequisite_id: catalog.id,
              connection_id: connection.id,
              model: "fixture-model",
              prompt_digest: "c".repeat(64),
            },
          },
        ),
      });
    }
    if (path.endsWith("/decision")) {
      const currentPhase = path.includes("catalog-flow")
        ? "catalog"
        : "inference";
      const id = path.includes("inference-rejected")
        ? "inference-rejected"
        : currentPhase === "catalog"
          ? "catalog-flow"
          : "inference-flow";
      if (body.decision === "reject")
        return response({
          operation: operation(id, currentPhase, "rejected", {
            revision: 2,
            certainty: "rejected",
            material:
              currentPhase === "catalog"
                ? catalog.material
                : {
                    ...material("inference"),
                    prerequisite_id: catalog.id,
                    connection_id: connection.id,
                    model: "fixture-model",
                    prompt_digest: "c".repeat(64),
                  },
          }),
        });
      if (currentPhase === "catalog") {
        catalog = { ...catalog, revision: 2, state: "approved" };
        return response({ operation: catalog });
      }
      return response({
        operation: operation(id, "inference", "approved", {
          revision: 2,
          material: {
            ...material("inference"),
            prerequisite_id: catalog.id,
            connection_id: connection.id,
            model: "fixture-model",
            prompt_digest: "c".repeat(64),
          },
        }),
      });
    }
    if (path.endsWith("/execute") && path.includes("catalog-flow")) {
      catalog = {
        ...catalog,
        revision: 4,
        state: "succeeded",
        attempts: 1,
        claim_sequence: 51,
        certainty: "completed",
        catalog: [{ id: "fixture-model", display_name: "Fixture model" }],
      };
      return response({ operation: catalog });
    }
    if (path.endsWith("/execute"))
      return response({
        operation: operation("inference-flow", "inference", "succeeded", {
          revision: 4,
          attempts: 1,
          claim_sequence: 52,
          certainty: "completed",
          material: {
            ...material("inference"),
            prerequisite_id: catalog.id,
            connection_id: connection.id,
            model: "fixture-model",
            prompt_digest: "c".repeat(64),
          },
        }),
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {}}
    />,
  );

  fireEvent.click(await screen.findByRole("button", { name: "创建目录材料" }));
  fireEvent.click(await screen.findByRole("button", { name: "批准目录材料" }));
  expect(await screen.findByRole("button", { name: "执行目录" })).toBeEnabled();
  fireEvent.click(screen.getByRole("button", { name: "执行目录" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "创建推理材料" })).toBeEnabled(),
  );

  fireEvent.click(screen.getByRole("button", { name: "创建推理材料" }));
  fireEvent.click(await screen.findByRole("button", { name: "批准推理材料" }));
  expect(await screen.findByRole("button", { name: "执行推理" })).toBeEnabled();
  fireEvent.click(screen.getByRole("button", { name: "执行推理" }));
  expect(await screen.findByText(/推理 · 已成功 · 本地模拟/)).toBeVisible();

  fireEvent.click(screen.getByRole("button", { name: "创建推理材料" }));
  fireEvent.click(await screen.findByRole("button", { name: "拒绝推理材料" }));
  expect(await screen.findByText(/推理 · 已拒绝 · 本地模拟/)).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "执行推理" }),
  ).not.toBeInTheDocument();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(8);
});

it("offers inference models only from the exact successful catalog and renders terminal denials without retry", async () => {
  const connection = operation("connection-ok", "connection", "succeeded", {
    attempts: 1,
    claim_sequence: 4,
    certainty: "completed",
    result_account_revision: 1,
  });
  const catalog = operation("catalog-ok", "catalog", "succeeded", {
    attempts: 1,
    claim_sequence: 5,
    certainty: "completed",
    material: {
      ...material("catalog"),
      prerequisite_id: connection.id,
      connection_id: connection.id,
    },
    catalog: [
      { id: "fixture-model-a", display_name: "Fixture A" },
      { id: "fixture-model-b", display_name: "Fixture B" },
    ],
  });
  const receipts = [
    operation("rejected-1", "connection", "rejected", {
      certainty: "rejected",
    }),
    operation("failed-1", "inference", "failed", {
      attempts: 1,
      claim_sequence: 6,
      certainty: "rejected",
      code: "quota_exhausted",
    }),
    operation("unknown-1", "inference", "unknown", {
      attempts: 1,
      claim_sequence: 7,
      certainty: "unknown",
      code: "response_incomplete",
    }),
    operation("review-inference", "inference", "pending", {
      material: {
        ...material("inference"),
        prerequisite_id: catalog.id,
        connection_id: connection.id,
        model: "fixture-model-a",
        prompt_digest: "b".repeat(64),
      },
    }),
    catalog,
    connection,
  ];
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) => {
    if (!init?.method) return history(receipts);
    const body = JSON.parse(String(init.body));
    expect(body).toMatchObject({
      phase: "inference",
      prerequisite_id: "catalog-ok",
      model: "fixture-model-b",
    });
    return response({
      operation: operation("inference-next", "inference", "pending"),
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {}}
    />,
  );
  const models = await screen.findByLabelText("推理模型");
  expect(
    Array.from((models as HTMLSelectElement).options).map((o) => o.value),
  ).toEqual(["fixture-model-a", "fixture-model-b"]);
  fireEvent.change(models, { target: { value: "fixture-model-b" } });
  fireEvent.click(screen.getByRole("button", { name: "创建推理材料" }));
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  expect(screen.getAllByText(/已拒绝/)[0]).toBeVisible();
  expect(screen.getByText(/quota_exhausted/)).toBeVisible();
  expect(screen.getByText(/response_incomplete/)).toBeVisible();
  expect(
    screen.queryByRole("button", { name: /重试/ }),
  ).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("推理已保存材料"), {
    target: { value: "review-inference" },
  });
  expect(await screen.findByText("fixture-model-a")).toBeVisible();
  expect(screen.getByText("b".repeat(64))).toBeVisible();
});

it("keeps production visibly blocked with no enabled mutation action", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => history([], false)),
  );
  render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {}}
    />,
  );
  expect(await screen.findByText("生产真实访问保持关闭")).toBeVisible();
  for (const button of screen.getAllByRole("button"))
    expect(button).toBeDisabled();
});

it("discards an older account read that settles after the newly selected account", async () => {
  let finishOld!: (value: Response) => void;
  const oldRead = new Promise<Response>((resolve) => {
    finishOld = resolve;
  });
  const second = { ...account, id: "acceptance-gate-kimi", revision: 3 };
  const secondRoute = { ...route, id: "acceptance-gate-kimi-route" };
  const fetcher = vi.fn(async (path: string) =>
    path.includes(second.id)
      ? response({
          operations: [],
          simulation_enabled: true,
          routes: [secondRoute],
        })
      : oldRead,
  );
  vi.stubGlobal("fetch", fetcher);
  const view = render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {}}
    />,
  );
  view.rerender(
    <ModelAcceptanceOperations
      account={second}
      refreshAccount={async () => {}}
    />,
  );
  await waitFor(() =>
    expect(screen.getByLabelText("本地模拟路由")).toHaveValue(secondRoute.id),
  );
  await act(async () => finishOld(history()));
  expect(screen.getByLabelText("本地模拟路由")).toHaveValue(secondRoute.id);
  expect(screen.queryByText(route.id)).not.toBeInTheDocument();
});

it("locks every mutation after a lost decision response until an explicit GET reads that saved receipt", async () => {
  const pending = subscriptionOperation("pending-1", "pending");
  let detailReads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method) {
      if (path.endsWith("/pending-1")) {
        detailReads++;
        return response({ operation: pending });
      }
      return response({
        operations: [pending],
        simulation_enabled: true,
        routes: [subscriptionRoute],
      });
    }
    throw new Error("synthetic network loss");
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={subscriptionAccount}
      refreshAccount={async () => {}}
    />,
  );
  await screen.findByRole("option", { name: "pending-1 · 待审批" });
  fireEvent.change(await screen.findByLabelText("连接已保存材料"), {
    target: { value: pending.id },
  });
  fireEvent.click(await screen.findByRole("button", { name: "批准连接材料" }));
  expect(await screen.findByText("network_unknown")).toBeVisible();
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "批准连接材料" })).toBeDisabled();
  const read = screen.getByRole("button", { name: "仅读取原验收记录" });
  fireEvent.click(read);
  await waitFor(() => expect(detailReads).toBe(1));
  expect(screen.getByRole("button", { name: "批准连接材料" })).toBeEnabled();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it("clears apparent eligibility after a failed recovery GET and unlocks only after a later complete GET recovery", async () => {
  const pending = operation("recovery-pending", "connection", "pending");
  let detailReads = 0;
  let historyReads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (init?.method) throw new Error("synthetic lost decision response");
    if (path.endsWith(`/${pending.id}`)) {
      detailReads++;
      return detailReads === 1
        ? response(
            {
              error: {
                code: "acceptance_record_unavailable",
                message: "untrusted storage detail",
              },
            },
            503,
          )
        : response({ operation: pending });
    }
    historyReads++;
    return history([pending]);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {}}
    />,
  );
  await screen.findByRole("option", { name: "recovery-pending · 待审批" });
  fireEvent.change(await screen.findByLabelText("连接已保存材料"), {
    target: { value: pending.id },
  });
  fireEvent.click(await screen.findByRole("button", { name: "批准连接材料" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "仅读取原验收记录" }),
  );
  expect(
    await screen.findByText("acceptance_record_unavailable"),
  ).toBeVisible();
  expect(screen.queryAllByText(pending.id)).toHaveLength(0);
  expect(screen.queryByText(route.id)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
  const retryRead = screen.getByRole("button", { name: "仅读取原验收记录" });
  expect(retryRead).toBeEnabled();
  fireEvent.click(retryRead);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "批准连接材料" })).toBeEnabled(),
  );
  expect(screen.getByLabelText("本地模拟路由")).toHaveValue(route.id);
  expect(detailReads).toBe(2);
  expect(historyReads).toBe(2);
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it("keeps a newer consumed history receipt when recovery detail is stale", async () => {
  const olderSuccess = operation(
    "connection-before-recovery-race",
    "connection",
    "succeeded",
    {
      revision: 4,
      attempts: 1,
      claim_sequence: 60,
      certainty: "completed",
      result_account_revision: 1,
    },
  );
  const pending = operation(
    "connection-recovery-race",
    "connection",
    "pending",
  );
  const staleDetail = operation(pending.id, "connection", "approved", {
    revision: 2,
  });
  const newerUnknown = operation(pending.id, "connection", "unknown", {
    revision: 4,
    attempts: 1,
    claim_sequence: 61,
    certainty: "unknown",
    code: "transport_unknown",
  });
  let historyReads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (init?.method) throw new Error("synthetic lost decision response");
    if (path.endsWith(`/${pending.id}`))
      return response({ operation: staleDetail });
    historyReads++;
    return historyReads === 1
      ? history([pending, olderSuccess])
      : history([newerUnknown, olderSuccess]);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {}}
    />,
  );
  await screen.findByRole("option", {
    name: "connection-recovery-race · 待审批",
  });
  fireEvent.change(screen.getByLabelText("连接已保存材料"), {
    target: { value: pending.id },
  });
  fireEvent.click(screen.getByRole("button", { name: "批准连接材料" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "仅读取原验收记录" }),
  );

  expect(await screen.findByText(/连接 · 结果未知 · 本地模拟/)).toBeVisible();
  expect(screen.getByRole("button", { name: "创建目录材料" })).toBeDisabled();
  expect(screen.getByLabelText("连接已保存材料")).toHaveValue(pending.id);
  expect(screen.getByText("transport_unknown")).toBeVisible();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it("locks stale conflict material until the user explicitly reads the original receipt", async () => {
  const pending = operation("stale-1", "connection", "pending");
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method)
      return path.endsWith("/stale-1")
        ? response({ operation: pending })
        : history([pending]);
    return response(
      {
        error: {
          code: "acceptance_conflict",
          message: "untrusted stale details",
        },
      },
      409,
    );
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {}}
    />,
  );
  await screen.findByRole("option", { name: "stale-1 · 待审批" });
  fireEvent.change(await screen.findByLabelText("连接已保存材料"), {
    target: { value: pending.id },
  });
  fireEvent.click(await screen.findByRole("button", { name: "批准连接材料" }));
  expect(await screen.findByText("acceptance_conflict")).toBeVisible();
  expect(document.body.textContent).not.toContain("untrusted stale details");
  expect(screen.getByRole("button", { name: "批准连接材料" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "仅读取原验收记录" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "批准连接材料" })).toBeEnabled(),
  );
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it("keeps a succeeded connection receipt authoritative when only the account-detail refresh fails", async () => {
  const approved = operation("approved-connection", "connection", "approved", {
    revision: 2,
  });
  const succeeded = {
    ...approved,
    revision: 4,
    state: "succeeded",
    attempts: 1,
    claim_sequence: 20,
    certainty: "completed",
    result_account_revision: 2,
  };
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) =>
    init?.method ? response({ operation: succeeded }) : history([approved]),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {
        throw new Error("account detail unavailable");
      }}
    />,
  );
  fireEvent.change(await screen.findByLabelText("连接已保存材料"), {
    target: { value: approved.id },
  });
  fireEvent.click(await screen.findByRole("button", { name: "执行连接" }));
  expect(await screen.findByText(/连接 · 已成功 · 本地模拟/)).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "仅读取原验收记录" }),
  ).not.toBeInTheDocument();
  expect(screen.getByText("account_refresh_failed")).toBeVisible();
});

it("clears healthy-looking history after a failed explicit read and shows only the safe server code", async () => {
  let reads = 0;
  const fetcher = vi.fn(async () =>
    ++reads === 1
      ? history([
          operation("pending-safe", "connection", "succeeded", {
            attempts: 1,
            claim_sequence: 31,
            certainty: "completed",
            result_account_revision: 1,
          }),
        ])
      : response(
          {
            error: {
              code: "acceptance_record_unavailable",
              message: "raw provider body fake-test-secret",
            },
          },
          503,
        ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {}}
    />,
  );
  await screen.findByText("pending-safe");
  fireEvent.click(screen.getByRole("button", { name: "重新读取验收记录" }));
  expect(
    await screen.findByText("acceptance_record_unavailable"),
  ).toBeVisible();
  expect(screen.queryByText("pending-safe")).not.toBeInTheDocument();
  expect(document.body.textContent).not.toContain("fake-test-secret");
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
});

it("fails closed instead of rendering an unbounded receipt error code", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      history([
        operation("unsafe-code", "inference", "failed", {
          attempts: 1,
          claim_sequence: 44,
          certainty: "rejected",
          code: "raw provider body fake-test-secret",
        }),
      ]),
    ),
  );
  render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {}}
    />,
  );
  expect(await screen.findByText("invalid_receipt")).toBeVisible();
  expect(document.body.textContent).not.toContain("fake-test-secret");
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
});

it("keeps identity-only connection review explicit and never unlocks model phases", async () => {
  const identityAccount = {
    ...account,
    id: "acceptance-gate-openai-identity",
    name: "SYNTHETIC OpenAI — identity only",
  };
  const identityRoute = {
    ...route,
    id: "acceptance-gate-openai-identity-route",
    connection_purpose: "identity_only",
  };
  const identityMaterial = () => ({
    ...material("connection"),
    account: identityAccount,
    route: {
      ...identityRoute,
      account_id: identityAccount.id,
      protocol: "responses",
      extra_usage: "off",
      created_at: "2026-10-19T00:00:00Z",
    },
    connection_purpose: "identity_only",
    requested_scopes: ["openid", "profile", "email"],
  });
  let identityOperation: ReturnType<typeof operation> & {
    result_account_revision?: number;
  } = {
    ...operation("identity-connection", "connection", "pending"),
    account_id: identityAccount.id,
    material: identityMaterial(),
  };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method)
      return response({
        operations: [],
        simulation_enabled: true,
        routes: [identityRoute],
      });
    const body = JSON.parse(String(init.body));
    if (path.endsWith("/acceptance-operations")) {
      expect(body).toEqual({
        phase: "connection",
        route_id: identityRoute.id,
        expected_account_revision: 1,
        connection_purpose: "identity_only",
      });
      return response({ operation: identityOperation });
    }
    if (path.endsWith("/decision")) {
      expect(body).toEqual({
        expected_revision: 1,
        material_digest: digest,
        decision: "approve",
      });
      identityOperation = {
        ...identityOperation,
        revision: 2,
        state: "approved",
      };
      return response({ operation: identityOperation });
    }
    if (path.endsWith("/execute")) {
      expect(body).toEqual({ expected_revision: 2, material_digest: digest });
      identityOperation = {
        ...identityOperation,
        revision: 3,
        state: "succeeded",
        attempts: 1,
        claim_sequence: 1,
        certainty: "completed",
        result_account_revision: 1,
      };
      return response({ operation: identityOperation });
    }
    throw new Error(`unexpected ${path}`);
  });
  const refreshAccount = vi.fn(async () => {});
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={identityAccount}
      refreshAccount={refreshAccount}
    />,
  );

  expect(await screen.findByText(/本地身份专用模拟/)).toBeVisible();
  expect(screen.getByText(/openid.*profile.*email/)).toBeVisible();
  expect(screen.getByRole("button", { name: "创建目录材料" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "创建推理材料" })).toBeDisabled();
  expect(
    screen.queryByRole("button", { name: "执行连接" }),
  ).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "创建连接材料" }));
  fireEvent.click(await screen.findByRole("button", { name: "批准连接材料" }));
  await screen.findByRole("button", { name: "执行连接" });
  expect(
    fetcher.mock.calls.some(([path]) => String(path).endsWith("/execute")),
  ).toBe(false);

  fireEvent.click(screen.getByRole("button", { name: "执行连接" }));
  await screen.findByText(/连接 · 已成功 · 本地模拟/);
  expect(
    fetcher.mock.calls.filter(([path]) => String(path).endsWith("/execute")),
  ).toHaveLength(1);
  expect(refreshAccount).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "创建目录材料" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "创建推理材料" })).toBeDisabled();
});

it("fails closed when an identity proposal returns material for a different trusted route", async () => {
  const identityAccount = {
    ...account,
    id: "acceptance-gate-openai-identity",
  };
  const identityRoute = {
    ...route,
    id: "acceptance-gate-openai-identity-route",
    connection_purpose: "identity_only",
  };
  const mismatched = {
    ...operation("identity-mismatch", "connection", "pending"),
    account_id: identityAccount.id,
    material: {
      ...material("connection"),
      account: identityAccount,
      route: {
        ...identityRoute,
        account_id: identityAccount.id,
        protocol: "responses",
        extra_usage: "off",
        created_at: "2026-10-19T00:00:00Z",
        base_url: "http://127.0.0.1:43124/v1",
      },
      connection_purpose: "identity_only",
      requested_scopes: ["openid", "profile", "email"],
    },
  };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method)
      return response({
        operations: [],
        simulation_enabled: true,
        routes: [identityRoute],
      });
    if (path.endsWith("/acceptance-operations"))
      return response({ operation: mismatched });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={identityAccount}
      refreshAccount={async () => {}}
    />,
  );

  fireEvent.click(await screen.findByRole("button", { name: "创建连接材料" }));
  expect(await screen.findByText("invalid_receipt")).toBeVisible();
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
  expect(document.body.textContent).not.toContain("43124");
});

it("fails closed when reconciliation returns mismatched identity material", async () => {
  const identityAccount = {
    ...account,
    id: "acceptance-gate-openai-identity",
  };
  const identityRoute = {
    ...route,
    id: "acceptance-gate-openai-identity-route",
    connection_purpose: "identity_only" as const,
  };
  const pending = {
    ...operation("identity-recovery", "connection", "pending"),
    account_id: identityAccount.id,
    material: {
      ...material("connection"),
      account: identityAccount,
      route: {
        ...identityRoute,
        account_id: identityAccount.id,
        protocol: "responses",
        extra_usage: "off",
        created_at: "2026-10-19T00:00:00Z",
      },
      connection_purpose: "identity_only" as const,
      requested_scopes: ["openid", "profile", "email"] as const,
    },
  };
  const mismatched = {
    ...pending,
    material: {
      ...pending.material,
      route: { ...pending.material.route, adapter: "untrusted_adapter" },
    },
  };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (init?.method) throw new Error("synthetic lost decision response");
    if (path.endsWith(`/${pending.id}`))
      return response({ operation: mismatched });
    return response({
      operations: [pending],
      simulation_enabled: true,
      routes: [identityRoute],
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={identityAccount}
      refreshAccount={async () => {}}
    />,
  );

  fireEvent.change(await screen.findByLabelText("连接已保存材料"), {
    target: { value: pending.id },
  });
  fireEvent.click(screen.getByRole("button", { name: "批准连接材料" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "仅读取原验收记录" }),
  );
  expect(await screen.findByText("invalid_receipt")).toBeVisible();
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
  expect(document.body.textContent).not.toContain("untrusted_adapter");
});

it("retains read-only historical evidence when simulation is disabled", async () => {
  const completed = {
    ...operation("read-only-connection", "connection", "succeeded", {
      attempts: 1,
      claim_sequence: 1,
      certainty: "completed",
      result_account_revision: 1,
    }),
    material: {
      ...material("connection"),
      connection_purpose: "identity_only" as const,
      requested_scopes: ["openid", "profile", "email"] as const,
    },
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        operations: [completed],
        simulation_enabled: false,
        routes: [],
      }),
    ),
  );
  render(
    <ModelAcceptanceOperations
      account={account}
      refreshAccount={async () => {}}
    />,
  );

  expect(await screen.findByText("read-only-connection")).toBeVisible();
  expect(screen.queryByText("invalid_receipt")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "创建目录材料" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "创建推理材料" })).toBeDisabled();
});

it.each([
  ["external host", "https://example.invalid/v1"],
  ["localhost alias", "http://localhost:43123/v1"],
  ["query string", "http://127.0.0.1:43123/v1?mode=unsafe"],
  ["userinfo", "http://user@127.0.0.1:43123/v1"],
  ["non-v1 path", "http://127.0.0.1:43123/"],
])("fails closed for a subscription route with %s", async (_name, baseURL) => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        operations: [],
        simulation_enabled: true,
        routes: [{ ...subscriptionRoute, base_url: baseURL }],
      }),
    ),
  );
  render(
    <ModelAcceptanceOperations
      account={subscriptionAccount}
      refreshAccount={async () => {}}
    />,
  );

  expect(await screen.findByText("invalid_receipt")).toBeVisible();
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
  expect(document.body.textContent).not.toContain(baseURL);
});

it("fails closed when subscription material names an account endpoint outside its trusted route", async () => {
  const mismatchedAccount = subscriptionOperation(
    "subscription-account-endpoint-mismatch",
    "pending",
    {
      material: {
        ...subscriptionMaterial(),
        account: {
          ...subscriptionAccount,
          base_url: "https://example.invalid/v1",
        },
      },
    },
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        operations: [mismatchedAccount],
        simulation_enabled: true,
        routes: [subscriptionRoute],
      }),
    ),
  );
  render(
    <ModelAcceptanceOperations
      account={subscriptionAccount}
      refreshAccount={async () => {}}
    />,
  );

  expect(await screen.findByText("invalid_receipt")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "批准连接材料" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
});

it("fails closed for a spent approval instead of exposing execute or replay actions", async () => {
  const spentApproval = subscriptionOperation(
    "spent-subscription-approval",
    "approved",
    {
      attempts: 1,
      claim_sequence: 9,
      certainty: "unknown",
    },
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        operations: [spentApproval],
        simulation_enabled: true,
        routes: [subscriptionRoute],
      }),
    ),
  );
  render(
    <ModelAcceptanceOperations
      account={subscriptionAccount}
      refreshAccount={async () => {}}
    />,
  );

  expect(await screen.findByText("invalid_receipt")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "执行连接" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /重试/ }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
});

it.each([
  {
    policy: "subscription_only" as const,
    state: "pending",
    field: "model",
    kind: "nonempty string",
    value: "gpt-forbidden",
  },
  {
    policy: "identity_only" as const,
    state: "approved",
    field: "model",
    kind: "malformed type",
    value: 7,
  },
  {
    policy: "subscription_only" as const,
    state: "approved",
    field: "prerequisite_id",
    kind: "nonempty string",
    value: "forbidden-prerequisite",
  },
  {
    policy: "identity_only" as const,
    state: "pending",
    field: "prerequisite_id",
    kind: "malformed type",
    value: ["forbidden"],
  },
  {
    policy: "subscription_only" as const,
    state: "pending",
    field: "connection_id",
    kind: "nonempty string",
    value: "forbidden-connection",
  },
  {
    policy: "identity_only" as const,
    state: "approved",
    field: "connection_id",
    kind: "malformed type",
    value: { id: "forbidden" },
  },
  {
    policy: "subscription_only" as const,
    state: "approved",
    field: "prompt_digest",
    kind: "nonempty string",
    value: "b".repeat(64),
  },
  {
    policy: "identity_only" as const,
    state: "pending",
    field: "prompt_digest",
    kind: "malformed type",
    value: true,
  },
])(
  "fails closed when explicit $policy $state material carries $field as a $kind",
  async ({ policy, state, field, value }) => {
    const identityAccount = {
      ...account,
      id: "acceptance-gate-openai-identity",
    };
    const identityRoute = {
      ...route,
      id: "acceptance-gate-openai-identity-route",
      connection_purpose: "identity_only" as const,
    };
    const selectedAccount =
      policy === "subscription_only" ? subscriptionAccount : identityAccount;
    const selectedRoute =
      policy === "subscription_only" ? subscriptionRoute : identityRoute;
    const explicitMaterial = {
      ...material("connection"),
      account: selectedAccount,
      route: {
        ...selectedRoute,
        account_id: selectedAccount.id,
        protocol: "responses",
        extra_usage: "off",
        created_at: "2026-10-19T00:00:00Z",
      },
      connection_purpose: policy,
      requested_scopes:
        policy === "subscription_only"
          ? subscriptionScopes
          : (["openid", "profile", "email"] as const),
      ...(policy === "subscription_only"
        ? { requested_resource: "https://api.openai.com/v1" as const }
        : {}),
      [field]: value,
    };
    const malformed = {
      ...operation(`forbidden-${field}-${policy}`, "connection", state),
      account_id: selectedAccount.id,
      material: explicitMaterial,
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        response({
          operations: [malformed],
          simulation_enabled: true,
          routes: [selectedRoute],
        }),
      ),
    );
    render(
      <ModelAcceptanceOperations
        account={selectedAccount}
        refreshAccount={async () => {}}
      />,
    );

    expect(await screen.findByText("invalid_receipt")).toBeVisible();
    expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: "批准连接材料" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "执行连接" }),
    ).not.toBeInTheDocument();
  },
);

it.each([
  {
    name: "unknown route purpose",
    routes: [{ ...route, connection_purpose: "model_use" }],
    operations: [],
    raw: "model_use",
  },
  {
    name: "incomplete identity scopes",
    routes: [{ ...route, connection_purpose: "identity_only" }],
    operations: [
      operation("malformed-identity", "connection", "pending", {
        material: {
          ...material("connection"),
          connection_purpose: "identity_only",
          requested_scopes: ["openid"],
        },
      }),
    ],
    raw: "malformed-identity",
  },
  {
    name: "reordered subscription scopes",
    routes: [subscriptionRoute],
    operations: [
      {
        ...subscriptionOperation("malformed-subscription-scopes", "pending"),
        material: {
          ...subscriptionMaterial(),
          requested_scopes: [
            "profile",
            "openid",
            "email",
            "offline_access",
            "resource.invoke",
            "chatgpt.tokens.use.direct",
          ],
        },
      },
    ],
    raw: "malformed-subscription-scopes",
  },
  {
    name: "subscription resource replaced with the loopback endpoint",
    routes: [subscriptionRoute],
    operations: [
      {
        ...subscriptionOperation("malformed-subscription-resource", "pending"),
        material: {
          ...subscriptionMaterial(),
          requested_resource: subscriptionRoute.base_url,
        },
      },
    ],
    raw: "malformed-subscription-resource",
  },
  {
    name: "subscription receipt carrying catalog output",
    routes: [subscriptionRoute],
    operations: [
      subscriptionOperation("subscription-with-output", "succeeded", {
        attempts: 1,
        claim_sequence: 3,
        certainty: "completed",
        result_account_revision: 1,
        catalog: [{ id: "unsafe-catalog", display_name: "Unsafe catalog" }],
      }),
    ],
    raw: "unsafe-catalog",
  },
  {
    name: "subscription route with the wrong adapter",
    routes: [{ ...subscriptionRoute, adapter: "untrusted_adapter" }],
    operations: [],
    raw: "untrusted_adapter",
  },
  {
    name: "mixed identity and subscription route purposes",
    routes: [
      { ...route, connection_purpose: "identity_only" },
      { ...subscriptionRoute, id: "mixed-subscription-route" },
    ],
    operations: [],
    raw: "mixed-subscription-route",
  },
  {
    name: "malformed legacy capability",
    routes: [],
    operations: [],
    simulation_enabled: false,
    legacy_siwc_simulation_enabled: "enabled",
    raw: "enabled",
  },
])(
  "fails closed for $name instead of treating it as a legacy route",
  async ({
    routes,
    operations,
    raw,
    simulation_enabled = true,
    legacy_siwc_simulation_enabled,
  }) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        response({
          operations,
          simulation_enabled,
          legacy_siwc_simulation_enabled,
          routes,
        }),
      ),
    );
    render(
      <ModelAcceptanceOperations
        account={account}
        refreshAccount={async () => {}}
      />,
    );

    expect(await screen.findByText("invalid_receipt")).toBeVisible();
    expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "创建目录材料" })).toBeDisabled();
    expect(document.body.textContent).not.toContain(raw);
  },
);
