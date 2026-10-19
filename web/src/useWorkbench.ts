import { useCallback, useEffect, useRef, useState } from "react";
import { api, errorMessage, initializeSession } from "./api";
import type { Mission, RunEvent } from "./types";

export function mergeMission(current: Mission[], incoming: Mission): Mission[] {
  const old = current.find((m) => m.id === incoming.id);
  if (old && old.revision >= incoming.revision) return current;
  return old
    ? current.map((m) => (m.id === incoming.id ? incoming : m))
    : [incoming, ...current];
}
export function useWorkbench() {
  const [missions, setMissions] = useState<Mission[]>([]);
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [stream, setStream] = useState("未连接");
  const [snapshotReceived, setSnapshotReceived] = useState(false);
  const [readReady, setReadReady] = useState(false);
  const [lastDataReceivedAt, setLastDataReceivedAt] = useState<number | null>(
    null,
  );
  const [attempt, setAttempt] = useState(0);
  const lastSequence = useRef(0);
  const update = useCallback((mission: Mission) => {
    setMissions((old) => mergeMission(old, mission));
    setLastDataReceivedAt(Date.now());
  }, []);
  useEffect(() => {
    let disposed = false;
    let source: EventSource | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let after = lastSequence.current;
    const connect = () => {
      if (disposed || typeof EventSource === "undefined") return;
      source = new EventSource(`/api/events?after=${after}`);
      source.onopen = () => {
        if (!disposed) setStream("事件流已连接");
      };
      source.addEventListener("change", (event) => {
        if (disposed) return;
        try {
          const item = JSON.parse((event as MessageEvent).data) as RunEvent;
          if (item.seq <= after) return;
          after = item.seq;
          lastSequence.current = item.seq;
          setEvents((old) => [...old.slice(-199), item]);
          void api
            .mission(item.mission_id)
            .then(({ mission }) => {
              if (!disposed) update(mission);
            })
            .catch((e) => {
              if (!disposed) setError(errorMessage(e));
            });
        } catch {
          setError("运行事件无法读取，请刷新任务");
        }
      });
      source.onerror = () => {
        source?.close();
        if (disposed) return;
        setStream("事件流中断 · 正在重连");
        timer = setTimeout(connect, 3000);
      };
    };
    setLoading(true);
    setError("");
    setStream("未连接");
    setReadReady(false);
    void initializeSession()
      .then(() => api.missions())
      .then((data) => {
        if (disposed) return;
        setMissions((old) => (data.missions ?? []).reduce(mergeMission, old));
        setSnapshotReceived(true);
        setReadReady(true);
        setLastDataReceivedAt(Date.now());
        setLoading(false);
        connect();
      })
      .catch((e) => {
        if (!disposed) {
          setError(errorMessage(e));
          setLoading(false);
        }
      });
    return () => {
      disposed = true;
      source?.close();
      clearTimeout(timer);
    };
  }, [attempt, update]);
  return {
    missions,
    events,
    loading,
    error,
    stream,
    snapshotReceived,
    readReady,
    lastDataReceivedAt,
    update,
    retry: () => {
      setStream("未连接");
      setReadReady(false);
      setAttempt((n) => n + 1);
    },
  };
}
