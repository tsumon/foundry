import type { ModelResponseDiagnostic } from "../types";

// No provider-controlled text is rendered, including unknown enum values.
const reasons: Record<string, [string, string, string]> = {
  missing_media_type: ["http_media", "", "响应缺少内容类型"],
  invalid_media_type: ["http_media", "", "响应内容类型无法解析"],
  unexpected_media_type: ["http_media", "", "响应不是所需的 SSE 类型"],
  missing_terminal: ["stream_read", "", "流结束时未收到终止事件"],
  unterminated_frame: ["stream_read", "", "流结束时仍有未闭合事件帧"],
  read_error: ["stream_read", "", "读取响应流时发生错误"],
  body_limit: ["stream_read", "", "响应流达到读取上限"],
  line_limit: ["stream_read", "", "响应流单行达到读取上限"],
  invalid_json: ["sse_decode", "", "事件 JSON 无法解析"],
  missing_event_type: ["sse_decode", "", "事件缺少类型"],
  event_type_mismatch: ["sse_decode", "", "事件名与内容类型不一致"],
  provider_incomplete: [
    "provider_terminal",
    "response.incomplete",
    "已收到提供方 incomplete 事件",
  ],
  provider_failed: [
    "provider_terminal",
    "response.failed",
    "已收到提供方 failed 事件",
  ],
  provider_error: ["provider_terminal", "error", "已收到提供方 error 事件"],
  invalid_envelope_json: [
    "completed_response",
    "response.completed",
    "完成响应的 JSON 结构无法解析",
  ],
  invalid_envelope: [
    "completed_response",
    "response.completed",
    "完成响应的对象、状态或错误字段未通过校验",
  ],
  invalid_output: [
    "completed_response",
    "response.completed",
    "完成响应的消息类型、角色或状态未通过校验",
  ],
  invalid_content: [
    "completed_response",
    "response.completed",
    "完成响应包含不接受的内容类型",
  ],
  empty_output: [
    "completed_response",
    "response.completed",
    "完成响应没有非空文本输出",
  ],
  credential_echo: [
    "output_guard",
    "response.completed",
    "输出命中凭据回显保护，未封存正文",
  ],
};
const stages: Record<string, string> = {
  http_media: "HTTP 内容类型校验",
  stream_read: "响应流读取",
  sse_decode: "SSE 事件解析",
  provider_terminal: "提供方终止事件",
  completed_response: "完成响应校验",
  output_guard: "输出安全校验",
};
const media: Record<string, string> = {
  sse: "SSE",
  json: "JSON",
  other: "其他类型",
  invalid: "类型无效",
  missing: "类型缺失",
};
const transportHeaders: Record<string, string> = {
  absent: "Content-Type 字段不存在",
  empty: "Content-Type 字段为空",
  single: "Content-Type 单一非空值",
  multiple: "Content-Type 多个值",
};
const transportProtocols: Record<string, string> = {
  http1_0: "HTTP/1.0",
  http1_1: "HTTP/1.1",
  http2: "HTTP/2",
  other: "其他协议",
};
const transportLengths: Record<string, string> = {
  zero: "0",
  positive: "大于 0",
  unknown: "未知",
};

function validTransport(d: ModelResponseDiagnostic): boolean {
  const t = d.transport;
  if (t === undefined) return true;
  if (
    !t ||
    t.version !== 1 ||
    typeof t.content_type !== "string" ||
    typeof t.protocol !== "string" ||
    typeof t.body_length !== "string" ||
    !Object.hasOwn(transportHeaders, t.content_type) ||
    !Object.hasOwn(transportProtocols, t.protocol) ||
    !Object.hasOwn(transportLengths, t.body_length)
  )
    return false;
  const missing = d.stage === "http_media" && d.reason === "missing_media_type";
  if (t.content_type === "absent" || t.content_type === "empty") return missing;
  return t.content_type !== "single" || !missing;
}

function validObservation(d: ModelResponseDiagnostic): boolean {
  if (!validTransport(d)) return false;
  if (d.stage === "http_media") {
    const kind =
      d.reason === "missing_media_type"
        ? "missing"
        : d.reason === "invalid_media_type"
          ? "invalid"
          : undefined;
    return (
      d.bytes_read === 0 &&
      d.events_seen === 0 &&
      (kind
        ? d.media_type === kind
        : d.media_type === "json" || d.media_type === "other")
    );
  }
  if (d.media_type !== "sse") return false;
  return (
    d.events_seen <= d.bytes_read &&
    (d.stage === "stream_read" || d.events_seen > 0)
  );
}

export function ResponseDiagnostic({
  value,
}: {
  value?: ModelResponseDiagnostic;
}) {
  if (value === undefined)
    return (
      <p className="muted">
        未记录分阶段诊断；不能据此区分提供方响应与本地解析问题。
      </p>
    );
  const d = value;
  const known =
    d && Object.hasOwn(reasons, d.reason) ? reasons[d.reason] : undefined;
  if (
    !known ||
    d.version !== 1 ||
    d.stage !== known[0] ||
    (d.terminal_event ?? "") !== known[1] ||
    !Number.isSafeInteger(d.bytes_read) ||
    d.bytes_read < 0 ||
    d.bytes_read > 2097153 ||
    !Number.isSafeInteger(d.events_seen) ||
    d.events_seen < 0 ||
    d.events_seen > 2097152 ||
    (d.media_type !== undefined && !Object.hasOwn(media, d.media_type)) ||
    !validObservation(d)
  )
    return (
      <p className="muted">
        分阶段诊断不可验证；不展示未知字段，也不推断失败来源。
      </p>
    );
  return (
    <div aria-label="响应分阶段诊断">
      <p>
        <strong>{stages[d.stage]}</strong> · {known[2]}
      </p>
      <div className="receipt-facts">
        {d.media_type && <span>内容类型：{media[d.media_type]}</span>}
        {known[1] && (
          <span>
            观察到的终止事件：<code>{known[1]}</code>
          </span>
        )}
        {d.stage !== "http_media" && (
          <span>
            已处理事件帧 {d.events_seen} · 已读取 {d.bytes_read}{" "}
            bytes（含缓冲预读，非 Token 用量）
          </span>
        )}
      </div>
      <p className="muted">
        仅说明失败观察点，不单独归因于服务端；HTTP 200 或 completed
        事件不等于有效输出或审批通过。
      </p>
      {d.transport ? (
        <div aria-label="脱敏传输观察">
          <p>HTTP 客户端返回时：{transportHeaders[d.transport.content_type]}</p>
          <p className="muted">
            {transportProtocols[d.transport.protocol]} · 客户端报告的正文长度：
            {transportLengths[d.transport.body_length]}（非实际读取字节数）
          </p>
        </div>
      ) : d.stage === "http_media" ? (
        <p className="muted">未记录传输层观察；不能区分字段不存在与空值。</p>
      ) : null}
      {d.stage === "http_media" && (
        <p className="muted">
          正文未解析；不代表响应为空，也不代表未消耗套餐额度。
        </p>
      )}
    </div>
  );
}
