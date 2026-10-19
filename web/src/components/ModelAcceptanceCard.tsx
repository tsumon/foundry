import { useEffect, useRef, useState } from "react";
import { modelAccountApi } from "../modelAccountApi";
import type { ModelAccountAcceptance, ModelAcceptanceReason } from "../types";

const reasons: Record<ModelAcceptanceReason, string> = {
  live_provider_access_pending_confirmation:
    "真实厂商访问尚待另行确认，本版本保持关闭",
  account_disabled: "账户已停用",
  route_missing: "尚无账户路由",
  route_invalid: "路由元数据无效",
  credential_metadata_unavailable: "凭据元数据不可用；未读取密钥",
  authorization_unavailable: "授权元数据不可用",
  authorization_expired: "授权已过期；不会自动刷新",
  inference_scope_missing: "身份授权不含直接调用权限",
  extra_usage_local_setting_unknown: "本地额外用量声明未知",
  remote_billing_unverified: "远端计费未验证；本地 off 不证明免费",
  task_approval_required: "任务需独立审批，本次未评估",
};
const phases = {
  local_metadata: "本地元数据",
  login: "登录",
  catalog: "目录",
  inference: "推理",
  task: "任务",
};
const states = {
  ready: "本地准备就绪（不代表可调用）",
  blocked: "阻止",
  not_evaluated: "未评估",
};
const adapters = [
  "openai_api_chat",
  "openai_api_responses",
  "openai_siwc_responses",
  "anthropic_api_messages",
  "zhipu_api_chat",
  "kimi_api_chat",
  "kimi_code_chat",
  "unknown",
];
type Props = { accountId: string; accountRevision: number };

function validReport(
  report: ModelAccountAcceptance,
  { accountId, accountRevision }: Props,
) {
  const validReasons = (codes: ModelAcceptanceReason[]) =>
    Array.isArray(codes) && codes.every((code) => Object.hasOwn(reasons, code));
  return (
    report &&
    report.account_id === accountId &&
    report.account_revision === accountRevision &&
    report.scope === "account_routes_only" &&
    report.live_access === "blocked" &&
    report.quota === "unknown" &&
    Array.isArray(report.checks) &&
    report.checks.length === 5 &&
    report.checks.every(
      (check, index) =>
        check &&
        check.phase === Object.keys(phases)[index] &&
        validReasons(check.reason_codes) &&
        (index === 0
          ? ["ready", "blocked"].includes(check.state)
          : check.state === (index === 4 ? "not_evaluated" : "blocked")),
    ) &&
    Array.isArray(report.routes) &&
    report.routes.every(
      (route) =>
        route &&
        typeof route.id === "string" &&
        /^[A-Za-z0-9_-]{1,256}$/.test(route.id) &&
        adapters.includes(route.adapter) &&
        ["live", "simulation", "unknown"].includes(route.environment) &&
        ["ready", "blocked"].includes(route.local_metadata) &&
        ["off", "unknown"].includes(route.local_extra_usage) &&
        route.remote_extra_usage === "unverified" &&
        validReasons(route.reason_codes),
    )
  );
}

export function ModelAcceptanceCard(props: Props) {
  // A new identity/revision cannot inherit a previous diagnostic or its pending request.
  return (
    <AcceptanceRead
      key={`${props.accountId}:${props.accountRevision}`}
      {...props}
    />
  );
}

function AcceptanceRead(props: Props) {
  const [report, setReport] = useState<ModelAccountAcceptance>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const active = useRef(true);
  const reading = useRef(false);
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  const inspect = async () => {
    if (reading.current) return;
    reading.current = true;
    setBusy(true);
    setReport(undefined);
    setError(false);
    try {
      const { acceptance } = await modelAccountApi.acceptance(props.accountId);
      if (!active.current) return;
      if (!validReport(acceptance, props))
        throw new Error("invalid acceptance snapshot");
      setReport(acceptance);
    } catch {
      if (active.current) {
        setReport(undefined);
        setError(true);
      }
    } finally {
      reading.current = false;
      if (active.current) setBusy(false);
    }
  };
  return (
    <section className="connection-card" aria-label="本地验收检查">
      <h4>本地验收准备</h4>
      <p className="field-help">
        仅检查所选账户路由的本地元数据；不读取密钥、不登录、不请求厂商、不消耗调用预算。旧版无账户连接不在范围内。
      </p>
      <p>真实访问：关闭 · 额度：未知</p>
      <button type="button" disabled={busy} onClick={() => void inspect()}>
        本地验收检查
      </button>
      {busy && <p role="status">正在读取本地元数据…</p>}
      {error && (
        <p role="alert">
          本地验收读取失败；状态未知，旧报告已清除。不会自动重试，请检查账户版本后显式重新读取。
        </p>
      )}
      {report ? (
        <>
          <p>账户版本 r{report.account_revision} · 时间点诊断，不是执行许可</p>
          <ul>
            {report.checks.map((check) => (
              <li key={check.phase}>
                <strong>
                  {phases[check.phase]}：{states[check.state]}
                </strong>
                {check.reason_codes.map((code) => (
                  <p className="field-help" key={code}>
                    {reasons[code]}
                  </p>
                ))}
              </li>
            ))}
          </ul>
          {report.routes.length === 0 && <p>尚无账户路由</p>}
          <ul>
            {report.routes.map((route) => (
              <li key={route.id}>
                <code>{route.id}</code>
                <p>
                  {route.adapter} · {route.environment} ·{" "}
                  {states[route.local_metadata]}
                </p>
                <p>
                  本地额外用量声明：
                  {route.local_extra_usage === "off"
                    ? "关闭（用户声明）"
                    : "未知"}{" "}
                  · 远端额外用量：未验证
                </p>
                {route.reason_codes.map((code) => (
                  <p className="field-help" key={code}>
                    {reasons[code]}
                  </p>
                ))}
              </li>
            ))}
          </ul>
        </>
      ) : (
        !busy && <p className="field-help">尚无当前版本的验收报告</p>
      )}
    </section>
  );
}
