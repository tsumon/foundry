import type { ModelConnectionDiagnostic } from "../types";

const stageLabels: Record<ModelConnectionDiagnostic["stage"], string> = {
  callback_validation: "回调校验",
  browser_authorization: "浏览器授权",
  authorization_intent: "授权意图",
  token_exchange: "令牌交换",
  grant_validation: "授权范围校验",
  oidc_discovery: "OIDC 配置发现",
  identity_validation: "身份凭证校验",
  credential_commit: "本地凭据写入",
  session: "连接会话",
};

const codeLabels: Record<ModelConnectionDiagnostic["code"], string> = {
  auth_invalid: "授权信息未通过安全校验",
  entitlement_denied: "当前账户不符合这次连接要求",
  quota_exhausted: "提供方报告额度不可用",
  rate_limited: "提供方限制了这次请求",
  request_rejected: "请求在发送前被拒绝",
  server_error: "提供方服务返回错误",
  transport_unknown: "连接结果无法确认",
  response_incomplete: "提供方响应不完整或无法验证",
  canceled: "连接由本地取消",
  expired: "连接窗口已过期",
  storage_error: "本地凭据存储未能完成",
};

const credentialLabels: Record<
  ModelConnectionDiagnostic["credential_commit"],
  string
> = {
  not_committed: "未提交",
  committed: "已提交",
  unknown: "未知",
};

const cleanupLabels: Record<ModelConnectionDiagnostic["cleanup"], string> = {
  not_required: "不需要",
  completed: "已完成",
  pending: "待处理",
  unknown: "未知",
};

const providerCodes = new Set([
  "invalid_client",
  "invalid_grant",
  "subscription_sharing_user_not_eligible",
  "invalid_refresh_token",
  "token_expired",
  "refresh_token_expired",
  "refresh_token_invalidated",
  "refresh_token_reused",
]);

function validDiagnostic(value: unknown): value is ModelConnectionDiagnostic {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const record = value as Record<string, unknown>;
  const statusValid =
    record.http_status === undefined ||
    (Number.isInteger(record.http_status) &&
      Number(record.http_status) >= 400 &&
      Number(record.http_status) <= 599);
  const providerValid =
    record.provider_code === undefined ||
    (typeof record.provider_code === "string" &&
      providerCodes.has(record.provider_code) &&
      record.http_status !== undefined);
  const credentialCleanupValid =
    record.credential_commit !== "not_committed" ||
    record.cleanup !== "pending";
  return (
    record.version === 1 &&
    typeof record.stage === "string" &&
    Object.hasOwn(stageLabels, record.stage) &&
    typeof record.code === "string" &&
    Object.hasOwn(codeLabels, record.code) &&
    typeof record.credential_commit === "string" &&
    Object.hasOwn(credentialLabels, record.credential_commit) &&
    typeof record.cleanup === "string" &&
    Object.hasOwn(cleanupLabels, record.cleanup) &&
    statusValid &&
    providerValid &&
    credentialCleanupValid
  );
}

export function ConnectionDiagnostic({ diagnostic }: { diagnostic?: unknown }) {
  const safe = validDiagnostic(diagnostic) ? diagnostic : undefined;
  return (
    <section className="note" aria-label="连接失败诊断">
      <h5>连接失败诊断</h5>
      {safe ? (
        <dl>
          <dt>失败阶段</dt>
          <dd>{stageLabels[safe.stage]}</dd>
          <dt>安全说明</dt>
          <dd>{codeLabels[safe.code]}</dd>
          {safe.http_status !== undefined && (
            <>
              <dt>提供方状态</dt>
              <dd>HTTP {safe.http_status}</dd>
            </>
          )}
          {safe.provider_code !== undefined && (
            <>
              <dt>提供方代码</dt>
              <dd>
                <code>{safe.provider_code}</code>
              </dd>
            </>
          )}
          <dt>本地凭据提交</dt>
          <dd>{credentialLabels[safe.credential_commit]}</dd>
          <dt>本地清理</dt>
          <dd>{cleanupLabels[safe.cleanup]}</dd>
        </dl>
      ) : (
        <>
          <p>阶段未记录</p>
          <p>较早的连接回执没有保存诊断阶段，或记录无法安全识别。</p>
        </>
      )}
      <p className="warning-copy">
        当前尝试不会重试。如需再次尝试，请创建并独立批准新的连接材料。远端授权状态仍未验证。
      </p>
    </section>
  );
}
