import { useCallback, useEffect, useRef, useState } from "react";
import { api, APIError, errorMessage } from "../api";
import { missionStateName } from "../deliveryStatus";
import { mcpTaskApi } from "../mcpTaskApi";
import type { MCPDecisionView, Mission } from "../types";

const makeActionID = () =>
  typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
    ? crypto.randomUUID()
    : `intent_${Date.now()}_${Math.random().toString(36).slice(2)}`;

const reasonLabels: Record<string, string> = {
  stale_snapshot: "任务快照已变化，请刷新后重新检查",
  local_test_disabled: "本地测试服务已停用，历史提案仅供检查",
  authority_unavailable: "当前调用权限不可用",
  engine_busy: "控制器正在处理其他工作",
  not_planning: "任务已不在调研阶段",
  no_pending_action: "当前没有可执行的调研动作",
  history_unavailable: "调研历史完整性暂时不可用",
};

const jsonText = (value: unknown) => JSON.stringify(value, null, 2);

export interface MCPUnresolvedDecision {
  proposal_id: string;
  proposal_digest: string;
  expected_revision: number;
  decision: "approve" | "reject";
  request_id: string;
}

export function MCPDecisionCard({
  mission,
  busy,
  inFlight,
  unresolved = null,
  onUnresolvedChange = () => {},
  onBusyChange,
  onUpdate,
  eventSequence = 0,
}: {
  mission: Mission;
  busy: boolean;
  inFlight: { current: boolean };
  unresolved?: MCPUnresolvedDecision | null;
  onUnresolvedChange?: (intent: MCPUnresolvedDecision | null) => void;
  onBusyChange: (busy: boolean) => void;
  onUpdate: (mission: Mission) => void;
  eventSequence?: number;
}) {
  const [decision, setDecision] = useState<MCPDecisionView | null>(null);
  const [read, setRead] = useState(false);
  const [loading, setLoading] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const identity = useRef("");
  const actionID = useRef(makeActionID());
  const continueIdentity = useRef("");
  const continueID = useRef(makeActionID());
  const decisionRequest = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    decisionRequest.current?.abort();
    const controller = new AbortController();
    decisionRequest.current = controller;
    setLoading(true);
    setError("");
    try {
      const result = await mcpTaskApi.decision(mission.id, controller.signal);
      const next = result.decision;
      const nextIdentity = next
        ? `${next.proposal_id}\u0000${next.proposal_digest}\u0000${next.revision}`
        : "";
      if (decisionRequest.current === controller) {
        if (nextIdentity !== identity.current) {
          identity.current = nextIdentity;
          actionID.current = makeActionID();
          setAcknowledged(false);
          setNotice("");
        }
        setDecision(next);
        setRead(true);
      }
    } catch (cause) {
      if (!(cause instanceof DOMException && cause.name === "AbortError"))
        setError(
          `工具审批读取失败：${errorMessage(cause)}。读取失败不表示没有待审批提案。`,
        );
    } finally {
      if (decisionRequest.current === controller) setLoading(false);
    }
  }, [mission.id]);

  useEffect(() => {
    void load();
    return () => decisionRequest.current?.abort();
  }, [eventSequence, load, mission.revision]);

  const refreshMission = async () => {
    const refreshed = (await api.mission(mission.id)).mission;
    onUpdate(refreshed);
    return refreshed;
  };

  const reconcile = async (requestID: string) => {
    const { decision: receipt } = await mcpTaskApi.receipt(
      mission.id,
      requestID,
    );
    onUnresolvedChange(null);
    setDecision(null);
    setRead(false);
    setNotice(
      receipt.approved
        ? "已按原请求编号确认审批记录；不会重新派发调用。"
        : "已按原请求编号确认拒绝记录；没有派发调用。",
    );
    try {
      await refreshMission();
    } catch (cause) {
      setError(
        `原请求记录已确认，但任务刷新失败：${errorMessage(cause)}。请重新连接后检查。`,
      );
    }
  };

  const decide = async (answer: "approve" | "reject") => {
    if (!decision || busy || inFlight.current || !decision.can_decide) return;
    const requestID = actionID.current;
    const intent: MCPUnresolvedDecision = {
      proposal_id: decision.proposal_id,
      proposal_digest: decision.proposal_digest,
      expected_revision: decision.revision,
      decision: answer,
      request_id: requestID,
    };
    inFlight.current = true;
    onBusyChange(true);
    onUnresolvedChange(intent);
    setError("");
    setNotice("");
    try {
      const result = await mcpTaskApi.decide(mission.id, decision.proposal_id, {
        expected_revision: decision.revision,
        proposal_digest: decision.proposal_digest,
        decision: answer,
        request_id: requestID,
      });
      onUnresolvedChange(null);
      onUpdate(result.mission);
      setNotice(
        answer === "approve"
          ? "审批已记录；控制器将按冻结参数派发一次调用。"
          : "拒绝已记录；本次提案不会派发调用。",
      );
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 409) {
        onUnresolvedChange(null);
        setAcknowledged(false);
        try {
          await refreshMission();
          await load();
          setError(
            "任务版本或当前权限已变化。已请求刷新；请重新检查，本次操作未自动重送。",
          );
        } catch (refreshError) {
          setError(
            `冲突后刷新失败：${errorMessage(refreshError)}。请重新连接后检查。`,
          );
        }
      } else if (cause instanceof APIError) {
        onUnresolvedChange(null);
        setError(errorMessage(cause));
      } else {
        try {
          await reconcile(requestID);
        } catch {
          setError(
            "本次决策响应未确认。不会生成新请求编号或重新提交；请使用原请求编号查询结果。",
          );
        }
      }
    } finally {
      inFlight.current = false;
      onBusyChange(false);
    }
  };

  const reconcileUnresolved = async () => {
    if (!unresolved || busy || inFlight.current) return;
    inFlight.current = true;
    onBusyChange(true);
    setError("");
    setNotice("");
    try {
      await reconcile(unresolved.request_id);
    } catch {
      setError(
        "原请求结果查询仍未确认。系统只进行了 GET 查询，没有重新提交决策；请稍后继续查询。",
      );
    } finally {
      inFlight.current = false;
      onBusyChange(false);
    }
  };

  const continueResearch = async () => {
    if (busy || inFlight.current || !mission.research_status?.can_continue)
      return;
    const key = `${mission.id}\u0000${mission.revision}\u0000${mission.stage}`;
    if (continueIdentity.current !== key) {
      continueIdentity.current = key;
      continueID.current = makeActionID();
    }
    inFlight.current = true;
    onBusyChange(true);
    setError("");
    try {
      const result = await mcpTaskApi.continueResearch(
        mission.id,
        mission.revision,
        continueID.current,
      );
      onUpdate(result.mission);
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 409) {
        setError("继续条件已变化。已请求刷新；系统没有自动重送继续请求。");
        try {
          await refreshMission();
        } catch (refreshError) {
          setError(`刷新失败：${errorMessage(refreshError)}`);
        }
      } else {
        setError(
          "继续请求结果未知；系统不会自动重送。刷新任务后仍会保留本次意图编号。",
        );
      }
    } finally {
      inFlight.current = false;
      onBusyChange(false);
    }
  };

  const unavailable = decision
    ? (decision.why_not ?? "")
    : (mission.research_status?.why_not ?? "");
  if (
    !decision &&
    read &&
    !mission.research_status?.can_continue &&
    !unresolved
  ) {
    if (["mcp_authorized_pending", "mcp_call"].includes(mission.stage))
      return (
        <section className="mcp-decision-card" aria-label="本地 MCP 调用状态">
          <h2>{missionStateName(mission)}</h2>
          <p className="muted small">
            调用状态来自控制器记录；刷新或事件重放不会再次派发。
          </p>
        </section>
      );
    return null;
  }

  return (
    <section className="mcp-decision-card" aria-label="本次 MCP 调用审批">
      <div className="section-heading">
        <div>
          <p className="eyebrow">单次工具审批</p>
          <h2>
            {unresolved
              ? "核对未确认的决策结果"
              : decision
                ? "检查本次本地测试工具调用"
                : "继续调研"}
          </h2>
        </div>
        <span className="tag">合成本地测试数据</span>
      </div>
      {loading && <p role="status">正在核对工具提案…</p>}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {notice && <p className="note">{notice}</p>}
      {unresolved && (
        <div className="note warning-note">
          <p>
            原请求 <code>{unresolved.request_id}</code> 的
            {unresolved.decision === "approve" ? "批准" : "拒绝"}
            结果尚未确认。新决策保持锁定，查询不会重新提交 POST。
          </p>
          <button
            disabled={busy || inFlight.current}
            onClick={() => void reconcileUnresolved()}
          >
            仅查询原请求结果
          </button>
        </div>
      )}
      {decision && (
        <>
          <p>{decision.purpose}</p>
          <dl className="decision-facts mcp-decision-facts">
            <dt>测试端点</dt>
            <dd>
              <code>{decision.endpoint}</code>
            </dd>
            <dt>工具</dt>
            <dd>
              <code>{decision.tool}</code>
            </dd>
            <dt>精确参数</dt>
            <dd>
              <pre className="immutable-json">
                {jsonText(decision.arguments)}
              </pre>
            </dd>
            <dt>共享预算</dt>
            <dd>
              已用 {decision.used_calls} · 剩余 {decision.remaining_calls} ·
              上限 {decision.budget.max_calls}
            </dd>
          </dl>
          <p className="muted small">
            参数不可编辑。服务只返回合成参考数据；结果不是 Docker
            验证、计划批准或交付许可。
          </p>
          <details className="disclosure">
            <summary>技术细节与派生简报接收方</summary>
            <p>
              提案 <code>{decision.proposal_id}</code> · SHA-256{" "}
              <code>{decision.proposal_digest}</code>
            </p>
            <p>
              来源调用 <code>{decision.source_invocation_id}</code> · SHA-256{" "}
              <code>{decision.source_digest}</code>
            </p>
            <h3>固定策略</h3>
            <pre className="immutable-json">{jsonText(decision.policy)}</pre>
            <h3>接收方</h3>
            <ul>
              {decision.recipients.map((recipient) => (
                <li key={recipient.role}>
                  {recipient.role} · {recipient.name} · {recipient.protocol} ·{" "}
                  {recipient.model}
                </li>
              ))}
            </ul>
            <p className="muted small">
              Researcher
              可接收受限观察正文；其余角色仅可能接收经验证的派生简报，不是原始结果。
            </p>
          </details>
          {unavailable && (
            <p className="warning-copy">
              {reasonLabels[unavailable] ?? `当前不可操作：${unavailable}`}
            </p>
          )}
          {unavailable === "engine_busy" && (
            <button
              disabled={busy || inFlight.current || loading}
              onClick={() => void load()}
            >
              刷新当前可用状态
            </button>
          )}
          <label className="check-label">
            <input
              type="checkbox"
              checked={acknowledged}
              disabled={
                !!unresolved ||
                !decision.current ||
                !decision.can_decide ||
                busy
              }
              onChange={(event) => setAcknowledged(event.target.checked)}
            />
            我已确认本次参数、固定策略、预算与接收方；只批准这一调用
          </label>
          <div className="actions mcp-decision-actions">
            <button
              className="primary"
              disabled={
                busy ||
                inFlight.current ||
                !!unresolved ||
                !acknowledged ||
                !decision.current ||
                !decision.can_decide
              }
              onClick={() => void decide("approve")}
            >
              批准本次调用并继续调研
            </button>
            <button
              disabled={
                busy ||
                inFlight.current ||
                !!unresolved ||
                !decision.current ||
                !decision.can_decide
              }
              onClick={() => void decide("reject")}
            >
              拒绝本次调用
            </button>
          </div>
        </>
      )}
      {!decision && mission.research_status?.can_continue && (
        <>
          <p>
            控制器已核对封存观察或 Planner
            检查点。继续只消费当前可恢复检查点，不会再次调用 MCP。
          </p>
          <button
            className="primary"
            disabled={busy || inFlight.current}
            onClick={() => void continueResearch()}
          >
            继续调研
          </button>
        </>
      )}
    </section>
  );
}
