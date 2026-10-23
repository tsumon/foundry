import { useEffect, useRef, useState } from "react";
import { api, errorMessage } from "../api";
import { activeStates, type Mission } from "../types";
import {
  c4CleanupState,
  c4ControlBound,
  c4ControlActivationMatches,
  c4ControlSource,
  c4OperationLabel,
  c4ReadinessFresh,
  c4Supported,
  isModelBackedC4,
  isTerminalModelBackedDelivery,
  type C4Action,
  type C4Control,
  type C4Status,
} from "../c4";

// Display checks complement, never replace, serialized server authorization.
function controlReason(
  m: Mission,
  c: C4Control | undefined,
  at: number,
  current: C4Status["control"],
): string {
  if (!c) return "尚未准备材料";
  if (c.dispatch || c.result || !["prepared", "approved"].includes(c.state))
    return "材料已消费；不会重放";
  if (
    !(Date.parse(c.expires_at) > at) ||
    !(Date.parse(c.activation_expires_at) > at)
  )
    return "材料或激活已过期";
  if (
    !c4ControlBound(m, c) ||
    !c4ControlActivationMatches(c, current) ||
    c.epoch !== m.epoch ||
    c.plan_revision !== (m.plan?.revision ?? 0)
  )
    return "材料与当前任务不匹配";
  if (
    (c.approval &&
      (c.approval.digest !== c.material_digest ||
        c.approval.revision + 1 !== m.revision)) ||
    (!c.approval && c.prepared_revision + 1 !== m.revision)
  )
    return "材料修订已过期";
  if (
    c.kind === "cleanup" &&
    !m.c4_operations?.some(
      (o) =>
        o.id === c.operation_id &&
        o.material_digest === c.operation_digest &&
        o.container_id === c.container_id &&
        !!o.result &&
        c4CleanupState(o) !== "confirmed",
    )
  )
    return "清理目标不匹配或已清理";
  return "";
}
export function C4Controls({
  mission: m,
  busy,
  onUpdate,
  onBusyChange,
  onAvailabilityChange,
  onExecutionAvailabilityChange,
  inFlight,
}: {
  mission: Mission;
  busy: boolean;
  onUpdate: (m: Mission) => void;
  onBusyChange?: (busy: boolean) => void;
  onAvailabilityChange?: (available: boolean) => void;
  onExecutionAvailabilityChange?: (available: boolean) => void;
  inFlight?: { current: boolean };
}) {
  const [status, setStatus] = useState<C4Status | null>(null);
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [failed, setFailed] = useState<string[]>([]);
  const [at, setAt] = useState(Date.now());
  const localLock = useRef(false);
  const lock = inFlight ?? localLock;
  const callback = useRef(onAvailabilityChange);
  callback.current = onAvailabilityChange;
  const executionCallback = useRef(onExecutionAvailabilityChange);
  executionCallback.current = onExecutionAvailabilityChange;
  useEffect(() => {
    let disposed = false;
    setLoading(true);
    setStatus(null);
    callback.current?.(false);
    executionCallback.current?.(false);
    void api
      .c4(m.id)
      .then((next) => {
        if (!disposed) {
          setStatus(next);
          setAt(Date.now());
        }
      })
      .catch((e) => {
        if (!disposed) setError(errorMessage(e));
      })
      .finally(() => {
        if (!disposed) setLoading(false);
      });
    return () => {
      disposed = true;
    };
  }, [m.id, m.revision, refresh]);
  // One local expiry notification, not polling or a network refresh.
  useEffect(() => {
    const deadlines = [
      status?.control?.expires_at,
      ...(m.c4_controls ?? []).flatMap((c) => [
        c.expires_at,
        c.activation_expires_at,
        c.result?.valid_until,
      ]),
    ]
      .map((x) => Date.parse(x ?? ""))
      .filter((x) => x > Date.now());
    if (!deadlines.length) return;
    const timer = setTimeout(
      () => setAt(Date.now()),
      Math.max(1, Math.min(...deadlines) - Date.now() + 1),
    );
    return () => clearTimeout(timer);
  }, [m, status, at]);
  const fresh = status?.mission_id === m.id && status.revision === m.revision;
  const source = c4ControlSource(m);
  const structural = !!(
    fresh &&
    !status?.read_only &&
    status?.control?.structurally_available &&
    status.control.source_kind === source &&
    c4Supported(m)
  );
  const configured = !!(
    structural &&
    status?.control?.available &&
    !status.control.expired &&
    Date.parse(status.control.expires_at ?? "") > at &&
    status.control.source_kind === source &&
    status.real_execution === (source === "docker_c4_control") &&
    c4Supported(m)
  );
  const available = configured && c4ReadinessFresh(m, at, status?.control);
  const pendingBaseline = m.c4_operations?.some(
    (o) => o.kind === "baseline" && !o.dispatch && !o.result,
  );
  const readinessPreparationReason = pendingBaseline
    ? "已有未执行基线，不能更换就绪材料；若授权已过期或失配，请新建任务"
    : "";
  const terminalReason = isTerminalModelBackedDelivery(m)
    ? "已交付；如需继续执行请新建任务，历史证据保持不变"
    : "";
  useEffect(() => {
    callback.current?.(structural);
    return () => callback.current?.(false);
  }, [structural, m.id, m.revision]);
  useEffect(() => {
    executionCallback.current?.(available);
    return () => executionCallback.current?.(false);
  }, [available, m.id, m.revision]);
  const reason =
    busy || pending || activeStates.includes(m.state)
      ? "任务忙碌，操作已禁用"
      : loading
        ? "正在读取控制状态"
        : !fresh
          ? "版本不一致或事实不可用"
          : status?.read_only
            ? "只读或配置不可用"
            : status?.control?.expired ||
                !(Date.parse(status?.control?.expires_at ?? "") > at)
              ? "激活已过期或未配置"
              : !configured
                ? "配置或来源不匹配"
                : "";
  const key = (body: C4Action) =>
    `${m.id}:${body.action}:${"control_id" in body ? body.control_id : "operation_id" in body ? body.operation_id : m.revision}`;
  const stopped = (body: C4Action) => failed.includes(key(body));
  const act = async (body: C4Action) => {
    if (reason || lock.current || stopped(body)) return;
    if (
      terminalReason &&
      [
        "prepare_readiness",
        "approve_control",
        "execute_readiness",
        "prepare_cleanup",
        "execute_cleanup",
      ].includes(body.action)
    )
      return;
    if (body.action === "prepare_readiness" && pendingBaseline) return;
    if (
      "control_id" in body &&
      controlReason(
        m,
        m.c4_controls?.find((c) => c.id === body.control_id),
        Date.now(),
        status?.control,
      )
    )
      return;
    if (
      ["prepare", "approve", "execute"].includes(body.action) &&
      !c4ReadinessFresh(m, Date.now(), status?.control)
    )
      return;
    lock.current = true;
    setPending(true);
    onBusyChange?.(true);
    setError("");
    try {
      onUpdate((await api.c4Action(m.id, body)).mission);
    } catch (e) {
      setFailed((old) => [...old, key(body)]);
      setError(`${errorMessage(e)}。仅刷新持久事实；未自动重试、执行或恢复。`);
      try {
        onUpdate((await api.mission(m.id)).mission);
      } catch (e) {
        setError(`刷新失败：${errorMessage(e)}。结果未知；未自动重试。`);
      }
    } finally {
      lock.current = false;
      setPending(false);
      onBusyChange?.(false);
    }
  };
  const button = (label: string, body: C4Action, extra = "") => (
    <button
      disabled={!!reason || !!extra || stopped(body)}
      title={
        reason ||
        extra ||
        (stopped(body) ? "失败动作已停止；请核对持久事实" : "")
      }
      onClick={() => void act(body)}
    >
      {label}
    </button>
  );
  const latest = (kind: string, op?: string) =>
    (m.c4_controls ?? [])
      .filter((c) => c.kind === kind && (!op || c.operation_id === op))
      .at(-1);
  const controlButtons = (c: C4Control | undefined, cleanup = false) => {
    const why = controlReason(m, c, at, status?.control);
    const actionReason = terminalReason || why;
    const body = {
      revision: m.revision,
      control_id: c?.id ?? "",
      material_digest: c?.material_digest ?? "",
    };
    return (
      <>
        <p className="muted small">
          {terminalReason
            ? "历史材料保持只读"
            : why || (c?.approval ? "已批准，尚未执行" : "已准备，尚未批准")}
        </p>
        {button(
          cleanup ? "批准清理材料" : "批准就绪材料",
          { action: "approve_control", ...body },
          actionReason ||
            (c?.state !== "prepared" || !!c.approval ? "不能重复批准" : ""),
        )}
        {button(
          cleanup ? "执行精确清理" : "执行就绪检查",
          {
            action: cleanup ? "execute_cleanup" : "execute_readiness",
            ...body,
          },
          actionReason ||
            (c?.state !== "approved" || !c.approval ? "必须先明确批准" : ""),
        )}
      </>
    );
  };
  const readiness = latest("readiness");
  const baseline = m.c4_operations?.filter((o) => o.kind === "baseline").at(-1);
  const baselineReason = !available
    ? "需要新鲜且匹配的就绪结果"
    : baseline &&
        (baseline.readiness_id !== readiness?.id ||
          baseline.readiness_digest !== readiness?.material_digest)
      ? "基线绑定的就绪材料已改变"
      : "";
  return (
    <section className="review-section" aria-label="C4 受控决策">
      <h3>
        {source === "synthetic_c4_control"
          ? "模拟就绪检查 · 非真实 Docker"
          : "C4 就绪检查 · 受控 Docker 配置"}
      </h3>
      <p className="warning-copy">
        {source === "synthetic_c4_control"
          ? "本任务仅本地模拟，未执行真实 Docker；模拟不能证明 Docker、Go、镜像或隔离就绪。"
          : isModelBackedC4(m)
            ? "配置可用不代表就绪或执行成功；模型调用仍需独立上下文授权，Docker 执行仍需基线与执行审批。"
            : "配置可用不代表就绪或执行成功；模型推理仍为本地模拟。"}
      </p>
      <p>
        配置来源：{status?.control?.source_kind ?? "未配置"} · 激活到期：
        {status?.control?.expires_at ?? "未配置"}
      </p>
      <p className="muted small">
        {m.spec.c4?.id} · {m.spec.c4?.platform} · Go 1.27.3 ·{" "}
        <code>{m.spec.c4?.image_id}</code> · <code>{m.spec.c4?.verifier}</code>
      </p>
      <p className="note">
        准备、批准、执行分别点击；就绪不是基线或候选通过，不授权上下文、计划、候选执行或交付。不会自动探测、重试或清理。
      </p>
      {terminalReason && <p className="warning-copy">{terminalReason}</p>}
      {reason && <p className="warning-copy">{reason}</p>}
      {error && <p role="alert">{error}</p>}
      {readinessPreparationReason && (
        <p className="muted small">{readinessPreparationReason}</p>
      )}
      {button(
        "准备就绪材料",
        { action: "prepare_readiness", revision: m.revision },
        terminalReason ||
          readinessPreparationReason ||
          (readiness && !controlReason(m, readiness, at, status?.control)
            ? "已有待决材料"
            : ""),
      )}
      {controlButtons(readiness)}
      <h4>基线 · 独立审批</h4>
      {baselineReason && <p className="muted small">{baselineReason}</p>}
      {!baseline &&
        button(
          "准备基线材料",
          { action: "prepare", revision: m.revision },
          baselineReason,
        )}
      {baseline && (
        <>
          <p>{c4OperationLabel(m, baseline)}</p>
          <p>
            材料 <code>{baseline.material_digest}</code>
          </p>
          {baseline.state === "prepared" &&
            button(
              "批准基线材料",
              {
                action: "approve",
                revision: m.revision,
                operation_id: baseline.id,
                material_digest: baseline.material_digest,
              },
              baselineReason ||
                (Number(baseline.id.split("-")[0]) + 1 !== m.revision
                  ? "基线准备修订已过期"
                  : ""),
            )}
          {baseline.state === "approved" &&
            button(
              source === "synthetic_c4_control"
                ? "执行已批准基线（模拟）"
                : "执行已批准基线",
              {
                action: "execute",
                revision: m.revision,
                operation_id: baseline.id,
                material_digest: baseline.material_digest,
              },
              baselineReason ||
                (baseline.approval?.revision !== m.revision - 1
                  ? "基线批准修订已过期"
                  : ""),
            )}
        </>
      )}
      {(m.c4_operations ?? [])
        .filter((o) => o.result && c4CleanupState(o) !== "confirmed")
        .map((o) => (
          <div className="note warning-note" key={o.id}>
            <h4>恢复清理 · {o.id}</h4>
            <p>{c4OperationLabel(m, o)}。清理不改变测试结果，不重放执行。</p>
            <code>{o.container_id ?? "无精确 ID；不可发现或输入对象"}</code>
            {o.container_id &&
              button(
                `准备清理材料 · ${o.id}`,
                {
                  action: "prepare_cleanup",
                  revision: m.revision,
                  operation_id: o.id,
                  material_digest: o.material_digest,
                  container_id: o.container_id,
                },
                terminalReason ||
                  (latest("cleanup", o.id) &&
                  !controlReason(
                    m,
                    latest("cleanup", o.id),
                    at,
                    status?.control,
                  )
                    ? "已有待决清理材料"
                    : ""),
              )}
            {controlButtons(latest("cleanup", o.id), true)}
          </div>
        ))}
      <C4ControlFacts mission={m} />
      <button
        disabled={pending || loading}
        onClick={() => {
          setError("");
          setRefresh((x) => x + 1);
        }}
      >
        刷新 C4 事实
      </button>
    </section>
  );
}

export function C4ControlFacts({ mission: m }: { mission: Mission }) {
  return (
    <>
      {(m.c4_controls ?? []).map((c) => (
        <article className="note" key={c.id}>
          <h4>
            {c.kind === "cleanup" ? "清理" : "就绪"}材料 · {c.state}
          </h4>
          <p>
            <code>{c.id}</code> · {c.source_kind} ·{" "}
            {c.result?.status ?? "无执行结果"}
            {c.result?.failure_code ? ` · ${c.result.failure_code}` : ""}
          </p>
          <p>
            范围：任务 {c.mission_id} · epoch {c.epoch} · 计划 r
            {c.plan_revision} · 准备 r{c.prepared_revision}
          </p>
          <p>
            材料 <code>{c.material_digest}</code> · 基线{" "}
            <code>{c.base_commit}</code> / <code>{c.base_digest}</code>
          </p>
          <p>
            配置 <code>{c.profile_digest}</code> · 激活{" "}
            <code>{c.activation_digest}</code>
          </p>
          <p>
            材料到期：{c.expires_at} · 就绪有效至：
            {c.result?.valid_until ?? "无"}
          </p>
          {c.kind === "cleanup" && (
            <p>
              仅清理操作 {c.operation_id} · 原材料{" "}
              <code>{c.operation_digest}</code> · 精确 ID{" "}
              <code>{c.container_id}</code>
            </p>
          )}
        </article>
      ))}
    </>
  );
}
