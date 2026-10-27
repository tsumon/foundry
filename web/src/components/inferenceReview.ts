import type { ModelAcceptanceOperation } from "../types";

export const inferenceModel = "gpt-6.1-sol";
export const inferencePrompt = "Reply exactly: FOUNDRY_INFERENCE_OK";
export const inferenceProfile = "openai_siwc_inference_v1";
const digest = /^[a-f0-9]{64}$/;

export function validInferenceBudget(value: unknown): boolean {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const b = value as Record<string, unknown>;
  return (
    Object.keys(b).length === 7 &&
    b.schema_version === 1 &&
    typeof b.mission_id === "string" &&
    /^[A-Za-z0-9_-]{1,256}$/.test(b.mission_id) &&
    typeof b.snapshot_digest === "string" &&
    digest.test(b.snapshot_digest) &&
    [b.revision, b.epoch, b.used_calls, b.max_calls].every(
      (n) => typeof n === "number" && Number.isSafeInteger(n) && n >= 0,
    ) &&
    (b.revision as number) > 0 &&
    (b.revision as number) < Number.MAX_SAFE_INTEGER &&
    (b.used_calls as number) < (b.max_calls as number)
  );
}
const providerCodes = new Set(
  "invalid_api_key authentication_error permission_error insufficient_quota rate_limit_exceeded rate_limit_error overloaded_error invalid_request_error model_not_found insufficient_balance exceeded_current_quota_error usage_limit_reached subscription_sharing_user_not_eligible subscription_sharing_usage_limit_exceeded subscription_sharing_usage_unavailable subscription_sharing_unsupported_capability subscription_sharing_route_not_supported subscription_sharing_invalid_user chatpass_v2_scope_not_authorized chatpass_v2_invalid_authorization_context subscription_sharing_user_unavailable".split(
    " ",
  ),
);
const providerParams = new Set(
  "model input tools tool_choice temperature max_output_tokens max_tokens service_tier stream store instructions".split(
    " ",
  ),
);

// Reuse the catalog's identical account/authorization boundary only after checking
// every inference-only field. No DTO value can choose a request body or endpoint.
export function validInferenceMaterial(
  o: ModelAcceptanceOperation,
  catalogBoundary: (o: ModelAcceptanceOperation) => boolean,
): boolean {
  const {
    model,
    inference_prompt,
    prompt_digest,
    catalog_digest,
    response_byte_limit,
    inference_budget,
    ...base
  } = o.material;
  return (
    o.phase === "inference" &&
    (inference_budget === undefined ||
      validInferenceBudget(inference_budget)) &&
    model === inferenceModel &&
    inference_prompt === inferencePrompt &&
    prompt_digest ===
      "90f75a47756b44fbcfe856df18999e6c3666e9045e0a95dca5f3bce873a2f8bb" &&
    digest.test(catalog_digest ?? "") &&
    response_byte_limit === 1048576 &&
    typeof base.prerequisite_id === "string" &&
    /^[A-Za-z0-9_-]{1,256}$/.test(base.prerequisite_id) &&
    base.request_method === "POST" &&
    base.request_url === "https://api.openai.com/v1/responses" &&
    base.production_admission?.profile === inferenceProfile &&
    catalogBoundary({
      ...o,
      phase: "catalog",
      material: {
        ...base,
        phase: "catalog",
        request_method: "GET",
        request_url: "https://api.openai.com/v1/models",
        prerequisite_id: base.connection_id,
        production_admission: {
          ...base.production_admission,
          profile: "openai_siwc_catalog_v1",
        },
      },
    })
  );
}

export function validInferenceReceipt(o: ModelAcceptanceOperation): boolean {
  const terminal = o.terminal_event;
  const completed = o.state === "succeeded";
  const unknown = o.state === "unknown";
  if (
    ["credential_access_failed", "credential_wait_expired"].includes(
      o.code ?? "",
    ) &&
    (o.state !== "failed" ||
      o.certainty !== "not_sent" ||
      o.inference_diagnostic !== undefined)
  )
    return false;
  if (
    o.catalog.length !== 0 ||
    (o.inference_diagnostic !== undefined &&
      (o.attempts !== 1 ||
        !["succeeded", "failed", "unknown"].includes(o.state))) ||
    o.connection_diagnostic !== undefined ||
    (o.attempts === 0 ? o.claim_sequence !== 0 : o.claim_sequence <= 0) ||
    typeof o.usage_known !== "boolean" ||
    ![o.input_tokens, o.output_tokens].every(
      (n) => n === undefined || (Number.isSafeInteger(n) && n >= 0),
    ) ||
    (!o.usage_known &&
      (o.input_tokens !== undefined || o.output_tokens !== undefined)) ||
    (o.usage_known &&
      !completed &&
      !(
        o.state === "failed" &&
        o.certainty === "rejected" &&
        terminal === "response.failed"
      )) ||
    (o.request_id !== undefined &&
      !/^[A-Za-z0-9_.:/-]{1,128}$/.test(o.request_id)) ||
    (o.provider_code !== undefined && !providerCodes.has(o.provider_code)) ||
    (o.provider_param !== undefined && !providerParams.has(o.provider_param))
  )
    return false;
  if (completed)
    return (
      o.certainty === "completed" &&
      terminal === "response.completed" &&
      typeof o.output_matched === "boolean" &&
      digest.test(o.output_digest ?? "") &&
      o.resolved_model === inferenceModel &&
      o.provider_code === undefined &&
      o.provider_param === undefined &&
      o.code === undefined
    );
  if (
    o.output_matched !== undefined ||
    o.output_digest !== undefined ||
    o.resolved_model !== undefined
  )
    return false;
  if (
    unknown ||
    o.attempts === 0 ||
    o.state === "running" ||
    o.certainty === "not_sent"
  )
    return (
      terminal === undefined &&
      o.provider_code === undefined &&
      o.provider_param === undefined &&
      o.request_id === undefined &&
      !o.usage_known &&
      o.certainty ===
        (unknown || o.state === "running" ? "unknown" : "not_sent")
    );
  return (
    o.state === "failed" &&
    ["rejected", "not_sent"].includes(o.certainty) &&
    (terminal === undefined ||
      terminal === "response.failed" ||
      terminal === "error")
  );
}
