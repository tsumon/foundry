package domain

const OpenAIInferenceModel = "gpt-6.1-sol"
const OpenAIInferencePrompt = "Reply exactly: FOUNDRY_INFERENCE_OK"
const OpenAIInferenceExpected = "FOUNDRY_INFERENCE_OK"
const OpenAIInferenceURL = "https://api.openai.com/v1/responses"
const OpenAIInferenceBody = `{"model":"gpt-6.1-sol","input":[{"role":"user","content":"Reply exactly: FOUNDRY_INFERENCE_OK"}],"store":false,"stream":true}`

// InferenceEvidence retains only a bounded projection, never generated text.
type InferenceEvidence struct {
	TerminalEvent string `json:"terminal_event,omitempty"`
	OutputMatched *bool  `json:"output_matched,omitempty"`
	OutputDigest  string `json:"output_digest,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
	ResolvedModel string `json:"resolved_model,omitempty"`
	ProviderCode  string `json:"provider_code,omitempty"`
	ProviderParam string `json:"provider_param,omitempty"`
}

// Finite provider diagnostics are shared by decoding and durable validation.
func SafeInferenceProviderCode(code string) string {
	switch code {
	case "invalid_api_key", "authentication_error", "permission_error", "insufficient_quota", "rate_limit_exceeded", "rate_limit_error", "overloaded_error", "invalid_request_error", "model_not_found", "insufficient_balance", "exceeded_current_quota_error", "usage_limit_reached", "subscription_sharing_user_not_eligible", "subscription_sharing_usage_limit_exceeded", "subscription_sharing_usage_unavailable", "subscription_sharing_unsupported_capability", "subscription_sharing_route_not_supported", "subscription_sharing_invalid_user", "chatpass_v2_scope_not_authorized", "chatpass_v2_invalid_authorization_context", "subscription_sharing_user_unavailable":
		return code
	}
	return ""
}
func SafeInferenceProviderParam(param string) string {
	switch param {
	case "model", "input", "tools", "tool_choice", "temperature", "max_output_tokens", "max_tokens", "service_tier", "stream", "store", "instructions":
		return param
	}
	return ""
}

func (m ModelAcceptanceMaterial) ValidProductionInference() bool {
	if m.ProductionAdmission == nil || m.ProductionAdmission.Profile != "openai_siwc_inference_v1" || !m.ProductionAdmission.Valid() || m.Phase != "inference" || m.RequestMethod != "POST" || m.RequestURL != OpenAIInferenceURL || m.Model != OpenAIInferenceModel || m.InferencePrompt != OpenAIInferencePrompt || m.PromptDigest != AcceptanceDigest(OpenAIInferencePrompt) || m.ResponseByteLimit != 1<<20 {
		return false
	}
	// The account, route and credential permission boundary is identical to catalog.
	m.ProductionAdmission = &ProductionAdmission{Generation: m.ProductionAdmission.Generation, Profile: "openai_siwc_catalog_v1"}
	m.Phase, m.RequestMethod, m.RequestURL = "catalog", "GET", OpenAICatalogURL
	m.Model, m.PromptDigest, m.InferencePrompt, m.CatalogDigest, m.ResponseByteLimit = "", "", "", "", 0
	return m.ValidProductionCatalog()
}
