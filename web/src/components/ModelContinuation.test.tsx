import { useState } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { MissionViews } from "./MissionViews";
import { DecisionPanel } from "./DecisionPanel";
import { mission, profile, response } from "../test/fixtures";
import invalidHistoryContract from "../test/modelContinuationInvalidHistoryContract.json";
import { roles, type Mission, type RoleModelSelection } from "../types";

const oldDigest = "a".repeat(64),
  newDigest = "b".repeat(64),
  digest = "c".repeat(64);
const future = "2099-01-01T00:00:00Z";
function fixture(): Mission {
  const selections = {} as NonNullable<Mission["model_snapshot"]>["roles"];
  for (const role of roles)
    selections[role] = {
      profile: {
        ...profile,
        account_id: "synthetic-account",
        account_revision: 1,
        route_id: "route",
        credential_version: 0,
      },
      account: {
        id: "synthetic-account",
        revision: 1,
        name: "Synthetic subscription",
        provider: "openai",
        enabled: true,
        billing_mode: "subscription",
        identity_status: "unverified",
        usage_status: "unknown",
        inference_enabled: false,
      },
      route: {
        id: "route",
        account_id: "synthetic-account",
        adapter: "openai_siwc_responses",
        protocol: "responses",
        base_url: "https://api.openai.com/v1",
        environment: "live",
        spend_class: "subscription_only",
        extra_usage: "off",
        created_at: "2026-01-01T00:00:00Z",
      },
      authorization: {
        id: "old-local-sign-in",
        account_id: "synthetic-account",
        issuer: "https://auth.openai.com",
        spend_class: "subscription_only",
        scopes: ["resource.invoke", "chatgpt.tokens.use.direct"],
      },
    } satisfies RoleModelSelection;
  return {
    ...structuredClone(mission),
    id: "synthetic-task",
    revision: 9,
    coding_version: 1,
    state: "blocked",
    plan: null,
    workspace_mode: "generations_account_snapshot_v2",
    profiles: Object.fromEntries(
      roles.map((role) => [role, selections[role].profile]),
    ) as Mission["profiles"],
    model_snapshot_status: "valid",
    model_snapshot: { schema_version: 2, digest: oldDigest, roles: selections },
    budget: {
      ...mission.budget,
      used_calls: 1,
      max_calls: 7,
      usage_unknown: true,
    },
  };
}
function material(state = "prepared") {
  return {
    id: "material",
    digest,
    state,
    prepared_revision: 9,
    predecessor_digest: oldDigest,
    replacement_digest: newDigest,
    authorization_id: "new-local-sign-in",
    authorization_generation: 1,
    authorization_expires_at: future,
    catalog_operation_id: "local-catalog",
    affected_roles: [...roles],
    created_at: "2026-10-24T00:00:00Z",
    expires_at: future,
  };
}
function withMaterial(state = "prepared"): Mission {
  return {
    ...fixture(),
    revision: state === "approved" ? 11 : 10,
    model_continuation_status: "valid",
    model_continuations: [
      {
        ...material(state),
        ...(state === "approved"
          ? {
              approval: {
                revision: 11,
                digest,
                created_at: "2026-10-24T00:01:00Z",
              },
            }
          : {}),
      },
    ],
  } as Mission;
}
function Harness({ initial = fixture() }: { initial?: Mission }) {
  const [current, setCurrent] = useState(initial);
  return <MissionViews mission={current} view="总览" onChanged={setCurrent} />;
}
const fetchMock = vi.fn();
beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
  fetchMock.mockReset();
});
it("renders local-only continuation with unchanged unknown 1/7 budget and no requests", () => {
  render(<Harness />);
  expect(
    screen.getByRole("button", { name: "读取本地续接候选" }),
  ).toBeEnabled();
  expect(screen.getByText(/续接不调用模型/)).toBeVisible();
  expect(screen.getByText(/已用 1 \/ 7 · 剩余 6/)).toBeVisible();
  expect(screen.getByText(/未知用量保持未知/)).toBeVisible();
  expect(fetchMock).not.toHaveBeenCalled();
});
it("reads existing metadata, then prepares, approves and applies exact independent materials", async () => {
  const prepared = withMaterial(),
    approved = withMaterial("approved");
  const applied = {
    ...approved,
    revision: 12,
    model_continuations: [
      {
        ...approved.model_continuations![0],
        state: "applied",
        application: {
          revision: 12,
          digest,
          created_at: "2026-10-24T00:02:00Z",
        },
      },
    ],
    effective_model_snapshot: {
      ...fixture().model_snapshot!,
      digest: newDigest,
      roles: Object.fromEntries(
        roles.map((role) => [
          role,
          {
            ...fixture().model_snapshot!.roles[role],
            authorization: {
              ...fixture().model_snapshot!.roles[role].authorization!,
              id: "new-local-sign-in",
            },
          },
        ]),
      ),
    },
  };
  fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
    if (
      url === "/api/missions/synthetic-task/model-continuations/candidates" &&
      !init?.method
    )
      return response({
        authorizations: [
          {
            account_id: "synthetic-account",
            id: "new-local-sign-in",
            state: "available",
            generation: 1,
            expires_at: future,
          },
        ],
      });
    if (
      url === "/api/model-accounts/synthetic-account/desktop-preparation" &&
      !init?.method
    )
      return response({
        preparation: {
          phase: "closed",
          connection_ready: false,
          catalog_ready: false,
        },
        catalog_choices: [
          {
            operation_id: "local-catalog",
            route_id: "route",
            model: "fixture-model",
            display_name: "Recorded fixture model",
            expires_at: future,
          },
        ],
      });
    const body = JSON.parse(init?.body as string);
    expect(init?.method).toBe("POST");
    if (url === "/api/missions/synthetic-task/model-continuations") {
      expect(body).toEqual({
        revision: 9,
        authorization_id: "new-local-sign-in",
        catalog_operation_id: "local-catalog",
      });
      return response({ mission: prepared });
    }
    if (
      url ===
      "/api/missions/synthetic-task/model-continuations/material/approve"
    ) {
      expect(body).toEqual({ revision: 10, digest });
      return response({ mission: approved });
    }
    if (
      url === "/api/missions/synthetic-task/model-continuations/material/apply"
    ) {
      expect(body).toEqual({ revision: 11, digest });
      return response({ mission: applied });
    }
    throw new Error("Unexpected request " + url);
  });
  render(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "读取本地续接候选" }));
  const choice = await screen.findByRole("combobox", { name: "本地续接候选" });
  fireEvent.change(choice, { target: { value: "0" } });
  expect(fetchMock).toHaveBeenCalledTimes(2);
  expect(document.body.textContent).not.toMatch(/RAW-PRIVATE/);
  fireEvent.click(screen.getByRole("button", { name: "准备续接材料" }));
  expect(await screen.findByText("待批准")).toBeVisible();
  expect(fetchMock).toHaveBeenCalledTimes(3);
  expect(screen.getByRole("button", { name: "应用续接" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "批准续接材料" }));
  expect(await screen.findByText("已批准，待应用")).toBeVisible();
  expect(fetchMock).toHaveBeenCalledTimes(4);
  fireEvent.click(screen.getByRole("button", { name: "应用续接" }));
  expect(await screen.findByText("已应用")).toBeVisible();
  expect(fetchMock).toHaveBeenCalledTimes(5);
  expect(screen.getByText(/已用 1 \/ 7 · 剩余 6/)).toBeVisible();
  expect(screen.getByLabelText("原始冻结模型")).toHaveTextContent(
    "old-local-sign-in",
  );
  expect(screen.getByLabelText("当前有效模型选择")).toHaveTextContent(
    "new-local-sign-in",
  );
});
it.each([
  "expired",
  "stale",
  "unknown",
  "malformed-expiry",
  "malformed-digest",
  "malformed-field",
  "invalid-history",
])("keeps %s materials inert", (state) => {
  const m = withMaterial(state);
  if (state === "malformed-expiry") {
    m.model_continuations![0].state = "prepared";
    m.model_continuations![0].expires_at = "not-a-date";
  }
  if (state === "malformed-digest") {
    m.model_continuations![0].state = "prepared";
    m.model_continuations![0].digest = "broken";
  }
  if (state === "malformed-field") {
    m.model_continuations![0].state = "prepared";
    m.model_continuations![0].authorization_id = {
      unexpected: "private-raw-value",
    } as unknown as string;
  }
  if (state === "invalid-history")
    m.model_continuation_status = "invalid_or_unsupported";
  render(<Harness initial={m} />);
  expect(screen.getByRole("button", { name: "批准续接材料" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "应用续接" })).toBeDisabled();
  expect(fetchMock).not.toHaveBeenCalled();
});
it("does not query a provider or prepare desktop access when local catalog is missing", async () => {
  fetchMock.mockImplementation(async () =>
    response({
      authorizations: [],
      remote_revocations: [],
      preparation: { phase: "closed" },
      catalog_choices: [],
    }),
  );
  render(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "读取本地续接候选" }));
  expect(await screen.findByText(/缺少可用的本地授权或目录证据/)).toBeVisible();
  expect(screen.getByRole("button", { name: "准备续接材料" })).toBeDisabled();
  expect(fetchMock.mock.calls.every(([, init]) => !init?.method)).toBe(true);
});
it("stops after a denied mutation without retry or following action", async () => {
  fetchMock.mockResolvedValue(
    response({ error: { code: "conflict", message: "材料已失效" } }, 409),
  );
  render(<Harness initial={withMaterial()} />);
  fireEvent.click(screen.getByRole("button", { name: "批准续接材料" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("材料已失效");
  expect(screen.getByRole("button", { name: "批准续接材料" })).toBeDisabled();
  expect(fetchMock).toHaveBeenCalledTimes(1);
});
it("future consent names the effective selection while keeping original history visible", () => {
  const m = withMaterial("approved");
  m.model_continuations![0].state = "applied";
  m.model_continuations![0].application = {
    revision: 12,
    digest,
    created_at: "2026-10-24T00:02:00Z",
  };
  m.revision = 12;
  m.effective_model_snapshot = structuredClone(m.model_snapshot!);
  m.effective_model_snapshot.digest = newDigest;
  for (const role of roles) {
    m.effective_model_snapshot.roles[role].authorization!.id =
      "effective-local-sign-in";
    m.effective_model_snapshot.roles[role].profile.account_revision = 2;
    m.effective_model_snapshot.roles[role].account!.revision = 2;
  }
  render(<DecisionPanel mission={m} busy={false} onAction={vi.fn()} />);
  expect(screen.getByLabelText("本次审批模型依据")).toHaveTextContent(
    "effective-local-sign-in",
  );
  expect(screen.getByLabelText("原始冻结模型")).toHaveTextContent(
    "old-local-sign-in",
  );
  expect(fetchMock).not.toHaveBeenCalled();
});
it("blocks an applied selection with missing route instead of approving a partial snapshot", () => {
  const m = withMaterial("approved");
  m.model_continuations![0].state = "applied";
  m.model_continuations![0].application = {
    revision: 12,
    digest,
    created_at: "2026-10-24T00:02:00Z",
  };
  m.revision = 12;
  m.effective_model_snapshot = structuredClone(m.model_snapshot!);
  m.effective_model_snapshot.digest = newDigest;
  delete m.effective_model_snapshot.roles.planner.route;
  render(<Harness initial={m} />);
  expect(
    screen.getByRole("button", { name: "读取本地续接候选" }),
  ).toBeDisabled();
  expect(screen.queryByLabelText("当前有效模型选择")).not.toBeInTheDocument();
});
it("retains readable original history when continuation authority is invalid", () => {
  const m = withMaterial();
  m.model_continuation_status = "invalid_or_unsupported";
  render(<Harness initial={m} />);
  expect(screen.getByLabelText("原始冻结模型")).toHaveTextContent(
    "old-local-sign-in",
  );
  expect(screen.getByRole("button", { name: "批准续接材料" })).toBeDisabled();
  expect(fetchMock).not.toHaveBeenCalled();
});
it("uses the persisted-malformed-history HTTP contract without hiding original history or enabling authority", () => {
  // The HTTP regression verifies these exact status fields from actual corrupt
  // persisted history, with no history/effective selection in the response.
  const m = { ...fixture(), ...invalidHistoryContract } as Mission;
  const view = render(<Harness initial={m} />);
  expect(screen.getByLabelText("原始冻结模型")).toHaveTextContent(
    "old-local-sign-in",
  );
  expect(screen.queryByLabelText("当前有效模型选择")).not.toBeInTheDocument();
  for (const name of ["读取本地续接候选", "准备续接材料"]) {
    const button = screen.getByRole("button", { name });
    expect(button).toBeDisabled();
    fireEvent.click(button);
  }
  view.unmount();
  const onAction = vi.fn();
  const decision = render(
    <DecisionPanel mission={m} busy={false} onAction={onAction} />,
  );
  expect(
    screen.getByRole("button", { name: "授权上下文并生成计划" }),
  ).toBeDisabled();
  decision.rerender(
    <DecisionPanel
      mission={{ ...m, state: "awaiting_approval", plan: mission.plan }}
      busy={false}
      onAction={onAction}
    />,
  );
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeDisabled();
  expect(onAction).not.toHaveBeenCalled();
  expect(fetchMock).not.toHaveBeenCalled();
});
it.each([
  ["authorization.id", { unexpected: "value" }],
  ["authorization.id", ""],
  ["authorization.spend_class", { unexpected: "value" }],
  ["authorization.scopes", [{ unexpected: "value" }]],
  ["account.name", { unexpected: "value" }],
  ["account.billing_mode", { unexpected: "value" }],
  ["account.billing_mode", "__proto__"],
  ["account.revision", { unexpected: "value" }],
  ["profile.name", { unexpected: "value" }],
  ["profile.account_revision", { unexpected: "value" }],
  ["profile.credential_version", { unexpected: "value" }],
  ["route.adapter", { unexpected: "value" }],
  ["route.base_url", { unexpected: "value" }],
  ["route.spend_class", { unexpected: "value" }],
  ["catalog.operation_id", { unexpected: "value" }],
  ["catalog.authorization_generation", { unexpected: "value" }],
  ["catalog.expires_at", { unexpected: "value" }],
])(
  "blocks malformed effective scalar %s while retaining original history",
  (path, value) => {
    const m = withMaterial("approved");
    m.model_continuations![0].state = "applied";
    m.model_continuations![0].application = {
      revision: 12,
      digest,
      created_at: "2026-10-24T00:02:00Z",
    };
    m.revision = 12;
    m.effective_model_snapshot = structuredClone(m.model_snapshot!);
    m.effective_model_snapshot.digest = newDigest;
    const worker = m.effective_model_snapshot.roles.worker;
    worker.catalog = {
      operation_id: "local-catalog",
      receipt_digest: digest,
      authorization_generation: 1,
      expires_at: future,
    };
    const [group, field] = (path as string).split(".");
    (
      worker[group as keyof typeof worker] as unknown as Record<string, unknown>
    )[field] = value;
    render(<Harness initial={m} />);
    expect(screen.getByLabelText("原始冻结模型")).toHaveTextContent(
      "old-local-sign-in",
    );
    expect(screen.queryByLabelText("当前有效模型选择")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "读取本地续接候选" }),
    ).toBeDisabled();
    expect(screen.getByRole("button", { name: "准备续接材料" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "批准续接材料" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "应用续接" })).toBeDisabled();
    expect(fetchMock).not.toHaveBeenCalled();
  },
);
it("disables a pending approval at local expiry without sending a request", () => {
  vi.useFakeTimers();
  try {
    const m = withMaterial();
    m.model_continuations![0].expires_at = new Date(
      Date.now() + 1000,
    ).toISOString();
    render(<Harness initial={m} />);
    expect(screen.getByRole("button", { name: "批准续接材料" })).toBeEnabled();
    act(() => {
      vi.advanceTimersByTime(1001);
    });
    expect(screen.getByRole("button", { name: "批准续接材料" })).toBeDisabled();
    expect(screen.getByText("已过期")).toBeVisible();
    expect(fetchMock).not.toHaveBeenCalled();
  } finally {
    vi.useRealTimers();
  }
});
it("fences duplicate approval clicks while the first local request is pending", () => {
  fetchMock.mockImplementation(() => new Promise(() => {}));
  render(<Harness initial={withMaterial()} />);
  const approve = screen.getByRole("button", { name: "批准续接材料" });
  fireEvent.click(approve);
  fireEvent.click(approve);
  expect(approve).toBeDisabled();
  expect(screen.getByRole("button", { name: "应用续接" })).toBeDisabled();
  expect(fetchMock).toHaveBeenCalledTimes(1);
});
it("offers one recorded catalog covering distinct frozen role models without querying a provider", async () => {
  const m = fixture();
  m.model_snapshot!.roles.planner.profile.model = "second-model";
  fetchMock.mockImplementation(async (url: string) =>
    url === "/api/missions/synthetic-task/model-continuations/candidates"
      ? response({
          authorizations: [
            {
              account_id: "synthetic-account",
              id: "new-local-sign-in",
              state: "available",
              generation: 1,
              expires_at: future,
            },
          ],
        })
      : response({
          preparation: {
            phase: "unprepared",
            connection_ready: false,
            catalog_ready: false,
          },
          catalog_choices: ["fixture-model", "second-model"].map((model) => ({
            operation_id: "shared-catalog",
            route_id: "route",
            model,
            display_name: model,
            expires_at: future,
          })),
        }),
  );
  render(<Harness initial={m} />);
  fireEvent.click(screen.getByRole("button", { name: "读取本地续接候选" }));
  fireEvent.change(
    await screen.findByRole("combobox", { name: "本地续接候选" }),
    { target: { value: "0" } },
  );
  expect(screen.getByRole("button", { name: "准备续接材料" })).toBeEnabled();
  expect(screen.getAllByRole("option")).toHaveLength(2);
  expect(fetchMock).toHaveBeenCalledTimes(2);
});
