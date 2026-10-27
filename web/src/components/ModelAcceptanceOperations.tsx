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
import { InferenceDiagnostic } from "./InferenceDiagnostic";
import {
  inferenceModel,
  inferencePrompt,
  inferenceProfile,
  validInferenceMaterial,
  validInferenceReceipt,
  validInferenceBudget,
} from "./inferenceReview";

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
  | "production_catalog"
  | "production_inference"
  | "legacy"
  | "unavailable";

function validAdmission(
  value: unknown,
  profile = "openai_siwc_subscription_v1",
): boolean {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const record = value as Record<string, unknown>;
  return (
    Object.keys(record).length === 2 &&
    typeof record.generation === "string" &&
    /^[a-f0-9]{32}$/.test(record.generation) &&
    record.profile === profile
  );
}

const catalogBlockers = {
  authorization_expired: "授权已过期；目录操作保持关闭，不会刷新或重新登录。",
  authorization_unavailable:
    "缺少可用授权；目录操作保持关闭，不会刷新或重新登录。",
  prerequisite_unavailable: "缺少当前成功的真实连接证据；目录操作保持关闭。",
  admission_unavailable: "本进程目录准入不可用；目录操作保持关闭。",
};

function isTrustedCatalogRoute(
  route: ModelAcceptanceHistory["routes"][number],
): boolean {
  return (
    route.environment === "live" &&
    route.adapter === identityAdapter &&
    route.spend_class === identitySpendClass &&
    route.base_url === subscriptionResource &&
    route.connection_purpose === undefined &&
    route.connection_mode === undefined
  );
}

function onlyKeys(value: object, keys: readonly string[]): boolean {
  return Object.keys(value).every((key) => keys.includes(key));
}

// Catalog data is display evidence, never a route, model selection or inference permit.
function validCatalogMaterial(
  value: ModelAcceptanceOperation,
  accountID: string,
  trustedRoute?: ModelAcceptanceHistory["routes"][number],
): boolean {
  const m = value.material;
  if (
    !m ||
    !onlyKeys(m, [
      "production_admission",
      "phase",
      "request_method",
      "request_url",
      "account",
      "route",
      "credential_version",
      "authorization",
      "prerequisite_id",
      "connection_id",
      "data_summary",
      "created_at",
      "expires_at",
      "deadline_seconds",
      "max_attempts",
    ])
  )
    return false;
  const a = m.account,
    r = m.route,
    auth = m.authorization,
    binding = auth?.binding;
  if (
    !a ||
    !r ||
    !auth ||
    !binding ||
    !onlyKeys(auth, ["binding", "state", "generation", "expires_at"]) ||
    !onlyKeys(binding, [
      "id",
      "account_id",
      "issuer",
      "subject",
      "client_id",
      "spend_class",
      "scopes",
    ])
  )
    return false;
  const created = Date.parse(m.created_at),
    expires = Date.parse(m.expires_at),
    authExpiry = Date.parse(auth.expires_at);
  return (
    value.phase === "catalog" &&
    validAdmission(m.production_admission, "openai_siwc_catalog_v1") &&
    m.request_method === "GET" &&
    m.request_url === "https://api.openai.com/v1/models" &&
    a.id === accountID &&
    a.provider === "openai" &&
    a.auth_method === "openai_siwc" &&
    a.billing_mode === "subscription" &&
    a.base_url === subscriptionResource &&
    a.enabled === true &&
    Number.isSafeInteger(a.revision) &&
    a.revision > 0 &&
    a.current_credential_version === 0 &&
    identifier.test(r.id) &&
    r.account_id === accountID &&
    r.environment === "live" &&
    r.adapter === identityAdapter &&
    r.protocol === "responses" &&
    r.base_url === subscriptionResource &&
    r.spend_class === identitySpendClass &&
    r.extra_usage === "off" &&
    (!trustedRoute ||
      (r.id === trustedRoute.id && isTrustedCatalogRoute(trustedRoute))) &&
    m.credential_version === 0 &&
    auth.state === "available" &&
    Number.isSafeInteger(auth.generation) &&
    auth.generation > 0 &&
    identifier.test(binding.id) &&
    binding.account_id === accountID &&
    binding.issuer === "https://auth.openai.com" &&
    [binding.subject, binding.client_id].every(
      (text) =>
        typeof text === "string" &&
        text.trim().length > 0 &&
        text.length <= 2048 &&
        !/\p{Cc}/u.test(text),
    ) &&
    binding.spend_class === identitySpendClass &&
    Array.isArray(binding.scopes) &&
    binding.scopes.length <= 64 &&
    new Set(binding.scopes).size === binding.scopes.length &&
    binding.scopes.every(
      (scope) =>
        typeof scope === "string" &&
        scope.length > 0 &&
        scope.length <= 256 &&
        !/[\s\p{Cc}]/u.test(scope),
    ) &&
    binding.scopes.includes("resource.invoke") &&
    binding.scopes.includes("chatgpt.tokens.use.direct") &&
    typeof m.prerequisite_id === "string" &&
    identifier.test(m.prerequisite_id) &&
    m.prerequisite_id === m.connection_id &&
    m.deadline_seconds === 30 &&
    m.max_attempts === 1 &&
    Number.isFinite(created) &&
    Number.isFinite(authExpiry) &&
    expires > created &&
    expires === Math.min(created + 600_000, authExpiry) &&
    typeof m.data_summary === "string" &&
    m.data_summary.length <= 4096
  );
}

function validCatalogEntries(value: ModelAcceptanceOperation): boolean {
  return (
    Array.isArray(value.catalog) &&
    value.catalog.length <= 128 &&
    (value.attempts === 0
      ? value.claim_sequence === 0
      : value.claim_sequence > 0) &&
    new Set(value.catalog.map((entry) => entry.id)).size ===
      value.catalog.length &&
    value.catalog.every(
      (entry) =>
        onlyKeys(entry, ["id", "display_name"]) &&
        typeof entry.id === "string" &&
        /^[A-Za-z0-9_.:/-]{1,128}$/.test(entry.id) &&
        typeof entry.display_name === "string" &&
        entry.display_name.trim().length > 0 &&
        Array.from(entry.display_name).length <= 200 &&
        !/[<>\p{Cc}]/u.test(entry.display_name),
    ) &&
    (value.state === "succeeded" || value.catalog.length === 0) &&
    value.usage_known === false &&
    value.input_tokens === undefined &&
    value.output_tokens === undefined &&
    ["not_sent", "completed", "rejected", "unknown"].includes(
      value.certainty,
    ) &&
    (value.state !== "succeeded" || value.certainty === "completed")
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
    validAdmission(history.production_admission, inferenceProfile) &&
    history.routes.length === 1 &&
    history.routes.every(isTrustedCatalogRoute)
  )
    return "production_inference";
  if (
    validAdmission(history.production_admission, "openai_siwc_catalog_v1") &&
    history.routes.length === 1 &&
    history.routes.every(isTrustedCatalogRoute)
  )
    return "production_catalog";
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
        value.live_access === "connection_only") ||
      (value.source === "production_catalog" &&
        value.live_access === "catalog_only") ||
      (value.source === "production_inference" &&
        value.live_access === "inference_once")) &&
    value.quota === "unknown" &&
    value.remote_billing === "unverified" &&
    !!value.material &&
    (value.source === "production_inference"
      ? validInferenceMaterial(value, (projected) =>
          validCatalogMaterial(projected, accountID, trustedRoute),
        )
      : value.source === "production_catalog"
        ? validCatalogMaterial(value, accountID, trustedRoute)
        : validConnectionMaterial(value, accountID, trustedRoute)) &&
    value.material.phase === value.phase &&
    value.material.account.id === accountID &&
    digest.test(value.material_digest) &&
    (value.code === undefined || receiptCodes.has(value.code)) &&
    Array.isArray(value.catalog) &&
    (value.source === "production_inference"
      ? validInferenceReceipt(value)
      : value.source === "production_catalog"
        ? validCatalogEntries(value)
        : value.catalog.every(
            (entry) =>
              identifier.test(entry.id) &&
              typeof entry.display_name === "string" &&
              entry.display_name.length <= 256,
          )) &&
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
  if (policy === "production_inference") {
    const connection = latestClaimed(history.operations, "connection");
    const catalog = history.operations.find(
      (entry) => entry.id === operation.material.prerequisite_id,
    );
    return (
      operation.source === "production_inference" &&
      !!operationRoute(operation, history.routes) &&
      validOperation(
        operation,
        accountID,
        operationRoute(operation, history.routes),
      ) &&
      operation.material.production_admission?.generation ===
        history.production_admission?.generation &&
      connection?.source === "production_connection" &&
      connection.state === "succeeded" &&
      connection.certainty === "completed" &&
      connection.result_account_revision ===
        operation.material.account.revision &&
      digest.test(connection.result_snapshot_digest ?? "") &&
      catalog?.source === "production_catalog" &&
      catalog.state === "succeeded" &&
      catalog.certainty === "completed" &&
      catalog.result_account_revision === operation.material.account.revision &&
      digest.test(catalog.result_snapshot_digest ?? "") &&
      catalog.material.connection_id === connection.id &&
      operation.material.connection_id === connection.id &&
      catalog.material.account.revision ===
        operation.material.account.revision &&
      catalog.material.route.id === operation.material.route.id &&
      connection.material.route.id === operation.material.route.id &&
      JSON.stringify(catalog.material.authorization) ===
        JSON.stringify(operation.material.authorization) &&
      catalog.catalog.some((entry) => entry.id === inferenceModel)
    );
  }
  if (policy === "production_catalog") {
    const prerequisite = latestClaimed(history.operations, "connection");
    return (
      operation.source === "production_catalog" &&
      !!operationRoute(operation, history.routes) &&
      validOperation(
        operation,
        accountID,
        operationRoute(operation, history.routes),
      ) &&
      operation.material.production_admission?.generation ===
        history.production_admission?.generation &&
      prerequisite?.source === "production_connection" &&
      prerequisite.state === "succeeded" &&
      prerequisite.certainty === "completed" &&
      digest.test(prerequisite.result_snapshot_digest ?? "") &&
      prerequisite.result_account_revision ===
        operation.material.account.revision &&
      prerequisite.material.route.id === operation.material.route.id &&
      operation.material.connection_id === prerequisite.id
    );
  }
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
    (value.production_catalog_enabled === undefined ||
      typeof value.production_catalog_enabled === "boolean") &&
    (value.production_inference_enabled === undefined ||
      typeof value.production_inference_enabled === "boolean") &&
    (value.production_inference_blocker === undefined ||
      Object.hasOwn(catalogBlockers, value.production_inference_blocker)) &&
    (value.production_catalog_blocker === undefined ||
      Object.hasOwn(catalogBlockers, value.production_catalog_blocker)) &&
    (value.production_admission === undefined ||
      validAdmission(value.production_admission) ||
      validAdmission(value.production_admission, "openai_siwc_catalog_v1") ||
      validAdmission(value.production_admission, inferenceProfile)) &&
    Array.isArray(value.operations) &&
    Array.isArray(value.routes) &&
    value.routes.every(validRoute) &&
    new Set(value.operations.map((operation) => operation.id)).size ===
      value.operations.length &&
    (() => {
      if (validAdmission(value.production_admission, inferenceProfile)) {
        return (
          value.simulation_enabled === false &&
          value.legacy_siwc_simulation_enabled !== true &&
          value.production_connection_enabled === false &&
          value.production_catalog_enabled === false &&
          typeof value.production_inference_enabled === "boolean" &&
          !(
            value.production_inference_enabled &&
            value.production_inference_blocker !== undefined
          ) &&
          value.production_catalog_blocker === undefined &&
          value.routes.length === 1 &&
          value.routes.every(isTrustedCatalogRoute) &&
          value.operations.every(
            (operation) =>
              [
                "production_connection",
                "production_catalog",
                "production_inference",
              ].includes(operation.source) &&
              validOperation(operation, accountID),
          )
        );
      }
      if (
        value.production_inference_enabled === true ||
        value.production_inference_blocker !== undefined
      )
        return false;
      if (
        validAdmission(value.production_admission, "openai_siwc_catalog_v1")
      ) {
        return (
          value.simulation_enabled === false &&
          value.legacy_siwc_simulation_enabled !== true &&
          value.production_connection_enabled === false &&
          typeof value.production_catalog_enabled === "boolean" &&
          !(
            value.production_catalog_enabled &&
            value.production_catalog_blocker !== undefined
          ) &&
          value.routes.length === 1 &&
          value.routes.every(isTrustedCatalogRoute) &&
          value.operations.every(
            (operation) =>
              (operation.source === "production_connection" ||
                operation.source === "production_catalog" ||
                operation.source === "production_inference") &&
              validOperation(operation, accountID),
          )
        );
      }
      if (
        value.production_catalog_enabled === true ||
        value.production_catalog_blocker !== undefined
      )
        return false;
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
              (operation.source === "production_connection" ||
                operation.source === "production_catalog" ||
                operation.source === "production_inference") &&
              validOperation(operation, accountID),
          )
        );
      }
      if (
        value.simulation_enabled &&
        value.operations.some(
          (operation) => operation.source !== "local_simulation",
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
  // Retain only the last validated presentation when an uncertain read clears
  // authority. The account/revision key remounts this state for a new account.
  const [productionPresentation, setProductionPresentation] = useState<
    "production_connection" | "production_catalog" | "production_inference"
  >();
  const [loaded, setLoaded] = useState(false);
  const [reading, setReading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ModelAcceptanceError>();
  const [connectionSession, setConnectionSession] =
    useState<ConnectionSession>();
  const [routeID, setRouteID] = useState("");
  const [model, setModel] = useState("");
  const [budgetMissionID, setBudgetMissionID] = useState("");
  const [budgetRevision, setBudgetRevision] = useState("");
  const validBudgetInput =
    identifier.test(budgetMissionID) &&
    /^[1-9][0-9]*$/.test(budgetRevision) &&
    Number.isSafeInteger(Number(budgetRevision)) &&
    Number(budgetRevision) < Number.MAX_SAFE_INTEGER;
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
    setBudgetMissionID("");
    setBudgetRevision("");
    setConnectionSession(undefined);
  };
  const acceptHistory = (value: unknown) => {
    const history = value as ModelAcceptanceHistory;
    requireValidReceipt(() => validHistory(history, account.id));
    setSnapshot(history);
    setLoaded(true);
    const nextPolicy = connectionPolicy(history);
    setProductionPresentation(
      nextPolicy === "production_connection" ||
        nextPolicy === "production_inference" ||
        nextPolicy === "production_catalog"
        ? nextPolicy
        : undefined,
    );
    onConnectionPolicy?.(nextPolicy);
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
  const productionInference =
    policy === "production_inference" ||
    (!snapshot && productionPresentation === "production_inference") ||
    (policy === "unavailable" &&
      operations.some(
        (operation) => operation.source === "production_inference",
      ));
  const productionCatalog =
    policy === "production_catalog" ||
    (!snapshot && productionPresentation === "production_catalog");
  const production =
    productionInference ||
    productionCatalog ||
    (!snapshot && productionPresentation === "production_connection") ||
    policy === "production_connection" ||
    operations.some(
      (operation) => operation.source === "production_connection",
    );
  const identityOnly = policy === "identity_only";
  const subscriptionOnly =
    policy === "subscription_only" ||
    (production && !productionCatalog && !productionInference);
  const historicalCatalog =
    policy === "production_connection" &&
    operations.some((operation) => operation.source === "production_catalog");
  const historicalInference =
    (policy === "production_connection" || policy === "production_catalog") &&
    operations.some((operation) => operation.source === "production_inference");
  const visiblePhases =
    productionInference || historicalInference
      ? phases
      : productionCatalog || historicalCatalog
        ? (["connection", "catalog"] as ModelAcceptancePhase[])
        : subscriptionOnly
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
    (productionCatalog || productionInference
      ? latestResultAccountRevision === account.revision &&
        latestConnection.source === "production_connection" &&
        latestConnection.certainty === "completed" &&
        digest.test(latestConnection.result_snapshot_digest ?? "") &&
        latestConnection.material.route.id === routeID
      : latestResultAccountRevision >= account.revision)
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
              (!productionInference ||
                (operation.source === "production_catalog" &&
                  operation.material.account.revision === account.revision &&
                  operation.result_account_revision === account.revision &&
                  digest.test(operation.result_snapshot_digest ?? "") &&
                  operation.material.route.id === routeID &&
                  Date.parse(
                    operation.material.authorization?.expires_at ?? "",
                  ) > Date.now())) &&
              operation.material.connection_id === connection.id,
          )
        : undefined,
    [
      operations,
      connection?.id,
      productionInference,
      account.revision,
      routeID,
    ],
  );
  useEffect(() => {
    if (productionInference) {
      setModel(
        catalog?.catalog.some((entry) => entry.id === inferenceModel)
          ? inferenceModel
          : "",
      );
      return;
    }
    if (productionCatalog) {
      setModel("");
      return;
    }
    const models = catalog?.catalog ?? [];
    setModel((current) =>
      models.some((entry) => entry.id === current)
        ? current
        : (models[0]?.id ?? ""),
    );
  }, [catalog?.id, catalog?.revision, productionCatalog, productionInference]);

  // No polling: only the current prerequisite controls the inference gate.
  // Expired diagnostic history remains readable, but is not current authority.
  // Each selected operation still checks its own expiry and frozen lineage.
  const catalogAuthorizationExpiry =
    productionCatalog || productionInference
      ? operations
          .filter(
            (operation) =>
              (productionInference
                ? operation.id === catalog?.id
                : operation.source === "production_catalog" &&
                  operation.material.production_admission?.generation ===
                    snapshot?.production_admission?.generation) &&
              operation.material.account.revision === account.revision,
          )
          .reduce(
            (earliest, operation) =>
              Math.min(
                earliest,
                Date.parse(operation.material.authorization!.expires_at),
              ),
            Infinity,
          )
      : Infinity;
  const [authorizationExpired, setAuthorizationExpired] = useState(false);
  useEffect(() => {
    setAuthorizationExpired(catalogAuthorizationExpiry <= Date.now());
    if (
      !Number.isFinite(catalogAuthorizationExpiry) ||
      catalogAuthorizationExpiry <= Date.now()
    )
      return;
    const timer = window.setTimeout(
      () => setAuthorizationExpired(true),
      Math.min(catalogAuthorizationExpiry - Date.now(), 2_147_483_647),
    );
    return () => window.clearTimeout(timer);
  }, [catalogAuthorizationExpiry]);

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
        (operation.source === "local_simulation" ||
          ((expectedOperationID === undefined ||
            operation.id === expectedOperationID) &&
            (!previous ||
              (operation.revision >= previous.revision &&
                operation.material_digest === previous.material_digest &&
                (operation.source !== "production_inference" ||
                  JSON.stringify(operation.material) ===
                    JSON.stringify(previous.material)) &&
                operation.attempts >= previous.attempts &&
                operation.claim_sequence >= previous.claim_sequence &&
                (operation.revision !== previous.revision ||
                  (operation.state === previous.state &&
                    operation.attempts === previous.attempts &&
                    operation.claim_sequence ===
                      previous.claim_sequence)))))) &&
        (session === undefined ||
          (!productionCatalog &&
            !productionInference &&
            validConnectionSession(operation, session, Date.now()))),
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
    authorizationExpired ||
    !(productionInference
      ? snapshot?.production_inference_enabled === true
      : productionCatalog
        ? snapshot?.production_catalog_enabled === true
        : production
          ? snapshot?.production_connection_enabled === true
          : snapshot?.simulation_enabled) ||
    (production &&
      (!account.enabled ||
        account.provider !== "openai" ||
        account.auth_method !== "openai_siwc" ||
        account.billing_mode !== "subscription" ||
        account.base_url !== subscriptionResource)) ||
    policy === "unavailable";
  const currentAccountRevision = Math.max(
    account.revision,
    connection?.result_account_revision ?? account.revision,
  );
  const propose = (phase: ModelAcceptancePhase) => {
    if (productionInference && !validBudgetInput) return;
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
        ...(phase === "connection" && (identityOnly || subscriptionOnly)
          ? {
              connection_purpose: identityOnly
                ? ("identity_only" as const)
                : ("subscription_only" as const),
            }
          : {}),
        ...(prerequisite ? { prerequisite_id: prerequisite } : {}),
        ...(phase === "inference" ? { model } : {}),
        ...(productionInference
          ? {
              budget_mission_id: budgetMissionID,
              expected_mission_revision: Number(budgetRevision),
            }
          : {}),
      }),
    );
  };
  const chosen = (phase: ModelAcceptancePhase) =>
    operations.find((operation) => operation.id === selected[phase]);

  return (
    <section
      className="acceptance-operations"
      aria-label={
        productionInference
          ? "单次真实推理"
          : productionCatalog
            ? "单次真实模型目录"
            : production
              ? "单次真实订阅连接"
              : "单次本地模拟验收"
      }
    >
      <div className="section-heading">
        <div>
          <h4>
            {productionInference
              ? "单次真实推理"
              : productionCatalog
                ? "单次真实模型目录"
                : production
                  ? "单次真实订阅连接"
                  : "单次本地模拟验收"}
          </h4>
          <p className="muted">
            {productionInference
              ? "连接与目录历史只读；推理材料独立创建、批准和执行。"
              : productionCatalog
                ? "连接历史只读；目录材料独立创建、批准和执行。"
                : subscriptionOnly
                  ? "订阅连接材料单独审阅、批准和执行。"
                  : "连接、目录与推理分别审阅、批准和执行。"}
          </p>
        </div>
        <span className="route-lock">
          {productionInference
            ? snapshot?.production_inference_enabled === true
              ? "仅固定单次推理 · 不授权任务调用"
              : "推理审批不可用 · 历史只读"
            : productionCatalog
              ? "仅目录可单独审批 · 推理关闭"
              : production
                ? snapshot?.production_connection_enabled
                  ? "仅连接可单独审批"
                  : "连接审批不可用 · 历史只读"
                : snapshot?.simulation_enabled
                  ? "仅本地模拟"
                  : "生产真实访问保持关闭"}
        </span>
      </div>
      <p className="field-help">
        {productionInference
          ? "仅发送固定合成提示词；不发送源代码、任务上下文或对话。单次请求，无自动重试、轮询、刷新、重新登录、付费回退；不修改任务模型或角色策略。"
          : productionCatalog
            ? "仅允许 GET https://api.openai.com/v1/models；不发送提示词、源代码、任务上下文或对话。最多一次请求，无自动查询、重试、轮询、刷新、重新登录或付费回退；不修改任务模型和角色策略。"
            : production
              ? "此入口仅允许独立审批的真实订阅连接，不开放目录、推理或任务调用。不会自动打开浏览器、重试、轮询、刷新、付费降级、运行脚本或写入 Git。"
              : "只使用本进程拥有的合成 fixture。不会登录真实账户、读取真实凭据、自动重试、轮询、刷新、付费降级、运行脚本或写入 Git。"}
      </p>
      {productionInference &&
        (snapshot?.production_inference_blocker || authorizationExpired) && (
          <p className="warning-copy">
            推理保持关闭，不刷新、不重新登录。
            <code>
              {snapshot?.production_inference_blocker ??
                "authorization_expired"}
            </code>
          </p>
        )}
      {productionCatalog &&
        (snapshot?.production_catalog_blocker || authorizationExpired) && (
          <p className="warning-copy">
            {
              catalogBlockers[
                snapshot?.production_catalog_blocker ?? "authorization_expired"
              ]
            }{" "}
            <code>
              {snapshot?.production_catalog_blocker ?? "authorization_expired"}
            </code>
          </p>
        )}
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
          {productionInference
            ? "固定真实推理路由"
            : productionCatalog
              ? "固定真实目录路由"
              : production
                ? "固定真实连接路由"
                : "本地模拟路由"}
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
            {productionCatalog && " · 仅目录，不授予推理权限"}
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
            !!unresolved ||
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
          subscriptionOnly || productionCatalog
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
            (phase === "inference" &&
              (!catalog ||
                !model ||
                (productionInference && !validBudgetInput)));
          const identityClosedPhase = identityOnly && phase !== "connection";
          const historicalPhase =
            (historicalInference && phase === "inference") ||
            (productionInference && phase !== "inference") ||
            (productionCatalog && phase === "connection") ||
            (policy === "production_connection" && phase !== "connection");
          return (
            <li key={phase}>
              <div className="acceptance-step-heading">
                <strong>{phaseLabels[phase]}</strong>
                {historicalPhase ? (
                  <span className="route-lock">历史只读</span>
                ) : (
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
                )}
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
              {phase === "inference" && !historicalInference && (
                <>
                  {productionInference && (
                    <>
                      <label>
                        预算任务 ID
                        <input
                          aria-label="预算任务 ID"
                          value={budgetMissionID}
                          onChange={(event) =>
                            setBudgetMissionID(event.target.value)
                          }
                          disabled={mutationDisabled}
                        />
                      </label>
                      <label>
                        预期任务版本
                        <input
                          aria-label="预期任务版本"
                          inputMode="numeric"
                          value={budgetRevision}
                          onChange={(event) =>
                            setBudgetRevision(event.target.value)
                          }
                          disabled={mutationDisabled}
                        />
                      </label>
                      <p className="field-help">
                        从任务详情核对 ID
                        和当前版本；准备后显示服务器绑定的已用/上限预算。仅限暂停或阻塞任务；执行扣除原任务一次调用，不继续任务，不退款或重试。
                      </p>
                    </>
                  )}
                  <label>
                    推理模型
                    <select
                      aria-label="推理模型"
                      value={model}
                      disabled={
                        productionInference || mutationDisabled || !catalog
                      }
                      onChange={(event) => setModel(event.target.value)}
                    >
                      {(catalog?.catalog ?? [])
                        .filter(
                          (entry) =>
                            !productionInference || entry.id === inferenceModel,
                        )
                        .map((entry) => (
                          <option key={entry.id} value={entry.id}>
                            {entry.display_name} · {entry.id}
                          </option>
                        ))}
                    </select>
                  </label>
                </>
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
                  readOnly={historicalPhase}
                  disabled={
                    mutationDisabled ||
                    historicalPhase ||
                    (operation.source !== "local_simulation" &&
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
                    productionCatalog ||
                    productionInference ||
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
          aria-label={
            productionInference
              ? "真实推理与前提回执"
              : productionCatalog
                ? "真实目录与连接回执"
                : production
                  ? "真实订阅连接回执"
                  : "本地模拟回执"
          }
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
                    {operation.source === "production_inference"
                      ? "真实单次推理"
                      : operation.source === "production_catalog"
                        ? "真实模型目录"
                        : operation.source === "production_connection"
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
  readOnly = false,
  disabled,
  sessionDisabled,
  decide,
  execute,
  connectionSession,
  readConnectionStatus,
  cancel,
}: {
  operation: ModelAcceptanceOperation;
  readOnly?: boolean;
  disabled: boolean;
  sessionDisabled: boolean;
  decide: (decision: "approve" | "reject") => void;
  execute: () => void;
  connectionSession?: ConnectionSession;
  readConnectionStatus: () => void;
  cancel: () => void;
}) {
  const material = operation.material;
  const catalog = operation.source === "production_catalog";
  const inference = operation.source === "production_inference";
  const production =
    operation.source === "production_connection" || catalog || inference;
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
    (inference && !validInferenceBudget(material.inference_budget)) ||
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
        <dt>
          {inference
            ? "精确推理请求"
            : catalog
              ? "精确目录请求"
              : production
                ? "官方连接资源"
                : "本地模拟端点"}
        </dt>
        <dd>
          {catalog || inference
            ? `${material.request_method} ${material.request_url}`
            : material.route.base_url}
        </dd>
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
              {(catalog || inference) && (
                <> · 授权到期 {material.authorization.expires_at}</>
              )}
            </>
          )}
        </dd>
        {catalog && (
          <>
            <dt>连接前提</dt>
            <dd>{material.connection_id}</dd>
            <dt>本进程目录准入</dt>
            <dd>
              {material.production_admission?.profile} ·{" "}
              {material.production_admission?.generation}
            </dd>
            <dt>应用能力</dt>
            <dd>仅目录 · 推理：禁用 · 应用刷新：禁用 · 付费回退：禁用</dd>
          </>
        )}
        {inference && (
          <>
            <dt>原任务调用预算</dt>
            <dd>
              {material.inference_budget
                ? `${material.inference_budget.mission_id} · r${material.inference_budget.revision} · epoch ${material.inference_budget.epoch} · 已用 ${material.inference_budget.used_calls}/${material.inference_budget.max_calls} · 执行扣除 1 次`
                : "历史未绑定材料：只读，不可执行"}
            </dd>
            {material.inference_budget && (
              <>
                <dt>有效模型快照</dt>
                <dd>
                  <code>{material.inference_budget.snapshot_digest}</code>
                </dd>
              </>
            )}
            <dt>完整固定提示词</dt>
            <dd>{inferencePrompt}</dd>
            <dt>目录前提</dt>
            <dd>{material.prerequisite_id}</dd>
            <dt>目录摘要</dt>
            <dd>
              <code>{material.catalog_digest}</code>
            </dd>
            <dt>连接前提</dt>
            <dd>{material.connection_id}</dd>
            <dt>本进程推理准入</dt>
            <dd>
              {material.production_admission?.profile} ·{" "}
              {material.production_admission?.generation}
            </dd>
            <dt>请求选项</dt>
            <dd>store:false · stream:true · 响应上限 1 MiB</dd>
          </>
        )}
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
          {inference
            ? "仅一次固定合成提示词；创建和批准不发送请求。成功不授予任务、角色、MCP、Skills、脚本、Git 或再次执行权限。"
            : catalog
              ? "不发送提示词、源代码、任务上下文或对话；不修改凭据、任务模型或角色策略。创建与批准不发送目录请求。"
              : production
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
      {inference && (
        <div className="note">
          <p>
            未设置 max_output_tokens；没有精确 token
            或金额上限。可能消耗套餐额度；额度未知、远端计费未验证。执行前请核对提供方设置并接受可能的套餐消耗。
          </p>
          <p>
            30 秒本地超时与 1 MiB 接收限制不保证远端取消或退款。store:false
            不承诺提供方不保留运营数据。这是连接测试，不是编码质量评估。
          </p>
        </div>
      )}
      {inference && operation.attempts > 0 && (
        <div aria-label="推理回执">
          <InferenceDiagnostic value={operation.inference_diagnostic} />
          {material.inference_budget && (
            <p>
              已扣除原任务 1 次调用：{material.inference_budget.used_calls + 1}/
              {material.inference_budget.max_calls}
              ；本回执之后的任务预算可能变化。
            </p>
          )}
          <p>
            {operation.certainty === "completed"
              ? "传输：已完成"
              : operation.certainty === "rejected"
                ? "传输：已拒绝"
                : operation.certainty === "not_sent" &&
                    operation.state === "failed"
                  ? "传输：未发送"
                  : "传输：结果未知"}
          </p>
          <p>
            {operation.output_matched === true
              ? "输出校验：匹配"
              : operation.output_matched === false
                ? "输出校验：不匹配（不重试）"
                : "输出校验：未知"}
          </p>
          <p>
            {operation.usage_known
              ? `实际用量：输入 ${operation.input_tokens ?? 0} · 输出 ${operation.output_tokens ?? 0} tokens`
              : "实际用量：未知；不能报告零消耗或零费用"}
          </p>
          {operation.terminal_event && (
            <p>终止事件：{operation.terminal_event}</p>
          )}
          {operation.code && <p>错误分类：{operation.code}</p>}
          {operation.provider_code && (
            <p>提供方代码：{operation.provider_code}</p>
          )}
          <p>
            实际用量不是剩余额度或价格；不重试、不刷新、不重新登录、不付费回退。
          </p>
        </div>
      )}
      {production && !catalog && !inference && (
        <div className="note">
          <p>
            账户与工作区由用户在官方浏览器流程中选择；不要在本界面输入密码或验证码。
          </p>
          <p>
            令牌保存在 Foundry 原生
            系统凭据存储；本地数据库仅保留标识符、授权范围、版本/到期、密钥引用与脱敏审计证据，不保存令牌。
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
        !catalog &&
        !inference &&
        (operation.state === "failed" || operation.state === "unknown") && (
          <p className="warning-copy">
            连接失败或结果未知；请核对已记录的本地凭据提交与清理证据。未记录的结果仍无法确认，远端授权状态仍未验证。请显式读取原记录，不会重放请求。
          </p>
        )}
      {operation.phase === "connection" &&
        (operation.state === "failed" || operation.state === "unknown") && (
          <ConnectionDiagnostic
            diagnostic={operation.connection_diagnostic as unknown}
          />
        )}
      {catalog &&
        (operation.state === "failed" || operation.state === "unknown") && (
          <p className="warning-copy">
            目录失败或结果未知；保留原连接，不刷新、不重新登录、不重放请求。错误分类：
            {operation.code ?? "unknown"}。
          </p>
        )}
      {catalog && operation.state === "succeeded" && (
        <div aria-label="目录展示结果">
          <h5>本次目录结果（仅展示）</h5>
          {operation.catalog.length === 0 ? (
            <p>目录为空：本次响应没有可展示的模型。</p>
          ) : (
            <ul>
              {operation.catalog.map((entry) => (
                <li key={entry.id}>
                  <span>{entry.display_name}</span> · <code>{entry.id}</code>
                </li>
              ))}
            </ul>
          )}
          <p className="field-help">
            可见模型不代表推理成功、可用额度或价格；不会自动选为任务模型。
          </p>
        </div>
      )}
      {!readOnly && operation.state === "pending" && (
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
      {!readOnly &&
        operation.state === "approved" &&
        operation.attempts === 0 && (
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
      {!readOnly &&
        material.connection_mode === "browser_handoff" &&
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
        {inference
          ? "单次推理记录不是可复用执行许可；任务访问：关闭"
          : catalog
            ? "目录记录不是可复用执行许可；推理访问：关闭"
            : production
              ? "真实连接记录不代表当前执行许可；模型访问：关闭"
              : "真实访问：关闭"}{" "}
        · 远端计费：未验证 · 额度：未知
      </p>
    </div>
  );
}
