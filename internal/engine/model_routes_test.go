package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/model"
)

func TestR5BCreationDerivesSchemaTwoAndKeepsProductionClosed(t *testing.T) {
	e, s, _, _, old := setup(t)
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "routed-account", Name: "Synthetic", Provider: "openai", AuthMethod: "api_key", BillingMode: "api_credits", BaseURL: "http://127.0.0.1:1234/v1", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "routed-key", SecretRef: "foundry-model-account-fake-routed-key"}, a.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishModelCredential("routed-key", "available"); err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateModelRoute(domain.ModelRoute{ID: "route", AccountID: a.ID, Adapter: "openai_api_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "explicit_api", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProfile("test")
	if err != nil {
		t.Fatal(err)
	}
	p.AccountID, p.RouteID, p.BaseURL, p.Protocol = a.ID, r.ID, r.BaseURL, r.Protocol
	if err = s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	e.gateway = model.NewClient(nil)
	for _, base := range []string{"", domain.WorkspaceModeGenerationsV1} {
		m, err := e.Create(context.Background(), accountCreateRequest(old, base))
		if err != nil {
			t.Fatal(err)
		}
		if !domain.IsInferenceSnapshot(m) {
			t.Fatal("server did not derive inference schema")
		}
		if _, err = e.Plan(context.Background(), m.ID, m.Revision); !errors.Is(err, model.ErrAccountInferenceDisabled) {
			t.Fatalf("production gate=%v", err)
		}
		got, _ := s.GetMission(m.ID)
		if got.Revision != m.Revision || got.Budget.UsedCalls != 0 {
			t.Fatal("denial mutated mission")
		}
	}
	for _, mode := range []string{"account_snapshot_v2", "generations_account_snapshot_v2", "generations_controlled_read_account_snapshot_v2", "generations_controlled_mcp_read_account_snapshot_v2"} {
		if _, err = e.Create(context.Background(), accountCreateRequest(old, mode)); err == nil {
			t.Fatal("client supplied authority wrapper", mode)
		}
	}
}

func TestR5BAuthorityUsesIndependentVersionDomain(t *testing.T) {
	e, s, _, _, old := setup(t)
	bindSyntheticAccount(t, s)
	m, err := e.Create(context.Background(), accountCreateRequest(old, ""))
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(e.dir(m.ID), "base")
	for _, version := range []int{1, 2} {
		if version == 2 {
			m.WorkspaceMode, m.ModelSnapshot.SchemaVersion = "account_snapshot_v2", 2
			for role, selection := range m.ModelSnapshot.Roles {
				selection.Account.Provider, selection.Account.BillingMode, selection.Account.BaseURL = "openai", "api_credits", "https://api.openai.com/v1"
				selection.Profile.RouteID, selection.Profile.Protocol, selection.Profile.BaseURL = "r", "responses", selection.Account.BaseURL
				selection.Route = &domain.ModelRoute{ID: "r", AccountID: selection.Account.ID, Adapter: "openai_api_responses", Protocol: "responses", BaseURL: selection.Account.BaseURL, Environment: "live", SpendClass: "explicit_api", ExtraUsage: "off"}
				m.Profiles[role], m.ModelSnapshot.Roles[role] = selection.Profile, selection
			}
			m.ModelSnapshot.Digest, _ = domain.ModelSnapshotDigest(*m.ModelSnapshot)
		}
		inner := m
		inner.WorkspaceMode = ""
		inner.ModelSnapshot = nil
		original, err := legacyAuthority(inner, base, false)
		if err != nil {
			t.Fatal(err)
		}
		prefix := "FOUNDRY account snapshot authority v1\x00"
		if version == 2 {
			prefix = "FOUNDRY account snapshot authority v2\x00"
		}
		want := sha256.Sum256([]byte(prefix + m.WorkspaceMode + "\x00" + m.ModelSnapshot.Digest + "\x00" + original))
		got, err := authority(m, base, false)
		if err != nil || got != hex.EncodeToString(want[:]) {
			t.Fatalf("schema%d authority is not domain-separated: %q %v", version, got, err)
		}
	}
}
