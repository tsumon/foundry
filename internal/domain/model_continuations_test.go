package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func continuationDomainFixture(t *testing.T) Mission {
	t.Helper()
	m := validModelSnapshotMission(t)
	m.ID = "fixture"
	m.Revision = 13
	m.WorkspaceMode = "account_snapshot_v2"
	m.ModelSnapshot.SchemaVersion = 2
	for role, sel := range m.ModelSnapshot.Roles {
		sel.Account.Provider = "openai"
		sel.Account.AuthMethod = "openai_siwc"
		sel.Account.BillingMode = "subscription"
		sel.Account.BaseURL = "https://api.openai.com/v1"
		sel.Account.CurrentCredentialVersion = 0
		sel.Profile.BaseURL = sel.Account.BaseURL
		sel.Profile.CredentialVersion = 0
		sel.Profile.RouteID = "route"
		sel.Route = &ModelRoute{ID: "route", AccountID: "a", Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: sel.Account.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"}
		sel.Authorization = &ModelAuthorizationBinding{ID: "original", AccountID: "a", Issuer: "https://auth.openai.com", Subject: "subject", ClientID: "client", SpendClass: "subscription_only", Scopes: []string{"openid", "resource.invoke", "chatgpt.tokens.use.direct"}}
		m.Profiles[role] = sel.Profile
		m.ModelSnapshot.Roles[role] = sel
	}
	m.ModelSnapshot.Digest, _ = ModelSnapshotDigest(*m.ModelSnapshot)
	var next MissionModelSnapshot
	raw, _ := json.Marshal(m.ModelSnapshot)
	if err := json.Unmarshal(raw, &next); err != nil {
		t.Fatal(err)
	}
	for role, sel := range next.Roles {
		sel.Authorization.ID = "replacement"
		sel.Profile.CatalogOperationID = "new-catalog"
		sel.Catalog = &ModelCatalogProvenance{OperationID: "new-catalog", ReceiptDigest: strings.Repeat("a", 64), AuthorizationGeneration: 1, ExpiresAt: "2030-01-01T01:00:00Z"}
		next.Roles[role] = sel
	}
	next.Digest, _ = ModelSnapshotDigest(next)
	c := ModelContinuation{SchemaVersion: 1, ID: "continuation", MissionID: m.ID, PreparedRevision: 10, PredecessorDigest: m.ModelSnapshot.Digest, Replacement: next, AffectedRoles: RolePolicyRoles(), AuthorizationGeneration: 1, AuthorizationExpiresAt: "2030-01-01T01:00:00Z", CreatedAt: "2030-01-01T00:00:00Z", ExpiresAt: "2030-01-01T00:10:00Z"}
	c.Digest = ModelContinuationDigest(c)
	c.Approval = &ModelContinuationFact{Revision: 12, Digest: c.Digest, CreatedAt: "2030-01-01T00:01:00Z"}
	c.Application = &ModelContinuationFact{Revision: 13, Digest: c.Digest, CreatedAt: "2030-01-01T00:02:00Z"}
	m.ModelContinuations = []ModelContinuation{c}
	if err := ValidateMissionModelSnapshot(m); err != nil {
		t.Fatal("fixture", err)
	}
	return m
}

func TestModelContinuationEffectiveAndHistoricalSelectionAreSeparate(t *testing.T) {
	m := continuationDomainFixture(t)
	c := m.ModelContinuations[0]
	for _, state := range []string{"prepared", "approved", "applied"} {
		next := m
		record := c
		if state != "applied" {
			record.Application = nil
		}
		if state == "prepared" {
			record.Approval = nil
		}
		next.ModelContinuations = []ModelContinuation{record}
		snapshot, err := EffectiveModelSnapshot(next)
		if err != nil {
			t.Fatal(err)
		}
		want := "original"
		if state == "applied" {
			want = "replacement"
		}
		if snapshot.Roles[Main].Authorization.ID != want {
			t.Fatal("unapplied material changed authority")
		}
		if _, err = HistoricalModelSnapshot(next, c.Replacement.Digest); (err == nil) != (state == "applied") {
			t.Fatal("unapplied replacement became historical authority")
		}
	}
	historical, err := HistoricalModelSnapshot(m, m.ModelSnapshot.Digest)
	if err != nil || historical.Roles[Main].Authorization.ID != "original" {
		t.Fatal("original history lost", err)
	}
	if _, err = HistoricalModelSnapshot(m, strings.Repeat("f", 64)); err == nil {
		t.Fatal("unknown history accepted")
	}
	// Applied records remain structurally valid after material/credential expiry.
	if _, err = EffectiveModelSnapshot(m); err != nil {
		t.Fatal(err)
	}
	if ApprovalPostdatesModelContinuation(m, Approval{Revision: 12, CreatedAt: time.Now().Format(time.RFC3339Nano)}) {
		t.Fatal("old approval reused")
	}
}

func TestModelContinuationRejectsRehashedIdentityRouteScopeAndHistoryDrift(t *testing.T) {
	cases := map[string]func(*ModelContinuation){
		"identity": func(c *ModelContinuation) { c.Replacement.Roles[Main].Authorization.Subject = "other" },
		"issuer":   func(c *ModelContinuation) { c.Replacement.Roles[Main].Authorization.Issuer = "https://other.invalid" },
		"client":   func(c *ModelContinuation) { c.Replacement.Roles[Main].Authorization.ClientID = "other" },
		"scope": func(c *ModelContinuation) {
			c.Replacement.Roles[Main].Authorization.Scopes = append(c.Replacement.Roles[Main].Authorization.Scopes, "extra")
		},
		"provider": func(c *ModelContinuation) { c.Replacement.Roles[Main].Account.Provider = "custom" },
		"account":  func(c *ModelContinuation) { c.Replacement.Roles[Main].Account.ID = "other" },
		"route":    func(c *ModelContinuation) { c.Replacement.Roles[Main].Route.ID = "other" },
		"protocol": func(c *ModelContinuation) {
			s := c.Replacement.Roles[Main]
			s.Profile.Protocol = "chat_completions"
			c.Replacement.Roles[Main] = s
		},
		"model": func(c *ModelContinuation) {
			s := c.Replacement.Roles[Main]
			s.Profile.Model = "other"
			c.Replacement.Roles[Main] = s
		},
		"spend":           func(c *ModelContinuation) { c.Replacement.Roles[Main].Route.SpendClass = "explicit_api" },
		"extra usage":     func(c *ModelContinuation) { c.Replacement.Roles[Main].Route.ExtraUsage = "on" },
		"generation":      func(c *ModelContinuation) { c.AuthorizationGeneration++ },
		"catalog expiry":  func(c *ModelContinuation) { c.Replacement.Roles[Main].Catalog.ExpiresAt = "2030-01-01T02:00:00Z" },
		"ttl":             func(c *ModelContinuation) { c.ExpiresAt = "2030-01-01T00:11:00Z" },
		"predecessor":     func(c *ModelContinuation) { c.PredecessorDigest = strings.Repeat("f", 64) },
		"unapproved":      func(c *ModelContinuation) { c.Approval = nil },
		"expired apply":   func(c *ModelContinuation) { c.Application.CreatedAt = c.ExpiresAt },
		"backdated apply": func(c *ModelContinuation) { c.Application.CreatedAt = c.CreatedAt },
		"future revision": func(c *ModelContinuation) {
			c.PreparedRevision = 20
			c.Approval.Revision = 22
			c.Application.Revision = 23
		},
		"omitted role":   func(c *ModelContinuation) { c.AffectedRoles = c.AffectedRoles[1:] },
		"duplicate role": func(c *ModelContinuation) { c.AffectedRoles = append(c.AffectedRoles, Main) },
		"unsupported":    func(c *ModelContinuation) { c.SchemaVersion = 2 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := continuationDomainFixture(t)
			c := &m.ModelContinuations[0]
			mutate(c)
			c.Replacement.Digest, _ = ModelSnapshotDigest(c.Replacement)
			c.Digest = ModelContinuationDigest(*c)
			if c.Approval != nil {
				c.Approval.Digest = c.Digest
			}
			if c.Application != nil {
				c.Application.Digest = c.Digest
			}
			if _, err := EffectiveModelSnapshot(m); err == nil {
				t.Fatal("rehashed invalid continuation accepted")
			}
		})
	}
}
