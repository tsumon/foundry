package acceptance

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"path/filepath"
	"testing"
	"time"
)

// Dropping the constructor purpose policy must not restore broader proposals.
func TestIdentityPolicyRejectsOmissionAndMixedConstructorRoutes(t *testing.T) {
	for _, policy := range []string{"valid", "unowned", "wrong_adapter", "mixed", "changed_route", "duplicate", "duplicate_route"} {
		t.Run(policy, func(t *testing.T) {
			s, err := store.Open(filepath.Join(t.TempDir(), "identity.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			a, err := s.SaveModelAccount(domain.ModelAccount{ID: "identity", Name: "identity", Provider: "openai", BaseURL: "http://127.0.0.1:12345/v1", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true}, 0)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.CreateModelRoute(domain.ModelRoute{ID: "identity-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
			if err != nil {
				t.Fatal(err)
			}
			var opts Options
			ids := []string{r.ID}
			if policy == "unowned" {
				ids = []string{"missing-route"}
			}
			if policy == "duplicate" {
				ids = append(ids, r.ID)
			}
			raw, _ := json.Marshal(map[string]any{"IdentityOnlyRouteIDs": ids})
			if err = json.Unmarshal(raw, &opts); err != nil {
				t.Fatal(err)
			}
			opts.Routes = []domain.ModelRoute{r}
			opts.Router = model.NewRouter(nil, nil, model.RouterOptions{})
			calls := 0
			opts.Connect = func(context.Context, domain.ModelAcceptanceMaterial) error { calls++; return nil }
			if policy == "wrong_adapter" {
				opts.Routes[0].Adapter = "kimi_code_chat"
			}
			if policy == "changed_route" {
				opts.Routes[0].BaseURL = "http://127.0.0.1:12346/v1"
			}
			if policy == "duplicate_route" {
				other := r
				other.BaseURL = "http://127.0.0.1:12346/v1"
				opts.Routes = append(opts.Routes, other)
			}
			if policy == "mixed" {
				other := r
				other.ID = "broader-route"
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
					t.Fatal("invalid identity policy retained eligible account")
				}
				if !svc.OwnsAccount(a.ID) {
					t.Fatal("invalid policy lost legacy bypass protection")
				}
			}
			for _, purpose := range []string{"", "subscription_only"} {
				raw, _ = json.Marshal(map[string]any{"phase": "connection", "route_id": r.ID, "expected_account_revision": a.Revision, "connection_purpose": purpose})
				var q domain.ModelAcceptanceProposal
				_ = json.Unmarshal(raw, &q)
				if _, err = svc.Propose(a.ID, q); err == nil {
					t.Fatalf("identity policy accepted purpose %q", purpose)
				}
			}
			if calls != 0 {
				t.Fatal("proposal dispatched a connection")
			}
			if policy == "valid" {
				// Old pending/approved broader records must not gain eligibility when a
				// new constructor pins their account to identity-only mode.
				legacy, e := s.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = svc.Decide(a.ID, legacy.ID, legacy.Revision, legacy.MaterialDigest, "approve"); e != ErrDisabled {
					t.Fatal("legacy material approved by identity constructor")
				}
				legacy, e = s.DecideModelAcceptance(a.ID, legacy.ID, legacy.Revision, legacy.MaterialDigest, "approve", time.Now())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = svc.Execute(context.Background(), a.ID, legacy.ID, legacy.Revision, legacy.MaterialDigest); e != ErrDisabled || calls != 0 {
					t.Fatal("legacy approved material executed by identity constructor")
				}
				identity, e := svc.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "identity_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = svc.Decide(a.ID, identity.ID, identity.Revision+1, identity.MaterialDigest, "approve"); e == nil {
					t.Fatal("stale identity revision approved")
				}
				a.Name = "changed"
				if _, e = s.SaveModelAccount(a, a.Revision); e != nil {
					t.Fatal(e)
				}
				if _, e = svc.Decide(a.ID, identity.ID, identity.Revision, identity.MaterialDigest, "approve"); e == nil || calls != 0 {
					t.Fatal("changed identity account approved")
				}
			}
		})
	}
}
