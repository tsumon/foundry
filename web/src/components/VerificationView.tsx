import type { Mission, CodingAttempt } from "../types";
import { ReplanSummary } from "./ReplanSummary";
import {
  verificationOutcome,
  verificationSourceLabel,
} from "../verificationSource";
export function VerificationView({ mission }: { mission: Mission }) {
  return (
    <>
      <ReplanSummary mission={mission} />
      <CodingAttempts mission={mission} />
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

export function codingAttemptLabel(attempt?: CodingAttempt): string {
  if (!attempt) return "尚无执行尝试";
  if (attempt.status === "succeeded") {
    if (attempt.phase === "worker" || attempt.phase === "debugger")
      return "候选修改已生成";
    if (attempt.phase === "verification") return "验证记录已生成（来源见证据）";
    return "模型审查通过（非命令执行证据）";
  }
  return (
    (
      {
        running: "执行中",
        failed: "失败",
        interrupted: "已中断",
        outcome_unknown: "结果未知",
      } as Record<string, string>
    )[attempt.status] ?? "未知状态"
  );
}

export function CodingAttempts({ mission }: { mission: Mission }) {
  if (!mission.coding_version)
    return <p className="muted small">历史任务未记录执行尝试</p>;
  if (mission.coding_version !== 1)
    return <p className="muted small">不支持的执行记录版本；不能推断结果</p>;
  const attempts = mission.coding_attempts ?? [];
  return (
    <section className="review-section" aria-label="控制器执行尝试">
      <h2>控制器执行尝试</h2>
      <p className="muted small">
        本地尝试
        ID，不是提供方回执。整体验证不归因到单个步骤；模型审查不是命令执行证据。
      </p>
      {!attempts.length && (
        <p className="quiet-state">尚无执行尝试；执行仍需单独批准</p>
      )}
      {attempts.map((a) => (
        <article className="review" key={a.id}>
          <div className="section-heading">
            <strong>
              {a.phase}
              {a.step_id ? ` · ${a.step_id}` : ""}
              {a.repair_round ? ` · 修复 ${a.repair_round}` : ""}
            </strong>
            <span>{codingAttemptLabel(a)}</span>
          </div>
          <p className="muted small">
            运行 {a.epoch} · 计划 r{a.plan_revision} · 尝试 <code>{a.id}</code>
          </p>
          {a.failure_code && (
            <p>
              失败类型 <code>{a.failure_code}</code>
            </p>
          )}
          {a.before && (
            <p className="muted small">
              之前 <code>{a.before.id}</code> · <code>{a.before.digest}</code>
            </p>
          )}
          {a.after && (
            <p className="muted small">
              选定候选 <code>{a.after.id}</code> · <code>{a.after.digest}</code>
            </p>
          )}
          {a.verification_id && (
            <p>
              验证记录 <code>{a.verification_id}</code>
            </p>
          )}
          {a.invocation_id && (
            <p>
              已记录调用关联 <code>{a.invocation_id}</code>
            </p>
          )}
          <p className="muted small">
            {a.started_at} · {a.finished_at || "尚无结束记录"}
          </p>
        </article>
      ))}
    </section>
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
        <h2>验证证据与来源</h2>
        <span className="muted small">保留失败与复测记录</span>
      </div>
      {!mission.evidence?.length && (
        <div className="quiet-state">尚无验证记录</div>
      )}
      {(mission.evidence ?? []).map((evidence, i) => (
        <section className="evidence-attempt" key={evidence.id}>
          <div className="section-heading">
            <h3>
              第 {i + 1} 次验证 · {verificationSourceLabel(evidence)}
            </h3>
            <span
              className={`status ${evidence.passed ? "success" : "warning"}`}
            >
              {verificationOutcome(evidence)}
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
