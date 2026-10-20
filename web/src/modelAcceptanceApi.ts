import { APIError, request } from "./api";
import type {
  ModelAcceptanceHistory,
  ModelAcceptanceOperation,
  ModelAcceptanceOperationResponse,
  ModelAcceptanceProposal,
} from "./types";

const base = (accountID: string) =>
  `/api/model-accounts/${encodeURIComponent(accountID)}/acceptance-operations`;
const detail = (accountID: string, operationID: string) =>
  `${base(accountID)}/${encodeURIComponent(operationID)}`;
const json = (body: unknown): RequestInit => ({
  method: "POST",
  body: JSON.stringify(body),
});

export const modelAcceptanceApi = {
  history: (accountID: string) =>
    request<ModelAcceptanceHistory>(base(accountID)),
  propose: (accountID: string, proposal: ModelAcceptanceProposal) =>
    request<ModelAcceptanceOperationResponse>(base(accountID), json(proposal)),
  operation: (accountID: string, operationID: string) =>
    request<ModelAcceptanceOperationResponse>(detail(accountID, operationID)),
  decide: (
    accountID: string,
    operation: ModelAcceptanceOperation,
    decision: "approve" | "reject",
  ) =>
    request<ModelAcceptanceOperationResponse>(
      `${detail(accountID, operation.id)}/decision`,
      json({
        expected_revision: operation.revision,
        material_digest: operation.material_digest,
        decision,
      }),
    ),
  execute: (accountID: string, operation: ModelAcceptanceOperation) =>
    request<ModelAcceptanceOperationResponse>(
      `${detail(accountID, operation.id)}/execute`,
      json({
        expected_revision: operation.revision,
        material_digest: operation.material_digest,
      }),
    ),
  cancel: (accountID: string, operation: ModelAcceptanceOperation) =>
    request<ModelAcceptanceOperationResponse>(
      `${detail(accountID, operation.id)}/cancel`,
      json({
        expected_revision: operation.revision,
        material_digest: operation.material_digest,
      }),
    ),
};

const messages: Record<string, string> = {
  acceptance_simulation_disabled:
    "本进程未启用构造器拥有的本地模拟；真实访问保持关闭。",
  acceptance_conflict: "材料、账户版本或操作状态已经变化；不会自动重送。",
  acceptance_record_unavailable:
    "验收记录当前不可读取或无法持久保存；不会自动重试。",
  not_found: "未找到原验收记录；不会创建替代操作。",
  validation: "验收请求不符合固定接口；不会发送替代请求。",
  invalid_receipt: "验收回执格式无效，当前材料已清除。",
  network_unknown: "请求结果未知；所有写操作保持锁定，必须显式读取原记录。",
  request_failed: "验收服务当前不可用；不会自动重试。",
};

export interface ModelAcceptanceError {
  code: string;
  message: string;
  requiresRead: boolean;
}

// Never expose server/provider messages or payloads. The finite machine code is
// the only server-originated value rendered, and is bounded to identifier text.
export function modelAcceptanceError(
  cause: unknown,
  mutation = false,
): ModelAcceptanceError {
  if (!(cause instanceof APIError))
    return {
      code: "network_unknown",
      message: messages.network_unknown,
      requiresRead: mutation,
    };
  const code = /^[a-z][a-z0-9_]{0,63}$/.test(cause.code)
    ? cause.code
    : "request_failed";
  return {
    code,
    message: messages[code] ?? messages.request_failed,
    requiresRead: mutation && (cause.status === 409 || cause.status >= 500),
  };
}
