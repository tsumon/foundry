package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserHandoffMaterialCannotDiscardOrChangePolicy(t *testing.T) {
	legacy := ModelAcceptanceMaterial{}
	before := AcceptanceDigest(legacy)
	raw, _ := json.Marshal(legacy)
	if strings.Contains(string(raw), "connection_mode") || strings.Contains(string(raw), "login_timeout_seconds") {
		t.Fatal("legacy digest projection changed")
	}
	var roundtrip ModelAcceptanceMaterial
	if err := json.Unmarshal(raw, &roundtrip); err != nil || AcceptanceDigest(roundtrip) != before {
		t.Fatal("legacy roundtrip changed")
	}
	for _, addition := range []string{`,"connection_mode":"browser_handoff","login_timeout_seconds":600`, `,"login_timeout_seconds":600`, `,"connection_mode":"other"`} {
		var m ModelAcceptanceMaterial
		if err := json.Unmarshal([]byte(strings.TrimSuffix(string(raw), "}")+addition+"}"), &m); err != nil {
			t.Fatal(err)
		}
		if m.ValidConnectionPurpose() {
			t.Fatal("invalid handoff policy accepted or discarded")
		}
		if AcceptanceDigest(m) == before {
			t.Fatal("handoff policy absent from digest")
		}
	}
}

func TestBrowserHandoffMaterialBindsExactLimitsAndPurpose(t *testing.T) {
	m := subscriptionMaterial()
	m.ConnectionMode = "browser_handoff"
	m.LoginTimeoutSeconds = 600
	m.DeadlineSeconds = 30
	m.MaxAttempts = 1
	if !m.ValidConnectionPurpose() {
		t.Fatal("valid browser material rejected")
	}
	for _, mutate := range []func(*ModelAcceptanceMaterial){func(m *ModelAcceptanceMaterial) { m.LoginTimeoutSeconds = 599 }, func(m *ModelAcceptanceMaterial) { m.DeadlineSeconds = 29 }, func(m *ModelAcceptanceMaterial) { m.ConnectionMode = "" }, func(m *ModelAcceptanceMaterial) { m.ConnectionPurpose = "identity_only" }, func(m *ModelAcceptanceMaterial) { m.MaxAttempts = 2 }, func(m *ModelAcceptanceMaterial) { m.Phase = "catalog" }} {
		bad := m
		mutate(&bad)
		if bad.ValidConnectionPurpose() || AcceptanceDigest(bad) == AcceptanceDigest(m) {
			t.Fatal("edited browser policy accepted/unbound")
		}
	}
}
