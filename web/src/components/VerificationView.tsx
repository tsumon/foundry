import type { Mission } from "../types";
export function VerificationView({ mission }: { mission: Mission }) {
  return (
    <>
      <EvidenceView mission={mission} />
      {!!mission.run_archives?.length && (
        <section className="review-section">
          <h2>历史归档 · 不用于当前交付审批</h2>
          <p className="muted">
            以下为重新规划前的记录，不代表当前候选的验证结果。
          </p>
          {mission.run_archives.map((archive) => (
            <details key={`${archive.epoch}:${archive.plan_revision}`}>
              <summary>
                运行 {archive.epoch} · 计划 r{archive.plan_revision} ·{" "}
                {archive.archived_at}
              </summary>
              <p className="muted">
                归档状态：{archive.state} · {archive.error || "无错误记录"}
              </p>
              <p className="muted small">
                候选摘要 <code>{archive.candidate_digest}</code>
              </p>
              <EvidenceView mission={archive} />
            </details>
          ))}
        </section>
      )}
    </>
  );
}
function EvidenceView({
  mission,
}: {
  mission: Pick<Mission, "evidence" | "reviews">;
}) {
  return (
    <>
      <div className="section-heading">
        <h2>命令执行证据</h2>
        <span className="muted small">保留失败与复测记录</span>
      </div>
      {!mission.evidence?.length && (
        <div className="quiet-state">尚未运行验收命令</div>
      )}
      {(mission.evidence ?? []).map((evidence, i) => (
        <section className="evidence-attempt" key={evidence.id}>
          <div className="section-heading">
            <h3>第 {i + 1} 次验证</h3>
            <span
              className={`status ${evidence.passed ? "success" : "warning"}`}
            >
              {evidence.passed ? "验收命令通过" : "验收未通过"}
            </span>
          </div>
          <dl className="metadata">
            <dt>验收版本</dt>
            <dd>r{evidence.acceptance_revision}</dd>
            <dt>候选摘要</dt>
            <dd>
              <code>{evidence.candidate_digest}</code>
            </dd>
            <dt>镜像 ID</dt>
            <dd>
              <code>{evidence.image_id}</code>
            </dd>
            <dt>时间</dt>
            <dd>{evidence.created_at}</dd>
          </dl>
          {(evidence.results ?? []).map((result, index) => (
            <div className="command-result" key={index}>
              <div className="command-heading">
                <code>{result.command}</code>
                <span>
                  退出码 {result.exit_code}
                  {result.timed_out ? " · 已超时" : ""}
                </span>
              </div>
              <pre>{result.output || "（无输出）"}</pre>
            </div>
          ))}
        </section>
      ))}
      <section className="review-section">
        <h2>模型审查 · 非命令执行证据</h2>
        <p className="muted">
          QA 与 Auditor 的判断不能替代真实运行结果。退出码 0
          也不保证软件没有缺陷。
        </p>
        {!mission.reviews?.length && (
          <p className="quiet-state">尚无模型审查</p>
        )}
        {(mission.reviews ?? []).map((review, index) => (
          <article className="review" key={index}>
            <div className="section-heading">
              <h3>{review.role === "qa" ? "QA" : "Auditor"}</h3>
              <span
                className={`status ${review.passed ? "success" : "warning"}`}
              >
                {review.passed ? "模型判断：可通过" : "模型判断：阻塞"}
              </span>
            </div>
            <p>{review.summary}</p>
            <p className="muted small">
              候选摘要 <code>{review.candidate_digest}</code>
            </p>
            {(review.findings ?? []).map((finding, i) => (
              <p key={i}>
                <span className="tag">{finding.severity}</span>{" "}
                {finding.summary}
                <br />
                <code>{(finding.paths ?? []).join(" · ")}</code>
              </p>
            ))}
          </article>
        ))}
      </section>
    </>
  );
}
