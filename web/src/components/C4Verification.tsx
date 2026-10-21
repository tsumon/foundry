import { Fragment, useEffect, useRef, useState } from "react";
import { api, errorMessage } from "../api";
import { activeStates, type Mission } from "../types";
import {
  c4CleanupState,
  c4OperationLabel,
  c4Supported,
  hasC4,
  type C4Action,
  type C4Operation,
  type C4Status,
} from "../c4";

function C4ProfileFacts({ mission }: { mission: Mission }) {
  const p = mission.spec.c4;
  return (
    <>
      <p className="warning-copy">
        真实 C4 入口关闭 · C4-C 未执行。模拟通过不证明
        Docker、Go、镜像、隔离或资源就绪。
      </p>
      <p className="muted small">
        {c4Supported(mission)
          ? "模型判断与验证来源均为本地模拟；source_kind=synthetic_stub。"
          : "配置不支持；来源未确认，不能推断成功。"}
        以下为冻结声明，不是主机探测结果。
      </p>
      <dl className="metadata">
        <dt>配置 / 版本 / 模式</dt>
        <dd>
          {p?.id ?? "未记录"} / {p?.schema_version ?? "未记录"} /{" "}
          {p?.mode ?? "未记录"}
        </dd>
        <dt>镜像 / 平台</dt>
        <dd>
          <code>{p?.image_id ?? "未记录"}</code> · {p?.platform ?? "未记录"}
        </dd>
        <dt>验证器</dt>
        <dd>
          <code>{p?.verifier ?? "未记录"}</code>
        </dd>
        <dt>Docker / endpoint 声明</dt>
        <dd>
          <code>{p?.docker_path ?? "未记录"}</code> ·{" "}
          <code>{p?.endpoint ?? "未记录"}</code>
        </dd>
        {c4Supported(mission) && (
          <>
            <dt>固定资源权限</dt>
            <dd>
              1 CPU · 1 GiB · 128 pids · 120 秒 · 合并日志 64 KiB · UID/GID
              65534
            </dd>
            <dt>可写 scratch 权限</dt>
            <dd>
              /tmp：64 MiB noexec；/foundry-build：256 MiB exec；均
              nosuid,nodev，16384 inodes
            </dd>
            <dt>只读与隔离声明</dt>
            <dd>
              根与候选只读；无网络、端口、拉取或
              capabilities；no-new-privileges；禁用 healthcheck / logging
            </dd>
            <dt>固定 Go 合约（未执行）</dt>
            <dd>
              Go 1.27.3 · <code>go test -json -count=1 -timeout=90s ./...</code>{" "}
              ·
              GOPROXY/GOSUMDB/GOENV/GOWORK=off，GOTOOLCHAIN=local，CGO_ENABLED=0；缓存仅
              scratch
            </dd>
          </>
        )}
      </dl>
    </>
  );
}

export function C4Decision({
  mission,
  busy,
  onUpdate,
  onBusyChange,
  onAvailabilityChange,
  inFlight,
}: {
  mission: Mission;
  busy: boolean;
  onUpdate: (m: Mission) => void;
  onBusyChange?: (busy: boolean) => void;
  onAvailabilityChange?: (available: boolean) => void;
  inFlight?: { current: boolean };
}) {
  const [status, setStatus] = useState<C4Status | null>(null);
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [refreshKey, setRefreshKey] = useState(0);
  const [failedActions, setFailedActions] = useState<string[]>([]);
  const ownFlight = useRef(false);
  const lock = inFlight ?? ownFlight;
  const statusCallback = useRef(onAvailabilityChange);
  statusCallback.current = onAvailabilityChange;
  useEffect(() => {
    let disposed = false;
    setLoading(true);
    statusCallback.current?.(false);
    void api
      .c4(mission.id)
      .then((next) => {
        if (disposed) return;
        setStatus(next);
        statusCallback.current?.(
          next.mission_id === mission.id &&
            next.revision === mission.revision &&
            next.simulation === true &&
            next.read_only === false &&
            next.real_execution === false,
        );
      })
      .catch((cause) => {
        if (!disposed) {
          setStatus(null);
          setError(`C4 读取失败：${errorMessage(cause)}`);
        }
      })
      .finally(() => {
        if (!disposed) setLoading(false);
      });
    return () => {
      disposed = true;
    };
  }, [mission.id, mission.revision, refreshKey]);
  const fresh =
    status?.mission_id === mission.id && status.revision === mission.revision;
  const disabled =
    busy ||
    pending ||
    loading ||
    !fresh ||
    !c4Supported(mission) ||
    status?.simulation !== true ||
    status?.read_only !== false ||
    status?.real_execution !== false ||
    activeStates.includes(mission.state);
  const operations = mission.c4_operations ?? [];
  const baseline = operations.filter((o) => o.kind === "baseline").at(-1);
  const canPrepare =
    !baseline && ["draft", "blocked", "paused"].includes(mission.state);
  const staleApproval =
    baseline?.state === "approved" &&
    !!baseline.approval &&
    mission.revision !== baseline.approval.revision + 1;
  const actionKey = (action: C4Action["action"], operationID = "") =>
    `${mission.id}:${action}:${operationID}${action === "cleanup" ? `:${mission.revision}` : ""}`;
  const actionFailed = (action: C4Action["action"], operationID = "") =>
    failedActions.includes(actionKey(action, operationID));
  const act = async (body: C4Action) => {
    const key = actionKey(
      body.action,
      "operation_id" in body ? body.operation_id : "",
    );
    if (
      disabled ||
      lock.current ||
      failedActions.includes(key) ||
      (body.action === "execute" && staleApproval)
    )
      return;
    lock.current = true;
    setPending(true);
    onBusyChange?.(true);
    setError("");
    try {
      onUpdate((await api.c4Action(mission.id, body)).mission);
    } catch (cause) {
      setFailedActions((current) => [...current, key]);
      setError(
        `${errorMessage(cause)}。仅刷新持久事实；未自动重试、重新执行或恢复。`,
      );
      try {
        onUpdate((await api.mission(mission.id)).mission);
      } catch (refreshError) {
        setError(
          `刷新失败：${errorMessage(refreshError)}。结果仍未知；未自动重试，请刷新 C4 事实。`,
        );
      }
    } finally {
      lock.current = false;
      setPending(false);
      onBusyChange?.(false);
      setRefreshKey((k) => k + 1);
    }
  };
  const material = (o: C4Operation) => ({
    revision: mission.revision,
    operation_id: o.id,
    material_digest: o.material_digest,
  });
  return (
    <section className="review-section" aria-label="C4 基线决策">
      <h3>C4 基线 · 独立审批</h3>
      <C4ProfileFacts mission={mission} />
      {loading && <p role="status">正在读取 C4 事实；不会自动执行。</p>}
      {!loading && !fresh && (
        <p className="warning-copy">
          版本不一致或事实不可用；操作已禁用，请刷新。
        </p>
      )}
      {!loading && fresh && (status?.read_only || !status?.simulation) && (
        <p className="warning-copy">此运行时仅供读取；没有本地模拟执行能力。</p>
      )}
      {!c4Supported(mission) && (
        <p className="warning-copy">配置缺失或不支持；不能推断权限与成功。</p>
      )}
      {error && <p role="alert">{error}</p>}
      {staleApproval && (
        <p className="warning-copy">
          已批准材料已过期；请诊断或新建任务。不会重新准备、重试或重放。
        </p>
      )}
      {failedActions.length > 0 && (
        <p className="warning-copy">
          失败动作已停止；刷新仅读取事实。新修订下的精确清理仍需再次明确点击。
        </p>
      )}
      {baseline && (
        <>
          <p>
            {c4Supported(mission)
              ? c4OperationLabel(baseline)
              : "配置不支持 · 仅显示原始事实"}
          </p>
          <p className="muted small">
            操作 <code>{baseline.id}</code> · 材料{" "}
            <code>{baseline.material_digest}</code> · 配置摘要{" "}
            <code>{baseline.profile_digest}</code>
          </p>
        </>
      )}
      <p className="note">
        准备仅生成材料；批准不执行；执行只消费一次已批准基线。基线不是候选证据，也不批准上下文、计划或交付。
      </p>
      {canPrepare && (
        <button
          className="full"
          disabled={disabled || actionFailed("prepare")}
          onClick={() =>
            void act({ action: "prepare", revision: mission.revision })
          }
        >
          准备基线材料
        </button>
      )}
      {baseline?.state === "prepared" && !baseline.approval && (
        <button
          className="full"
          disabled={disabled || actionFailed("approve", baseline.id)}
          onClick={() => void act({ action: "approve", ...material(baseline) })}
        >
          批准基线材料
        </button>
      )}
      {baseline?.state === "approved" &&
        !!baseline.approval &&
        !baseline.dispatch && (
          <button
            className="full"
            disabled={
              disabled || staleApproval || actionFailed("execute", baseline.id)
            }
            onClick={() =>
              void act({ action: "execute", ...material(baseline) })
            }
          >
            执行已批准基线（模拟）
          </button>
        )}
      {operations
        .filter((o) => o.result && c4CleanupState(o) !== "confirmed")
        .map((o) => (
          <div className="note warning-note" key={o.id}>
            <p>
              {o.id} ·{" "}
              {c4Supported(mission)
                ? c4OperationLabel(o)
                : "配置不支持 · 仅显示原始事实"}
              。清理仅检查并移除已知精确对象，不重放，不改变结果。
            </p>
            <code>
              {o.container_id ?? "未记录精确对象 ID；不能自动发现或清理"}
            </code>
            {o.container_id && (
              <button
                disabled={disabled || actionFailed("cleanup", o.id)}
                onClick={() =>
                  void act({
                    action: "cleanup",
                    ...material(o),
                    container_id: o.container_id!,
                  })
                }
              >
                仅清理已记录对象 · {o.id}
              </button>
            )}
          </div>
        ))}
      <button
        disabled={pending || loading}
        onClick={() => {
          setError("");
          setRefreshKey((k) => k + 1);
        }}
      >
        刷新 C4 事实
      </button>
    </section>
  );
}

function OperationFacts({
  operation: o,
  mission,
}: {
  operation: C4Operation;
  mission: Mission;
}) {
  const attempt = mission.coding_attempts?.find(
    (a) =>
      a.phase === "verification" &&
      a.epoch === o.epoch &&
      a.plan_revision === o.plan_revision &&
      a.before?.digest === o.candidate_digest,
  );
  return (
    <article className="review">
      <h3>
        {o.id} ·{" "}
        {c4Supported(mission)
          ? c4OperationLabel(o)
          : "配置不支持 · 仅显示原始事实"}
      </h3>
      <dl className="metadata">
        <dt>运行 / 计划</dt>
        <dd>
          {o.epoch} / r{o.plan_revision}
        </dd>
        <dt>步骤 / 尝试 / 修复轮</dt>
        <dd>
          {o.kind === "baseline"
            ? "基线不属于计划步骤"
            : `${attempt?.step_id || "整体验证（非单步骤）"} / ${attempt?.id || "未记录关联"} / ${attempt?.repair_round ?? "未记录"}`}
        </dd>
        <dt>材料摘要</dt>
        <dd>
          <code>{o.material_digest}</code>
        </dd>
        <dt>配置摘要</dt>
        <dd>
          <code>{o.profile_digest}</code>
        </dd>
        <dt>冻结基线</dt>
        <dd>
          <code>{o.base_commit}</code> · <code>{o.base_digest}</code>
        </dd>
        <dt>候选代次 / 摘要</dt>
        <dd>
          {o.kind === "baseline" ? (
            "不适用 · 基线不是候选证据"
          ) : (
            <>
              <code>{o.candidate_generation?.id ?? "未记录"}</code> ·{" "}
              <code>{o.candidate_digest ?? "未记录"}</code>
            </>
          )}
        </dd>
        <dt>容器声明 / 精确 ID</dt>
        <dd>
          <code>{o.container_name}</code> /{" "}
          <code>{o.container_id ?? "未记录"}</code>
        </dd>
        <dt>准备</dt>
        <dd>{o.prepared_at}</dd>
        {(
          [
            ["批准", o.approval],
            ["派发", o.dispatch],
            ["创建", o.created],
            ["启动", o.started],
            ["结果", o.result],
          ] as const
        ).map(([label, fact]) => (
          <Fragment key={label}>
            <dt>{label}</dt>
            <dd>{fact ? `r${fact.revision} · ${fact.at}` : "未记录"}</dd>
          </Fragment>
        ))}
        <dt>结果 / 退出码 / 模拟测试数</dt>
        <dd>
          {o.result?.class ?? "未知"} / {o.result?.exit_code ?? "未记录"} /{" "}
          {o.result?.tests ?? "未记录"}
        </dd>
        <dt>日志截断</dt>
        <dd>
          {o.result
            ? o.result.truncated === true
              ? "已截断"
              : o.result.truncated === false
                ? "未截断"
                : "未记录"
            : "未记录"}
        </dd>
        <dt>清理</dt>
        <dd>{c4CleanupState(o)}</dd>
      </dl>
      {(o.cleanup ?? []).map((c, i) => (
        <p className="muted small" key={i}>
          清理 {i + 1} · {c.state} · r{c.revision} · {c.at} ·{" "}
          <code>{c.container_id}</code> · owner <code>{c.owner}</code>
        </p>
      ))}
      <pre>{o.result?.output || "无已记录输出"}</pre>
    </article>
  );
}
export function C4Verification({ mission }: { mission: Mission }) {
  if (!hasC4(mission)) return null;
  return (
    <section className="review-section">
      <h2>C4 验证事实{c4Supported(mission) ? " · synthetic_stub" : ""}</h2>
      <C4ProfileFacts mission={mission} />
      {(
        [
          ["baseline", "C4 基线记录"],
          ["candidate", "C4 候选记录"],
        ] as const
      ).map(([kind, title]) => {
        const operations = (mission.c4_operations ?? []).filter(
          (o) => o.kind === kind,
        );
        return (
          <section aria-label={title} key={kind}>
            <h2>{title}</h2>
            {!operations.length && (
              <p className="quiet-state">尚无记录；不能推断成功。</p>
            )}
            {operations.map((o) => (
              <OperationFacts key={o.id} operation={o} mission={mission} />
            ))}
          </section>
        );
      })}
      {(mission.c4_operations ?? [])
        .filter((o) => !["baseline", "candidate"].includes(o.kind))
        .map((o) => (
          <OperationFacts key={o.id} operation={o} mission={mission} />
        ))}
    </section>
  );
}
