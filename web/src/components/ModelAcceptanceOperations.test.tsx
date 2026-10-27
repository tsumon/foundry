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
import { catalogFixture } from "../test/catalogFixtures";
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

function inferenceFixture() {
  const f = catalogFixture();
  const catalog = {
    ...f.pending,
    state: "succeeded",
    revision: 4,
    attempts: 1,
    claim_sequence: 2,
    certainty: "completed",
    result_account_revision: f.account.revision,
    result_snapshot_digest: "c".repeat(64),
    catalog: [{ id: "gpt-6.1-sol", display_name: "Fixed model" }],
  };
  const admission = {
    generation: "e".repeat(32),
    profile: "openai_siwc_inference_v1",
  };
  const pending = {
    ...f.pending,
    id: "synthetic-inference",
    phase: "inference",
    source: "production_inference",
    live_access: "inference_once",
    material: {
      ...f.pending.material,
      phase: "inference",
      production_admission: admission,
      request_method: "POST",
      request_url: "https://api.openai.com/v1/responses",
      model: "gpt-6.1-sol",
      inference_prompt: "Reply exactly: FOUNDRY_INFERENCE_OK",
      prompt_digest:
        "90f75a47756b44fbcfe856df18999e6c3666e9045e0a95dca5f3bce873a2f8bb",
      catalog_digest: "a".repeat(64),
      prerequisite_id: catalog.id,
      response_byte_limit: 1048576,
      inference_budget: {
        schema_version: 1,
        mission_id: "synthetic-task",
        revision: 1,
        epoch: 1,
        snapshot_digest: digest,
        used_calls: 3,
        max_calls: 7,
      },
    },
  };
  return {
    ...f,
    catalog,
    pending,
    history: {
      ...f.history,
      operations: [f.connection, catalog, pending],
      production_catalog_enabled: false,
      production_inference_enabled: true,
      production_admission: admission,
    },
  };
}
async function selectInference() {
  await screen.findByRole("option", { name: /^synthetic-inference ·/ });
  fireEvent.change(screen.getByLabelText("推理已保存材料"), {
    target: { value: "synthetic-inference" },
  });
}

it.each(["old connection", "old generation"])(
  "does not let expired %s diagnostic history disable a fresh inference gate",
  async (kind) => {
    const f = inferenceFixture();
    const old = structuredClone(f.pending);
    old.id = "historical-inference";
    old.material.created_at = new Date(Date.now() - 700000).toISOString();
    old.material.expires_at = new Date(Date.now() - 100000).toISOString();
    old.material.authorization.expires_at = old.material.expires_at;
    old.material.production_admission.generation = "f".repeat(32);
    if (kind === "old connection") {
      old.material.connection_id = "historical-connection";
      old.material.authorization.binding.id = "historical-auth";
    } else {
      f.catalog.material.authorization.generation = 2;
    }
    const historical = {
      ...old,
      state: "unknown",
      attempts: 1,
      claim_sequence: 3,
      certainty: "unknown",
      code: "response_incomplete",
    };
    const fetcher = vi.fn(async () =>
      response({
        ...f.history,
        operations: [f.connection, f.catalog, historical],
      }),
    );
    vi.stubGlobal("fetch", fetcher);
    render(
      <ModelAcceptanceOperations
        account={f.account}
        refreshAccount={async () => {}}
      />,
    );
    const budget = await screen.findByLabelText("预算任务 ID");
    expect(budget).toBeEnabled();
    fireEvent.change(budget, { target: { value: "synthetic-task" } });
    fireEvent.change(screen.getByLabelText("预期任务版本"), {
      target: { value: "53" },
    });
    expect(
      screen.getByRole("button", { name: "创建真实推理材料" }),
    ).toBeEnabled();
    fireEvent.change(screen.getByLabelText("推理已保存材料"), {
      target: { value: historical.id },
    });
    expect(screen.getByText("response_incomplete")).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "执行推理" }),
    ).not.toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(1);
  },
);

it("requires an explicit task revision before preparing a charged diagnostic", async () => {
  const f = inferenceFixture();
  const fetcher = vi.fn(async () =>
    response({ ...f.history, operations: [f.connection, f.catalog] }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  const create = await screen.findByRole("button", {
    name: "创建真实推理材料",
  });
  await waitFor(() =>
    expect(screen.getByLabelText("推理模型")).toBeInTheDocument(),
  );
  expect(create).toBeDisabled();
  fireEvent.change(screen.getByLabelText("预算任务 ID"), {
    target: { value: "synthetic-task" },
  });
  expect(create).toBeDisabled();
  fireEvent.change(screen.getByLabelText("预期任务版本"), {
    target: { value: "1" },
  });
  expect(create).toBeEnabled();
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it.each([
  null,
  "unsafe",
  { schema_version: 2 },
  { revision: 0 },
  { used_calls: 7 },
  { snapshot_digest: "unsafe" },
  { epoch: -1 },
])(
  "rejects malformed task budget material without mutations: %j",
  async (change) => {
    const f = inferenceFixture();
    const material = {
      ...f.pending.material,
      inference_budget:
        typeof change === "object" && change !== null
          ? { ...f.pending.material.inference_budget, ...change }
          : change,
    };
    const fetcher = vi.fn(async () =>
      response({
        ...f.history,
        operations: [f.connection, f.catalog, { ...f.pending, material }],
      }),
    );
    vi.stubGlobal("fetch", fetcher);
    render(
      <ModelAcceptanceOperations
        account={f.account}
        refreshAccount={async () => {}}
      />,
    );
    expect(await screen.findByText("invalid_receipt")).toBeVisible();
    expect(fetcher).toHaveBeenCalledTimes(1);
  },
);

it("keeps historical unbound inference readable but cannot approve or execute it", async () => {
  const f = inferenceFixture();
  delete (f.pending.material as Record<string, unknown>).inference_budget;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response(f.history)),
  );
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  await selectInference();
  expect(screen.getByRole("button", { name: "批准推理材料" })).toBeDisabled();
});

it("shows bounded missing-media diagnostics without promoting unknown to success", async () => {
  const f = inferenceFixture();
  const unknown = {
    ...f.pending,
    state: "unknown",
    attempts: 1,
    claim_sequence: 3,
    certainty: "unknown",
    code: "response_incomplete",
    inference_diagnostic: {
      exchange: {
        version: 1,
        client_request_id: "fdy_" + "a".repeat(32),
        dns: { attempts: 0, millis: 0 },
        connect: { attempts: 0, millis: 0 },
        tls: { attempts: 0, millis: 0 },
        headers: { state: "completed", attempts: 1, millis: 1 },
        first_byte_millis: 1,
        request_writes: 1,
        write_failed: false,
        reused_connection: false,
        proxy_mode: "disabled",
        peer_class: "loopback",
        tls_verified: false,
        final_status: 200,
        header_fields: 0,
        transport: {
          version: 1,
          content_type: "absent",
          protocol: "http1_1",
          body_length: "unknown",
        },
      },
      response: {
        version: 1,
        stage: "http_media",
        reason: "missing_media_type",
        media_type: "missing",
        bytes_read: 0,
        events_seen: 0,
        transport: {
          version: 1,
          content_type: "absent",
          protocol: "http1_1",
          body_length: "unknown",
        },
      },
    },
  };
  const fetcher = vi.fn(async () =>
    response({ ...f.history, operations: [f.connection, f.catalog, unknown] }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  await selectInference();
  expect(screen.getByText(/响应缺少内容类型/)).toBeVisible();
  expect(screen.getByText(/已扣除原任务 1 次调用：4\/7/)).toBeVisible();
  expect(screen.getByText("传输：结果未知")).toBeVisible();
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("reads a legitimate running inference claim as consumed and unknown without polling or mutation", async () => {
  vi.useFakeTimers();
  try {
    const f = inferenceFixture();
    const running = {
      ...f.pending,
      revision: 3,
      state: "running",
      attempts: 1,
      claim_sequence: 3,
      certainty: "unknown",
    };
    const fetcher = vi.fn(async () =>
      response({
        ...f.history,
        operations: [f.connection, f.catalog, running],
      }),
    );
    vi.stubGlobal("fetch", fetcher);
    render(
      <ModelAcceptanceOperations
        account={f.account}
        refreshAccount={async () => {}}
      />,
    );
    await act(async () => {});
    expect(screen.queryByText("invalid_receipt")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("推理已保存材料"), {
      target: { value: running.id },
    });
    expect(screen.getByText("传输：结果未知")).toBeVisible();
    expect(screen.getByText(/单次尝试已消耗；没有重试操作/)).toBeVisible();
    expect(
      screen.queryByRole("button", { name: /批准推理|执行推理/ }),
    ).not.toBeInTheDocument();
    await act(async () => {
      vi.advanceTimersByTime(60000);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);
  } finally {
    vi.useRealTimers();
  }
});

it("rejects a forged running inference claim that reports not_sent certainty", async () => {
  const f = inferenceFixture();
  const running = {
    ...f.pending,
    revision: 3,
    state: "running",
    attempts: 1,
    claim_sequence: 3,
    certainty: "not_sent",
  };
  const fetcher = vi.fn(async () =>
    response({ ...f.history, operations: [f.connection, f.catalog, running] }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  expect(await screen.findByText("invalid_receipt")).toBeVisible();
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("explicitly reconciles a lost inference execution to still running without poll or replay", async () => {
  vi.useFakeTimers();
  try {
    const f = inferenceFixture();
    const approved = { ...f.pending, revision: 2, state: "approved" };
    const running = {
      ...approved,
      revision: 3,
      state: "running",
      attempts: 1,
      claim_sequence: 3,
      certainty: "unknown",
    };
    let historyReads = 0;
    const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
      if (init?.method) throw new Error("synthetic lost execute response");
      if (path.endsWith(`/${running.id}`))
        return response({ operation: running });
      historyReads++;
      return response({
        ...f.history,
        operations: [
          f.connection,
          f.catalog,
          historyReads === 1 ? approved : running,
        ],
      });
    });
    vi.stubGlobal("fetch", fetcher);
    render(
      <ModelAcceptanceOperations
        account={f.account}
        refreshAccount={async () => {}}
      />,
    );
    await act(async () => {});
    fireEvent.change(screen.getByLabelText("推理已保存材料"), {
      target: { value: approved.id },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "执行推理" }));
    });
    expect(
      screen.getByRole("button", { name: "仅读取原验收记录" }),
    ).toBeEnabled();
    expect(fetcher).toHaveBeenCalledTimes(2);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "仅读取原验收记录" }));
    });
    expect(screen.queryByText("invalid_receipt")).not.toBeInTheDocument();
    expect(screen.getByText("传输：结果未知")).toBeVisible();
    expect(screen.getByText(/单次尝试已消耗；没有重试操作/)).toBeVisible();
    expect(
      screen.queryByRole("button", { name: /批准推理|执行推理/ }),
    ).not.toBeInTheDocument();
    await act(async () => {
      vi.advanceTimersByTime(60000);
    });
    expect(fetcher).toHaveBeenCalledTimes(4);
    const writes = fetcher.mock.calls.filter(([, init]) => init?.method);
    expect(writes).toHaveLength(1);
    expect(writes[0][0]).toBe(
      `/api/model-accounts/${f.account.id}/acceptance-operations/${approved.id}/execute`,
    );
    expect(JSON.parse(String(writes[0][1]?.body))).toEqual({
      expected_revision: 2,
      material_digest: approved.material_digest,
    });
  } finally {
    vi.useRealTimers();
  }
});

it.each(
  (["catalog", "connection"] as const).flatMap((mode) =>
    (["pending", "approved", "succeeded"] as const).map((state) => ({
      mode,
      state,
    })),
  ),
)(
  "keeps $state inference history read-only in $mode mode without blocking its independent approval",
  async ({ mode, state }) => {
    const f = inferenceFixture();
    const admission = {
      generation: "d".repeat(32),
      profile:
        mode === "catalog"
          ? "openai_siwc_catalog_v1"
          : "openai_siwc_subscription_v1",
    };
    const historical = {
      ...f.pending,
      state,
      ...(state === "succeeded"
        ? {
            attempts: 1,
            claim_sequence: 3,
            certainty: "completed",
            terminal_event: "response.completed",
            resolved_model: "gpt-6.1-sol",
            output_matched: true,
            output_digest: digest,
          }
        : {}),
    };
    const next =
      mode === "catalog"
        ? catalogFixture().pending
        : {
            ...f.connection,
            revision: 1,
            state: "pending",
            attempts: 0,
            claim_sequence: 0,
            certainty: "not_sent",
            result_account_revision: undefined,
            result_snapshot_digest: undefined,
          };
    let active = {
      ...next,
      state: "pending",
      id: `fresh-${mode}`,
      material: {
        ...next.material,
        account: { ...f.account },
        production_admission: admission,
      },
    };
    const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
      if (init?.method) {
        if (path.endsWith("/decision"))
          active = { ...active, revision: 2, state: "approved" };
        return response({ operation: active });
      }
      return response({
        ...f.history,
        operations: [f.connection, f.catalog, historical],
        production_inference_enabled: false,
        production_catalog_enabled: mode === "catalog",
        production_connection_enabled: mode === "connection",
        production_admission: admission,
        routes: [
          {
            ...f.route,
            ...(mode === "connection"
              ? {
                  connection_purpose: "subscription_only",
                  connection_mode: "browser_handoff",
                }
              : {}),
          },
        ],
      });
    });
    vi.stubGlobal("fetch", fetcher);
    render(
      <ModelAcceptanceOperations
        account={f.account}
        refreshAccount={async () => {}}
      />,
    );
    const phase = mode === "catalog" ? "目录" : "连接";
    const create = await screen.findByRole("button", {
      name: `创建真实${phase}材料`,
    });
    await waitFor(() => expect(create).toBeEnabled());
    await selectInference();
    expect(
      within(screen.getByLabelText("推理材料审阅")).getByText(
        "Reply exactly: FOUNDRY_INFERENCE_OK",
      ),
    ).toBeVisible();
    expect(
      within(screen.getByLabelText("推理材料审阅")).queryByRole("button"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /创建真实推理|批准推理|执行推理/ }),
    ).not.toBeInTheDocument();
    expect(fetcher.mock.calls.filter(([, init]) => init?.method)).toHaveLength(
      0,
    );
    fireEvent.click(create);
    fireEvent.click(
      await screen.findByRole("button", { name: `批准${phase}材料` }),
    );
    expect(
      await screen.findByRole("button", { name: `执行${phase}` }),
    ).toBeEnabled();
    const writes = fetcher.mock.calls.filter(([, init]) => init?.method);
    expect(writes).toHaveLength(2);
    expect(JSON.parse(String(writes[0][1]?.body)).phase).toBe(mode);
    expect(writes[1][0]).toContain(`/fresh-${mode}/decision`);
  },
);

it("keeps all nonconnection phases read-only when an inference history response omits catalog rows", async () => {
  const f = inferenceFixture();
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        ...f.history,
        operations: [f.connection, f.pending],
        production_inference_enabled: false,
        production_catalog_enabled: false,
        production_connection_enabled: true,
        production_admission: {
          generation: "d".repeat(32),
          profile: "openai_siwc_subscription_v1",
        },
        routes: [
          {
            ...f.route,
            connection_purpose: "subscription_only",
            connection_mode: "browser_handoff",
          },
        ],
      }),
    ),
  );
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  expect(
    await screen.findByRole("button", { name: "创建真实连接材料" }),
  ).toBeEnabled();
  await selectInference();
  expect(
    screen.queryByRole("button", {
      name: /创建真实目录|创建真实推理|批准推理|执行推理/,
    }),
  ).not.toBeInTheDocument();
});

it.each(["catalog", "connection"])(
  "rejects malformed historical inference even in %s mode",
  async (mode) => {
    const f = inferenceFixture();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        response({
          ...f.history,
          operations: [
            f.connection,
            f.catalog,
            {
              ...f.pending,
              material: { ...f.pending.material, model: "wrong-model" },
            },
          ],
          production_inference_enabled: false,
          production_catalog_enabled: mode === "catalog",
          production_connection_enabled: mode === "connection",
          production_admission: {
            generation: "d".repeat(32),
            profile:
              mode === "catalog"
                ? "openai_siwc_catalog_v1"
                : "openai_siwc_subscription_v1",
          },
          routes: [
            {
              ...f.route,
              ...(mode === "connection"
                ? {
                    connection_purpose: "subscription_only",
                    connection_mode: "browser_handoff",
                  }
                : {}),
            },
          ],
        }),
      ),
    );
    render(
      <ModelAcceptanceOperations
        account={f.account}
        refreshAccount={async () => {}}
      />,
    );
    expect(await screen.findByText("invalid_receipt")).toBeVisible();
  },
);

it("reports a consumed pre-send credential failure as not sent, without retry controls", async () => {
  const f = inferenceFixture();
  const failed = {
    ...f.pending,
    state: "failed",
    attempts: 1,
    claim_sequence: 3,
    certainty: "not_sent",
    code: "auth_invalid",
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({ ...f.history, operations: [f.connection, f.catalog, failed] }),
    ),
  );
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  await selectInference();
  expect(screen.getByText("传输：未发送")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "执行推理" }),
  ).not.toBeInTheDocument();
});

it.each([
  [
    "usage on unconfirmed failure",
    {
      state: "failed",
      certainty: "not_sent",
      code: "auth_invalid",
      usage_known: true,
    },
  ],
  [
    "terminal on unconfirmed failure",
    {
      state: "failed",
      certainty: "not_sent",
      code: "auth_invalid",
      terminal_event: "response.failed",
    },
  ],
  [
    "usage on nonterminal rejection",
    {
      state: "failed",
      certainty: "rejected",
      code: "auth_invalid",
      usage_known: true,
    },
  ],
])(
  "rejects %s instead of inventing provider evidence",
  async (_name, extra) => {
    const f = inferenceFixture();
    const failed = { ...f.pending, attempts: 1, claim_sequence: 3, ...extra };
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        response({
          ...f.history,
          operations: [f.connection, f.catalog, failed],
        }),
      ),
    );
    render(
      <ModelAcceptanceOperations
        account={f.account}
        refreshAccount={async () => {}}
      />,
    );
    expect(await screen.findByText("invalid_receipt")).toBeVisible();
  },
);
it("independently creates, approves and executes fixed production inference without automatic mutations", async () => {
  const f = inferenceFixture();
  let current = { ...f.pending, state: "pending" };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method)
      return response({ ...f.history, operations: [f.connection, f.catalog] });
    if (path.endsWith("/decision"))
      current = { ...current, revision: 2, state: "approved" };
    if (path.endsWith("/execute"))
      return response({
        operation: {
          ...current,
          revision: 4,
          state: "succeeded",
          attempts: 1,
          claim_sequence: 3,
          certainty: "completed",
          terminal_event: "response.completed",
          resolved_model: "gpt-6.1-sol",
          output_matched: true,
          output_digest: digest,
          usage_known: true,
        },
      });
    return response({ operation: current });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  const create = await screen.findByRole("button", {
    name: "创建真实推理材料",
  });
  fireEvent.change(screen.getByLabelText("预算任务 ID"), {
    target: { value: "synthetic-task" },
  });
  fireEvent.change(screen.getByLabelText("预期任务版本"), {
    target: { value: "1" },
  });
  await waitFor(() => expect(create).toBeEnabled());
  expect(fetcher.mock.calls.filter(([, init]) => init?.method)).toHaveLength(0);
  expect(
    screen.queryByRole("button", { name: /创建真实连接|创建真实目录/ }),
  ).not.toBeInTheDocument();
  fireEvent.click(create);
  const approve = await screen.findByRole("button", { name: "批准推理材料" });
  expect(JSON.parse(String(fetcher.mock.calls[1][1]?.body))).toEqual({
    phase: "inference",
    route_id: f.route.id,
    expected_account_revision: 2,
    model: "gpt-6.1-sol",
    prerequisite_id: f.catalog.id,
    budget_mission_id: "synthetic-task",
    expected_mission_revision: 1,
  });
  expect(screen.getByText("Reply exactly: FOUNDRY_INFERENCE_OK")).toBeVisible();
  expect(screen.getByText(/未设置 max_output_tokens/)).toBeVisible();
  expect(
    screen.getByText("POST https://api.openai.com/v1/responses"),
  ).toBeVisible();
  fireEvent.click(approve);
  const execute = await screen.findByRole("button", { name: "执行推理" });
  expect(
    fetcher.mock.calls.filter(([path]) => path.endsWith("/execute")),
  ).toHaveLength(0);
  fireEvent.click(execute);
  expect(await screen.findByText("输出校验：匹配")).toBeVisible();
  expect(screen.getByText("实际用量：输入 0 · 输出 0 tokens")).toBeVisible();
  expect(fetcher.mock.calls.filter(([, init]) => init?.method)).toHaveLength(3);
});
it.each([
  ["false", { production_inference_enabled: false }],
  ["absent", { production_inference_enabled: undefined }],
  ["mixed", { production_catalog_enabled: true }],
  ["simulation", { simulation_enabled: true }],
  ["missing admission", { production_admission: undefined }],
  [
    "wrong profile",
    {
      production_admission: {
        generation: "e".repeat(32),
        profile: "openai_siwc_catalog_v1",
      },
    },
  ],
])("denies inference with %s capability", async (_name, extra) => {
  const f = inferenceFixture();
  const fetcher = vi.fn(async () => response({ ...f.history, ...extra }));
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  await waitFor(() =>
    expect(screen.queryByText("正在读取验收记录…")).not.toBeInTheDocument(),
  );
  for (const button of screen.queryAllByRole("button", {
    name: /创建.*材料|批准推理|执行推理/,
  }))
    expect(button).toBeDisabled();
  expect(fetcher).toHaveBeenCalledTimes(1);
});
it.each([
  "stale generation",
  "expired",
  "wrong model",
  "missing model",
  "stale catalog",
  "expired credential",
  "stale catalog result",
  "malformed catalog result",
])("closes inference for %s material or lineage", async (kind) => {
  const f = inferenceFixture();
  const p = structuredClone(f.pending);
  if (kind === "stale generation")
    p.material.production_admission.generation = "b".repeat(32);
  if (kind === "wrong model") p.material.model = "other";
  if (kind === "expired" || kind === "expired credential") {
    p.material.created_at = new Date(Date.now() - 700000).toISOString();
    p.material.expires_at = new Date(
      Date.parse(p.material.created_at) + 600000,
    ).toISOString();
    if (kind === "expired credential")
      p.material.authorization.expires_at = p.material.expires_at;
  }
  if (kind === "missing model") f.catalog.catalog = [];
  if (kind === "stale catalog") f.catalog.material.account.revision = 1;
  if (kind === "stale catalog result") f.catalog.result_account_revision = 1;
  if (kind === "malformed catalog result")
    f.catalog.result_snapshot_digest = "broken";
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({ ...f.history, operations: [f.connection, f.catalog, p] }),
    ),
  );
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  await waitFor(() =>
    expect(screen.queryByText("正在读取验收记录…")).not.toBeInTheDocument(),
  );
  if (screen.queryByRole("option", { name: /^synthetic-inference ·/ }))
    await selectInference();
  const approve = screen.queryByRole("button", { name: "批准推理材料" });
  if (approve) expect(approve).toBeDisabled();
  else expect(screen.getByRole("alert")).toBeVisible();
});
it.each([true, false])(
  "reports completed inference match=%s separately from unknown usage",
  async (matched) => {
    const f = inferenceFixture();
    const terminal = {
      ...f.pending,
      state: "succeeded",
      attempts: 1,
      claim_sequence: 3,
      certainty: "completed",
      terminal_event: "response.completed",
      resolved_model: "gpt-6.1-sol",
      output_matched: matched,
      output_digest: digest,
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        response({
          ...f.history,
          operations: [f.connection, f.catalog, terminal],
        }),
      ),
    );
    render(
      <ModelAcceptanceOperations
        account={f.account}
        refreshAccount={async () => {}}
      />,
    );
    await selectInference();
    expect(screen.getByText("传输：已完成")).toBeVisible();
    expect(
      screen.getByText(
        matched ? "输出校验：匹配" : "输出校验：不匹配（不重试）",
      ),
    ).toBeVisible();
    expect(
      screen.getByText("实际用量：未知；不能报告零消耗或零费用"),
    ).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "执行推理" }),
    ).not.toBeInTheDocument();
  },
);
it.each(["lost", "stale", "changed material"])(
  "locks %s inference decision responses until explicit read, never replaying",
  async (kind) => {
    const f = inferenceFixture();
    const pending = { ...f.pending, revision: 2 };
    const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
      if (init?.method) {
        if (kind === "lost") throw new Error("synthetic lost response");
        return response({
          operation: {
            ...pending,
            revision: kind === "stale" ? 1 : 3,
            state: "approved",
            material: {
              ...pending.material,
              ...(kind === "changed material"
                ? { data_summary: "changed frozen data" }
                : {}),
            },
          },
        });
      }
      if (path.endsWith(`/${pending.id}`))
        return response({
          operation: {
            ...pending,
            revision: 4,
            state: "unknown",
            attempts: 1,
            claim_sequence: 3,
            certainty: "unknown",
          },
        });
      return response({
        ...f.history,
        operations: [f.connection, f.catalog, pending],
      });
    });
    vi.stubGlobal("fetch", fetcher);
    render(
      <ModelAcceptanceOperations
        account={f.account}
        refreshAccount={async () => {}}
      />,
    );
    await selectInference();
    fireEvent.click(screen.getByRole("button", { name: "批准推理材料" }));
    const read = await screen.findByRole("button", {
      name: "仅读取原验收记录",
    });
    expect(fetcher.mock.calls.filter(([, init]) => init?.method)).toHaveLength(
      1,
    );
    fireEvent.click(read);
    await waitFor(() =>
      expect(
        fetcher.mock.calls.filter(([, init]) => !init?.method).length,
      ).toBe(3),
    );
    expect(fetcher.mock.calls.filter(([, init]) => init?.method)).toHaveLength(
      1,
    );
    expect(
      screen.queryByRole("button", { name: "执行推理" }),
    ).not.toBeInTheDocument();
    expect(await screen.findByText("传输：结果未知")).toBeVisible();
  },
);

it("keeps inference history readable and execution closed without process admission", async () => {
  const f = inferenceFixture();
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        ...f.history,
        production_admission: undefined,
        production_inference_enabled: false,
        routes: [],
        operations: [
          f.connection,
          f.catalog,
          { ...f.pending, state: "approved" },
        ],
      }),
    ),
  );
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  await selectInference();
  expect(screen.getByText("Reply exactly: FOUNDRY_INFERENCE_OK")).toBeVisible();
  expect(screen.getByRole("button", { name: "执行推理" })).toBeDisabled();
});

it("expires inference and credential controls locally without polling or secret-triggering calls", async () => {
  vi.useFakeTimers();
  try {
    const f = inferenceFixture();
    const expiry = new Date(Date.now() + 1000).toISOString();
    f.pending.material.authorization.expires_at = expiry;
    f.pending.material.expires_at = expiry;
    f.catalog.material.authorization.expires_at = expiry;
    f.catalog.material.expires_at = expiry;
    const fetcher = vi.fn(async () => response(f.history));
    vi.stubGlobal("fetch", fetcher);
    render(
      <ModelAcceptanceOperations
        account={f.account}
        refreshAccount={async () => {}}
      />,
    );
    await act(async () => {});
    fireEvent.change(screen.getByLabelText("推理已保存材料"), {
      target: { value: f.pending.id },
    });
    expect(screen.getByRole("button", { name: "批准推理材料" })).toBeEnabled();
    await act(async () => {
      vi.advanceTimersByTime(1001);
    });
    expect(screen.getByRole("button", { name: "批准推理材料" })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "创建真实推理材料" }),
    ).toBeDisabled();
    expect(fetcher).toHaveBeenCalledTimes(1);
  } finally {
    vi.useRealTimers();
  }
});

it.each([
  ["endpoint", { request_url: "https://example.invalid" }],
  ["token cap override", { max_output_tokens: 128 }],
  ["prompt override", { inference_prompt: "different prompt" }],
  ["digest", { prompt_digest: "e".repeat(64) }],
  ["missing prerequisite", { prerequisite_id: undefined }],
  ["response bound", { response_byte_limit: 2048 }],
])(
  "rejects malformed inference %s without rendering mutation controls",
  async (_name, change) => {
    const f = inferenceFixture();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        response({
          ...f.history,
          operations: [
            f.connection,
            f.catalog,
            { ...f.pending, material: { ...f.pending.material, ...change } },
          ],
        }),
      ),
    );
    render(
      <ModelAcceptanceOperations
        account={f.account}
        refreshAccount={async () => {}}
      />,
    );
    expect(await screen.findByText("invalid_receipt")).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "批准推理材料" }),
    ).not.toBeInTheDocument();
  },
);

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

// Production presentation is tested with offline DTOs only; fetch never leaves this stub.
const productionAccount = {
  ...handoffAccount,
  base_url: "https://api.openai.com/v1",
};
const productionRoute = {
  ...handoffRoute,
  environment: "live",
  base_url: productionAccount.base_url,
};
const productionAdmission = {
  generation: "a".repeat(32),
  profile: "openai_siwc_subscription_v1",
};
const productionOperation = (
  state = "pending",
  extra: Record<string, unknown> = {},
) => {
  const now = Date.now();
  return {
    ...handoffOperation("production-connection", state),
    source: "production_connection",
    live_access: "connection_only",
    material: {
      ...handoffMaterial(),
      account: productionAccount,
      route: { ...handoffMaterial().route, ...productionRoute },
      production_admission: productionAdmission,
      created_at: new Date(now).toISOString(),
      expires_at: new Date(now + 600_000).toISOString(),
    },
    ...extra,
  };
};
const productionHistory = (
  operations: unknown[] = [],
  extra: Record<string, unknown> = {},
) => ({
  operations,
  routes: [productionRoute],
  simulation_enabled: false,
  legacy_siwc_simulation_enabled: false,
  production_connection_enabled: true,
  production_admission: productionAdmission,
  ...extra,
});

it("separates real connection proposal, six-scope approval, execution and manual handoff without model actions", async () => {
  const writes: Array<{ path: string; body: unknown }> = [];
  let current = productionOperation();
  const opened = vi.spyOn(window, "open").mockImplementation(() => null);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      if (!init?.method) return response(productionHistory());
      writes.push({ path, body: JSON.parse(String(init.body)) });
      if (path.endsWith("/decision"))
        current = { ...current, state: "approved", revision: 2 };
      if (path.endsWith("/execute")) {
        current = {
          ...current,
          state: "running",
          revision: 3,
          attempts: 1,
          claim_sequence: 1,
        };
        return response({
          operation: current,
          connection_session: handoffSession(current.id),
        });
      }
      return response({ operation: current });
    }),
  );
  render(
    <ModelAcceptanceOperations
      account={productionAccount}
      refreshAccount={async () => {}}
    />,
  );
  const create = await screen.findByRole("button", {
    name: "创建真实连接材料",
  });
  expect(create).toBeEnabled();
  expect(
    screen.queryByRole("button", { name: /创建目录|创建推理/ }),
  ).not.toBeInTheDocument();
  fireEvent.click(create);
  const approve = await screen.findByRole("button", { name: "批准连接材料" });
  expect(writes).toEqual([
    {
      path: `/api/model-accounts/${productionAccount.id}/acceptance-operations`,
      body: {
        phase: "connection",
        route_id: productionRoute.id,
        expected_account_revision: 1,
        connection_purpose: "subscription_only",
      },
    },
  ]);
  for (const scope of subscriptionScopes)
    expect(screen.getByText(scope, { selector: "code" })).toBeVisible();
  expect(screen.getByText(/令牌保存在.*系统凭据存储/)).toBeVisible();
  expect(screen.getByText(/账户与工作区由用户/)).toBeVisible();
  expect(screen.getByText(/远端授权不是单次/)).toBeVisible();
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  fireEvent.click(approve);
  const execute = await screen.findByRole("button", { name: "执行连接" });
  expect(writes).toHaveLength(2);
  expect(writes[1].body).toEqual({
    expected_revision: 1,
    material_digest: digest,
    decision: "approve",
  });
  expect(writes.some((item) => item.path.endsWith("/execute"))).toBe(false);
  fireEvent.click(execute);
  expect(
    await screen.findByRole("link", { name: "手动打开真实连接授权入口" }),
  ).toHaveAttribute("href", "http://127.0.0.1:43126/auth/start");
  expect(writes).toHaveLength(3);
  expect(writes[2].body).toEqual({
    expected_revision: 2,
    material_digest: digest,
  });
  expect(opened).not.toHaveBeenCalled();
});

it.each([
  [
    "default old receipt",
    {
      routes: [],
      production_connection_enabled: false,
      production_admission: undefined,
    },
  ],
  ["expired readiness", { production_connection_enabled: false }],
  [
    "new generation",
    {
      production_admission: {
        ...productionAdmission,
        generation: "b".repeat(32),
      },
    },
  ],
])(
  "keeps %s readable without reauthorizing a production receipt",
  async (_name, overrides) => {
    const old = productionOperation("approved");
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => response(productionHistory([old], overrides))),
    );
    render(
      <ModelAcceptanceOperations
        account={productionAccount}
        refreshAccount={async () => {}}
      />,
    );
    fireEvent.click(
      await screen.findByRole("button", {
        name: /连接 · 已批准，尚未执行 · 真实订阅连接/,
      }),
    );
    expect(screen.getByRole("button", { name: "执行连接" })).toBeDisabled();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /创建目录|创建推理/ }),
    ).not.toBeInTheDocument();
  },
);

it.each([
  ["boolean alone", { production_admission: undefined }],
  [
    "malformed generation",
    {
      production_admission: {
        ...productionAdmission,
        generation: "A".repeat(32),
      },
    },
  ],
  [
    "unknown profile",
    { production_admission: { ...productionAdmission, profile: "other" } },
  ],
  ["mixed simulation", { simulation_enabled: true }],
  ["mixed legacy authority", { legacy_siwc_simulation_enabled: true }],
  [
    "nonofficial route",
    {
      routes: [{ ...productionRoute, base_url: "https://attacker.invalid/v1" }],
    },
  ],
  ["duplicate routes", { routes: [productionRoute, productionRoute] }],
  [
    "catalog receipt",
    { operations: [productionOperation("approved", { phase: "catalog" })] },
  ],
  [
    "mixed source",
    {
      operations: [
        productionOperation("approved", { source: "local_simulation" }),
      ],
    },
  ],
])("fails closed on production %s", async (_name, overrides) => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response(productionHistory([], overrides))),
  );
  render(
    <ModelAcceptanceOperations
      account={productionAccount}
      refreshAccount={async () => {}}
    />,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent("invalid_receipt");
  expect(
    screen.queryByRole("button", { name: "创建真实连接材料" }),
  ).not.toBeInTheDocument();
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
});

it("allows owned running production status and cancel after readiness expires without restoring a history link", async () => {
  const running = productionOperation("running", {
    attempts: 1,
    claim_sequence: 1,
  });
  const requests: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      requests.push(`${init?.method ?? "GET"} ${path}`);
      if (path.endsWith("/cancel"))
        return response({
          operation: {
            ...running,
            state: "failed",
            revision: 4,
            certainty: "unknown",
            code: "transport_unknown",
          },
        });
      if (path.endsWith(running.id))
        return response({
          operation: running,
          connection_session: handoffSession(running.id),
        });
      return response(
        productionHistory([running], { production_connection_enabled: false }),
      );
    }),
  );
  render(
    <ModelAcceptanceOperations
      account={productionAccount}
      refreshAccount={async () => {}}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: /连接 · 已消耗，执行中 · 真实订阅连接/,
    }),
  );
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "读取连接状态" })).toBeEnabled();
  fireEvent.click(screen.getByRole("button", { name: "读取连接状态" }));
  expect(
    await screen.findByRole("link", { name: "手动打开真实连接授权入口" }),
  ).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "取消连接会话" }));
  expect(await screen.findByText(/未记录的结果仍无法确认/)).toBeVisible();
  expect(screen.getByLabelText("连接失败诊断")).toHaveTextContent("阶段未记录");
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  expect(requests.filter((item) => item.startsWith("POST"))).toHaveLength(1);
});

it("expires a displayed production approval locally without polling or sending execution", async () => {
  const approved = productionOperation("approved");
  const fetcher = vi.fn(async () => response(productionHistory([approved])));
  vi.stubGlobal("fetch", fetcher);
  vi.useFakeTimers();
  try {
    await act(async () => {
      render(
        <ModelAcceptanceOperations
          account={productionAccount}
          refreshAccount={async () => {}}
        />,
      );
    });
    fireEvent.click(
      screen.getByRole("button", {
        name: /连接 · 已批准，尚未执行 · 真实订阅连接/,
      }),
    );
    expect(screen.getByRole("button", { name: "执行连接" })).toBeEnabled();
    // A rerender is not needed to revoke a deadline-bound approval.
    act(() => vi.advanceTimersByTime(600_001));
    expect(screen.getByRole("button", { name: "执行连接" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "执行连接" }));
    expect(fetcher).toHaveBeenCalledOnce();
  } finally {
    vi.useRealTimers();
  }
});

it("does not enable a production proposal for a mismatched account snapshot", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response(productionHistory())),
  );
  render(
    <ModelAcceptanceOperations
      account={{ ...productionAccount, provider: "other" }}
      refreshAccount={async () => {}}
    />,
  );
  expect(
    await screen.findByRole("button", { name: "创建真实连接材料" }),
  ).toBeDisabled();
});

it.each([
  ["short scopes", { requested_scopes: ["openid", "profile", "email"] }],
  ["wrong resource", { requested_resource: "https://attacker.invalid/v1" }],
  ["missing admission", { production_admission: undefined }],
  ["missing browser mode", { connection_mode: undefined }],
  ["wrong timeout", { login_timeout_seconds: 30 }],
  ["unbounded request", { deadline_seconds: 60 }],
  ["retries", { max_attempts: 2 }],
])(
  "rejects malformed production material: %s",
  async (_name, materialOverrides) => {
    const receipt = productionOperation();
    receipt.material = {
      ...receipt.material,
      ...materialOverrides,
    } as typeof receipt.material;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => response(productionHistory([receipt]))),
    );
    render(
      <ModelAcceptanceOperations
        account={productionAccount}
        refreshAccount={async () => {}}
      />,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "invalid_receipt",
    );
    expect(
      screen.queryByRole("button", { name: "批准连接材料" }),
    ).not.toBeInTheDocument();
  },
);

it("rejects a stale-generation proposal response without approval, execution or restoring its link", async () => {
  const stale = productionOperation();
  stale.material.production_admission = {
    ...productionAdmission,
    generation: "b".repeat(32),
  };
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) =>
    response(init?.method ? { operation: stale } : productionHistory()),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={productionAccount}
      refreshAccount={async () => {}}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "创建真实连接材料" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent("invalid_receipt");
  expect(
    screen.queryByRole("button", { name: "批准连接材料" }),
  ).not.toBeInTheDocument();
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  expect(fetcher).toHaveBeenCalledTimes(2);
});

it("keeps arbitrary production receipt text out of the approval disclosure", async () => {
  const receipt = productionOperation();
  receipt.material.data_summary =
    "https://auth.openai.com/authorize?code=private_token_payload";
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response(productionHistory([receipt]))),
  );
  render(
    <ModelAcceptanceOperations
      account={productionAccount}
      refreshAccount={async () => {}}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: /连接 · 待审批 · 真实订阅连接/ }),
  );
  expect(screen.queryByText(/private_token_payload/)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "批准连接材料" })).toBeEnabled();
});

it.each(["older revision", "different operation"])(
  "rejects a production status response with %s",
  async (kind) => {
    const running = productionOperation("running", {
      revision: 3,
      attempts: 1,
      claim_sequence: 1,
    });
    const returned =
      kind === "older revision"
        ? {
            ...running,
            state: "approved",
            revision: 2,
            attempts: 0,
            claim_sequence: 0,
          }
        : { ...running, id: "other-operation" };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string) =>
        response(
          path.endsWith(running.id)
            ? { operation: returned }
            : productionHistory([running]),
        ),
      ),
    );
    render(
      <ModelAcceptanceOperations
        account={productionAccount}
        refreshAccount={async () => {}}
      />,
    );
    fireEvent.click(
      await screen.findByRole("button", {
        name: /连接 · 已消耗，执行中 · 真实订阅连接/,
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "读取连接状态" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "invalid_receipt",
    );
    expect(
      screen.queryByRole("button", { name: "执行连接" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  },
);

it("rejects another production operation returned by an ambiguous execution recovery read", async () => {
  const approved = productionOperation("approved", { revision: 2 });
  const other = {
    ...approved,
    id: "other-production-operation",
    state: "running",
    revision: 3,
    attempts: 1,
    claim_sequence: 1,
  };
  const requests: string[] = [];
  const opened = vi.spyOn(window, "open").mockImplementation(() => null);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      requests.push(`${init?.method ?? "GET"} ${path}`);
      if (path.endsWith("/execute")) throw new TypeError("lost response");
      if (path.endsWith(`/${approved.id}`))
        return response({
          operation: other,
          connection_session: handoffSession(other.id),
        });
      return response(productionHistory([approved]));
    }),
  );
  render(
    <ModelAcceptanceOperations
      account={productionAccount}
      refreshAccount={async () => {}}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: /连接 · 已批准，尚未执行 · 真实订阅连接/,
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "执行连接" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "仅读取原验收记录" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent("invalid_receipt");
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  expect(screen.queryByText(other.id)).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "执行连接" }),
  ).not.toBeInTheDocument();
  expect(requests.filter((request) => request.startsWith("POST"))).toEqual([
    `POST /api/model-accounts/${productionAccount.id}/acceptance-operations/${approved.id}/execute`,
  ]);
  expect(
    requests.filter((request) => request.endsWith(`/${approved.id}`)),
  ).toEqual([
    `GET /api/model-accounts/${productionAccount.id}/acceptance-operations/${approved.id}`,
  ]);
  expect(opened).not.toHaveBeenCalled();
});

it.each(["default restart", "new-generation restart"])(
  "reads the consumed original production receipt after %s without restoring authority",
  async (restart) => {
    const approved = productionOperation("approved", { revision: 2 });
    const recovered = {
      ...approved,
      revision: 4,
      state: "unknown",
      attempts: 1,
      claim_sequence: 1,
      certainty: "unknown",
      code: "transport_unknown",
    };
    let executeSent = false;
    const writes: string[] = [];
    const opened = vi.spyOn(window, "open").mockImplementation(() => null);
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string, init?: RequestInit) => {
        if (init?.method) {
          writes.push(path);
          executeSent = true;
          throw new TypeError("lost execute response before restart");
        }
        if (path.endsWith(`/${approved.id}`))
          return response({ operation: recovered });
        if (!executeSent) return response(productionHistory([approved]));
        return response(
          productionHistory(
            [recovered],
            restart === "default restart"
              ? {
                  routes: [],
                  production_connection_enabled: false,
                  production_admission: undefined,
                }
              : {
                  production_admission: {
                    ...productionAdmission,
                    generation: "b".repeat(32),
                  },
                },
          ),
        );
      }),
    );
    render(
      <ModelAcceptanceOperations
        account={productionAccount}
        refreshAccount={async () => {}}
      />,
    );
    fireEvent.click(
      await screen.findByRole("button", {
        name: /连接 · 已批准，尚未执行 · 真实订阅连接/,
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "执行连接" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "仅读取原验收记录" }),
    );
    expect(
      await screen.findByRole("button", {
        name: /连接 · 结果未知 · 真实订阅连接/,
      }),
    ).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "仅读取原验收记录" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "批准连接材料" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "执行连接" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "读取连接状态" })).toBeDisabled();
    expect(writes).toEqual([
      `/api/model-accounts/${productionAccount.id}/acceptance-operations/${approved.id}/execute`,
    ]);
    expect(opened).not.toHaveBeenCalled();
  },
);

it.each([
  "changed original material",
  "conflicting newer history material",
  "unowned session",
])("rejects restart reconciliation with %s", async (kind) => {
  const approved = productionOperation("approved", { revision: 2 });
  const recovered = {
    ...approved,
    revision: 4,
    state: kind === "unowned session" ? "running" : "unknown",
    attempts: 1,
    claim_sequence: 1,
    certainty: "unknown",
    code: "transport_unknown",
    material_digest:
      kind === "changed original material" ? "b".repeat(64) : digest,
  };
  const historical =
    kind === "conflicting newer history material"
      ? { ...recovered, revision: 5, material_digest: "b".repeat(64) }
      : recovered;
  let executeSent = false;
  const writes: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      if (init?.method) {
        writes.push(path);
        executeSent = true;
        throw new TypeError("lost response");
      }
      if (path.endsWith(`/${approved.id}`))
        return response({
          operation: recovered,
          ...(kind === "unowned session"
            ? { connection_session: handoffSession(recovered.id) }
            : {}),
        });
      return response(
        executeSent
          ? productionHistory([historical], {
              production_admission: {
                ...productionAdmission,
                generation: "b".repeat(32),
              },
            })
          : productionHistory([approved]),
      );
    }),
  );
  render(
    <ModelAcceptanceOperations
      account={productionAccount}
      refreshAccount={async () => {}}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: /连接 · 已批准，尚未执行 · 真实订阅连接/,
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "执行连接" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "仅读取原验收记录" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent("invalid_receipt");
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "执行连接" }),
  ).not.toBeInTheDocument();
  expect(writes).toHaveLength(1);
});

it("ignores a production execution response after switching accounts", async () => {
  const approved = productionOperation("approved");
  let resolve!: (value: Response) => void;
  const pending = new Promise<Response>((done) => {
    resolve = done;
  });
  const otherAccount = { ...productionAccount, id: "other-production-account" };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (init?.method) return pending;
    return response(
      path.includes(otherAccount.id)
        ? productionHistory([], {
            routes: [],
            production_admission: undefined,
            production_connection_enabled: false,
          })
        : productionHistory([approved]),
    );
  });
  vi.stubGlobal("fetch", fetcher);
  const view = render(
    <ModelAcceptanceOperations
      account={productionAccount}
      refreshAccount={async () => {}}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: /连接 · 已批准，尚未执行 · 真实订阅连接/,
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "执行连接" }));
  view.rerender(
    <ModelAcceptanceOperations
      account={otherAccount}
      refreshAccount={async () => {}}
    />,
  );
  await screen.findByText("生产真实访问保持关闭");
  await act(async () =>
    resolve(
      response({
        operation: {
          ...approved,
          state: "running",
          revision: 3,
          attempts: 1,
          claim_sequence: 1,
        },
        connection_session: handoffSession(approved.id),
      }),
    ),
  );
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  expect(screen.queryByText(approved.id)).not.toBeInTheDocument();
  expect(fetcher).toHaveBeenCalledTimes(3);
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

it("shows a recorded safe connection failure without adding retry or approval actions", async () => {
  const failed = handoffOperation("recorded-token-failure", "unknown", {
    revision: 4,
    attempts: 1,
    claim_sequence: 9,
    certainty: "unknown",
    code: "transport_unknown",
    connection_diagnostic: {
      version: 1,
      stage: "token_exchange",
      code: "auth_invalid",
      http_status: 403,
      provider_code: "invalid_grant",
      credential_commit: "not_committed",
      cleanup: "completed",
    },
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        operations: [failed],
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
    target: { value: failed.id },
  });
  const diagnostic = await screen.findByLabelText("连接失败诊断");
  expect(diagnostic).toHaveTextContent("令牌交换");
  expect(diagnostic).toHaveTextContent("当前尝试不会重试");
  expect(diagnostic).toHaveTextContent("未提交");
  expect(diagnostic).toHaveTextContent("已完成");
  expect(screen.queryByText(/清理与凭据提交结果不能确认/)).toBeNull();
  expect(screen.queryByRole("button", { name: /重试|重新连接/ })).toBeNull();
  expect(screen.queryByRole("button", { name: /批准连接材料/ })).toBeNull();
  expect(screen.queryByRole("button", { name: /执行连接/ })).toBeNull();
});

it("defers the production failure notice to recorded local identity evidence", async () => {
  const failed = productionOperation("unknown", {
    revision: 4,
    attempts: 1,
    claim_sequence: 9,
    certainty: "unknown",
    code: "transport_unknown",
    connection_diagnostic: {
      version: 1,
      stage: "identity_validation",
      code: "auth_invalid",
      identity_check: "audience",
      credential_commit: "not_committed",
      cleanup: "completed",
    },
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response(productionHistory([failed]))),
  );
  render(
    <ModelAcceptanceOperations
      account={productionAccount}
      refreshAccount={async () => {}}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: /连接 · 结果未知 · 真实订阅连接/,
    }),
  );
  expect(screen.getByLabelText("连接失败诊断")).toHaveTextContent("令牌受众");
  expect(screen.queryByText(/清理与凭据提交结果不能确认/)).toBeNull();
  expect(
    screen.getByText(/请核对已记录的本地凭据提交与清理证据/),
  ).toBeVisible();
  expect(
    screen.queryByRole("button", {
      name: /重试|重新连接|批准连接材料|执行连接/,
    }),
  ).toBeNull();
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

it.each([
  ["null envelope", () => null],
  ["empty envelope", () => ({})],
  ["null operation", () => ({ operation: null })],
  [
    "malformed nested material",
    (running: ReturnType<typeof handoffOperation>) => ({
      operation: { ...running, material: null },
    }),
  ],
  [
    "malformed catalog entry",
    (running: ReturnType<typeof handoffOperation>) => ({
      operation: { ...running, catalog: [null] },
    }),
  ],
  [
    "unsafe session",
    (running: ReturnType<typeof handoffOperation>) => ({
      operation: running,
      connection_session: {
        ...handoffSession(running.id),
        handoff_url: "http://127.0.0.1:43126/auth/start?secret=1",
      },
    }),
  ],
] as const)(
  "removes a previously safe link and disables every mutation for a %s status response",
  async (_name, malformed) => {
    const running = handoffOperation("status-turns-malformed", "running", {
      revision: 3,
      attempts: 1,
      claim_sequence: 6,
    });
    let detailReads = 0;
    const fetcher = vi.fn(async (path: string) => {
      if (path.endsWith(`/${running.id}`)) {
        detailReads++;
        return response(
          detailReads === 1
            ? {
                operation: running,
                connection_session: handoffSession(running.id),
              }
            : malformed(running),
        );
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
    for (const name of ["创建连接材料", "创建目录材料", "创建推理材料"])
      expect(screen.getByRole("button", { name })).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: /^(批准|拒绝|执行|取消)/ }),
    ).not.toBeInTheDocument();
  },
);

it("fails closed when explicit reconciliation receives a malformed operation envelope", async () => {
  const running = handoffOperation("reconcile-malformed", "running", {
    revision: 3,
    attempts: 1,
    claim_sequence: 6,
  });
  let detailReads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith("/cancel") && init?.method)
      throw new Error("synthetic lost cancellation response");
    if (path.endsWith(`/${running.id}`)) {
      detailReads++;
      return response(
        detailReads === 1
          ? {
              operation: running,
              connection_session: handoffSession(running.id),
            }
          : {},
      );
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
  await screen.findByRole("link", { name: "打开本地合成授权页" });
  fireEvent.click(screen.getByRole("button", { name: "取消连接会话" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "仅读取原验收记录" }),
  );

  expect(await screen.findByText("invalid_receipt")).toBeVisible();
  for (const name of ["创建连接材料", "创建目录材料", "创建推理材料"])
    expect(screen.getByRole("button", { name })).toBeDisabled();
  expect(
    screen.queryByRole("button", { name: "取消连接会话" }),
  ).not.toBeInTheDocument();
});

it.each(["waiting", "exchanging", "failed"] as const)(
  "fails closed when a linkless %s session claims more than the 600-second future lifetime",
  async (state) => {
    const running = handoffOperation(`future-${state}-session`, "running", {
      revision: 3,
      attempts: 1,
      claim_sequence: 7,
    });
    const fetcher = vi.fn(async (path: string) => {
      if (path.endsWith(`/${running.id}`))
        return response({
          operation: running,
          connection_session: {
            ...handoffSession(running.id),
            state,
            expires_at: new Date(Date.now() + 601 * 1000).toISOString(),
            handoff_url: undefined,
          },
        });
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

    expect(await screen.findByText("invalid_receipt")).toBeVisible();
    expect(screen.getByRole("button", { name: "创建连接材料" })).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: "取消连接会话" }),
    ).not.toBeInTheDocument();
  },
);

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
