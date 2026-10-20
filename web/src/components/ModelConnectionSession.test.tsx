import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { ConnectionSession, ModelAcceptanceOperation } from "../types";
import {
  ModelConnectionSession,
  validConnectionSession,
} from "./ModelConnectionSession";

const operation = {
  id: "handoff-operation",
  account_id: "handoff-account",
  revision: 3,
  phase: "connection",
  state: "running",
  material: {
    phase: "connection",
    connection_purpose: "subscription_only",
    connection_mode: "browser_handoff",
    login_timeout_seconds: 600,
    requested_scopes: [
      "openid",
      "profile",
      "email",
      "offline_access",
      "resource.invoke",
      "chatgpt.tokens.use.direct",
    ],
    requested_resource: "https://api.openai.com/v1",
    account: {
      id: "handoff-account",
      revision: 1,
      name: "SYNTHETIC OpenAI — browser connection",
      provider: "openai",
      base_url: "http://127.0.0.1:43125/v1",
      auth_method: "openai_siwc",
      billing_mode: "subscription",
      enabled: true,
      current_credential_version: 0,
      created_at: "2026-10-20T00:00:00Z",
      updated_at: "2026-10-20T00:00:00Z",
    },
    route: {
      id: "handoff-route",
      account_id: "handoff-account",
      adapter: "openai_siwc_responses",
      protocol: "responses",
      base_url: "http://127.0.0.1:43125/v1",
      environment: "simulation",
      spend_class: "subscription_only",
      extra_usage: "off",
      created_at: "2026-10-20T00:00:00Z",
    },
    credential_version: 0,
    data_summary: "fixed synthetic input; no repository or user data",
    created_at: "2026-10-20T00:01:00Z",
    expires_at: "2026-10-20T00:11:00Z",
    deadline_seconds: 30,
    max_attempts: 1,
  },
  material_digest: "a".repeat(64),
  attempts: 1,
  claim_sequence: 1,
  source: "local_simulation",
  certainty: "not_sent",
  catalog: [],
  usage_known: false,
  quota: "unknown",
  remote_billing: "unverified",
  live_access: "blocked",
} satisfies ModelAcceptanceOperation;

const expiresAt = "2026-10-20T00:10:00.000Z";
const session = {
  operation_id: operation.id,
  account_id: operation.account_id,
  state: "waiting",
  expires_at: expiresAt,
  handoff_url: "http://127.0.0.1:43126/auth/start",
} satisfies ConnectionSession;

it("renders only a manual safe local handoff with separate read and cancel actions", () => {
  const onRead = vi.fn();
  const onCancel = vi.fn();
  render(
    <ModelConnectionSession
      operation={operation}
      session={session}
      now={() => Date.parse("2026-10-20T00:00:00Z")}
      disabled={false}
      onRead={onRead}
      onCancel={onCancel}
    />,
  );

  const handoff = screen.getByRole("link", { name: "打开本地合成授权页" });
  expect(handoff).toHaveAttribute("href", session.handoff_url);
  expect(handoff).toHaveAttribute("target", "_blank");
  expect(handoff).toHaveAttribute("rel", "noopener noreferrer");
  expect(screen.getByText(expiresAt)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "读取连接状态" }));
  fireEvent.click(screen.getByRole("button", { name: "取消连接会话" }));
  expect(onRead).toHaveBeenCalledOnce();
  expect(onCancel).toHaveBeenCalledOnce();
});

it("removes the handoff locally at its deadline without reading or canceling", () => {
  vi.useFakeTimers();
  const onRead = vi.fn();
  const onCancel = vi.fn();
  try {
    render(
      <ModelConnectionSession
        operation={operation}
        session={session}
        now={() => Date.parse("2026-10-20T00:09:59Z")}
        disabled={false}
        onRead={onRead}
        onCancel={onCancel}
      />,
    );
    expect(
      screen.getByRole("link", { name: "打开本地合成授权页" }),
    ).toBeVisible();

    act(() => vi.advanceTimersByTime(1_000));

    expect(
      screen.queryByRole("link", { name: "打开本地合成授权页" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText(/本地显示期限已到/)).toBeVisible();
    expect(onRead).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
  } finally {
    vi.useRealTimers();
  }
});

it.each([
  ["different operation", { ...session, operation_id: "other-operation" }],
  ["different account", { ...session, account_id: "other-account" }],
  [
    "noncanonical port",
    { ...session, handoff_url: "http://127.0.0.1:043126/auth/start" },
  ],
  [
    "localhost host",
    { ...session, handoff_url: "http://localhost:43126/auth/start" },
  ],
  [
    "credentials",
    { ...session, handoff_url: "http://user@127.0.0.1:43126/auth/start" },
  ],
  [
    "query",
    {
      ...session,
      handoff_url: "http://127.0.0.1:43126/auth/start?state=secret",
    },
  ],
  [
    "fragment",
    { ...session, handoff_url: "http://127.0.0.1:43126/auth/start#secret" },
  ],
  [
    "escaped path",
    { ...session, handoff_url: "http://127.0.0.1:43126/auth/%73tart" },
  ],
  ["expired URL", { ...session, expires_at: "2026-10-19T23:59:59Z" }],
  [
    "terminal URL",
    { ...session, state: "failed", handoff_url: session.handoff_url },
  ],
  ["unknown state", { ...session, state: "authorizing" }],
] as const)("rejects a session with %s", (_name, candidate) => {
  expect(
    validConnectionSession(
      operation,
      candidate,
      Date.parse("2026-10-20T00:00:00Z"),
    ),
  ).toBe(false);
});

it.each([
  "exchanging",
  "available",
  "declined",
  "failed",
  "canceled",
  "expired",
] as const)(
  "accepts a linkless %s session only while the durable operation is running",
  (state) => {
    expect(
      validConnectionSession(
        operation,
        { ...session, state, handoff_url: undefined },
        Date.parse("2026-10-20T00:00:00Z"),
      ),
    ).toBe(true);
    expect(
      validConnectionSession(
        { ...operation, state: "unknown" },
        { ...session, state, handoff_url: undefined },
        Date.parse("2026-10-20T00:00:00Z"),
      ),
    ).toBe(false);
  },
);

it.each([
  ["exchanging", "正在完成本地交换", true],
  ["available", "连接证据已可用，等待回执完成", false],
  ["declined", "用户已拒绝", false],
  ["failed", "连接失败", false],
  ["canceled", "已取消", false],
  ["expired", "登录窗口已过期", false],
] as const)(
  "renders the linkless %s state without a handoff",
  (state, label, cancellable) => {
    render(
      <ModelConnectionSession
        operation={operation}
        session={{ ...session, state, handoff_url: undefined }}
        disabled={false}
        onRead={() => {}}
        onCancel={() => {}}
      />,
    );

    expect(screen.getByText(new RegExp(label))).toBeVisible();
    expect(
      screen.queryByRole("link", { name: "打开本地合成授权页" }),
    ).not.toBeInTheDocument();
    const cancel = screen.queryByRole("button", { name: "取消连接会话" });
    if (cancellable) expect(cancel).toBeEnabled();
    else expect(cancel).not.toBeInTheDocument();
  },
);

it("keeps explicit cancellation for a future linkless waiting session", () => {
  const linkless = { ...session, handoff_url: undefined };
  const now = Date.parse("2026-10-20T00:00:00Z");
  expect(validConnectionSession(operation, linkless, now)).toBe(true);

  render(
    <ModelConnectionSession
      operation={operation}
      session={linkless}
      now={() => now}
      disabled={false}
      onRead={() => {}}
      onCancel={() => {}}
    />,
  );
  expect(screen.getByText(/本地授权链接不可用/)).toBeVisible();
  expect(screen.queryByText(/会话已终止/)).not.toBeInTheDocument();
  expect(
    screen.queryByRole("link", { name: "打开本地合成授权页" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "取消连接会话" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "读取连接状态" })).toBeEnabled();
});

it.each(["waiting", "exchanging", "failed"] as const)(
  "accepts an elapsed linkless %s projection",
  (state) => {
    expect(
      validConnectionSession(
        operation,
        {
          ...session,
          state,
          expires_at: "2026-10-19T23:59:59Z",
          handoff_url: undefined,
        },
        Date.parse("2026-10-20T00:00:00Z"),
      ),
    ).toBe(true);
  },
);

it("keeps an already-expired linkless waiting session locally expired", () => {
  const now = Date.parse("2026-10-20T00:11:00Z");
  const expired = {
    ...session,
    expires_at: "2026-10-20T00:10:00Z",
    handoff_url: undefined,
  };
  expect(validConnectionSession(operation, expired, now)).toBe(true);

  render(
    <ModelConnectionSession
      operation={operation}
      session={expired}
      now={() => now}
      disabled={false}
      onRead={() => {}}
      onCancel={() => {}}
    />,
  );
  expect(screen.getByText(/本地显示期限已到/)).toBeVisible();
  expect(screen.queryByText(/本地授权链接不可用/)).not.toBeInTheDocument();
  expect(screen.queryByText(/会话已终止/)).not.toBeInTheDocument();
});
