import { useEffect } from "react";
import type { Mission } from "../../types";
import type { CatalogController } from "../../useCatalog";
import {
  EnvironmentForm,
  ProjectForm,
  RenameForm,
  ResourceForm,
  TargetForm,
} from "./CatalogForms";
import { MissionRelations, ResourceBindings } from "./CatalogRelations";
import "../../catalog.css";

export function CatalogWorkspace({
  enabled,
  missions,
  onOpenMission,
  catalog,
  selected,
  onSelected,
  view,
  onView,
}: {
  enabled: boolean;
  missions: Mission[];
  onOpenMission: (id: string) => void;
  catalog: CatalogController;
  selected: string;
  onSelected: (id: string) => void;
  view: CatalogView;
  onView: (view: CatalogView) => void;
}) {
  const snapshot = catalog.snapshot;
  const project = snapshot?.projects.find((item) => item.id === selected);
  const projectTargets = snapshot?.targets.filter(
    (item) => item.project_id === selected,
  );
  const projectEnvironments = snapshot?.environments.filter(
    (item) => item.project_id === selected,
  );
  const projectBindings = snapshot?.bindings.filter(
    (item) => item.project_id === selected,
  );
  const projectMissionLinks = snapshot?.mission_links.filter(
    (item) => item.project_id === selected,
  );
  useEffect(() => {
    if (snapshot && selected && !project) onSelected("");
  }, [snapshot, selected, project, onSelected]);
  const disabled =
    !enabled ||
    !catalog.ready ||
    catalog.loading ||
    catalog.saving ||
    catalog.conflict;
  const actions = { disabled, execute: catalog.execute };
  return (
    <div className="catalog-workspace">
      <header className="catalog-header">
        <div>
          <p className="eyebrow">
            工作空间 / 项目资产{project ? ` / ${project.name}` : ""}
          </p>
          <h1>{project?.name ?? "项目资产"}</h1>
        </div>
        <button
          disabled={!enabled || catalog.loading || catalog.saving}
          onClick={() => void catalog.refresh()}
        >
          重新加载
        </button>
      </header>
      <div className="catalog-boundary" aria-label="台账边界">
        本地元数据 · 未核验 · 不连接外部平台 · 请勿输入秘密
      </div>
      <div role="status" aria-live="polite" className="catalog-status">
        {catalog.saving
          ? "正在保存台账…"
          : catalog.loading
            ? "正在读取台账…"
            : !enabled
              ? "等待本地会话连接成功"
              : snapshot
                ? `台账 revision ${snapshot.revision}`
                : "台账尚未读取"}
      </div>
      {catalog.error && (
        <div role="alert" className="form-error">
          {catalog.error}
          {catalog.conflict && (
            <p>
              存在版本或关系冲突。输入已保留；请先重新加载并检查最新台账，再决定是否提交。不会自动重试。
            </p>
          )}
        </div>
      )}
      {snapshot && (
        <>
          {project ? (
            <div key={project.id}>
              <button className="text-button" onClick={() => onSelected("")}>
                ← 返回项目列表
              </button>
              <div className="catalog-project-context" aria-label="项目概况">
                <p>{project.description || "暂无项目说明"}</p>
                <ul className="catalog-counts">
                  <li>{projectTargets?.length ?? 0} 个目标</li>
                  <li>{projectEnvironments?.length ?? 0} 个环境</li>
                  <li>{projectBindings?.length ?? 0} 个绑定</li>
                  <li>{projectMissionLinks?.length ?? 0} 个任务关系</li>
                </ul>
              </div>
              <details className="catalog-disclosure catalog-project-details">
                <summary>项目资料</summary>
                <ProjectForm project={project} {...actions} />
              </details>
              <nav aria-label="项目资产视角" className="catalog-tabs">
                {catalogViews.map((tab) => (
                  <button
                    key={tab}
                    aria-current={view === tab ? "page" : undefined}
                    className={view === tab ? "selected" : ""}
                    onClick={() => onView(tab)}
                  >
                    {tab}
                  </button>
                ))}
              </nav>
              <div hidden={view !== "目标"} className="catalog-panel">
                <h2>交付目标</h2>
                <p className="muted">
                  允许的安装包意向，不代表具备构建或分发能力。
                </p>
                <section aria-label="已登记目标">
                  <ul className="catalog-list">
                    {projectTargets?.map((item) => (
                      <li key={item.id}>
                        <div>
                          <strong>{item.name}</strong>
                          <p>
                            {item.platform} · {item.format} ·{" "}
                            {item.architecture} · {item.distribution}
                          </p>
                          <small>已登记 · 未核验</small>
                        </div>
                        <RenameForm
                          kind="target.rename"
                          id={item.id}
                          name={item.name}
                          {...actions}
                        />
                      </li>
                    ))}
                  </ul>
                  {!projectTargets?.length && (
                    <p className="muted">
                      暂无交付目标。展开下方操作可登记 exe / dmg / apk / ipa。
                    </p>
                  )}
                </section>
                <details className="catalog-disclosure">
                  <summary>登记交付目标</summary>
                  <p className="muted small">
                    请勿输入签名密钥、凭据或其他秘密。
                  </p>
                  <TargetForm projectId={project.id} {...actions} />
                </details>
              </div>
              <div hidden={view !== "环境"} className="catalog-panel">
                <h2>项目环境</h2>
                <section aria-label="已登记环境">
                  <ul className="catalog-list">
                    {projectEnvironments?.map((item) => (
                      <li key={item.id}>
                        <div>
                          <strong>{item.name}</strong>
                          <p>{item.tier} · 已登记 · 未核验</p>
                        </div>
                        <RenameForm
                          kind="environment.rename"
                          id={item.id}
                          name={item.name}
                          {...actions}
                        />
                      </li>
                    ))}
                  </ul>
                  {!projectEnvironments?.length && (
                    <p className="muted">暂无环境。展开下方操作可登记环境。</p>
                  )}
                </section>
                <details className="catalog-disclosure">
                  <summary>登记环境</summary>
                  <p className="muted small">
                    只登记本地元数据；请勿输入凭据或秘密。
                  </p>
                  <EnvironmentForm projectId={project.id} {...actions} />
                </details>
              </div>
              <div hidden={view !== "资源"} className="catalog-panel">
                <h2>工作空间全部资源</h2>
                <p className="muted">
                  显示所有归属的资源，可被多个项目环境共享。
                </p>
                <section aria-label="已登记资源">
                  <ul className="catalog-list">
                    {snapshot.resources.map((item) => (
                      <li key={item.id}>
                        <div>
                          <strong>{item.name}</strong>
                          <p>
                            {item.kind} · {item.provider} ·{" "}
                            {snapshot.projects.find(
                              (project) => project.id === item.owner_project_id,
                            )?.name ?? "工作空间共享"}
                          </p>
                          <small>已登记 · 未核验 · 保护：保留</small>
                          {item.provider !== "manual" && (
                            <p className="small">
                              {item.account} / {item.region} /{" "}
                              {item.external_id}
                            </p>
                          )}
                        </div>
                        <RenameForm
                          kind="resource.rename"
                          id={item.id}
                          name={item.name}
                          {...actions}
                        />
                      </li>
                    ))}
                  </ul>
                  {!snapshot.resources.length && (
                    <p className="muted">
                      工作空间尚无资源。展开“登记工作空间资源”可添加资源。
                    </p>
                  )}
                </section>
                <details className="catalog-disclosure">
                  <summary>登记工作空间资源</summary>
                  <p className="muted small">
                    只允许非秘密标识；禁止密钥、连接串及 URL。
                  </p>
                  <ResourceForm snapshot={snapshot} {...actions} />
                </details>
                <ResourceBindings
                  snapshot={snapshot}
                  project={project}
                  {...actions}
                />
              </div>
              <div hidden={view !== "项目任务"} className="catalog-panel">
                <MissionRelations
                  snapshot={snapshot}
                  project={project}
                  missions={missions}
                  onOpenMission={onOpenMission}
                  {...actions}
                />
              </div>
            </div>
          ) : (
            <div className="catalog-projects">
              <section>
                <h2>
                  项目列表{" "}
                  <span className="muted small">
                    {snapshot.projects.length}
                  </span>
                </h2>
                <ul className="catalog-list">
                  {snapshot.projects.map((item) => (
                    <li key={item.id}>
                      <div>
                        <button
                          className="text-button"
                          onClick={() => {
                            onSelected(item.id);
                            onView("目标");
                          }}
                        >
                          {item.name}
                        </button>
                        <p className="muted">
                          {item.description || "暂无项目说明"}
                        </p>
                        <small>已登记 · 未核验</small>
                      </div>
                    </li>
                  ))}
                </ul>
                {!snapshot.projects.length && (
                  <p className="muted">
                    还没有项目。先建立项目，再登记交付目标、环境及共享资源。
                  </p>
                )}
              </section>
              <details className="catalog-disclosure catalog-create-project">
                <summary>新建项目</summary>
                <p className="muted small">
                  只登记本地元数据；请勿在名称或说明中输入秘密。
                </p>
                <ProjectForm {...actions} />
              </details>
            </div>
          )}
          <details className="catalog-history">
            <summary>最近台账操作（{catalog.events.length}）</summary>
            <p className="muted small">
              只读操作索引，最多显示当前快照的最近100项；不重放命令。
            </p>
            {!catalog.events.length && (
              <p className="muted">
                {snapshot.revision ? "历史暂未完整读取。" : "尚无台账操作。"}
              </p>
            )}
            <ol>
              {[...catalog.events].reverse().map((event) => (
                <li key={event.seq}>
                  <strong>{event.kind}</strong>
                  <span>
                    revision {event.catalog_revision} · seq {event.seq}
                  </span>
                  <time dateTime={event.created_at}>{event.created_at}</time>
                </li>
              ))}
            </ol>
          </details>
        </>
      )}
    </div>
  );
}

export const catalogViews = ["目标", "环境", "资源", "项目任务"] as const;
export type CatalogView = (typeof catalogViews)[number];
