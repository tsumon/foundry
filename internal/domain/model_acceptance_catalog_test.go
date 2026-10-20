package domain

import "testing"

func TestCatalogProfileCannotAuthorizeConnection(t *testing.T) {
	m := ModelAcceptanceMaterial{ProductionAdmission: &ProductionAdmission{Generation: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Profile: "openai_siwc_catalog_v1"}, Phase: "catalog", RequestMethod: "GET", RequestURL: OpenAICatalogURL, Account: ModelAccount{ID: "a", Enabled: true, Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", BaseURL: OpenAISubscriptionRequestedResource}, Route: ModelRoute{AccountID: "a", Environment: "live", Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: OpenAISubscriptionRequestedResource, SpendClass: "subscription_only", ExtraUsage: "off"}, Authorization: &ModelAuthorization{State: "available", Generation: 1, Binding: ModelAuthorizationBinding{ID: "proof", AccountID: "a", Issuer: "https://auth.openai.com", Subject: "synthetic", ClientID: "synthetic", SpendClass: "subscription_only", Scopes: []string{"resource.invoke", "chatgpt.tokens.use.direct"}}}, DeadlineSeconds: 30, MaxAttempts: 1}
	if !m.ValidProductionCatalog() {
		t.Fatal("valid catalog rejected")
	}
	if m.ValidConnectionPurpose() {
		t.Fatal("catalog profile passed connection validation")
	}
}
