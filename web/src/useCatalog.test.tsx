import { act, renderHook, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { useCatalog } from "./useCatalog";
import type { CatalogSnapshot } from "./catalogTypes";
import { response } from "./test/fixtures";

const empty: CatalogSnapshot = {
  schema_version: 1,
  revision: 0,
  projects: [],
  targets: [],
  environments: [],
  resources: [],
  bindings: [],
  mission_links: [],
};
const command = {
  kind: "project.create",
  input: { name: "A", description: "" },
} as const;
const event = (revision: number, seq = revision * 7) => ({
  seq,
  catalog_revision: revision,
  kind: "project.create",
  entity_ids: ["p1"],
  created_at: "2026-10-18T00:00:00Z",
});
const deferred = () => {
  let resolve!: (value: Response) => void;
  let reject!: (e: Error) => void;
  const promise = new Promise<Response>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  // Keep the intentionally delayed rejection observed even in the RED scaffold.
  void promise.catch(() => {});
  return { promise, resolve, reject };
};

it("does not request before bootstrap and ignores disabled or unmounted responses", async () => {
  const pending = deferred();
  const fetcher = vi.fn(() => pending.promise);
  vi.stubGlobal("fetch", fetcher);
  const hook = renderHook(({ enabled }) => useCatalog(enabled), {
    initialProps: { enabled: false },
  });
  expect(fetcher).not.toHaveBeenCalled();
  hook.rerender({ enabled: true });
  hook.rerender({ enabled: false });
  await act(async () => pending.resolve(response({ catalog: empty })));
  expect(hook.result.current.snapshot).toBeNull();
  hook.unmount();
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("retains the catalog snapshot but marks it unread until a recovered session read completes", async () => {
  const recovered = deferred();
  let reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      ++reads === 1
        ? Promise.resolve(response({ catalog: empty }))
        : recovered.promise,
    ),
  );
  const hook = renderHook(({ enabled }) => useCatalog(enabled), {
    initialProps: { enabled: true },
  });
  await waitFor(() => expect(hook.result.current.ready).toBe(true));
  expect(hook.result.current.snapshot).toEqual(empty);
  hook.rerender({ enabled: false });
  expect(hook.result.current.snapshot).toEqual(empty);
  expect(hook.result.current.ready).toBe(false);
  hook.rerender({ enabled: true });
  expect(hook.result.current.ready).toBe(false);
  await act(async () => recovered.resolve(response({ catalog: empty })));
  await waitFor(() => expect(hook.result.current.ready).toBe(true));
  expect(reads).toBe(2);
});

it("does not let delayed history from an older snapshot replace newer history", async () => {
  const oldHistory = deferred();
  let snapshotReads = 0;
  let eventReads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) => {
      if (path === "/api/catalog")
        return Promise.resolve(
          response({ catalog: { ...empty, revision: ++snapshotReads } }),
        );
      return ++eventReads === 1
        ? oldHistory.promise
        : Promise.resolve(response({ events: [event(1), event(2), event(3)] }));
    }),
  );
  const { result } = renderHook(() => useCatalog(true));
  await waitFor(() => expect(result.current.snapshot?.revision).toBe(1));
  await act(async () => {
    await result.current.refresh();
  });
  expect(result.current.events.map((item) => item.catalog_revision)).toEqual([
    1, 2,
  ]);
  await act(async () => {
    oldHistory.resolve(response({ events: [event(1)] }));
  });
  expect(result.current.snapshot?.revision).toBe(2);
  expect(result.current.events.map((item) => item.catalog_revision)).toEqual([
    1, 2,
  ]);
});

it("preserves known catalog after read/save failures and accepts a no-op without fabricated events", async () => {
  let failure = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      failure
        ? Promise.reject(new Error("offline"))
        : Promise.resolve(response({ catalog: empty })),
    ),
  );
  const { result } = renderHook(() => useCatalog(true));
  await waitFor(() => expect(result.current.loading).toBe(false));
  await act(async () => {
    expect(await result.current.execute(command)).toBe(true);
  });
  expect(result.current.snapshot?.revision).toBe(0);
  expect(result.current.events).toEqual([]);
  failure = true;
  await act(async () => {
    expect(await result.current.execute(command)).toBe(false);
  });
  expect(result.current.error).toBe("offline");
  expect(result.current.snapshot).toEqual(empty);
  await act(async () => {
    await result.current.refresh();
  });
  expect(result.current.snapshot).toEqual(empty);
  expect(result.current.error).toBe("offline");
});

it("ignores pending success and failure after unmount without fetching history", async () => {
  const pending = deferred();
  const fetcher = vi.fn(() => pending.promise);
  vi.stubGlobal("fetch", fetcher);
  const hook = renderHook(() => useCatalog(true));
  hook.unmount();
  await act(async () => {
    pending.resolve(response({ catalog: { ...empty, revision: 4 } }));
  });
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(hook.result.current.snapshot).toBeNull();
});

it("pins paginated history to revision, uses seq cursors, deduplicates and keeps latest 100", async () => {
  const fetcher = vi.fn((path: string) =>
    Promise.resolve(
      response(
        path === "/api/catalog"
          ? { catalog: { ...empty, revision: 105 } }
          : {
              events: path.includes("after=0&")
                ? Array.from({ length: 100 }, (_, i) => event(i + 1))
                : [
                    event(100),
                    ...Array.from({ length: 6 }, (_, i) => event(i + 101)),
                  ],
            },
      ),
    ),
  );
  vi.stubGlobal("fetch", fetcher);
  const { result } = renderHook(() => useCatalog(true));
  await waitFor(() => expect(result.current.events).toHaveLength(100));
  expect(result.current.events[0].catalog_revision).toBe(6);
  expect(result.current.events.at(-1)?.catalog_revision).toBe(105);
  expect(fetcher.mock.calls.map((c) => c[0])).toContain(
    "/api/catalog/events?after=700&limit=100",
  );
  expect(result.current.error).toBe("");
});

it.each([
  { events: [] },
  { events: [event(2)] },
  { events: [event(1), event(3)] },
])(
  "reports incomplete history without hiding the snapshot: $events",
  async ({ events }) => {
    vi.stubGlobal(
      "fetch",
      vi.fn((path: string) =>
        Promise.resolve(
          response(
            path === "/api/catalog"
              ? { catalog: { ...empty, revision: 2 } }
              : { events },
          ),
        ),
      ),
    );
    const { result } = renderHook(() => useCatalog(true));
    await waitFor(() => expect(result.current.error).toMatch(/历史/));
    expect(result.current.snapshot?.revision).toBe(2);
    expect(result.current.events).toEqual([]);
  },
);

it("does not let older refresh or its history overwrite a newer command", async () => {
  const old = deferred();
  let reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) => {
      if (path === "/api/catalog")
        return ++reads === 1
          ? Promise.resolve(response({ catalog: empty }))
          : old.promise;
      return Promise.resolve(
        response(
          path === "/api/catalog/commands"
            ? { catalog: { ...empty, revision: 2 } }
            : { events: [event(1), event(2)] },
        ),
      );
    }),
  );
  const { result } = renderHook(() => useCatalog(true));
  await waitFor(() => expect(result.current.loading).toBe(false));
  let refresh!: Promise<void>;
  act(() => {
    refresh = result.current.refresh();
  });
  await act(async () => {
    expect(await result.current.execute(command)).toBe(true);
  });
  await act(async () => {
    old.resolve(response({ catalog: { ...empty, revision: 1 } }));
    await refresh;
  });
  expect(result.current.snapshot?.revision).toBe(2);
  expect(result.current.events.at(-1)?.catalog_revision).toBe(2);
});

it("ignores stale refresh errors and errors after unmount", async () => {
  const old = deferred();
  let reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      ++reads === 2
        ? old.promise
        : Promise.resolve(response({ catalog: empty })),
    ),
  );
  const hook = renderHook(() => useCatalog(true));
  await waitFor(() => expect(hook.result.current.loading).toBe(false));
  let first!: Promise<void>;
  act(() => {
    first = hook.result.current.refresh();
  });
  await act(async () => {
    await hook.result.current.refresh();
  });
  await act(async () => {
    old.reject(new Error("old failure"));
    await first;
  });
  expect(hook.result.current.error).toBe("");
  hook.unmount();
  expect(await hook.result.current.execute(command)).toBe(false);
});

it("locks same-tick duplicate saves, blocks conflict replay and only refresh unlocks", async () => {
  const pending = deferred();
  const writes: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string, init?: RequestInit) => {
      if (path === "/api/catalog/commands") {
        writes.push(JSON.parse(String(init?.body)));
        return pending.promise;
      }
      return Promise.resolve(response({ catalog: empty }));
    }),
  );
  const { result } = renderHook(() => useCatalog(true));
  await waitFor(() => expect(result.current.snapshot).not.toBeNull());
  let save!: Promise<boolean>;
  await act(async () => {
    save = result.current.execute(command);
    expect(await result.current.execute(command)).toBe(false);
  });
  await act(async () => {
    pending.resolve(
      response({ error: { code: "conflict", message: "版本冲突" } }, 409),
    );
    expect(await save).toBe(false);
  });
  expect(result.current.conflict).toBe(true);
  await act(async () => {
    expect(await result.current.execute(command)).toBe(false);
  });
  expect(writes).toEqual([{ expected_revision: 0, ...command }]);
  await act(async () => {
    await result.current.refresh();
  });
  expect(result.current.conflict).toBe(false);
  expect(writes).toHaveLength(1);
});
