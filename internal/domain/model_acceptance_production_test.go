package domain

import (
	"encoding/json"
	"testing"
)

// Catches both loss of frozen admission provenance and promotion of simulation.
func TestProductionMaterialRequiresExactLiveConnectionShape(t *testing.T) {
	m := subscriptionMaterial()
	m.Account.BaseURL = "https://api.openai.com/v1"
	m.Route.BaseURL, m.Route.Environment = m.Account.BaseURL, "live"
	m.ConnectionMode, m.LoginTimeoutSeconds, m.DeadlineSeconds, m.MaxAttempts = "browser_handoff", 600, 30, 1
	raw, _ := json.Marshal(m)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	fields["production_admission"] = map[string]string{"generation": "0123456789abcdef0123456789abcdef", "profile": "openai_siwc_subscription_v1"}
	raw, _ = json.Marshal(fields)
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if !m.ValidConnectionPurpose() {
		t.Fatal("exact production connection rejected")
	}
	for _, mutate := range []func(*ModelAcceptanceMaterial){
		func(m *ModelAcceptanceMaterial) { m.Route.Environment = "simulation" },
		func(m *ModelAcceptanceMaterial) { m.ConnectionMode = ""; m.LoginTimeoutSeconds = 0 },
		func(m *ModelAcceptanceMaterial) { m.Account.Enabled = false },
		func(m *ModelAcceptanceMaterial) { m.Route.SpendClass = "paid_api" },
		func(m *ModelAcceptanceMaterial) { m.Account.BaseURL = "http://127.0.0.1:123/v1" },
	} {
		bad := m
		mutate(&bad)
		if bad.ValidConnectionPurpose() {
			t.Fatal("invalid production shape accepted")
		}
	}
}

func TestSimulationCannotCarryProductionAdmission(t *testing.T) {
	m := subscriptionMaterial()
	raw, _ := json.Marshal(m)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	fields["production_admission"] = map[string]string{"generation": "0123456789abcdef0123456789abcdef", "profile": "openai_siwc_subscription_v1"}
	raw, _ = json.Marshal(fields)
	_ = json.Unmarshal(raw, &m)
	if m.ValidConnectionPurpose() {
		t.Fatal("simulation carries production admission")
	}
}
