import { expect, it } from "vitest";
import type { CatalogSnapshot } from "./catalogTypes";
import { missionProjectContext } from "./projectContext";

const snapshot: CatalogSnapshot = {
  schema_version: 1,
  revision: 2,
  projects: [
    {
      id: "p1",
      name: "项目 A",
      description: "",
      revision: 1,
      created_at: "",
      updated_at: "",
    },
  ],
  targets: [],
  environments: [],
  resources: [],
  bindings: [],
  mission_links: [{ mission_id: "m1", project_id: "p1", linked_at: "" }],
};

it("distinguishes unread and unavailable catalog state from a known unlinked mission", () => {
  expect(
    missionProjectContext("m1", {
      snapshot: null,
      enabled: true,
      loading: true,
      error: "",
    }),
  ).toEqual({ state: "not_read", label: "项目归类尚未读取" });
  expect(
    missionProjectContext("m1", {
      snapshot: null,
      enabled: false,
      loading: false,
      error: "",
    }),
  ).toEqual({ state: "unavailable", label: "项目归类不可用" });
  expect(
    missionProjectContext("m1", {
      snapshot: null,
      enabled: true,
      loading: false,
      error: "catalog offline",
    }),
  ).toEqual({ state: "unavailable", label: "项目归类不可用" });
  expect(
    missionProjectContext("m2", {
      snapshot,
      enabled: true,
      loading: false,
      error: "",
    }),
  ).toEqual({ state: "unlinked", label: "未归类" });
});

it("resolves an actual project and exposes a dangling relation without inventing ownership", () => {
  expect(
    missionProjectContext("m1", {
      snapshot,
      enabled: true,
      loading: false,
      error: "",
    }),
  ).toEqual({
    state: "linked",
    label: "项目 A",
    projectId: "p1",
    projectName: "项目 A",
  });
  expect(
    missionProjectContext("missing-project", {
      snapshot: {
        ...snapshot,
        mission_links: [
          {
            mission_id: "missing-project",
            project_id: "gone",
            linked_at: "",
          },
        ],
      },
      enabled: true,
      loading: false,
      error: "",
    }),
  ).toEqual({
    state: "dangling",
    label: "项目关系无法解析",
    projectId: "gone",
  });
});

it.each([
  { loading: true, error: "" },
  { loading: false, error: "catalog offline" },
  { loading: false, error: "", enabled: false },
])(
  "marks retained linked data as a last snapshot for $loading/$error",
  (state) => {
    expect(
      missionProjectContext("m1", {
        snapshot,
        enabled: state.enabled ?? true,
        loading: state.loading,
        error: state.error,
      }),
    ).toEqual({
      state: "last_snapshot",
      label: "项目 A（最后快照）",
      projectId: "p1",
      projectName: "项目 A",
      relation: "linked",
    });
  },
);
