import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { mission, profile, response } from "../test/fixtures";
import { IdeaCreate } from "./IdeaCreate";

function setup(available = true, createFailure = false) {
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile],
        settings: { default_profile_id: "p1", role_bindings: {} },
      });
    if (path === "/api/idea-tasks" && init?.method === "POST") {
      if (createFailure)
        return response(
          { error: { code: "unavailable", message: "模型配置已改变" } },
          409,
        );
      return response({ mission });
    }
    return response({
      available,
      reason: available ? "" : "embedded_vm_v2_bundle_unavailable",
    });
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

it("creates from an idea without a repository, and creation does not approve any execution", async () => {
  const fetcher = setup();
  const created = vi.fn();
  render(<IdeaCreate onCancel={() => {}} onCreated={created} />);
  await screen.findByRole("option", { name: /测试连接/ });
  expect(screen.queryByLabelText(/仓库路径/)).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("应用名称"), {
    target: { value: "习惯打卡" },
  });
  fireEvent.change(screen.getByLabelText("描述你的想法"), {
    target: { value: "添加习惯并记录每日完成状态" },
  });
  fireEvent.change(screen.getByLabelText("怎样算做好了"), {
    target: { value: "可以添加习惯\n完成后更新统计" },
  });
  fireEvent.change(screen.getByLabelText("调用上限"), {
    target: { value: "12" },
  });
  fireEvent.click(screen.getByRole("button", { name: "创建应用任务" }));
  await waitFor(() => expect(created).toHaveBeenCalledWith(mission));
  const writes = fetcher.mock.calls.filter(
    ([, init]) => init?.method === "POST",
  );
  expect(writes).toHaveLength(1);
  expect(writes[0][0]).toBe("/api/idea-tasks");
  expect(JSON.parse(writes[0][1]!.body as string)).toEqual({
    title: "习惯打卡",
    goal: "添加习惯并记录每日完成状态",
    acceptance: ["可以添加习惯", "完成后更新统计"],
    profile_id: "p1",
    max_calls: 12,
  });
});

it("keeps the brief after a creation error without automatic retry", async () => {
  const fetcher = setup(true, true);
  render(<IdeaCreate onCancel={() => {}} onCreated={() => {}} />);
  await screen.findByRole("option", { name: /测试连接/ });
  fireEvent.change(screen.getByLabelText("应用名称"), {
    target: { value: "预算工具" },
  });
  fireEvent.change(screen.getByLabelText("描述你的想法"), {
    target: { value: "记录开销" },
  });
  fireEvent.change(screen.getByLabelText("怎样算做好了"), {
    target: { value: "显示总额" },
  });
  fireEvent.click(screen.getByRole("button", { name: "创建应用任务" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("模型配置已改变");
  expect(screen.getByLabelText("描述你的想法")).toHaveValue("记录开销");
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
});

it("does not offer host fallback when the installed VM is unavailable", async () => {
  setup(false);
  render(<IdeaCreate onCancel={() => {}} onCreated={() => {}} />);
  expect(await screen.findByRole("status")).toHaveTextContent(
    "内置应用运行时不可用",
  );
  expect(screen.getByRole("button", { name: "创建应用任务" })).toBeDisabled();
});

it("populates starter templates locally, warns on low call ceilings, and links to model settings", async () => {
  const fetcher = vi.fn(async (path: string, _init?: RequestInit) => {
    if (path === "/api/profiles")
      return response({
        profiles: [],
        settings: { default_profile_id: "", role_bindings: {} },
      });
    return response({ available: true, reason: "" });
  });
  vi.stubGlobal("fetch", fetcher);
  const openSettings = vi.fn();
  render(
    <IdeaCreate
      onCancel={() => {}}
      onCreated={() => {}}
      onOpenSettings={openSettings}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "前往配置模型" }),
  );
  expect(openSettings).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "报价利润计算器" }));
  expect(screen.getByLabelText("应用名称")).toHaveValue("项目报价与利润计算器");
  expect(screen.getByLabelText("怎样算做好了")).not.toHaveValue("");
  fireEvent.change(screen.getByLabelText("调用上限"), {
    target: { value: "3" },
  });
  expect(screen.getByText(/通常需要 6～12 次调用/)).toBeVisible();
  expect(
    fetcher.mock.calls.some(([, init]) => init?.method === "POST"),
  ).toBe(false);
});

it("submits follow-up iterations to /idea-continue while pre-filling title and acceptance", async () => {
  const v1 = {
    ...mission,
    id: "m-v1",
    title: "客户看板",
    candidate_digest: "abcdef0123456789",
    idea_project: {
      schema_version: 2 as const,
      template: "browser_app_v1" as const,
      acceptance: ["可以添加客户", "可以筛选"],
    },
  };
  const v2 = { ...v1, id: "m-v2", title: "客户看板 v2" };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile],
        settings: { default_profile_id: "p1", role_bindings: {} },
      });
    if (path === "/api/missions/m-v1/idea-continue" && init?.method === "POST")
      return response({ mission: v2 });
    return response({ available: true, reason: "" });
  });
  vi.stubGlobal("fetch", fetcher);
  const created = vi.fn();
  render(
    <IdeaCreate
      continueFrom={v1}
      onCancel={() => {}}
      onCreated={created}
    />,
  );
  await screen.findByRole("option", { name: /测试连接/ });
  expect(screen.getByLabelText("应用名称")).toHaveValue("客户看板 v2");
  expect(screen.getByLabelText("怎样算做好了")).toHaveValue(
    "可以添加客户\n可以筛选",
  );
  fireEvent.change(screen.getByLabelText("描述你的想法"), {
    target: { value: "增加导出 JSON 按钮" },
  });
  fireEvent.click(screen.getByRole("button", { name: "创建改进任务" }));
  await waitFor(() => expect(created).toHaveBeenCalledWith(v2));
});
