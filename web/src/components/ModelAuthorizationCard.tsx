import { useEffect, useRef, useState } from "react";
import { APIError } from "../api";
import { modelAuthorizationApi } from "../modelAccountApi";
import type { ModelAcceptanceConnectionPolicy } from "./ModelAcceptanceOperations";
import type {
  ModelAccount,
  ModelAuthorization,
  ModelAuthorizationFlow,
  ModelRemoteRevocation,
  ModelRevocation,
  ModelRuntime,
} from "../types";

const flowActive = (flow?: ModelAuthorizationFlow) =>
  flow?.state === "waiting" || flow?.state === "exchanging";

const safeHandoff = (value: string) => {
  try {
    const url = new URL(value);
    const port = Number(url.port);
    return url.protocol === "http:" &&
      url.hostname === "127.0.0.1" &&
      Number.isInteger(port) &&
      port > 0 &&
      port <= 65535 &&
      url.username === "" &&
      url.password === "" &&
      url.pathname === "/auth/start" &&
      url.search === "" &&
      url.hash === "" &&
      url.href === value
      ? value
      : "";
  } catch {
    return "";
  }
};

const readRevocation = (cause: unknown): ModelRevocation | undefined => {
  if (
    !(cause instanceof APIError) ||
    !cause.data ||
    typeof cause.data !== "object"
  )
    return undefined;
  const value = (cause.data as { revocation?: unknown }).revocation;
  if (!value || typeof value !== "object") return undefined;
  const record = value as Record<string, unknown>;
  if (
    typeof record.authorization_id !== "string" ||
    typeof record.local_state !== "string" ||
    (record.remote_state !== "confirmed" &&
      record.remote_state !== "unconfirmed") ||
    typeof record.updated_at !== "string"
  )
    return undefined;
  return record as unknown as ModelRevocation;
};

const authorizationError = (cause: unknown) => {
  if (!(cause instanceof APIError))
    return "授权结果未知；不会自动重试。请显式检查或重新读取记录。";
  if (cause.code === "live_provider_access_pending_confirmation")
    return "账户路由真实授权尚未开放；本次未发起真实厂商访问。";
  if (
    cause.code === "conflict" ||
    cause.code === "model_authorization_in_progress"
  )
    return "授权操作冲突或已有流程进行中；不会自动重送。";
  if (cause.code === "refresh_not_ready")
    return "当前授权未满足刷新条件；不会自动重试。";
  if (
    cause.code === "auth_invalid" ||
    cause.code === "entitlement_denied" ||
    cause.code === "quota_exhausted" ||
    cause.code === "rate_limited" ||
    cause.code === "request_rejected"
  )
    return "授权请求已被拒绝；不会自动重试或切换付费通道。";
  if (
    cause.code === "transport_unknown" ||
    cause.code === "response_incomplete"
  )
    return "授权结果未知；不会自动重试。请显式检查或重新读取记录。";
  if (
    cause.code === "invalid_json" ||
    cause.code === "validation" ||
    cause.code === "invalid_model_authorization"
  )
    return "授权请求格式或绑定无效；请重新读取记录后检查。";
  if (cause.code === "not_found")
    return "未找到原授权流程或记录；不会创建替代流程。";
  return "授权服务当前不可用；状态未知，不会自动重试。";
};

const authorizationStates = new Set([
  "available",
  "refreshing",
  "refresh_unknown",
  "revoked",
  "failed",
]);
const validAuthorization = (value: unknown): value is ModelAuthorization => {
  if (!value || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  return (
    [
      "id",
      "account_id",
      "issuer",
      "subject",
      "client_id",
      "spend_class",
      "state",
      "expires_at",
    ].every((field) => typeof record[field] === "string") &&
    authorizationStates.has(record.state as string) &&
    Number.isSafeInteger(record.generation) &&
    (record.generation as number) > 0 &&
    Array.isArray(record.scopes) &&
    record.scopes.every((scope) => typeof scope === "string")
  );
};
const validRemoteRevocation = (
  value: unknown,
): value is ModelRemoteRevocation => {
  if (!value || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  return (
    typeof record.authorization_id === "string" &&
    (record.outcome === "confirmed" || record.outcome === "unconfirmed") &&
    typeof record.updated_at === "string"
  );
};

export function ModelAuthorizationCard({
  account,
  runtime,
  connectionPolicy = "unavailable",
  legacySIWCCapability = false,
}: {
  account: ModelAccount;
  runtime?: ModelRuntime;
  connectionPolicy?: ModelAcceptanceConnectionPolicy;
  legacySIWCCapability?: boolean;
}) {
  const [authorizations, setAuthorizations] = useState<ModelAuthorization[]>(
    [],
  );
  const [revocations, setRevocations] = useState<ModelRemoteRevocation[]>([]);
  const [flow, setFlow] = useState<ModelAuthorizationFlow>();
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const mounted = useRef(true);
  const epoch = useRef(0);
  const locked = useRef(false);

  const acceptHistory = (data: {
    authorizations?: ModelAuthorization[];
    remote_revocations?: ModelRemoteRevocation[];
  }) => {
    if (
      !Array.isArray(data.authorizations) ||
      !Array.isArray(data.remote_revocations) ||
      !data.authorizations.every(validAuthorization) ||
      !data.remote_revocations.every(validRemoteRevocation)
    )
      throw new Error("invalid model authorization snapshot");
    const snapshotAuthorizations = data.authorizations;
    const snapshotRevocations = data.remote_revocations;
    const accountAuthorizations = snapshotAuthorizations.filter(
      (authorization) => authorization.account_id === account.id,
    );
    setAuthorizations(accountAuthorizations);
    setRevocations((known) => {
      const received = new Set(
        snapshotRevocations.map((item) => item.authorization_id),
      );
      const revoked = new Set(
        accountAuthorizations
          .filter((item) => item.state === "revoked")
          .map((item) => item.id),
      );
      return [
        ...snapshotRevocations,
        ...known.filter(
          (item) =>
            item.outcome === "unconfirmed" &&
            revoked.has(item.authorization_id) &&
            !received.has(item.authorization_id),
        ),
      ];
    });
    setLoaded(true);
  };

  useEffect(() => {
    mounted.current = true;
    const requestEpoch = ++epoch.current;
    setAuthorizations([]);
    setRevocations([]);
    setFlow(undefined);
    setLoaded(false);
    locked.current = false;
    setBusy(false);
    setError("");
    setNotice("");
    void modelAuthorizationApi
      .list()
      .then((data) => {
        if (mounted.current && requestEpoch === epoch.current)
          acceptHistory(data);
      })
      .catch((cause) => {
        if (mounted.current && requestEpoch === epoch.current)
          setError(authorizationError(cause));
      });
    return () => {
      mounted.current = false;
      epoch.current++;
    };
  }, [account.id]);

  const run = async (operation: (valid: () => boolean) => Promise<void>) => {
    if (locked.current) return;
    locked.current = true;
    setBusy(true);
    setError("");
    setNotice("");
    const requestEpoch = epoch.current;
    const valid = () => mounted.current && requestEpoch === epoch.current;
    try {
      await operation(valid);
    } catch (cause) {
      if (valid()) setError(authorizationError(cause));
    } finally {
      locked.current = false;
      if (valid()) setBusy(false);
    }
  };

  const reload = () =>
    run(async (valid) => {
      const data = await modelAuthorizationApi.list();
      if (valid()) {
        acceptHistory(data);
        setNotice("授权历史已重新读取；这是当前元数据，不改写任务快照。");
      }
    });

  const mergeAuthorization = (authorization: ModelAuthorization) =>
    setAuthorizations((old) => [
      authorization,
      ...old.filter((item) => item.id !== authorization.id),
    ]);

  const applyRevocation = (revocation: ModelRevocation) => {
    setAuthorizations((old) =>
      old.map((item) =>
        item.id === revocation.authorization_id
          ? { ...item, state: revocation.local_state }
          : item,
      ),
    );
    setRevocations((old) => [
      {
        authorization_id: revocation.authorization_id,
        outcome: revocation.remote_state,
        updated_at: revocation.updated_at,
      },
      ...old.filter(
        (item) => item.authorization_id !== revocation.authorization_id,
      ),
    ]);
  };

  const reconcileRevocation = async (
    revocation: ModelRevocation,
    valid: () => boolean,
  ) => {
    if (!valid()) return;
    applyRevocation(revocation);
    const message =
      revocation.remote_state === "confirmed"
        ? "本地授权已撤销，远端撤销已确认。"
        : "本地撤销已持久保存；远端撤销未确认。";
    setNotice(message);
    try {
      const history = await modelAuthorizationApi.list();
      if (valid()) acceptHistory(history);
    } catch {
      if (valid())
        setNotice(`${message} 重新读取当前元数据未完成，安全回执仍保留。`);
    }
  };

  const revokeAuthorization = (authorizationID: string) =>
    run(async (valid) => {
      try {
        const result = await modelAuthorizationApi.revoke(
          authorizationID,
          account.revision,
        );
        await reconcileRevocation(result.revocation, valid);
      } catch (cause) {
        const revocation = readRevocation(cause);
        if (!revocation) throw cause;
        await reconcileRevocation(revocation, valid);
      }
    });

  const identityOnly = connectionPolicy === "identity_only";
  const subscriptionOnly =
    connectionPolicy === "subscription_only" ||
    connectionPolicy === "production_connection";
  const acceptanceManaged = identityOnly || subscriptionOnly;
  const legacyPolicy = legacySIWCCapability === true;
  const refreshable =
    legacyPolicy && !acceptanceManaged
      ? authorizations.find((item) => item.state === "available")
      : undefined;
  const hasOfflineAccess =
    refreshable?.scopes.includes("offline_access") ?? false;
  const hasDirectUse =
    refreshable?.scopes.includes("resource.invoke") &&
    refreshable.scopes.includes("chatgpt.tokens.use.direct");
  const simulation = runtime?.simulation_authorization === true;
  const handoff = flow?.handoff_url ? safeHandoff(flow.handoff_url) : "";

  return (
    <section
      className="model-authorization"
      aria-label={`${account.name} OpenAI SIWC 授权`}
    >
      <div className="section-heading">
        <div>
          <h3>OpenAI SIWC 授权</h3>
          <p className="muted">仅管理此账户的安全授权元数据与显式操作。</p>
        </div>
        <span className="route-lock">
          {simulation ? "本地模拟可用" : "直接登录与刷新关闭"}
        </span>
      </div>
      <p className="note">
        {identityOnly
          ? "本地身份专用模拟仅记录身份验证；不开放模型权限、刷新、目录或推理。不会自动打开浏览器、轮询或切换付费通道。"
          : subscriptionOnly
            ? "订阅连接由验收材料处理。请求 offline_access 不会开放应用刷新；目录、推理、自动轮询与付费通道切换保持禁用。"
            : "直接登录与刷新保持关闭；账户级真实订阅连接另由验收材料独立审批。旧式独立连接的既有行为保持不变。不会自动打开浏览器、轮询或切换付费通道。"}
      </p>
      {!loaded && (
        <p className="warning-copy">授权历史尚未读取，不能视为无授权或可用。</p>
      )}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      <div className="authorization-actions">
        {acceptanceManaged ? (
          <button type="button" disabled>
            {identityOnly ? "身份验证" : "订阅连接"}由验收材料处理
          </button>
        ) : simulation && legacyPolicy ? (
          <button
            type="button"
            disabled={busy || flowActive(flow)}
            onClick={() =>
              void run(async (valid) => {
                const result = await modelAuthorizationApi.start(
                  account.id,
                  account.revision,
                );
                if (valid()) {
                  setFlow(result.flow);
                  setNotice("本地模拟登录已创建；请手动打开本地交接页。");
                }
              })
            }
          >
            开始本地模拟登录
          </button>
        ) : legacyPolicy ? (
          <button
            type="button"
            disabled
            aria-describedby="authorization-live-reason"
          >
            真实登录（未开放）
          </button>
        ) : (
          <button type="button" disabled>
            本地模拟登录（等待路由策略）
          </button>
        )}
        <button type="button" disabled={busy} onClick={() => void reload()}>
          重新读取授权记录
        </button>
      </div>
      {!acceptanceManaged && legacyPolicy && !simulation && (
        <p id="authorization-live-reason" className="field-help">
          当前进程没有本地模拟授权能力；生产真实登录不会发起请求。
        </p>
      )}
      {flow && (
        <div className="authorization-flow" aria-label="本地模拟登录状态">
          <strong>本地模拟流程：{flow.state}</strong>
          <code>{flow.operation_id}</code>
          {flow.expires_at && <span>截止 {flow.expires_at}</span>}
          {flow.handoff_url && !handoff && (
            <p className="warning-copy">
              交接链接未通过本地安全检查，因此不会显示。
            </p>
          )}
          {handoff && (
            <a href={handoff} target="_blank" rel="noreferrer">
              打开本地模拟登录交接页
            </a>
          )}
          {flowActive(flow) && (
            <div className="authorization-actions">
              <button
                type="button"
                disabled={busy}
                onClick={() =>
                  void run(async (valid) => {
                    const result = await modelAuthorizationApi.status(
                      flow.operation_id,
                    );
                    if (valid()) setFlow(result.flow);
                  })
                }
              >
                检查本地模拟登录状态
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={() =>
                  void run(async (valid) => {
                    const result = await modelAuthorizationApi.cancel(
                      flow.operation_id,
                    );
                    if (valid()) setFlow(result.flow);
                  })
                }
              >
                取消本地模拟登录
              </button>
            </div>
          )}
        </div>
      )}
      {loaded && !authorizations.length && <p>尚无此账户的授权历史</p>}
      <div className="authorization-list">
        {authorizations.map((authorization) => {
          const remote = revocations.find(
            (item) => item.authorization_id === authorization.id,
          );
          const direct =
            authorization.scopes.includes("resource.invoke") &&
            authorization.scopes.includes("chatgpt.tokens.use.direct");
          return (
            <article key={authorization.id}>
              <strong>
                {authorization.state === "available"
                  ? direct
                    ? "身份与直接调用权限已记录（不代表当前可用）"
                    : "仅身份已确认；未获直接调用权限"
                  : `本地状态：${authorization.state}`}
              </strong>
              <code>{authorization.id}</code>
              <span>issuer {authorization.issuer}</span>
              <span>subject {authorization.subject}</span>
              <span>client {authorization.client_id}</span>
              <span>{authorization.spend_class}</span>
              {authorization.generation > 0 && (
                <span>当前令牌元数据：代数 {authorization.generation}</span>
              )}
              {authorization.expires_at && (
                <span>当前到期时间 {authorization.expires_at}</span>
              )}
              <div className="authorization-scopes">
                {authorization.scopes.map((scope) => (
                  <code key={scope}>{scope}</code>
                ))}
              </div>
              {remote && (
                <p
                  className={
                    remote.outcome === "confirmed" ? "note" : "warning-copy"
                  }
                >
                  {remote.outcome === "confirmed"
                    ? "远端撤销已确认"
                    : "远端撤销未确认"}
                  {remote.updated_at ? ` · ${remote.updated_at}` : ""}
                </p>
              )}
              {authorization.state !== "revoked" && (
                <div className="authorization-management">
                  <button
                    type="button"
                    aria-label={`撤销授权 ${authorization.id}`}
                    disabled={busy}
                    onClick={() => void revokeAuthorization(authorization.id)}
                  >
                    撤销此授权
                  </button>
                </div>
              )}
            </article>
          );
        })}
      </div>
      {refreshable && (
        <div className="authorization-management">
          <p className="field-help">
            {hasDirectUse
              ? "该授权记录包含直接调用权限；当前运行能力仍由后端门禁决定。"
              : "仅身份已确认；未获直接调用权限，不能用于模型推理。"}
          </p>
          {simulation ? (
            <button
              type="button"
              disabled={busy || !hasOfflineAccess}
              onClick={() =>
                void run(async (valid) => {
                  try {
                    const result = await modelAuthorizationApi.refresh(
                      refreshable.id,
                      account.revision,
                    );
                    if (valid()) {
                      mergeAuthorization(result.authorization);
                      setNotice("本地模拟授权已显式刷新。");
                    }
                  } catch (cause) {
                    if (valid()) {
                      setAuthorizations((old) =>
                        old.map((item) =>
                          item.id === refreshable.id
                            ? { ...item, state: "refresh_unknown" }
                            : item,
                        ),
                      );
                      setLoaded(false);
                    }
                    try {
                      const history = await modelAuthorizationApi.list();
                      if (valid()) acceptHistory(history);
                    } catch {
                      // Keep the conservative local state and unknown-read label.
                    }
                    throw cause;
                  }
                })
              }
            >
              刷新本地模拟授权
            </button>
          ) : (
            <button type="button" disabled>
              真实刷新（未开放）
            </button>
          )}
          {!hasOfflineAccess && (
            <p className="field-help">未授予 offline_access；不能刷新。</p>
          )}
        </div>
      )}
    </section>
  );
}
