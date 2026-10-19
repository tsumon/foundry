import { isControlledMCPReadMission, type Mission } from "../types";
export function ResearchBrief({ mission }: { mission: Mission }) {
  const research = mission.research;
  if (!research)
    return (
      <div className="quiet-state">
        <h3>仓库调研</h3>
        <p>
          {mission.spec.research
            ? "调研简报尚未生成"
            : "本任务未启用 Researcher"}
        </p>
      </div>
    );
  const sources = new Set(
    (mission.context_manifest ?? []).map((file) => file.path),
  );
  return (
    <section className="research-brief">
      <div className="section-heading">
        <h2>
          <span aria-hidden="true">▤</span> 调研简报
        </h2>
        <span className="muted small">
          {isControlledMCPReadMission(mission)
            ? "Researcher · 仓库只读 + 已审批的本地合成参考"
            : "Researcher · 仓库只读"}
        </span>
      </div>
      <p>{research.summary}</p>
      <h3>
        {isControlledMCPReadMission(mission)
          ? "仓库观察、本地合成参考与模型判断"
          : "仓库观察与模型判断"}
      </h3>
      <p className="muted small">
        以下为模型分析，不是命令执行证据；未确认事项单独列出。
      </p>
      {(research.findings ?? []).map((finding, i) => (
        <div className="finding" key={i}>
          <span className="tag">{finding.severity}</span> {finding.summary}
          <div className="path-list">
            {(finding.paths ?? []).map((path) => (
              <code key={path}>
                {path}
                {sources.has(path) ? "" : " · 不在上下文清单"}
              </code>
            ))}
          </div>
          {!!finding.sources?.length && (
            <div className="source-reference-list">
              {finding.sources.map((source) => (
                <div
                  className="source-reference"
                  key={`${source.kind}:${source.invocation_id}:${source.digest}`}
                >
                  <strong>本地合成参考 · 调用 {source.invocation_id}</strong>
                  <code>SHA-256 {source.digest}</code>
                </div>
              ))}
            </div>
          )}
        </div>
      ))}
      <h3>未确认问题</h3>
      {research.unknowns?.length ? (
        <ul>
          {research.unknowns.map((unknown, i) => (
            <li key={i}>{unknown}</li>
          ))}
        </ul>
      ) : (
        <p className="muted">简报未列出未确认问题，不代表已全面验证。</p>
      )}
      <p className="muted small">
        {isControlledMCPReadMission(mission) &&
          "本地 MCP 引用是已封存的合成参考观察，不是 Docker 验证或交付证据。"}
        V1 不进行网页检索；外部事实需要另行调研。
      </p>
    </section>
  );
}
