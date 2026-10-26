import type {
  ModelAcceptanceOperation,
  ModelExchangeDiagnostic,
  ModelExchangePhase,
} from "../types";
import { ResponseDiagnostic } from "./ResponseDiagnostic";

const bounded = (n: unknown, min: number, max: number) =>
  typeof n === "number" && Number.isSafeInteger(n) && n >= min && n <= max;
const member = (s: unknown, values: string[]) =>
  typeof s === "string" && values.includes(s);
const hex = (s: unknown) => typeof s === "string" && /^[a-f0-9]{64}$/.test(s);
function phase(p: ModelExchangePhase): boolean {
  return (
    !!p &&
    ((p.state === undefined && p.attempts === 0 && p.millis === 0) ||
      (member(p.state, ["completed", "failed", "incomplete"]) &&
        bounded(p.attempts, 1, 32) &&
        bounded(p.millis, 0, 120000)))
  );
}
function valid(d: ModelExchangeDiagnostic): boolean {
  if (
    !d ||
    d.version !== 1 ||
    typeof d.client_request_id !== "string" ||
    !/^fdy_[a-f0-9]{32}$/.test(d.client_request_id) ||
    ![d.dns, d.connect, d.tls, d.headers].every(phase) ||
    d.headers.attempts !== 1 ||
    !bounded(d.first_byte_millis, -1, 120000) ||
    !bounded(d.request_writes, 0, 32) ||
    !bounded(d.header_fields, 0, 4096) ||
    typeof d.write_failed !== "boolean" ||
    typeof d.reused_connection !== "boolean" ||
    typeof d.tls_verified !== "boolean" ||
    (d.write_failed && d.request_writes === 0) ||
    !member(d.proxy_mode, ["disabled", "configured", "unknown"]) ||
    !member(d.peer_class, [
      "unknown",
      "loopback",
      "private",
      "public",
      "other",
    ]) ||
    !member(d.tls_version ?? "", ["", "tls12", "tls13", "other"]) ||
    !member(d.alpn ?? "", ["", "none", "http1_1", "h2", "other"])
  )
    return false;
  if (d.headers.state === "failed") {
    if (
      d.final_status !== 0 ||
      d.transport !== undefined ||
      d.header_fields !== 0
    )
      return false;
  } else if (
    d.headers.state !== "completed" ||
    !bounded(d.final_status, 0, 999) ||
    !d.transport ||
    d.transport.version !== 1 ||
    !member(d.transport.content_type, [
      "absent",
      "empty",
      "single",
      "multiple",
    ]) ||
    !member(d.transport.protocol, ["http1_0", "http1_1", "http2", "other"]) ||
    !member(d.transport.body_length, ["zero", "positive", "unknown"])
  )
    return false;
  if (d.tls.state !== "completed")
    return (
      !d.tls_version &&
      !d.alpn &&
      !d.tls_verified &&
      !d.peer_certificate_sha256 &&
      !d.issuer_sha256
    );
  if (!d.tls_version || !d.alpn) return false;
  return d.tls_verified
    ? hex(d.peer_certificate_sha256) &&
        (d.issuer_sha256 === undefined || hex(d.issuer_sha256))
    : !d.peer_certificate_sha256 && !d.issuer_sha256;
}

export function InferenceDiagnostic({
  value,
}: {
  value?: ModelAcceptanceOperation["inference_diagnostic"];
}) {
  if (value === undefined)
    return <p className="muted">未记录请求观察；历史回执不会回填。</p>;
  if (!value || !valid(value.exchange))
    return <p className="muted">请求观察不可验证；不展示未知字段。</p>;
  const d = value.exchange;
  return (
    <div aria-label="单次推理脱敏诊断">
      <p>
        请求关联 ID：<code>{d.client_request_id}</code>
      </p>
      <p>
        HTTP 状态：{d.final_status || "未知"} · 请求写入：{d.request_writes} ·
        响应头字段数：{d.header_fields}
      </p>
      <p>
        DNS / TCP / TLS：{d.dns.state ?? "未观察"} /{" "}
        {d.connect.state ?? "未观察"} / {d.tls.state ?? "未观察"} · 代理：
        {d.proxy_mode} · 对端类别：{d.peer_class}
      </p>
      {d.tls_verified && (
        <p>
          已验证证书指纹：<code>{d.peer_certificate_sha256}</code>
          {d.issuer_sha256 && (
            <>
              {" "}
              · 签发证书：<code>{d.issuer_sha256}</code>
            </>
          )}
        </p>
      )}
      {value.response && <ResponseDiagnostic value={value.response} />}
      <p className="muted">
        仅为客户端观察，不证明远端执行、扣费、故障归属或审批通过。
      </p>
    </div>
  );
}
