import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { response } from "../../test/fixtures";
import { HookSettings } from "./HookSettings";

const point = (kind: string, label = kind) => ({
  kind,
  label,
  description: `${label} 发生不等于验证成功`,
});
const processor = {
  initialized_after_seq: 4,
  cursor: 8,
  latest_event_seq: 10,
  pending_events: 2,
  last_processed_at: "2026-10-18T01:02:03Z",
};
const runtime = {
  state: "running",
  last_attempt_at: "2026-10-18T01:02:04Z",
  last_success_at: "2026-10-18T01:02:03Z",
  error_code: "",
};
const rule = {
  id: "a".repeat(32),
  revision: 3,
  name: "现有规则",
  enabled: true,
  event_kinds: ["plan.ready"],
  mission_id: "m1",
  action: "record_activity",
  effective_after_seq: 6,
  created_at: "2026-10-18T01:00:00Z",
  updated_at: "2026-10-18T01:01:00Z",
};
const snapshot = (rules = [rule]) => ({
  hook_points: [
    point("mission.created", "任务创建"),
    point("plan.ready", "计划就绪"),
  ],
  rules,
  processor,
  runtime,
});
const records = {
  records: [],
  next_before: 0,
  processor,
  runtime,
};
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => (resolve = done));
  return { promise, resolve };
};
const missions = [
  { id: "m1", title: "任务一" },
  { id: "m2", title: "任务二" },
];

it("lazily reads independent hook state and starts new rules disabled without executable actions", async () => {
  const fetcher = vi.fn(async (path: string) =>
    response(path === "/api/hooks" ? snapshot([]) : records),
  );
  vi.stubGlobal("fetch", fetcher);
  const view = render(
    <HookSettings
      enabled
      visible={false}
      missions={missions}
      onOpenMission={() => {}}
    />,
  );
  expect(fetcher).not.toHaveBeenCalled();
  view.rerender(
    <HookSettings
      enabled
      visible
      missions={missions}
      onOpenMission={() => {}}
    />,
  );
  expect(
    await screen.findByRole("heading", { name: "事件钩子" }),
  ).toBeVisible();
  expect(await screen.findByText("任务创建")).toBeVisible();
  expect(screen.getByLabelText("启用规则")).not.toBeChecked();
  expect(screen.getByText("record_activity")).toBeVisible();
  expect(screen.getByText(/只记录，无执行动作/)).toBeVisible();
  expect(
    screen.queryByRole("button", { name: /运行|测试|重放|删除|脚本/ }),
  ).not.toBeInTheDocument();
  expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
    "/api/hooks",
    "/api/hooks/records?limit=50",
  ]);
});

it("creates a scoped disabled rule with the exact fixed action payload", async () => {
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/hooks") return response(snapshot([]));
    if (path.startsWith("/api/hooks/records")) return response(records);
    if (path === "/api/hooks/rules")
      return response({
        rule: {
          ...rule,
          revision: 1,
          name: "新建观察规则",
          enabled: false,
          event_kinds: ["mission.created"],
          mission_id: "m2",
        },
      });
    throw new Error(`unexpected ${path} ${init?.method}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <HookSettings
      enabled
      visible
      missions={missions}
      onOpenMission={() => {}}
    />,
  );
  await screen.findByText("任务创建");
  fireEvent.change(screen.getByLabelText("规则名称"), {
    target: { value: "新建观察规则" },
  });
  fireEvent.click(screen.getByLabelText("任务创建"));
  fireEvent.change(screen.getByLabelText("任务范围"), {
    target: { value: "m2" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存观察规则" }));
  expect(await screen.findByText(/已保存规则 r1/)).toBeVisible();
  const post = fetcher.mock.calls.find(
    ([path, init]) => path === "/api/hooks/rules" && init?.method === "POST",
  );
  expect(JSON.parse(String(post?.[1]?.body))).toEqual({
    expected_revision: 0,
    name: "新建观察规则",
    enabled: false,
    event_kinds: ["mission.created"],
    mission_id: "m2",
    action: "record_activity",
  });
});

it("retains every edited field and the old expected revision after a CAS conflict without retry", async () => {
  const refreshed = { ...rule, revision: 4, name: "服务端最新规则" };
  let reads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/hooks")
      return response(snapshot(++reads === 1 ? [rule] : [refreshed]));
    if (path.startsWith("/api/hooks/records")) return response(records);
    if (path === "/api/hooks/rules" && init?.method === "POST")
      return response({ error: { code: "conflict", message: "stale" } }, 409);
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const user = userEvent.setup();
  render(
    <HookSettings
      enabled
      visible
      missions={missions}
      onOpenMission={() => {}}
    />,
  );
  await user.click(
    await screen.findByRole("button", { name: "编辑规则 现有规则 r3" }),
  );
  await user.clear(screen.getByLabelText("规则名称"));
  await user.type(screen.getByLabelText("规则名称"), "待保留的修改");
  await user.click(screen.getByLabelText("启用规则"));
  await user.click(screen.getByLabelText("计划就绪"));
  await user.click(screen.getByLabelText("任务创建"));
  await user.selectOptions(screen.getByLabelText("任务范围"), "m2");
  await user.click(screen.getByRole("button", { name: "保存观察规则" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("草稿");
  expect(screen.getByLabelText("规则名称")).toHaveValue("待保留的修改");
  expect(screen.getByLabelText("启用规则")).not.toBeChecked();
  expect(screen.getByLabelText("任务创建")).toBeChecked();
  expect(screen.getByLabelText("计划就绪")).not.toBeChecked();
  expect(screen.getByLabelText("任务范围")).toHaveValue("m2");
  expect(
    await screen.findByRole("button", { name: "编辑规则 服务端最新规则 r4" }),
  ).toBeVisible();
  const posts = fetcher.mock.calls.filter(
    ([path, init]) => path === "/api/hooks/rules" && init?.method === "POST",
  );
  expect(posts).toHaveLength(1);
  expect(JSON.parse(String(posts[0][1]?.body)).expected_revision).toBe(3);
});

it("does not let an older refresh overwrite a successful save", async () => {
  const refresh = deferred<Response>();
  let reads = 0;
  const saved = { ...rule, revision: 4, name: "保存成功的新正文" };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/hooks") {
      reads++;
      if (reads === 1) return response(snapshot([rule]));
      return refresh.promise;
    }
    if (path.startsWith("/api/hooks/records")) return response(records);
    if (path === "/api/hooks/rules" && init?.method === "POST")
      return response({ rule: saved });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <HookSettings
      enabled
      visible
      missions={missions}
      onOpenMission={() => {}}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "编辑规则 现有规则 r3" }),
  );
  fireEvent.change(screen.getByLabelText("规则名称"), {
    target: { value: saved.name },
  });
  fireEvent.click(screen.getByRole("button", { name: "重新读取事件钩子" }));
  fireEvent.click(screen.getByRole("button", { name: "保存观察规则" }));
  expect(await screen.findByText(/已保存规则 r4/)).toBeVisible();
  await act(async () => refresh.resolve(response(snapshot([rule]))));
  expect(
    screen.getByRole("button", { name: "编辑规则 保存成功的新正文 r4" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "编辑规则 现有规则 r3" }),
  ).not.toBeInTheDocument();
});

it("keeps an authorized save owned while hidden and reconciles its server revision before another save", async () => {
  const pendingSave = deferred<Response>();
  const saved = {
    ...rule,
    id: "c".repeat(32),
    revision: 1,
    name: "隐藏期间完成",
    enabled: true,
    event_kinds: ["mission.created"],
    mission_id: "",
  };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/hooks") return response(snapshot([]));
    if (path.startsWith("/api/hooks/records")) return response(records);
    if (path === "/api/hooks/rules" && init?.method === "POST")
      return pendingSave.promise;
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const props = { enabled: true, missions, onOpenMission: () => {} };
  const view = render(<HookSettings {...props} visible />);
  await screen.findByText("任务创建");
  fireEvent.change(screen.getByLabelText("规则名称"), {
    target: { value: saved.name },
  });
  fireEvent.click(screen.getByLabelText("任务创建"));
  fireEvent.click(screen.getByLabelText("启用规则"));
  fireEvent.click(screen.getByRole("button", { name: "保存观察规则" }));
  view.rerender(<HookSettings {...props} visible={false} />);
  view.rerender(<HookSettings {...props} visible />);
  expect(screen.getByRole("button", { name: "保存观察规则" })).toBeDisabled();
  expect(
    fetcher.mock.calls.filter(
      ([path, init]) => path === "/api/hooks/rules" && init?.method === "POST",
    ),
  ).toHaveLength(1);
  await act(async () => pendingSave.resolve(response({ rule: saved })));
  expect(await screen.findByText(/已保存规则 r1/)).toBeVisible();
  expect(screen.getByLabelText("规则名称")).toHaveValue(saved.name);
  expect(screen.getByLabelText("启用规则")).toBeChecked();
  fireEvent.change(screen.getByLabelText("规则名称"), {
    target: { value: "基于保存版本继续编辑" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存观察规则" }));
  const posts = fetcher.mock.calls.filter(
    ([path, init]) => path === "/api/hooks/rules" && init?.method === "POST",
  );
  expect(posts).toHaveLength(2);
  expect(JSON.parse(String(posts[1][1]?.body))).toMatchObject({
    id: saved.id,
    expected_revision: 1,
    name: "基于保存版本继续编辑",
    enabled: true,
  });
});

it("reconciles a successful save that finishes while settings remain hidden", async () => {
  const pendingSave = deferred<Response>();
  const saved = {
    ...rule,
    id: "d".repeat(32),
    revision: 1,
    name: "隐藏时服务端完成",
    enabled: true,
    event_kinds: ["mission.created"],
    mission_id: "m2",
  };
  let writes = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/hooks") return response(snapshot([]));
    if (path.startsWith("/api/hooks/records")) return response(records);
    if (path === "/api/hooks/rules" && init?.method === "POST") {
      writes++;
      if (writes === 1) return pendingSave.promise;
      const input = JSON.parse(String(init.body));
      return response({ rule: { ...saved, revision: 2, name: input.name } });
    }
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const props = { enabled: true, missions, onOpenMission: () => {} };
  const view = render(<HookSettings {...props} visible />);
  await screen.findByText("任务创建");
  fireEvent.change(screen.getByLabelText("规则名称"), {
    target: { value: "提交时的草稿名称" },
  });
  fireEvent.click(screen.getByLabelText("任务创建"));
  fireEvent.change(screen.getByLabelText("任务范围"), {
    target: { value: "m2" },
  });
  fireEvent.click(screen.getByLabelText("启用规则"));
  fireEvent.click(screen.getByRole("button", { name: "保存观察规则" }));
  view.rerender(<HookSettings {...props} visible={false} />);

  await act(async () => pendingSave.resolve(response({ rule: saved })));

  view.rerender(<HookSettings {...props} visible />);
  expect(
    screen.getByRole("button", {
      name: "编辑规则 隐藏时服务端完成 r1",
    }),
  ).toBeVisible();
  expect(screen.getByLabelText("规则名称")).toHaveValue(saved.name);
  expect(screen.getByLabelText("启用规则")).toBeChecked();
  expect(screen.getByLabelText("任务范围")).toHaveValue("m2");
  fireEvent.change(screen.getByLabelText("规则名称"), {
    target: { value: "从服务端版本继续编辑" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存观察规则" }));
  await waitFor(() => expect(writes).toBe(2));
  const posts = fetcher.mock.calls.filter(
    ([path, init]) => path === "/api/hooks/rules" && init?.method === "POST",
  );
  expect(JSON.parse(String(posts[1][1]?.body))).toMatchObject({
    id: saved.id,
    expected_revision: saved.revision,
    name: "从服务端版本继续编辑",
    enabled: true,
    event_kinds: ["mission.created"],
    mission_id: "m2",
  });
});

it("replaces a pre-conflict snapshot read with a genuinely newer conflict refresh", async () => {
  const staleRead = deferred<Response>();
  const latest = { ...rule, revision: 4, name: "冲突后的最新规则" };
  let reads = 0;
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/hooks") {
      reads++;
      if (reads === 1) return response(snapshot([rule]));
      if (reads === 2) return staleRead.promise;
      return response(snapshot([latest]));
    }
    if (path.startsWith("/api/hooks/records")) return response(records);
    if (path === "/api/hooks/rules" && init?.method === "POST")
      return response({ error: { code: "conflict", message: "stale" } }, 409);
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <HookSettings
      enabled
      visible
      missions={missions}
      onOpenMission={() => {}}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "编辑规则 现有规则 r3" }),
  );
  fireEvent.change(screen.getByLabelText("规则名称"), {
    target: { value: "冲突草稿" },
  });
  fireEvent.click(screen.getByRole("button", { name: "重新读取事件钩子" }));
  fireEvent.click(screen.getByRole("button", { name: "保存观察规则" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("草稿");
  expect(screen.getByLabelText("规则名称")).toHaveValue("冲突草稿");
  expect(
    await screen.findByRole("button", {
      name: "编辑规则 冲突后的最新规则 r4",
    }),
  ).toBeVisible();
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/api/hooks"),
  ).toHaveLength(3);
  await act(async () => staleRead.resolve(response(snapshot([rule]))));
  expect(
    screen.getByRole("button", {
      name: "编辑规则 冲突后的最新规则 r4",
    }),
  ).toBeVisible();
  expect(screen.getByLabelText("规则名称")).toHaveValue("冲突草稿");
  const post = fetcher.mock.calls.find(
    ([path, init]) => path === "/api/hooks/rules" && init?.method === "POST",
  );
  expect(JSON.parse(String(post?.[1]?.body)).expected_revision).toBe(3);
});

it("keeps a hook draft while its settings subpage is hidden and requires explicit latest selection", async () => {
  const fetcher = vi.fn(async (path: string) =>
    response(path === "/api/hooks" ? snapshot([rule]) : records),
  );
  vi.stubGlobal("fetch", fetcher);
  const props = { enabled: true, missions, onOpenMission: () => {} };
  const view = render(<HookSettings {...props} visible />);
  await screen.findByText("任务创建");
  fireEvent.change(screen.getByLabelText("规则名称"), {
    target: { value: "跨子页保留" },
  });
  view.rerender(<HookSettings {...props} visible={false} />);
  view.rerender(<HookSettings {...props} visible />);
  expect(screen.getByLabelText("规则名称")).toHaveValue("跨子页保留");
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([path]) => path === "/api/hooks"),
    ).toHaveLength(1),
  );
});

it("validates names by Unicode characters and rejects control characters", async () => {
  const fetcher = vi.fn(async (path: string) =>
    response(path === "/api/hooks" ? snapshot([]) : records),
  );
  vi.stubGlobal("fetch", fetcher);
  const user = userEvent.setup();
  render(
    <HookSettings
      enabled
      visible
      missions={missions}
      onOpenMission={() => {}}
    />,
  );
  await screen.findByText("任务创建");
  await user.click(screen.getByLabelText("任务创建"));
  const name = screen.getByLabelText("规则名称");
  await user.type(name, "😀".repeat(80));
  expect(name).toHaveValue("😀".repeat(80));
  expect(screen.getByRole("button", { name: "保存观察规则" })).toBeEnabled();
  fireEvent.change(name, { target: { value: `${"😀".repeat(80)}a` } });
  expect(screen.getByRole("button", { name: "保存观察规则" })).toBeDisabled();
  fireEvent.change(name, { target: { value: "含\u0007控制符" } });
  expect(screen.getByRole("button", { name: "保存观察规则" })).toBeDisabled();
});

it("jumps from a recent global record to the existing App mission by its readable name", async () => {
  const onOpenMission = vi.fn();
  const fetcher = vi.fn(async (path: string) => {
    if (path === "/api/hooks") return response(snapshot([]));
    return response({
      ...records,
      records: [
        {
          id: 1,
          event_seq: 2,
          mission_id: "m1",
          mission_revision: 3,
          event_kind: "mission.created",
          event_created_at: "2026-10-18T00:00:00Z",
          rule_id: "a".repeat(32),
          rule_revision: 1,
          rule_name: "全局规则",
          action: "record_activity",
          handler_version: "record_activity/v1",
          status: "recorded",
          processed_at: "2026-10-18T00:00:01Z",
        },
      ],
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <HookSettings
      enabled
      visible
      missions={missions}
      onOpenMission={onOpenMission}
    />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "打开任务 任务一" }),
  );
  expect(onOpenMission).toHaveBeenCalledWith("m1");
});

it("labels an unavailable hook read without inventing an empty list and retries only on request", async () => {
  let fail = true;
  const fetcher = vi.fn(async (path: string) => {
    if (path.startsWith("/api/hooks/records")) return response(records);
    if (fail) throw new Error("secret local path");
    return response(snapshot([]));
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <HookSettings
      enabled
      visible
      missions={missions}
      onOpenMission={() => {}}
    />,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent("数据不可用");
  expect(
    screen.queryByText("已完成读取，目前没有规则。"),
  ).not.toBeInTheDocument();
  expect(document.body.textContent).not.toContain("secret local path");
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/api/hooks"),
  ).toHaveLength(1);
  fail = false;
  fireEvent.click(screen.getByRole("button", { name: "重新读取事件钩子" }));
  expect(await screen.findByText("已完成读取，目前没有规则。")).toBeVisible();
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/api/hooks"),
  ).toHaveLength(2);
});
