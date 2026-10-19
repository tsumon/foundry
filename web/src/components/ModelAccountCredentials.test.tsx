import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ModelAccounts } from "./ModelAccounts";
import { ModelAccountCredentials } from "./ModelAccountCredentials";
import { response } from "../test/fixtures";
import type { ModelAccount, ModelCredential } from "../types";

const account: ModelAccount = {
  id: "lifecycle-account",
  revision: 1,
  name: "Synthetic lifecycle",
  provider: "custom",
  base_url: "https://example.invalid",
  auth_method: "api_key",
  billing_mode: "unknown",
  enabled: true,
  current_credential_version: 0,
  created_at: "now",
  updated_at: "now",
  identity_status: "unverified",
  usage_status: "unknown",
  inference_enabled: false,
};
const credential: ModelCredential = {
  account_id: account.id,
  version: 1,
  operation_id: "durable-original",
  state: "available",
  cleanup_pending: false,
  created_at: "now",
  updated_at: "now",
};

it("reconstructs pending operation identity from durable history after a fresh mount and uses GET only", async () => {
  const fetcher = vi.fn(async (_path: string, _init?: RequestInit) =>
    response({ operation: { ...credential, state: "interrupted" } }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAccountCredentials
      account={account}
      credentials={[{ ...credential, state: "pending" }]}
      refresh={async () => {}}
    />,
  );
  expect(screen.getByRole("button", { name: "保存新版本" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "仅查询原操作结果" }));
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  expect(fetcher.mock.calls[0][0]).toBe(
    "/api/model-accounts/lifecycle-account/operations/durable-original",
  );
  expect(fetcher.mock.calls[0][1]?.method).toBeUndefined();
});

it("clears success and cancellation inputs then uses refreshed CAS revision for rename/disable and exact revoke", async () => {
  let live = { ...account };
  let versions: ModelCredential[] = [];
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    const q = init?.body ? JSON.parse(String(init.body)) : null;
    if (path === "/api/model-accounts") return response({ accounts: [live] });
    if (init?.method === "PUT") {
      live = {
        ...live,
        name: q.name,
        enabled: q.enabled,
        revision: live.revision + 1,
      };
      return response({ account: live });
    }
    if (path.endsWith("/revoke")) {
      versions = [{ ...versions[0], state: "revoked", cleanup_pending: true }];
      live = { ...live, revision: live.revision + 1 };
      return response({ operation: versions[0] });
    }
    if (init?.method === "POST") {
      versions = [{ ...credential, operation_id: q.operation_id }];
      live = { ...live, revision: 2, current_credential_version: 1 };
      return response({ operation: versions[0] });
    }
    return response({ account: live, credentials: versions });
  });
  vi.stubGlobal("fetch", fetcher);
  render(<ModelAccounts onAccounts={() => {}} />);
  fireEvent.click(
    await screen.findByRole("button", { name: "管理账户 Synthetic lifecycle" }),
  );
  const key = await screen.findByLabelText("凭据");
  fireEvent.change(key, { target: { value: "discard-this" } });
  fireEvent.click(screen.getByRole("button", { name: "取消凭据输入" }));
  expect(key).toHaveValue("");
  fireEvent.change(key, { target: { value: "synthetic-only" } });
  fireEvent.click(screen.getByRole("button", { name: "保存新版本" }));
  await screen.findByRole("button", {
    name: "撤销 Synthetic lifecycle 版本 1",
  });
  expect(key).toHaveValue("");
  fireEvent.change(screen.getByLabelText("账户新名称"), {
    target: { value: "Renamed synthetic" },
  });
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.click(screen.getByRole("button", { name: "保存账户变更" }));
  await screen.findByRole("button", { name: "管理账户 Renamed synthetic" });
  const update = fetcher.mock.calls.find(([, init]) => init?.method === "PUT");
  expect(JSON.parse(String(update?.[1]?.body))).toEqual({
    name: "Renamed synthetic",
    enabled: false,
    expected_revision: 2,
  });
  fireEvent.click(
    screen.getByRole("button", { name: "撤销 Renamed synthetic 版本 1" }),
  );
  await screen.findByText("清理待处理；不可使用");
  const revoke = fetcher.mock.calls.find(([path]) => path.endsWith("/revoke"));
  expect(JSON.parse(String(revoke?.[1]?.body))).toEqual({
    expected_revision: 3,
  });
  expect(
    screen.getByRole("button", { name: "撤销 Renamed synthetic 版本 1" }),
  ).toBeDisabled();
});

it("refreshes a credential conflict without replay and never displays raw server messages or the key", async () => {
  const refresh = vi.fn(async () => {});
  const fetcher = vi.fn(async () =>
    response(
      { error: { code: "conflict", message: "raw SYNTHETIC-SECRET" } },
      409,
    ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAccountCredentials
      account={{ ...account, id: "conflict-account" }}
      credentials={[]}
      refresh={refresh}
    />,
  );
  fireEvent.change(screen.getByLabelText("凭据"), {
    target: { value: "SYNTHETIC-SECRET" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存新版本" }));
  await waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
  expect(screen.getByLabelText("凭据")).toHaveValue("");
  expect(document.body.textContent).not.toContain("SYNTHETIC-SECRET");
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("does not let a late credential refresh switch the currently selected account or resurrect its editor", async () => {
  const other = { ...account, id: "other-account", name: "Other synthetic" };
  let resolveRefresh!: (value: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    resolveRefresh = resolve;
  });
  let reads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/model-accounts")
      return response({ accounts: [account, other] });
    if (path.endsWith("/credentials") && init?.method === "POST")
      return response({ operation: credential });
    if (path.endsWith("/other-account"))
      return response({ account: other, credentials: [] });
    if (++reads === 2) return pending;
    return response({ account, credentials: [] });
  });
  vi.stubGlobal("fetch", fetcher);
  render(<ModelAccounts onAccounts={() => {}} />);
  fireEvent.click(
    await screen.findByRole("button", { name: "管理账户 Synthetic lifecycle" }),
  );
  fireEvent.change(await screen.findByLabelText("凭据"), {
    target: { value: "synthetic-late" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存新版本" }));
  await waitFor(() => expect(reads).toBe(2));
  fireEvent.click(
    screen.getByRole("button", { name: "管理账户 Other synthetic" }),
  );
  await screen.findByRole("heading", { name: "管理 Other synthetic" });
  await act(async () =>
    resolveRefresh(
      response({
        account: { ...account, revision: 2 },
        credentials: [credential],
      }),
    ),
  );
  expect(
    screen.getByRole("heading", { name: "管理 Other synthetic" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("heading", { name: "管理 Synthetic lifecycle" }),
  ).not.toBeInTheDocument();
});

it("an uncertain revoke only queries its original version operation and stays locked on an available receipt", async () => {
  const a = {
    ...account,
    id: "uncertain-revoke",
    revision: 2,
    current_credential_version: 1,
  };
  const v = { ...credential, account_id: a.id };
  let reads = 0;
  const fetcher = vi.fn(async (_path: string, init?: RequestInit) => {
    if (init?.method === "POST") throw new Error("untrusted raw failure");
    return response({
      operation: { ...v, state: ++reads === 1 ? "available" : "revoked" },
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <ModelAccountCredentials
      account={a}
      credentials={[v]}
      refresh={async () => {}}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "撤销 Synthetic lifecycle 版本 1" }),
  );
  const reconcile = await screen.findByRole("button", {
    name: "仅查询原操作结果",
  });
  await waitFor(() => expect(reconcile).toBeEnabled());
  fireEvent.click(reconcile);
  await waitFor(() => expect(reads).toBe(1));
  expect(
    screen.getByRole("button", { name: "撤销 Synthetic lifecycle 版本 1" }),
  ).toBeDisabled();
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "仅查询原操作结果" }),
    ).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "仅查询原操作结果" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "仅查询原操作结果" }),
    ).not.toBeInTheDocument(),
  );
  expect(
    fetcher.mock.calls.filter(([, i]) => i?.method === "POST"),
  ).toHaveLength(1);
  expect(
    fetcher.mock.calls
      .slice(1)
      .every(([p]) => p.endsWith("/operations/durable-original")),
  ).toBe(true);
});
