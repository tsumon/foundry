package main

import (
	"context"
	"foundry/internal/domain"
	"testing"
)

func TestInferenceStartupMetadataOnlyAndExclusive(t *testing.T) {
	db, runtime, a, r := operatorFixture(t, "valid")
	cfg := config{openaiInferenceRoute: r.ID}
	if e := validateOpenAIStartupOptions(cfg); e != nil {
		t.Fatal(e)
	}
	for _, connection := range []config{{openaiInferenceRoute: r.ID, openaiSubscriptionRoute: r.ID}, {openaiInferenceRoute: r.ID, openaiKeychainJournal: "/unused/probe"}, {openaiInferenceRoute: "bad/route"}, {openaiInferenceRoute: r.ID, openaiCatalogRoute: r.ID}} {
		if validateOpenAIStartupOptions(connection) == nil {
			t.Fatal("mixed or malformed options accepted")
		}
	}
	// Pending credential cleanup would invoke native Delete under normal account
	// recovery. Inference startup must leave it as an explicit metadata blocker.
	if _, e := db.BeginModelAuthorization(a.ID, "pending-signin", "sign_in", "pending-proof"); e != nil {
		t.Fatal(e)
	}
	if e := activateOpenAIInference(cfg, db, &runtime, false); e != nil {
		t.Fatal(e)
	}
	if e := runtime.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	status := runtime.acceptance.ProductionInferenceStatus(a.ID)
	if status.Admission == nil || status.Admission.Profile != "openai_siwc_inference_v1" || status.Enabled || status.Blocker != "authorization_unavailable" || runtime.acceptance.ProductionConnectionStatus(a.ID).Enabled {
		t.Fatalf("bad inference readiness: %+v", status)
	}
	if _, e := runtime.acceptance.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", RouteID: r.ID, ExpectedAccountRevision: a.Revision}); e == nil {
		t.Fatal("inference enabled connection")
	}
}
