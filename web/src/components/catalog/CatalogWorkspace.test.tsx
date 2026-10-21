import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { CatalogWorkspace, type CatalogView } from "./CatalogWorkspace";
import { useCatalog } from "../../useCatalog";
import { response, mission } from "../../test/fixtures";
import type { CatalogSnapshot, CatalogCommand } from "../../catalogTypes";

const empty: CatalogSnapshot = {
  schema_version: 1,
  revision: 0,
  projects: [],
  targets: [],
  environments: [],
  resources: [],
  bindings: [],
  mission_links: [],
};
const meta = {
  revision: 1,
  created_at: "2026-10-18T00:00:00Z",
  updated_at: "2026-10-18T00:00:00Z",
};
const project = { ...meta, id: "p1", name: "项目 A", description: "初始说明" };
const other = { ...project, id: "p2", name: "项目 B" };
const base = { ...empty, projects: [project, other] };
function CatalogHarness({
  enabled,
  onOpenMission,
  onCreateFromRepository,
}: {
  enabled: boolean;
  onOpenMission: (id: string) => void;
  onCreateFromRepository?: (
    repository: import("../../capabilityTypes").GitRepository,
  ) => void;
}) {
  const catalog = useCatalog(enabled);
  const [selected, setSelected] = useState("");
  const [view, setView] = useState<CatalogView>("目标");
  return (
    <CatalogWorkspace
      enabled={enabled}
      missions={[mission]}
      onOpenMission={onOpenMission}
      onCreateFromRepository={onCreateFromRepository}
      catalog={catalog}
      selected={selected}
      onSelected={setSelected}
      view={view}
      onView={setView}
    />
  );
}
function setup(catalog = empty, reject = 0) {
  let current = catalog;
  const writes: (CatalogCommand & { expected_revision: number })[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string, init?: RequestInit) => {
      if (path === "/api/catalog/commands") {
        const cmd = JSON.parse(String(init?.body));
        writes.push(cmd);
        if (reject)
          return Promise.resolve(
            response(
              {
                error: {
                  code: "invalid",
                  message: reject === 409 ? "版本冲突" : "字段错误",
                },
              },
              reject,
            ),
          );
        const entity = { ...meta, ...cmd.input, id: "new" };
        if (cmd.kind === "project.create")
          current = { ...current, projects: [...current.projects, entity] };
        if (cmd.kind === "target.create")
          current = { ...current, targets: [...current.targets, entity] };
        if (cmd.kind === "environment.create")
          current = {
            ...current,
            environments: [...current.environments, entity],
          };
        if (cmd.kind === "resource.create")
          current = {
            ...current,
            resources: [
              ...current.resources,
              { ...entity, protected: true, retention: "retain" },
            ],
          };
        return Promise.resolve(response({ catalog: current }));
      }
      return Promise.resolve(
        response(
          path.startsWith("/api/catalog/events")
            ? { events: [] }
            : { catalog: current },
        ),
      );
    }),
  );
  const open = vi.fn();
  render(<CatalogHarness enabled onOpenMission={open} />);
  return {
    writes,
    open,
    replace: (next: CatalogSnapshot) => {
      current = next;
    },
  };
}
async function selectA() {
  fireEvent.click(await screen.findByRole("button", { name: "项目 A" }));
}

it("opens creation from project-linked registration without reading repository contents", async () => {
  const repo = {
    ...meta,
    id: "repo-1",
    name: "Registered source",
    path: "/tmp/source",
    project_id: "p1",
  };
  const requests: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      requests.push(path);
      return response(
        path === "/api/capabilities"
          ? {
              repositories: [
                repo,
                { ...repo, id: "other", project_id: "p2", name: "Other" },
              ],
            }
          : { catalog: base },
      );
    }),
  );
  const create = vi.fn();
  render(
    <CatalogHarness
      enabled
      onOpenMission={vi.fn()}
      onCreateFromRepository={create}
    />,
  );
  await selectA();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "从 Registered source 创建任务",
    }),
  );
  expect(create).toHaveBeenCalledWith(repo);
  expect(
    screen.queryByRole("button", { name: "从 Other 创建任务" }),
  ).not.toBeInTheDocument();
  expect(requests.filter((p) => p.includes("inspect"))).toEqual([]);
});

function toggleDisclosure(name: string) {
  fireEvent.click(screen.getByText(name, { selector: "summary" }));
}

it("shows one concise global catalog boundary without repeating it in the header", async () => {
  setup(base);
  const boundary = await screen.findByLabelText("台账边界");
  expect(boundary).toHaveTextContent(
    "本地元数据 · 未核验 · 不连接外部平台 · 请勿输入秘密",
  );
  const header = screen.getByRole("banner");
  expect(within(header).queryByText(/未连接外部平台/)).not.toBeInTheDocument();

  await selectA();
  expect(
    screen.getByRole("heading", { level: 1, name: "项目 A" }),
  ).toBeVisible();
  expect(screen.getByText("0 个目标")).toBeVisible();
});

it("keeps authenticated reload available while stale writes wait for a successful catalog read", async () => {
  let reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) => {
      if (path.startsWith("/api/catalog/events"))
        return Promise.resolve(response({ events: [] }));
      reads++;
      return reads === 2
        ? Promise.reject(new Error("catalog offline"))
        : Promise.resolve(response({ catalog: base }));
    }),
  );
  const open = vi.fn();
  const rendered = render(<CatalogHarness enabled onOpenMission={open} />);
  await selectA();
  toggleDisclosure("项目资料");
  fireEvent.change(screen.getByLabelText("项目说明"), {
    target: { value: "未保存说明" },
  });
  fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("catalog offline");
  expect(screen.getByRole("button", { name: "重新加载" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "保存项目" })).toBeDisabled();
  expect(screen.getByLabelText("项目说明")).toHaveValue("未保存说明");

  fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
  await waitFor(() => expect(reads).toBe(3));
  expect(screen.getByRole("button", { name: "保存项目" })).toBeEnabled();
  expect(screen.getByLabelText("项目说明")).toHaveValue("未保存说明");

  rendered.rerender(<CatalogHarness enabled={false} onOpenMission={open} />);
  expect(screen.getByRole("button", { name: "重新加载" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "保存项目" })).toBeDisabled();
});

it("points an empty resource list to the resource registration disclosure", async () => {
  setup(base);
  await selectA();
  fireEvent.click(screen.getByRole("button", { name: "资源" }));
  expect(
    screen.getByText("工作空间尚无资源。展开“登记工作空间资源”可添加资源。"),
  ).toBeVisible();
  expect(
    screen.getByText("登记工作空间资源", { selector: "summary" }),
  ).toBeVisible();
});

it("keeps catalog forms collapsed while existing project data and scoped counts stay visible", async () => {
  setup({
    ...base,
    targets: [
      {
        ...meta,
        id: "t1",
        project_id: "p1",
        name: "A 安装包",
        platform: "windows",
        format: "exe",
        architecture: "x64",
        distribution: "direct",
      },
      {
        ...meta,
        id: "t2",
        project_id: "p2",
        name: "B 安装包",
        platform: "macos",
        format: "dmg",
        architecture: "arm64",
        distribution: "direct",
      },
    ],
    environments: [
      { ...meta, id: "e1", project_id: "p1", name: "A dev", tier: "dev" },
    ],
    resources: [
      {
        ...meta,
        id: "r1",
        name: "A database",
        kind: "database",
        owner_project_id: "p1",
        provider: "manual",
        account: "",
        region: "",
        external_id: "",
        protected: true,
        retention: "retain",
      },
    ],
    bindings: [
      {
        ...meta,
        id: "b1",
        project_id: "p1",
        environment_id: "e1",
        resource_id: "r1",
        purpose: "db",
      },
    ],
    mission_links: [
      { mission_id: "m1", project_id: "p1", linked_at: meta.created_at },
    ],
  });

  expect(
    await screen.findByText("新建项目", { selector: "summary" }),
  ).toBeVisible();
  expect(screen.getByLabelText("项目名称")).not.toBeVisible();
  await selectA();
  expect(screen.getByText("1 个目标")).toBeVisible();
  expect(screen.getByText("1 个环境")).toBeVisible();
  expect(screen.getByText("1 个绑定")).toBeVisible();
  expect(screen.getByText("1 个任务关系")).toBeVisible();
  expect(screen.getByText("A 安装包")).toBeVisible();
  expect(screen.queryByText("B 安装包")).not.toBeInTheDocument();
  expect(screen.getByLabelText("目标名称")).not.toBeVisible();
  expect(screen.getByLabelText("项目名称")).not.toBeVisible();

  fireEvent.click(screen.getByRole("button", { name: "环境" }));
  expect(screen.getByText("A dev")).toBeVisible();
  expect(screen.getByLabelText("环境名称")).not.toBeVisible();

  fireEvent.click(screen.getByRole("button", { name: "资源" }));
  expect(
    within(screen.getByRole("region", { name: "已登记资源" })).getByText(
      "A database",
    ),
  ).toBeVisible();
  expect(screen.getByLabelText("资源名称")).not.toBeVisible();
  expect(screen.getByLabelText("用途")).not.toBeVisible();

  fireEvent.click(screen.getByRole("button", { name: "项目任务" }));
  expect(
    screen.getByRole("button", { name: "打开任务 改进导入校验" }),
  ).toBeVisible();
  expect(screen.getByLabelText("已有交付任务")).not.toBeVisible();
});

it("keeps a target draft mounted across disclosure toggles and explicit conflict refresh", async () => {
  const { writes } = setup(base, 409);
  await selectA();
  expect(screen.getByLabelText("目标名称")).not.toBeVisible();
  toggleDisclosure("登记交付目标");
  expect(screen.getByLabelText("目标名称")).toBeVisible();
  fireEvent.change(screen.getByLabelText("目标名称"), {
    target: { value: "草稿" },
  });
  toggleDisclosure("登记交付目标");
  toggleDisclosure("登记交付目标");
  expect(screen.getByLabelText("目标名称")).toHaveValue("草稿");
  fireEvent.click(screen.getByRole("button", { name: "登记目标" }));
  await screen.findByRole("alert");
  fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
  await waitFor(() =>
    expect(screen.queryByRole("alert")).not.toBeInTheDocument(),
  );
  expect(screen.getByLabelText("目标名称")).toHaveValue("草稿");
  expect(writes).toHaveLength(1);
});

it("creates a project without exposing execution controls", async () => {
  const { writes } = setup();
  expect(
    await screen.findByText("新建项目", { selector: "summary" }),
  ).toBeVisible();
  expect(screen.getByLabelText("台账边界")).toHaveTextContent(
    "本地元数据 · 未核验",
  );
  toggleDisclosure("新建项目");
  fireEvent.change(screen.getByLabelText("项目名称"), {
    target: { value: "新项目" },
  });
  fireEvent.change(screen.getByLabelText("项目说明"), {
    target: { value: "说明" },
  });
  fireEvent.click(screen.getByRole("button", { name: "新建项目" }));
  expect(await screen.findByRole("button", { name: "新项目" })).toBeVisible();
  expect(writes[0]).toEqual({
    expected_revision: 0,
    kind: "project.create",
    input: { name: "新项目", description: "说明" },
  });
  expect(
    screen.queryByRole("button", { name: /^(部署|执行迁移|下载|签名)$/ }),
  ).not.toBeInTheDocument();
});

it("normalizes target matrix changes and registers ipa metadata", async () => {
  const { writes } = setup(base);
  await selectA();
  toggleDisclosure("登记交付目标");
  fireEvent.change(screen.getByLabelText("目标名称"), {
    target: { value: "iOS 内测" },
  });
  fireEvent.change(screen.getByLabelText("目标平台"), {
    target: { value: "macos" },
  });
  fireEvent.change(screen.getByLabelText("目标架构"), {
    target: { value: "universal" },
  });
  fireEvent.change(screen.getByLabelText("目标平台"), {
    target: { value: "ios" },
  });
  expect(screen.getByLabelText("目标格式")).toHaveValue("ipa");
  expect(screen.getByLabelText("目标架构")).toHaveValue("arm64");
  fireEvent.change(screen.getByLabelText("目标分发"), {
    target: { value: "testflight" },
  });
  fireEvent.click(screen.getByRole("button", { name: "登记目标" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].input).toEqual({
    project_id: "p1",
    name: "iOS 内测",
    platform: "ios",
    format: "ipa",
    architecture: "arm64",
    distribution: "testflight",
  });
  expect(await screen.findByText("iOS 内测")).toBeVisible();
});

it.each([400, 409])(
  "preserves drafts and project selection on error %s and explicit refresh",
  async (status) => {
    const { writes } = setup(base, status);
    await selectA();
    toggleDisclosure("项目资料");
    fireEvent.change(screen.getByLabelText("项目名称"), {
      target: { value: "未保存名称" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存项目" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      status === 409 ? "版本冲突" : "字段错误",
    );
    expect(screen.getByLabelText("项目名称")).toHaveValue("未保存名称");
    if (status === 409)
      expect(screen.getByRole("button", { name: "保存项目" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
    await waitFor(() =>
      expect(screen.queryByRole("alert")).not.toBeInTheDocument(),
    );
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
      "项目 A",
    );
    expect(screen.getByLabelText("项目名称")).toHaveValue("未保存名称");
    expect(writes).toHaveLength(1);
  },
);

it("returns to the list when selected project disappears", async () => {
  const api = setup(base);
  await selectA();
  api.replace({ ...empty, revision: 0, projects: [other] });
  fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
  expect(
    await screen.findByRole("heading", { name: "项目资产" }),
  ).toBeVisible();
  expect(screen.getByRole("button", { name: "项目 B" })).toBeVisible();
});

it("registers an environment and clears provider identity when switching to manual", async () => {
  const { writes } = setup(base);
  await selectA();
  fireEvent.click(screen.getByRole("button", { name: "环境" }));
  toggleDisclosure("登记环境");
  fireEvent.change(screen.getByLabelText("环境名称"), {
    target: { value: "预发布" },
  });
  fireEvent.change(screen.getByLabelText("环境级别"), {
    target: { value: "staging" },
  });
  fireEvent.click(screen.getByRole("button", { name: "登记环境" }));
  expect(await screen.findByText("预发布")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "资源" }));
  toggleDisclosure("登记工作空间资源");
  fireEvent.change(screen.getByLabelText("资源名称"), {
    target: { value: "共享数据库" },
  });
  fireEvent.change(screen.getByLabelText("资源类型"), {
    target: { value: "database" },
  });
  fireEvent.change(screen.getByLabelText("资源提供方"), {
    target: { value: "aws" },
  });
  fireEvent.change(screen.getByLabelText("账号标识"), {
    target: { value: "account" },
  });
  fireEvent.change(screen.getByLabelText("资源提供方"), {
    target: { value: "manual" },
  });
  fireEvent.click(screen.getByRole("button", { name: "登记资源" }));
  expect(
    await within(screen.getByRole("region", { name: "已登记资源" })).findByText(
      "共享数据库",
    ),
  ).toBeVisible();
  expect(writes[1].input).toEqual({
    name: "共享数据库",
    kind: "database",
    provider: "manual",
    owner_project_id: null,
    account: "",
    region: "",
    external_id: "",
  });
});

it("offers other-owner resources, binds locally and confirms exact unbinding", async () => {
  const resource = {
    ...meta,
    id: "r1",
    name: "B 的数据库",
    kind: "database" as const,
    owner_project_id: "p2",
    provider: "manual" as const,
    account: "",
    region: "",
    external_id: "",
    protected: true as const,
    retention: "retain" as const,
  };
  const environment = {
    ...meta,
    id: "e1",
    project_id: "p1",
    name: "A staging",
    tier: "staging" as const,
  };
  const { writes } = setup({
    ...base,
    resources: [resource],
    environments: [environment],
    bindings: [
      {
        ...meta,
        id: "b1",
        project_id: "p1",
        environment_id: "e1",
        resource_id: "r1",
        purpose: "db",
      },
    ],
  });
  await selectA();
  fireEvent.click(screen.getByRole("button", { name: "资源" }));
  toggleDisclosure("绑定到项目环境");
  expect(screen.getByLabelText("绑定资源")).toHaveTextContent("B 的数据库");
  expect(screen.getByLabelText("用途")).toHaveAccessibleDescription(
    /main-db.*1–64.*小写字母.*数字/,
  );
  fireEvent.change(screen.getByLabelText("绑定环境"), {
    target: { value: "e1" },
  });
  fireEvent.change(screen.getByLabelText("绑定资源"), {
    target: { value: "r1" },
  });
  fireEvent.change(screen.getByLabelText("用途"), {
    target: { value: "cache" },
  });
  fireEvent.click(screen.getByRole("button", { name: "绑定资源" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].input).toEqual({
    project_id: "p1",
    environment_id: "e1",
    resource_id: "r1",
    purpose: "cache",
  });
  fireEvent.click(
    screen.getByRole("button", {
      name: "解除绑定 B 的数据库 / A staging / db",
    }),
  );
  expect(writes).toHaveLength(1);
  const confirmation = screen.getByRole("group", { name: "确认解除绑定" });
  expect(confirmation).toHaveTextContent("仅解除关联，不删除外部资源");
  fireEvent.click(
    within(confirmation).getByRole("button", { name: "确认解除绑定" }),
  );
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1]).toEqual({
    expected_revision: 0,
    kind: "resource.unbind",
    input: { id: "b1" },
  });
});

it("links existing missions, explicitly relinks/unlinks and opens the exact mission", async () => {
  const { writes, open } = setup({
    ...base,
    mission_links: [
      { mission_id: "m1", project_id: "p2", linked_at: meta.created_at },
    ],
  });
  await selectA();
  fireEvent.click(screen.getByRole("button", { name: "项目任务" }));
  toggleDisclosure("归类已有任务");
  fireEvent.change(screen.getByLabelText("已有交付任务"), {
    target: { value: "m1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "换到当前项目" }));
  expect(writes).toHaveLength(0);
  const confirmation = screen.getByRole("group", { name: "确认换项目" });
  expect(confirmation).toHaveTextContent("项目 B → 项目 A");
  fireEvent.click(
    within(confirmation).getByRole("button", { name: "确认换项目" }),
  );
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({
    expected_revision: 0,
    kind: "mission.relink",
    input: { mission_id: "m1", from_project_id: "p2", to_project_id: "p1" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "打开任务 改进导入校验" }),
  );
  expect(open).toHaveBeenCalledWith("m1");
});

it("shows linked mission facts and keeps a missing linked mission visibly unavailable", async () => {
  setup({
    ...base,
    mission_links: [
      { mission_id: "m1", project_id: "p1", linked_at: meta.created_at },
      { mission_id: "gone", project_id: "p1", linked_at: meta.created_at },
    ],
  });
  await selectA();
  fireEvent.click(screen.getByRole("button", { name: "项目任务" }));
  const current = screen
    .getByText("改进导入校验", { selector: "strong" })
    .closest("li")!;
  expect(current).toHaveTextContent("等待计划审批 · r9");
  expect(current).toHaveTextContent("2026-10-18T00:00:00Z");
  expect(screen.getByText("任务不可用 · ID：gone")).toBeVisible();
  expect(screen.getByText("任务状态不可用")).toBeVisible();
});

it("submits link and confirmed unlink without mission execution requests", async () => {
  const { writes, replace } = setup(base);
  await selectA();
  fireEvent.click(screen.getByRole("button", { name: "项目任务" }));
  toggleDisclosure("归类已有任务");
  fireEvent.change(screen.getByLabelText("已有交付任务"), {
    target: { value: "m1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "归入当前项目" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].kind).toBe("mission.link");
  replace({
    ...base,
    mission_links: [
      { mission_id: "m1", project_id: "p1", linked_at: meta.created_at },
    ],
  });
  fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "移出项目 改进导入校验" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "确认移出项目" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1]).toEqual({
    expected_revision: 0,
    kind: "mission.unlink",
    input: { mission_id: "m1", project_id: "p1" },
  });
});

it("renames registered entities without changing their immutable fields", async () => {
  const { writes } = setup({
    ...base,
    targets: [
      {
        ...meta,
        id: "t1",
        project_id: "p1",
        name: "旧目标",
        platform: "ios",
        format: "ipa",
        architecture: "arm64",
        distribution: "testflight",
      },
    ],
  });
  await selectA();
  fireEvent.click(screen.getByRole("button", { name: "重命名 旧目标" }));
  fireEvent.change(screen.getByLabelText("新名称 旧目标"), {
    target: { value: "新目标" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存名称" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({
    expected_revision: 0,
    kind: "target.rename",
    input: { id: "t1", name: "新目标" },
  });
});

it("uses labeled keyboard fields and does not submit an empty project name", async () => {
  const user = userEvent.setup();
  const { writes } = setup();
  await screen.findByText("新建项目", { selector: "summary" });
  toggleDisclosure("新建项目");
  const name = await screen.findByLabelText("项目名称");
  await user.click(screen.getByRole("button", { name: "新建项目" }));
  expect(writes).toEqual([]);
  name.focus();
  await user.type(name, "键盘项目");
  await user.tab();
  expect(screen.getByLabelText("项目说明")).toHaveFocus();
  await user.keyboard("键盘说明");
  await user.tab();
  await user.keyboard("{Enter}");
  expect(await screen.findByRole("button", { name: "键盘项目" })).toBeVisible();
  expect(writes[0].input).toEqual({
    name: "键盘项目",
    description: "键盘说明",
  });
});

it("rejects whitespace in non-manual identity fields before submitting", async () => {
  const user = userEvent.setup();
  const { writes } = setup(base);
  await selectA();
  fireEvent.click(screen.getByRole("button", { name: "资源" }));
  toggleDisclosure("登记工作空间资源");
  fireEvent.change(screen.getByLabelText("资源名称"), {
    target: { value: "资源" },
  });
  fireEvent.change(screen.getByLabelText("资源提供方"), {
    target: { value: "aws" },
  });
  fireEvent.change(screen.getByLabelText("账号标识"), {
    target: { value: "two words" },
  });
  fireEvent.change(screen.getByLabelText("区域标识"), {
    target: { value: "region" },
  });
  fireEvent.change(screen.getByLabelText("外部资源标识"), {
    target: { value: "external/id" },
  });
  await user.click(screen.getByRole("button", { name: "登记资源" }));
  expect(writes).toEqual([]);
  expect(screen.getByLabelText("账号标识")).toBeInvalid();
});

it("preserves target draft and view when a conflict is explicitly refreshed", async () => {
  const { writes } = setup(base, 409);
  await selectA();
  toggleDisclosure("登记交付目标");
  fireEvent.change(screen.getByLabelText("目标名称"), {
    target: { value: "保留目标" },
  });
  fireEvent.change(screen.getByLabelText("目标平台"), {
    target: { value: "ios" },
  });
  fireEvent.change(screen.getByLabelText("目标分发"), {
    target: { value: "ad_hoc" },
  });
  fireEvent.click(screen.getByRole("button", { name: "登记目标" }));
  await screen.findByRole("alert");
  fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
  await waitFor(() =>
    expect(screen.queryByRole("alert")).not.toBeInTheDocument(),
  );
  expect(screen.getByLabelText("目标名称")).toHaveValue("保留目标");
  expect(screen.getByLabelText("目标平台")).toHaveValue("ios");
  expect(screen.getByLabelText("目标分发")).toHaveValue("ad_hoc");
  expect(writes).toHaveLength(1);
});

it("updates project metadata and handles same-value saves without inventing history", async () => {
  const { writes } = setup(base);
  await selectA();
  toggleDisclosure("项目资料");
  fireEvent.change(screen.getByLabelText("项目说明"), {
    target: { value: "新说明" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存项目" }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({
    expected_revision: 0,
    kind: "project.update",
    input: { id: "p1", name: "项目 A", description: "新说明" },
  });
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "保存项目" })).not.toBeDisabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "保存项目" }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(screen.getByText("最近台账操作（0）")).toBeVisible();
});

it.each(["environment", "resource"] as const)(
  "renames %s with only id and name",
  async (kind) => {
    const environment = {
      ...meta,
      id: "e1",
      project_id: "p1",
      name: "环境旧名",
      tier: "dev" as const,
    };
    const resource = {
      ...meta,
      id: "r1",
      name: "资源旧名",
      kind: "database" as const,
      owner_project_id: null,
      provider: "manual" as const,
      account: "",
      region: "",
      external_id: "",
      protected: true as const,
      retention: "retain" as const,
    };
    const { writes } = setup({
      ...base,
      environments: [environment],
      resources: [resource],
    });
    await selectA();
    fireEvent.click(
      screen.getByRole("button", {
        name: kind === "environment" ? "环境" : "资源",
      }),
    );
    const name = kind === "environment" ? "环境旧名" : "资源旧名";
    fireEvent.click(screen.getByRole("button", { name: `重命名 ${name}` }));
    fireEvent.change(screen.getByLabelText(`新名称 ${name}`), {
      target: { value: "已改名" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存名称" }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual({
      expected_revision: 0,
      kind: `${kind}.rename`,
      input: { id: kind === "environment" ? "e1" : "r1", name: "已改名" },
    });
  },
);
