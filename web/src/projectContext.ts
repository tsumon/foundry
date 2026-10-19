import type { CatalogSnapshot } from "./catalogTypes";

export interface CatalogReadState {
  snapshot: CatalogSnapshot | null;
  enabled: boolean;
  loading: boolean;
  error: string;
}

export type MissionProjectContext =
  | { state: "not_read" | "unavailable" | "unlinked"; label: string }
  | { state: "dangling"; label: string; projectId: string }
  | {
      state: "linked";
      label: string;
      projectId: string;
      projectName: string;
    }
  | {
      state: "last_snapshot";
      label: string;
      relation: "linked" | "unlinked" | "dangling";
      projectId?: string;
      projectName?: string;
    };

export function missionProjectContext(
  missionId: string,
  catalog: CatalogReadState,
): MissionProjectContext {
  if (!catalog.snapshot) {
    if (!catalog.enabled || catalog.error)
      return { state: "unavailable", label: "项目归类不可用" };
    return { state: "not_read", label: "项目归类尚未读取" };
  }
  const link = catalog.snapshot.mission_links.find(
    (item) => item.mission_id === missionId,
  );
  const project = link
    ? catalog.snapshot.projects.find((item) => item.id === link.project_id)
    : undefined;
  const stale = !catalog.enabled || catalog.loading || !!catalog.error;
  if (stale) {
    if (!link)
      return {
        state: "last_snapshot",
        label: "最后快照：未归类",
        relation: "unlinked",
      };
    if (!project)
      return {
        state: "last_snapshot",
        label: "最后快照：项目关系无法解析",
        relation: "dangling",
        projectId: link.project_id,
      };
    return {
      state: "last_snapshot",
      label: `${project.name}（最后快照）`,
      relation: "linked",
      projectId: project.id,
      projectName: project.name,
    };
  }
  if (!link) return { state: "unlinked", label: "未归类" };
  if (!project)
    return {
      state: "dangling",
      label: "项目关系无法解析",
      projectId: link.project_id,
    };
  return {
    state: "linked",
    label: project.name,
    projectId: project.id,
    projectName: project.name,
  };
}
