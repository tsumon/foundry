package main

import (
	"context"
	"foundry/internal/acceptance"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"path/filepath"
	"testing"
	"time"
)

type forbiddenProductionSecrets struct{ t *testing.T }

func (s forbiddenProductionSecrets) Get(string) (string, error) {
	s.t.Fatal("default runtime read credentials/probed native storage")
	return "", nil
}
func (s forbiddenProductionSecrets) Set(string, string) error {
	s.t.Fatal("default runtime wrote credentials/probed native storage")
	return nil
}
func (s forbiddenProductionSecrets) Delete(string) error {
	s.t.Fatal("default runtime deleted credentials/probed native storage")
	return nil
}

func TestProductionRuntimeRecoversAdmissionLedgerWithoutActivating(t *testing.T) {
	t.Setenv("FOUNDRY_PRODUCTION_ADMISSION", "true")
	t.Setenv("FOUNDRY_NATIVE_KEYCHAIN_READY", "true")
	t.Setenv("FOUNDRY_OPENAI_SUBSCRIPTION_ROUTE", "production-route")
	t.Setenv("FOUNDRY_OPENAI_KEYCHAIN_JOURNAL", filepath.Join(t.TempDir(), "not-created.json"))
	cfg, err := loadConfig()
	if err != nil || cfg.openaiSubscriptionRoute != "" || cfg.openaiKeychainJournal != "" {
		t.Fatalf("environment activated operator entry: %+v %v", cfg, err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := db.SaveModelAccount(domain.ModelAccount{ID: "production", Name: "production", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "https://api.openai.com/v1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := db.CreateModelRoute(domain.ModelRoute{ID: "production-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	o, err := db.ProposeProductionConnection(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, domain.ProductionAdmission{Generation: "0123456789abcdef0123456789abcdef", Profile: "openai_siwc_subscription_v1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err = db.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err = db.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	secrets := forbiddenProductionSecrets{t}
	runtime := newProductionModels(db, model.NewClient(secrets), secrets)
	defer runtime.siwc.Close()
	defer runtime.acceptance.Close()
	recoverer, ok := any(runtime).(interface{ Recover(context.Context) error })
	if !ok {
		t.Fatal("production runtime does not own acceptance recovery")
	}
	if err = recoverer.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := runtime.acceptance.ProductionConnectionStatus(a.ID)
	if status.Enabled || status.Admission != nil || len(runtime.acceptance.Routes(a.ID)) != 0 || runtime.acceptance.SimulationEligible(a.ID) {
		t.Fatalf("default startup projected authority: %+v", status)
	}
	if _, err := runtime.acceptance.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}); err != acceptance.ErrDisabled {
		t.Fatalf("default startup enabled proposal: %v", err)
	}
	after, err := db.GetModelAcceptance(a.ID, o.ID)
	if err != nil || after.State != "unknown" || after.Attempts != 1 || after.Source != "production_connection" {
		t.Fatalf("interrupted production claim: %+v %v", after, err)
	}
	if _, err = runtime.siwc.Begin(context.Background(), a.ID, a.Revision); err == nil {
		t.Fatal("default startup enabled login")
	}
	if _, err = runtime.siwc.Refresh(context.Background(), "forged", a.Revision); err == nil {
		t.Fatal("default startup enabled refresh")
	}
}

func TestProductionAdmissionRejectsLegacyReadinessAndActivationFlags(t *testing.T) {
	t.Setenv("FOUNDRY_DATA_DIR", t.TempDir())
	for _, flag := range []string{"--production-admission", "--native-keychain-ready", "--openai-live"} {
		if err := runWithSecrets([]string{flag}, forbiddenProductionSecrets{t}); err == nil {
			t.Fatalf("accepted activation flag %s", flag)
		}
	}
}
