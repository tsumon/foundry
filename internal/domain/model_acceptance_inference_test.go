package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProductionInferencePurposeIsIndependent(t *testing.T) {
	m := ModelAcceptanceMaterial{ProductionAdmission: &ProductionAdmission{Generation: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Profile: "openai_siwc_catalog_v1"}, Phase: "catalog", RequestMethod: "GET", RequestURL: OpenAICatalogURL, Account: ModelAccount{ID: "a", Enabled: true, Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", BaseURL: OpenAISubscriptionRequestedResource}, Route: ModelRoute{AccountID: "a", Environment: "live", Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: OpenAISubscriptionRequestedResource, SpendClass: "subscription_only", ExtraUsage: "off"}, Authorization: &ModelAuthorization{State: "available", Generation: 1, Binding: ModelAuthorizationBinding{ID: "proof", AccountID: "a", Issuer: "https://auth.openai.com", Subject: "synthetic", ClientID: "synthetic", SpendClass: "subscription_only", Scopes: []string{"resource.invoke", "chatgpt.tokens.use.direct"}}}, DeadlineSeconds: 30, MaxAttempts: 1}
	m.ProductionAdmission.Profile = "openai_siwc_inference_v1"
	m.Phase, m.RequestMethod, m.RequestURL = "inference", "POST", "https://api.openai.com/v1/responses"
	m.Model, m.InferencePrompt, m.PromptDigest, m.ResponseByteLimit = "gpt-6.1-sol", "Reply exactly: FOUNDRY_INFERENCE_OK", AcceptanceDigest("Reply exactly: FOUNDRY_INFERENCE_OK"), 1<<20
	if !m.ValidProductionInference() || !m.ValidAcceptancePurpose() || m.ValidProductionCatalog() || m.ValidConnectionPurpose() {
		t.Fatal("purpose boundary not independent")
	}
	m.ProductionAdmission.Profile = "openai_siwc_catalog_v1"
	if m.ValidAcceptancePurpose() {
		t.Fatal("inference fields accepted by catalog")
	}
}

func TestProductionInferenceOutputMatchAbsentVersusFalse(t *testing.T) {
	pending, _ := json.Marshal(ModelAcceptanceOperation{})
	if strings.Contains(string(pending), "output_matched") {
		t.Fatal("pending reported mismatch")
	}
	matched := false
	raw, _ := json.Marshal(ModelAcceptanceOperation{InferenceEvidence: InferenceEvidence{OutputMatched: &matched}})
	if !strings.Contains(string(raw), `"output_matched":false`) {
		t.Fatal("content mismatch omitted")
	}
	var restored ModelAcceptanceOperation
	if json.Unmarshal(raw, &restored) != nil || restored.OutputMatched == nil || *restored.OutputMatched {
		t.Fatal("false match lost on roundtrip")
	}
}
