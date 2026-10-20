import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { catalogFixture } from "../test/catalogFixtures";
import { response } from "../test/fixtures";
import { ModelAcceptanceOperations } from "./ModelAcceptanceOperations";

function mount(
  f = catalogFixture(),
  operations: unknown[] = [f.connection],
  extra = {},
) {
  const fetcher = vi.fn(async () =>
    response({ ...f.history, operations, ...extra }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  return fetcher;
}
async function selectCatalog() {
  await screen.findByRole("option", { name: /^synthetic-catalog ·/ });
  fireEvent.change(
    await screen.findByRole("combobox", { name: "目录已保存材料" }),
    { target: { value: "synthetic-catalog" } },
  );
}

it("creates only independently approved catalog material with exact prerequisite, then separates approval and execution", async () => {
  const f = catalogFixture();
  const writes: { path: string; body: unknown }[] = [];
  let current: any = f.pending;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      if (!init?.method) return response(f.history);
      writes.push({ path, body: JSON.parse(String(init.body)) });
      if (path.endsWith("/decision"))
        current = { ...current, state: "approved", revision: 2 };
      if (path.endsWith("/execute"))
        current = {
          ...current,
          state: "succeeded",
          revision: 3,
          attempts: 1,
          claim_sequence: 2,
          certainty: "completed",
          catalog: [{ id: "gpt-5.1", display_name: "GPT 5.1" }],
        };
      return response({ operation: current });
    }),
  );
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  const create = await screen.findByRole("button", {
    name: "创建真实目录材料",
  });
  expect(create).toBeEnabled();
  expect(
    screen.queryByRole("button", { name: /创建.*连接|创建.*推理/ }),
  ).not.toBeInTheDocument();
  fireEvent.click(create);
  const approve = await screen.findByRole("button", { name: "批准目录材料" });
  expect(writes).toEqual([
    {
      path: `/api/model-accounts/${f.account.id}/acceptance-operations`,
      body: {
        phase: "catalog",
        route_id: f.route.id,
        expected_account_revision: 2,
        prerequisite_id: f.connection.id,
      },
    },
  ]);
  expect(
    screen.getByText("GET https://api.openai.com/v1/models"),
  ).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "执行目录" }),
  ).not.toBeInTheDocument();
  fireEvent.click(approve);
  const execute = await screen.findByRole("button", { name: "执行目录" });
  expect(writes).toHaveLength(2);
  expect(writes[1].body).toEqual({
    decision: "approve",
    expected_revision: 1,
    material_digest: "a".repeat(64),
  });
  fireEvent.click(execute);
  expect(await screen.findByText("GPT 5.1")).toBeVisible();
  expect(screen.getByText("gpt-5.1")).toBeVisible();
  expect(writes).toHaveLength(3);
  expect(writes[2].body).toEqual({
    expected_revision: 2,
    material_digest: "a".repeat(64),
  });
  expect(
    screen.queryByRole("combobox", { name: "推理模型" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /刷新|重试|登录|执行连接|取消连接/ }),
  ).not.toBeInTheDocument();
});

it("keeps the historical real connection read-only in catalog mode", async () => {
  const f = catalogFixture();
  mount(f);
  fireEvent.click(await screen.findByRole("button", { name: /连接 · 已成功/ }));
  const review = await screen.findByLabelText("连接材料审阅");
  expect(within(review).queryByRole("button")).not.toBeInTheDocument();
});

it("creates a fresh independent material after a failed catalog without replaying the old operation", async () => {
  const f = catalogFixture();
  const failed = {
    ...f.pending,
    state: "failed",
    attempts: 1,
    claim_sequence: 2,
    revision: 3,
    certainty: "rejected",
    code: "entitlement_denied",
  };
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) =>
    response(
      init?.method
        ? { operation: { ...f.pending, id: "synthetic-catalog-new" } }
        : { ...f.history, operations: [f.connection, failed] },
    ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "创建真实目录材料" }),
  );
  expect(
    await screen.findByRole("button", { name: "批准目录材料" }),
  ).toBeEnabled();
  expect(screen.getByLabelText("目录材料审阅")).toHaveTextContent(
    "synthetic-catalog-new",
  );
  expect(
    screen.getByRole("option", { name: /synthetic-catalog · 已失败/ }),
  ).toBeInTheDocument();
  expect(fetcher.mock.calls.filter(([, init]) => init?.method)).toHaveLength(1);
  expect(
    screen.queryByRole("button", { name: "执行目录" }),
  ).not.toBeInTheDocument();
});

it.each([
  "authorization_expired",
  "authorization_unavailable",
  "prerequisite_unavailable",
  "admission_unavailable",
])("shows bounded %s blocker without recovery writes", async (blocker) => {
  const fetcher = mount(undefined, undefined, {
    production_catalog_enabled: false,
    production_catalog_blocker: blocker,
  });
  expect(
    await screen.findByRole("button", { name: "创建真实目录材料" }),
  ).toBeDisabled();
  expect(screen.getByText(blocker)).toBeVisible();
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(
    screen.queryByRole("button", { name: /刷新|重试|登录|连接材料/ }),
  ).not.toBeInTheDocument();
});

it("does not let an older success override the latest failed connection", async () => {
  const f = catalogFixture();
  mount(f, [
    f.connection,
    {
      ...f.connection,
      id: "synthetic-failed",
      state: "failed",
      certainty: "rejected",
      claim_sequence: 3,
    },
  ]);
  expect(
    await screen.findByRole("button", { name: "创建真实目录材料" }),
  ).toBeDisabled();
});

it("locks stale generation approval while keeping historical material readable", async () => {
  const f = catalogFixture();
  f.pending.material.production_admission.generation = "d".repeat(32);
  mount(f, [f.connection, f.pending]);
  await selectCatalog();
  expect(screen.getByRole("button", { name: "批准目录材料" })).toBeDisabled();
});

it("expires approval locally without polling and cannot prepare against expired authorization", async () => {
  vi.useFakeTimers();
  try {
    const f = catalogFixture();
    f.pending.material.authorization.expires_at = new Date(
      Date.now() + 1000,
    ).toISOString();
    f.pending.material.expires_at = f.pending.material.authorization.expires_at;
    const fetcher = mount(f, [f.connection, f.pending]);
    await act(async () => {});
    fireEvent.change(screen.getByRole("combobox", { name: "目录已保存材料" }), {
      target: { value: "synthetic-catalog" },
    });
    expect(screen.getByRole("button", { name: "批准目录材料" })).toBeEnabled();
    await act(async () => {
      vi.advanceTimersByTime(1001);
    });
    expect(screen.getByRole("button", { name: "批准目录材料" })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "创建真实目录材料" }),
    ).toBeDisabled();
    expect(fetcher).toHaveBeenCalledTimes(1);
  } finally {
    vi.useRealTimers();
  }
});

it("shows an honest empty catalog without granting inference or selecting a model", async () => {
  const f = catalogFixture();
  mount(f, [
    f.connection,
    {
      ...f.pending,
      state: "succeeded",
      attempts: 1,
      claim_sequence: 2,
      certainty: "completed",
    },
  ]);
  await selectCatalog();
  expect(
    screen.getByText("目录为空：本次响应没有可展示的模型。"),
  ).toBeVisible();
  expect(
    screen.queryByRole("combobox", { name: "推理模型" }),
  ).not.toBeInTheDocument();
});

// Each mutation would falsely turn malformed display evidence into approval authority.
it.each([
  [
    "method",
    (o: any) => {
      o.material.request_method = "POST";
    },
  ],
  [
    "destination",
    (o: any) => {
      o.material.request_url += "?other=1";
    },
  ],
  [
    "model",
    (o: any) => {
      o.material.model = "gpt-5.1";
    },
  ],
  [
    "scope",
    (o: any) => {
      o.material.requested_scopes = [];
    },
  ],
  [
    "login",
    (o: any) => {
      o.material.login_timeout_seconds = 600;
    },
  ],
  [
    "secret",
    (o: any) => {
      o.material.authorization.secret_ref = "synthetic-secret";
    },
  ],
  [
    "auth generation",
    (o: any) => {
      o.material.authorization.generation = 0;
    },
  ],
  [
    "binding",
    (o: any) => {
      o.material.authorization.binding.account_id = "other";
    },
  ],
  [
    "duplicate scopes",
    (o: any) => {
      o.material.authorization.binding.scopes.push("resource.invoke");
    },
  ],
  [
    "control in binding",
    (o: any) => {
      o.material.authorization.binding.subject = "synthetic\nsubject";
    },
  ],
  [
    "claimed pending",
    (o: any) => {
      o.claim_sequence = 2;
    },
  ],
  [
    "expiry",
    (o: any) => {
      o.material.expires_at = new Date(Date.now() + 3600000).toISOString();
    },
  ],
  [
    "attempts",
    (o: any) => {
      o.material.max_attempts = 2;
    },
  ],
  [
    "unsafe name",
    (o: any) => {
      o.catalog = [{ id: "gpt-5.1", display_name: "<script>" }];
    },
  ],
  [
    "duplicate catalog",
    (o: any) => {
      o.catalog = [
        { id: "gpt-5.1", display_name: "one" },
        { id: "gpt-5.1", display_name: "two" },
      ];
    },
  ],
  [
    "oversize catalog",
    (o: any) => {
      o.catalog = Array.from({ length: 129 }, (_, i) => ({
        id: `m-${i}`,
        display_name: "Model",
      }));
    },
  ],
  [
    "mixed source",
    (o: any) => {
      o.source = "local_simulation";
      o.live_access = "blocked";
    },
  ],
] as const)("rejects catalog %s provenance", async (_name, change) => {
  const f = catalogFixture();
  change(f.pending);
  const fetcher = mount(f, [f.connection, f.pending]);
  expect(await screen.findByRole("alert")).toHaveTextContent("invalid_receipt");
  expect(
    screen.queryByRole("button", { name: "批准目录材料" }),
  ).not.toBeInTheDocument();
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("does not reinterpret production catalog evidence as local simulation authority", async () => {
  const f = catalogFixture();
  mount(f, [f.pending], {
    simulation_enabled: true,
    production_catalog_enabled: false,
    production_admission: undefined,
    routes: [{ ...f.route, environment: "simulation" }],
  });
  expect(await screen.findByRole("alert")).toHaveTextContent("invalid_receipt");
});

it("locks existing catalog approvals when the current account is disabled", async () => {
  const f = catalogFixture();
  const pending = structuredClone(f.pending);
  f.account.enabled = false;
  mount(f, [f.connection, pending]);
  await selectCatalog();
  expect(screen.getByRole("button", { name: "批准目录材料" })).toBeDisabled();
});

it.each([403, 409, 503])(
  "keeps denied %s catalog writes bounded without automatic retry",
  async (status) => {
    const f = catalogFixture();
    const code =
      status === 403
        ? "acceptance_simulation_disabled"
        : status === 409
          ? "acceptance_conflict"
          : "acceptance_record_unavailable";
    const fetcher = vi.fn(async (_path: string, init?: RequestInit) =>
      init?.method
        ? response(
            { error: { code, message: "UNTRUSTED provider body" } },
            status,
          )
        : response(f.history),
    );
    vi.stubGlobal("fetch", fetcher);
    render(
      <ModelAcceptanceOperations
        account={f.account}
        refreshAccount={async () => {}}
      />,
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "创建真实目录材料" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(code);
    if (status === 403)
      expect(screen.getByRole("alert")).toHaveTextContent("本账户或阶段未开放");
    expect(screen.queryByText(/UNTRUSTED/)).not.toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(2);
    if (status !== 403)
      expect(
        screen.getByRole("button", { name: "创建真实目录材料" }),
      ).toBeDisabled();
  },
);

it("locks unknown malformed mutation responses and refuses changed original-record identity", async () => {
  const f = catalogFixture();
  let mutating = false;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (init?.method) {
      mutating = true;
      return response({ operation: { ...f.pending, material: null } });
    }
    if (mutating && path.endsWith(`/${f.pending.id}`))
      return response({
        operation: { ...f.pending, id: "synthetic-other-operation" },
      });
    return response({ ...f.history, operations: [f.connection, f.pending] });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  await selectCatalog();
  fireEvent.click(screen.getByRole("button", { name: "批准目录材料" }));
  const reconcile = await screen.findByRole("button", {
    name: "仅读取原验收记录",
  });
  expect(screen.getByRole("alert")).toHaveTextContent("invalid_receipt");
  expect(
    screen.getByRole("heading", { name: "单次真实模型目录" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("button", { name: /创建.*推理/ }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("combobox", { name: "推理模型" }),
  ).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "创建真实目录材料" }),
  ).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "重新读取验收记录" }),
  ).toBeDisabled();
  fireEvent.click(reconcile);
  await waitFor(() => expect(reconcile).toBeEnabled());
  expect(screen.getByRole("alert")).toHaveTextContent("invalid_receipt");
  expect(fetcher.mock.calls.filter(([, init]) => init?.method)).toHaveLength(1);
  expect(
    screen.queryByRole("button", { name: "批准目录材料" }),
  ).not.toBeInTheDocument();
});

it("locks all writes after an uncertain execute and reconciles only the original record without replay", async () => {
  const f = catalogFixture();
  const approved = { ...f.pending, state: "approved" };
  let unknown = false;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (init?.method) {
      unknown = true;
      throw new TypeError("synthetic lost response");
    }
    const op = unknown
      ? {
          ...approved,
          state: "unknown",
          attempts: 1,
          claim_sequence: 2,
          certainty: "unknown",
          revision: 2,
        }
      : approved;
    return response(
      path.endsWith(`/${approved.id}`)
        ? { operation: op }
        : { ...f.history, operations: [f.connection, op] },
    );
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAcceptanceOperations
      account={f.account}
      refreshAccount={async () => {}}
    />,
  );
  await selectCatalog();
  fireEvent.click(screen.getByRole("button", { name: "执行目录" }));
  const reconcile = await screen.findByRole("button", {
    name: "仅读取原验收记录",
  });
  expect(
    screen.getByRole("button", { name: "创建真实目录材料" }),
  ).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "重新读取验收记录" }),
  ).toBeDisabled();
  fireEvent.click(reconcile);
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "仅读取原验收记录" }),
    ).not.toBeInTheDocument(),
  );
  expect(fetcher.mock.calls.filter(([, init]) => init?.method)).toHaveLength(1);
  expect(
    fetcher.mock.calls
      .map(([path]) => path)
      .filter((path) => path.endsWith(`/${approved.id}`)),
  ).toHaveLength(1);
  expect(
    screen.queryByRole("button", { name: "执行目录" }),
  ).not.toBeInTheDocument();
});
