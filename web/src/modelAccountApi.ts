import { APIError, request } from "./api";
import type {
  BillingMode,
  ModelAccount,
  ModelAccountAcceptance,
  ModelAuthorization,
  ModelAuthorizationFlow,
  ModelCredential,
  ModelRemoteRevocation,
  ModelRevocation,
  ModelRoute,
  ModelRuntime,
} from "./types";

export interface AccountCreateInput {
  name: string;
  provider: string;
  base_url: string;
  auth_method: ModelAccount["auth_method"];
  billing_mode: BillingMode;
}
export interface AccountUpdateInput {
  name: string;
  enabled: boolean;
  expected_revision: number;
}
export interface CredentialPublishInput {
  expected_revision: number;
  operation_id: string;
  key: string;
}
const path = (id: string) => `/api/model-accounts/${encodeURIComponent(id)}`;
const json = (method: string, body: unknown) => ({
  method,
  body: JSON.stringify(body),
});
export const modelAccountApi = {
  acceptance: (id: string) =>
    request<{ acceptance: ModelAccountAcceptance }>(`${path(id)}/acceptance`),
  list: () => request<{ accounts: ModelAccount[] }>("/api/model-accounts"),
  create: (q: AccountCreateInput) =>
    request<{ account: ModelAccount }>(
      "/api/model-accounts",
      json("POST", {
        name: q.name,
        provider: q.provider,
        base_url: q.base_url,
        auth_method: q.auth_method,
        billing_mode: q.billing_mode,
      }),
    ),
  detail: (id: string) =>
    request<{ account: ModelAccount; credentials: ModelCredential[] }>(
      path(id),
    ),
  update: (id: string, q: AccountUpdateInput) =>
    request<{ account: ModelAccount }>(
      path(id),
      json("PUT", {
        name: q.name,
        enabled: q.enabled,
        expected_revision: q.expected_revision,
      }),
    ),
  publish: (id: string, q: CredentialPublishInput) =>
    request<{ operation: ModelCredential }>(
      `${path(id)}/credentials`,
      json("POST", {
        expected_revision: q.expected_revision,
        operation_id: q.operation_id,
        key: q.key,
      }),
    ),
  revoke: (id: string, version: number, expected_revision: number) =>
    request<{ operation: ModelCredential }>(
      `${path(id)}/credentials/${version}/revoke`,
      json("POST", { expected_revision }),
    ),
  operation: (id: string, operation: string) =>
    request<{ operation: ModelCredential }>(
      `${path(id)}/operations/${encodeURIComponent(operation)}`,
    ),
};
export interface ModelRouteCreateInput {
  account_id: string;
  expected_account_revision: number;
  adapter: string;
  environment: ModelRoute["environment"];
  extra_usage: ModelRoute["extra_usage"];
}
export const modelRouteApi = {
  list: () =>
    request<{ routes: ModelRoute[]; runtime: ModelRuntime }>(
      "/api/model-routes",
    ),
  create: (q: ModelRouteCreateInput) =>
    request<{ route: ModelRoute }>(
      "/api/model-routes",
      json("POST", {
        account_id: q.account_id,
        expected_account_revision: q.expected_account_revision,
        adapter: q.adapter,
        environment: q.environment,
        extra_usage: q.extra_usage,
      }),
    ),
};
const authorizationPath = (id: string) =>
  `/api/model-authorizations/${encodeURIComponent(id)}`;
export const modelAuthorizationApi = {
  list: () =>
    request<{
      authorizations: ModelAuthorization[];
      remote_revocations: ModelRemoteRevocation[];
    }>("/api/model-authorizations"),
  start: (accountID: string, expectedAccountRevision: number) =>
    request<{ flow: ModelAuthorizationFlow }>(
      "/api/model-authorizations/start",
      json("POST", {
        account_id: accountID,
        expected_account_revision: expectedAccountRevision,
      }),
    ),
  status: (operationID: string) =>
    request<{ flow: ModelAuthorizationFlow }>(
      `/api/model-authorizations/operations/${encodeURIComponent(operationID)}`,
    ),
  cancel: (operationID: string) =>
    request<{ flow: ModelAuthorizationFlow }>(
      `/api/model-authorizations/operations/${encodeURIComponent(operationID)}/cancel`,
      json("POST", {}),
    ),
  refresh: (authorizationID: string, expectedAccountRevision: number) =>
    request<{ authorization: ModelAuthorization }>(
      `${authorizationPath(authorizationID)}/refresh`,
      json("POST", { expected_account_revision: expectedAccountRevision }),
    ),
  revoke: (authorizationID: string, expectedAccountRevision: number) =>
    request<{ revocation: ModelRevocation }>(
      `${authorizationPath(authorizationID)}/revoke`,
      json("POST", { expected_account_revision: expectedAccountRevision }),
    ),
};
// Session memory only: deliberately contains no input key or request body.
// It survives settings navigation/unmount; a reload reads durable version history.
export interface CredentialIntent {
  operation_id: string;
  kind: "publish" | "revoke";
  version?: number;
}
export const credentialIntents = new Map<string, CredentialIntent>();
export function accountError(error: unknown): string {
  if (error instanceof APIError) {
    if (error.status === 409)
      return "版本或操作冲突。已请求刷新；请检查当前记录，不会自动重送。";
    if (error.status === 400 || error.status === 413)
      return "输入格式或大小不受支持，请检查后重新操作。";
    if (error.status === 401 || error.status === 403)
      return "本地会话不可用，请重新连接工作台。";
    if (error.status === 404)
      return "尚未查到原操作；结果仍未确认，不会重新提交。";
  }
  return "请求结果未确认；不会自动重送。请只查询原操作或重新读取记录。";
}
