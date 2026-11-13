import { useEffect, useRef, useState } from "react";
import { api, errorMessage } from "../api";
import {
  knowledgeApi,
  type KnowledgeGraph,
  type Ontology,
} from "../knowledgeApi";
import type { CatalogController } from "../useCatalog";
import type { Mission } from "../types";
import "../knowledge.css";

const sources: Record<string, string> = {
  synthetic_stub: "流程模拟（未执行命令）",
  synthetic_content_check: "内容检查模拟（未执行命令）",
  isolated_execution: "隔离命令执行",
  unknown: "来源未记录",
};
export function KnowledgeWorkspace({
  enabled,
  readOnly = false,
  catalog,
  missions,
  initialMission = "",
  initialProject = "",
  onOpenMission,
  onOpenProject,
}: {
  enabled: boolean;
  readOnly?: boolean;
  catalog: CatalogController;
  missions: Mission[];
  initialMission?: string;
  initialProject?: string;
  onOpenMission: (id: string) => void;
  onOpenProject: (id: string) => void;
}) {
  const [recoveryReadOnly, setRecoveryReadOnly] = useState(true);
  useEffect(() => {
    if (!enabled) return;
    let active = true;
    api
      .health()
      .then((h) => {
        if (active) setRecoveryReadOnly(!!h.read_only);
      })
      .catch(() => {
        if (active) setRecoveryReadOnly(true);
      });
    return () => {
      active = false;
    };
  }, [enabled]);
  const [tab, setTab] = useState("graph");
  const [project, setProject] = useState(initialProject),
    [mission, setMission] = useState(initialMission),
    [kind, setKind] = useState(""),
    [q, setQ] = useState("");
  const [root, setRoot] = useState(""),
    [mode, setMode] = useState("neighbors"),
    [depth, setDepth] = useState("1");
  const [schema, setSchema] = useState<Ontology | null>(null),
    [graph, setGraph] = useState<KnowledgeGraph | null>(null),
    [selected, setSelected] = useState("");
  const [error, setError] = useState(""),
    [schemaError, setSchemaError] = useState(""),
    [loading, setLoading] = useState(false),
    [rebuilding, setRebuilding] = useState(false),
    [refresh, setRefresh] = useState(0);
  const rebuildController = useRef<AbortController | null>(null);
  useEffect(() => () => rebuildController.current?.abort(), []);
  useEffect(() => {
    if (!enabled) return;
    const c = new AbortController();
    setSchemaError("");
    knowledgeApi
      .schema(c.signal)
      .then((value) => {
        if (!c.signal.aborted) setSchema(value);
      })
      .catch((e) => {
        if (!c.signal.aborted) setSchemaError(errorMessage(e));
      });
    return () => c.abort();
  }, [enabled, refresh]);
  useEffect(() => {
    if (!enabled) return;
    const c = new AbortController();
    setLoading(true);
    setError("");
    setGraph(null);
    const query = new URLSearchParams({ depth, limit: "200", mode });
    for (const [key, value] of Object.entries({
      project_id: project,
      mission_id: mission,
      kind,
      q,
      root,
    }))
      if (value) query.set(key, value);
    knowledgeApi
      .graph(query, c.signal)
      .then((g) => {
        if (!c.signal.aborted) {
          setGraph(g);
          setSelected((s) =>
            (g.nodes ?? []).some((n) => n.id === s) ? s : "",
          );
        }
      })
      .catch((e) => {
        if (!c.signal.aborted) setError(errorMessage(e));
      })
      .finally(() => {
        if (!c.signal.aborted) setLoading(false);
      });
    return () => c.abort();
  }, [enabled, project, mission, kind, q, root, mode, depth, refresh]);
  const rebuild = async () => {
    const c = new AbortController();
    rebuildController.current = c;
    setRebuilding(true);
    setError("");
    try {
      await knowledgeApi.rebuild(c.signal);
      if (!c.signal.aborted) setRefresh((n) => n + 1);
    } catch (e) {
      if (!c.signal.aborted) setError(errorMessage(e));
    } finally {
      if (!c.signal.aborted) setRebuilding(false);
    }
  };
  const nodes = graph?.nodes ?? [],
    edges = graph?.edges ?? [],
    node = nodes.find((n) => n.id === selected);
  const typeLabel = (k: string) =>
    schema?.types.find((t) => t.kind === k)?.label ?? k;
  const relationLabel = (k: string) =>
    schema?.relations.find((t) => t.kind === k)?.label ?? k;
  const adjacency = new Map(nodes.map((n) => [n.id, new Set<string>()]));
  for (const edge of edges) {
    adjacency.get(edge.from)?.add(edge.to);
    adjacency.get(edge.to)?.add(edge.from);
  }
  const mostConnected = (items: typeof nodes) =>
    [...items].sort(
      (a, b) =>
        (adjacency.get(b.id)?.size ?? 0) - (adjacency.get(a.id)?.size ?? 0),
    )[0];
  const seed =
    node ??
    mostConnected(
      nodes.filter(
        (n) => n.kind === "task" && (!mission || n.mission_id === mission),
      ),
    ) ??
    mostConnected(
      nodes.filter(
        (n) => n.kind === "project" && (!project || n.project_id === project),
      ),
    ) ??
    mostConnected(nodes);
  const byID = new Map(nodes.map((n) => [n.id, n]));
  const diagramNodes: typeof nodes = [];
  const queue = seed ? [seed.id] : [],
    seen = new Set(queue);
  for (let i = 0; i < queue.length && diagramNodes.length < 16; i++) {
    const current = byID.get(queue[i]);
    if (!current) continue;
    diagramNodes.push(current);
    for (const neighbor of adjacency.get(current.id) ?? [])
      if (!seen.has(neighbor) && byID.has(neighbor)) {
        seen.add(neighbor);
        queue.push(neighbor);
      }
  }
  const displayLabel = (n: (typeof nodes)[number]) =>
    n.kind === "plan"
      ? `${n.label} · v${n.properties?.revision ?? n.source.revision}`
      : n.kind === "candidate"
        ? `${n.label} · ${(n.properties?.digest ?? n.id.split("/").at(-1) ?? n.id).slice(0, 8)}`
        : n.label;
  const taskName = (id?: string) => missions.find((m) => m.id === id)?.title;
  const positions = new Map(
    diagramNodes.map((n, i) => [
      n.id,
      { x: 110 + (i % 4) * 210, y: 60 + Math.floor(i / 4) * 135 },
    ]),
  );
  return (
    <div className="knowledge-workspace">
      <header className="mission-header">
        <div className="eyebrow">工作空间 / 本地事实</div>
        <h1>知识图谱</h1>
        <p className="muted">
          浏览业务记录及其来源。关系不代表测试通过或执行授权；影响范围仅沿已记录的下游关系展开。
        </p>
      </header>
      <div className="actions" role="tablist" aria-label="知识视图">
        <button
          role="tab"
          aria-selected={tab === "graph"}
          onClick={() => setTab("graph")}
        >
          关系图谱
        </button>
        <button
          role="tab"
          aria-selected={tab === "schema"}
          onClick={() => setTab("schema")}
        >
          本体与存储
        </button>
      </div>
      {!enabled && <p role="status">等待本地工作台连接后读取图谱。</p>}
      {schemaError && <p role="alert">本体读取失败：{schemaError}</p>}
      {tab === "graph" && (
        <div className="knowledge-sync">
          <span className="muted">
            {graph
              ? `同步：${graph.synced_at || "未同步"} · ${nodes.length} 个节点 / ${edges.length} 条关系`
              : "尚未读取图谱"}
          </span>
          <button
            disabled={!enabled || loading || rebuilding}
            onClick={() => setRefresh((n) => n + 1)}
          >
            刷新图谱
          </button>
        </div>
      )}
      {error && (
        <div role="alert">
          图谱请求失败：{error}{" "}
          <button onClick={() => setRefresh((n) => n + 1)}>重新读取</button>
        </div>
      )}
      {tab === "schema" ? (
        <section aria-label="本体定义">
          <section className="knowledge-storage" aria-label="图存储状态">
            <strong>图存储</strong>
            <p>
              {graph
                ? `${graph.backend} · 本体版本 ${graph.schema_version} · ${graph.total_nodes} 节点 / ${graph.total_edges} 关系`
                : "尚未读取存储状态"}
            </p>
            {graph && (
              <>
                <p>同步时间：{graph.synced_at || "未同步"}</p>
                <p className="knowledge-digest">
                  来源摘要：{graph.source_digest || "未记录"}
                </p>
              </>
            )}
            <button
              disabled={!enabled || rebuilding || readOnly || recoveryReadOnly}
              onClick={() => void rebuild()}
            >
              {rebuilding ? "正在重建…" : "从业务记录重建图谱"}
            </button>
            {(readOnly || recoveryReadOnly) && (
              <p className="muted">恢复只读模式，无法重建。</p>
            )}
            <p className="muted small">
              重建仅更新派生图数据，不调用模型或执行任务。
            </p>
          </section>

          {!schema && !schemaError && <p role="status">正在读取本体…</p>}
          {schema && (
            <>
              <h2>节点类型 · v{schema.version}</h2>
              <dl>
                {schema.types.map((t) => (
                  <div key={t.kind}>
                    <dt>
                      {t.label} <code>{t.kind}</code>
                    </dt>
                    <dd>{t.description}</dd>
                  </div>
                ))}
              </dl>
              <h2>允许的关系</h2>
              <ul>
                {schema.relations.map((r) => (
                  <li key={r.kind}>
                    <strong>{r.label}</strong> <code>{r.kind}</code>：
                    {r.pairs?.length
                      ? r.pairs
                          .map(
                            ([from, to]) =>
                              `${typeLabel(from)} → ${typeLabel(to)}`,
                          )
                          .join("；")
                      : `${r.from.map(typeLabel).join("、")} → ${r.to.map(typeLabel).join("、")}`}
                  </li>
                ))}
              </ul>
              <h2>校验规则</h2>
              <ul>
                {schema.rules.map((r) => (
                  <li key={r}>{r}</li>
                ))}
              </ul>
            </>
          )}
        </section>
      ) : (
        <>
          <form
            className="knowledge-filters"
            onSubmit={(e) => e.preventDefault()}
          >
            <label>
              项目
              <select
                value={project}
                onChange={(e) => {
                  setProject(e.target.value);
                  setMission("");
                  setRoot("");
                }}
              >
                <option value="">全部项目</option>
                {catalog.snapshot?.projects.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              任务
              <select
                value={mission}
                onChange={(e) => {
                  setMission(e.target.value);
                  setRoot("");
                }}
              >
                <option value="">全部任务</option>
                {missions
                  .filter(
                    (m) =>
                      !project ||
                      catalog.snapshot?.mission_links.some(
                        (l) =>
                          l.project_id === project && l.mission_id === m.id,
                      ),
                  )
                  .map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.title}
                    </option>
                  ))}
              </select>
            </label>
            <label>
              节点类型
              <select value={kind} onChange={(e) => setKind(e.target.value)}>
                <option value="">全部类型</option>
                {schema?.types.map((t) => (
                  <option key={t.kind} value={t.kind}>
                    {t.label}
                  </option>
                ))}
              </select>
            </label>
            <label>
              关键词
              <input
                type="search"
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
            </label>
            <label>
              探索方向
              <select value={mode} onChange={(e) => setMode(e.target.value)}>
                <option value="neighbors">邻接关系</option>
                <option value="impact">影响范围（下游）</option>
                <option value="lineage">证据追溯（上游）</option>
              </select>
            </label>
            <label>
              深度
              <select value={depth} onChange={(e) => setDepth(e.target.value)}>
                {[1, 2, 3, 4].map((d) => (
                  <option key={d}>{d}</option>
                ))}
              </select>
            </label>
            {root && <button onClick={() => setRoot("")}>清除探索起点</button>}
          </form>
          {catalog.error && (
            <p role="alert">项目台账读取失败：{catalog.error}</p>
          )}
          {catalog.loading && <p role="status">正在读取项目台账…</p>}
          {!root && (
            <p className="muted small">
              选择节点并点击“以此节点探索”后，探索方向与深度将应用于该起点。
            </p>
          )}
          {root && (
            <p>探索起点：{nodes.find((n) => n.id === root)?.label ?? root}</p>
          )}
          {loading && <p role="status">正在读取知识图谱…</p>}
          {graph?.truncated && (
            <p role="status">
              结果已截断（最多 200
              个节点），请缩小筛选范围；当前展示并非完整图谱。
            </p>
          )}
          {!!graph?.issues?.length && (
            <details className="knowledge-issues">
              <summary>
                投影问题（{graph.issues.length}）· 点击查看不完整记录
              </summary>
              <ul>
                {graph.issues.map((issue, i) => (
                  <li key={i}>
                    [{issue.code}] {issue.message}
                    <small> · 来源 {issue.source_id}</small>
                  </li>
                ))}
              </ul>
            </details>
          )}
          {!loading && !error && graph && !nodes.length && (
            <p role="status">
              没有匹配的业务记录。请调整筛选条件，或在产生业务记录后重建图谱。
            </p>
          )}
          {!!nodes.length && (
            <>
              <p className="muted small">
                关系图展示 {diagramNodes.length} / {nodes.length} 个节点
                （焦点：{seed ? displayLabel(seed) : "无"}，沿相连关系预览）
                ；完整节点与关系保留在下方列表。
              </p>
              <div className="knowledge-diagram">
                <svg
                  viewBox={`0 0 900 ${Math.max(180, Math.ceil(diagramNodes.length / 4) * 135 + 30)}`}
                  role="group"
                  aria-label="关系图，选择节点查看详情"
                >
                  <defs>
                    <marker
                      id="knowledge-arrow"
                      viewBox="0 0 10 10"
                      refX="9"
                      refY="5"
                      markerWidth="6"
                      markerHeight="6"
                      orient="auto-start-reverse"
                    >
                      <path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" />
                    </marker>
                  </defs>
                  {edges.map((e) => {
                    const a = positions.get(e.from),
                      b = positions.get(e.to);
                    if (!a || !b || e.from === e.to) return null;
                    const dx = b.x - a.x,
                      dy = b.y - a.y;
                    const clip =
                      1 / Math.max(Math.abs(dx) / 94, Math.abs(dy) / 27);
                    return (
                      <g key={e.id}>
                        <line
                          x1={a.x + dx * clip}
                          y1={a.y + dy * clip}
                          x2={b.x - dx * clip}
                          y2={b.y - dy * clip}
                          markerEnd="url(#knowledge-arrow)"
                        />
                        <text x={(a.x + b.x) / 2} y={(a.y + b.y) / 2 - 5}>
                          {relationLabel(e.kind)}
                        </text>
                      </g>
                    );
                  })}
                  {diagramNodes.map((n) => {
                    const p = positions.get(n.id)!;
                    return (
                      <g
                        key={n.id}
                        transform={`translate(${p.x},${p.y})`}
                        role="button"
                        tabIndex={0}
                        aria-label={`选择节点 ${displayLabel(n)}${["plan", "candidate"].includes(n.kind) ? ` · ${n.id}` : ""}`}
                        aria-pressed={selected === n.id}
                        onClick={() => setSelected(n.id)}
                        onKeyDown={(event) => {
                          if (event.key === "Enter" || event.key === " ") {
                            event.preventDefault();
                            setSelected(n.id);
                          }
                        }}
                      >
                        <rect x="-90" y="-23" width="180" height="46" rx="6" />
                        <text textAnchor="middle" y="-3">
                          {displayLabel(n).length > 23
                            ? displayLabel(n).slice(0, 22) + "…"
                            : displayLabel(n)}
                        </text>
                        <text textAnchor="middle" y="15">
                          {typeLabel(n.kind)}
                        </text>
                      </g>
                    );
                  })}
                </svg>
              </div>
              <div className="knowledge-columns">
                <section aria-label="图谱结果">
                  <h2>节点（{nodes.length}）</h2>
                  <ul className="knowledge-results">
                    {nodes.map((n) => (
                      <li key={n.id}>
                        <button
                          aria-pressed={selected === n.id}
                          onClick={() => setSelected(n.id)}
                        >
                          {displayLabel(n)} · {typeLabel(n.kind)}
                          {n.historical ? " · 历史" : ""}
                        </button>
                        {taskName(n.mission_id) && (
                          <small className="muted">
                            任务：{taskName(n.mission_id)}
                          </small>
                        )}
                      </li>
                    ))}
                  </ul>
                  <h2>关系（{edges.length}）</h2>
                  <ul>
                    {edges.map((e) => (
                      <li key={e.id}>
                        {nodes.find((n) => n.id === e.from)?.label ?? e.from} →{" "}
                        <strong>{relationLabel(e.kind)}</strong> →{" "}
                        {nodes.find((n) => n.id === e.to)?.label ?? e.to}
                        <small>
                          {" "}
                          · 来源 {e.source.kind}/{e.source.id} · 版本{" "}
                          {e.source.revision}
                        </small>
                      </li>
                    ))}
                  </ul>
                </section>
                <section aria-label="节点详情">
                  <h2>节点详情</h2>
                  {!node ? (
                    <p>从结果列表选择一个节点。</p>
                  ) : (
                    <>
                      <h3>{displayLabel(node)}</h3>
                      {taskName(node.mission_id) && (
                        <p>所属任务：{taskName(node.mission_id)}</p>
                      )}
                      <dl>
                        <dt>标识 / 类型</dt>
                        <dd>
                          {node.id} / {typeLabel(node.kind)}
                        </dd>
                        <dt>状态</dt>
                        <dd>{node.state || "未记录"}</dd>
                        <dt>记录范围</dt>
                        <dd>{node.historical ? "历史记录" : "当前记录"}</dd>
                        <dt>来源</dt>
                        <dd>
                          {node.source.kind} / {node.source.id}
                        </dd>
                        <dt>来源版本</dt>
                        <dd>{node.source.revision}</dd>
                        {node.kind === "evidence" && (
                          <>
                            <dt>证据来源</dt>
                            <dd>
                              {sources[node.evidence_source ?? "unknown"] ??
                                node.evidence_source}
                            </dd>
                          </>
                        )}
                      </dl>
                      <h3>属性</h3>
                      <dl>
                        {Object.entries(node.properties ?? {}).map(([k, v]) => (
                          <div key={k}>
                            <dt>{k}</dt>
                            <dd>{v}</dd>
                          </div>
                        ))}
                      </dl>
                      <div className="actions">
                        <button onClick={() => setRoot(node.id)}>
                          以此节点探索
                        </button>
                        {node.mission_id && (
                          <button
                            onClick={() => onOpenMission(node.mission_id!)}
                          >
                            打开源任务
                          </button>
                        )}
                        {node.project_id && (
                          <button
                            onClick={() => onOpenProject(node.project_id!)}
                          >
                            打开源项目
                          </button>
                        )}
                      </div>
                    </>
                  )}
                </section>
              </div>
            </>
          )}
        </>
      )}
    </div>
  );
}
