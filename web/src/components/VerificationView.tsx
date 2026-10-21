import type { Mission, CodingAttempt } from "../types";
import { ReplanSummary } from "./ReplanSummary";
import { C4Verification } from "./C4Verification";
import {
  verificationOutcome,
  verificationSourceLabel,
} from "../verificationSource";

// UI completeness only. The controller recomputes cryptographic bindings and
// validates actual files on delivery; this display never grants authority.
export function codingReviewsCurrent(mission: Mission): boolean {
  if (!mission.coding_version) return true;
  if (mission.coding_version !== 1 || !mission.plan) return false;
  const approval = mission.approvals
    ?.filter((a) => a.kind === "execute")
    .at(-1);
  const evidence = mission.evidence?.at(-1);
  if (
    !approval?.digest ||
    approval.plan_revision !== mission.plan.revision ||
    !evidence?.passed ||
    evidence.acceptance_revision !== mission.plan.revision ||
    evidence.image_id !== mission.spec.image_id ||
    evidence.candidate_digest !== mission.candidate_digest ||
    evidence.results?.length !== mission.spec.acceptance.length ||
    evidence.results.some(
      (r, i) =>
        r.command !== mission.spec.acceptance[i] ||
        r.exit_code !== 0 ||
        r.timed_out,
    )
  )
    return false;
  const qa = mission.reviews?.filter((r) => r.role === "qa").at(-1);
  const auditor = mission.reviews?.filter((r) => r.role === "auditor").at(-1);
  if (
    !qa?.binding_digest ||
    !qa.trace_digest ||
    qa.binding_digest !== auditor?.binding_digest ||
    qa.trace_digest !== auditor?.trace_digest
  )
    return false;
  return [qa, auditor].every((review) => {
    if (
      !review ||
      !review.passed ||
      review.coding_version !== 1 ||
      review.plan_revision !== mission.plan!.revision ||
      review.candidate_digest !== mission.candidate_digest ||
      review.findings?.some((f) =>
        ["high", "critical", "blocking"].includes(f.severity),
      )
    )
      return false;
    const required =
      review.role === "qa"
        ? mission.spec.acceptance.map((_, i) => `acceptance:${i}`)
        : ["scope", "protected", "approval", "integrity"];
    const checks = review.checks ?? [];
    return (
      checks.length === required.length &&
      new Set(checks.map((c) => c.id)).size === required.length &&
      checks.every(
        (c) =>
          required.includes(c.id) &&
          c.passed === true &&
          !!c.summary?.trim() &&
          (review.role === "qa"
            ? c.verification_id === evidence.id &&
              c.id === `acceptance:${c.acceptance_index}` &&
              !c.reference_digest
            : !!c.reference_digest &&
              !c.verification_id &&
              c.acceptance_index == null),
      )
    );
  });
}
export function VerificationView({ mission }: { mission: Mission }) {
  return (
    <>
      <ReplanSummary mission={mission} />
      <C4Verification mission={mission} />
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
        <section
          className="evidence-attempt"
          key={evidence.id}
          id={`verification-${evidence.id}`}
        >
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
            {review.coding_version === 1 ? (
              <>
                <p className="muted small">
                  计划 r{review.plan_revision} · 审查前执行轨迹{" "}
                  <code>{review.trace_digest}</code>
                </p>
                <p className="muted small">
                  绑定摘要 <code>{review.binding_digest}</code> ·
                  控制器在交付时重新核对
                </p>
                {!review.checks?.length && (
                  <p className="warning-copy">必需清单缺失，不能交付</p>
                )}
                {(review.checks ?? []).map((check, i) => (
                  <div key={`${check.id}:${i}`} className="command-result">
                    <strong>{check.id}</strong> ·{" "}
                    {check.passed === true
                      ? "模型判断：通过"
                      : check.passed === false
                        ? "模型判断：阻塞"
                        : "判断未记录"}
                    <p>{check.summary}</p>
                    {check.verification_id && (
                      <p>
                        验证引用{" "}
                        <a href={`#verification-${check.verification_id}`}>
                          {check.verification_id}
                        </a>{" "}
                        · 验收索引 {check.acceptance_index}
                      </p>
                    )}
                    {check.reference_digest && (
                      <p>
                        控制器引用 <code>{check.reference_digest}</code>
                      </p>
                    )}
                    <code>{(check.paths ?? []).join(" · ")}</code>
                  </div>
                ))}
              </>
            ) : (
              <p className="muted small">
                {review.coding_version
                  ? "不支持的审查清单版本，不能推断有效性"
                  : "历史审查未记录必需清单，不能补推为证据核对"}
              </p>
            )}
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
