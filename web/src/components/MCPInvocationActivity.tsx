import { useEffect, useRef, useState } from "react";
import { errorMessage } from "../api";
import { mcpTaskApi } from "../mcpTaskApi";
import type { MCPDecisionReceipt, MCPInvocationView, Mission } from "../types";

const callLabels: Record<string, string> = {
  authorized_pending: "已批准，等待派发",
  dispatching: "正在调用本地测试工具",
  succeeded: "调用成功并封存结果",
  failed: "调用失败",
  outcome_unknown: "调用结果未知",
  interrupted_before_dispatch: "派发前中断",
  interrupted: "调用被中断",
};
const terminalStatuses = new Set([
  "succeeded",
  "failed",
  "outcome_unknown",
  "interrupted_before_dispatch",
  "interrupted",
]);

export function MCPInvocationActivity({
  mission,
  visible,
  eventSequence = 0,
}: {
  mission: Mission;
  visible: boolean;
  eventSequence?: number;
}) {
  const [calls, setCalls] = useState<MCPInvocationView[]>([]);
  const [decisions, setDecisions] = useState<MCPDecisionReceipt[]>([]);
  const [read, setRead] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [details, setDetails] = useState<Record<string, MCPInvocationView>>({});
  const [detailError, setDetailError] = useState<Record<string, string>>({});
  const [detailLoading, setDetailLoading] = useState<Record<string, boolean>>(
    {},
  );
  const [detailVersions, setDetailVersions] = useState<Record<string, string>>(
    {},
  );
  const [historyVersion, setHistoryVersion] = useState(0);
  const request = useRef<AbortController | null>(null);
  const detailRequests = useRef<
    Record<string, { controller: AbortController; version: string }>
  >({});

  useEffect(() => {
    request.current?.abort();
    for (const active of Object.values(detailRequests.current))
      active.controller.abort();
    detailRequests.current = {};
    setCalls([]);
    setDecisions([]);
    setRead(false);
    setError("");
    setExpanded({});
    setDetails({});
    setDetailError({});
    setDetailLoading({});
    setDetailVersions({});
    setHistoryVersion(0);
    return () => {
      for (const active of Object.values(detailRequests.current))
        active.controller.abort();
      detailRequests.current = {};
    };
  }, [mission.id, visible]);

  useEffect(() => {
    if (!visible) return;
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setError("");
    void mcpTaskApi
      .history(mission.id, controller.signal)
      .then((result) => {
        if (request.current !== controller) return;
        setCalls(result.invocations);
        setDecisions(result.decisions);
        setRead(true);
        setHistoryVersion((current) => current + 1);
      })
      .catch((cause) => {
        if (!(cause instanceof DOMException && cause.name === "AbortError"))
          setError(
            `MCP 历史读取失败：${errorMessage(cause)}。保留上次数据；失败不表示没有记录。`,
          );
      })
      .finally(() => {
        if (request.current === controller) setLoading(false);
      });
    return () => controller.abort();
  }, [eventSequence, mission.id, mission.revision, visible]);

  useEffect(() => {
    const callIDs = new Set(calls.map((call) => call.id));
    for (const [id, active] of Object.entries(detailRequests.current)) {
      if (!expanded[id] || !callIDs.has(id)) {
        active.controller.abort();
        delete detailRequests.current[id];
        setDetailLoading((current) => ({ ...current, [id]: false }));
      }
    }
    for (const call of calls) {
      if (!expanded[call.id]) continue;
      const version = [
        historyVersion,
        call.status,
        call.phase,
        call.result_digest ?? "",
      ].join("\u0000");
      const active = detailRequests.current[call.id];
      if (active?.version === version || detailVersions[call.id] === version)
        continue;
      active?.controller.abort();
      const controller = new AbortController();
      detailRequests.current[call.id] = { controller, version };
      setDetailLoading((current) => ({ ...current, [call.id]: true }));
      setDetailError((current) => ({ ...current, [call.id]: "" }));
      void mcpTaskApi
        .invocation(mission.id, call.id, controller.signal)
        .then((result) => {
          if (detailRequests.current[call.id]?.controller !== controller)
            return;
          setDetails((current) => ({
            ...current,
            [call.id]: result.invocation,
          }));
          setDetailVersions((current) => ({
            ...current,
            [call.id]: version,
          }));
        })
        .catch((cause) => {
          if (cause instanceof DOMException && cause.name === "AbortError")
            return;
          if (detailRequests.current[call.id]?.controller !== controller)
            return;
          setDetailError((current) => ({
            ...current,
            [call.id]: `结果完整性读取失败：${errorMessage(cause)}`,
          }));
          setDetailVersions((current) => ({
            ...current,
            [call.id]: version,
          }));
        })
        .finally(() => {
          if (detailRequests.current[call.id]?.controller !== controller)
            return;
          delete detailRequests.current[call.id];
          setDetailLoading((current) => ({
            ...current,
            [call.id]: false,
          }));
        });
    }
  }, [calls, detailVersions, expanded, historyVersion, mission.id]);

  const toggle = (call: MCPInvocationView) => {
    if (expanded[call.id]) {
      setExpanded((current) => ({ ...current, [call.id]: false }));
      return;
    }
    // Failed versions suppress automatic render retries, not a user's retry.
    if (detailError[call.id]) {
      setDetailVersions((current) => {
        const next = { ...current };
        delete next[call.id];
        return next;
      });
    }
    setExpanded((current) => ({ ...current, [call.id]: true }));
  };

  return (
    <section
      className="mcp-invocation-activity"
      aria-label="任务内本地 MCP 历史"
    >
      <div className="invocation-heading">
        <div>
          <h3>任务内本地 MCP</h3>
          <p className="muted">
            决策与调用分别来自持久记录；合成本地测试数据不是验证证据。
          </p>
        </div>
      </div>
      {loading && <p role="status">正在读取 MCP 历史…</p>}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      <section className="mcp-history-group" aria-label="本地 MCP 决策历史">
        <h4>单次决策历史</h4>
        {read && !decisions.length && (
          <p className="muted">已读取，尚无 MCP 决策记录。</p>
        )}
        {decisions.map((decision) => (
          <article className="mcp-history-row" key={decision.request_id}>
            <strong>{decision.approved ? "已批准" : "已拒绝"}</strong>
            <span>
              任务 r{decision.expected_revision} · 请求{" "}
              <code>{decision.request_id}</code>
            </span>
            <span>
              提案 <code>{decision.proposal_id}</code> · SHA-256{" "}
              <code>{decision.proposal_digest}</code>
            </span>
            {decision.approved ? (
              <span>
                调用 <code>{decision.invocation_id}</code> · grant{" "}
                <code>{decision.grant_id}</code>
              </span>
            ) : (
              <span className="muted">拒绝记录没有调用或授权对象。</span>
            )}
          </article>
        ))}
      </section>
      <section className="mcp-history-group" aria-label="本地 MCP 调用历史">
        <h4>调用与封存结果</h4>
        {read && !calls.length && (
          <p className="muted">已读取，尚无 MCP 调用记录。</p>
        )}
        {calls.map((call) => (
          <article className="mcp-history-row" key={call.id}>
            <strong>
              {callLabels[call.status] ?? `未知状态：${call.status}`}
            </strong>
            <span>
              <code>{call.tool}</code> · phase {call.phase}
            </span>
            <pre className="immutable-json">
              {JSON.stringify(call.arguments, null, 2)}
            </pre>
            <span>
              请求 {call.requests} · 响应 {call.response_bytes} bytes ·{" "}
              {call.duration_millis} ms
            </span>
            {call.result_digest && (
              <span>
                结果 SHA-256 <code>{call.result_digest}</code>
              </span>
            )}
            {call.error_code && (
              <span>
                安全错误类别：<code>{call.error_code}</code>
              </span>
            )}
            <small>
              {call.created_at}
              {call.finished_at ? ` → ${call.finished_at}` : ""}
            </small>
            <button onClick={() => toggle(call)}>
              {expanded[call.id] ? "收起结果详情" : "查看结果详情"} {call.id}
            </button>
            {expanded[call.id] && (
              <div className="invocation-detail">
                {detailLoading[call.id] ? (
                  <p role="status">正在核对结果…</p>
                ) : detailError[call.id] ? (
                  <p className="form-error" role="alert">
                    {detailError[call.id]}
                  </p>
                ) : details[call.id]?.result !== undefined ? (
                  <pre>{JSON.stringify(details[call.id].result, null, 2)}</pre>
                ) : details[call.id] ? (
                  <p className="muted">
                    {terminalStatuses.has(details[call.id].status) ||
                    terminalStatuses.has(call.status)
                      ? "调用已结束，没有封存结果。"
                      : "调用尚未结束，结果尚未封存。"}
                  </p>
                ) : (
                  <p role="status">正在核对结果…</p>
                )}
              </div>
            )}
          </article>
        ))}
      </section>
    </section>
  );
}
