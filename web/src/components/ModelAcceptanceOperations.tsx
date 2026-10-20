import { useEffect, useMemo, useRef, useState } from "react";
import {
  modelAcceptanceApi,
  modelAcceptanceError,
  type ModelAcceptanceError,
} from "../modelAcceptanceApi";
import type {
  ConnectionSession,
  ModelAccount,
  ModelAcceptanceHistory,
  ModelAcceptanceOperation,
  ModelAcceptanceOperationResponse,
  ModelAcceptancePhase,
} from "../types";
import {
  ModelConnectionSession,
  validConnectionSession,
} from "./ModelConnectionSession";
import { ConnectionDiagnostic } from "./ConnectionDiagnostic";

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
  | "identity_only"
  | "subscription_only"
  | "production_connection"
  | "legacy"
  | "unavailable";

function validAdmission(value: unknown): boolean {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const record = value as Record<string, unknown>;
  return (
    Object.keys(record).length === 2 &&
    typeof record.generation === "string" &&
    /^[a-f0-9]{32}$/.test(record.generation) &&
    record.profile === "openai_siwc_subscription_v1"
  );
}

function isTrustedProductionRoute(
  route: ModelAcceptanceHistory["routes"][number],
): boolean {
  return (
    route.environment === "live" &&
    route.adapter === identityAdapter &&
    route.spend_class === identitySpendClass &&
    route.base_url === subscriptionResource &&
    route.connection_purpose === "subscription_only" &&
    route.connection_mode === "browser_handoff"
  );
}

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

function canonicalLoopbackV1(value: string): boolean {
  try {
    const url = new URL(value);
    const port = Number(url.port);
    return (
      url.protocol === "http:" &&
      url.hostname === "127.0.0.1" &&
      Number.isInteger(port) &&
      port > 0 &&
      port <= 65535 &&
      url.username === "" &&
      url.password === "" &&
      url.pathname === "/v1" &&
      url.search === "" &&
      url.hash === "" &&
      url.href === value
    );
  } catch {
    return false;
  }
}

function invalidReceipt(): Error {
  return new Error("invalid receipt");
}

function requireValidReceipt(validate: () => boolean): void {
  try {
    if (validate()) return;
  } catch {
    // Runtime response data is untrusted; validator exceptions are invalid DTOs.
  }
  throw invalidReceipt();
}

function requireOperationResponse(
  value: unknown,
): ModelAcceptanceOperationResponse {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw invalidReceipt();
  const operation = (value as Record<string, unknown>).operation;
  if (!operation || typeof operation !== "object" || Array.isArray(operation))
    throw invalidReceipt();
  return value as ModelAcceptanceOperationResponse;
}

function validAttemptCount(value: ModelAcceptanceOperation): boolean {
  switch (value.state) {
    case "pending":
    case "approved":
    case "rejected":
    case "expired":
    case "invalidated":
      return value.attempts === 0;
    default:
      return value.attempts === 1;
  }
}

function validConnectionMaterial(
  value: ModelAcceptanceOperation,
  accountID: string,
  trustedRoute?: ModelAcceptanceHistory["routes"][number],
): boolean {
  const material = value.material as unknown;
  if (!material || typeof material !== "object") return false;
  const record = material as Record<string, unknown>;
  const production = value.source === "production_connection";
  if (
    production
      ? !validAdmission(record.production_admission) ||
        record.connection_purpose !== "subscription_only" ||
        record.connection_mode !== "browser_handoff"
      : record.production_admission !== undefined
  )
    return false;
  if (!validConnectionPurpose(record.connection_purpose)) return false;
  if (record.connection_mode === undefined) {
    if (
      record.login_timeout_seconds !== undefined &&
      record.login_timeout_seconds !== 0
    )
      return false;
  } else if (
    record.connection_mode !== "browser_handoff" ||
    record.connection_purpose !== "subscription_only" ||
    record.login_timeout_seconds !== 600
  )
    return false;
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
  const downstreamFieldsEmpty = [
    record.model,
    record.prerequisite_id,
    record.connection_id,
    record.prompt_digest,
  ].every((field) => field === undefined || field === "");
  const account = record.account;
  const materialRoute = record.route;
  if (
    value.phase !== "connection" ||
    !permissionsValid ||
    !downstreamFieldsEmpty ||
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
    accountRecord.base_url === route.base_url &&
    accountRecord.auth_method === "openai_siwc" &&
    accountRecord.billing_mode === "subscription" &&
    route.account_id === accountID &&
    route.adapter === identityAdapter &&
    route.environment === (production ? "live" : "simulation") &&
    (!production ||
      (route.base_url === subscriptionResource &&
        route.protocol === "responses" &&
        accountRecord.enabled === true)) &&
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
    route.base_url === trustedRoute.base_url &&
    record.connection_mode === trustedRoute.connection_mode
  );
}

function validRoute(
  route: unknown,
): route is ModelAcceptanceHistory["routes"][number] {
  if (!route || typeof route !== "object") return false;
  const record = route as Record<string, unknown>;
  return (
    (record.environment === "simulation" || record.environment === "live") &&
    typeof record.id === "string" &&
    identifier.test(record.id) &&
    typeof record.adapter === "string" &&
    typeof record.spend_class === "string" &&
    typeof record.base_url === "string" &&
    validConnectionPurpose(record.connection_purpose) &&
    (record.connection_mode === undefined ||
      (record.connection_mode === "browser_handoff" &&
        record.connection_purpose === "subscription_only"))
  );
}

function isTrustedIdentityRoute(
  route: ModelAcceptanceHistory["routes"][number],
): boolean {
  return (
    route.connection_purpose === "identity_only" &&
    route.adapter === identityAdapter &&
    route.environment === "simulation" &&
    route.spend_class === identitySpendClass &&
    canonicalLoopbackV1(route.base_url)
  );
}

function isTrustedSubscriptionRoute(
  route: ModelAcceptanceHistory["routes"][number],
): boolean {
  return (
    route.connection_purpose === "subscription_only" &&
    route.adapter === identityAdapter &&
    route.environment === "simulation" &&
    route.spend_class === identitySpendClass &&
    canonicalLoopbackV1(route.base_url)
  );
}

export function connectionPolicy(
  history: ModelAcceptanceHistory,
): ModelAcceptanceConnectionPolicy {
  if (
    validAdmission(history.production_admission) &&
    history.routes.length === 1 &&
    history.routes.every(isTrustedProductionRoute)
  )
    return "production_connection";
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
    validAttemptCount(value) &&
    ((value.source === "local_simulation" && value.live_access === "blocked") ||
      (value.source === "production_connection" &&
        value.live_access === "connection_only")) &&
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
  if (policy === "production_connection") {
    const trustedRoute = operationRoute(operation, history.routes);
    return (
      !!trustedRoute &&
      operation.source === "production_connection" &&
      validOperation(operation, accountID, trustedRoute) &&
      operation.material.production_admission?.generation ===
        history.production_admission?.generation &&
      operation.material.production_admission?.profile ===
        history.production_admission?.profile
    );
  }
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
    (value.production_connection_enabled === undefined ||
      typeof value.production_connection_enabled === "boolean") &&
    (value.production_admission === undefined ||
      validAdmission(value.production_admission)) &&
    Array.isArray(value.operations) &&
    Array.isArray(value.routes) &&
    value.routes.every(validRoute) &&
    (() => {
      if (
        value.production_admission !== undefined ||
        value.production_connection_enabled === true ||
        value.routes.some((route) => route.environment === "live")
      ) {
        return (
          value.simulation_enabled === false &&
          value.legacy_siwc_simulation_enabled !== true &&
          validAdmission(value.production_admission) &&
          value.routes.length === 1 &&
          value.routes.every(isTrustedProductionRoute) &&
          value.operations.every(
            (operation) =>
              operation.source === "production_connection" &&
              validOperation(operation, accountID),
          )
        );
      }
      if (
        value.simulation_enabled &&
        value.operations.some(
          (operation) => operation.source === "production_connection",
        )
      )
        return false;
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
  const [connectionSession, setConnectionSession] =
    useState<ConnectionSession>();
  const [routeID, setRouteID] = useState("");
  const [model, setModel] = useState("");
  const [selected, setSelected] = useState<
    Partial<Record<ModelAcceptancePhase, string>>
  >({});
  const [unresolved, setUnresolved] = useState<{
    operationID?: string;
    materialDigest?: string;
  }>();
  const lifecycle = useRef({ active: true, epoch: 0, locked: false });

  const clearSnapshot = () => {
    setSnapshot(undefined);
    setSelected({});
    setRouteID("");
    setModel("");
    setConnectionSession(undefined);
  };
  const acceptHistory = (value: unknown) => {
    const history = value as ModelAcceptanceHistory;
    requireValidReceipt(() => validHistory(history, account.id));
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
  const production =
    policy === "production_connection" ||
    operations.some(
      (operation) => operation.source === "production_connection",
    );
  const identityOnly = policy === "identity_only";
  const subscriptionOnly = policy === "subscription_only" || production;
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

  const replaceOperation = (value: unknown, expectedOperationID?: string) => {
    const result = requireOperationResponse(value);
    const { operation, connection_session: session } = result;
    const previous = snapshot?.operations.find(
      (item) => item.id === (expectedOperationID ?? operation.id),
    );
    requireValidReceipt(
      () =>
        !!snapshot &&
        validOperationForSnapshot(operation, account.id, snapshot) &&
        (operation.source !== "production_connection" ||
          ((expectedOperationID === undefined ||
            operation.id === expectedOperationID) &&
            (!previous ||
              (operation.revision >= previous.revision &&
                operation.material_digest === previous.material_digest &&
                operation.attempts >= previous.attempts &&
                operation.claim_sequence >= previous.claim_sequence &&
                (operation.revision !== previous.revision ||
                  (operation.state === previous.state &&
                    operation.attempts === previous.attempts &&
                    operation.claim_sequence ===
                      previous.claim_sequence)))))) &&
        (session === undefined ||
          validConnectionSession(operation, session, Date.now())),
    );
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
    setConnectionSession(session);
    setSelected((current) => ({ ...current, [operation.phase]: operation.id }));
    return operation;
  };
  const runMutation = async (
    operationID: string | undefined,
    work: () => Promise<ModelAcceptanceOperationResponse>,
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
      const operation = replaceOperation(result, operationID);
      if (after) {
        try {
          await after(operation);
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
      if (decoded.requiresRead)
        setUnresolved({
          operationID,
          materialDigest: operations.find((item) => item.id === operationID)
            ?.material_digest,
        });
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
      let recoveredSession: ConnectionSession | undefined;
      if (unresolved.operationID) {
        const result = requireOperationResponse(
          await modelAcceptanceApi.operation(
            account.id,
            unresolved.operationID,
          ),
        );
        if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
          return;
        requireValidReceipt(
          () =>
            result.operation.id === unresolved.operationID &&
            result.operation.material_digest === unresolved.materialDigest,
        );
        recovered = result.operation;
        recoveredSession = result.connection_session;
      }
      const history = await modelAcceptanceApi.history(account.id);
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      requireValidReceipt(() => validHistory(history, account.id));
      requireValidReceipt(
        () =>
          !recovered ||
          (validOperation(recovered, account.id) &&
            ((!history.simulation_enabled && !recoveredSession) ||
              validOperationForSnapshot(recovered, account.id, history)) &&
            (recoveredSession === undefined ||
              validConnectionSession(recovered, recoveredSession, Date.now()))),
      );
      const historyReceipt = recovered
        ? history.operations.find((item) => item.id === recovered.id)
        : undefined;
      if (
        historyReceipt &&
        recovered &&
        (historyReceipt.material_digest !== recovered.material_digest ||
          (historyReceipt.revision === recovered.revision &&
            (historyReceipt.state !== recovered.state ||
              historyReceipt.claim_sequence !== recovered.claim_sequence ||
              historyReceipt.attempts !== recovered.attempts)))
      )
        throw invalidReceipt();
      const historyIsNewer =
        !!historyReceipt &&
        !!recovered &&
        historyReceipt.revision > recovered.revision;
      const recoveredReceipt = historyIsNewer ? historyReceipt : recovered;
      if (historyIsNewer) recoveredSession = undefined;
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
      setConnectionSession(recoveredSession);
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

  const readConnectionStatus = async (operation: ModelAcceptanceOperation) => {
    if (lifecycle.current.locked) return;
    lifecycle.current.locked = true;
    const epoch = lifecycle.current.epoch;
    setBusy(true);
    setError(undefined);
    setConnectionSession(undefined);
    try {
      const result = await modelAcceptanceApi.operation(
        account.id,
        operation.id,
      );
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      replaceOperation(result, operation.id);
    } catch (cause) {
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      const invalid =
        cause instanceof Error && cause.message === "invalid receipt";
      setError(
        invalid
          ? { ...modelAcceptanceError(cause), code: "invalid_receipt" }
          : modelAcceptanceError(cause),
      );
      if (invalid) {
        clearSnapshot();
        setLoaded(false);
        onConnectionPolicy?.("unavailable");
        onLegacySIWCCapability?.(false);
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
    !(production
      ? snapshot?.production_connection_enabled === true
      : snapshot?.simulation_enabled) ||
    (production &&
      (account.provider !== "openai" ||
        account.auth_method !== "openai_siwc" ||
        account.billing_mode !== "subscription" ||
        account.base_url !== subscriptionResource)) ||
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
          ? {
              connection_purpose: identityOnly
                ? ("identity_only" as const)
                : ("subscription_only" as const),
            }
          : {}),
        ...(prerequisite ? { prerequisite_id: prerequisite } : {}),
        ...(phase === "inference" ? { model } : {}),
      }),
    );
  };
  const chosen = (phase: ModelAcceptancePhase) =>
    operations.find((operation) => operation.id === selected[phase]);

  return (
    <section
      className="acceptance-operations"
      aria-label={production ? "单次真实订阅连接" : "单次本地模拟验收"}
    >
      <div className="section-heading">
        <div>
          <h4>{production ? "单次真实订阅连接" : "单次本地模拟验收"}</h4>
          <p className="muted">
            {subscriptionOnly
              ? "订阅连接材料单独审阅、批准和执行。"
              : "连接、目录与推理分别审阅、批准和执行。"}
          </p>
        </div>
        <span className="route-lock">
          {production
            ? snapshot?.production_connection_enabled
              ? "仅连接可单独审批"
              : "连接审批不可用 · 历史只读"
            : snapshot?.simulation_enabled
              ? "仅本地模拟"
              : "生产真实访问保持关闭"}
        </span>
      </div>
      <p className="field-help">
        {production
          ? "此入口仅允许独立审批的真实订阅连接，不开放目录、推理或任务调用。不会自动打开浏览器、重试、轮询、刷新、付费降级、运行脚本或写入 Git。"
          : "只使用本进程拥有的合成 fixture。不会登录真实账户、读取真实凭据、自动重试、轮询、刷新、付费降级、运行脚本或写入 Git。"}
      </p>
      {identityOnly && (
        <p className="note">
          本地身份专用模拟；请求身份范围：openid、profile、email。真实登录仍待确认；账户与工作区由用户稍后选择；额度和计费未知。身份验证成功不代表模型可用。
        </p>
      )}
      {subscriptionOnly && !production && (
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
        <label htmlFor={`acceptance-route-${account.id}`}>
          {production ? "固定真实连接路由" : "本地模拟路由"}
        </label>
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
            {snapshot.routes.find((route) => route.id === routeID)!
              .connection_mode === "browser_handoff" &&
              " · 浏览器交接需手动打开"}
          </p>
        )}
        <button
          type="button"
          disabled={
            reading ||
            busy ||
            (snapshot?.simulation_enabled === false && !production)
          }
          onClick={() => void readHistory()}
        >
          重新读取验收记录
        </button>
      </div>
      {reading && <p role="status">正在读取验收记录…</p>}
      {!reading && loaded && operations.length === 0 && (
        <p>尚无已保存的验收材料</p>
      )}
      <ol
        className={
          subscriptionOnly
            ? "acceptance-steps acceptance-steps--subscription-only"
            : "acceptance-steps"
        }
      >
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
                  创建{production ? "真实" : ""}
                  {phaseLabels[phase]}材料
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
                  disabled={
                    mutationDisabled ||
                    (operation.source === "production_connection" &&
                      (!snapshot ||
                        !validOperationForSnapshot(
                          operation,
                          account.id,
                          snapshot,
                        ) ||
                        operation.material.account.revision !==
                          account.revision ||
                        Date.parse(operation.material.expires_at) <=
                          Date.now()))
                  }
                  sessionDisabled={
                    busy ||
                    reading ||
                    !!unresolved ||
                    !loaded ||
                    (operation.source === "production_connection" &&
                      (!snapshot ||
                        !validOperationForSnapshot(
                          operation,
                          account.id,
                          snapshot,
                        )))
                  }
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
                  connectionSession={
                    connectionSession?.operation_id === operation.id
                      ? connectionSession
                      : undefined
                  }
                  readConnectionStatus={() =>
                    void readConnectionStatus(operation)
                  }
                  cancel={() => {
                    setConnectionSession(undefined);
                    void runMutation(operation.id, () =>
                      modelAcceptanceApi.cancel(account.id, operation),
                    );
                  }}
                />
              )}
            </li>
          );
        })}
      </ol>
      {operations.length > 0 && (
        <div
          className="acceptance-receipts"
          aria-label={production ? "真实订阅连接回执" : "本地模拟回执"}
        >
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
                    {stateLabels[operation.state]} ·{" "}
                    {operation.source === "production_connection"
                      ? "真实订阅连接"
                      : "本地模拟"}
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
  sessionDisabled,
  decide,
  execute,
  connectionSession,
  readConnectionStatus,
  cancel,
}: {
  operation: ModelAcceptanceOperation;
  disabled: boolean;
  sessionDisabled: boolean;
  decide: (decision: "approve" | "reject") => void;
  execute: () => void;
  connectionSession?: ConnectionSession;
  readConnectionStatus: () => void;
  cancel: () => void;
}) {
  const material = operation.material;
  const production = operation.source === "production_connection";
  const [deadlineExpired, setDeadlineExpired] = useState(false);
  useEffect(() => {
    setDeadlineExpired(false);
    if (!production || !["pending", "approved"].includes(operation.state))
      return;
    const remaining = Date.parse(material.expires_at) - Date.now();
    if (remaining <= 0) {
      setDeadlineExpired(true);
      return;
    }
    const timeout = window.setTimeout(
      () => setDeadlineExpired(true),
      Math.min(remaining, 2_147_483_647),
    );
    return () => window.clearTimeout(timeout);
  }, [operation.id, operation.state, material.expires_at, production]);
  const reviewDisabled =
    disabled ||
    (production &&
      (deadlineExpired || Date.parse(material.expires_at) <= Date.now()));
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
        <dt>{production ? "官方连接资源" : "本地模拟端点"}</dt>
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
            <dd>
              {production ? "真实订阅连接" : "本地订阅连接模拟"}
              ；不授予后续模型操作
            </dd>
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
        <dd>
          {production
            ? "仅订阅连接：浏览器账户/工作区选择及凭据存储；无目录、推理、刷新、重试或付费降级。"
            : material.data_summary}
        </dd>
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
          {material.connection_mode === "browser_handoff"
            ? `批准有效期 10 分钟 · 人工登录窗口 ${material.login_timeout_seconds} 秒（执行后开始） · 网络操作期限 ${material.deadline_seconds} 秒 · 批准到期 ${material.expires_at}`
            : material.connection_purpose === "subscription_only"
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
      {production && (
        <div className="note">
          <p>
            账户与工作区由用户在官方浏览器流程中选择；不要在本界面输入密码或验证码。
          </p>
          <p>
            令牌保存在 Foundry 原生
            Keychain；本地数据库仅保留标识符、授权范围、版本/到期、密钥引用与脱敏审计证据，不保存令牌。
          </p>
          <p>
            远端授权不是单次；一次批准只限制本地一次连接尝试。请求的订阅使用与离线权限不代表本阶段会使用模型或刷新令牌。
          </p>
          <p>
            本地清理不等于远端撤销；本功能不执行远端撤销，可在 ChatGPT
            设置中断开应用。
          </p>
        </div>
      )}
      {production &&
        (operation.state === "failed" || operation.state === "unknown") && (
          <p className="warning-copy">
            连接失败或结果未知；清理与凭据提交结果不能确认，不能据此声称已完全回滚。请显式读取并核对原记录，不会重放请求。
          </p>
        )}
      {operation.phase === "connection" &&
        (operation.state === "failed" || operation.state === "unknown") && (
          <ConnectionDiagnostic
            diagnostic={operation.connection_diagnostic as unknown}
          />
        )}
      {operation.state === "pending" && (
        <div className="actions acceptance-actions">
          <button
            type="button"
            disabled={reviewDisabled}
            onClick={() => {
              if (!production || Date.parse(material.expires_at) > Date.now())
                decide("approve");
            }}
          >
            批准{phaseLabels[operation.phase]}材料
          </button>
          <button
            type="button"
            disabled={reviewDisabled}
            onClick={() => decide("reject")}
          >
            拒绝{phaseLabels[operation.phase]}材料
          </button>
        </div>
      )}
      {operation.state === "approved" && operation.attempts === 0 && (
        <button
          type="button"
          disabled={reviewDisabled}
          onClick={() => {
            if (!production || Date.parse(material.expires_at) > Date.now())
              execute();
          }}
        >
          执行{phaseLabels[operation.phase]}
        </button>
      )}
      {(operation.state === "running" || operation.attempts > 0) && (
        <p className="warning-copy">
          单次尝试已消耗；没有重试操作。结果未知也不会重放请求。
        </p>
      )}
      {material.connection_mode === "browser_handoff" &&
        operation.attempts > 0 && (
          <ModelConnectionSession
            operation={operation}
            session={connectionSession}
            disabled={sessionDisabled}
            onRead={readConnectionStatus}
            onCancel={cancel}
          />
        )}
      <p className="field-help">
        {production
          ? "真实连接记录不代表当前执行许可；模型访问：关闭"
          : "真实访问：关闭"}{" "}
        · 远端计费：未验证 · 额度：未知
      </p>
    </div>
  );
}
