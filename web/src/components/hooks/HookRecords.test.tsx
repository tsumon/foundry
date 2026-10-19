import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { response } from "../../test/fixtures";
import { HookRecords } from "./HookRecords";

const processor = {
  initialized_after_seq: 2,
  cursor: 9,
  latest_event_seq: 10,
  pending_events: 1,
  last_processed_at: "2026-10-18T02:03:04Z",
};
const runtime = {
  state: "degraded",
  last_attempt_at: "2026-10-18T02:03:05Z",
  last_success_at: "2026-10-18T02:03:04Z",
  error_code: "observation_storage_error",
};
const record = (id: number, missionID = "m/1") => ({
  id,
  event_seq: 30 + id,
  mission_id: missionID,
  mission_revision: 7,
  event_kind: "verification.completed",
  event_created_at: "2026-10-18T02:00:00Z",
  rule_id: "b".repeat(32),
  rule_revision: 4,
  rule_name: `观察规则 ${id}`,
  action: "record_activity",
  handler_version: "record_activity/v1",
  status: "recorded",
  processed_at: "2026-10-18T02:00:01Z",
});
const page = (records = [record(9)], next_before = 0) => ({
  records,
  next_before,
  processor,
  runtime,
});
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => (resolve = done));
  return { promise, resolve };
};

it("lazily reads the exact mission and renders immutable observation facts without success claims", async () => {
  const fetcher = vi.fn(async () => response(page()));
  vi.stubGlobal("fetch", fetcher);
  const view = render(<HookRecords missionID="m/1" enabled visible={false} />);
  expect(fetcher).not.toHaveBeenCalled();
  view.rerender(<HookRecords missionID="m/1" enabled visible />);
  expect(await screen.findByText("观察规则 9")).toBeVisible();
  expect(fetcher).toHaveBeenCalledWith(
    "/api/hooks/records?mission_id=m%2F1&limit=50",
    expect.objectContaining({ credentials: "same-origin" }),
  );
  const item = screen.getByRole("article", { name: "观察记录 9" });
  expect(item).toHaveTextContent("源事件 #39");
  expect(item).toHaveTextContent("verification.completed");
  expect(item).toHaveTextContent("任务 r7");
  expect(item).toHaveTextContent("规则 r4");
  expect(item).toHaveTextContent("record_activity/v1");
  expect(item).toHaveTextContent("已记录");
  expect(item).toHaveTextContent("只记录，无执行动作");
  expect(item).not.toHaveTextContent(/测试通过|执行成功/);
  expect(screen.getByText(/待扫描事件：1/)).toBeVisible();
  expect(screen.getByText(/运行状态：degraded/)).toBeVisible();
});

it("loads older pages manually without duplicate rows and can return to the latest page", async () => {
  const fetcher = vi.fn(async (path: string) =>
    response(
      path.includes("before=8")
        ? page([record(7), record(6)], 0)
        : page([record(9), record(8)], 8),
    ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<HookRecords missionID="m/1" enabled visible />);
  expect(await screen.findByText("观察规则 9")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "加载更早记录" }));
  expect(await screen.findByText("观察规则 7")).toBeVisible();
  expect(screen.queryByText("观察规则 9")).not.toBeInTheDocument();
  expect(screen.getAllByRole("article")).toHaveLength(2);
  expect(fetcher.mock.calls.at(-1)?.[0]).toBe(
    "/api/hooks/records?mission_id=m%2F1&before=8&limit=50",
  );
  fireEvent.click(screen.getByRole("button", { name: "返回最新" }));
  expect(await screen.findByText("观察规则 9")).toBeVisible();
  expect(screen.getAllByRole("article")).toHaveLength(2);
});

it("keeps labeled last-known records when a later local read fails", async () => {
  let reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      if (++reads === 1) return response(page());
      throw new Error("secret database pathname");
    }),
  );
  render(<HookRecords missionID="m/1" enabled visible />);
  expect(await screen.findByText("观察规则 9")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "刷新观察记录" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/保留上次数据/);
  expect(screen.getByText("观察规则 9")).toBeVisible();
  expect(document.body.textContent).not.toContain("secret database");
});

it("ignores a late response from the old mission after the mission filter changes", async () => {
  const oldRead = deferred<Response>();
  const fetcher = vi.fn(async (path: string) =>
    path.includes("mission_id=old")
      ? oldRead.promise
      : response(page([record(5, "new")], 0)),
  );
  vi.stubGlobal("fetch", fetcher);
  const view = render(<HookRecords missionID="old" enabled visible />);
  view.rerender(<HookRecords missionID="new" enabled visible />);
  expect(await screen.findByText("观察规则 5")).toBeVisible();
  await act(async () =>
    oldRead.resolve(response(page([record(99, "old")], 0))),
  );
  expect(screen.queryByText("观察规则 99")).not.toBeInTheDocument();
  expect(screen.getByText("观察规则 5")).toBeVisible();
});

it("polls only the visible latest page, never overlaps, and stops when hidden", async () => {
  vi.useFakeTimers();
  const second = deferred<Response>();
  let reads = 0;
  const fetcher = vi.fn(async () => {
    reads++;
    return reads === 2 ? second.promise : response(page([record(reads)], 0));
  });
  vi.stubGlobal("fetch", fetcher);
  const view = render(<HookRecords missionID="m1" enabled visible />);
  await act(async () => Promise.resolve());
  expect(fetcher).toHaveBeenCalledTimes(1);
  await act(async () => vi.advanceTimersByTimeAsync(2000));
  expect(fetcher).toHaveBeenCalledTimes(2);
  await act(async () => vi.advanceTimersByTimeAsync(4000));
  expect(fetcher).toHaveBeenCalledTimes(2);
  await act(async () => second.resolve(response(page([record(2)], 0))));
  view.rerender(<HookRecords missionID="m1" enabled visible={false} />);
  await act(async () => vi.advanceTimersByTimeAsync(4000));
  expect(fetcher).toHaveBeenCalledTimes(2);
  vi.useRealTimers();
});

it("does not poll an older page until the user explicitly returns to latest", async () => {
  vi.useFakeTimers();
  const fetcher = vi.fn(async (path: string) =>
    response(
      path.includes("before=8") ? page([record(7)], 0) : page([record(9)], 8),
    ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<HookRecords missionID="m1" enabled visible />);
  await act(async () => Promise.resolve());
  fireEvent.click(screen.getByRole("button", { name: "加载更早记录" }));
  await act(async () => Promise.resolve());
  expect(screen.getByText("观察规则 7")).toBeVisible();
  const count = fetcher.mock.calls.length;
  await act(async () => vi.advanceTimersByTimeAsync(6000));
  expect(fetcher).toHaveBeenCalledTimes(count);
  vi.useRealTimers();
});
