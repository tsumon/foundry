import {
  decisionKind,
  isMissionInProgress,
  knownStates,
  missionStateName,
  orderMissions,
} from "../deliveryStatus";
import type { Mission, RunEvent } from "../types";
import type { MissionProjectContext } from "../projectContext";

interface Props {
  missions: Mission[];
  events: RunEvent[];
  loading: boolean;
  error: string;
  stream: string;
  onOpenMission: (id: string, decision?: boolean) => void;
  onCreate: () => void;
  onSettings: () => void;
  onDemo: () => void;
  onRetry?: () => void;
  projectContextFor?: (id: string) => MissionProjectContext;
}

export function DeliveryOverview(props: Props) {
  const { missions, events, loading, error, stream } = props;
  if (loading && !missions.length)
    return <StatusPage title="交付总览" message="正在读取本地交付状态…" />;
  if (error && !missions.length)
    return (
      <StatusPage title="交付总览" message={`无法读取最新状态：${error}`} alert>
        <p className="muted">
          当前页面没有把缺失数据计为零；若下方仍有内容，它也是上次收到的状态。
        </p>
        {props.onRetry && <button onClick={props.onRetry}>重新连接</button>}
      </StatusPage>
    );
  if (!missions.length)
    return (
      <StatusPage title="交付总览" message="第一份交付，从这里开始">
        <p className="muted">
          连接模型，选择一个已提交的 Git 仓库，检查上下文、计划与真实验证证据。
        </p>
        <div className="actions">
          <button className="primary" onClick={props.onCreate}>
            创建交付任务
          </button>
          <button onClick={props.onSettings}>配置模型</button>
          <button className="text-button" onClick={props.onDemo}>
            查看演示
          </button>
        </div>
      </StatusPage>
    );

  const pending = missions.filter((m) =>
    ["context", "mcp_call", "plan", "delivery", "c4_baseline"].includes(decisionKind(m) ?? ""),
  ).length;
  const metrics = [
    ["in-progress", "进行中", missions.filter(isMissionInProgress).length],
    ["decisions", "待决策", pending],
    [
      "attention",
      "需要处理",
      missions.filter((m) => decisionKind(m) === "attention").length,
    ],
    [
      "delivered",
      "已交付",
      missions.filter((m) => m.state === "delivered").length,
    ],
    [
      "unknown",
      "未知状态",
      missions.filter((m) => !knownStates.includes(m.state)).length,
    ],
  ] as const;
  const missionsByID = new Map(
    missions.map((mission) => [mission.id, mission]),
  );
  return (
    <div className="delivery-page">
      <header className="delivery-header">
        <div className="eyebrow">本地工作台 / 真实任务状态</div>
        <div className="delivery-title-row">
          <div>
            <h1>交付总览</h1>
            <p className="muted">
              只汇总控制器已返回的数据，不推断完成百分比或发布就绪。
            </p>
          </div>
          <button className="primary" onClick={props.onCreate}>
            创建交付任务
          </button>
        </div>
      </header>
      {(error || loading || stream !== "事件流已连接") && (
        <div className="stale-state" role={error ? "alert" : "status"}>
          <span>
            {error
              ? `以下为最后收到的任务状态 · 更新失败：${error}`
              : loading
                ? "正在刷新 · 以下为最后收到的任务状态"
                : `以下为最后收到的任务状态 · ${stream}`}
          </span>
          {props.onRetry && (
            <button onClick={props.onRetry} disabled={loading}>
              重新连接
            </button>
          )}
        </div>
      )}
      <section className="metric-grid" aria-label="交付状态统计">
        {metrics.map(([key, label, value]) => (
          <div className="metric-card" data-testid={`metric-${key}`} key={key}>
            <strong>{value}</strong>
            <span>{label}</span>
          </div>
        ))}
      </section>
      <div className="delivery-columns">
        <section aria-labelledby="mission-list-title">
          <div className="section-heading">
            <h2 id="mission-list-title">交付任务</h2>
            <span className="tag">{missions.length} 项</span>
          </div>
          <div className="delivery-list">
            {orderMissions(missions).map((item) => {
              const unknown = !knownStates.includes(item.state);
              return (
                <article className="delivery-row" key={item.id}>
                  <div>
                    <h3>{item.title}</h3>
                    <p className="muted">{item.goal}</p>
                    {props.projectContextFor && (
                      <p className="project-context">
                        任务项目：{props.projectContextFor(item.id).label}
                      </p>
                    )}
                  </div>
                  <div className="delivery-facts">
                    <span>
                      {unknown
                        ? `未知状态：${item.state}`
                        : missionStateName(item)}
                    </span>
                    <span>r{item.revision}</span>
                    <span>
                      {item.budget.used_calls} / {item.budget.max_calls} 次调用
                    </span>
                    <time>{item.updated_at}</time>
                  </div>
                  <button onClick={() => props.onOpenMission(item.id)}>
                    查看任务
                  </button>
                </article>
              );
            })}
          </div>
        </section>
        <section className="event-panel" aria-labelledby="recent-events-title">
          <div className="section-heading">
            <h2 id="recent-events-title">最近状态事件</h2>
            <span className="tag">
              {stream === "事件流已连接" ? stream : `${stream} · 最后收到状态`}
            </span>
          </div>
          <p className="muted small">
            本次页面会话最多保留最近 200 条；这里是连接缓冲，不是完整审计日志。
          </p>
          {!events.length && (
            <p className="muted">本次连接尚未收到状态事件。</p>
          )}
          {[...events]
            .slice(-12)
            .reverse()
            .map((event) => {
              const eventMission = missionsByID.get(event.mission_id);
              return (
                <div className="overview-event" key={event.seq}>
                  <code>#{event.seq}</code>
                  <span className="event-mission">
                    {eventMission ? (
                      <>
                        <strong>{eventMission.title}</strong>
                        <code>{event.mission_id}</code>
                      </>
                    ) : (
                      <>
                        任务 ID <code>{event.mission_id}</code>
                      </>
                    )}
                  </span>
                  <span>{event.kind}</span>
                  <span>r{event.revision}</span>
                  <time>{event.created_at}</time>
                </div>
              );
            })}
        </section>
      </div>
    </div>
  );
}

function StatusPage({
  title,
  message,
  alert = false,
  children,
}: {
  title: string;
  message: string;
  alert?: boolean;
  children?: React.ReactNode;
}) {
  return (
    <div className="delivery-page delivery-status-page">
      <header>
        <div className="eyebrow">本地工作台</div>
        <h1>{title}</h1>
      </header>
      <section className="status-card" role={alert ? "alert" : undefined}>
        <h2>{message}</h2>
        {children}
      </section>
    </div>
  );
}
