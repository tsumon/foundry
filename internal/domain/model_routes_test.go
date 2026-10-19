package domain

import (
	"encoding/json"
	"testing"
)

// Losing any of these comparisons would let an unrelated receiver or billing
// channel acquire the authority of a user's explicitly chosen route.
func TestR5BRouteIdentityEndpointAndBillingFence(t *testing.T) {
	a := ModelAccount{ID: "a", Name: "Synthetic", Provider: "openai", AuthMethod: "api_key", BillingMode: "api_credits", BaseURL: "https://api.openai.com/v1"}
	r := ModelRoute{ID: "r", AccountID: "a", Adapter: "openai_api_chat", Protocol: "chat_completions", BaseURL: a.BaseURL, Environment: "live", SpendClass: "explicit_api", ExtraUsage: "off"}
	if err := ValidateModelRoute(r, a); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ModelRoute, *ModelAccount){
		"account":          func(r *ModelRoute, a *ModelAccount) { r.AccountID = "other" },
		"adapter":          func(r *ModelRoute, a *ModelAccount) { r.Adapter = "kimi_api_chat" },
		"protocol":         func(r *ModelRoute, a *ModelAccount) { r.Protocol = "responses" },
		"endpoint":         func(r *ModelRoute, a *ModelAccount) { r.BaseURL = "https://attacker.invalid/v1"; a.BaseURL = r.BaseURL },
		"account endpoint": func(r *ModelRoute, a *ModelAccount) { a.BaseURL = "https://api.openai.com" },
		"billing":          func(r *ModelRoute, a *ModelAccount) { a.BillingMode = "subscription" },
		"unknown billing":  func(r *ModelRoute, a *ModelAccount) { a.BillingMode = "unknown" },
		"spend":            func(r *ModelRoute, a *ModelAccount) { r.SpendClass = "subscription_only" },
		"auth":             func(r *ModelRoute, a *ModelAccount) { a.AuthMethod = "openai_siwc" },
		"environment":      func(r *ModelRoute, a *ModelAccount) { r.Environment = "unknown" },
		"query":            func(r *ModelRoute, a *ModelAccount) { r.BaseURL += "?secret=x"; a.BaseURL = r.BaseURL },
		"userinfo": func(r *ModelRoute, a *ModelAccount) {
			r.BaseURL = "https://fake@api.openai.com/v1"
			a.BaseURL = r.BaseURL
		},
	} {
		t.Run(name, func(t *testing.T) {
			rr, aa := r, a
			mutate(&rr, &aa)
			if ValidateModelRoute(rr, aa) == nil {
				t.Fatal("unsafe route accepted")
			}
		})
	}
	for _, base := range []string{"http://localhost:1234/v1", "http://127.0.0.2:1234/v1", "http://[::1]:1234/v1", "http://127.0.0.1.evil:1234/v1", "https://attacker.invalid/v1"} {
		r.Environment, r.BaseURL, a.BaseURL = "simulation", base, base
		if ValidateModelRoute(r, a) == nil {
			t.Fatal("nonliteral simulation receiver accepted", base)
		}
	}
	r.BaseURL, a.BaseURL = "http://127.0.0.1:1234/v1", "http://127.0.0.1:1234/v1"
	if err := ValidateModelRoute(r, a); err != nil {
		t.Fatal(err)
	}
}

func TestR5BRouteRejectsUnsupportedPackage(t *testing.T) {
	a := ModelAccount{Provider: "zhipu", AuthMethod: "api_key", BillingMode: "subscription", BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4"}
	r := ModelRoute{Adapter: "zhipu_api_chat", Protocol: "chat_completions", BaseURL: a.BaseURL, SpendClass: "explicit_api", Environment: "live"}
	if ValidateModelRoute(r, a) == nil {
		t.Fatal("coding plan gained a standard-API route")
	}
}

func TestR5BExplicitAdapterMatrixRejectsCodingEndpointSubstitution(t *testing.T) {
	for _, tc := range []struct{ adapter, provider, auth, protocol, billing, spend, base string }{
		{"openai_api_chat", "openai", "api_key", "chat_completions", "api_credits", "explicit_api", "https://api.openai.com/v1"},
		{"openai_api_responses", "openai", "api_key", "responses", "metered_api", "explicit_api", "https://api.openai.com/v1"},
		{"openai_siwc_responses", "openai", "openai_siwc", "responses", "subscription", "subscription_only", "https://api.openai.com/v1"},
		{"anthropic_api_messages", "anthropic", "api_key", "messages", "api_credits", "explicit_api", "https://api.anthropic.com/v1"},
		{"zhipu_api_chat", "zhipu", "api_key", "chat_completions", "api_credits", "explicit_api", "https://open.bigmodel.cn/api/paas/v4"},
		{"kimi_api_chat", "kimi", "api_key", "chat_completions", "metered_api", "explicit_api", "https://api.moonshot.cn/v1"},
		{"kimi_api_chat", "kimi", "api_key", "chat_completions", "metered_api", "explicit_api", "https://api.moonshot.ai/v1"},
		{"kimi_code_chat", "kimi", "api_key", "chat_completions", "subscription", "interactive_package", "https://api.kimi.com/coding/v1"},
	} {
		t.Run(tc.adapter+tc.base, func(t *testing.T) {
			a := ModelAccount{ID: "a", Name: "Synthetic", Provider: tc.provider, AuthMethod: tc.auth, BillingMode: tc.billing, BaseURL: tc.base}
			r := ModelRoute{ID: "r", AccountID: "a", Adapter: tc.adapter, Protocol: tc.protocol, SpendClass: tc.spend, BaseURL: tc.base, Environment: "live", ExtraUsage: "off"}
			if err := ValidateModelRoute(r, a); err != nil {
				t.Fatal("valid adapter rejected", err)
			}
			if tc.adapter == "zhipu_api_chat" {
				for _, base := range []string{"https://open.bigmodel.cn/api/coding/paas/v4", "https://open.bigmodel.cn/api/anthropic"} {
					r.BaseURL, a.BaseURL = base, base
					if ValidateModelRoute(r, a) == nil {
						t.Fatal("unsupported package endpoint accepted")
					}
				}
			}
		})
	}
}

func inferenceSnapshotFixture(t *testing.T) Mission {
	t.Helper()
	m := validModelSnapshotMission(t)
	m.WorkspaceMode, m.ModelSnapshot.SchemaVersion = "account_snapshot_v2", 2
	for role, s := range m.ModelSnapshot.Roles {
		s.Account.Provider, s.Account.BillingMode, s.Account.BaseURL = "openai", "api_credits", "https://api.openai.com/v1"
		s.Profile.RouteID, s.Profile.BaseURL = "r", s.Account.BaseURL
		s.Route = &ModelRoute{ID: "r", AccountID: "a", Adapter: "openai_api_responses", Protocol: "responses", BaseURL: s.Account.BaseURL, Environment: "live", SpendClass: "explicit_api", ExtraUsage: "off"}
		m.Profiles[role], m.ModelSnapshot.Roles[role] = s.Profile, s
	}
	m.ModelSnapshot.Digest, _ = ModelSnapshotDigest(*m.ModelSnapshot)
	return m
}

func TestR5BSnapshotExactModeSchemaPairAndRouteFence(t *testing.T) {
	for _, tc := range []struct{ base, wrapper, tool string }{
		{"", "account_snapshot_v2", ""},
		{WorkspaceModeGenerationsV1, "generations_account_snapshot_v2", ""},
		{WorkspaceModeControlledReadV1, "generations_controlled_read_account_snapshot_v2", ToolModeControlledReadV1},
		{WorkspaceModeControlledMCPReadV2, "generations_controlled_mcp_read_account_snapshot_v2", ToolModeControlledMCPReadV2},
	} {
		mode, err := InferenceWorkspaceMode(tc.base)
		if err != nil || mode != tc.wrapper {
			t.Fatalf("wrapper %q %v", mode, err)
		}
		m := inferenceSnapshotFixture(t)
		m.WorkspaceMode, m.ToolMode = mode, tc.tool
		if err := ValidateMissionModelSnapshot(m); err != nil || !IsInferenceSnapshot(m) {
			t.Fatalf("valid v2 rejected: %v", err)
		}
		m.ModelSnapshot.SchemaVersion = 1
		m.ModelSnapshot.Digest, _ = ModelSnapshotDigest(*m.ModelSnapshot)
		if ValidateMissionModelSnapshot(m) == nil || IsInferenceSnapshot(m) {
			t.Fatal("v1 became inference through wrapper")
		}
	}
	for name, mutate := range map[string]func(*Mission){
		"v1 wrapper":    func(m *Mission) { m.WorkspaceMode = "account_snapshot_v1" },
		"missing route": func(m *Mission) { s := m.ModelSnapshot.Roles[Worker]; s.Route = nil; m.ModelSnapshot.Roles[Worker] = s },
		"wrong route":   func(m *Mission) { m.ModelSnapshot.Roles[Worker].Route.ID = "other" },
		"wrong account": func(m *Mission) { m.ModelSnapshot.Roles[Worker].Route.AccountID = "other" },
		"key authorization injection": func(m *Mission) {
			s := m.ModelSnapshot.Roles[Worker]
			s.Authorization = &ModelAuthorizationBinding{ID: "auth"}
			m.ModelSnapshot.Roles[Worker] = s
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := inferenceSnapshotFixture(t)
			mutate(&m)
			m.ModelSnapshot.Digest, _ = ModelSnapshotDigest(*m.ModelSnapshot)
			if ValidateMissionModelSnapshot(m) == nil {
				t.Fatal("invalid v2 accepted")
			}
		})
	}
	// Schema1's JSON must not gain route or authorization semantics, even if its
	// digest is recomputed after injection.
	for _, field := range []string{"route", "authorization"} {
		m := validModelSnapshotMission(t)
		raw, _ := json.Marshal(m)
		var body map[string]any
		json.Unmarshal(raw, &body)
		body["model_snapshot"].(map[string]any)["roles"].(map[string]any)["worker"].(map[string]any)[field] = map[string]any{"id": "injected"}
		raw, _ = json.Marshal(body)
		json.Unmarshal(raw, &m)
		m.ModelSnapshot.Digest, _ = ModelSnapshotDigest(*m.ModelSnapshot)
		if ValidateMissionModelSnapshot(m) == nil {
			t.Fatal("schema1 accepted", field)
		}
	}
}
