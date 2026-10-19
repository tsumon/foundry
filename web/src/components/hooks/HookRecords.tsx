import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { hookApi } from "../../hookApi";
import type { HookRecord, HookRecordResponse } from "../../hookTypes";

const uniqueRecords = (records: HookRecord[]) =>
  records.filter(
    (record, index) =>
      records.findIndex((item) => item.id === record.id) === index,
  );

const recordReadError =
  "观察记录读取失败，请检查本地服务。保留上次数据；数据不可用不等于没有记录。";

export function HookRecords({
  missionID,
  enabled,
  visible,
  heading = "观察处理记录",
  onOpenMission,
  missionLabels = {},
}: {
  missionID: string;
  enabled: boolean;
  visible: boolean;
  heading?: string;
  onOpenMission?: (id: string) => void;
  missionLabels?: Record<string, string>;
}) {
  const [data, setData] = useState<HookRecordResponse | null>(null);
  const [pageBefore, setPageBefore] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [received, setReceived] = useState("");
  const lifecycle = useRef({
    active: false,
    epoch: 0,
    inFlight: false,
    controller: null as AbortController | null,
  });

  useLayoutEffect(() => {
    const current = lifecycle.current;
    current.controller?.abort();
    current.active = enabled && visible;
    current.epoch++;
    current.inFlight = false;
    setData(null);
    setPageBefore(0);
    setLoading(false);
    setError("");
    setReceived("");
    return () => {
      current.controller?.abort();
      current.active = false;
      current.epoch++;
      current.inFlight = false;
    };
  }, [enabled, missionID, visible]);

  const read = useCallback(
    async (before: number) => {
      const current = lifecycle.current;
      if (!current.active || current.inFlight) return;
      const epoch = current.epoch;
      const controller = new AbortController();
      current.controller = controller;
      current.inFlight = true;
      setLoading(true);
      setError("");
      const valid = () => current.active && current.epoch === epoch;
      try {
        const next = await hookApi.records(
          missionID,
          before,
          50,
          controller.signal,
        );
        if (valid()) {
          setData({ ...next, records: uniqueRecords(next.records ?? []) });
          setPageBefore(before);
          setReceived(new Date().toISOString());
        }
      } catch (cause) {
        if (
          valid() &&
          !(cause instanceof DOMException && cause.name === "AbortError")
        )
          setError(recordReadError);
      } finally {
        if (valid()) {
          current.inFlight = false;
          current.controller = null;
          setLoading(false);
        }
      }
    },
    [missionID],
  );

  useEffect(() => {
    if (enabled && visible) void read(0);
  }, [enabled, read, visible]);

  useEffect(() => {
    if (!enabled || !visible || !data || pageBefore !== 0) return;
    const timer = window.setInterval(() => void read(0), 2000);
    return () => window.clearInterval(timer);
  }, [data, enabled, pageBefore, read, visible]);

  const locked = !enabled || !visible || loading || lifecycle.current.inFlight;
  return (
    <section
      className="hook-records"
      aria-labelledby={`hook-records-${missionID || "all"}`}
    >
      <div className="hook-section-heading">
        <div>
          <h3 id={`hook-records-${missionID || "all"}`}>{heading}</h3>
          <p className="muted">
            持久记录来自已提交源事件；观察结果不改变任务或审批。
          </p>
        </div>
        <button
          disabled={locked || pageBefore !== 0}
          onClick={() => void read(0)}
        >
          刷新观察记录
        </button>
      </div>
      {loading && <p role="status">正在读取观察记录…</p>}
      {received && (
        <p className="muted hook-read-note">
          最近读取：{received}
          {error ? "（保留上次数据）" : ""}
        </p>
      )}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {!data && !loading && !error && (
        <p className="note">观察记录尚未读取，不能据此认定没有记录。</p>
      )}
      {data && (
        <>
          <div className="hook-runtime-summary" aria-label="观察处理状态">
            <span>运行状态：{data.runtime.state}</span>
            <span>初始水位：{data.processor.initialized_after_seq}</span>
            <span>当前水位：{data.processor.cursor}</span>
            <span>最新事件：{data.processor.latest_event_seq}</span>
            <span>待扫描事件：{data.processor.pending_events}</span>
            <span>最近处理：{data.processor.last_processed_at || "尚无"}</span>
            <span>最近尝试：{data.runtime.last_attempt_at || "尚无"}</span>
            <span>最近成功：{data.runtime.last_success_at || "尚无"}</span>
            {data.runtime.error_code && (
              <span>错误码：{data.runtime.error_code}</span>
            )}
          </div>
          {!data.records.length && (
            <p className="note">已完成读取，目前没有匹配的观察记录。</p>
          )}
          <div className="hook-record-list">
            {data.records.map((record) => (
              <article
                key={record.id}
                aria-label={`观察记录 ${record.id}`}
                className="hook-record"
              >
                <div className="hook-record-title">
                  <strong>{record.rule_name}</strong>
                  <span className="tag">
                    {record.status === "recorded" ? "已记录" : record.status}
                  </span>
                </div>
                <div className="hook-record-facts">
                  <span>记录 #{record.id}</span>
                  <span>源事件 #{record.event_seq}</span>
                  <span>{record.event_kind}</span>
                  <span>任务 r{record.mission_revision}</span>
                  <span>规则 r{record.rule_revision}</span>
                  <span>{record.handler_version}</span>
                </div>
                <p>
                  事件时间：<time>{record.event_created_at || "未记录"}</time> ·
                  处理时间：
                  <time>{record.processed_at || "未记录"}</time>
                </p>
                <p className="muted">只记录，无执行动作</p>
                {!missionID && onOpenMission && (
                  <button
                    className="text-button"
                    onClick={() => onOpenMission(record.mission_id)}
                  >
                    打开任务{" "}
                    {missionLabels[record.mission_id] ?? record.mission_id}
                  </button>
                )}
              </article>
            ))}
          </div>
          <div className="actions hook-record-actions">
            {pageBefore === 0 && data.next_before > 0 && (
              <button
                disabled={locked}
                onClick={() => void read(data.next_before)}
              >
                加载更早记录
              </button>
            )}
            {pageBefore > 0 && (
              <button disabled={locked} onClick={() => void read(0)}>
                返回最新
              </button>
            )}
            {pageBefore > 0 && data.next_before > 0 && (
              <button
                disabled={locked}
                onClick={() => void read(data.next_before)}
              >
                继续加载更早记录
              </button>
            )}
          </div>
        </>
      )}
    </section>
  );
}
