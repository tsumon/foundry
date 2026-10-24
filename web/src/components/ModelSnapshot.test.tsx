import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { DecisionPanel } from "./DecisionPanel";
import { MissionWorkspace } from "./MissionWorkspace";
import { mission, profile } from "../test/fixtures";
import { roles, type Mission, type Role, type ModelAccount } from "../types";
import { ModelSnapshot } from "./ModelSnapshot";
import { createPlanDraft, PlanRevisionEditor } from "./PlanRevisionEditor";
import { accountActionBlockReason } from "../modelSnapshot";

it("shows frozen catalog authorization generation and expiry without a current-use grant", () => {
  const m = routed("live");
  const selection = m.model_snapshot!.roles.worker;
  selection.route!.adapter = "openai_siwc_responses";
  selection.authorization = {
    id: "catalog-auth",
    account_id: "a-frozen",
    issuer: "https://auth.openai.com",
    spend_class: "subscription_only",
    scopes: ["openid"],
  };
  selection.catalog = {
    operation_id: "catalog-frozen",
    receipt_digest: "b".repeat(64),
    authorization_generation: 3,
    expires_at: "2030-01-01T00:00:00Z",
  };
  render(<ModelSnapshot mission={m} />);
  expect(screen.getByText(/目录 catalog-frozen/)).toBeVisible();
  expect(screen.getByText(/授权代次 3/)).toBeVisible();
  expect(screen.getByText(/2030-01-01T00:00:00Z/)).toBeVisible();
});

function readAppStyles() {
  const process = (
    globalThis as typeof globalThis & {
      process: {
        cwd(): string;
        getBuiltinModule(name: "node:fs"): {
          readFileSync(path: string, encoding: "utf8"): string;
        };
      };
    }
  ).process;
  return process
    .getBuiltinModule("node:fs")
    .readFileSync(`${process.cwd()}/src/styles.css`, "utf8");
}

function frozen(accountRoles: Role[] = ["worker"]): Mission {
  const profiles = { ...mission.profiles };
  const entries = roles.map((role) => {
    if (accountRoles.includes(role))
      profiles[role] = {
        ...profile,
        account_id: "a-frozen",
        account_revision: 2,
        credential_version: 1,
      };
    return [
      role,
      {
        profile: profiles[role],
        ...(accountRoles.includes(role)
          ? {
              account: {
                id: "a-frozen",
                revision: 2,
                name: "Historical account V1",
                provider: "custom",
                billing_mode: "subscription",
                enabled: true,
                identity_status: "unverified",
                usage_status: "unknown",
                inference_enabled: false,
              },
            }
          : {}),
      },
    ];
  });
  return {
    ...mission,
    profiles,
    workspace_mode: "generations_account_snapshot_v1",
    model_snapshot_status: "valid",
    model_snapshot: {
      schema_version: 1,
      roles: Object.fromEntries(entries),
      digest: "a".repeat(64),
    },
  };
}

function routed(environment: "live" | "simulation" = "simulation"): Mission {
  const result = frozen(["worker"]);
  result.workspace_mode = "generations_account_snapshot_v2";
  result.model_snapshot!.schema_version = 2;
  result.profiles.worker.route_id = "route-frozen";
  result.model_snapshot!.roles.worker.profile.route_id = "route-frozen";
  result.model_snapshot!.roles.worker.route = {
    id: "route-frozen",
    account_id: "a-frozen",
    adapter: "openai_api_responses",
    protocol: "responses",
    base_url: "http://127.0.0.1:9812/v1",
    environment,
    spend_class: "explicit_api",
    extra_usage: "off",
    created_at: "then",
  };
  result.model_runtime = {
    live_authorization: false,
    live_discovery: false,
    live_inference: false,
    simulation_inference: true,
    simulation_authorization: false,
    automatic_paid_fallback: false,
    adapters: [],
  };
  return result;
}

it("does not call SIWC preview metadata a missing API key before backend authorization recheck", () => {
  const account: ModelAccount = {
    id: "siwc-preview",
    revision: 1,
    name: "Synthetic subscription",
    provider: "openai",
    billing_mode: "subscription",
    enabled: true,
    identity_status: "unverified",
    usage_status: "unknown",
    inference_enabled: false,
    auth_method: "openai_siwc",
    current_credential_version: 0,
    base_url: "http://127.0.0.1:9812/v1",
    created_at: "then",
    updated_at: "then",
  };
  render(
    <ModelSnapshot
      preview={{
        planner: {
          profile: {
            ...profile,
            account_id: account.id,
            credential_version: 0,
          },
          account,
        },
      }}
    />,
  );
  const region = screen.getByRole("region", { name: "创建前角色预览" });
  expect(region).toHaveTextContent("SIWC 授权将在创建时由后端复核");
  expect(region).not.toHaveTextContent("凭据版本：0");
  expect(region).not.toHaveTextContent("稳定授权 缺失");
});

it("describes an explicitly selected coding preview as separately gated instead of unavailable", () => {
  render(
    <ModelSnapshot
      codingModePreview
      preview={{
        worker: {
          profile: {
            ...profile,
            account_id: "a-preview",
            account_revision: 1,
          },
          account: {
            id: "a-preview",
            revision: 1,
            name: "Preview account",
            provider: "openai",
            billing_mode: "api_credits",
            enabled: true,
            identity_status: "unverified",
            usage_status: "unknown",
            inference_enabled: false,
          },
        },
      }}
    />,
  );

  const region = screen.getByRole("region", { name: "创建前角色预览" });
  expect(region).toHaveTextContent(/账户模型调用.*单独门禁/);
  expect(region).toHaveTextContent(/配置不等于当前调用授权/);
  expect(region).not.toHaveTextContent(/真实账户调用尚未开放/);
});

it("shows historical seven-role version metadata in execution approval and blocks only account execution roles", () => {
  const action = vi.fn();
  render(<DecisionPanel mission={frozen()} busy={false} onAction={action} />);
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
  fireEvent.click(screen.getByText("查看冻结模型明细（7 个角色）"));
  expect(screen.getByText("Historical account V1")).toBeVisible();
  expect(screen.getByText(/凭据版本：1/)).toBeVisible();
  expect(screen.getByText(/已冻结配置；真实账户调用尚未开放/)).toBeVisible();
  expect(screen.getAllByText(/旧式连接/)).toHaveLength(6);
});

it("keeps approval materials/action visible outside default-collapsed seven-role details and expands every frozen role", () => {
  render(
    <DecisionPanel
      mission={frozen([...roles])}
      busy={false}
      onAction={vi.fn()}
    />,
  );
  const summary = screen.getByText("查看冻结模型明细（7 个角色）");
  const details = summary.closest("details")!;
  expect(details).not.toHaveAttribute("open");
  expect(screen.getAllByText("Historical account V1")).toHaveLength(7);
  for (const node of screen.getAllByText("Historical account V1"))
    expect(node).not.toBeVisible();
  const action = screen.getByRole("button", { name: "批准并执行计划" });
  expect(action).toBeVisible();
  expect(action).toBeDisabled();
  expect(action.closest("details")).toBeNull();
  expect(screen.getByText("修改范围")).toBeVisible();
  expect(screen.getByText("验收命令")).toBeVisible();
  expect(screen.getByText(/本次动作涉及账户绑定角色/)).toBeVisible();
  fireEvent.click(summary);
  expect(details).toHaveAttribute("open");
  for (const role of roles)
    expect(
      within(details).getByText(
        {
          main: "Main",
          researcher: "Researcher",
          planner: "Planner",
          worker: "Worker",
          debugger: "Debugger",
          qa: "QA",
          auditor: "Auditor",
        }[role],
      ),
    ).toBeVisible();
  expect(within(details).getAllByText(/凭据版本：1/)).toHaveLength(7);
  expect(action).toBeDisabled();
});
it("keeps legacy planning enabled when only Worker is account-bound, and blocks Researcher only when enabled", () => {
  const m = {
    ...frozen(["worker", "researcher"]),
    state: "draft",
    spec: { ...mission.spec, research: false },
  };
  const { rerender } = render(
    <DecisionPanel mission={m} busy={false} onAction={vi.fn()} />,
  );
  fireEvent.click(screen.getByRole("checkbox"));
  expect(
    screen.getByRole("button", { name: "授权上下文并生成计划" }),
  ).toBeEnabled();
  rerender(
    <DecisionPanel
      mission={{ ...m, spec: { ...m.spec, research: true } }}
      busy={false}
      onAction={vi.fn()}
    />,
  );
  expect(
    screen.getByRole("button", { name: "授权上下文并生成计划" }),
  ).toBeDisabled();
});
it("does not disable legacy execution because Main is account-bound, but disables Main chat", async () => {
  const m = frozen(["main"]);
  const { unmount } = render(
    <DecisionPanel mission={m} busy={false} onAction={vi.fn()} />,
  );
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeEnabled();
  unmount();
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({ plan_revisions: [], local_operations: [] }),
        ),
    ),
  );
  render(<MissionWorkspace mission={m} onUpdate={vi.fn()} />);
  expect(screen.getByLabelText("向 Main 提问")).toBeDisabled();
  expect(screen.getByText(/Main 的账户调用已禁用/)).toBeVisible();
  expect(screen.getByText(/schema1 账户任务不具备推理执行权/)).toBeVisible();
});
it("invalid or future snapshots remain readable but cannot claim complete frozen context or authorize", () => {
  const invalid = {
    ...frozen(),
    model_snapshot_status: "invalid_or_unsupported" as const,
    model_snapshot: undefined,
  };
  render(<DecisionPanel mission={invalid} busy={false} onAction={vi.fn()} />);
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
  expect(screen.getByText(/模型快照缺失、损坏或版本不受支持/)).toBeVisible();
});
it("keeps pause and matching local delivery available despite historical disabled account metadata", () => {
  const m = frozen();
  m.model_snapshot!.roles.worker.account!.enabled = false;
  const { rerender } = render(
    <DecisionPanel
      mission={{ ...m, state: "running" }}
      busy={false}
      onAction={vi.fn()}
    />,
  );
  expect(screen.getByRole("button", { name: "请求暂停" })).toBeEnabled();
  const delivered = {
    ...m,
    state: "awaiting_delivery",
    candidate_digest: "candidate",
    evidence: [{ passed: true, candidate_digest: "candidate" }],
    reviews: [
      { role: "qa", passed: true, candidate_digest: "candidate" },
      { role: "auditor", passed: true, candidate_digest: "candidate" },
    ],
  } as Mission;
  rerender(
    <DecisionPanel mission={delivered} busy={false} onAction={vi.fn()} />,
  );
  expect(screen.getByRole("button", { name: "批准并导出交付" })).toBeEnabled();
});

it("renders frozen zero as missing rather than latest and never fetches live account metadata", () => {
  const m = frozen();
  delete m.profiles.worker.credential_version;
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const { rerender } = render(<ModelSnapshot mission={m} />);
  expect(screen.getByText(/凭据版本：0（未配置）/)).toBeVisible();
  const historical = frozen();
  rerender(<ModelSnapshot mission={historical} />);
  expect(screen.getByText(/凭据版本：1/)).toBeVisible();
  expect(screen.getByText("Historical account V1")).toBeVisible();
  expect(fetcher).not.toHaveBeenCalled();
  rerender(
    <ModelSnapshot
      mission={{
        ...historical,
        model_snapshot: { ...historical.model_snapshot!, schema_version: 2 },
      }}
    />,
  );
  expect(screen.getByText(/模型快照缺失、损坏或版本不受支持/)).toBeVisible();
  expect(screen.queryByText("Historical account V1")).not.toBeInTheDocument();
});

it("does not offer an editable plan for a structurally invalid account snapshot", () => {
  const m = { ...frozen(), model_snapshot: undefined };
  render(
    <PlanRevisionEditor
      mission={m}
      draft={createPlanDraft(m)}
      history={[]}
      historyLoading={false}
      historyError=""
      busy={false}
      saveError=""
      onDraftChange={vi.fn()}
      onSave={vi.fn()}
      onDiscard={vi.fn()}
      onRebase={vi.fn()}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "修订计划（保存不审批）" }),
  );
  expect(screen.getByLabelText("计划摘要")).toHaveAttribute("readonly");
});

it("uses server runtime metadata to enable only a ready schema2 simulation route", () => {
  const action = vi.fn();
  const m = routed();
  const view = render(
    <DecisionPanel mission={m} busy={false} onAction={action} />,
  );
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeEnabled();
  fireEvent.click(screen.getByText("查看冻结模型明细（7 个角色）"));
  expect(screen.getByText(/route-frozen/)).toBeVisible();
  expect(screen.getByText(/本地模拟 · explicit_api/)).toBeVisible();
  expect(screen.getByText(/额外用量关闭/)).toBeVisible();
  view.rerender(
    <DecisionPanel
      mission={{ ...m, model_runtime: undefined }}
      busy={false}
      onAction={action}
    />,
  );
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
  expect(screen.getByText(/当前进程未提供可用的模拟推理能力/)).toBeVisible();
  view.rerender(
    <DecisionPanel mission={routed("live")} busy={false} onAction={action} />,
  );
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
  expect(screen.getByText(/冻结路由指向真实环境/)).toBeVisible();
});

it("shows a model-backed live route as per-call gated while keeping ordinary live routes blocked", () => {
  const ordinary = routed("live");
  expect(accountActionBlockReason(ordinary, "approve")).toMatch(
    /真实环境.*门禁保持关闭/,
  );

  const modelBacked = routed("live");
  modelBacked.spec = {
    ...modelBacked.spec,
    c4: {
      schema_version: 1,
      control_version: 1,
      id: "c4_go_model_v1",
      mode: "docker",
      image_id: `sha256:${"a".repeat(64)}`,
      platform: "linux/arm64",
      docker_path: "/opt/docker/bin/docker",
      endpoint: "unix:///opt/docker/run/docker.sock",
      verifier: "/opt/foundry/verify-go-v1",
    },
  };
  modelBacked.model_runtime = {
    ...modelBacked.model_runtime!,
    live_inference: true,
    simulation_inference: false,
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
  };

  expect(accountActionBlockReason(modelBacked, "approve")).toBe("");
  render(<ModelSnapshot mission={modelBacked} />);
  expect(screen.getByText(/真实环境（逐次门禁） · explicit_api/)).toBeVisible();
  expect(screen.getByText(/配置不等于当前调用授权/)).toBeVisible();
  expect(screen.queryByText(/真实账户调用尚未开放/)).not.toBeInTheDocument();
});

it("shows per-sign-in authorization without inventing token generation or current readiness", () => {
  const m = routed();
  const worker = m.model_snapshot!.roles.worker;
  worker.profile.credential_version = 0;
  worker.route = {
    ...worker.route!,
    adapter: "openai_siwc_responses",
    spend_class: "subscription_only",
  };
  worker.authorization = {
    id: "auth-stable",
    account_id: "a-frozen",
    issuer: "https://auth.openai.com",
    spend_class: "subscription_only",
    scopes: ["resource.invoke", "chatgpt.tokens.use.direct"],
  };
  render(<ModelSnapshot mission={m} />);
  expect(screen.getByText(/本次登录本地授权 auth-stable/)).toBeVisible();
  expect(screen.getByText(/resource.invoke/)).toBeVisible();
  expect(screen.getByText(/当前 token 代数不是冻结快照/)).toBeVisible();
  expect(document.body.textContent).not.toContain("token generation");
  expect(document.body.textContent).not.toContain("expires");
});

it("summarizes OAuth snapshots by stable binding and wraps long authorization IDs", () => {
  const m = routed();
  const authorizationID = `auth-${"f".repeat(64)}`;
  const oauthProfile = {
    ...m.model_snapshot!.roles.worker.profile,
    credential_version: 0,
  };
  const oauthRoute = {
    ...m.model_snapshot!.roles.worker.route!,
    adapter: "openai_siwc_responses",
    spend_class: "subscription_only" as const,
  };
  const stableAuthorization = {
    id: authorizationID,
    account_id: "a-frozen",
    issuer: "https://auth.openai.com",
    subject: "subject-bounded",
    client_id: "issued-client",
    spend_class: "subscription_only",
    scopes: ["resource.invoke", "chatgpt.tokens.use.direct"],
  };
  for (const role of roles) {
    m.profiles[role] = { ...oauthProfile };
    m.model_snapshot!.roles[role] = {
      profile: { ...oauthProfile },
      account: { ...m.model_snapshot!.roles.worker.account! },
      route: { ...oauthRoute },
      authorization: { ...stableAuthorization },
    };
  }

  const compactParagraphRule = readAppStyles().match(
    /\.model-snapshot-compact\s*>\s*p\s*\{[^}]+\}/,
  )?.[0];
  expect(compactParagraphRule).toBeDefined();
  const style = document.createElement("style");
  style.textContent = compactParagraphRule!;
  document.head.append(style);
  try {
    render(<ModelSnapshot mission={m} compact />);

    const summary = screen.getByLabelText("冻结模型摘要");
    const authorizationSummary = [
      ...summary.querySelectorAll(":scope > p"),
    ].find((paragraph) => paragraph.textContent?.includes(authorizationID));
    expect(authorizationSummary).toBeDefined();
    expect(getComputedStyle(authorizationSummary!).overflowWrap).toBe(
      "anywhere",
    );
    expect(summary).not.toHaveTextContent(
      `${authorizationID}、${authorizationID}`,
    );
    expect(summary).not.toHaveTextContent("0（未配置）");
    expect(summary).toHaveTextContent("当前 token 代数不是冻结快照");
  } finally {
    style.remove();
  }
});
