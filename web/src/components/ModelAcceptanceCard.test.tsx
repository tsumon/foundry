import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { response } from "../test/fixtures";
import { ModelAcceptanceCard } from "./ModelAcceptanceCard";

const report = {
  account_id: "synthetic",
  account_revision: 3,
  scope: "account_routes_only",
  live_access: "blocked",
  quota: "unknown",
  checks: [
    { phase: "local_metadata", state: "ready", reason_codes: [] },
    ...["login", "catalog", "inference"].map((phase) => ({
      phase,
      state: "blocked",
      reason_codes: ["live_provider_access_pending_confirmation"],
    })),
    {
      phase: "task",
      state: "not_evaluated",
      reason_codes: ["task_approval_required"],
    },
  ],
  routes: [
    {
      id: "synthetic-route",
      adapter: "kimi_code_chat",
      environment: "simulation",
      local_metadata: "ready",
      local_extra_usage: "off",
      remote_extra_usage: "unverified",
      reason_codes: ["remote_billing_unverified"],
    },
  ],
};

const openAIPreparation = {
  scope: "openai_subscription_stage0",
  endpoint_policy: "exact_official",
  credential_storage: "not_checked",
  remote_billing: "unverified",
  quota: "unknown",
  real_connection: "blocked",
  real_catalog: "blocked",
  real_inference: "blocked",
  subscription_adapter: {
    state: "implemented_disabled",
    validation: "local_simulation_only",
  },
};
const openAIReport = {
  ...report,
  routes: [
    {
      ...report.routes[0],
      adapter: "openai_siwc_responses",
    },
  ],
  openai_preparation: openAIPreparation,
};

it("shows honest OpenAI stage-zero preparation without promoting metadata to permission", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response({ acceptance: openAIReport })),
  );
  render(<ModelAcceptanceCard accountId="synthetic" accountRevision={3} />);
  fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));

  expect(
    await screen.findByRole("heading", { name: "OpenAI · 阶段 0" }),
  ).toBeVisible();
  expect(screen.getByText(/地址策略：官方规范基址/)).toHaveTextContent(
    /仅账户基础元数据；不证明全部路由或端点归属已通过/,
  );
  expect(screen.getByText(/Keychain：本页未检查/)).toHaveTextContent(
    /本地合成诊断命令 foundry-keychain-check/,
  );
  expect(screen.getByText(/远端计费：未验证/)).toHaveTextContent(/额度：未知/);
  expect(
    screen.getByText(/订阅连接适配器：已实现，默认关闭/),
  ).toHaveTextContent(/仅通过本地仿真验证/);
  expect(screen.getByText(/真实账户、Keychain 与额度均未验证/)).toBeVisible();
  expect(screen.getByText("真实连接：待单独审批")).toBeVisible();
  expect(screen.getByText("真实目录：待单独审批")).toBeVisible();
  expect(screen.getByText("真实推理：待单独审批")).toBeVisible();
  expect(screen.queryByText(/Keychain：.*成功/)).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /登录|启用/ }),
  ).not.toBeInTheDocument();
});

it("keeps an omitted subscription adapter unreported instead of implying readiness", async () => {
  const { subscription_adapter: _omitted, ...legacyPreparation } =
    openAIPreparation;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        acceptance: {
          ...openAIReport,
          openai_preparation: legacyPreparation,
        },
      }),
    ),
  );
  render(<ModelAcceptanceCard accountId="synthetic" accountRevision={3} />);
  fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));

  expect(
    await screen.findByRole("heading", { name: "OpenAI · 阶段 0" }),
  ).toBeVisible();
  expect(screen.queryByText(/订阅连接适配器：已实现/)).not.toBeInTheDocument();
  expect(screen.queryByText(/仅通过本地仿真验证/)).not.toBeInTheDocument();
  expect(screen.queryByText(/真实账户.*已验证/)).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /登录|启用/ }),
  ).not.toBeInTheDocument();
});

it.each([
  null,
  { state: "unknown", validation: "local_simulation_only" },
  { state: "enabled", validation: "local_simulation_only" },
  { state: "implemented_disabled", validation: "live_account" },
  {
    state: "implemented_disabled",
    validation: "local_simulation_only",
    enabled: true,
  },
])(
  "fails closed on unsafe subscription adapter metadata: %j",
  async (value) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        response({
          acceptance: {
            ...openAIReport,
            openai_preparation: {
              ...openAIPreparation,
              subscription_adapter: value,
            },
          },
        }),
      ),
    );
    render(<ModelAcceptanceCard accountId="synthetic" accountRevision={3} />);
    fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/读取失败/);
    expect(screen.queryByText("synthetic-route")).not.toBeInTheDocument();
    expect(
      screen.queryByText(/订阅连接适配器：已实现/),
    ).not.toBeInTheDocument();
  },
);

it("clears prior OpenAI preparation when a later payload claims enabled inference", async () => {
  let reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        acceptance:
          ++reads === 1
            ? openAIReport
            : {
                ...openAIReport,
                openai_preparation: {
                  ...openAIPreparation,
                  real_inference: "enabled",
                },
              },
      }),
    ),
  );
  render(<ModelAcceptanceCard accountId="synthetic" accountRevision={3} />);
  fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));
  await screen.findByRole("heading", { name: "OpenAI · 阶段 0" });

  fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/读取失败/);
  expect(screen.queryByText("synthetic-route")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("heading", { name: "OpenAI · 阶段 0" }),
  ).not.toBeInTheDocument();
});

it.each([
  { ...openAIPreparation, real_connection: "ready" },
  { ...openAIPreparation, endpoint_policy: "future_policy" },
])("fails closed on an unsafe OpenAI preparation enum", async (value) => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        acceptance: { ...openAIReport, openai_preparation: value },
      }),
    ),
  );
  render(<ModelAcceptanceCard accountId="synthetic" accountRevision={3} />);
  fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/读取失败/);
  expect(screen.queryByText("synthetic-route")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("heading", { name: "OpenAI · 阶段 0" }),
  ).not.toBeInTheDocument();
});

it("keeps missing optional preparation compatible and shows no OpenAI section for Kimi", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response({ acceptance: report })),
  );
  render(<ModelAcceptanceCard accountId="synthetic" accountRevision={3} />);
  fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));
  expect(await screen.findByText("synthetic-route")).toBeVisible();
  expect(
    screen.queryByRole("heading", { name: "OpenAI · 阶段 0" }),
  ).not.toBeInTheDocument();
});

// Any mount fetch, retry, mutation, or promotion of local metadata to live permission breaks this boundary.
it("reads once only on an explicit action and keeps live access and billing unknown", async () => {
  const fetcher = vi.fn(async (_path: string, _init?: RequestInit) =>
    response({ acceptance: report }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<ModelAcceptanceCard accountId="synthetic" accountRevision={3} />);
  expect(fetcher).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));
  expect(await screen.findByText("synthetic-route")).toBeVisible();
  expect(screen.getByText(/真实访问：关闭/)).toBeVisible();
  expect(screen.getByText(/远端额外用量：未验证/)).toBeVisible();
  expect(screen.getByText(/额度：未知/)).toBeVisible();
  expect(screen.getAllByRole("button")).toHaveLength(1);
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0][0]).toBe(
    "/api/model-accounts/synthetic/acceptance",
  );
  expect(fetcher.mock.calls[0][1]?.method ?? "GET").toBe("GET");
  vi.useFakeTimers();
  try {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(120_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);
  } finally {
    vi.useRealTimers();
  }
});

it("clears a previous report before reading and on errors, with no automatic retry", async () => {
  let reads = 0;
  const fetcher = vi.fn(async () =>
    ++reads === 1
      ? response({ acceptance: openAIReport })
      : response(
          {
            error: {
              code: "storage_failure",
              message: "secret provider details",
            },
          },
          503,
        ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<ModelAcceptanceCard accountId="synthetic" accountRevision={3} />);
  fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));
  await screen.findByRole("heading", { name: "OpenAI · 阶段 0" });
  fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));
  expect(screen.queryByText("synthetic-route")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("heading", { name: "OpenAI · 阶段 0" }),
  ).not.toBeInTheDocument();
  expect(await screen.findByRole("alert")).toHaveTextContent(/读取失败/);
  expect(document.body.textContent).not.toContain("secret provider details");
  expect(fetcher).toHaveBeenCalledTimes(2);
});

it.each([
  { id: "other", revision: 3 },
  { id: "synthetic", revision: 4 },
])(
  "clears displayed OpenAI preparation on identity or revision change: %s",
  async ({ id, revision }) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => response({ acceptance: openAIReport })),
    );
    const view = render(
      <ModelAcceptanceCard accountId="synthetic" accountRevision={3} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));
    await screen.findByRole("heading", { name: "OpenAI · 阶段 0" });

    view.rerender(
      <ModelAcceptanceCard accountId={id} accountRevision={revision} />,
    );
    expect(
      screen.queryByRole("heading", { name: "OpenAI · 阶段 0" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText(/尚无当前版本的验收报告/)).toBeVisible();
  },
);

it.each([
  { id: "other", revision: 3 },
  { id: "synthetic", revision: 4 },
])(
  "discards late reports on identity or revision change: %s",
  async ({ id, revision }) => {
    let finish!: (value: Response) => void;
    const fetcher = vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          finish = resolve;
        }),
    );
    vi.stubGlobal("fetch", fetcher);
    const view = render(
      <ModelAcceptanceCard accountId="synthetic" accountRevision={3} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));
    view.rerender(
      <ModelAcceptanceCard accountId={id} accountRevision={revision} />,
    );
    await act(async () => finish(response({ acceptance: openAIReport })));
    expect(screen.queryByText("synthetic-route")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "OpenAI · 阶段 0" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "本地验收检查" })).toBeEnabled();
    expect(fetcher).toHaveBeenCalledTimes(1);
  },
);

it.each([
  { ...report, account_revision: 4 },
  { ...report, checks: null },
  { ...report, live_access: "allowed" },
])("fails closed on mismatched or malformed data", async (acceptance) => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response({ acceptance })),
  );
  render(<ModelAcceptanceCard accountId="synthetic" accountRevision={3} />);
  fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/读取失败/);
  expect(screen.queryByText("synthetic-route")).not.toBeInTheDocument();
});

it("ignores completion after unmount without another request", async () => {
  let finish!: (value: Response) => void;
  const fetcher = vi.fn(
    () =>
      new Promise<Response>((resolve) => {
        finish = resolve;
      }),
  );
  vi.stubGlobal("fetch", fetcher);
  const view = render(
    <ModelAcceptanceCard accountId="synthetic" accountRevision={3} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));
  view.unmount();
  await act(async () => finish(response({ acceptance: report })));
  expect(fetcher).toHaveBeenCalledTimes(1);
});
