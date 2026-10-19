import { useEffect, useRef, useState } from "react";
import { APIError } from "../../api";
import { capabilityApi } from "../../capabilityApi";
import { mcpTaskApi } from "../../mcpTaskApi";
import type { LocalMCPTest } from "../../types";
import type {
  CapabilityPanelProps,
  DiscoveryOperation,
  DiscoveryResult,
  MCPConnection,
  MCPInput,
  MCPToolSnapshot,
} from "../../capabilityTypes";
import {
  compareCapabilityTime,
  useCapabilityRequest,
} from "./useCapabilityRequest";

const blank = (): MCPInput => ({
  expected_revision: 0,
  name: "",
  endpoint: "",
  enabled: true,
  clear_key: false,
});
const inputFor = (item: MCPConnection): MCPInput => ({
  id: item.id,
  expected_revision: item.revision,
  name: item.name,
  endpoint: item.endpoint,
  enabled: item.enabled,
  clear_key: false,
});
const statusLabel = (status: string) =>
  ({
    authorized_pending: "已授权，等待派发",
    dispatching: "派发中，结果未确定",
    succeeded: "发现成功（未执行工具）",
    failed: "发现失败",
    outcome_unknown: "结果未知",
  })[status] ?? "未知状态";

function DiscoveryDetails({ operation }: { operation: DiscoveryOperation }) {
  return (
    <section className="capability-observation" aria-label="最近发现尝试">
      <h4>最近发现尝试 · 配置 v{operation.connection_revision}</h4>
      <p>
        <strong>{statusLabel(operation.status)}</strong>
        {operation.error_code && <> · {operation.error_code}</>}
      </p>
      <p>
        授权：{operation.authorized_at} · 完成：
        {operation.finished_at || "尚无完成记录"}
      </p>
      <p>
        请求次数：{operation.requests ?? "未知"} · 读取字节：
        {operation.response_bytes ?? "未知"}
      </p>
      <details>
        <summary>操作记录与预算</summary>
        <p>
          Request ID：<code>{operation.id}</code>
        </p>
        <p>
          端点：<code>{operation.endpoint}</code> · 凭据版本{" "}
          {operation.credential_revision}
        </p>
        <p>
          记录创建：{operation.created_at} · 快照 ID：
          {operation.snapshot_id || "无"}
        </p>
        <pre>{JSON.stringify(operation.budget, null, 2)}</pre>
      </details>
    </section>
  );
}
function ToolSnapshot({
  snapshot,
  revision,
}: {
  snapshot: MCPToolSnapshot;
  revision: number;
}) {
  return (
    <section
      className="capability-observation"
      aria-label="最后已知有效工具快照"
    >
      <h4>最后已知有效工具快照 · v{snapshot.connection_revision}</h4>
      <p
        className={
          snapshot.connection_revision !== revision ? "warning-text" : "muted"
        }
      >
        {snapshot.connection_revision !== revision
          ? "旧配置快照，不代表当前配置或最近尝试成功。"
          : "版本与当前配置匹配；仅代表观察时刻，不代表当前健康或执行授权。"}
      </p>
      <p>观察时间：{snapshot.observed_at}</p>
      <p>
        来源：{snapshot.server_name} / {snapshot.server_version} · 协议{" "}
        {snapshot.protocol}
      </p>
      <p className="muted">
        摘要：<code>{snapshot.digest}</code>
      </p>
      {!snapshot.tools.length && <p>该次发现返回 0 个工具（有效空目录）。</p>}
      {snapshot.tools.map((tool) => (
        <details className="capability-tool" key={tool.name}>
          <summary>{tool.title || tool.name}</summary>
          <p>
            工具名：<code>{tool.name}</code>
          </p>
          <pre>{tool.description || "无描述"}</pre>
          <h5>输入 schema</h5>
          <pre>
            {tool.input_schema_json || "原始 JSON 文本不可用；未从解析值重建。"}
          </pre>
          {(tool.output_schema_json || tool.output_schema != null) && (
            <>
              <h5>输出 schema</h5>
              <pre>
                {tool.output_schema_json ||
                  "原始 JSON 文本不可用；未从解析值重建。"}
              </pre>
            </>
          )}
          {(tool.annotations_json || tool.annotations != null) && (
            <>
              <h5>不可信注解</h5>
              <pre>
                {tool.annotations_json ||
                  "原始 JSON 文本不可用；未从解析值重建。"}
              </pre>
            </>
          )}
          <p>
            定义摘要：<code>{tool.definition_digest}</code>
          </p>
        </details>
      ))}
    </section>
  );
}

export function MCPSettings({
  enabled,
  visible,
  snapshot,
  update,
}: CapabilityPanelProps & { visible: boolean }) {
  const [form, setForm] = useState<MCPInput>(blank);
  const [key, setKey] = useState("");
  const [notice, setNotice] = useState("");
  const [confirmation, setConfirmation] = useState<MCPConnection | null>(null);
  const confirmationHeading = useRef<HTMLHeadingElement>(null);
  const confirmationTrigger = useRef<HTMLButtonElement | null>(null);
  const [unresolved, setUnresolved] = useState<{
    requestID: string;
    connectionID: string;
  } | null>(null);
  const [localTest, setLocalTest] = useState<LocalMCPTest | null>(null);
  const [localTestLoading, setLocalTestLoading] = useState(false);
  const [localTestError, setLocalTestError] = useState("");
  const editor = useRef(0);
  const request = useCapabilityRequest(enabled);
  useEffect(() => {
    if (confirmation) confirmationHeading.current?.focus();
  }, [confirmation]);
  useEffect(() => {
    if (!visible || !enabled) setKey("");
  }, [visible, enabled]);
  useEffect(() => {
    if (!visible || !enabled) return;
    let active = true;
    setLocalTestLoading(true);
    setLocalTestError("");
    void mcpTaskApi
      .localTest()
      .then((value) => {
        if (active) setLocalTest(value);
      })
      .catch(() => {
        if (active)
          setLocalTestError(
            "本地测试服务状态读取失败；这不表示服务已关闭或可执行。",
          );
      })
      .finally(() => {
        if (active) setLocalTestLoading(false);
      });
    return () => {
      active = false;
    };
  }, [enabled, visible]);
  const edit = (item?: MCPConnection) => {
    if (request.locked()) return;
    editor.current++;
    setForm(item ? inputFor(item) : blank());
    setKey("");
    setNotice("");
    request.setError("");
  };
  const field = (change: Partial<MCPInput>) => {
    if (request.locked()) return;
    editor.current++;
    setForm((old) => ({ ...old, ...change }));
    setNotice("");
  };
  const accept = (result: DiscoveryResult) =>
    update((old) => {
      const recorded = old.discoveries.find(
        (item) => item.id === result.operation.id,
      );
      const phase = (item: DiscoveryOperation) =>
        item.finished_at ? 2 : item.status === "dispatching" ? 1 : 0;
      const operation =
        recorded &&
        (phase(recorded) > phase(result.operation) ||
          compareCapabilityTime(
            recorded.finished_at,
            result.operation.finished_at,
          ) > 0)
          ? recorded
          : result.operation;
      const incoming = result.snapshot;
      const known =
        incoming &&
        old.snapshots.find(
          (item) => item.connection_id === incoming.connection_id,
        );
      const acceptSnapshot =
        incoming &&
        (!known ||
          incoming.connection_revision > known.connection_revision ||
          (incoming.connection_revision === known.connection_revision &&
            compareCapabilityTime(incoming.observed_at, known.observed_at) >=
              0));
      return {
        ...old,
        discoveries: [
          operation,
          ...old.discoveries.filter((item) => item.id !== operation.id),
        ],
        snapshots: acceptSnapshot
          ? [
              incoming,
              ...old.snapshots.filter(
                (item) => item.connection_id !== incoming.connection_id,
              ),
            ]
          : old.snapshots,
      };
    });
  const save = () =>
    void request.run(async (valid) => {
      const generation = editor.current;
      const { connection } = await capabilityApi.saveMCP({
        ...form,
        ...(key ? { api_key: key } : {}),
      });
      if (!valid()) return;
      update((old) =>
        old.connections.some(
          (item) =>
            item.id === connection.id && item.revision > connection.revision,
        )
          ? old
          : {
              ...old,
              connections: [
                connection,
                ...old.connections.filter((item) => item.id !== connection.id),
              ],
            },
      );
      if (generation === editor.current) {
        setForm(inputFor(connection));
        setKey("");
        setNotice("配置已保存；未发起发现，也未授权执行工具。");
      }
    });
  const discover = () => {
    if (!confirmation || unresolved) return;
    const selected = confirmation;
    void request.run(async (valid) => {
      const requestID = crypto.randomUUID();
      setUnresolved({ requestID, connectionID: selected.id });
      let result: DiscoveryResult;
      try {
        result = await capabilityApi.discover(
          selected.id,
          selected.revision,
          requestID,
        );
      } catch (e) {
        if (!valid()) return;
        if (e instanceof APIError && e.status < 500) {
          setUnresolved(null);
          throw e;
        }
        // One read of the original intent only. Never generate a second POST here.
        result = await capabilityApi.discovery(requestID);
      }
      if (!valid()) return;
      accept(result);
      setUnresolved(null);
      setConfirmation(null);
      setNotice("已读取本次发现记录；请以状态、版本和观察时间为准。");
    });
  };
  const reconcile = () => {
    if (!unresolved) return;
    const original = unresolved;
    void request.run(async (valid) => {
      const result = await capabilityApi.discovery(original.requestID);
      if (valid()) {
        accept(result);
        setUnresolved(null);
        setConfirmation(null);
      }
    });
  };
  return (
    <>
      <div className="capability-layout">
        <section className="connection-section">
          <div className="section-heading">
            <h2>{form.id ? "编辑 MCP 连接" : "登记 MCP 连接"}</h2>
            {form.id && (
              <button
                disabled={!enabled || request.busy}
                onClick={() => edit()}
              >
                新建 MCP 连接
              </button>
            )}
          </div>
          <p className="muted">
            保存只登记配置，不访问端点。登记启用不等于获得工具调用授权。
          </p>
          {request.error && (
            <p role="alert" className="form-error">
              {request.error}
            </p>
          )}
          {notice && (
            <p role="status" className="notice">
              {notice}
            </p>
          )}
          <form
            className="connection-form"
            onSubmit={(e) => {
              e.preventDefault();
              save();
            }}
          >
            <fieldset disabled={!enabled || request.busy}>
              <legend className="sr-only">MCP 配置</legend>
              {form.id && (
                <p>
                  编辑基于配置 v{form.expected_revision}；重新读取不会覆盖草稿。
                </p>
              )}
              <label htmlFor="mcp-name">MCP 连接名称</label>
              <input
                id="mcp-name"
                required
                maxLength={200}
                value={form.name}
                onChange={(e) => field({ name: e.target.value })}
              />
              <label htmlFor="mcp-endpoint">MCP 端点</label>
              <input
                id="mcp-endpoint"
                type="url"
                required
                value={form.endpoint}
                onChange={(e) => field({ endpoint: e.target.value })}
                placeholder="http://127.0.0.1:端口/mcp"
              />
              <p className="field-help">
                远程 HTTPS 或字面回环 IP 的
                HTTP；不支持用户信息、查询参数、片段或重定向。更换端点必须重新输入凭据或明确清空。
              </p>
              <label className="capability-check">
                <input
                  type="checkbox"
                  checked={form.enabled}
                  onChange={(e) => field({ enabled: e.target.checked })}
                />
                登记启用（非执行授权）
              </label>
              <label htmlFor="mcp-key">MCP API Key（只写）</label>
              <input
                id="mcp-key"
                type="password"
                autoComplete="new-password"
                spellCheck={false}
                value={key}
                disabled={form.clear_key}
                onChange={(e) => {
                  if (!request.locked()) setKey(e.target.value);
                }}
              />
              <p className="field-help">
                凭据保存在后端
                Keychain。相同端点留空保留，保存成功或离开此页清空输入；不存浏览器。
              </p>
              <label className="capability-check">
                <input
                  type="checkbox"
                  checked={form.clear_key}
                  onChange={(e) => {
                    field({ clear_key: e.target.checked });
                    if (e.target.checked) setKey("");
                  }}
                />
                明确清空凭据
              </label>
              <div className="actions">
                <button className="primary" type="submit">
                  保存 MCP 配置
                </button>
              </div>
            </fieldset>
          </form>
        </section>
        <aside className="connection-guide">
          <h2>仅发现工具目录</h2>
          <p>
            支持 MCP 2025-11-25 · Streamable HTTP。请求内 JSON / SSE；不是完整
            MCP 客户端。
          </p>
          <p>
            15 秒总期限，单请求 5 秒，最多 13 个请求 / 10 页 / 500
            个工具；单响应 1 MiB，合计 4 MiB；单 schema 64 KiB。
          </p>
          <p>
            不支持 stdio、OAuth、独立 GET
            流、自动重连、协议降级或服务端请求。不会读取资源、提示词、链接、图标或远程
            schema。
          </p>
          <div className="note">
            工具定义与注解均为不可信文本。发现成功不是执行成功，也不是模型可调用授权。
          </div>
        </aside>
      </div>
      {confirmation && (
        <section
          className="note capability-confirm"
          role="region"
          aria-label="确认 MCP 发现"
        >
          <h3 ref={confirmationHeading} tabIndex={-1}>
            确认 MCP 发现
          </h3>
          <p>
            连接：{confirmation.name} · 已保存配置 v{confirmation.revision}
          </p>
          <p>
            端点：<code>{confirmation.endpoint}</code>
          </p>
          <p>
            只发送协议元数据及此端点的已配置认证；不发送任务源码，不执行工具。最多
            15 秒 / 13 次请求，结果不明不会自动重试。
          </p>
          <div className="actions">
            <button
              className="primary"
              disabled={!enabled || request.busy || !!unresolved}
              onClick={discover}
            >
              确认并发现
            </button>
            <button
              disabled={request.busy || !!unresolved}
              onClick={() => {
                setConfirmation(null);
                confirmationTrigger.current?.focus();
              }}
            >
              取消发现
            </button>
          </div>
        </section>
      )}
      {unresolved && !request.busy && (
        <section className="note warning-note" aria-label="未确定的发现请求">
          <p>
            本次请求结果尚未确认。原 Request ID：
            <code>{unresolved.requestID}</code>。不会重新发送发现。
          </p>
          <button disabled={!enabled || request.busy} onClick={reconcile}>
            查询本次结果
          </button>
        </section>
      )}
      <section className="saved-connections" aria-label="任务内本地 MCP 策略">
        <h2>任务内可执行本地测试服务</h2>
        <p className="muted">
          普通 MCP 连接与发现目录不授予执行权限。只有 controller
          拥有且当前合格的合成测试服务，可在精确 v2 任务中逐次审批调用；通用 MCP
          tools/call 仍关闭。
        </p>
        {localTestLoading && <p role="status">正在读取本地测试策略…</p>}
        {localTestError && (
          <p className="form-error" role="alert">
            {localTestError}
          </p>
        )}
        {localTest && (
          <div className="capability-record">
            <h3>
              {localTest.eligible
                ? "本地合成测试服务可用于新任务"
                : "本地合成测试服务当前不可用于新任务"}
            </h3>
            <p>
              运行开关：{localTest.enabled ? "已启用" : "已关闭"} · 创建资格：
              {localTest.eligible ? "当前有效" : "不可用"}
            </p>
            {localTest.reason && <p>{localTest.reason}</p>}
            {localTest.endpoint && <code>{localTest.endpoint}</code>}
            {localTest.selection?.tools.map((tool) => (
              <p key={tool.name}>
                <code>{tool.name}</code> · 定义{" "}
                <code>{tool.definition_digest}</code> · 审核策略{" "}
                <code>{tool.policy_digest}</code>
              </p>
            ))}
            <p>
              共享调用上限 {localTest.budget.max_calls} · 单次结果{" "}
              {localTest.budget.max_result_bytes} bytes · 总结果{" "}
              {localTest.budget.max_total_result_bytes} bytes
            </p>
          </div>
        )}
      </section>
      <section className="saved-connections">
        <h2>已登记 MCP 连接</h2>
        {!snapshot.connections.length && (
          <p className="muted">尚未登记 MCP 连接</p>
        )}
        {snapshot.connections.map((item) => {
          const latest = snapshot.discoveries
            .filter((op) => op.connection_id === item.id)
            .sort((a, b) =>
              compareCapabilityTime(b.created_at, a.created_at),
            )[0];
          const tools = snapshot.snapshots.find(
            (entry) => entry.connection_id === item.id,
          );
          const active =
            latest &&
            ["authorized_pending", "dispatching"].includes(latest.status);
          return (
            <article className="capability-record" key={item.id}>
              <div className="capability-record-header">
                <div>
                  <h3>{item.name}</h3>
                  <p>
                    当前配置 v{item.revision} ·{" "}
                    {item.enabled ? "登记启用" : "登记停用"} ·{" "}
                    {item.has_credential ? "凭据已配置" : "未配置凭据"}
                  </p>
                  <code>{item.endpoint}</code>
                  <p className="muted">
                    配置更新时间：{item.updated_at} · 协议 {item.protocol}
                  </p>
                </div>
                <div className="actions">
                  <button
                    disabled={!enabled || request.busy || !!active}
                    aria-label={`编辑 MCP ${item.name}`}
                    onClick={() => edit(item)}
                  >
                    编辑
                  </button>
                  <button
                    disabled={
                      !enabled ||
                      request.busy ||
                      !item.enabled ||
                      !!active ||
                      !!unresolved
                    }
                    aria-label={`发现工具目录 ${item.name}`}
                    onClick={(event) => {
                      confirmationTrigger.current = event.currentTarget;
                      setConfirmation(item);
                      setNotice("");
                    }}
                  >
                    发现工具目录
                  </button>
                </div>
              </div>
              {latest ? (
                <DiscoveryDetails operation={latest} />
              ) : (
                <p className="muted">尚无发现尝试；连接可用性未知。</p>
              )}
              {tools ? (
                <ToolSnapshot snapshot={tools} revision={item.revision} />
              ) : (
                <p className="muted">尚无有效工具快照。</p>
              )}
            </article>
          );
        })}
      </section>
      <section className="saved-connections">
        <h2>内建固定流程</h2>
        <p className="muted">
          固定受控仓库内建工具与 controller 流程的说明；Agent 可调用不表示 MCP
          tools/call 已开放。
        </p>
        {snapshot.builtin_capabilities.map((item) => (
          <div className="capability-record" key={item.id}>
            <h3>{item.name}</h3>
            <p>{item.description}</p>
            <p>
              {item.agent_callable
                ? "仅在显式受控任务中按冻结权限可调用"
                : "Agent 不可调用（controller 固定流程）"}
            </p>
          </div>
        ))}
        {!snapshot.builtin_capabilities.length && (
          <p className="muted">本次读取未返回内建能力说明。</p>
        )}
      </section>
    </>
  );
}
