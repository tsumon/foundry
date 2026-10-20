package main

import (
	"context"
	"errors"
	"foundry/internal/acceptance"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Catches parsing that treats the opt-in as independent flags or creates local
// state before rejecting an incomplete operator request.
func TestOperatorStartupRequiresPairedOptions(t *testing.T) {
	for _, option := range []string{"--openai-subscription-route=production-route", "--openai-keychain-journal=/unused/probe.json"} {
		t.Run(option, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "not-created")
			t.Setenv("FOUNDRY_DATA_DIR", data)
			err := runWithSecrets([]string{option}, forbiddenProductionSecrets{t})
			if err == nil || !strings.Contains(err.Error(), "must be supplied together") {
				t.Fatalf("paired operator validation: %v", err)
			}
			if _, err := os.Stat(data); !os.IsNotExist(err) {
				t.Fatalf("invalid startup created state: %v", err)
			}
		})
	}
}

func TestOperatorMalformedOptionsDoNotCreateStartupState(t *testing.T) {
	for _, input := range []struct{ route, journal string }{
		{"bad/route", "/unused/probe.json"},
		{"production-route", "relative.json"},
		{"production-route", "/tmp/../probe.json"},
		{"production-route", "/tmp/invalid\x00.json"},
	} {
		t.Run(input.route+input.journal, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "not-created")
			t.Setenv("FOUNDRY_DATA_DIR", data)
			err := runWithSecrets([]string{"--openai-subscription-route=" + input.route, "--openai-keychain-journal=" + input.journal}, forbiddenProductionSecrets{t})
			if err == nil {
				t.Fatal("malformed operator options accepted")
			}
			if _, err := os.Stat(data); !os.IsNotExist(err) {
				t.Fatalf("malformed startup created state: %v", err)
			}
		})
	}
}

func operatorFixture(t *testing.T, kind string) (*store.Store, productionModels, domain.ModelAccount, domain.ModelRoute) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "operator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a := domain.ModelAccount{ID: "production", Name: "production", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "https://api.openai.com/v1"}
	r := domain.ModelRoute{ID: "production-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"}
	switch kind {
	case "simulation":
		a.BaseURL, r.BaseURL, r.Environment = "http://127.0.0.1:12345", "http://127.0.0.1:12345", "simulation"
	case "wrong account":
		a.AuthMethod, a.BillingMode, r.Adapter, r.SpendClass = "api_key", "metered_api", "openai_api_responses", "explicit_api"
	case "disabled account":
		a.Enabled = false
	case "extra usage":
		r.ExtraUsage = "unknown"
	}
	a, err = db.SaveModelAccount(a, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err = db.CreateModelRoute(r)
	if err != nil {
		t.Fatal(err)
	}
	secrets := forbiddenProductionSecrets{t}
	runtime := newProductionModels(db, model.NewClient(secrets), secrets)
	t.Cleanup(func() { _ = runtime.siwc.Close() })
	return db, runtime, a, r
}

// A selected route must be validated against the stored account and official
// policy before invoking the potentially native diagnostic boundary.
func TestOperatorActivationValidatesBeforeProbe(t *testing.T) {
	for _, kind := range []string{"none", "missing route option", "missing journal option", "bad route", "missing route", "simulation", "wrong account", "disabled account", "extra usage", "readonly", "probe failure"} {
		t.Run(kind, func(t *testing.T) {
			db, runtime, _, r := operatorFixture(t, kind)
			defer func() { _ = runtime.acceptance.Close() }()
			cfg := config{openaiSubscriptionRoute: r.ID, openaiKeychainJournal: filepath.Join(t.TempDir(), "new.json")}
			switch kind {
			case "none":
				cfg.openaiSubscriptionRoute, cfg.openaiKeychainJournal = "", ""
			case "missing route option":
				cfg.openaiSubscriptionRoute = ""
			case "missing journal option":
				cfg.openaiKeychainJournal = ""
			case "bad route":
				cfg.openaiSubscriptionRoute = "bad/route"
			case "missing route":
				cfg.openaiSubscriptionRoute = "missing"
			}
			calls := 0
			deps := openaiConnectionDeps{now: time.Now, probe: func(path string) error {
				calls++
				if path != cfg.openaiKeychainJournal {
					t.Fatal("probe path changed")
				}
				return errors.New("synthetic diagnostic failed or unconfirmed")
			}}
			err := activateOpenAIConnection(cfg, db, &runtime, kind == "readonly", deps)
			if (err == nil) != (kind == "none") {
				t.Fatalf("activation: %v", err)
			}
			wantCalls := 0
			if kind == "probe failure" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("native diagnostic boundary called %d times, want %d", calls, wantCalls)
			}
			if err := runtime.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.acceptance.Propose(r.AccountID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: 1}); err != acceptance.ErrDisabled {
				t.Fatalf("failed/default activation enabled proposal: %v", err)
			}
		})
	}
}

func TestOperatorActivationFreshLeaseRequiresRecoveryApprovalAndExecution(t *testing.T) {
	db, runtime, a, r := operatorFixture(t, "valid")
	defer func() { _ = runtime.acceptance.Close() }()
	now := time.Now()
	calls := 0
	cfg := config{openaiSubscriptionRoute: r.ID, openaiKeychainJournal: filepath.Join(t.TempDir(), "new.json")}
	if err := activateOpenAIConnection(cfg, db, &runtime, false, openaiConnectionDeps{now: func() time.Time { return now }, probe: func(string) error { calls++; return nil }}); err != nil {
		t.Fatal(err)
	}
	q := domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}
	if _, err := runtime.acceptance.Propose(a.ID, q); err != acceptance.ErrDisabled {
		t.Fatalf("unrecovered proposal: %v", err)
	}
	if err := runtime.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.acceptance.SimulationEligible(a.ID) {
		t.Fatal("production became simulation")
	}
	o, err := runtime.acceptance.Propose(a.ID, q)
	if err != nil || o.Source != "production_connection" || o.Material.ProductionAdmission == nil {
		t.Fatalf("proposal: %+v %v", o, err)
	}
	if _, err := runtime.acceptance.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); err == nil {
		t.Fatal("execution without approval")
	}
	o, err = runtime.acceptance.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
	if err != nil || o.Attempts != 0 {
		t.Fatalf("approval dispatched: %+v %v", o, err)
	}
	status := runtime.acceptance.ProductionConnectionStatus(a.ID)
	if !status.Enabled || status.Admission == nil || len(runtime.acceptance.Routes(a.ID)) != 1 {
		t.Fatalf("fresh readiness: %+v", status)
	}
	now = now.Add(10 * time.Minute)
	if runtime.acceptance.ProductionConnectionStatus(a.ID).Enabled {
		t.Fatal("lease valid at expiration boundary")
	}
	if _, err := runtime.acceptance.Propose(a.ID, q); err != acceptance.ErrDisabled {
		t.Fatalf("stale proposal: %v", err)
	}
	if _, err := runtime.acceptance.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); err != acceptance.ErrDisabled {
		t.Fatalf("stale execution: %v", err)
	}
	if calls != 1 {
		t.Fatalf("readiness reprobed %d times", calls)
	}
	if _, err := runtime.siwc.Begin(context.Background(), a.ID, a.Revision); err == nil {
		t.Fatal("legacy login enabled")
	}
	if _, err := runtime.siwc.Refresh(context.Background(), "missing", a.Revision); err == nil {
		t.Fatal("refresh enabled")
	}
}
