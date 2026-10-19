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
      ? response({ acceptance: report })
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
  await screen.findByText("synthetic-route");
  fireEvent.click(screen.getByRole("button", { name: "本地验收检查" }));
  expect(screen.queryByText("synthetic-route")).not.toBeInTheDocument();
  expect(await screen.findByRole("alert")).toHaveTextContent(/读取失败/);
  expect(document.body.textContent).not.toContain("secret provider details");
  expect(fetcher).toHaveBeenCalledTimes(2);
});

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
    await act(async () => finish(response({ acceptance: report })));
    expect(screen.queryByText("synthetic-route")).not.toBeInTheDocument();
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
