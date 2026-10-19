import { useEffect, useMemo, useRef, useState } from "react";
import {
  modelAcceptanceApi,
  modelAcceptanceError,
  type ModelAcceptanceError,
} from "../modelAcceptanceApi";
import type {
  ModelAccount,
  ModelAcceptanceHistory,
  ModelAcceptanceOperation,
  ModelAcceptancePhase,
} from "../types";

const phaseLabels: Record<ModelAcceptancePhase, string> = {
  connection: "连接",
  catalog: "目录",
  inference: "推理",
};
const stateLabels: Record<ModelAcceptanceOperation["state"], string> = {
  pending: "待审批",
  approved: "已批准，尚未执行",
  running: "已消耗，执行中",
  succeeded: "已成功",
  failed: "已失败",
  unknown: "结果未知",
  rejected: "已拒绝",
  expired: "已过期",
  invalidated: "已失效",
};
const phases: ModelAcceptancePhase[] = ["connection", "catalog", "inference"];
const receiptCodes = new Set([
  "auth_invalid",
  "entitlement_denied",
  "quota_exhausted",
  "rate_limited",
  "request_rejected",
  "server_error",
  "transport_unknown",
  "response_incomplete",
]);
const identifier = /^[A-Za-z0-9_-]{1,256}$/;
const digest = /^[a-f0-9]{64}$/;
const identityScopes = ["openid", "profile", "email"] as const;
const subscriptionScopes = [
  "openid",
  "profile",
  "email",
  "offline_access",
  "resource.invoke",
  "chatgpt.tokens.use.direct",
] as const;
const subscriptionResource = "https://api.openai.com/v1";
const identityAdapter = "openai_siwc_responses";
const identitySpendClass = "subscription_only";
const scopeMeanings: Record<(typeof subscriptionScopes)[number], string> = {
  openid: "用于身份协议登录",
  profile: "读取基础个人资料",
  email: "读取账户邮箱声明",
  offline_access: "请求离线授权权限；应用刷新仍禁用",
  "resource.invoke": "请求资源调用权限",
  "chatgpt.tokens.use.direct": "请求订阅令牌直接使用权限",
};

export type ModelAcceptanceConnectionPolicy =
  "identity_only" | "subscription_only" | "legacy" | "unavailable";

function validConnectionPurpose(
  value: unknown,
): value is "identity_only" | "subscription_only" | undefined {
  return (
    value === undefined ||
    value === "identity_only" ||
    value === "subscription_only"
  );
}

function exactScopes(value: unknown, expected: readonly string[]): boolean {
  return (
    Array.isArray(value) &&
    value.length === expected.length &&
    value.every((scope, index) => scope === expected[index])
  );
}

function validConnectionMaterial(
  value: ModelAcceptanceOperation,
  accountID: string,
  trustedRoute?: ModelAcceptanceHistory["routes"][number],
): boolean {
  const material = value.material as unknown;
  if (!material || typeof material !== "object") return false;
  const record = material as Record<string, unknown>;
  if (!validConnectionPurpose(record.connection_purpose)) return false;
  if (record.connection_purpose === undefined)
    return (
      record.requested_scopes === undefined &&
      record.requested_resource === undefined
    );
  const identityOnly = record.connection_purpose === "identity_only";
  const permissionsValid = identityOnly
    ? exactScopes(record.requested_scopes, identityScopes) &&
      record.requested_resource === undefined
    : exactScopes(record.requested_scopes, subscriptionScopes) &&
      record.requested_resource === subscriptionResource;
  const account = record.account;
  const materialRoute = record.route;
  if (
    value.phase !== "connection" ||
    !permissionsValid ||
    !account ||
    typeof account !== "object" ||
    !materialRoute ||
    typeof materialRoute !== "object" ||
    record.deadline_seconds !== 30 ||
    record.max_attempts !== 1
  )
    return false;
  const accountRecord = account as Record<string, unknown>;
  const route = materialRoute as Record<string, unknown>;
  const created = Date.parse(String(record.created_at));
  const expires = Date.parse(String(record.expires_at));
  const snapshotValid =
    accountRecord.id === accountID &&
    accountRecord.provider === "openai" &&
    accountRecord.auth_method === "openai_siwc" &&
    accountRecord.billing_mode === "subscription" &&
    route.account_id === accountID &&
    route.adapter === identityAdapter &&
    route.environment === "simulation" &&
    route.spend_class === identitySpendClass &&
    route.extra_usage === "off" &&
    Number.isFinite(created) &&
    Number.isFinite(expires) &&
    expires - created === 10 * 60 * 1000;
  if (!snapshotValid || !trustedRoute) return snapshotValid;
  return (
    route.id === trustedRoute.id &&
    route.adapter === trustedRoute.adapter &&
    route.environment === trustedRoute.environment &&
    route.spend_class === trustedRoute.spend_class &&
    route.base_url === trustedRoute.base_url
  );
}

function validRoute(
  route: unknown,
): route is ModelAcceptanceHistory["routes"][number] {
  if (!route || typeof route !== "object") return false;
  const record = route as Record<string, unknown>;
  return (
    record.environment === "simulation" &&
    typeof record.id === "string" &&
    identifier.test(record.id) &&
    typeof record.adapter === "string" &&
    typeof record.spend_class === "string" &&
    typeof record.base_url === "string" &&
    validConnectionPurpose(record.connection_purpose)
  );
}

function isTrustedIdentityRoute(
  route: ModelAcceptanceHistory["routes"][number],
): boolean {
  return (
    route.connection_purpose === "identity_only" &&
    route.adapter === identityAdapter &&
    route.environment === "simulation" &&
    route.spend_class === identitySpendClass
  );
}

function isTrustedSubscriptionRoute(
  route: ModelAcceptanceHistory["routes"][number],
): boolean {
  return (
    route.connection_purpose === "subscription_only" &&
    route.adapter === identityAdapter &&
    route.environment === "simulation" &&
    route.spend_class === identitySpendClass
  );
}

export function connectionPolicy(
  history: ModelAcceptanceHistory,
): ModelAcceptanceConnectionPolicy {
  if (!history.simulation_enabled) return "unavailable";
  return routePolicy(history.routes);
}

function routePolicy(
  routes: ModelAcceptanceHistory["routes"],
): ModelAcceptanceConnectionPolicy {
  if (routes.length === 0) return "unavailable";
  const purposes = routes.map((route) => route.connection_purpose);
  if (
    purposes.every((purpose) => purpose === "identity_only") &&
    routes.every(isTrustedIdentityRoute)
  )
    return "identity_only";
  if (
    purposes.every((purpose) => purpose === "subscription_only") &&
    routes.every(isTrustedSubscriptionRoute)
  )
    return "subscription_only";
  if (purposes.every((purpose) => purpose === undefined)) return "legacy";
  return "unavailable";
}

function validOperation(
  value: ModelAcceptanceOperation,
  accountID: string,
  trustedRoute?: ModelAcceptanceHistory["routes"][number],
): boolean {
  return (
    !!value &&
    identifier.test(value.id) &&
    value.account_id === accountID &&
    phases.includes(value.phase) &&
    Object.hasOwn(stateLabels, value.state) &&
    Number.isSafeInteger(value.revision) &&
    value.revision > 0 &&
    Number.isSafeInteger(value.claim_sequence) &&
    value.claim_sequence >= 0 &&
    value.source === "local_simulation" &&
    value.live_access === "blocked" &&
    value.quota === "unknown" &&
    value.remote_billing === "unverified" &&
    !!value.material &&
    validConnectionMaterial(value, accountID, trustedRoute) &&
    value.material.phase === value.phase &&
    value.material.account.id === accountID &&
    digest.test(value.material_digest) &&
    (value.code === undefined || receiptCodes.has(value.code)) &&
    Array.isArray(value.catalog) &&
    value.catalog.every(
      (entry) =>
        identifier.test(entry.id) &&
        typeof entry.display_name === "string" &&
        entry.display_name.length <= 256,
    ) &&
    (value.material.connection_purpose !== "subscription_only" ||
      (value.catalog.length === 0 &&
        value.usage_known === false &&
        value.input_tokens === undefined &&
        value.output_tokens === undefined))
  );
}

function operationRoute(
  operation: ModelAcceptanceOperation,
  routes: ModelAcceptanceHistory["routes"],
) {
  const id = operation.material?.route?.id;
  return typeof id === "string"
    ? routes.find((route) => route.id === id)
    : undefined;
}

function validOperationForSnapshot(
  operation: ModelAcceptanceOperation,
  accountID: string,
  history: ModelAcceptanceHistory,
): boolean {
  const policy = connectionPolicy(history);
  if (policy === "unavailable") return false;
  const trustedRoute =
    policy === "identity_only" || policy === "subscription_only"
      ? operationRoute(operation, history.routes)
      : undefined;
  return (
    (policy === "legacy" || !!trustedRoute) &&
    validOperation(operation, accountID, trustedRoute) &&
    (policy === "identity_only" || policy === "subscription_only"
      ? operation.phase === "connection" &&
        operation.material.connection_purpose === policy
      : operation.material.connection_purpose === undefined)
  );
}

function validHistory(
  value: ModelAcceptanceHistory,
  accountID: string,
): boolean {
  return (
    !!value &&
    typeof value.simulation_enabled === "boolean" &&
    (value.legacy_siwc_simulation_enabled === undefined ||
      typeof value.legacy_siwc_simulation_enabled === "boolean") &&
    Array.isArray(value.operations) &&
    Array.isArray(value.routes) &&
    value.routes.every(validRoute) &&
    (() => {
      const policy = routePolicy(value.routes);
      if (!value.simulation_enabled) {
        if (value.routes.length === 0)
          return value.operations.every((operation) =>
            validOperation(operation, accountID),
          );
        return (
          policy !== "unavailable" &&
          value.operations.every((operation) => {
            if (policy === "legacy")
              return (
                operation.material.connection_purpose === undefined &&
                validOperation(operation, accountID)
              );
            const trustedRoute = operationRoute(operation, value.routes);
            return (
              !!trustedRoute &&
              validOperation(operation, accountID, trustedRoute) &&
              operation.phase === "connection" &&
              operation.material.connection_purpose === policy
            );
          })
        );
      }
      return (
        policy !== "unavailable" &&
        value.operations.every((operation) =>
          validOperationForSnapshot(operation, accountID, value),
        )
      );
    })()
  );
}

function latestClaimed(
  operations: ModelAcceptanceOperation[],
  phase: ModelAcceptancePhase,
  predicate: (operation: ModelAcceptanceOperation) => boolean = () => true,
) {
  return operations
    .filter(
      (operation) =>
        operation.phase === phase &&
        operation.claim_sequence > 0 &&
        predicate(operation),
    )
    .reduce<ModelAcceptanceOperation | undefined>(
      (latest, operation) =>
        !latest || operation.claim_sequence > latest.claim_sequence
          ? operation
          : latest,
      undefined,
    );
}

export function ModelAcceptanceOperations({
  account,
  refreshAccount,
  onConnectionPolicy,
  onLegacySIWCCapability,
}: {
  account: ModelAccount;
  refreshAccount: () => Promise<void>;
  onConnectionPolicy?: (policy: ModelAcceptanceConnectionPolicy) => void;
  onLegacySIWCCapability?: (enabled: boolean) => void;
}) {
  return (
    <AcceptanceOperations
      key={`${account.id}:${account.revision}`}
      account={account}
      refreshAccount={refreshAccount}
      onConnectionPolicy={onConnectionPolicy}
      onLegacySIWCCapability={onLegacySIWCCapability}
    />
  );
}

function AcceptanceOperations({
  account,
  refreshAccount,
  onConnectionPolicy,
  onLegacySIWCCapability,
}: {
  account: ModelAccount;
  refreshAccount: () => Promise<void>;
  onConnectionPolicy?: (policy: ModelAcceptanceConnectionPolicy) => void;
  onLegacySIWCCapability?: (enabled: boolean) => void;
}) {
  const [snapshot, setSnapshot] = useState<ModelAcceptanceHistory>();
  const [loaded, setLoaded] = useState(false);
  const [reading, setReading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ModelAcceptanceError>();
  const [routeID, setRouteID] = useState("");
  const [model, setModel] = useState("");
  const [selected, setSelected] = useState<
    Partial<Record<ModelAcceptancePhase, string>>
  >({});
  const [unresolved, setUnresolved] = useState<{
    operationID?: string;
  }>();
  const lifecycle = useRef({ active: true, epoch: 0, locked: false });

  const clearSnapshot = () => {
    setSnapshot(undefined);
    setSelected({});
    setRouteID("");
    setModel("");
  };
  const acceptHistory = (history: ModelAcceptanceHistory) => {
    if (!validHistory(history, account.id)) throw new Error("invalid receipt");
    setSnapshot(history);
    setLoaded(true);
    onConnectionPolicy?.(connectionPolicy(history));
    onLegacySIWCCapability?.(history.legacy_siwc_simulation_enabled === true);
    setRouteID((current) =>
      history.routes.some((route) => route.id === current)
        ? current
        : (history.routes[0]?.id ?? ""),
    );
  };
  const readHistory = async () => {
    const epoch = lifecycle.current.epoch;
    setReading(true);
    setError(undefined);
    clearSnapshot();
    onConnectionPolicy?.("unavailable");
    onLegacySIWCCapability?.(false);
    try {
      const history = await modelAcceptanceApi.history(account.id);
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      acceptHistory(history);
    } catch (cause) {
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      const decoded =
        cause instanceof Error && cause.message === "invalid receipt"
          ? { ...modelAcceptanceError(cause), code: "invalid_receipt" }
          : modelAcceptanceError(cause);
      setError(decoded);
      setLoaded(false);
      onConnectionPolicy?.("unavailable");
      onLegacySIWCCapability?.(false);
    } finally {
      if (lifecycle.current.active && lifecycle.current.epoch === epoch)
        setReading(false);
    }
  };

  useEffect(() => {
    lifecycle.current.active = true;
    lifecycle.current.epoch++;
    lifecycle.current.locked = false;
    onConnectionPolicy?.("unavailable");
    onLegacySIWCCapability?.(false);
    void readHistory();
    return () => {
      lifecycle.current.active = false;
      lifecycle.current.epoch++;
      lifecycle.current.locked = false;
    };
  }, [account.id, account.revision]);

  const operations = snapshot?.operations ?? [];
  const policy = snapshot ? connectionPolicy(snapshot) : "unavailable";
  const identityOnly = policy === "identity_only";
  const subscriptionOnly = policy === "subscription_only";
  const visiblePhases = subscriptionOnly
    ? (["connection"] as ModelAcceptancePhase[])
    : phases;
  // Connection authority is dispatch order, never response/list/proposal/time order.
  const latestConnection = useMemo(
    () => latestClaimed(operations, "connection"),
    [operations],
  );
  const latestResultAccountRevision = latestConnection?.result_account_revision;
  const connection =
    latestConnection?.state === "succeeded" &&
    typeof latestResultAccountRevision === "number" &&
    Number.isSafeInteger(latestResultAccountRevision) &&
    latestResultAccountRevision >= account.revision
      ? latestConnection
      : undefined;
  const catalog = useMemo(
    () =>
      connection
        ? latestClaimed(
            operations,
            "catalog",
            (operation) =>
              operation.state === "succeeded" &&
              operation.material.connection_id === connection.id,
          )
        : undefined,
    [operations, connection?.id],
  );
  useEffect(() => {
    const models = catalog?.catalog ?? [];
    setModel((current) =>
      models.some((entry) => entry.id === current)
        ? current
        : (models[0]?.id ?? ""),
    );
  }, [catalog?.id, catalog?.revision]);

  const replaceOperation = (operation: ModelAcceptanceOperation) => {
    if (
      !snapshot ||
      !validOperationForSnapshot(operation, account.id, snapshot)
    )
      throw new Error("invalid receipt");
    setSnapshot((current) =>
      current
        ? {
            ...current,
            operations: [
              operation,
              ...current.operations.filter((item) => item.id !== operation.id),
            ],
          }
        : current,
    );
    setSelected((current) => ({ ...current, [operation.phase]: operation.id }));
  };
  const runMutation = async (
    operationID: string | undefined,
    work: () => Promise<{ operation: ModelAcceptanceOperation }>,
    after?: (operation: ModelAcceptanceOperation) => Promise<void>,
  ) => {
    if (lifecycle.current.locked || unresolved) return;
    lifecycle.current.locked = true;
    const epoch = lifecycle.current.epoch;
    setBusy(true);
    setError(undefined);
    try {
      const result = await work();
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      replaceOperation(result.operation);
      if (after) {
        try {
          await after(result.operation);
        } catch {
          if (lifecycle.current.active && lifecycle.current.epoch === epoch)
            setError({
              code: "account_refresh_failed",
              message:
                "连接回执已保存，但账户详情刷新失败；验收回执不会被清除。请显式重新打开或读取账户。",
              requiresRead: false,
            });
        }
      }
    } catch (cause) {
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      const decoded =
        cause instanceof Error && cause.message === "invalid receipt"
          ? { ...modelAcceptanceError(cause, true), code: "invalid_receipt" }
          : modelAcceptanceError(cause, true);
      setError(decoded);
      if (decoded.code === "invalid_receipt") {
        clearSnapshot();
        setLoaded(false);
        onConnectionPolicy?.("unavailable");
        onLegacySIWCCapability?.(false);
      }
      if (decoded.requiresRead) setUnresolved({ operationID });
    } finally {
      lifecycle.current.locked = false;
      if (lifecycle.current.active && lifecycle.current.epoch === epoch)
        setBusy(false);
    }
  };

  const reconcile = async () => {
    if (!unresolved || lifecycle.current.locked) return;
    lifecycle.current.locked = true;
    const epoch = lifecycle.current.epoch;
    setBusy(true);
    setError(undefined);
    clearSnapshot();
    setLoaded(false);
    onConnectionPolicy?.("unavailable");
    onLegacySIWCCapability?.(false);
    try {
      let recovered: ModelAcceptanceOperation | undefined;
      if (unresolved.operationID) {
        const result = await modelAcceptanceApi.operation(
          account.id,
          unresolved.operationID,
        );
        if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
          return;
        recovered = result.operation;
      }
      const history = await modelAcceptanceApi.history(account.id);
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      if (!validHistory(history, account.id))
        throw new Error("invalid receipt");
      if (
        recovered &&
        (!validOperation(recovered, account.id) ||
          (history.simulation_enabled &&
            !validOperationForSnapshot(recovered, account.id, history)))
      )
        throw new Error("invalid receipt");
      const historyReceipt = recovered
        ? history.operations.find((item) => item.id === recovered.id)
        : undefined;
      const recoveredReceipt =
        historyReceipt &&
        recovered &&
        historyReceipt.revision >= recovered.revision
          ? historyReceipt
          : recovered;
      acceptHistory(
        recoveredReceipt
          ? {
              ...history,
              operations: [
                recoveredReceipt,
                ...history.operations.filter(
                  (item) => item.id !== recoveredReceipt.id,
                ),
              ],
            }
          : history,
      );
      if (recoveredReceipt)
        setSelected((current) => ({
          ...current,
          [recoveredReceipt.phase]: recoveredReceipt.id,
        }));
      setUnresolved(undefined);
    } catch (cause) {
      if (lifecycle.current.active && lifecycle.current.epoch === epoch) {
        clearSnapshot();
        setLoaded(false);
        onConnectionPolicy?.("unavailable");
        onLegacySIWCCapability?.(false);
        setError(
          cause instanceof Error && cause.message === "invalid receipt"
            ? { ...modelAcceptanceError(cause), code: "invalid_receipt" }
            : modelAcceptanceError(cause),
        );
      }
    } finally {
      lifecycle.current.locked = false;
      if (lifecycle.current.active && lifecycle.current.epoch === epoch)
        setBusy(false);
    }
  };

  const mutationDisabled =
    busy ||
    reading ||
    !!unresolved ||
    !loaded ||
    !snapshot?.simulation_enabled ||
    policy === "unavailable";
  const currentAccountRevision = Math.max(
    account.revision,
    connection?.result_account_revision ?? account.revision,
  );
  const propose = (phase: ModelAcceptancePhase) => {
    const prerequisite =
      phase === "catalog"
        ? connection?.id
        : phase === "inference"
          ? catalog?.id
          : undefined;
    void runMutation(undefined, () =>
      modelAcceptanceApi.propose(account.id, {
        phase,
        route_id: routeID,
        expected_account_revision: currentAccountRevision,
        ...(identityOnly || subscriptionOnly
          ? { connection_purpose: policy }
          : {}),
        ...(prerequisite ? { prerequisite_id: prerequisite } : {}),
        ...(phase === "inference" ? { model } : {}),
      }),
    );
  };
  const chosen = (phase: ModelAcceptancePhase) =>
    operations.find((operation) => operation.id === selected[phase]);

  return (
    <section className="acceptance-operations" aria-label="单次本地模拟验收">
      <div className="section-heading">
        <div>
          <h4>单次本地模拟验收</h4>
          <p className="muted">
            {subscriptionOnly
              ? "订阅连接材料单独审阅、批准和执行。"
              : "连接、目录与推理分别审阅、批准和执行。"}
          </p>
        </div>
        <span className="route-lock">
          {snapshot?.simulation_enabled ? "仅本地模拟" : "生产真实访问保持关闭"}
        </span>
      </div>
      <p className="field-help">
        只使用本进程拥有的合成
        fixture。不会登录真实账户、读取真实凭据、自动重试、轮询、刷新、付费降级、运行脚本或写入
        Git。
      </p>
      {identityOnly && (
        <p className="note">
          本地身份专用模拟；请求身份范围：openid、profile、email。真实登录仍待确认；账户与工作区由用户稍后选择；额度和计费未知。身份验证成功不代表模型可用。
        </p>
      )}
      {subscriptionOnly && (
        <p className="note">
          本地订阅连接模拟；额度：未知，真实访问：关闭。成功只记录合成连接证据，不代表真实订阅、工作区或额度已经验证。
        </p>
      )}
      {error && (
        <p className="form-error" role="alert">
          {error.message} 错误码：<code>{error.code}</code>
        </p>
      )}
      {unresolved && (
        <div className="note warning-note">
          <p>
            原写操作的结果未确认。不会重新提交；读取成功前所有写操作保持锁定。
          </p>
          <button
            type="button"
            disabled={busy}
            onClick={() => void reconcile()}
          >
            仅读取原验收记录
          </button>
        </div>
      )}
      <div className="acceptance-toolbar">
        <label htmlFor={`acceptance-route-${account.id}`}>本地模拟路由</label>
        <select
          id={`acceptance-route-${account.id}`}
          value={routeID}
          disabled={mutationDisabled || !snapshot?.routes.length}
          onChange={(event) => setRouteID(event.target.value)}
        >
          {(snapshot?.routes ?? []).map((route) => (
            <option key={route.id} value={route.id}>
              {route.id}
            </option>
          ))}
        </select>
        {snapshot?.routes.find((route) => route.id === routeID) && (
          <p className="field-help acceptance-route-summary">
            <code>{routeID}</code> ·{" "}
            {snapshot.routes.find((route) => route.id === routeID)!.adapter} ·{" "}
            {snapshot.routes.find((route) => route.id === routeID)!.spend_class}
            {identityOnly && " · 仅身份验证，不授予模型权限"}
            {subscriptionOnly && " · 仅订阅连接，不授予目录或推理权限"}
          </p>
        )}
        <button
          type="button"
          disabled={reading || busy || snapshot?.simulation_enabled === false}
          onClick={() => void readHistory()}
        >
          重新读取验收记录
        </button>
      </div>
      {reading && <p role="status">正在读取验收记录…</p>}
      {!reading && loaded && operations.length === 0 && (
        <p>尚无已保存的验收材料</p>
      )}
      <ol className="acceptance-steps">
        {visiblePhases.map((phase) => {
          const phaseOperations = operations.filter(
            (operation) => operation.phase === phase,
          );
          const operation = chosen(phase);
          const prerequisiteMissing =
            (phase === "catalog" && !connection) ||
            (phase === "inference" && (!catalog || !model));
          const identityClosedPhase = identityOnly && phase !== "connection";
          return (
            <li key={phase}>
              <div className="acceptance-step-heading">
                <strong>{phaseLabels[phase]}</strong>
                <button
                  type="button"
                  disabled={
                    mutationDisabled ||
                    !routeID ||
                    prerequisiteMissing ||
                    identityClosedPhase ||
                    !account.enabled
                  }
                  onClick={() => propose(phase)}
                >
                  创建{phaseLabels[phase]}材料
                </button>
              </div>
              {phase === "catalog" && latestConnection && !connection && (
                <p className="warning-copy">
                  最新连接回执为{stateLabels[latestConnection.state]}
                  ；较早成功不能授权目录请求。
                </p>
              )}
              {identityClosedPhase && (
                <p className="field-help">
                  身份专用连接不授权目录、推理、刷新或任何模型使用。
                </p>
              )}
              {phase === "inference" && (
                <label>
                  推理模型
                  <select
                    aria-label="推理模型"
                    value={model}
                    disabled={mutationDisabled || !catalog}
                    onChange={(event) => setModel(event.target.value)}
                  >
                    {(catalog?.catalog ?? []).map((entry) => (
                      <option key={entry.id} value={entry.id}>
                        {entry.display_name} · {entry.id}
                      </option>
                    ))}
                  </select>
                </label>
              )}
              <label>
                {phaseLabels[phase]}已保存材料
                <select
                  aria-label={`${phaseLabels[phase]}已保存材料`}
                  value={selected[phase] ?? ""}
                  onChange={(event) =>
                    setSelected((current) => ({
                      ...current,
                      [phase]: event.target.value,
                    }))
                  }
                >
                  <option value="">选择材料查看与操作</option>
                  {phaseOperations.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.id} · {stateLabels[item.state]}
                    </option>
                  ))}
                </select>
              </label>
              {operation && (
                <OperationReview
                  operation={operation}
                  disabled={mutationDisabled}
                  decide={(decision) =>
                    void runMutation(operation.id, () =>
                      modelAcceptanceApi.decide(
                        account.id,
                        operation,
                        decision,
                      ),
                    )
                  }
                  execute={() =>
                    void runMutation(
                      operation.id,
                      () => modelAcceptanceApi.execute(account.id, operation),
                      async (result) => {
                        if (
                          !identityOnly &&
                          !subscriptionOnly &&
                          result.phase === "connection" &&
                          result.state === "succeeded"
                        )
                          await refreshAccount();
                      },
                    )
                  }
                />
              )}
            </li>
          );
        })}
      </ol>
      {operations.length > 0 && (
        <div className="acceptance-receipts" aria-label="本地模拟回执">
          <h5>已保存回执</h5>
          <ul>
            {operations.map((operation) => (
              <li key={operation.id}>
                <button
                  type="button"
                  className="acceptance-receipt"
                  onClick={() =>
                    setSelected((current) => ({
                      ...current,
                      [operation.phase]: operation.id,
                    }))
                  }
                >
                  <span>
                    {phaseLabels[operation.phase]} ·{" "}
                    {stateLabels[operation.state]} · 本地模拟
                  </span>
                  <code>{operation.id}</code>
                  {operation.code && <code>{operation.code}</code>}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

function OperationReview({
  operation,
  disabled,
  decide,
  execute,
}: {
  operation: ModelAcceptanceOperation;
  disabled: boolean;
  decide: (decision: "approve" | "reject") => void;
  execute: () => void;
}) {
  const material = operation.material;
  return (
    <div
      className="acceptance-review"
      aria-label={`${phaseLabels[operation.phase]}材料审阅`}
    >
      <p>
        <strong>{operation.id}</strong> · r{operation.revision} ·{" "}
        {stateLabels[operation.state]}
      </p>
      <dl>
        <dt>本地模拟端点</dt>
        <dd>{material.route.base_url}</dd>
        <dt>账户快照</dt>
        <dd>
          {material.account.id} · r{material.account.revision}
        </dd>
        <dt>路由快照</dt>
        <dd>{material.route.id}</dd>
        <dt>凭据快照</dt>
        <dd>
          版本 {material.credential_version}
          {material.authorization && (
            <>
              {" "}
              · 授权 {material.authorization.binding.id} · generation{" "}
              {material.authorization.generation}
            </>
          )}
        </dd>
        <dt>计费边界</dt>
        <dd>
          {material.account.billing_mode} · {material.route.spend_class}
        </dd>
        {material.connection_purpose === "identity_only" && (
          <>
            <dt>连接目的</dt>
            <dd>仅身份验证；不授予模型权限</dd>
            <dt>请求身份范围</dt>
            <dd>{material.requested_scopes?.join(" · ")}</dd>
          </>
        )}
        {material.connection_purpose === "subscription_only" && (
          <>
            <dt>连接目的</dt>
            <dd>本地订阅连接模拟；不授予后续模型操作</dd>
            <dt>请求协议权限</dt>
            <dd>
              <ul>
                {subscriptionScopes.map((scope) => (
                  <li key={scope}>
                    <code>{scope}</code> — {scopeMeanings[scope]}
                  </li>
                ))}
              </ul>
            </dd>
            <dt>请求资源 / audience</dt>
            <dd>
              <code>{material.requested_resource}</code>
              （协议 audience，不是本地传输地址）
            </dd>
            <dt>应用能力</dt>
            <dd>
              目录查询：禁用 · 推理：禁用 · 应用刷新：禁用 · 付费回退：禁用
            </dd>
          </>
        )}
        <dt>输入摘要</dt>
        <dd>{material.data_summary}</dd>
        {material.model && (
          <>
            <dt>精确模型</dt>
            <dd>{material.model}</dd>
          </>
        )}
        {material.prompt_digest && (
          <>
            <dt>输入摘要哈希</dt>
            <dd>
              <code>{material.prompt_digest}</code>
            </dd>
          </>
        )}
        <dt>期限</dt>
        <dd>
          {material.connection_purpose === "subscription_only"
            ? `批准有效期 10 分钟 · 执行期限 ${material.deadline_seconds} 秒 · 到期 ${material.expires_at}`
            : `${material.deadline_seconds} 秒 · 到期 ${material.expires_at}`}
        </dd>
        <dt>尝试预算</dt>
        <dd>
          最多 {material.max_attempts} 次 · 已记录 {operation.attempts} 次
        </dd>
        <dt>材料摘要</dt>
        <dd>
          <code>{operation.material_digest}</code>
        </dd>
      </dl>
      {operation.state === "pending" && (
        <div className="actions acceptance-actions">
          <button
            type="button"
            disabled={disabled}
            onClick={() => decide("approve")}
          >
            批准{phaseLabels[operation.phase]}材料
          </button>
          <button
            type="button"
            disabled={disabled}
            onClick={() => decide("reject")}
          >
            拒绝{phaseLabels[operation.phase]}材料
          </button>
        </div>
      )}
      {operation.state === "approved" && (
        <button type="button" disabled={disabled} onClick={execute}>
          执行{phaseLabels[operation.phase]}
        </button>
      )}
      {(operation.state === "running" || operation.attempts > 0) && (
        <p className="warning-copy">
          单次尝试已消耗；没有重试操作。结果未知也不会重放请求。
        </p>
      )}
      <p className="field-help">
        真实访问：关闭 · 远端计费：未验证 · 额度：未知
      </p>
    </div>
  );
}
