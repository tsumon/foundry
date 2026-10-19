import { useId, useState } from "react";
import type {
  CatalogCommand,
  CatalogSnapshot,
  Project,
} from "../../catalogTypes";
import type { Mission } from "../../types";
import { stateNames } from "../../types";
import { CatalogForm, type Save } from "./CatalogForms";

type Props = {
  snapshot: CatalogSnapshot;
  project: Project;
  disabled: boolean;
  execute: Save;
};
type Confirmation = { label: string; text: string; command: CatalogCommand };
function Confirm({
  value,
  disabled,
  execute,
  close,
}: {
  value: Confirmation;
  disabled: boolean;
  execute: Save;
  close: () => void;
}) {
  return (
    <div className="catalog-confirm" role="group" aria-label={value.label}>
      <p>{value.text}</p>
      <div className="actions">
        <button
          disabled={disabled}
          onClick={async () => {
            if (await execute(value.command)) close();
          }}
        >
          {value.label}
        </button>
        <button disabled={disabled} onClick={close}>
          取消
        </button>
      </div>
    </div>
  );
}
export function ResourceBindings({
  snapshot,
  project,
  disabled,
  execute,
}: Props) {
  const purposeHelpId = useId();
  const [environmentId, setEnvironmentId] = useState("");
  const [resourceId, setResourceId] = useState("");
  const [purpose, setPurpose] = useState("");
  const [confirmation, setConfirmation] = useState<Confirmation | null>(null);
  const environments = snapshot.environments.filter(
    (item) => item.project_id === project.id,
  );
  const validEnvironment = environments.some(
    (item) => item.id === environmentId,
  )
    ? environmentId
    : "";
  const validResource = snapshot.resources.some(
    (item) => item.id === resourceId,
  )
    ? resourceId
    : "";
  return (
    <section aria-label="项目资源绑定">
      <h2>当前项目的资源绑定</h2>
      <p className="muted">
        可以绑定其他项目拥有的资源。仅记录关系，不转移所有权或授予执行权限。
      </p>
      <ul className="catalog-list">
        {snapshot.bindings
          .filter((item) => item.project_id === project.id)
          .map((binding) => {
            const resource = snapshot.resources.find(
              (item) => item.id === binding.resource_id,
            )!;
            const environment = snapshot.environments.find(
              (item) => item.id === binding.environment_id,
            )!;
            const label = `${resource.name} / ${environment.name} / ${binding.purpose}`;
            return (
              <li key={binding.id}>
                <div>
                  <strong>{label}</strong>
                  <p className="muted small">仅本地关联 · 已登记 · 未核验</p>
                </div>
                <button
                  disabled={disabled}
                  onClick={() =>
                    setConfirmation({
                      label: "确认解除绑定",
                      text: `${project.name} / ${label}：仅解除关联，不删除外部资源。`,
                      command: {
                        kind: "resource.unbind",
                        input: { id: binding.id },
                      },
                    })
                  }
                >
                  解除绑定 {label}
                </button>
              </li>
            );
          })}
      </ul>
      {!snapshot.bindings.some((item) => item.project_id === project.id) && (
        <p className="muted">
          当前项目尚无资源绑定。展开下方操作可关联环境与工作空间资源。
        </p>
      )}
      <details className="catalog-disclosure">
        <summary>绑定到项目环境</summary>
        <p className="muted small">
          只记录本地关系，不授予权限。请勿在用途中输入秘密。
        </p>
        <CatalogForm
          title="绑定到项目环境"
          submit="绑定资源"
          disabled={disabled}
          onSubmit={async () => {
            if (
              validEnvironment &&
              validResource &&
              (await execute({
                kind: "resource.bind",
                input: {
                  project_id: project.id,
                  environment_id: validEnvironment,
                  resource_id: validResource,
                  purpose: purpose.trim(),
                },
              }))
            )
              setPurpose("");
          }}
        >
          <label>
            绑定环境
            <select
              required
              value={validEnvironment}
              onChange={(e) => setEnvironmentId(e.target.value)}
            >
              <option value="">选择当前项目环境</option>
              {environments.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name} · {item.tier}
                </option>
              ))}
            </select>
          </label>
          <label>
            绑定资源
            <select
              required
              value={validResource}
              onChange={(e) => setResourceId(e.target.value)}
            >
              <option value="">选择工作空间资源</option>
              {snapshot.resources.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            用途
            <input
              required
              maxLength={64}
              pattern="[a-z0-9][a-z0-9_\-]*"
              placeholder="例如 main-db"
              aria-describedby={purposeHelpId}
              value={purpose}
              onChange={(e) => setPurpose(e.target.value)}
            />
          </label>
          <p id={purposeHelpId} className="muted small">
            例如 main-db；1–64 个字符，仅小写字母、数字、下划线 _ 或连字符
            -，以小写字母或数字开头。
          </p>
        </CatalogForm>
        {!environments.length && (
          <p className="muted">请先在“环境”中登记当前项目环境。</p>
        )}
      </details>
      {confirmation && (
        <Confirm
          value={confirmation}
          disabled={disabled}
          execute={execute}
          close={() => setConfirmation(null)}
        />
      )}
    </section>
  );
}

export function MissionRelations({
  snapshot,
  project,
  missions,
  onOpenMission,
  disabled,
  execute,
}: Props & { missions: Mission[]; onOpenMission: (id: string) => void }) {
  const [missionId, setMissionId] = useState("");
  const [confirmation, setConfirmation] = useState<Confirmation | null>(null);
  const mission = missions.find((item) => item.id === missionId);
  const link = snapshot.mission_links.find(
    (item) => item.mission_id === missionId,
  );
  const source = snapshot.projects.find((item) => item.id === link?.project_id);
  const changing = !!link && link.project_id !== project.id;
  const projectMissionLinks = snapshot.mission_links.filter(
    (item) => item.project_id === project.id,
  );
  return (
    <section>
      <h2>项目任务</h2>
      <p className="muted">
        归类不改变任务内容、审批、执行范围或事件。执行仍在原交付任务工作区。
      </p>
      {!projectMissionLinks.length && (
        <p className="muted">
          当前项目尚无归类任务。展开下方操作可关联已有任务。
        </p>
      )}
      <ul className="catalog-list">
        {projectMissionLinks.map((projectLink) => {
          const item = missions.find(
            (mission) => mission.id === projectLink.mission_id,
          );
          const title =
            item?.title ?? `任务不可用 · ID：${projectLink.mission_id}`;
          return (
            <li key={projectLink.mission_id}>
              <div>
                <strong>{title}</strong>
                {item ? (
                  <>
                    <p className="muted small">
                      {stateNames[item.state] ?? `未知状态：${item.state}`} · r
                      {item.revision}
                    </p>
                    <time>{item.updated_at}</time>
                  </>
                ) : (
                  <p className="muted small">任务状态不可用</p>
                )}
              </div>
              <div className="actions">
                {item && (
                  <button onClick={() => onOpenMission(item.id)}>
                    打开任务 {item.title}
                  </button>
                )}
                <button
                  disabled={disabled}
                  onClick={() =>
                    setConfirmation({
                      label: "确认移出项目",
                      text: `${title}：从 ${project.name} 移出。只取消归类，不删除任务。`,
                      command: {
                        kind: "mission.unlink",
                        input: {
                          mission_id: projectLink.mission_id,
                          project_id: project.id,
                        },
                      },
                    })
                  }
                >
                  移出项目 {title}
                </button>
              </div>
            </li>
          );
        })}
      </ul>
      <details className="catalog-disclosure">
        <summary>归类已有任务</summary>
        <p className="muted small">
          归类只改变本地关系，不触发任务执行、审批或重试。
        </p>
        <CatalogForm
          title="归类已有任务"
          disabled={disabled}
          submit={changing ? "换到当前项目" : "归入当前项目"}
          onSubmit={async () => {
            if (!mission) return;
            if (changing && source)
              setConfirmation({
                label: "确认换项目",
                text: `${mission.title}：${source.name} → ${project.name}。只更改归类，不改变任务或审批。`,
                command: {
                  kind: "mission.relink",
                  input: {
                    mission_id: mission.id,
                    from_project_id: source.id,
                    to_project_id: project.id,
                  },
                },
              });
            else
              await execute({
                kind: "mission.link",
                input: { mission_id: mission.id, project_id: project.id },
              });
          }}
        >
          <label>
            已有交付任务
            <select
              required
              value={mission?.id ?? ""}
              onChange={(e) => setMissionId(e.target.value)}
            >
              <option value="">选择已有交付任务</option>
              {missions.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.title}
                </option>
              ))}
            </select>
          </label>
          {mission && (
            <div className="catalog-selection">
              <p>当前归类：{source?.name ?? "未归类"}</p>
              <button type="button" onClick={() => onOpenMission(mission.id)}>
                打开任务 {mission.title}
              </button>
            </div>
          )}
        </CatalogForm>
      </details>
      {confirmation && (
        <Confirm
          value={confirmation}
          disabled={disabled}
          execute={execute}
          close={() => setConfirmation(null)}
        />
      )}
    </section>
  );
}
