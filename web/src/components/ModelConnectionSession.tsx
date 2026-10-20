import { useEffect, useState } from "react";
import type {
  ConnectionSession,
  ConnectionSessionState,
  ModelAcceptanceOperation,
} from "../types";

const states = new Set<ConnectionSessionState>([
  "waiting",
  "exchanging",
  "available",
  "declined",
  "failed",
  "canceled",
  "expired",
]);
const stateLabels: Record<ConnectionSessionState, string> = {
  waiting: "等待本地授权",
  exchanging: "正在完成本地交换",
  available: "连接证据已可用，等待回执完成",
  declined: "用户已拒绝",
  failed: "连接失败",
  canceled: "已取消",
  expired: "登录窗口已过期",
};
const rfc3339 =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/;

function safeHandoffURL(value: string): boolean {
  try {
    const url = new URL(value);
    const port = Number(url.port);
    const canonical = `http://127.0.0.1:${port}/auth/start`;
    return (
      url.protocol === "http:" &&
      url.hostname === "127.0.0.1" &&
      Number.isInteger(port) &&
      port > 0 &&
      port <= 65535 &&
      url.username === "" &&
      url.password === "" &&
      url.pathname === "/auth/start" &&
      url.search === "" &&
      url.hash === "" &&
      value === canonical &&
      url.href === value
    );
  } catch {
    return false;
  }
}

export function validConnectionSession(
  operation: ModelAcceptanceOperation,
  value: unknown,
  now = Date.now(),
): value is ConnectionSession {
  if (!value || typeof value !== "object") return false;
  const session = value as Record<string, unknown>;
  if (
    !(
      (operation.source === "local_simulation" &&
        operation.live_access === "blocked" &&
        operation.material.route.environment === "simulation" &&
        operation.material.production_admission === undefined) ||
      (operation.source === "production_connection" &&
        operation.live_access === "connection_only" &&
        operation.material.route.environment === "live" &&
        operation.material.route.base_url === "https://api.openai.com/v1" &&
        operation.material.production_admission?.profile ===
          "openai_siwc_subscription_v1" &&
        /^[a-f0-9]{32}$/.test(
          operation.material.production_admission.generation,
        ))
    ) ||
    Object.keys(session).some(
      (key) =>
        ![
          "operation_id",
          "account_id",
          "state",
          "expires_at",
          "handoff_url",
        ].includes(key),
    ) ||
    operation.phase !== "connection" ||
    operation.state !== "running" ||
    operation.attempts !== 1 ||
    operation.material.connection_purpose !== "subscription_only" ||
    operation.material.connection_mode !== "browser_handoff" ||
    operation.material.login_timeout_seconds !== 600 ||
    session.operation_id !== operation.id ||
    session.account_id !== operation.account_id ||
    typeof session.state !== "string" ||
    !states.has(session.state as ConnectionSessionState) ||
    typeof session.expires_at !== "string" ||
    !rfc3339.test(session.expires_at)
  )
    return false;
  const expires = Date.parse(session.expires_at);
  if (!Number.isFinite(expires)) return false;
  if (expires - now > 600 * 1000) return false;
  if (session.state === "waiting") {
    if (session.handoff_url === undefined) return true;
    return (
      typeof session.handoff_url === "string" &&
      expires > now &&
      safeHandoffURL(session.handoff_url)
    );
  }
  return session.handoff_url === undefined;
}

export function ModelConnectionSession({
  operation,
  session,
  disabled,
  onRead,
  onCancel,
  now = Date.now,
}: {
  operation: ModelAcceptanceOperation;
  session?: ConnectionSession;
  disabled: boolean;
  onRead: () => void;
  onCancel: () => void;
  now?: () => number;
}) {
  const production = operation.source === "production_connection";
  const trustedSession =
    !!session && validConnectionSession(operation, session, now());
  const [locallyExpired, setLocallyExpired] = useState(
    () => !!session && Date.parse(session.expires_at) <= now(),
  );

  useEffect(() => {
    setLocallyExpired(false);
    if (!session || session.state !== "waiting") return;
    const remaining = Date.parse(session.expires_at) - now();
    if (remaining <= 0) {
      setLocallyExpired(true);
      return;
    }
    if (!session.handoff_url) return;
    const timeout = window.setTimeout(
      () => setLocallyExpired(true),
      Math.min(remaining, 2_147_483_647),
    );
    return () => window.clearTimeout(timeout);
  }, [session?.operation_id, session?.expires_at, session?.handoff_url, now]);

  const liveLink =
    trustedSession &&
    !disabled &&
    session?.state === "waiting" &&
    !!session.handoff_url &&
    !locallyExpired;
  const cancellable =
    trustedSession &&
    !!session &&
    !locallyExpired &&
    (session.state === "waiting" || session.state === "exchanging");

  return (
    <section className="note" aria-label="浏览器连接会话">
      <h5>浏览器连接会话（{production ? "真实订阅连接" : "仅合成"}）</h5>
      {session &&
      validConnectionSession(
        operation,
        { ...session, handoff_url: undefined },
        now(),
      ) ? (
        <>
          <p>
            状态：{stateLabels[session.state]} · 人工登录绝对到期：
            <code>{session.expires_at}</code>
          </p>
          {liveLink && (
            <a
              href={session.handoff_url}
              target="_blank"
              rel="noopener noreferrer"
            >
              {production ? "手动打开真实连接授权入口" : "打开本地合成授权页"}
            </a>
          )}
          {locallyExpired && (
            <p className="warning-copy">
              本地显示期限已到；链接已移除。请显式读取连接状态，不会自动轮询。
            </p>
          )}
          {!liveLink && !locallyExpired && session.state === "waiting" && (
            <p className="warning-copy">
              本地授权链接不可用；不会恢复或猜测地址。请显式读取连接状态。
            </p>
          )}
          {!liveLink &&
            !locallyExpired &&
            session.state !== "waiting" &&
            session.state !== "exchanging" && (
              <p className="warning-copy">
                会话已终止；此批准的单次尝试已经消耗。再次尝试需要创建并独立批准新材料。
              </p>
            )}
        </>
      ) : (
        <p className="warning-copy">
          当前没有可用的浏览器连接会话；不会恢复或重建链接。请显式读取状态，再次尝试需要创建并独立批准新材料。
        </p>
      )}
      <div className="actions acceptance-actions">
        <button type="button" disabled={disabled} onClick={onRead}>
          读取连接状态
        </button>
        {cancellable && (
          <button type="button" disabled={disabled} onClick={onCancel}>
            取消连接会话
          </button>
        )}
      </div>
      <p className="field-help">
        仅打开字面量 127.0.0.1
        本地页；不会自动启动浏览器、重试、刷新、查询目录或运行推理。
      </p>
    </section>
  );
}
