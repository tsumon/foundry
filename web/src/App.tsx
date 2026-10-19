import { useRef, useState } from "react";
import { useWorkbench } from "./useWorkbench";
import { Sidebar, type Section } from "./components/Sidebar";
import {
  MissionWorkspace,
  createMissionSession,
  type MissionSession,
} from "./components/MissionWorkspace";
import { StageRail } from "./components/StageRail";
import { CreateMission } from "./components/CreateMission";
import { CapabilitySettings } from "./components/capabilities/CapabilitySettings";
import { DemoWorkspace } from "./components/DemoWorkspace";
import {
  CatalogWorkspace,
  type CatalogView,
} from "./components/catalog/CatalogWorkspace";
import { DeliveryOverview } from "./components/DeliveryOverview";
import { DecisionQueue } from "./components/DecisionQueue";
import type { DecisionTargetView } from "./decisionMaterials";
import { useCatalog } from "./useCatalog";
import { missionProjectContext } from "./projectContext";
import { RecoveryNotice } from "./components/RecoveryNotice";

type MissionSource =
  | { kind: "section"; section: Section }
  | {
      kind: "catalog";
      projectId: string;
      projectName: string;
      view: CatalogView;
    };

export default function App() {
  const work = useWorkbench();
  const [section, setSection] = useState<Section>("overview");
  const [selected, setSelected] = useState("");
  // Drafts and the shared save/action lock outlive workspace navigation.
  const [missionSessions, setMissionSessions] = useState<
    Record<string, MissionSession>
  >({});
  const [creating, setCreating] = useState(false);
  const [demo, setDemo] = useState(false);
  const [decisionOpen, setDecisionOpen] = useState(false);
  const [initialMissionView, setInitialMissionView] = useState<
    DecisionTargetView | undefined
  >();
  const [selectedProject, setSelectedProject] = useState("");
  const [catalogView, setCatalogView] = useState<CatalogView>("目标");
  const [missionSource, setMissionSource] = useState<MissionSource>({
    kind: "section",
    section: "projects",
  });
  const creationSource = useRef<MissionSource>({
    kind: "section",
    section: "overview",
  });
  const missionSnapshotAvailable =
    work.snapshotReceived || work.missions.length > 0;
  const catalog = useCatalog(work.readReady);
  const current = work.missions.find((m) => m.id === selected);
  const projectContextFor = (id: string) =>
    missionProjectContext(id, {
      snapshot: catalog.snapshot,
      enabled: work.readReady && catalog.ready,
      loading: catalog.loading,
      error: catalog.error,
    });
  const navigate = (value: Section) => {
    setSection(value);
    setCreating(false);
    setDemo(false);
    setSelected("");
    setDecisionOpen(false);
    setInitialMissionView(undefined);
  };
  const beginCreate = () => {
    creationSource.current =
      section === "catalog" && selectedProject
        ? {
            kind: "catalog",
            projectId: selectedProject,
            projectName:
              catalog.snapshot?.projects.find(
                (project) => project.id === selectedProject,
              )?.name ?? "项目任务",
            view: catalogView,
          }
        : { kind: "section", section };
    setCreating(true);
    setDemo(false);
  };
  const openMission = (
    id: string,
    openDecision = false,
    initialView?: DecisionTargetView,
    source?: MissionSource,
  ) => {
    const mission = work.missions.find((item) => item.id === id);
    if (mission)
      setMissionSessions((current) =>
        current[id]
          ? current
          : { ...current, [id]: createMissionSession(mission) },
      );
    setSelected(id);
    setDecisionOpen(openDecision);
    setInitialMissionView(initialView);
    setMissionSource(
      source ??
        (section === "catalog"
          ? {
              kind: "catalog",
              projectId: selectedProject,
              projectName:
                catalog.snapshot?.projects.find(
                  (project) => project.id === selectedProject,
                )?.name ?? "项目任务",
              view: catalogView,
            }
          : { kind: "section", section }),
    );
    setSection("projects");
    setCreating(false);
    setDemo(false);
  };
  const returnFromMission = () => {
    setSelected("");
    setDecisionOpen(false);
    setInitialMissionView(undefined);
    if (missionSource.kind === "catalog") {
      setSelectedProject(missionSource.projectId);
      setCatalogView(missionSource.view);
      setSection("catalog");
    } else setSection(missionSource.section);
  };
  const openProject = (projectId: string) => {
    setSelectedProject(projectId);
    setCatalogView("项目任务");
    setSelected("");
    setDecisionOpen(false);
    setSection("catalog");
  };
  const catalogVisible =
    section === "catalog" && !creating && !demo && !current;
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main">
        跳到工作区
      </a>
      <Sidebar
        missions={work.missions}
        missionSnapshotAvailable={missionSnapshotAvailable}
        section={section}
        selected={selected}
        onSection={navigate}
        onSelect={(id) =>
          openMission(id, false, undefined, { kind: "section", section })
        }
        onCreate={beginCreate}
        loading={work.loading}
        error={work.error}
        stream={work.stream}
        lastDataReceivedAt={work.lastDataReceivedAt}
      />
      <main id="main" className="main-shell">
        <RecoveryNotice enabled={work.readReady} />
        {work.error && !["overview", "decisions"].includes(section) && (
          <div className="global-error" role="alert">
            本地服务请求失败：{work.error}
            <button onClick={work.retry}>重新连接</button>
          </div>
        )}
        <div hidden={!catalogVisible}>
          <CatalogWorkspace
            enabled={work.readReady}
            missions={work.missions}
            onOpenMission={(id) =>
              openMission(id, false, undefined, {
                kind: "catalog",
                projectId: selectedProject,
                projectName:
                  catalog.snapshot?.projects.find(
                    (project) => project.id === selectedProject,
                  )?.name ?? "项目任务",
                view: catalogView,
              })
            }
            catalog={catalog}
            selected={selectedProject}
            onSelected={setSelectedProject}
            view={catalogView}
            onView={setCatalogView}
          />
        </div>
        {catalogVisible ? null : creating ? (
          <CreateMission
            onCancel={() => setCreating(false)}
            onCreated={(m) => {
              work.update(m);
              setMissionSessions((current) => ({
                ...current,
                [m.id]: createMissionSession(m),
              }));
              setSelected(m.id);
              setMissionSource(creationSource.current);
              setDecisionOpen(false);
              setInitialMissionView(undefined);
              setCreating(false);
              setSection("projects");
            }}
          />
        ) : section === "settings" ? (
          <CapabilitySettings
            enabled={work.readReady}
            catalog={catalog}
            missions={work.missions.map(({ id, title }) => ({ id, title }))}
            onOpenMission={(id) =>
              openMission(id, false, undefined, {
                kind: "section",
                section: "settings",
              })
            }
          />
        ) : demo ? (
          <DemoWorkspace onClose={() => setDemo(false)} />
        ) : current ? (
          <MissionWorkspace
            key={current.id}
            mission={current}
            session={missionSessions[current.id]}
            onSessionChange={(value) =>
              setMissionSessions((sessions) => {
                const previous =
                  sessions[current.id] ?? createMissionSession(current);
                return {
                  ...sessions,
                  [current.id]:
                    typeof value === "function" ? value(previous) : value,
                };
              })
            }
            events={work.events.filter((e) => e.mission_id === current.id)}
            stream={work.stream}
            hooksEnabled={work.readReady}
            onUpdate={work.update}
            initialDecisionOpen={decisionOpen}
            initialView={initialMissionView}
            projectContext={projectContextFor(current.id)}
            onOpenProject={openProject}
            onBack={returnFromMission}
            backLabel={
              missionSource.kind === "catalog"
                ? `返回项目 ${missionSource.projectName}`
                : missionSource.section === "decisions"
                  ? "返回待决策"
                  : missionSource.section === "overview"
                    ? "返回交付总览"
                    : "返回交付任务"
            }
          />
        ) : section === "overview" ? (
          <DeliveryOverview
            missions={work.missions}
            events={work.events}
            loading={work.loading}
            error={work.error}
            stream={work.stream}
            onOpenMission={(id, decision) =>
              openMission(id, decision, undefined, {
                kind: "section",
                section: "overview",
              })
            }
            onCreate={beginCreate}
            onSettings={() => navigate("settings")}
            onDemo={() => setDemo(true)}
            onRetry={work.retry}
            projectContextFor={projectContextFor}
          />
        ) : section === "decisions" ? (
          <DecisionQueue
            missions={work.missions}
            loading={work.loading}
            error={work.error}
            stream={work.stream}
            onOpenMission={(id, decision, view) =>
              openMission(id, decision, view, {
                kind: "section",
                section: "decisions",
              })
            }
            onRetry={work.retry}
            projectContextFor={projectContextFor}
          />
        ) : (
          <div className="workspace empty-workspace">
            <section className="workspace-center">
              <header className="mission-header">
                <div className="eyebrow">
                  工作空间 /{" "}
                  {section === "deliveries" ? "交付记录" : "软件交付"}
                </div>
                <h1>
                  {section === "deliveries"
                    ? "每次交付，有据可查。"
                    : "让需求成为可验证的交付。"}
                </h1>
                <p className="muted">从明确目标开始，在关键节点由你做决定。</p>
                <StageRail />
              </header>
              <section className="empty-content">
                <span className="empty-symbol" aria-hidden="true">
                  ▤
                </span>
                <p className="eyebrow">YOUR NEXT DELIVERY</p>
                <h2>
                  {work.loading
                    ? "正在连接本地工作台…"
                    : !missionSnapshotAvailable
                      ? "任务数据尚不可用"
                      : work.missions.length
                        ? "选择一个交付任务"
                        : "第一份交付，从这里开始"}
                </h2>
                <p className="muted">
                  连接模型，选择一个已提交的 Git 仓库。
                  <br />
                  检查上下文、审批计划，再审阅真实的验证证据。
                </p>
                <div className="actions">
                  <button
                    className="primary"
                    disabled={work.loading || !!work.error}
                    onClick={beginCreate}
                  >
                    创建交付任务
                  </button>
                  <button onClick={() => navigate("settings")}>配置模型</button>
                </div>
                <button className="text-button" onClick={() => setDemo(true)}>
                  查看演示 <span aria-hidden="true">↗</span>
                </button>
              </section>
              <div className="empty-footnote">
                原仓库只读 · 单 Worker 串行执行 · 交付本地 patch
              </div>
            </section>
            <aside className="decision-column empty-guide">
              <p className="eyebrow">你掌握每一步</p>
              <h2>从需求到交付</h2>
              <ol className="guide-list">
                <li>
                  <strong>界定工作范围</strong>
                  <p>明确目标、可修改文件和验收命令。</p>
                </li>
                <li>
                  <strong>检查并授权上下文</strong>
                  <p>查看文件清单与实际模型端点后，再发起规划。</p>
                </li>
                <li>
                  <strong>审计划，审证据</strong>
                  <p>只有明确批准，才执行计划或生成交付。</p>
                </li>
              </ol>
              <div className="note">
                验证在受限 Docker 容器内运行。不会自动安装依赖、推送或部署。
              </div>
            </aside>
          </div>
        )}
      </main>
    </div>
  );
}
