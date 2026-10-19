import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { response } from "../test/fixtures";
import type { ModelAccount, ModelRuntime } from "../types";
import { ModelAuthorizationCard } from "./ModelAuthorizationCard";

const account: ModelAccount = {
  id: "siwc-account",
  revision: 7,
  name: "OpenAI subscription",
  provider: "openai",
  base_url: "https://api.openai.com/v1",
  auth_method: "openai_siwc",
  billing_mode: "subscription",
  enabled: true,
  current_credential_version: 0,
  created_at: "2026-10-19T00:00:00Z",
  updated_at: "2026-10-19T00:00:00Z",
  identity_status: "unverified",
  usage_status: "unknown",
  inference_enabled: false,
};

const runtime = (simulation_authorization: boolean): ModelRuntime => ({
  live_authorization: false,
  live_discovery: false,
  live_inference: false,
  simulation_inference: false,
  simulation_authorization,
  automatic_paid_fallback: false,
  adapters: [],
});

const authorization = {
  id: "authorization-one",
  account_id: account.id,
  issuer: "http://127.0.0.1:9911",
  subject: "synthetic-subject",
  client_id: "synthetic-client",
  scopes: [
    "openid",
    "offline_access",
    "resource.invoke",
    "chatgpt.tokens.use.direct",
  ],
  spend_class: "subscription_only",
  state: "available",
  generation: 3,
  expires_at: "2026-10-19T02:00:00Z",
};

it("reads safe history without mutations and keeps production login and refresh closed", async () => {
  const storage = vi.spyOn(Storage.prototype, "setItem");
  const fetcher = vi.fn(async (_path: string) =>
    response({ authorizations: [authorization], remote_revocations: [] }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<ModelAuthorizationCard account={account} runtime={runtime(false)} />);

  expect(await screen.findByText("authorization-one")).toBeVisible();
  expect(screen.getByText(/代数 3/)).toBeVisible();
  expect(screen.getByText(/直接调用权限已记录/)).toBeVisible();
  expect(
    screen.getByRole("button", { name: "真实登录（未开放）" }),
  ).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "真实刷新（未开放）" }),
  ).toBeDisabled();
  expect(screen.getByText(/账户路由的真实登录与刷新保持关闭/)).toBeVisible();
  expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
    "/api/model-authorizations",
  ]);
  expect(storage).not.toHaveBeenCalled();
});

it("starts, checks, and cancels a mock flow only after explicit clicks with exact requests", async () => {
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/model-authorizations" && !init?.method)
      return response({ authorizations: [], remote_revocations: [] });
    if (path === "/api/model-authorizations/start")
      return response({
        flow: {
          operation_id: "mock-flow",
          state: "waiting",
          handoff_url: "http://127.0.0.1:9876/auth/start",
          expires_at: "2026-10-19T03:00:00Z",
        },
      });
    if (
      path === "/api/model-authorizations/operations/mock-flow" &&
      !init?.method
    )
      return response({
        flow: {
          operation_id: "mock-flow",
          state: "exchanging",
          handoff_url: "",
          expires_at: "2026-10-19T03:00:00Z",
        },
      });
    if (path === "/api/model-authorizations/operations/mock-flow/cancel")
      return response({
        flow: {
          operation_id: "mock-flow",
          state: "canceled",
          handoff_url: "",
          expires_at: "2026-10-19T03:00:00Z",
        },
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<ModelAuthorizationCard account={account} runtime={runtime(true)} />);
  await screen.findByText("尚无此账户的授权历史");
  expect(fetcher).toHaveBeenCalledTimes(1);

  fireEvent.click(screen.getByRole("button", { name: "开始本地模拟登录" }));
  const handoff = await screen.findByRole("link", {
    name: "打开本地模拟登录交接页",
  });
  expect(handoff).toHaveAttribute("href", "http://127.0.0.1:9876/auth/start");
  expect(handoff).toHaveAttribute("target", "_blank");
  let call = fetcher.mock.calls[1];
  expect(call[0]).toBe("/api/model-authorizations/start");
  expect(JSON.parse(String(call[1]?.body))).toEqual({
    account_id: account.id,
    expected_account_revision: 7,
  });

  fireEvent.click(screen.getByRole("button", { name: "检查本地模拟登录状态" }));
  expect(await screen.findByText(/exchanging/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "取消本地模拟登录" }));
  expect(await screen.findByText(/canceled/)).toBeVisible();
  call = fetcher.mock.calls.at(-1)!;
  expect(call[0]).toBe("/api/model-authorizations/operations/mock-flow/cancel");
  expect(JSON.parse(String(call[1]?.body))).toEqual({});
});

it.each([
  "https://auth.openai.com/authorize",
  "javascript:alert(1)",
  "http://127.0.0.1:9876/auth/start?state=secret",
  "http://127.0.0.1:9876/auth/start#fragment",
  "http://user@127.0.0.1:9876/auth/start",
  "http://127.0.0.1:09876/auth/start",
])(
  "does not render an unsafe handoff as an actionable link: %s",
  async (handoff_url) => {
    const fetcher = vi.fn(async (_path: string, init?: RequestInit) =>
      init?.method === "POST"
        ? response({
            flow: {
              operation_id: "unsafe-flow",
              state: "waiting",
              handoff_url,
              expires_at: "2026-10-19T03:00:00Z",
            },
          })
        : response({ authorizations: [], remote_revocations: [] }),
    );
    vi.stubGlobal("fetch", fetcher);
    render(
      <ModelAuthorizationCard account={account} runtime={runtime(true)} />,
    );
    await screen.findByText("尚无此账户的授权历史");
    fireEvent.click(screen.getByRole("button", { name: "开始本地模拟登录" }));
    expect(await screen.findByText(/交接链接未通过本地安全检查/)).toBeVisible();
    expect(
      screen.queryByRole("link", { name: "打开本地模拟登录交接页" }),
    ).not.toBeInTheDocument();
  },
);

it("refreshes only with offline access and the exact account revision", async () => {
  const refreshed = {
    ...authorization,
    generation: 4,
    expires_at: "2026-10-19T04:00:00Z",
  };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) =>
    init?.method === "POST"
      ? response({ authorization: refreshed })
      : response({ authorizations: [authorization], remote_revocations: [] }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<ModelAuthorizationCard account={account} runtime={runtime(true)} />);
  fireEvent.click(
    await screen.findByRole("button", { name: "刷新本地模拟授权" }),
  );
  expect(await screen.findByText(/代数 4/)).toBeVisible();
  const post = fetcher.mock.calls.find(([, init]) => init?.method === "POST")!;
  expect(post[0]).toBe("/api/model-authorizations/authorization-one/refresh");
  expect(JSON.parse(String(post[1]?.body))).toEqual({
    expected_account_revision: 7,
  });
});

it("invalidates stale availability and leaves history unknown when refresh reconciliation fails", async () => {
  let reads = 0;
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) => {
    if (init?.method === "POST")
      return response(
        {
          error: {
            code: "transport_unknown",
            message: "untrusted provider detail",
          },
        },
        503,
      );
    reads++;
    return reads === 1
      ? response({ authorizations: [authorization], remote_revocations: [] })
      : response(
          {
            error: {
              code: "model_authorization_unavailable",
              message: "untrusted storage detail",
            },
          },
          503,
        );
  });
  vi.stubGlobal("fetch", fetcher);
  render(<ModelAuthorizationCard account={account} runtime={runtime(true)} />);

  fireEvent.click(
    await screen.findByRole("button", { name: "刷新本地模拟授权" }),
  );

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "授权结果未知；不会自动重试",
  );
  expect(screen.getByText(/授权历史尚未读取/)).toBeVisible();
  expect(screen.getByText("本地状态：refresh_unknown")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "刷新本地模拟授权" }),
  ).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "撤销授权 authorization-one" }),
  ).toBeEnabled();
  expect(reads).toBe(2);
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
  expect(document.body.textContent).not.toContain("untrusted");
});

it("allows exact-ID revocation of retained refresh-unknown and failed authorizations", async () => {
  const refreshUnknown = { ...authorization, state: "refresh_unknown" };
  const failed = {
    ...authorization,
    id: "authorization-failed",
    state: "failed",
  };
  let revoked = false;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (init?.method === "POST") {
      revoked = true;
      return response({
        revocation: {
          authorization_id: failed.id,
          local_state: "revoked",
          remote_state: "confirmed",
          updated_at: "2026-10-19T05:00:00Z",
        },
      });
    }
    return response({
      authorizations: [
        refreshUnknown,
        revoked ? { ...failed, state: "revoked" } : failed,
      ],
      remote_revocations: revoked
        ? [
            {
              authorization_id: failed.id,
              outcome: "confirmed",
              updated_at: "2026-10-19T05:00:00Z",
            },
          ]
        : [],
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(<ModelAuthorizationCard account={account} runtime={runtime(true)} />);

  expect(
    await screen.findByRole("button", {
      name: "撤销授权 authorization-one",
    }),
  ).toBeEnabled();
  fireEvent.click(
    screen.getByRole("button", {
      name: "撤销授权 authorization-failed",
    }),
  );
  expect(await screen.findByText("本地状态：revoked")).toBeVisible();
  const post = fetcher.mock.calls.find(([, init]) => init?.method === "POST")!;
  expect(post[0]).toBe("/api/model-authorizations/authorization-failed/revoke");
  expect(JSON.parse(String(post[1]?.body))).toEqual({
    expected_account_revision: 7,
  });
});

it("preserves durable local revocation when the remote production revoke is unconfirmed", async () => {
  let revoked = false;
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) => {
    if (init?.method === "POST") {
      revoked = true;
      return response(
        {
          error: {
            code: "model_authorization_unavailable",
            message: "safe finite message",
          },
          revocation: {
            authorization_id: authorization.id,
            local_state: "revoked",
            remote_state: "unconfirmed",
            updated_at: "",
          },
        },
        503,
      );
    }
    return response({
      authorizations: [
        revoked ? { ...authorization, state: "revoked" } : authorization,
      ],
      remote_revocations: [],
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(<ModelAuthorizationCard account={account} runtime={runtime(false)} />);
  fireEvent.click(
    await screen.findByRole("button", {
      name: "撤销授权 authorization-one",
    }),
  );
  expect(await screen.findByText(/本地撤销已持久保存/)).toBeVisible();
  expect(screen.getAllByText(/远端撤销未确认/)).toHaveLength(2);
  expect(screen.getByText(/本地状态：revoked/)).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "撤销授权 authorization-one" }),
  ).not.toBeInTheDocument();
  const post = fetcher.mock.calls.find(([, init]) => init?.method === "POST")!;
  expect(post[0]).toBe("/api/model-authorizations/authorization-one/revoke");
  expect(JSON.parse(String(post[1]?.body))).toEqual({
    expected_account_revision: 7,
  });
  expect(
    fetcher.mock.calls.filter(
      ([path, init]) => path === "/api/model-authorizations" && !init?.method,
    ),
  ).toHaveLength(2);
});

it("marks failed reads unknown, ignores late unmounted data, and explains missing scopes", async () => {
  const noDirectUse = {
    ...authorization,
    scopes: ["openid", "resource.invoke"],
  };
  let finish!: (value: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(() => pending),
  );
  const view = render(
    <ModelAuthorizationCard account={account} runtime={runtime(true)} />,
  );
  expect(screen.getByText(/授权历史尚未读取/)).toBeVisible();
  view.unmount();
  await act(async () =>
    finish(response({ authorizations: [noDirectUse], remote_revocations: [] })),
  );

  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({ authorizations: [noDirectUse], remote_revocations: [] }),
    ),
  );
  render(<ModelAuthorizationCard account={account} runtime={runtime(true)} />);
  expect(
    await screen.findAllByText(/仅身份已确认；未获直接调用权限/),
  ).toHaveLength(2);
  expect(screen.getByText(/未授予 offline_access；不能刷新/)).toBeVisible();
  expect(
    screen.getByRole("button", { name: "刷新本地模拟授权" }),
  ).toBeDisabled();
});

it("clears the prior account immediately and ignores its stale authorization read", async () => {
  const otherAccount = {
    ...account,
    id: "siwc-account-two",
    name: "Second OpenAI subscription",
  };
  const otherAuthorization = {
    ...authorization,
    id: "authorization-two",
    account_id: otherAccount.id,
  };
  let finishSecond!: (value: Response) => void;
  const second = new Promise<Response>((resolve) => {
    finishSecond = resolve;
  });
  let reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      ++reads === 1
        ? Promise.resolve(
            response({
              authorizations: [authorization],
              remote_revocations: [],
            }),
          )
        : second,
    ),
  );
  const view = render(
    <ModelAuthorizationCard account={account} runtime={runtime(false)} />,
  );
  await screen.findByText("authorization-one");
  view.rerender(
    <ModelAuthorizationCard account={otherAccount} runtime={runtime(false)} />,
  );
  expect(screen.queryByText("authorization-one")).not.toBeInTheDocument();
  expect(screen.getByText(/授权历史尚未读取/)).toBeVisible();
  await act(async () =>
    finishSecond(
      response({
        authorizations: [authorization, otherAuthorization],
        remote_revocations: [],
      }),
    ),
  );
  expect(await screen.findByText("authorization-two")).toBeVisible();
  expect(screen.queryByText("authorization-one")).not.toBeInTheDocument();
});

it("shows finite authorization errors and never retries an unknown result", async () => {
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) =>
    init?.method === "POST"
      ? response(
          {
            error: {
              code: "transport_unknown",
              message: "untrusted provider detail with a secret",
            },
          },
          503,
        )
      : response({ authorizations: [], remote_revocations: [] }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<ModelAuthorizationCard account={account} runtime={runtime(true)} />);
  await screen.findByText("尚无此账户的授权历史");
  fireEvent.click(screen.getByRole("button", { name: "开始本地模拟登录" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "授权结果未知；不会自动重试。请显式检查或重新读取记录。",
  );
  expect(document.body.textContent).not.toContain("untrusted provider detail");
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it("treats a malformed authorization snapshot as unknown rather than empty", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        authorizations: [{ account_id: account.id, scopes: null }],
        remote_revocations: [],
      }),
    ),
  );
  render(<ModelAuthorizationCard account={account} runtime={runtime(false)} />);
  expect(await screen.findByRole("alert")).toHaveTextContent(/授权结果未知/);
  expect(screen.getByText(/授权历史尚未读取/)).toBeVisible();
  expect(screen.queryByText("尚无此账户的授权历史")).not.toBeInTheDocument();
});

it("does not apply a late mock mutation result to a newly selected account", async () => {
  const otherAccount = {
    ...account,
    id: "siwc-account-three",
    name: "Third OpenAI subscription",
  };
  let finishStart!: (value: Response) => void;
  const pendingStart = new Promise<Response>((resolve) => {
    finishStart = resolve;
  });
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) =>
    init?.method === "POST"
      ? pendingStart
      : response({ authorizations: [], remote_revocations: [] }),
  );
  vi.stubGlobal("fetch", fetcher);
  const view = render(
    <ModelAuthorizationCard account={account} runtime={runtime(true)} />,
  );
  await screen.findByText("尚无此账户的授权历史");
  fireEvent.click(screen.getByRole("button", { name: "开始本地模拟登录" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
    ).toHaveLength(1),
  );
  view.rerender(
    <ModelAuthorizationCard account={otherAccount} runtime={runtime(true)} />,
  );
  await screen.findByText("尚无此账户的授权历史");
  await act(async () =>
    finishStart(
      response({
        flow: {
          operation_id: "old-account-flow",
          state: "waiting",
          handoff_url: "http://127.0.0.1:9876/auth/start",
          expires_at: "2026-10-19T03:00:00Z",
        },
      }),
    ),
  );
  expect(screen.queryByText("old-account-flow")).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "开始本地模拟登录" }),
  ).toBeEnabled();
});
