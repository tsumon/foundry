package engine

import (
	"foundry/internal/domain"
	"testing"
)

func TestCodingSubscriptionRequiresCatalogSelection(t *testing.T) {
	a := domain.ModelAccount{ID: "a", Revision: 1, Name: "Synthetic", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "https://api.openai.com/v1"}
	r := domain.ModelRoute{ID: "r", AccountID: "a", Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"}
	p := domain.Profile{ID: "p", AccountID: "a", RouteID: "r", BaseURL: r.BaseURL, Protocol: r.Protocol, Model: "gpt-5.1"}
	profiles := map[domain.Role]domain.Profile{}
	snap := &domain.MissionModelSnapshot{SchemaVersion: 2, Roles: map[domain.Role]domain.RoleModelSelection{}}
	for _, role := range domain.RolePolicyRoles() {
		profiles[role] = p
		snap.Roles[role] = domain.RoleModelSelection{Profile: p, Account: &a, Route: &r}
	}
	if e := modelC4Profiles(profiles, snap); e == nil {
		t.Fatal("unproven live subscription model enabled a new coding task")
	}
}
