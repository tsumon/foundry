package acceptance

import (
	"context"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"path/filepath"
	"testing"
)

func TestSubscriptionConstructorPolicyOnlyAdmitsMatchingConnection(t *testing.T) {
	for _, policy := range []string{"valid", "stale", "duplicate", "mixed", "both"} {
		t.Run(policy, func(t *testing.T) {
			s, err := store.Open(filepath.Join(t.TempDir(), "subscription.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			a, err := s.SaveModelAccount(domain.ModelAccount{ID: "subscription", Name: "subscription", Provider: "openai", BaseURL: "http://127.0.0.1:12345/v1", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true}, 0)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.CreateModelRoute(domain.ModelRoute{ID: "subscription-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
			if err != nil {
				t.Fatal(err)
			}
			opts := Options{Routes: []domain.ModelRoute{r}, SubscriptionConnectionRouteIDs: []string{r.ID}, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(context.Context, domain.ModelAcceptanceMaterial) error { return nil }}
			switch policy {
			case "stale":
				opts.SubscriptionConnectionRouteIDs = []string{"missing-route"}
			case "duplicate":
				opts.SubscriptionConnectionRouteIDs = []string{r.ID, r.ID}
			case "both":
				opts.IdentityOnlyRouteIDs = []string{r.ID}
			case "mixed":
				other := r
				other.ID = "other-route"
				other, err = s.CreateModelRoute(other)
				if err != nil {
					t.Fatal(err)
				}
				opts.Routes = append(opts.Routes, other)
			}
			svc := NewSimulation(s, opts)
			if err = svc.Recover(); err != nil {
				t.Fatal(err)
			}
			if policy != "valid" {
				if svc.SimulationEligible(a.ID) {
					t.Fatal("invalid policy remained eligible")
				}
				if !svc.OwnsAccount(a.ID) {
					t.Fatal("invalid policy lost bypass ownership")
				}
				return
			}
			if svc.ConnectionPurpose(r.ID) != "subscription_only" {
				t.Fatal("route purpose not projected")
			}
			for _, q := range []domain.ModelAcceptanceProposal{
				{Phase: "connection", RouteID: r.ID, ExpectedAccountRevision: a.Revision},
				{Phase: "connection", ConnectionPurpose: "identity_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision},
				{Phase: "catalog", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision},
				{Phase: "inference", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision},
			} {
				if _, err = svc.Propose(a.ID, q); err == nil {
					t.Fatalf("unsafe proposal accepted: %+v", q)
				}
			}
			if _, err = svc.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
