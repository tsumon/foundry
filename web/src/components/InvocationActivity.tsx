import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { api } from "../api";
import type {
  Invocation,
  InvocationDetail,
  Mission,
  ModelCallReceipt,
} from "../types";

const labels: Record<string, string> = {
  authorized_pending: "已授权，等待派发",
  dispatching: "正在派发",
  failed: "调用失败",
  outcome_unknown: "结果未知",
  interrupted_before_dispatch: "派发前中断",
};
const invocationLabel = (row: Invocation) =>
  row.status === "succeeded"
    ? row.kind === "model"
      ? "模型输出已封存"
      : "仓库观察已封存"
    : (labels[row.status] ?? `未知状态：${row.status}`);
const detailVersion = (row: Invocation) =>
  `${row.status}\u0000${row.result?.digest ?? ""}`;
const detailCanAdvance = (row: Invocation) =>
  !!row.result || !["authorized_pending", "dispatching"].includes(row.status);
const unique = (rows: Invocation[]) =>
  rows.filter(
    (row, index) => rows.findIndex((item) => item.id === row.id) === index,
  );
const certaintyLabels: Record<ModelCallReceipt["certainty"], string> = {
  completed: "厂商响应已完整返回（不代表角色输出有效或审批通过）",
  not_sent: "未发送",
  rejected: "已拒绝",
  unknown: "结果未知；预算已消耗，不会自动重试",
};

function ModelReceipt({ row }: { row: Invocation }) {
  const receipt = row.model_receipt;
  if (!receipt) return null;
  return (
    <div className="model-receipt" aria-label={`模型调用回执 #${row.ordinal}`}>
      {row.error_code === "quota_exhausted" && (
        <p className="warning-copy">
          <span>当前路由额度受限</span>
          ；这不代表整个套餐余额或重置时间，也不会触发付费通道切换。
        </p>
      )}
      <strong>{certaintyLabels[receipt.certainty]}</strong>
      <div className="receipt-facts">
        {receipt.request_id && (
          <span>
            request <code>{receipt.request_id}</code>
          </span>
        )}
        {receipt.resolved_model && (
          <span>
            resolved model <code>{receipt.resolved_model}</code>
          </span>
        )}
        {!!receipt.credential_generation && (
          <span>凭据代数 {receipt.credential_generation}</span>
        )}
        {!!receipt.http_status && <span>HTTP {receipt.http_status}</span>}
        {receipt.provider_code && (
          <span>
            provider code <code>{receipt.provider_code}</code>
          </span>
        )}
        {receipt.provider_param && (
          <span>
            provider param <code>{receipt.provider_param}</code>
          </span>
        )}
        {receipt.body_shape && (
          <span>
            body shape <code>{receipt.body_shape}</code>
          </span>
        )}
        {!!receipt.retry_after_seconds && (
          <span>provider retry-after {receipt.retry_after_seconds}s</span>
        )}
      </div>
    </div>
  );
}

export function InvocationActivity({
  mission,
  visible,
  eventSequence = 0,
}: {
  mission: Mission;
  visible: boolean;
  eventSequence?: number;
}) {
  const [rows, setRows] = useState<Invocation[]>([]);
  const [read, setRead] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [hasMore, setHasMore] = useState(false);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [details, setDetails] = useState<Record<string, InvocationDetail>>({});
  const [detailErrors, setDetailErrors] = useState<Record<string, string>>({});
  const [detailLoading, setDetailLoading] = useState<Record<string, boolean>>(
    {},
  );
  const lifecycle = useRef({
    epoch: 0,
    controller: null as AbortController | null,
    detailControllers: new Map<string, AbortController>(),
    detailRequestedVersions: new Map<string, string>(),
    active: false,
    inFlight: false,
    pendingNewest: false,
    read: false,
  });

  useLayoutEffect(() => {
    const current = lifecycle.current;
    current.controller?.abort();
    current.detailControllers.forEach((controller) => controller.abort());
    current.detailControllers.clear();
    current.detailRequestedVersions.clear();
    current.epoch++;
    current.active = visible;
    current.inFlight = false;
    current.pendingNewest = false;
    current.read = false;
    setRows([]);
    setRead(false);
    setLoading(false);
    setError("");
    setHasMore(false);
    setExpanded({});
    setDetails({});
    setDetailErrors({});
    setDetailLoading({});
    return () => {
      current.controller?.abort();
      current.detailControllers.forEach((controller) => controller.abort());
      current.detailControllers.clear();
      current.detailRequestedVersions.clear();
      current.active = false;
      current.epoch++;
      current.inFlight = false;
      current.pendingNewest = false;
      current.read = false;
    };
  }, [mission.id, visible]);

  const load = useCallback(
    async (before: number) => {
      const current = lifecycle.current;
      if (!current.active) return;
      if (current.inFlight) {
        if (before === 0) current.pendingNewest = true;
        return;
      }
      const epoch = current.epoch;
      const controller = new AbortController();
      current.controller = controller;
      current.inFlight = true;
      setLoading(true);
      setError("");
      try {
        const result = await api.invocations(
          mission.id,
          before,
          30,
          controller.signal,
        );
        if (!current.active || current.epoch !== epoch) return;
        const next = result.invocations ?? [];
        setRows((previous) =>
          unique(
            before === 0 ? [...next, ...previous] : [...previous, ...next],
          ).sort((a, b) => b.ordinal - a.ordinal),
        );
        setRead(true);
        if (!current.read || before !== 0) setHasMore(next.length === 30);
        current.read = true;
      } catch (cause) {
        if (
          current.active &&
          current.epoch === epoch &&
          !(cause instanceof DOMException && cause.name === "AbortError")
        )
          setError(
            "调用记录读取失败，请检查本地服务。保留上次数据；读取失败不等于没有记录。",
          );
      } finally {
        if (current.active && current.epoch === epoch) {
          const trailingNewest = current.pendingNewest;
          current.pendingNewest = false;
          current.inFlight = false;
          current.controller = null;
          setLoading(false);
          if (trailingNewest) void load(0);
        }
      }
    },
    [mission.id],
  );

  useEffect(() => {
    if (visible) void load(0);
  }, [eventSequence, load, mission.revision, visible]);

  const fetchDetail = useCallback(
    async (row: Invocation) => {
      if (
        !lifecycle.current.active ||
        lifecycle.current.detailControllers.has(row.id)
      )
        return;
      lifecycle.current.detailRequestedVersions.set(row.id, detailVersion(row));
      setDetailLoading((value) => ({ ...value, [row.id]: true }));
      setDetailErrors((value) => ({ ...value, [row.id]: "" }));
      const epoch = lifecycle.current.epoch;
      const controller = new AbortController();
      lifecycle.current.detailControllers.set(row.id, controller);
      try {
        const result = await api.invocation(
          mission.id,
          row.id,
          controller.signal,
        );
        if (lifecycle.current.active && lifecycle.current.epoch === epoch)
          setDetails((value) => ({ ...value, [row.id]: result.invocation }));
      } catch (cause) {
        if (
          lifecycle.current.active &&
          lifecycle.current.epoch === epoch &&
          !(cause instanceof DOMException && cause.name === "AbortError")
        )
          setDetailErrors((value) => ({
            ...value,
            [row.id]: "调用内容完整性读取失败；元数据保留，但正文不可用。",
          }));
      } finally {
        if (lifecycle.current.epoch === epoch) {
          lifecycle.current.detailControllers.delete(row.id);
          setDetailLoading((value) => ({ ...value, [row.id]: false }));
        }
      }
    },
    [mission.id],
  );

  useEffect(() => {
    for (const row of rows) {
      const cached = details[row.id];
      if (
        expanded[row.id] &&
        cached &&
        detailCanAdvance(row) &&
        lifecycle.current.detailRequestedVersions.get(row.id) !==
          detailVersion(row) &&
        detailVersion(cached.invocation) !== detailVersion(row)
      )
        void fetchDetail(row);
    }
  }, [details, expanded, fetchDetail, rows]);

  const toggleDetail = async (row: Invocation) => {
    if (expanded[row.id]) {
      setExpanded((value) => ({ ...value, [row.id]: false }));
      lifecycle.current.detailRequestedVersions.delete(row.id);
      return;
    }
    setExpanded((value) => ({ ...value, [row.id]: true }));
    if (
      details[row.id] &&
      detailVersion(details[row.id].invocation) === detailVersion(row)
    )
      return;
    await fetchDetail(row);
  };
  const oldest = rows.length ? Math.min(...rows.map((row) => row.ordinal)) : 0;
  return (
    <section className="invocation-activity" aria-label="模型与受控调用记录">
      <div className="invocation-heading">
        <div>
          <h3>模型与受控调用记录</h3>
          <p className="muted">
            普通模型调用与受控读取共享持久活动记录。模型输出和仓库观察都不是
            Docker 验证、审批或“通过”。
          </p>
        </div>
        <button disabled={loading} onClick={() => void load(0)}>
          刷新调用记录
        </button>
      </div>
      {loading && <p role="status">正在读取调用记录…</p>}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {!read && !loading && !error && (
        <p className="note">调用记录尚未读取。</p>
      )}
      {read && !rows.length && (
        <p className="note">已完成读取，目前没有调用记录。</p>
      )}
      <div className="invocation-list">
        {rows.map((row) => {
          const detail = details[row.id];
          return (
            <article className="invocation-row" key={row.id}>
              <div className="invocation-summary">
                <strong>{invocationLabel(row)}</strong>
                <code>
                  #{row.ordinal} · {row.kind} · {row.role} · {row.name}
                </code>
                <span>
                  epoch {row.epoch} · session <code>{row.session_id}</code>
                </span>
                <span>
                  grant <code>{row.grant_id || "无"}</code> · binding{" "}
                  <code>{row.binding_digest}</code> · authority{" "}
                  <code>{row.authority_digest}</code>
                </span>
                <span>
                  {row.usage_known
                    ? `${row.input_tokens} 输入 / ${row.output_tokens} 输出 tokens`
                    : "用量未知"}
                </span>
                {row.error_code && (
                  <span>
                    安全错误类别：<code>{row.error_code}</code>
                  </span>
                )}
                <ModelReceipt row={row} />
                <small>
                  输入 {row.input.bytes} bytes · SHA-256 {row.input.digest}
                  {row.input.truncated ? " · 已截断" : ""}
                </small>
                {row.result && (
                  <small>
                    结果 {row.result.bytes} bytes · SHA-256 {row.result.digest}
                    {row.result.truncated ? " · 已截断" : ""}
                  </small>
                )}
                <small>
                  {row.created_at}
                  {row.dispatched_at ? ` → ${row.dispatched_at}` : ""}
                  {row.finished_at ? ` → ${row.finished_at}` : ""}
                </small>
              </div>
              <button onClick={() => void toggleDetail(row)}>
                {expanded[row.id] ? "收起调用内容" : "查看调用内容"} #
                {row.ordinal}
              </button>
              {expanded[row.id] && (
                <div className="invocation-detail">
                  {detailLoading[row.id] && (
                    <p role="status">正在核对封存内容…</p>
                  )}
                  {detailErrors[row.id] && (
                    <p role="alert" className="form-error">
                      {detailErrors[row.id]}
                    </p>
                  )}
                  {detail && (
                    <>
                      {detail.input_redacted ? (
                        <p className="note">
                          模型输入已由安全任务视图有意隐藏；封存摘要与来源仍保留。这不是证据缺失或损坏。
                        </p>
                      ) : (
                        <>
                          <h4>
                            输入预览
                            {detail.input_truncated ? "（已截断）" : ""}
                          </h4>
                          <pre>{detail.input_preview}</pre>
                        </>
                      )}
                      {detail.result_redacted ? (
                        <p className="note">
                          模型输出已由安全任务视图有意隐藏；结果 envelope
                          的摘要、状态与来源仍可检查。
                        </p>
                      ) : detail.invocation.result ? (
                        <>
                          <h4>
                            结果预览
                            {detail.result_truncated ? "（已截断）" : ""}
                          </h4>
                          <pre>{detail.result_preview ?? ""}</pre>
                        </>
                      ) : (
                        <p className="muted">没有封存结果正文。</p>
                      )}
                    </>
                  )}
                </div>
              )}
            </article>
          );
        })}
      </div>
      {hasMore && (
        <button
          disabled={loading || oldest <= 0}
          onClick={() => void load(oldest)}
        >
          读取更早记录
        </button>
      )}
    </section>
  );
}
