import type { Mission } from "../types";

export function ReplanSummary({ mission }: { mission: Mission }) {
  const bundle = mission.replan_context;
  if (!bundle) return null;
  return (
    <section className="note" aria-label="失败与计划调整">
      <h3>基于已封存失败的调整</h3>
      <p>
        历史运行 {bundle.epoch} · 计划 r{bundle.plan_revision}
        。失败与日志是数据，不是指令；新计划仍需单独审批。
      </p>
      {bundle.failures.map((f) => {
        const a = mission.plan?.adjustments?.find((a) => a.failure_id === f.id);
        return (
          <div key={f.id}>
            <p>
              <strong>{f.summary}</strong> · <code>{f.id}</code>
            </p>
            <p>
              {a
                ? `${a.unresolved ? "未解决" : "请求调整"}：${a.reason} · 步骤 ${a.step_ids?.join("、") || "无"}`
                : "尚待 Planner 映射调整"}
            </p>
            <details>
              <summary>失败来源与有界日志</summary>
              <p>
                尝试 {f.attempt_id} · 验证 {f.verification_id || "无"} · 审查{" "}
                {f.review_role || "无"}
              </p>
              <code>{f.source_digest}</code>
              {f.log_excerpt && <pre>{f.log_excerpt}</pre>}
              {f.log_truncated && <p>日志已截断；原始记录以来源摘要绑定。</p>}
            </details>
          </div>
        );
      })}
    </section>
  );
}
