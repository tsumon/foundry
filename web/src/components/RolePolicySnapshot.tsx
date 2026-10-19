import type {
  MissionRolePolicySnapshot,
  RolePolicyCapability,
  RolePolicyExecutionNotice,
  RolePolicyEvidenceErrorCode,
  RolePolicySpendClass,
} from "../rolePolicyTypes";
import { roleNames, roles } from "../types";

const capabilityLabels: Record<RolePolicyCapability, string> = {
  structured_output: "结构化输出",
  reasoning: "推理",
  coding: "编码",
  long_context: "长上下文",
  review: "审查",
  concise_explanation: "简洁说明",
};
const spendLabels: Record<RolePolicySpendClass, string> = {
  subscription_only: "仅订阅",
  interactive_package: "交互套餐",
  explicit_api: "明确 API 费用",
  unknown: "未知费用渠道",
};
const noticeLabels: Record<RolePolicyExecutionNotice, string> = {
  production_live_gate_closed: "真实运行门禁关闭",
  credentials_and_quota_unknown: "凭据与额度未知",
};
const evidenceStatusLabels = {
  completed: "调用返回完成",
  failed: "调用失败",
  outcome_unknown: "结果未知",
  interrupted_before_dispatch: "派发前中断",
} as const;
const evidenceEnvironmentLabels = {
  live: "真实环境记录",
  simulation: "本地模拟记录",
  unknown: "历史环境未知",
} as const;
const evidenceErrorLabels: Record<RolePolicyEvidenceErrorCode, string> = {
  auth_invalid: "认证无效",
  entitlement_denied: "权益拒绝",
  quota_exhausted: "额度耗尽",
  rate_limited: "请求限流",
  request_rejected: "请求被拒绝",
  server_error: "服务端错误",
  transport_unknown: "传输结果未知",
  model_transport_unknown: "模型传输结果未知",
  response_incomplete: "响应不完整",
  live_provider_access_pending_confirmation: "真实提供方访问等待确认",
  dispatch_interrupted: "派发中断",
  stale_dispatch: "派发已过期",
  result_seal_failed: "结果封存失败",
  interrupted_before_dispatch: "派发前中断",
  other_failure: "其他失败",
};

export function RolePolicySnapshot({
  snapshot,
  compact = false,
}: {
  snapshot: MissionRolePolicySnapshot;
  compact?: boolean;
}) {
  const content = (
    <>
      <p className="muted small">
        任务创建时确认于 {snapshot.confirmed_at}
        。这份历史来源只说明任务创建时的选择来源；不会授权模型调用、工具或交付，也不批准上下文披露或执行。
      </p>
      <div className="policy-snapshot-roles">
        {roles.map((role) => {
          const result = snapshot.roles[role];
          const selected = result?.candidates?.find(
            (candidate) => candidate.profile_id === result.selected_profile_id,
          );
          return (
            <article className="policy-snapshot-role" key={role}>
              <strong>{roleNames[role]}</strong>
              <code>{result?.selected_profile_id || "冻结选择缺失"}</code>
              {selected && (
                <>
                  <span>{spendLabels[selected.spend_class]}</span>
                  <p>{selected.rationale}</p>
                  <small>
                    用户声明，未独立验证：
                    {selected.capabilities
                      .map((value) => capabilityLabels[value])
                      .join("、") || "无"}
                  </small>
                  <small>
                    冻结能力要求：
                    {result.required_capabilities
                      .map((value) => capabilityLabels[value])
                      .join("、") || "无"}
                  </small>
                  <small>
                    {noticeLabels[selected.execution_notice]}
                    ；来源不是运行许可。
                  </small>
                  {selected.evidence && (
                    <details className="policy-frozen-evidence">
                      <summary>查看冻结调用收据</summary>
                      <p>
                        {evidenceStatusLabels[selected.evidence.status]} ·{" "}
                        {
                          evidenceEnvironmentLabels[
                            selected.evidence.environment
                          ]
                        }
                        ；不是能力验证。
                      </p>
                      <dl>
                        <dt>任务 ID</dt>
                        <dd>
                          <code>{selected.evidence.mission_id}</code>
                        </dd>
                        <dt>调用 ID</dt>
                        <dd>
                          <code>{selected.evidence.invocation_id}</code>
                        </dd>
                        <dt>角色</dt>
                        <dd>{roleNames[selected.evidence.role]}</dd>
                        <dt>结束时间</dt>
                        <dd>{selected.evidence.finished_at || "未知"}</dd>
                        <dt>错误原因</dt>
                        <dd>
                          {selected.evidence.error_code
                            ? evidenceErrorLabels[selected.evidence.error_code]
                            : "无记录"}
                        </dd>
                      </dl>
                    </details>
                  )}
                </>
              )}
            </article>
          );
        })}
      </div>
      <dl className="policy-snapshot-meta">
        <dt>决定 ID</dt>
        <dd>
          <code>{snapshot.decision_id}</code>
        </dd>
        <dt>建议摘要</dt>
        <dd>
          <code>{snapshot.recommendation_digest}</code>
        </dd>
        <dt>来源摘要</dt>
        <dd>
          <code>{snapshot.digest}</code>
        </dd>
      </dl>
    </>
  );
  return (
    <section
      className={`role-policy-snapshot${compact ? " role-policy-snapshot-compact" : ""}`}
      aria-label="冻结的角色策略来源"
    >
      <div className="section-heading">
        <h3>冻结的角色策略来源</h3>
        <span>策略版本 {snapshot.policy_revision}</span>
      </div>
      {compact ? (
        <details className="disclosure">
          <summary>查看创建时确认的七角色依据</summary>
          {content}
        </details>
      ) : (
        content
      )}
    </section>
  );
}
