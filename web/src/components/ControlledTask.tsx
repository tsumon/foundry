import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import {
  isControlledMCPReadMission,
  roleNames,
  type Mission,
  type ReadGrant,
} from "../types";

export function ControlledTask({ mission }: { mission: Mission }) {
  const [grants, setGrants] = useState<ReadGrant[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const bindings = mission.task_bindings;
  const mcp = isControlledMCPReadMission(mission);
  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setLoading(true);
    setError("");
    void api
      .readGrants(mission.id, controller.signal)
      .then(({ read_grants }) => {
        if (active) setGrants(read_grants ?? []);
      })
      .catch((cause) => {
        if (
          active &&
          !(cause instanceof DOMException && cause.name === "AbortError")
        )
          setError(errorMessage(cause));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [mission.id, mission.revision]);

  if (!bindings)
    return (
      <p className="note warning-note" role="alert">
        受控任务缺少冻结绑定，完整性检查失败；规划已禁用。
      </p>
    );
  return (
    <section className="controlled-task" aria-label="受控只读任务绑定">
      <h3>{mcp ? "受控仓库读取与任务内 MCP" : "受控本地读取"}</h3>
      <p className="muted small">
        Researcher 只能读取创建时冻结的清单。Skill
        提供上下文，不执行文件，也不授予权限。
      </p>
      <dl className="controlled-facts">
        <dt>登记来源</dt>
        <dd>
          <code>{bindings.git.root}</code>
          <small>
            登记 v{bindings.git.repository_revision} · 观察{" "}
            {bindings.git.observation_id} · HEAD{" "}
            <code>{bindings.git.base_commit}</code>
          </small>
        </dd>
        <dt>策略与工具</dt>
        <dd>
          {bindings.policy_version} · {(bindings.tools ?? []).join(" · ")}
        </dd>
        <dt>固定预算</dt>
        <dd>
          {mcp ? (
            <>
              {bindings.budget.max_turns} 个 Researcher 回合 · 仓库读取与 MCP
              合计最多 {bindings.budget.max_calls} 次调用 · 单次参数{" "}
              {bindings.budget.max_argument_bytes} bytes · 单次结果{" "}
              {bindings.budget.max_result_bytes} bytes · 共享总预留{" "}
              {bindings.budget.max_total_result_bytes} bytes · 仓库读取{" "}
              {bindings.budget.deadline_seconds}s 截止
            </>
          ) : (
            <>
              {bindings.budget.max_turns} 个 Researcher 回合 ·{" "}
              {bindings.budget.max_calls} 次仓库调用 · 单次参数{" "}
              {bindings.budget.max_argument_bytes} bytes · 单次结果{" "}
              {bindings.budget.max_result_bytes} bytes · 总预留{" "}
              {bindings.budget.max_total_result_bytes} bytes ·{" "}
              {bindings.budget.deadline_seconds}s 截止
            </>
          )}
        </dd>
      </dl>
      {mcp && mission.mcp_selection && (
        <>
          <h4>冻结的本地 MCP 测试选择</h4>
          <dl className="controlled-facts">
            <dt>测试服务</dt>
            <dd>
              <code>{mission.mcp_selection.endpoint}</code>
              <small>
                连接 {mission.mcp_selection.connection_id} · 配置 v
                {mission.mcp_selection.connection_revision} · 协议{" "}
                {mission.mcp_selection.protocol}
              </small>
            </dd>
            <dt>工具与策略</dt>
            <dd>
              {mission.mcp_selection.tools.map((tool) => (
                <span className="block-code" key={tool.name}>
                  <code>{tool.name}</code> · 定义{" "}
                  <code>{tool.definition_digest}</code>
                </span>
              ))}
            </dd>
            <dt>MCP 单次边界</dt>
            <dd>
              使用上述共享调用与总结果预算，不另增加调用额度 · 参数{" "}
              {mission.mcp_selection.budget.max_argument_bytes} bytes · 结果{" "}
              {mission.mcp_selection.budget.max_result_bytes} bytes ·{" "}
              {mission.mcp_selection.budget.deadline_seconds}s 截止
            </dd>
          </dl>
          <p className="muted small">
            这是 controller
            拥有的合成本地测试服务；创建任务不执行工具，每次调用仍需单独审批。
          </p>
        </>
      )}
      <h4>冻结 Skill 与发送目标</h4>
      {!(bindings.skills ?? []).length ? (
        <p className="muted">未绑定 Skill。</p>
      ) : (
        <ul className="controlled-skill-list">
          {(bindings.skills ?? []).map((skill) => {
            const profile = mission.profiles?.[skill.role];
            const endpoint =
              profile && "base_url" in profile ? profile.base_url : undefined;
            return (
              <li key={`${skill.id}:${skill.revision}:${skill.role}`}>
                <strong>
                  {skill.name} · v{skill.revision} · {roleNames[skill.role]}
                </strong>
                <span>{profile?.model ?? "未配置"}</span>
                <code>
                  {typeof endpoint === "string"
                    ? endpoint
                    : "模型端点由安全任务视图隐藏"}
                </code>
                <small>SHA-256 {skill.digest}</small>
              </li>
            );
          })}
        </ul>
      )}
      <h4>本地读取授权记录</h4>
      {loading && <p role="status">正在读取授权记录…</p>}
      {error && (
        <p className="form-error" role="alert">
          授权记录读取失败：{error}。不可用不等于没有记录。
        </p>
      )}
      {grants && !grants.length && (
        <p className="muted">已读取，尚无本地读取授权记录。</p>
      )}
      {(grants ?? []).map((grant) => {
        const detailed = grant.epoch !== undefined;
        const current =
          detailed &&
          grant.epoch === mission.epoch &&
          grant.role === "researcher" &&
          grant.required_state === "planning" &&
          mission.state === "planning";
        return (
          <div className="read-grant" key={grant.id}>
            <strong>
              {current ? "当前 epoch/状态有记录" : "历史记录，当前不适用"}
            </strong>
            <span>
              {detailed
                ? `epoch ${grant.epoch} · ${grant.role} · 要求状态 ${grant.required_state}`
                : `${grant.role} · ${grant.created_at}`}
            </span>
            {grant.session_id && <code>{grant.session_id}</code>}
            <small>控制器仍会核对 session 与摘要；此显示不构成审批。</small>
          </div>
        );
      })}
    </section>
  );
}
