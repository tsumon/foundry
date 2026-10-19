import { useCallback, useEffect, useRef, useState } from "react";
import { APIError, errorMessage } from "./api";
import { catalogApi } from "./catalogApi";
import type {
  CatalogCommand,
  CatalogEvent,
  CatalogSnapshot,
} from "./catalogTypes";

export function useCatalog(enabled: boolean) {
  const [snapshot, setSnapshot] = useState<CatalogSnapshot | null>(null);
  const [events, setEvents] = useState<CatalogEvent[]>([]);
  const [loading, setLoading] = useState(false);
  const [ready, setReady] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);
  const state = useRef({
    active: false,
    epoch: 0,
    operation: 0,
    history: 0,
    refresh: 0,
    saving: false,
    conflict: false,
    snapshot: null as CatalogSnapshot | null,
  });
  const alive = (epoch: number) =>
    state.current.active && state.current.epoch === epoch;

  // Each history traversal belongs to one accepted snapshot, never to a live head.
  const accept = useCallback(
    async (incoming: CatalogSnapshot, epoch: number, operation: number) => {
      const current = state.current;
      if (
        !alive(epoch) ||
        incoming.revision < (current.snapshot?.revision ?? -1)
      )
        return false;
      current.snapshot = incoming;
      setSnapshot(incoming);
      setEvents([]);
      const history = ++current.history;
      const valid = () => alive(epoch) && history === state.current.history;
      if (incoming.revision === 0) return true;
      let after = 0;
      let revision = 0;
      const seen = new Set<number>();
      let tail: CatalogEvent[] = [];
      try {
        while (revision < incoming.revision) {
          const page = await catalogApi.events(after, 100);
          if (!valid()) return true;
          const previous = after;
          for (const item of page.events) {
            if (seen.has(item.seq)) continue;
            if (item.seq <= after || item.catalog_revision !== revision + 1)
              throw new Error("台账历史不完整，请重新加载");
            if (item.catalog_revision > incoming.revision) break;
            seen.add(item.seq);
            after = item.seq;
            revision = item.catalog_revision;
            tail = [...tail.slice(-99), item];
            if (revision === incoming.revision) break;
          }
          if (after === previous) throw new Error("台账历史不完整，请重新加载");
        }
        if (valid()) setEvents(tail);
      } catch (e) {
        if (valid() && operation === state.current.operation)
          setError(`台账历史读取失败：${errorMessage(e)}`);
      }
      return true;
    },
    [],
  );

  const refresh = useCallback(async () => {
    const current = state.current;
    if (!current.active) return;
    const epoch = current.epoch;
    const operation = ++current.operation;
    const request = ++current.refresh;
    setLoading(true);
    setReady(false);
    setError("");
    try {
      const { catalog } = await catalogApi.snapshot();
      if (!alive(epoch)) return;
      if (
        catalog.revision >= (current.snapshot?.revision ?? -1) &&
        operation === current.operation
      ) {
        current.conflict = false;
        setConflict(false);
      }
      if (
        (await accept(catalog, epoch, operation)) &&
        alive(epoch) &&
        operation === state.current.operation
      )
        setReady(true);
    } catch (e) {
      if (alive(epoch) && operation === current.operation)
        setError(errorMessage(e));
    } finally {
      if (alive(epoch) && request === current.refresh) setLoading(false);
    }
  }, [accept]);

  const execute = useCallback(
    async (command: CatalogCommand) => {
      const current = state.current;
      if (
        !current.active ||
        !current.snapshot ||
        current.saving ||
        current.conflict
      )
        return false;
      const epoch = current.epoch;
      const operation = ++current.operation;
      current.saving = true;
      setSaving(true);
      setError("");
      try {
        const { catalog } = await catalogApi.command(
          current.snapshot.revision,
          command,
        );
        if (!alive(epoch)) return false;
        await accept(catalog, epoch, operation);
        return alive(epoch);
      } catch (e) {
        if (alive(epoch) && operation === current.operation) {
          setError(errorMessage(e));
          if (e instanceof APIError && e.status === 409) {
            current.conflict = true;
            setConflict(true);
          }
        }
        return false;
      } finally {
        if (alive(epoch)) {
          current.saving = false;
          setSaving(false);
        }
      }
    },
    [accept],
  );

  useEffect(() => {
    const current = state.current;
    current.active = enabled;
    current.epoch++;
    current.saving = false;
    setSaving(false);
    setLoading(false);
    if (!enabled) setReady(false);
    if (enabled) void refresh();
    return () => {
      current.active = false;
      current.epoch++;
    };
  }, [enabled, refresh]);
  return {
    snapshot,
    ready: enabled && ready,
    events,
    loading,
    saving,
    error,
    conflict,
    refresh,
    execute,
  };
}

export type CatalogController = ReturnType<typeof useCatalog>;
