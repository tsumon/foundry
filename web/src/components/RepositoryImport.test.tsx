import { legacyReceipt, sealReceipt } from "../test/codingV2Fixtures";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { CreateMission } from "./CreateMission";
import { mission, profile, response } from "../test/fixtures";

const receipt = legacyReceipt({
  scope: ["src/"],
  protected: [],
  acceptance: ["test -f src/main.txt"],
  image: "local",
});

function setup(
  inspect = async () => response(receipt),
  createFails = false,
  repository?: import("../capabilityTypes").GitRepository,
  readOnly = false,
) {
  const requests: Array<{ path: string; body: any }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      if (init?.method === "POST")
        requests.push({ path, body: JSON.parse(String(init.body)) });
      if (path === "/api/profiles")
        return response({
          profiles: [profile],
          settings: { default_profile_id: "p1", role_bindings: {} },
        });
      if (path === "/api/health")
        return response({ capabilities: {}, read_only: readOnly });
      if (path === "/api/role-model-policy") return response({ state: {} });
      if (path === "/api/capabilities")
        return response({ repositories: repository ? [repository] : [] });
      if (path === "/api/repository-imports/operations")
        return response([
          {
            id: "interrupted",
            inspection_id: "old",
            mission_id: "reserved-only",
            status: "pending",
            stage: "reserve",
            code: "",
            revision: 1,
            created_at: "now",
            updated_at: "now",
            intent_digest: "d".repeat(64),
          },
        ]);
      if (path === "/api/repository-imports/inspections")
        return init?.method === "POST"
          ? repository
            ? response(
                sealReceipt({
                  ...receipt,
                  repository_id: repository.id,
                  repository_revision: repository.revision,
                }),
              )
            : inspect()
          : response([]);
      if (path === "/api/missions")
        return createFails
          ? response(
              { error: { code: "source_changed", message: "source changed" } },
              409,
            )
          : response({ mission });
      throw Error(path);
    }),
  );
  const created = vi.fn();
  render(
    <CreateMission
      initialRepository={repository}
      onCancel={() => {}}
      onCreated={created}
    />,
  );
  return { requests, created };
}
async function fill() {
  await screen.findByRole("option", { name: "测试连接 · fixture-model" });
  for (const [label, value] of [
    ["任务标题", "C3"],
    ["交付目标", "goal"],
    ["Git 仓库绝对路径", "/tmp/source"],
    ["允许修改范围（每行一项）", "src/"],
    ["验收命令（每行一条）", "test -f src/main.txt"],
    ["本地 Docker 镜像", "local"],
  ])
    fireEvent.change(screen.getByLabelText(label), { target: { value } });
}
it("requires explicit inspection and binds the exact receipt only on creation confirmation", async () => {
  const { requests, created } = setup();
  await fill();
  expect(requests).toEqual([]);
  const create = screen.getByRole("button", { name: "创建任务并检查上下文" });
  expect(create).toBeDisabled();
  fireEvent.click(screen.getByText("最近导入记录（只读）"));
  expect(await screen.findByText(/reserved-only/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
  await screen.findByText("可创建任务");
  expect(created).not.toHaveBeenCalled();
  expect(requests[0]).toEqual({
    path: "/api/repository-imports/inspections",
    body: { repo_path: "/tmp/source", spec: receipt.spec, consent: true },
  });
  fireEvent.click(create);
  await waitFor(() => expect(created).toHaveBeenCalledWith(mission));
  expect(requests[1].body).toMatchObject({
    import_version: 1,
    import_selection: { inspection_id: "inspection-1", digest: receipt.digest },
  });
});
it.each([
  "Git 仓库绝对路径",
  "允许修改范围（每行一项）",
  "受保护文件（每行一项）",
  "验收命令（每行一条）",
  "本地 Docker 镜像",
])("invalidates admission immediately after changing %s", async (label) => {
  setup();
  await fill();
  fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
  await screen.findByText("可创建任务");
  fireEvent.change(screen.getByLabelText(label), {
    target: { value: "/changed" },
  });
  expect(
    screen.getByRole("button", { name: "创建任务并检查上下文" }),
  ).toBeDisabled();
  expect(screen.queryByText("可创建任务")).not.toBeInTheDocument();
});
it("never restores a late receipt after inputs change away and back or mode changes", async () => {
  let resolve!: (r: Response) => void;
  setup(
    () =>
      new Promise((r) => {
        resolve = r;
      }),
  );
  await fill();
  fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
  fireEvent.change(screen.getByLabelText("本地 Docker 镜像"), {
    target: { value: "other" },
  });
  fireEvent.change(screen.getByLabelText("本地 Docker 镜像"), {
    target: { value: "local" },
  });
  await act(async () => resolve(response(receipt)));
  expect(
    screen.getByRole("button", { name: "创建任务并检查上下文" }),
  ).toBeDisabled();
  fireEvent.click(screen.getByLabelText(/启用可恢复候选/));
  expect(screen.getByText(/未绑定 C3/)).toBeVisible();
  fireEvent.click(screen.getByLabelText(/启用可恢复候选/));
  expect(
    screen.getByRole("button", { name: "创建任务并检查上下文" }),
  ).toBeDisabled();
});
it("clears readiness after a refused creation and never retries it automatically", async () => {
  const { requests } = setup(undefined, true);
  await fill();
  fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
  await screen.findByText("可创建任务");
  fireEvent.click(screen.getByRole("button", { name: "创建任务并检查上下文" }));
  await screen.findByText("source changed");
  expect(
    screen.getByRole("button", { name: "创建任务并检查上下文" }),
  ).toBeDisabled();
  expect(screen.queryByText("可创建任务")).not.toBeInTheDocument();
  expect(requests.map((r) => r.path)).toEqual([
    "/api/repository-imports/inspections",
    "/api/missions",
  ]);
});
it("does not turn a failed inspection request into readiness", async () => {
  const { requests } = setup(async () =>
    response({ error: { message: "inspection unavailable" } }, 500),
  );
  await fill();
  fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
  await screen.findByText("inspection unavailable");
  expect(
    screen.getByRole("button", { name: "创建任务并检查上下文" }),
  ).toBeDisabled();
  expect(requests).toHaveLength(1);
});
it("keeps import mutations disabled under global read-only health", async () => {
  const { requests } = setup(undefined, false, undefined, true);
  await fill();
  expect(screen.getByRole("button", { name: "检查可导入性" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
  expect(
    screen.getByRole("button", { name: "创建任务并检查上下文" }),
  ).toBeDisabled();
  expect(requests).toEqual([]);
});
it("never labels an unsupported inspection schema ready", async () => {
  setup(async () => response({ ...receipt, schema_version: 2 }));
  await fill();
  fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
  await screen.findByRole("alert");
  expect(screen.queryByText("可创建任务")).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "创建任务并检查上下文" }),
  ).toBeDisabled();
});
it("prefills registered project identity but requires fresh consent and excludes ambiguous path input", async () => {
  const repository = {
    id: "registered",
    revision: 3,
    path: "/tmp/source",
    project_id: "project",
    name: "Project source",
    created_at: "now",
    updated_at: "now",
  };
  const { requests } = setup(undefined, false, repository);
  await screen.findByRole("option", { name: "Project source · 登记 v3" });
  for (const [label, value] of [
    ["允许修改范围（每行一项）", "src/"],
    ["验收命令（每行一条）", "test -f src/main.txt"],
    ["本地 Docker 镜像", "local"],
  ])
    fireEvent.change(screen.getByLabelText(label), { target: { value } });
  expect(screen.getByLabelText("Git 仓库绝对路径")).toHaveValue("/tmp/source");
  expect(
    screen.getByRole("button", { name: "创建任务并检查上下文" }),
  ).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
  await screen.findByText("可创建任务");
  expect(requests[0].body).toMatchObject({
    repository_id: "registered",
    repository_revision: 3,
    consent: true,
  });
  expect(requests[0].body).not.toHaveProperty("repo_path");
});
