import { act, renderHook, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { useWorkbench } from "./useWorkbench";
import { mission, response } from "./test/fixtures";

class TestEventSource extends EventTarget {
  static instances: TestEventSource[] = [];
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    super();
    TestEventSource.instances.push(this);
  }
  close() {}
  emit(seq: number) {
    this.dispatchEvent(
      new MessageEvent("change", {
        data: JSON.stringify({
          seq,
          mission_id: "m1",
          kind: "plan.ready",
          created_at: "2026-10-18",
          revision: seq,
        }),
      }),
    );
  }
}
it("does not duplicate replayed activity after explicit session reconnect", async () => {
  TestEventSource.instances = [];
  vi.stubGlobal("EventSource", TestEventSource);
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) =>
      Promise.resolve(
        response(
          path === "/api/missions"
            ? { missions: [mission] }
            : path === "/api/missions/m1"
              ? { mission }
              : {},
        ),
      ),
    ),
  );
  const { result } = renderHook(useWorkbench);
  await waitFor(() => expect(TestEventSource.instances).toHaveLength(1));
  act(() => TestEventSource.instances[0].emit(1));
  await waitFor(() => expect(result.current.events).toHaveLength(1));
  act(() => result.current.retry());
  await waitFor(() => expect(TestEventSource.instances).toHaveLength(2));
  act(() => TestEventSource.instances[1].emit(1));
  await waitFor(() => expect(result.current.events).toHaveLength(1));
});

it("remembers a successful empty mission snapshot through a failed retry", async () => {
  let rejectRetry!: (error: Error) => void;
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(response({}))
    .mockResolvedValueOnce(response({ missions: [] }))
    .mockImplementationOnce(
      () =>
        new Promise<Response>((_resolve, reject) => {
          rejectRetry = reject;
        }),
    );
  vi.stubGlobal("fetch", fetcher);
  const { result } = renderHook(useWorkbench);
  await waitFor(() => expect(result.current.loading).toBe(false));
  expect(result.current.snapshotReceived).toBe(true);
  expect(result.current.readReady).toBe(true);
  act(() => result.current.retry());
  await waitFor(() => expect(result.current.loading).toBe(true));
  expect(result.current.snapshotReceived).toBe(true);
  expect(result.current.readReady).toBe(false);
  await act(async () => rejectRetry(new Error("retry offline")));
  await waitFor(() => expect(result.current.error).toContain("retry offline"));
  expect(result.current.snapshotReceived).toBe(true);
  expect(result.current.readReady).toBe(false);
});

it("does not mark an initial failed read as a received snapshot", async () => {
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
  const { result } = renderHook(useWorkbench);
  await waitFor(() => expect(result.current.loading).toBe(false));
  expect(result.current.snapshotReceived).toBe(false);
});

it("resets the event stream label as soon as an explicit reconnect begins", async () => {
  TestEventSource.instances = [];
  vi.stubGlobal("EventSource", TestEventSource);
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) =>
      Promise.resolve(
        response(path === "/api/missions" ? { missions: [mission] } : {}),
      ),
    ),
  );
  const { result } = renderHook(useWorkbench);
  await waitFor(() => expect(TestEventSource.instances).toHaveLength(1));
  act(() => TestEventSource.instances[0].onopen?.());
  expect(result.current.stream).toBe("事件流已连接");
  act(() => result.current.retry());
  expect(result.current.stream).toBe("未连接");
});

it("timestamps successful mission receipts but not retry clicks or failed detail refreshes", async () => {
  TestEventSource.instances = [];
  vi.stubGlobal("EventSource", TestEventSource);
  let rejectDetail!: (error: Error) => void;
  const fetcher = vi.fn((path: string) => {
    if (path === "/api/missions/m1")
      return new Promise<Response>((_resolve, reject) => {
        rejectDetail = reject;
      });
    return Promise.resolve(
      response(path === "/api/missions" ? { missions: [mission] } : {}),
    );
  });
  vi.stubGlobal("fetch", fetcher);
  const { result } = renderHook(useWorkbench);
  await waitFor(() => expect(result.current.snapshotReceived).toBe(true));
  const received = result.current.lastDataReceivedAt;
  expect(received).toEqual(expect.any(Number));
  act(() => result.current.retry());
  expect(result.current.lastDataReceivedAt).toBe(received);
  await waitFor(() => expect(TestEventSource.instances).toHaveLength(2));
  const retryReceipt = result.current.lastDataReceivedAt;
  expect(retryReceipt).toEqual(expect.any(Number));
  act(() => TestEventSource.instances[1].emit(1));
  await act(async () => rejectDetail(new Error("detail offline")));
  await waitFor(() => expect(result.current.error).toContain("detail offline"));
  expect(result.current.lastDataReceivedAt).toBe(retryReceipt);
});
