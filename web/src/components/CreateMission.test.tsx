import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { it, expect, vi } from "vitest";
import { CreateMission } from "./CreateMission";
import { mission, profile, response } from "../test/fixtures";
import { emptyCapabilities, skill } from "./capabilities/testFixtures";
import { roles } from "../types";

const fillRequired = () => {
  for (const [label, value] of [
    ["任务标题", "新任务"],
    ["交付目标", "改进校验"],
    ["Git 仓库绝对路径", "/tmp/manual-source"],
    ["允许修改范围（每行一项）", "src/"],
    ["验收命令（每行一条）", "/opt/foundry/verify"],
    ["本地 Docker 镜像", "fixture:local"],
  ])
    fireEvent.change(screen.getByLabelText(label), { target: { value } });
};

it("previews actual role overrides and current credential then accepts only the canonical created mission", async () => {
  const bound = {
    ...profile,
    id: "p-account",
    name: "Current account profile",
    account_id: "a1",
    account: {
      id: "a1",
      revision: 4,
      name: "Current renamed account",
      provider: "custom",
      billing_mode: "unknown",
      enabled: true,
      current_credential_version: 2,
    },
  };
  const created = { ...mission, id: "canonical-server-snapshot" };
  const onCreated = vi.fn();
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile, bound],
        settings: {
          default_profile_id: "p1",
          role_bindings: { worker: "p-account" },
        },
      });
    if (path === "/api/health") return response({ capabilities: {} });
    return response({ mission: created });
  });
  vi.stubGlobal("fetch", fetcher);
  render(<CreateMission onCancel={() => {}} onCreated={onCreated} />);
  const preview = await screen.findByRole("region", { name: "创建前角色预览" });
  expect(within(preview).getByText("Current renamed account")).toBeVisible();
  expect(within(preview).getByText(/凭据版本：2/)).toBeVisible();
  expect(within(preview).getAllByText(/旧式连接/)).toHaveLength(6);
  fillRequired();
  fireEvent.click(screen.getByRole("button", { name: "创建任务并检查上下文" }));
  await waitFor(() => expect(onCreated).toHaveBeenCalledWith(created));
  const body = JSON.parse(
    String(fetcher.mock.calls.find(([p]) => p === "/api/missions")?.[1]?.body),
  );
  expect(body.model_snapshot).toBeUndefined();
  expect(body.workspace_mode).toBeUndefined();
  expect(body.spec.profile_id).toBe("p1");
});

it("previews the active confirmed policy as future-only and does not imply a default override changes its seven roles", async () => {
  const activeProposal = {
    id: "proposal-active",
    policy_revision: 4,
    created_at: "2026-10-19T01:00:00Z",
    settings_digest: "2".repeat(64),
    inputs_digest: "3".repeat(64),
    digest: "4".repeat(64),
    confirmable: true,
    roles: Object.fromEntries(
      roles.map((role) => [
        role,
        {
          selected_profile_id: "p1",
          required_capabilities: [],
          candidates: [],
        },
      ]),
    ),
  };
  const fetcher = vi.fn(async (path: string) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile, { ...profile, id: "other", name: "Other" }],
        settings: {
          default_profile_id: "p1",
          role_bindings: Object.fromEntries(roles.map((role) => [role, "p1"])),
          model_policy_decision_id: "decision-active",
        },
      });
    if (path === "/api/health") return response({ capabilities: {} });
    if (path === "/api/role-model-policy")
      return response({
        state: {
          policy: { revision: 4, created_at: "now", roles: {} },
          profiles_digest: "0".repeat(64),
          active_decision: {
            id: "decision-active",
            proposal_id: activeProposal.id,
            confirmed_at: "2026-10-19T01:04:00Z",
            proposal: activeProposal,
          },
        },
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<CreateMission onCancel={() => {}} onCreated={vi.fn()} />);
  const source = await screen.findByRole("region", {
    name: "创建任务的活动策略来源",
  });
  expect(source).toHaveTextContent("策略版本 4");
  expect(source).toHaveTextContent("创建时由服务端再次核对并冻结");
  fireEvent.change(screen.getByLabelText("任务默认模型连接"), {
    target: { value: "other" },
  });
  expect(source).toHaveTextContent(/默认连接选择不会覆盖已确认策略的七个角色/);
  expect(within(source).getAllByText(/p1/)).toHaveLength(7);
});

it("keeps controlled creation default-off, loads capabilities only on opt-in, and sends exact frozen identities", async () => {
  const snapshot = {
    ...emptyCapabilities,
    skills: [skill],
    repositories: [
      {
        id: "repo-1",
        revision: 2,
        name: "Source",
        path: "/tmp/source",
        project_id: "",
        created_at: "now",
        updated_at: "now",
      },
    ],
    git_observations: [
      {
        id: "obs-1",
        repository_id: "repo-1",
        repository_revision: 2,
        root: "/tmp/source",
        branch: "main",
        head: "abc123",
        detached: false,
        unborn: false,
        checked_at: "now",
        status: "succeeded",
        error_code: "",
        working_tree_status: "not_checked",
      },
    ],
  };
  const fetcher = vi.fn(async (path: string, _init?: RequestInit) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile],
        settings: { default_profile_id: "p1", role_bindings: {} },
      });
    if (path === "/api/health")
      return response({
        status: "ok",
        version: "test",
        capabilities: { controlled_read_creation: true },
      });
    if (path === "/api/capabilities") return response(snapshot);
    if (path === "/api/missions") return response({ mission });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<CreateMission onCancel={() => {}} onCreated={() => {}} />);
  await screen.findByRole("option", { name: "测试连接 · fixture-model" });
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/api/capabilities"),
  ).toHaveLength(0);
  fillRequired();
  fireEvent.click(screen.getByLabelText("启用受控 Researcher 本地读取"));
  expect(screen.getByLabelText(/启用 Researcher 仓库调研/)).toBeChecked();
  expect(screen.getByLabelText(/启用 Researcher 仓库调研/)).toBeDisabled();
  expect(screen.getByLabelText(/启用可恢复候选/)).not.toBeChecked();
  expect(screen.getByLabelText(/启用可恢复候选/)).toBeDisabled();
  expect(screen.getByText(/旧版可恢复候选与受控模式互斥/)).toBeVisible();
  expect(
    await screen.findByRole("option", { name: /Source · 登记 v2/ }),
  ).toBeVisible();
  fireEvent.change(screen.getByLabelText("冻结的登记来源"), {
    target: { value: "repo-1:2:obs-1" },
  });
  expect(screen.getByText(/not_checked 不表示工作区干净/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "添加 Skill 绑定" }));
  fireEvent.change(screen.getByLabelText("Skill 版本 1"), {
    target: { value: "skill-1:1" },
  });
  fireEvent.change(screen.getByLabelText("Skill 角色 1"), {
    target: { value: "researcher" },
  });
  fireEvent.click(screen.getByRole("button", { name: "创建任务并检查上下文" }));
  await waitFor(() =>
    expect(fetcher.mock.calls.some(([path]) => path === "/api/missions")).toBe(
      true,
    ),
  );
  const call = fetcher.mock.calls.find(([path]) => path === "/api/missions")!;
  expect(JSON.parse(String(call[1]?.body))).toMatchObject({
    repo_path: "/tmp/source",
    workspace_mode: "generations_controlled_read_v1",
    tool_mode: "controlled_read_v1",
    spec: { research: true },
    capability_selection: {
      repository_id: "repo-1",
      repository_revision: 2,
      observation_id: "obs-1",
      skills: [
        {
          id: "skill-1",
          revision: 1,
          digest: "skill-digest-1",
          name: "review-source",
          role: "researcher",
        },
      ],
    },
  });
});

it("sends only the eligible server selection for an explicitly chosen v2 local MCP task", async () => {
  const snapshot = {
    ...emptyCapabilities,
    repositories: [
      {
        id: "repo-1",
        revision: 2,
        name: "Source",
        path: "/tmp/source",
        project_id: "",
        created_at: "now",
        updated_at: "now",
      },
    ],
    git_observations: [
      {
        id: "obs-1",
        repository_id: "repo-1",
        repository_revision: 2,
        root: "/tmp/source",
        branch: "main",
        head: "abc123",
        detached: false,
        unborn: false,
        checked_at: "now",
        status: "succeeded",
        error_code: "",
        working_tree_status: "not_checked",
      },
    ],
  };
  const selection = {
    connection_id: "local-test",
    connection_revision: 4,
    snapshot_id: "snapshot-4",
    snapshot_digest: "snapshot-digest",
    tools: [
      {
        name: "reference.lookup",
        definition_digest: "definition-digest",
        policy_digest: "policy-digest",
      },
    ],
  };
  const fetcher = vi.fn(async (path: string, _init?: RequestInit) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile],
        settings: { default_profile_id: "p1", role_bindings: {} },
      });
    if (path === "/api/health")
      return response({
        status: "ok",
        version: "test",
        capabilities: {
          controlled_read_creation: true,
          task_local_mcp: true,
        },
      });
    if (path === "/api/mcp-local-test")
      return response({
        enabled: true,
        eligible: true,
        test_only: true,
        endpoint: "http://127.0.0.1:43123/mcp",
        selection,
        budget: {
          max_calls: 3,
          max_argument_bytes: 8192,
          max_result_bytes: 65536,
          max_total_result_bytes: 196608,
          deadline_seconds: 30,
          max_requests: 14,
          max_pages: 10,
          max_tools: 500,
          max_response_bytes: 1048576,
          max_total_response_bytes: 4194304,
          max_event_bytes: 1048576,
          max_notifications: 64,
          request_timeout_seconds: 5,
          cleanup_timeout_seconds: 1,
        },
      });
    if (path === "/api/capabilities") return response(snapshot);
    if (path === "/api/missions") return response({ mission });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<CreateMission onCancel={() => {}} onCreated={() => {}} />);
  await screen.findByRole("option", { name: "测试连接 · fixture-model" });
  fillRequired();
  const mcp = await screen.findByLabelText(
    "启用任务内本地 MCP 调研（测试服务）",
  );
  fireEvent.click(mcp);
  fireEvent.change(await screen.findByLabelText("冻结的登记来源"), {
    target: { value: "repo-1:2:obs-1" },
  });
  expect(
    screen.getByText(/仓库读取与 MCP 合计使用上述 3 次调用/),
  ).toBeVisible();
  expect(screen.queryByText(/3 次仓库调用/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "创建任务并检查上下文" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([path]) => path === "/api/missions"),
    ).toHaveLength(1),
  );
  const create = fetcher.mock.calls.find(([path]) => path === "/api/missions")!;
  expect(JSON.parse(String(create[1]?.body))).toMatchObject({
    repo_path: "/tmp/source",
    workspace_mode: "generations_controlled_mcp_read_v2",
    tool_mode: "controlled_mcp_read_v2",
    mcp_selection: selection,
    capability_selection: {
      repository_id: "repo-1",
      repository_revision: 2,
      observation_id: "obs-1",
      skills: [],
    },
    spec: { research: true },
  });
  expect(JSON.parse(String(create[1]?.body)).mcp_selection).not.toHaveProperty(
    "endpoint",
  );
});

it("rejects duplicate Skill-role pairs and prevents a fourth binding", async () => {
  const snapshot = {
    ...emptyCapabilities,
    skills: [skill],
    repositories: [
      {
        id: "repo-1",
        revision: 2,
        name: "Source",
        path: "/tmp/source",
        project_id: "",
        created_at: "now",
        updated_at: "now",
      },
    ],
    git_observations: [
      {
        id: "obs-1",
        repository_id: "repo-1",
        repository_revision: 2,
        root: "/tmp/source",
        branch: "main",
        head: "abc123",
        detached: false,
        unborn: false,
        checked_at: "now",
        status: "succeeded",
        error_code: "",
        working_tree_status: "not_checked",
      },
    ],
  };
  const fetcher = vi.fn(async (path: string, _init?: RequestInit) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile],
        settings: { default_profile_id: "p1", role_bindings: {} },
      });
    if (path === "/api/health")
      return response({
        status: "ok",
        version: "test",
        capabilities: { controlled_read_creation: true },
      });
    if (path === "/api/capabilities") return response(snapshot);
    if (path === "/api/missions") return response({ mission });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<CreateMission onCancel={() => {}} onCreated={() => {}} />);
  await screen.findByRole("option", { name: "测试连接 · fixture-model" });
  fillRequired();
  fireEvent.click(screen.getByLabelText("启用受控 Researcher 本地读取"));
  await screen.findByRole("option", { name: /Source · 登记 v2/ });
  fireEvent.change(screen.getByLabelText("冻结的登记来源"), {
    target: { value: "repo-1:2:obs-1" },
  });
  for (let index = 1; index <= 2; index++) {
    fireEvent.click(screen.getByRole("button", { name: "添加 Skill 绑定" }));
    fireEvent.change(screen.getByLabelText(`Skill 版本 ${index}`), {
      target: { value: "skill-1:1" },
    });
  }
  fireEvent.click(screen.getByRole("button", { name: "创建任务并检查上下文" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/不能重复绑定/);
  expect(fetcher.mock.calls.some(([path]) => path === "/api/missions")).toBe(
    false,
  );
  fireEvent.click(screen.getByRole("button", { name: "添加 Skill 绑定" }));
  expect(
    screen.getByRole("button", { name: "添加 Skill 绑定" }),
  ).toBeDisabled();
});
it("creates an explicit bounded spec from newline fields without starting model calls", async () => {
  const fetcher = vi.fn(async (path: string, _init?: RequestInit) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile],
        settings: { default_profile_id: "p1", role_bindings: {} },
      });
    if (path === "/api/health")
      return response({
        status: "ok",
        version: "test",
        capabilities: { controlled_read_creation: true },
      });
    if (path === "/api/missions") return response({ mission });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const created = vi.fn();
  render(<CreateMission onCancel={() => {}} onCreated={created} />);
  await screen.findByRole("option", { name: "测试连接 · fixture-model" });
  for (const [label, value] of [
    ["任务标题", "新任务"],
    ["交付目标", "改进校验"],
    ["Git 仓库绝对路径", "/tmp/source"],
    ["允许修改范围（每行一项）", "src/\nlib/"],
    ["受保护文件（每行一项）", "tests/"],
    ["验收命令（每行一条）", "/opt/foundry/verify\ntest -f src/main.ts"],
    ["本地 Docker 镜像", "fixture:local"],
  ])
    fireEvent.change(screen.getByLabelText(label), { target: { value } });
  fireEvent.click(screen.getByLabelText(/启用 Researcher/));
  fireEvent.click(screen.getByRole("button", { name: "创建任务并检查上下文" }));
  await waitFor(() => expect(created).toHaveBeenCalledWith(mission));
  const createCall = fetcher.mock.calls.find(
    ([path]) => path === "/api/missions",
  )!;
  expect(JSON.parse(String(createCall[1]?.body))).toEqual({
    title: "新任务",
    goal: "改进校验",
    repo_path: "/tmp/source",
    spec: {
      scope: ["src/", "lib/"],
      protected: ["tests/"],
      acceptance: ["/opt/foundry/verify", "test -f src/main.ts"],
      image: "fixture:local",
      image_id: "",
      profile_id: "p1",
      research: true,
      max_calls: 16,
    },
  });
  expect(fetcher).toHaveBeenCalledTimes(4);
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/api/role-model-policy"),
  ).toHaveLength(1);
});

it("opts new missions into immutable generations only when explicitly checked", async () => {
  const fetcher = vi.fn(async (path: string, _init?: RequestInit) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile],
        settings: { default_profile_id: "p1", role_bindings: {} },
      });
    if (path === "/api/health")
      return response({
        status: "ok",
        version: "test",
        capabilities: { controlled_read_creation: true },
      });
    if (path === "/api/missions") return response({ mission });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<CreateMission onCancel={() => {}} onCreated={() => {}} />);
  await screen.findByRole("option", { name: "测试连接 · fixture-model" });
  for (const [label, value] of [
    ["任务标题", "新任务"],
    ["交付目标", "改进校验"],
    ["Git 仓库绝对路径", "/tmp/source"],
    ["允许修改范围（每行一项）", "src/"],
    ["验收命令（每行一条）", "/opt/foundry/verify"],
    ["本地 Docker 镜像", "fixture:local"],
  ])
    fireEvent.change(screen.getByLabelText(label), { target: { value } });
  const optIn = screen.getByLabelText("启用可恢复候选（新任务）");
  expect(optIn).not.toBeChecked();
  fireEvent.click(optIn);
  fireEvent.click(screen.getByRole("button", { name: "创建任务并检查上下文" }));
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(4));
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/api/role-model-policy"),
  ).toHaveLength(1);
  const recoverableCall = fetcher.mock.calls.find(
    ([path]) => path === "/api/missions",
  )!;
  expect(JSON.parse(String(recoverableCall[1]?.body)).workspace_mode).toBe(
    "generations_v1",
  );
});

it("preserves the recoverable draft while controlled mode presents it as disabled and unchecked", async () => {
  const snapshot = {
    ...emptyCapabilities,
    repositories: [
      {
        id: "repo-1",
        revision: 2,
        name: "Source",
        path: "/tmp/source",
        project_id: "",
        created_at: "now",
        updated_at: "now",
      },
    ],
    git_observations: [
      {
        id: "obs-1",
        repository_id: "repo-1",
        repository_revision: 2,
        root: "/tmp/source",
        branch: "main",
        head: "abc123",
        detached: false,
        unborn: false,
        checked_at: "now",
        status: "succeeded",
        error_code: "",
        working_tree_status: "not_checked",
      },
    ],
  };
  const fetcher = vi.fn(async (path: string, _init?: RequestInit) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile],
        settings: { default_profile_id: "p1", role_bindings: {} },
      });
    if (path === "/api/health")
      return response({
        status: "ok",
        version: "test",
        capabilities: { controlled_read_creation: true },
      });
    if (path === "/api/capabilities") return response(snapshot);
    if (path === "/api/missions") return response({ mission });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<CreateMission onCancel={() => {}} onCreated={() => {}} />);
  await screen.findByRole("option", { name: "测试连接 · fixture-model" });
  fillRequired();
  const recoverable = screen.getByLabelText("启用可恢复候选（新任务）");
  const controlled = screen.getByLabelText("启用受控 Researcher 本地读取");
  fireEvent.click(recoverable);
  expect(recoverable).toBeChecked();
  fireEvent.click(controlled);
  expect(recoverable).toBeDisabled();
  expect(recoverable).not.toBeChecked();
  await screen.findByRole("option", { name: /Source · 登记 v2/ });
  fireEvent.change(screen.getByLabelText("冻结的登记来源"), {
    target: { value: "repo-1:2:obs-1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "创建任务并检查上下文" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([path]) => path === "/api/missions"),
    ).toHaveLength(1),
  );
  const controlledCall = fetcher.mock.calls.find(
    ([path]) => path === "/api/missions",
  )!;
  expect(JSON.parse(String(controlledCall[1]?.body))).toMatchObject({
    workspace_mode: "generations_controlled_read_v1",
    tool_mode: "controlled_read_v1",
  });
  fireEvent.click(controlled);
  expect(recoverable).toBeEnabled();
  expect(recoverable).toBeChecked();
  fireEvent.click(screen.getByRole("button", { name: "创建任务并检查上下文" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([path]) => path === "/api/missions"),
    ).toHaveLength(2),
  );
  const call = fetcher.mock.calls.filter(
    ([path]) => path === "/api/missions",
  )[1]!;
  expect(JSON.parse(String(call[1]?.body))).toMatchObject({
    workspace_mode: "generations_v1",
  });
  expect(JSON.parse(String(call[1]?.body))).not.toHaveProperty("tool_mode");
});

it("fails the controlled gate closed without blocking profiles or legacy creation", async () => {
  const fetcher = vi.fn(async (path: string) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile],
        settings: { default_profile_id: "p1", role_bindings: {} },
      });
    if (path === "/api/health")
      return response({ error: { message: "health unavailable" } }, 500);
    if (path === "/api/missions") return response({ mission });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const created = vi.fn();
  render(<CreateMission onCancel={() => {}} onCreated={created} />);

  await screen.findByRole("option", { name: "测试连接 · fixture-model" });
  expect(await screen.findByText(/创建开关读取失败/)).toBeVisible();
  expect(screen.getByLabelText("启用受控 Researcher 本地读取")).toBeDisabled();
  fillRequired();
  fireEvent.click(screen.getByRole("button", { name: "创建任务并检查上下文" }));
  await waitFor(() => expect(created).toHaveBeenCalledWith(mission));
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/api/capabilities"),
  ).toHaveLength(0);
});

it("retries a failed capability read and retains drafts and exact selections after a create conflict", async () => {
  const snapshot = {
    ...emptyCapabilities,
    skills: [skill],
    repositories: [
      {
        id: "repo-1",
        revision: 2,
        name: "Source",
        path: "/tmp/source",
        project_id: "",
        created_at: "now",
        updated_at: "now",
      },
    ],
    git_observations: [
      {
        id: "obs-1",
        repository_id: "repo-1",
        repository_revision: 2,
        root: "/tmp/source",
        branch: "main",
        head: "abc123",
        detached: false,
        unborn: false,
        checked_at: "now",
        status: "succeeded",
        error_code: "",
        working_tree_status: "not_checked",
      },
    ],
  };
  let capabilityCalls = 0;
  const fetcher = vi.fn(async (path: string) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile],
        settings: { default_profile_id: "p1", role_bindings: {} },
      });
    if (path === "/api/health")
      return response({
        status: "ok",
        version: "test",
        capabilities: { controlled_read_creation: true },
      });
    if (path === "/api/capabilities")
      return ++capabilityCalls === 1
        ? response({ error: { message: "catalog unavailable" } }, 500)
        : response(snapshot);
    if (path === "/api/missions")
      return response({ error: { message: "revision conflict" } }, 409);
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<CreateMission onCancel={() => {}} onCreated={() => {}} />);
  await screen.findByRole("option", { name: "测试连接 · fixture-model" });
  fillRequired();
  fireEvent.change(screen.getByLabelText("任务标题"), {
    target: { value: "保留标题" },
  });
  fireEvent.change(screen.getByLabelText("交付目标"), {
    target: { value: "保留目标" },
  });
  fireEvent.click(screen.getByLabelText("启用受控 Researcher 本地读取"));
  expect(
    await screen.findByText(/能力目录读取失败：catalog unavailable/),
  ).toBeVisible();
  expect(screen.getByLabelText("任务标题")).toHaveValue("保留标题");
  fireEvent.click(screen.getByRole("button", { name: "重试能力目录" }));
  await screen.findByRole("option", { name: /Source · 登记 v2/ });
  fireEvent.change(screen.getByLabelText("冻结的登记来源"), {
    target: { value: "repo-1:2:obs-1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "添加 Skill 绑定" }));
  fireEvent.change(screen.getByLabelText("Skill 版本 1"), {
    target: { value: "skill-1:1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "创建任务并检查上下文" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "revision conflict",
  );
  expect(screen.getByLabelText("任务标题")).toHaveValue("保留标题");
  expect(screen.getByLabelText("交付目标")).toHaveValue("保留目标");
  expect(screen.getByLabelText("冻结的登记来源")).toHaveValue("repo-1:2:obs-1");
  expect(screen.getByLabelText("Skill 版本 1")).toHaveValue("skill-1:1");
});

it("does not offer or infer a source when the exact successful observation is missing", async () => {
  const fetcher = vi.fn(async (path: string) => {
    if (path === "/api/profiles")
      return response({
        profiles: [profile],
        settings: { default_profile_id: "p1", role_bindings: {} },
      });
    if (path === "/api/health")
      return response({
        status: "ok",
        version: "test",
        capabilities: { controlled_read_creation: true },
      });
    if (path === "/api/capabilities")
      return response({
        ...emptyCapabilities,
        repositories: [
          {
            id: "repo-1",
            revision: 2,
            name: "Source",
            path: "/tmp/source",
            project_id: "",
            created_at: "now",
            updated_at: "now",
          },
        ],
        git_observations: [
          {
            id: "stale-observation",
            repository_id: "repo-1",
            repository_revision: 1,
            root: "/tmp/source",
            branch: "main",
            head: "old-head",
            detached: false,
            unborn: false,
            checked_at: "before-registration-update",
            status: "succeeded",
            error_code: "",
            working_tree_status: "not_checked",
          },
        ],
      });
    throw new Error(`unexpected ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<CreateMission onCancel={() => {}} onCreated={() => {}} />);
  await screen.findByRole("option", { name: "测试连接 · fixture-model" });
  fillRequired();
  fireEvent.click(screen.getByLabelText("启用受控 Researcher 本地读取"));
  const source = await screen.findByLabelText("冻结的登记来源");
  expect(source.querySelectorAll("option")).toHaveLength(1);
  fireEvent.submit(
    screen
      .getByRole("button", { name: "创建任务并检查上下文" })
      .closest("form")!,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    /请选择仍可用的成功登记来源/,
  );
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/api/missions"),
  ).toHaveLength(0);
});
