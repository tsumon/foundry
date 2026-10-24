import {
  act,
  fireEvent,
  render,
  screen,
  within,
  waitFor,
} from "@testing-library/react";
import { it, expect, vi } from "vitest";
import App from "./App";
import { mission, profile, response } from "./test/fixtures";
import { mergeMission } from "./useWorkbench";
import {
  deferred,
  emptyCapabilities,
} from "./components/capabilities/testFixtures";

it.each(["invalid_mission_model_snapshot", "c4_runtime_mismatch"])(
  "keeps history usable with a global %s read-only notice across navigation",
  async (recoveryError) => {
    const fetcher = vi.fn(async (path: string, _init?: RequestInit) => {
      if (path === "/api/health")
        return response({
          status: "recovery_limited",
          read_only: true,
          recovery_error: recoveryError,
          capabilities: {},
        });
      if (path === "/api/missions") return response({ missions: [mission] });
      return response({});
    });
    vi.stubGlobal("fetch", fetcher);
    render(<App />);
    const notice = await screen.findByRole("alert", {
      name: "恢复受限：只读诊断",
    });
    expect(notice).toHaveTextContent("历史仍可查看");
    expect(notice).toHaveTextContent("所有业务变更、模型和工具调用已禁止");
    if (recoveryError === "c4_runtime_mismatch") {
      expect(notice).not.toHaveTextContent("任务模型快照损坏");
      expect(notice).toHaveTextContent("原基线、证据和已用预算保留");
    }
    fireEvent.click(
      await screen.findByRole("button", { name: /改进导入校验/ }),
    );
    expect(
      screen.getByRole("alert", { name: "恢复受限：只读诊断" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /改进导入校验/ }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "交付记录" }));
    expect(
      screen.getByRole("alert", { name: "恢复受限：只读诊断" }),
    ).toBeInTheDocument();
    expect(
      fetcher.mock.calls.filter(([path]) => path === "/api/health"),
    ).toHaveLength(1);
    expect(
      fetcher.mock.calls.filter(
        ([path, init]) =>
          path !== "/api/session" && init?.method && init.method !== "GET",
      ),
    ).toEqual([]);
  },
);

it("does not show a diagnostic notice for normal startup", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) =>
      response(
        path === "/api/health"
          ? { status: "ok", capabilities: {} }
          : path === "/api/missions"
            ? { missions: [mission] }
            : {},
      ),
    ),
  );
  render(<App />);
  await screen.findByRole("button", { name: /改进导入校验/ });
  expect(
    screen.queryByRole("alert", { name: "恢复受限：只读诊断" }),
  ).not.toBeInTheDocument();
});

it("gates capability settings on the one App session and reuses its existing project catalog", async () => {
  const session = deferred<Response>();
  const catalog = {
    schema_version: 1,
    revision: 0,
    projects: [
      {
        id: "project-r2",
        revision: 1,
        name: "已有项目 R2",
        description: "",
        created_at: "",
        updated_at: "",
      },
    ],
    targets: [],
    environments: [],
    resources: [],
    bindings: [],
    mission_links: [],
  };
  const fetcher = vi.fn(async (path: string) => {
    if (path === "/api/session") return session.promise;
    if (path === "/api/missions") return response({ missions: [] });
    if (path === "/api/catalog") return response({ catalog });
    if (path === "/api/capabilities") return response(emptyCapabilities);
    throw new Error(`Unexpected test request: ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<App />);
  fireEvent.click(screen.getByRole("button", { name: "设置" }));
  fireEvent.click(screen.getByRole("button", { name: "Git" }));
  expect(fetcher.mock.calls.map(([path]) => path)).toEqual(["/api/session"]);
  await act(async () => session.resolve(response({})));
  expect(
    await screen.findByRole("option", { name: "已有项目 R2" }),
  ).toBeInTheDocument();
  expect(screen.getByLabelText("关联项目（可选）")).toBeEnabled();
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/api/session"),
  ).toHaveLength(1);
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/api/catalog"),
  ).toHaveLength(1);
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/api/capabilities"),
  ).toHaveLength(1);
});

it("retains per-mission plan drafts and approval locks across settings, back, project and other mission navigation", async () => {
  const other = { ...mission, id: "m2", title: "第二个任务" };
  const pendingCatalog = deferred<Response>();
  const catalog = {
    schema_version: 1,
    revision: 0,
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
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/api/catalog") return pendingCatalog.promise;
      return response(
        path === "/api/missions" ? { missions: [mission, other] } : {},
      );
    }),
  );
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: /改进导入校验/ }));
  fireEvent.click(
    screen.getByRole("button", { name: "修订计划（保存不审批）" }),
  );
  fireEvent.change(screen.getByLabelText("计划摘要"), {
    target: { value: "保留跨导航草稿" },
  });
  fireEvent.change(screen.getByLabelText("修订原因"), {
    target: { value: "保留原因" },
  });
  const reopen = () => {
    fireEvent.click(
      screen.getByRole("button", { name: /改进导入校验等待计划审批/ }),
    );
    const toggle = screen.getByRole("button", {
      name: "修订计划（保存不审批）",
    });
    if (toggle.getAttribute("aria-expanded") === "false")
      fireEvent.click(toggle);
    expect(screen.getByLabelText("计划摘要")).toHaveValue("保留跨导航草稿");
    expect(screen.getByLabelText("修订原因")).toHaveValue("保留原因");
    expect(
      screen.getByRole("button", { name: "批准并执行计划" }),
    ).toBeDisabled();
  };
  fireEvent.click(screen.getByRole("button", { name: "设置" }));
  reopen();
  fireEvent.click(screen.getByRole("button", { name: "返回交付任务" }));
  reopen();
  // Catalog loading is independent of the mission list. Release it only now
  // so project navigation must wait for the actual linked-project button.
  pendingCatalog.resolve(response({ catalog }));
  fireEvent.click(
    await screen.findByRole("button", { name: "打开项目 项目 A" }),
  );
  reopen();
  fireEvent.click(screen.getByRole("button", { name: /第二个任务/ }));
  expect(screen.getByRole("button", { name: "批准并执行计划" })).toBeEnabled();
  reopen();
});

it.each(["success", "failure", "conflict"])(
  "keeps an in-flight revision locked through navigation and resolves %s into its original mission",
  async (outcome) => {
    let finish!: (value: Response) => void;
    const other = { ...mission, id: "m2", title: "第二个任务" };
    const updated = {
      ...mission,
      revision: 10,
      plan: { ...mission.plan!, revision: 4, summary: "保存后正文" },
    };
    const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
      if (path.endsWith("/plan-revisions") && init?.method === "POST")
        return new Promise<Response>((resolve) => {
          finish = resolve;
        });
      return response(
        path === "/api/missions"
          ? { missions: [mission, other] }
          : path === "/api/missions/m1"
            ? { mission: updated }
            : {},
      );
    });
    vi.stubGlobal("fetch", fetcher);
    render(<App />);
    fireEvent.click(
      await screen.findByRole("button", { name: /改进导入校验/ }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "修订计划（保存不审批）" }),
    );
    fireEvent.change(screen.getByLabelText("计划摘要"), {
      target: { value: "保存后正文" },
    });
    fireEvent.change(screen.getByLabelText("修订原因"), {
      target: { value: "修订原因保留" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存修订（不审批）" }));
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    fireEvent.click(screen.getByRole("button", { name: /改进导入校验/ }));
    const toggle = screen.getByRole("button", {
      name: "修订计划（保存不审批）",
    });
    if (toggle.getAttribute("aria-expanded") === "false")
      fireEvent.click(toggle);
    expect(
      screen.getByRole("button", { name: "批准并执行计划" }),
    ).toBeDisabled();
    expect(screen.getByLabelText("计划摘要")).toHaveAttribute("readonly");
    fireEvent.click(screen.getByRole("button", { name: "保存修订（不审批）" }));
    expect(
      fetcher.mock.calls.filter(
        ([path, init]) =>
          path.endsWith("/plan-revisions") && init?.method === "POST",
      ),
    ).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: /第二个任务/ }));
    await act(async () =>
      finish(
        outcome === "success"
          ? response({
              mission: updated,
              plan_revision: { epoch: 1, plan_revision: 4, plan: updated.plan },
            })
          : response(
              { error: { message: "保存被拒绝" } },
              outcome === "conflict" ? 409 : 400,
            ),
      ),
    );
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
      "第二个任务",
    );
    expect(
      screen.getByRole("button", { name: "批准并执行计划" }),
    ).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: /改进导入校验/ }));
    const after = screen.getByRole("button", {
      name: "修订计划（保存不审批）",
    });
    if (after.getAttribute("aria-expanded") === "false") fireEvent.click(after);
    expect(screen.getByLabelText("计划摘要")).toHaveValue("保存后正文");
    if (outcome === "success") {
      expect(screen.getByLabelText("修订原因")).toHaveValue("");
      expect(
        screen.getByRole("button", { name: "批准并执行计划" }),
      ).toBeEnabled();
    } else {
      expect(screen.getByLabelText("修订原因")).toHaveValue("修订原因保留");
      expect(
        screen.getByRole("button", { name: "批准并执行计划" }),
      ).toBeDisabled();
      expect(
        screen.getByText(
          outcome === "conflict" ? /版本冲突：草稿已保留/ : "保存被拒绝",
        ),
      ).toBeVisible();
    }
  },
);

const appInspection = {
  schema_version: 1,
  id: "app-receipt",
  digest: "c".repeat(64),
  root: "/tmp/new-mission",
  branch: "main",
  commit: "a".repeat(40),
  spec: {
    scope: ["src/"],
    protected: [],
    acceptance: ["/opt/foundry/verify"],
    image: "fixture:local",
  },
  policy_version: "committed_text_v1",
  status: "ready",
  stage: "complete",
  code: "",
  checked_at: "now",
  working_tree_status: "not_checked",
  file_count: 1,
  total_bytes: 10,
  context_file_count: 1,
  context_bytes: 10,
  context_manifest: [],
  protected_inputs: [],
};
async function submitNewMission(prefilled = false) {
  await screen.findByRole("option", { name: "测试连接 · fixture-model" });
  fireEvent.change(screen.getByLabelText("任务标题"), {
    target: { value: "新建任务" },
  });
  fireEvent.change(screen.getByLabelText("交付目标"), {
    target: { value: "验证创建后的返回位置" },
  });
  if (!prefilled)
    fireEvent.change(screen.getByLabelText("Git 仓库绝对路径"), {
      target: { value: "/tmp/new-mission" },
    });
  fireEvent.change(screen.getByLabelText("允许修改范围（每行一项）"), {
    target: { value: "src/" },
  });
  fireEvent.change(screen.getByLabelText("验收命令（每行一条）"), {
    target: { value: "/opt/foundry/verify" },
  });
  fireEvent.change(screen.getByLabelText("本地 Docker 镜像"), {
    target: { value: "fixture:local" },
  });
  const submit = screen.getByRole("button", {
    name: "创建任务并检查上下文",
  });
  expect(submit).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
  await waitFor(() => expect(submit).toBeEnabled());
  fireEvent.click(submit);
  expect(
    await screen.findByRole("heading", { level: 1, name: "新建任务" }),
  ).toBeVisible();
}

it("enters catalog only after bootstrap, preserves navigation priorities and opens the exact old mission workspace", async () => {
  const catalog = {
    schema_version: 1,
    revision: 0,
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
  let session!: (r: Response) => void;
  const fetcher = vi.fn((path: string) =>
    path === "/api/session"
      ? new Promise<Response>((resolve) => {
          session = resolve;
        })
      : Promise.resolve(
          response(
            path === "/api/catalog"
              ? { catalog }
              : path === "/api/profiles"
                ? { profiles: [], settings: { roles: {} } }
                : { missions: [mission] },
          ),
        ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<App />);
  fireEvent.click(screen.getByRole("button", { name: "项目资产" }));
  expect(fetcher.mock.calls.map((call) => call[0])).not.toContain(
    "/api/catalog",
  );
  await act(async () => session(response({})));
  fireEvent.click(await screen.findByRole("button", { name: "项目 A" }));
  fireEvent.click(screen.getByRole("button", { name: "项目任务" }));
  fireEvent.click(
    screen.getByRole("button", { name: "打开任务 改进导入校验" }),
  );
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
    "改进导入校验",
  );
  expect(screen.getByRole("button", { name: "交付任务" })).toHaveAttribute(
    "aria-current",
    "page",
  );
  fireEvent.click(screen.getByRole("button", { name: "项目资产" }));
  expect(await screen.findByRole("heading", { name: "项目 A" })).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "新建任务" }));
  expect(
    screen.getByRole("heading", { name: "定义这一次交付。" }),
  ).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "设置" }));
  expect(
    await screen.findByRole("heading", { name: /模型/, level: 1 }),
  ).toBeVisible();
  expect(
    fetcher.mock.calls.filter((call) => call[0] === "/api/session"),
  ).toHaveLength(1);
});

it("boots the same-origin session first and shows an honest empty workspace", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(response({}))
    .mockResolvedValue(response({ missions: [] }));
  vi.stubGlobal("fetch", fetcher);
  render(<App />);
  expect(
    await screen.findByRole("button", { name: "创建交付任务" }),
  ).toBeVisible();
  expect(screen.getByRole("button", { name: "配置模型" })).toBeVisible();
  expect(screen.getByRole("button", { name: "查看演示" })).toBeVisible();
  expect(fetcher.mock.calls[0][0]).toBe("/api/session");
  expect(fetcher.mock.calls[0][1].credentials).toBe("same-origin");
  expect(screen.queryByText("改进导入校验")).not.toBeInTheDocument();
  expect(screen.getByLabelText("工作台状态")).toHaveTextContent(
    /任务数据\s*已收到/,
  );
  expect(screen.getByLabelText("工作台状态")).toHaveTextContent(
    /前端版本\s*0\.1\.0/,
  );
  expect(screen.getByLabelText("工作台状态")).toHaveTextContent(
    /构建时间\s*\d{4}-\d{2}-\d{2}/,
  );
});
it("does not claim zero pending decisions before the first mission snapshot or after initial failure", async () => {
  let rejectSession!: (error: Error) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      () =>
        new Promise<Response>((_resolve, reject) => {
          rejectSession = reject;
        }),
    ),
  );
  render(<App />);
  const pendingButton = screen.getByRole("button", { name: /待决策/ });
  expect(
    within(pendingButton).getByLabelText("待决策数量不可用"),
  ).toHaveTextContent("—");
  expect(within(pendingButton).queryByText("0")).not.toBeInTheDocument();
  await act(async () => rejectSession(new Error("offline")));
  expect(await screen.findByRole("alert")).toHaveTextContent("offline");
  expect(
    within(pendingButton).getByLabelText("待决策数量不可用"),
  ).toHaveTextContent("—");
  expect(within(pendingButton).queryByText("0")).not.toBeInTheDocument();
});

it("shows a real zero pending count after a successful empty mission snapshot", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValueOnce(response({}))
      .mockResolvedValue(response({ missions: [] })),
  );
  render(<App />);
  await screen.findByRole("button", { name: "创建交付任务" });
  const pendingButton = screen.getByRole("button", { name: /待决策/ });
  expect(within(pendingButton).getByText("0")).toBeVisible();
  expect(
    within(pendingButton).queryByLabelText("待决策数量不可用"),
  ).not.toBeInTheDocument();
});
it("keeps a known empty decision count after an event refresh failure", async () => {
  let source!: EventTarget;
  class Stream extends EventTarget {
    onopen = null;
    onerror = null;
    constructor() {
      super();
      source = this;
    }
    close() {}
  }
  vi.stubGlobal("EventSource", Stream);
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) =>
      path === "/api/missions/missing"
        ? Promise.reject(new Error("refresh offline"))
        : Promise.resolve(
            response(path === "/api/missions" ? { missions: [] } : {}),
          ),
    ),
  );
  render(<App />);
  await screen.findByRole("button", { name: "创建交付任务" });
  const pendingButton = screen.getByRole("button", { name: /待决策/ });
  expect(within(pendingButton).getByText("0")).toBeVisible();
  act(() =>
    source.dispatchEvent(
      new MessageEvent("change", {
        data: JSON.stringify({
          seq: 1,
          mission_id: "missing",
          kind: "mission.changed",
          revision: 1,
          created_at: "2026-10-18T00:00:00Z",
        }),
      }),
    ),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent("refresh offline");
  expect(within(pendingButton).getByText("0")).toBeVisible();
  expect(
    within(pendingButton).queryByLabelText("待决策数量不可用"),
  ).not.toBeInTheDocument();
});
it("starts on the real overview and queue navigation opens the exact decision region without approving", async () => {
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({ matches: true })),
  );
  const draft = {
    ...mission,
    id: "draft-1",
    title: "检查上下文",
    state: "draft",
    approvals: [],
  };
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValueOnce(response({}))
      .mockResolvedValue(response({ missions: [draft, mission] })),
  );
  render(<App />);
  await waitFor(() => expect(screen.getByText("2 项")).toBeVisible());
  expect(screen.getByRole("heading", { name: "交付总览" })).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: /待决策/ }));
  fireEvent.click(
    screen.getByRole("button", { name: "查看上下文：检查上下文" }),
  );
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
    "检查上下文",
  );
  expect(screen.getByRole("region", { name: "决策与 Main Agent" })).toHaveClass(
    "drawer-open",
  );
  expect(
    screen.getByRole("region", { name: "决策与 Main Agent" }),
  ).toHaveFocus();
  expect(screen.getByRole("tab", { name: "总览" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  expect(screen.getByLabelText(/我已检查/)).not.toBeChecked();
  expect(
    screen.getByRole("button", { name: "授权上下文并生成计划" }),
  ).toBeDisabled();
});
it("passes disconnected stream state to the decision queue", async () => {
  vi.stubGlobal("EventSource", undefined);
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValueOnce(response({}))
      .mockResolvedValue(response({ missions: [mission] })),
  );
  render(<App />);
  await waitFor(() => expect(screen.getByText("1 项")).toBeVisible());
  fireEvent.click(screen.getByRole("button", { name: /待决策/ }));
  expect(screen.getByRole("status")).toHaveTextContent(
    "以下为最后收到的待决策状态 · 未连接",
  );
  expect(screen.getByRole("heading", { name: mission.title })).toBeVisible();
});
it("does not turn a failed API into demonstration data", async () => {
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
  render(<App />);
  expect(await screen.findByRole("alert")).toHaveTextContent(/offline/);
  expect(screen.queryByText(/演示数据/)).not.toBeInTheDocument();
  expect(screen.queryByText("暂无任务")).not.toBeInTheDocument();
  expect(screen.queryByText("第一份交付，从这里开始")).not.toBeInTheDocument();
  expect(screen.getByText("任务列表尚未读取")).toBeVisible();
});
it("ignores older mission fetches while preserving other tasks", () => {
  const other = { ...mission, id: "m2", revision: 4 };
  expect(
    mergeMission([mission, other], { ...mission, revision: 8, title: "old" }),
  ).toEqual([mission, other]);
  expect(mergeMission([mission, other], { ...other, revision: 5 })[0]).toBe(
    mission,
  );
});
it("can enter and leave isolated demonstration without changing real missions", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValueOnce(response({}))
      .mockResolvedValue(response({ missions: [] })),
  );
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "查看演示" }));
  expect(screen.getByText(/演示数据/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "退出演示" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "创建交付任务" })).toBeVisible(),
  );
});
it("keeps the selected task when SSE refetches arrive out of order for another task", async () => {
  let source!: EventTarget;
  class Stream extends EventTarget {
    onopen = null;
    onerror = null;
    constructor() {
      super();
      source = this;
    }
    close() {}
  }
  vi.stubGlobal("EventSource", Stream);
  const resolvers: ((response: Response) => void)[] = [];
  const other = { ...mission, id: "m2", title: "另一个任务" };
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) =>
      path === "/api/missions/m1"
        ? new Promise<Response>((resolve) => resolvers.push(resolve))
        : Promise.resolve(
            response(
              path === "/api/missions" ? { missions: [mission, other] } : {},
            ),
          ),
    ),
  );
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: /改进导入校验/ }));
  const emit = (seq: number) =>
    source.dispatchEvent(
      new MessageEvent("change", {
        data: JSON.stringify({
          seq,
          mission_id: "m1",
          kind: "plan.ready",
          revision: seq,
          created_at: "",
        }),
      }),
    );
  act(() => {
    emit(1);
    emit(2);
  });
  fireEvent.click(screen.getByRole("button", { name: /另一个任务/ }));
  await act(async () => {
    resolvers[1](
      response({
        mission: { ...mission, revision: 11, title: "新版任务标题" },
      }),
    );
  });
  await act(async () => {
    resolvers[0](
      response({
        mission: { ...mission, revision: 10, title: "过期任务标题" },
      }),
    );
  });
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
    "另一个任务",
  );
  expect(screen.getByRole("button", { name: /新版任务标题/ })).toBeVisible();
  expect(
    screen.queryByRole("button", { name: /过期任务标题/ }),
  ).not.toBeInTheDocument();
});

it("shows one linked project across overview, queue and task then returns to the queue", async () => {
  const draft = {
    ...mission,
    state: "draft",
    approvals: [],
    title: "关联任务",
  };
  const catalog = {
    schema_version: 1,
    revision: 0,
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
  const fetcher = vi.fn((path: string) =>
    Promise.resolve(
      response(
        path === "/api/missions"
          ? { missions: [draft] }
          : path === "/api/catalog"
            ? { catalog }
            : {},
      ),
    ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<App />);
  expect(await screen.findByText("任务项目：项目 A")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "查看任务" }));
  fireEvent.click(screen.getByRole("button", { name: "返回交付总览" }));
  expect(screen.getByRole("heading", { name: "交付总览" })).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: /待决策/ }));
  expect(screen.getByText("任务项目：项目 A")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "查看上下文：关联任务" }));
  expect(screen.getByRole("button", { name: "打开项目 项目 A" })).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "返回待决策" }));
  expect(screen.getByRole("heading", { name: "待决策" })).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "查看上下文：关联任务" }));
  fireEvent.click(screen.getByRole("button", { name: "打开项目 项目 A" }));
  expect(screen.getByRole("heading", { name: "项目 A" })).toBeVisible();
  expect(screen.getByRole("button", { name: "项目任务" })).toHaveAttribute(
    "aria-current",
    "page",
  );
  expect(
    fetcher.mock.calls.filter((call) => call[0] === "/api/catalog"),
  ).toHaveLength(1);
  expect(
    fetcher.mock.calls.some((call) => String(call[0]).includes("/actions")),
  ).toBe(false);
});

it("returns from a project task to the selected task tab with an unsaved form draft intact", async () => {
  const catalog = {
    schema_version: 1,
    revision: 0,
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
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) =>
      Promise.resolve(
        response(
          path === "/api/missions"
            ? { missions: [mission] }
            : path === "/api/catalog"
              ? { catalog }
              : {},
        ),
      ),
    ),
  );
  render(<App />);
  fireEvent.click(screen.getByRole("button", { name: "项目资产" }));
  fireEvent.click(await screen.findByRole("button", { name: "项目 A" }));
  fireEvent.click(screen.getByText("登记交付目标", { selector: "summary" }));
  fireEvent.change(screen.getByLabelText("目标名称"), {
    target: { value: "未保存目标" },
  });
  fireEvent.click(screen.getByRole("button", { name: "项目任务" }));
  fireEvent.click(
    screen.getByRole("button", { name: "打开任务 改进导入校验" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "返回项目 项目 A" }));
  expect(screen.getByRole("button", { name: "项目任务" })).toHaveAttribute(
    "aria-current",
    "page",
  );
  fireEvent.click(screen.getByRole("button", { name: "目标" }));
  expect(screen.getByLabelText("目标名称")).toHaveValue("未保存目标");
});

it("returns a newly created overview task to the overview", async () => {
  const created = {
    ...mission,
    id: "created-overview",
    title: "新建任务",
    state: "draft",
    approvals: [],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string, init?: RequestInit) =>
      Promise.resolve(
        response(
          path === "/api/repository-imports/inspections" &&
            init?.method === "POST"
            ? appInspection
            : path === "/api/missions" && init?.method === "POST"
              ? { mission: created }
              : path === "/api/missions"
                ? { missions: [] }
                : path === "/api/profiles"
                  ? {
                      profiles: [profile],
                      settings: {
                        default_profile_id: profile.id,
                        role_bindings: {},
                      },
                    }
                  : {},
        ),
      ),
    ),
  );
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "创建交付任务" }));
  await submitNewMission();
  fireEvent.click(screen.getByRole("button", { name: "返回交付总览" }));
  expect(screen.getByRole("heading", { name: "交付总览" })).toBeVisible();
});

it("returns a task created from a selected project to its prior project task view without linking it", async () => {
  const registered = {
    id: "repo-project",
    revision: 2,
    name: "Project source",
    path: "/tmp/new-mission",
    project_id: "p1",
    created_at: "now",
    updated_at: "now",
  };
  const created = {
    ...mission,
    id: "created-project",
    title: "新建任务",
    state: "draft",
    approvals: [],
  };
  const catalog = {
    schema_version: 1,
    revision: 0,
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
    mission_links: [],
  };
  const fetcher = vi.fn((path: string, init?: RequestInit) =>
    Promise.resolve(
      response(
        path === "/api/capabilities"
          ? { ...emptyCapabilities, repositories: [registered] }
          : path === "/api/repository-imports/inspections" &&
              init?.method === "POST"
            ? appInspection
            : path === "/api/missions" && init?.method === "POST"
              ? { mission: created }
              : path === "/api/missions"
                ? { missions: [mission] }
                : path === "/api/catalog"
                  ? { catalog }
                  : path === "/api/profiles"
                    ? {
                        profiles: [profile],
                        settings: {
                          default_profile_id: profile.id,
                          role_bindings: {},
                        },
                      }
                    : {},
      ),
    ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<App />);
  fireEvent.click(screen.getByRole("button", { name: "项目资产" }));
  fireEvent.click(await screen.findByRole("button", { name: "项目 A" }));
  fireEvent.click(screen.getByRole("button", { name: "项目任务" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "从 Project source 创建任务" }),
  );
  expect(screen.getByLabelText("Git 仓库绝对路径")).toHaveValue(
    "/tmp/new-mission",
  );
  await submitNewMission(true);
  const inspected = fetcher.mock.calls.find(
    ([path, init]) =>
      path === "/api/repository-imports/inspections" && init?.method === "POST",
  );
  expect(JSON.parse(String(inspected?.[1]?.body))).toMatchObject({
    repository_id: "repo-project",
    repository_revision: 2,
    consent: true,
  });
  fireEvent.click(screen.getByRole("button", { name: "返回项目 项目 A" }));
  expect(screen.getByRole("heading", { name: "项目 A" })).toBeVisible();
  expect(screen.getByRole("button", { name: "项目任务" })).toHaveAttribute(
    "aria-current",
    "page",
  );
  expect(
    fetcher.mock.calls.some((call) => call[0] === "/api/catalog/commands"),
  ).toBe(false);
});

it("keeps catalog context stale and drafts disabled until authenticated recovery rereads the catalog", async () => {
  const initialCatalog = {
    schema_version: 1,
    revision: 0,
    projects: [
      {
        id: "p1",
        name: "项目 A",
        description: "初始说明",
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
  const recoveredCatalog = { ...initialCatalog, mission_links: [] };
  let rejectFailedSession!: (error: Error) => void;
  let rejectRecoveredCatalog!: (error: Error) => void;
  let sessionReads = 0;
  let catalogReads = 0;
  vi.stubGlobal("EventSource", undefined);
  const fetcher = vi.fn((path: string) => {
    if (path === "/api/session") {
      sessionReads++;
      if (sessionReads === 2)
        return new Promise<Response>((_resolve, reject) => {
          rejectFailedSession = reject;
        });
      return Promise.resolve(response({}));
    }
    if (path === "/api/missions")
      return Promise.resolve(response({ missions: [mission] }));
    if (path === "/api/catalog") {
      catalogReads++;
      if (catalogReads === 1)
        return Promise.resolve(response({ catalog: initialCatalog }));
      if (catalogReads === 2)
        return new Promise<Response>((_resolve, reject) => {
          rejectRecoveredCatalog = reject;
        });
      return Promise.resolve(response({ catalog: recoveredCatalog }));
    }
    return Promise.resolve(response({}));
  });
  vi.stubGlobal("fetch", fetcher);
  render(<App />);
  expect(await screen.findByText("任务项目：项目 A")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "项目资产" }));
  fireEvent.click(await screen.findByRole("button", { name: "项目 A" }));
  fireEvent.click(screen.getByText("项目资料", { selector: "summary" }));
  fireEvent.change(screen.getByLabelText("项目说明"), {
    target: { value: "未保存说明" },
  });

  fireEvent.click(screen.getByRole("button", { name: "交付总览" }));
  fireEvent.click(screen.getByRole("button", { name: "重新连接" }));
  await act(async () => rejectFailedSession(new Error("session offline")));
  expect(await screen.findByText("任务项目：项目 A（最后快照）")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "项目资产" }));
  expect(screen.getByLabelText("项目说明")).toHaveValue("未保存说明");
  expect(screen.getByRole("button", { name: "重新加载" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "保存项目" })).toBeDisabled();

  fireEvent.click(screen.getByRole("button", { name: "交付总览" }));
  fireEvent.click(screen.getByRole("button", { name: "重新连接" }));
  await waitFor(() => expect(catalogReads).toBe(2));
  expect(screen.getByText("任务项目：项目 A（最后快照）")).toBeVisible();
  await act(async () => rejectRecoveredCatalog(new Error("catalog offline")));
  fireEvent.click(screen.getByRole("button", { name: "项目资产" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("catalog offline");
  expect(screen.getByLabelText("项目说明")).toHaveValue("未保存说明");
  expect(screen.getByRole("button", { name: "重新加载" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "保存项目" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
  await waitFor(() => expect(catalogReads).toBe(3));
  expect(screen.getByRole("button", { name: "保存项目" })).toBeEnabled();
  expect(screen.getByLabelText("项目说明")).toHaveValue("未保存说明");
  fireEvent.click(screen.getByRole("button", { name: "交付总览" }));
  expect(await screen.findByText("任务项目：未归类")).toBeVisible();
  expect(
    fetcher.mock.calls.some((call) => call[0] === "/api/catalog/commands"),
  ).toBe(false);
  expect(
    fetcher.mock.calls.some((call) =>
      String(call[0]).startsWith("/api/missions/"),
    ),
  ).toBe(false);
});

it("does not call an unavailable catalog unclassified and labels a genuine unlinked mission", async () => {
  const fetcher = vi.fn((path: string) => {
    if (path === "/api/catalog") return Promise.reject(new Error("offline"));
    return Promise.resolve(
      response(path === "/api/missions" ? { missions: [mission] } : {}),
    );
  });
  vi.stubGlobal("fetch", fetcher);
  const rendered = render(<App />);
  expect(await screen.findByText("任务项目：项目归类不可用")).toBeVisible();
  expect(screen.queryByText("任务项目：未归类")).not.toBeInTheDocument();
  rendered.unmount();
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) =>
      Promise.resolve(
        response(
          path === "/api/missions"
            ? { missions: [mission] }
            : path === "/api/catalog"
              ? {
                  catalog: {
                    schema_version: 1,
                    revision: 0,
                    projects: [],
                    targets: [],
                    environments: [],
                    resources: [],
                    bindings: [],
                    mission_links: [],
                  },
                }
              : {},
        ),
      ),
    ),
  );
  render(<App />);
  expect(await screen.findByText("任务项目：未归类")).toBeVisible();
});
