package domain

import (
	"encoding/json"
	"testing"
)

func subscriptionMaterial() ModelAcceptanceMaterial {
	account := ModelAccount{ID: "subscription", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", BaseURL: "http://127.0.0.1:12345/v1", Enabled: true}
	return ModelAcceptanceMaterial{
		Phase: "connection", ConnectionPurpose: "subscription_only",
		RequestedScopes:   []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"},
		RequestedResource: "https://api.openai.com/v1",
		Account:           account,
		Route:             ModelRoute{ID: "subscription-route", AccountID: account.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: account.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"},
	}
}

func TestSubscriptionConnectionMaterialRequiresExactServerOwnedPermission(t *testing.T) {
	valid := subscriptionMaterial()
	if !valid.ValidConnectionPurpose() {
		t.Fatal("exact subscription connection material rejected")
	}
	for _, test := range []struct {
		name   string
		mutate func(*ModelAcceptanceMaterial)
	}{
		{"missing scopes", func(m *ModelAcceptanceMaterial) { m.RequestedScopes = nil }},
		{"extra scope", func(m *ModelAcceptanceMaterial) { m.RequestedScopes = append(m.RequestedScopes, "extra") }},
		{"reordered scopes", func(m *ModelAcceptanceMaterial) {
			m.RequestedScopes[0], m.RequestedScopes[1] = m.RequestedScopes[1], m.RequestedScopes[0]
		}},
		{"missing resource", func(m *ModelAcceptanceMaterial) { m.RequestedResource = "" }},
		{"wrong resource", func(m *ModelAcceptanceMaterial) { m.RequestedResource = "http://127.0.0.1:12345/v1" }},
		{"model", func(m *ModelAcceptanceMaterial) { m.Model = "gpt" }},
		{"catalog phase", func(m *ModelAcceptanceMaterial) { m.Phase = "catalog" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := valid
			m.RequestedScopes = append([]string(nil), valid.RequestedScopes...)
			test.mutate(&m)
			if m.ValidConnectionPurpose() {
				t.Fatal("invalid subscription permission accepted")
			}
		})
	}
	identity := valid
	identity.ConnectionPurpose = "identity_only"
	identity.RequestedScopes = []string{"openid", "profile", "email"}
	identity.RequestedResource = ""
	if !identity.ValidConnectionPurpose() {
		t.Fatal("identity-only contract regressed")
	}
}

func TestSubscriptionResourceIsDigestBoundWithoutChangingLegacyJSON(t *testing.T) {
	m := subscriptionMaterial()
	digest := AcceptanceDigest(m)
	m.RequestedResource = "https://api.openai.com/v2"
	if AcceptanceDigest(m) == digest {
		t.Fatal("resource mutation retained material digest")
	}
	legacy := ModelAcceptanceMaterial{Phase: "connection"}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["requested_resource"] != nil || fields["requested_scopes"] != nil || fields["connection_purpose"] != nil {
		t.Fatalf("legacy material gained permission fields: %s", raw)
	}
}
