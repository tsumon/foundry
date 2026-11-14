import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { KnowledgeWorkspace } from "./KnowledgeWorkspace";
import { knowledgeApi, type KnowledgeGraph } from "../knowledgeApi";
import { api } from "../api";
import type { CatalogController } from "../useCatalog";
import { mission } from "../test/fixtures";
vi.mock("../knowledgeApi", () => ({
  knowledgeApi: { schema: vi.fn(), graph: vi.fn(), rebuild: vi.fn() },
}));
const graph: KnowledgeGraph = {
  schema_version: 1,
  backend: "sqlite-property-graph",
  source_digest: "digest",
  synced_at: "2026-11-14",
  total_nodes: 1,
  total_edges: 0,
  truncated: false,
  issues: [],
  edges: [],
  nodes: [
    {
      id: "test:1",
      kind: "evidence",
      label: "真实记录",
      project_id: "p1",
      mission_id: mission.id,
      historical: true,
      evidence_source: "synthetic_stub",
      source: { kind: "mission", id: mission.id, revision: 7 },
      properties: { note: "<script>never execute</script>" },
    },
  ],
};
const catalog = {
  snapshot: {
    projects: [{ id: "p1", name: "项目一" }],
    mission_links: [{ project_id: "p1", mission_id: mission.id }],
  },
  loading: false,
  error: "",
} as unknown as CatalogController;
const onOpenMission = vi.fn(),
  onOpenProject = vi.fn();
function mount() {
  return render(
    <KnowledgeWorkspace
      enabled
      catalog={catalog}
      missions={[mission]}
      onOpenMission={onOpenMission}
      onOpenProject={onOpenProject}
    />,
  );
}
beforeEach(() => {
  vi.clearAllMocks();
  vi.spyOn(api, "health").mockResolvedValue({
    status: "ok",
    version: "1",
    capabilities: {},
  });
  vi.mocked(knowledgeApi.schema).mockResolvedValue({
    version: 1,
    types: [{ kind: "evidence", label: "测试证据", description: "来源证据" }],
    relations: [
      { kind: "verifies", label: "验证", from: ["evidence"], to: ["evidence"] },
    ],
    rules: ["保留来源"],
  });
  vi.mocked(knowledgeApi.graph).mockResolvedValue(graph);
  vi.mocked(knowledgeApi.rebuild).mockResolvedValue(graph);
});
it("waits for mode metadata and explains isolated C4 scope without forbidden graph requests", async () => {
  let resolve!: (value: Awaited<ReturnType<typeof api.health>>) => void;
  vi.mocked(api.health).mockReturnValue(new Promise((done) => { resolve = done; }));
  mount();
  expect(knowledgeApi.schema).not.toHaveBeenCalled();
  expect(knowledgeApi.graph).not.toHaveBeenCalled();
  await act(async () => resolve({ status: "ok", version: "0.3.0", capabilities: {}, c4_controlled_only: true }));
  expect(await screen.findByRole("status")).toHaveTextContent("当前是隔离 C4 演示或受限执行模式");
  expect(screen.getByRole("status")).toHaveTextContent("Persistent Workbench");
  expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(knowledgeApi.schema).not.toHaveBeenCalled();
  expect(knowledgeApi.graph).not.toHaveBeenCalled();
  expect(knowledgeApi.rebuild).not.toHaveBeenCalled();
});
it("filters, traverses and links to source records with explicit provenance", async () => {
  mount();
  fireEvent.click(
    await screen.findByRole("button", { name: "真实记录 · 测试证据 · 历史" }),
  );
  expect(screen.getByText("流程模拟（未执行命令）")).toBeInTheDocument();
  expect(
    screen.getByText("<script>never execute</script>"),
  ).toBeInTheDocument();
  expect(screen.getByText("7")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "打开源任务" }));
  expect(onOpenMission).toHaveBeenCalledWith(mission.id);
  fireEvent.click(screen.getByRole("button", { name: "打开源项目" }));
  expect(onOpenProject).toHaveBeenCalledWith("p1");
  fireEvent.click(screen.getByRole("button", { name: "以此节点探索" }));
  await waitFor(() =>
    expect(
      vi.mocked(knowledgeApi.graph).mock.calls.at(-1)![0].get("root"),
    ).toBe("test:1"),
  );
  fireEvent.change(screen.getByLabelText("项目"), { target: { value: "p1" } });
  fireEvent.change(screen.getByLabelText("任务"), {
    target: { value: mission.id },
  });
  fireEvent.change(screen.getByLabelText("节点类型"), {
    target: { value: "evidence" },
  });
  fireEvent.change(screen.getByLabelText("关键词"), {
    target: { value: "证据" },
  });
  fireEvent.change(screen.getByLabelText("探索方向"), {
    target: { value: "lineage" },
  });
  fireEvent.change(screen.getByLabelText("深度"), { target: { value: "3" } });
  await waitFor(() => {
    const params = vi.mocked(knowledgeApi.graph).mock.calls.at(-1)![0];
    expect(Object.fromEntries(params)).toMatchObject({
      project_id: "p1",
      mission_id: mission.id,
      kind: "evidence",
      q: "证据",
      mode: "lineage",
      depth: "3",
      limit: "200",
    });
  });
});
it("shows ontology and rebuilds derived records", async () => {
  mount();
  fireEvent.click(await screen.findByRole("tab", { name: "本体与存储" }));
  expect(await screen.findByText("保留来源")).toBeInTheDocument();
  expect(screen.getByText("允许的关系")).toBeInTheDocument();
  const button = screen.getByRole("button", { name: "从业务记录重建图谱" });
  await waitFor(() => expect(button).toBeEnabled());
  fireEvent.click(button);
  await waitFor(() => expect(knowledgeApi.rebuild).toHaveBeenCalledOnce());
  await waitFor(() => expect(knowledgeApi.graph).toHaveBeenCalledTimes(2));
});
it("reports empty results and truncation without inventing data", async () => {
  vi.mocked(knowledgeApi.graph).mockResolvedValue({
    ...graph,
    nodes: [],
    truncated: true,
  });
  mount();
  expect(await screen.findByText(/没有匹配的业务记录/)).toBeInTheDocument();
  expect(screen.getByText(/结果已截断/)).toBeInTheDocument();
});
it("reports failures and retries", async () => {
  vi.mocked(knowledgeApi.graph).mockRejectedValueOnce(new Error("数据库损坏"));
  mount();
  expect(await screen.findByText(/数据库损坏/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "重新读取" }));
  expect(
    await screen.findByRole("button", { name: "真实记录 · 测试证据 · 历史" }),
  ).toBeInTheDocument();
});
it("aborts obsolete graph reads and disables rebuild in read-only recovery", async () => {
  vi.mocked(api.health).mockResolvedValue({
    status: "recovery_limited",
    read_only: true,
    version: "1",
    capabilities: {},
  });
  mount();
  await screen.findByRole("button", { name: "真实记录 · 测试证据 · 历史" });
  const signal = vi.mocked(knowledgeApi.graph).mock.calls[0][1];
  fireEvent.change(screen.getByLabelText("关键词"), {
    target: { value: "new" },
  });
  expect(signal?.aborted).toBe(true);
  fireEvent.click(await screen.findByRole("tab", { name: "本体与存储" }));
  expect(
    screen.getByRole("button", { name: "从业务记录重建图谱" }),
  ).toBeDisabled();
});

it("shows rebuild failure without claiming success", async () => {
  vi.mocked(knowledgeApi.rebuild).mockRejectedValueOnce(new Error("重建回滚"));
  mount();
  fireEvent.click(await screen.findByRole("tab", { name: "本体与存储" }));
  const b = await screen.findByRole("button", { name: "从业务记录重建图谱" });
  await waitFor(() => expect(b).toBeEnabled());
  fireEvent.click(b);
  expect(await screen.findByText(/重建回滚/)).toBeInTheDocument();
  expect(knowledgeApi.graph).toHaveBeenCalledTimes(1);
});

it("bounds diagram to sixteen nodes while preserving the full accessible list", async () => {
  vi.mocked(knowledgeApi.graph).mockResolvedValue({
    ...graph,
    edges: Array.from({ length: 29 }, (_, i) => ({
      id: `edge:${i}`,
      from: `node:${i}`,
      to: `node:${i + 1}`,
      kind: "verifies",
      source: graph.nodes[0].source,
    })),
    nodes: Array.from({ length: 30 }, (_, i) => ({
      ...graph.nodes[0],
      id: `node:${i}`,
      label: `节点 ${i}`,
    })),
  });
  mount();
  const diagram = await screen.findByRole("group", {
    name: "关系图，选择节点查看详情",
  });
  expect(diagram.querySelectorAll('[role="button"]')).toHaveLength(16);
  expect(screen.getByText(/关系图展示 16 \/ 30 个节点/)).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "节点 29 · 测试证据 · 历史" }),
  );
  expect(
    screen.getByRole("button", { name: "选择节点 节点 29" }),
  ).toHaveAttribute("aria-pressed", "true");
  fireEvent.keyDown(screen.getByRole("button", { name: "选择节点 节点 28" }), {
    key: "Enter",
  });
  expect(
    screen.getByRole("button", { name: "选择节点 节点 28" }),
  ).toHaveAttribute("aria-pressed", "true");
  fireEvent.click(screen.getByRole("button", { name: "选择节点 节点 27" }));
  expect(
    screen.getByRole("button", { name: "选择节点 节点 27" }),
  ).toHaveAttribute("aria-pressed", "true");
  expect(screen.queryByText(/来源摘要/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("tab", { name: "本体与存储" }));
  expect(screen.getByText(/来源摘要：digest/)).toBeInTheDocument();
});
it("starts the preview from a connected task instead of sorted approval records", async () => {
  const task = {
    ...graph.nodes[0],
    id: "task:one",
    kind: "task",
    label: "关联任务",
  };
  const candidate = {
    ...graph.nodes[0],
    id: "candidate:one",
    kind: "candidate",
    label: "Candidate",
    properties: { digest: "abcdef1234567890" },
  };
  vi.mocked(knowledgeApi.graph).mockResolvedValue({
    ...graph,
    nodes: [
      ...Array.from({ length: 20 }, (_, i) => ({
        ...graph.nodes[0],
        id: `approval:${i}`,
        kind: "approval",
        label: "Approval",
      })),
      task,
      candidate,
    ],
    edges: [
      {
        id: "connected",
        from: task.id,
        to: candidate.id,
        kind: "verifies",
        source: task.source,
      },
    ],
    issues: [
      { code: "missing", source_id: "full-source-id", message: "缺少来源" },
    ],
  });
  mount();
  const diagram = await screen.findByRole("group", {
    name: "关系图，选择节点查看详情",
  });
  expect(diagram.querySelectorAll('[role="button"]')).toHaveLength(2);
  expect(diagram.querySelector("line[marker-end]")).toBeInTheDocument();
  expect(screen.getByText(/焦点：关联任务/)).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: /^Candidate · abcdef12/ }),
  ).toBeInTheDocument();
  const summary = screen.getByText(/投影问题（1）/);
  expect(summary.closest("details")).not.toHaveAttribute("open");
  fireEvent.click(summary);
  expect(summary.closest("details")).toHaveAttribute("open");
});
