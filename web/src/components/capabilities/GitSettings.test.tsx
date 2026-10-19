import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it } from "vitest";
import userEvent from "@testing-library/user-event";
import { CapabilitySettings } from "./CapabilitySettings";
import {
  catalogState,
  deferred,
  emptyCapabilities,
  mockServer,
  observation,
  repository,
} from "./testFixtures";
import { response } from "../../test/fixtures";

it("moves keyboard focus to read-only confirmation review and restores its exact trigger on cancel without inspection", async () => {
  const user = userEvent.setup();
  const fetcher = mockServer({
    ...emptyCapabilities,
    repositories: [
      repository,
      { ...repository, id: "git-2", name: "第二个 Git" },
    ],
  });
  render(<CapabilitySettings enabled catalog={catalogState} />);
  fireEvent.click(screen.getByRole("button", { name: "Git" }));
  const trigger = await screen.findByRole("button", {
    name: "检查仓库身份 第二个 Git",
  });
  trigger.focus();
  await user.keyboard("{Enter}");
  expect(
    screen.getByRole("heading", { name: "确认 Git 只读检查" }),
  ).toHaveFocus();
  await user.keyboard("{Enter}");
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
  await user.tab();
  expect(screen.getByRole("button", { name: "确认只读检查" })).toHaveFocus();
  await user.tab();
  expect(screen.getByRole("button", { name: "取消检查" })).toHaveFocus();
  await user.keyboard("{Enter}");
  expect(
    screen.queryByRole("region", { name: "确认 Git 只读检查" }),
  ).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
});

it("registers only metadata, reuses catalog availability and never calls inspect on save", async () => {
  const fetcher = mockServer(emptyCapabilities, (path) =>
    path === "/api/capabilities/git" ? response({ repository }) : undefined,
  );
  render(
    <CapabilitySettings
      enabled
      catalog={{ ...catalogState, error: "catalog unavailable" }}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Git" }));
  await screen.findByRole("button", { name: "登记 Git 仓库" });
  expect(screen.getByText(/项目列表读取失败/)).toBeVisible();
  expect(screen.getByLabelText("关联项目（可选）")).toBeDisabled();
  fireEvent.change(screen.getByLabelText("Git 仓库名称"), {
    target: { value: "临时仓库" },
  });
  fireEvent.change(screen.getByLabelText("Git 仓库路径"), {
    target: { value: "/temporary/fixture" },
  });
  fireEvent.click(screen.getByRole("button", { name: "登记 Git 仓库" }));
  expect(
    await screen.findByRole("button", { name: "检查仓库身份 临时仓库" }),
  ).toBeVisible();
  expect(
    fetcher.mock.calls
      .filter(([, init]) => init?.method === "POST")
      .map(([path, init]) => [path, JSON.parse(init!.body as string)]),
  ).toEqual([
    [
      "/api/capabilities/git",
      {
        expected_revision: 0,
        name: "临时仓库",
        path: "/temporary/fixture",
        project_id: "",
      },
    ],
  ]);
  expect(fetcher.mock.calls.map(([path]) => path)).not.toContain(
    "/api/catalog",
  );
});

it("shows stale identity and always reports working tree not checked; inspection requires explicit consent", async () => {
  const fetcher = mockServer(
    {
      ...emptyCapabilities,
      repositories: [repository],
      git_observations: [observation],
    },
    (path) =>
      path.endsWith("/inspect")
        ? response({
            observation: {
              ...observation,
              repository_revision: 2,
              unborn: true,
              head: "",
            },
          })
        : undefined,
  );
  render(<CapabilitySettings enabled catalog={catalogState} />);
  fireEvent.click(screen.getByRole("button", { name: "Git" }));
  expect(await screen.findByText(/旧配置观察/)).toBeVisible();
  expect(screen.getByText(/工作区改动未检查/)).toBeVisible();
  expect(screen.getByText(/not_checked 不是工作区干净声明/)).toBeVisible();
  expect(screen.queryByText(/^工作区干净$|无改动/)).not.toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "检查仓库身份 临时仓库" }),
  );
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "确认只读检查" }));
  expect(await screen.findByText(/尚无提交（unborn）/)).toBeVisible();
  const post = fetcher.mock.calls.find(([path]) => path.endsWith("/inspect"))!;
  expect(JSON.parse(post[1]!.body as string)).toEqual({
    expected_revision: 2,
    consent: true,
  });
});

it("does not overwrite repository drafts on conflict or falsely show a failed inspection as success", async () => {
  mockServer(
    {
      ...emptyCapabilities,
      repositories: [repository],
      git_observations: [
        {
          ...observation,
          status: "failed",
          root: "",
          branch: "",
          head: "",
          error_code: "unsupported_layout",
        },
      ],
    },
    (path) =>
      path === "/api/capabilities/git"
        ? response({ error: { code: "conflict" } }, 409)
        : undefined,
  );
  render(<CapabilitySettings enabled catalog={catalogState} />);
  fireEvent.click(screen.getByRole("button", { name: "Git" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "编辑 Git 临时仓库" }),
  );
  fireEvent.change(screen.getByLabelText("Git 仓库名称"), {
    target: { value: "保留 Git 草稿" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存 Git 登记" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("草稿已保留");
  expect(screen.getByLabelText("Git 仓库名称")).toHaveValue("保留 Git 草稿");
  expect(screen.getByText(/检查失败/)).toBeVisible();
  expect(screen.getByText(/unsupported_layout/)).toBeVisible();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "保存 Git 登记" })).toBeEnabled(),
  );
});

it("locks duplicate Git saves and preserves an unavailable existing project association", async () => {
  const pending = deferred<Response>();
  const existing = { ...repository, project_id: "project-retained" };
  const fetcher = mockServer(
    { ...emptyCapabilities, repositories: [existing] },
    (path) => (path === "/api/capabilities/git" ? pending.promise : undefined),
  );
  render(
    <CapabilitySettings
      enabled
      catalog={{ ...catalogState, error: "unavailable" }}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Git" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "编辑 Git 临时仓库" }),
  );
  const save = screen.getByRole("button", { name: "保存 Git 登记" });
  fireEvent.click(save);
  fireEvent.click(save);
  expect(save).toBeDisabled();
  expect(screen.getByLabelText("关联项目（可选）")).toHaveValue(
    "project-retained",
  );
  const posts = fetcher.mock.calls.filter(
    ([path]) => path === "/api/capabilities/git",
  );
  expect(posts).toHaveLength(1);
  expect(JSON.parse(posts[0][1]!.body as string)).toEqual({
    id: "git-1",
    expected_revision: 2,
    name: "临时仓库",
    path: "/temporary/fixture",
    project_id: "project-retained",
  });
  await act(async () => pending.resolve(response({ repository: existing })));
  expect(save).toBeEnabled();
});

it("keeps a newer observed identity when an older inspect response arrives after refresh", async () => {
  const inspected = deferred<Response>();
  let reads = 0;
  mockServer({ ...emptyCapabilities, repositories: [repository] }, (path) => {
    if (path === "/api/capabilities" && ++reads > 1)
      return response({
        ...emptyCapabilities,
        repositories: [repository],
        git_observations: [
          {
            ...observation,
            checked_at: "2026-10-18T05:00:00Z",
            branch: "newer-branch",
          },
        ],
      });
    if (path.endsWith("/inspect")) return inspected.promise;
  });
  render(<CapabilitySettings enabled catalog={catalogState} />);
  fireEvent.click(screen.getByRole("button", { name: "Git" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "检查仓库身份 临时仓库" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "确认只读检查" }));
  fireEvent.click(screen.getByRole("button", { name: "重新读取能力设置" }));
  expect(await screen.findByText("newer-branch")).toBeVisible();
  await act(async () => inspected.resolve(response({ observation })));
  expect(screen.getByText("newer-branch")).toBeVisible();
});
