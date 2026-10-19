import type { Mission } from "../types";
import { decisionKind, missionStateName } from "../deliveryStatus";
import { WorkbenchStatus } from "./WorkbenchStatus";
export type Section =
  "overview" | "projects" | "catalog" | "decisions" | "deliveries" | "settings";
export function Sidebar({
  missions,
  missionSnapshotAvailable,
  section,
  selected,
  onSection,
  onSelect,
  onCreate,
  loading,
  error,
  stream,
  lastDataReceivedAt,
}: {
  missions: Mission[];
  missionSnapshotAvailable: boolean;
  section: Section;
  selected: string;
  onSection: (s: Section) => void;
  onSelect: (id: string) => void;
  onCreate: () => void;
  loading: boolean;
  error: string;
  stream: string;
  lastDataReceivedAt: number | null;
}) {
  const decisions = missions.filter((m) => decisionKind(m));
  const visible =
    section === "decisions"
      ? decisions
      : section === "deliveries"
        ? missions.filter((m) => m.state === "delivered")
        : missions;
  return (
    <aside className="sidebar">
      <a className="brand" href="#main">
        <span className="brand-mark" aria-hidden="true">
          F
        </span>{" "}
        FOUNDRY
      </a>
      <nav aria-label="主导航">
        {(
          [
            ["overview", "⌂", "交付总览"],
            ["decisions", "◷", "待决策"],
            ["projects", "▱", "交付任务"],
            ["catalog", "▦", "项目资产"],
            ["deliveries", "◇", "交付记录"],
            ["settings", "⚙", "设置"],
          ] as const
        ).map(([key, icon, label]) => (
          <button
            key={key}
            className={section === key ? "nav-item selected" : "nav-item"}
            aria-current={section === key ? "page" : undefined}
            onClick={() => onSection(key)}
          >
            <span aria-hidden="true">{icon}</span>
            {label}
            {key === "decisions" && (
              <span
                className="count"
                aria-label={
                  missionSnapshotAvailable ? undefined : "待决策数量不可用"
                }
              >
                {missionSnapshotAvailable ? decisions.length : "—"}
              </span>
            )}
          </button>
        ))}
      </nav>
      <section className="mission-nav">
        <div className="section-label">
          交付任务
          <button
            className="icon-button"
            aria-label="新建任务"
            onClick={onCreate}
          >
            ＋
          </button>
        </div>
        {!visible.length && (
          <p className="muted small">
            {!missionSnapshotAvailable
              ? "任务列表尚未读取"
              : section === "deliveries"
                ? "尚无交付记录"
                : "暂无任务"}
          </p>
        )}
        {visible.map((m) => (
          <button
            key={m.id}
            onClick={() => onSelect(m.id)}
            className={`mission-item ${selected === m.id ? "selected" : ""}`}
          >
            <span aria-hidden="true">▤</span>
            <span>
              {m.title}
              <small>{missionStateName(m)}</small>
            </span>
          </button>
        ))}
      </section>
      <div className="sidebar-footer">
        <WorkbenchStatus
          loading={loading}
          error={error}
          snapshotReceived={missionSnapshotAvailable}
          stream={stream}
          lastDataReceivedAt={lastDataReceivedAt}
        />
        <p>独立候选 · 本地交付</p>
      </div>
    </aside>
  );
}
