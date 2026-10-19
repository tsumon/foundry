import {
  act,
  fireEvent,
  render,
  screen,
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
  const pending = operation("pending-1", "connection", "pending");
  let detailReads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (!init?.method) {
      if (path.endsWith("/pending-1")) {
        detailReads++;
        return response({ operation: pending });
      }
      return history([pending]);
    }
    throw new Error("synthetic network loss");
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={account}
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
    spend_class: "identity_only",
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
])(
  "fails closed for $name instead of treating it as a legacy route",
  async ({ routes, operations, raw }) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        response({
          operations,
          simulation_enabled: true,
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
