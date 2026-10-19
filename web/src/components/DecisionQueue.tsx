import {
  decisionKind,
  missionStateName,
  orderMissions,
  type DecisionKind,
} from "../deliveryStatus";
import type { Mission } from "../types";
import {
  decisionMaterial,
  type DecisionTargetView,
} from "../decisionMaterials";
import type { MissionProjectContext } from "../projectContext";

export function DecisionQueue({
  missions,
  loading,
  error,
  stream = "事件流已连接",
  onOpenMission,
  onRetry,
  projectContextFor,
}: {
  missions: Mission[];
  loading: boolean;
  error: string;
  stream?: string;
  onOpenMission: (
    id: string,
    decision: boolean,
    view?: DecisionTargetView,
  ) => void;
  onRetry?: () => void;
  projectContextFor?: (id: string) => MissionProjectContext;
}) {
  if (loading && !missions.length)
    return <QueueStatus message="正在读取待决策任务…" />;
  if (error && !missions.length)
    return (
      <QueueStatus message={`无法读取待决策任务：${error}`} alert>
        {onRetry && <button onClick={onRetry}>重新连接</button>}
      </QueueStatus>
    );
  const rows = orderMissions(missions).filter((mission) =>
    decisionKind(mission),
  );
  const approvals = rows.filter(
    (mission) => decisionKind(mission) !== "attention",
  );
  const attention = rows.filter(
    (mission) => decisionKind(mission) === "attention",
  );
  return (
    <div className="delivery-page decision-page">
      <header className="delivery-header">
        <div className="eyebrow">本地工作台 / 决策入口</div>
        <h1>待决策</h1>
        <p className="muted">
          此处只负责导航。审批、重新规划与交付仍在任务原有决策区完成。
        </p>
      </header>
      {(error || loading || stream !== "事件流已连接") && (
        <div className="stale-state" role={error ? "alert" : "status"}>
          <span>
            {error
              ? `以下为最后收到的待决策状态 · 更新失败：${error}`
              : loading
                ? "正在刷新 · 以下为最后收到的待决策状态"
                : `以下为最后收到的待决策状态 · ${stream}`}
          </span>
          {onRetry && (
            <button onClick={onRetry} disabled={loading}>
              重新连接
            </button>
          )}
        </div>
      )}
      {!rows.length && (
        <section className="status-card">
          <h2>当前没有待审批或需要处理的任务。</h2>
          <p className="muted">运行中的任务不会出现在这里。</p>
        </section>
      )}
      {!!approvals.length && (
        <QueueSection
          title="待审批"
          rows={approvals}
          onOpenMission={onOpenMission}
          projectContextFor={projectContextFor}
        />
      )}
      {!!attention.length && (
        <QueueSection
          title="需要处理"
          rows={attention}
          onOpenMission={onOpenMission}
          projectContextFor={projectContextFor}
        />
      )}
    </div>
  );
}

function QueueSection({
  title,
  rows,
  onOpenMission,
  projectContextFor,
}: {
  title: string;
  rows: Mission[];
  onOpenMission: (
    id: string,
    decision: boolean,
    view?: DecisionTargetView,
  ) => void;
  projectContextFor?: (id: string) => MissionProjectContext;
}) {
  return (
    <section className="queue-section" aria-label={title}>
      <div className="section-heading">
        <h2>{title}</h2>
        <span className="tag">{rows.length}</span>
      </div>
      <div className="delivery-list">
        {rows.map((mission) => {
          const kind = decisionKind(mission)!;
          const material = decisionMaterial(mission);
          return (
            <article className="queue-row" key={mission.id}>
              <div>
                <h3>{mission.title}</h3>
                <p className="muted">{decisionCopy(kind, mission)}</p>
                {projectContextFor && (
                  <p className="project-context">
                    任务项目：{projectContextFor(mission.id).label}
                  </p>
                )}
                <dl className="decision-material">
                  {material.facts.map((fact) => (
                    <div key={fact.label}>
                      <dt>{fact.label}</dt>
                      <dd>{fact.value}</dd>
                    </div>
                  ))}
                </dl>
              </div>
              <div className="delivery-facts">
                <span>{missionStateName(mission)}</span>
                <span>r{mission.revision}</span>
                <time>{mission.updated_at}</time>
              </div>
              <button
                aria-label={`${material.actionLabel}：${mission.title}`}
                onClick={() => onOpenMission(mission.id, true, material.view)}
              >
                {material.actionLabel}
              </button>
            </article>
          );
        })}
      </div>
    </section>
  );
}

function decisionCopy(kind: DecisionKind, mission: Mission) {
  if (kind === "context")
    return "检查将披露的仓库上下文，并在原决策区明确授权。";
  if (kind === "mcp_call")
    return "检查 Researcher 提议的一次精确本地测试工具调用；审批仍在任务总览完成。";
  if (kind === "plan")
    return mission.plan
      ? `审阅计划 r${mission.plan.revision}，再决定是否执行。`
      : "计划尚不可用";
  if (kind === "delivery")
    return mission.evidence.length
      ? "审阅当前验证证据；待审阅不代表已通过或可导出。"
      : "交付证据尚不可用；待审阅状态不代表已通过或可导出。";
  return "查看阻塞或暂停原因；原决策区只提供重新规划，不会直接恢复或重试。";
}

function QueueStatus({
  message,
  alert = false,
  children,
}: {
  message: string;
  alert?: boolean;
  children?: React.ReactNode;
}) {
  return (
    <div className="delivery-page decision-page">
      <header className="delivery-header">
        <div className="eyebrow">本地工作台 / 决策入口</div>
        <h1>待决策</h1>
      </header>
      <section className="status-card" role={alert ? "alert" : undefined}>
        <h2>{message}</h2>
        {children}
      </section>
    </div>
  );
}
