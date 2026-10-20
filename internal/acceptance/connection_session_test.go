package acceptance

import (
	"context"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestBrowserHandoffConstructorPolicyCannotBeForgedOrDowngraded(t *testing.T) {
	for _, policy := range []string{"valid", "missing subscription", "duplicate", "missing SIWC", "production SIWC", "short lifetime", "missing route", "legacy material", "different store"} {
		t.Run(policy, func(t *testing.T) {
			s, err := store.Open(filepath.Join(t.TempDir(), "gate.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			a, err := s.SaveModelAccount(domain.ModelAccount{ID: "browser", Name: "browser", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "http://127.0.0.1:1/v1"}, 0)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.CreateModelRoute(domain.ModelRoute{ID: "browser-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
			if err != nil {
				t.Fatal(err)
			}
			secrets := gateSecrets{}
			lifecycle := accounts.New(s, secrets)
			if policy == "different store" {
				other, err := store.Open(filepath.Join(t.TempDir(), "other.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				lifecycle = accounts.New(other, secrets)
			}
			const origin = "http://127.0.0.1:1"
			flowTimeout := 10 * time.Minute
			if policy == "short lifetime" {
				flowTimeout = time.Minute
			}
			siwc, err := accounts.NewSIWCSimulation(lifecycle, accounts.SIWCOptions{AuthorizationOrigin: origin, TokenOrigin: origin, OIDCOrigin: origin, JWKSOrigin: origin, RevocationOrigin: origin, Issuer: origin, FlowTimeout: flowTimeout})
			if err != nil {
				t.Fatal(err)
			}
			defer siwc.Close()
			options := Options{Routes: []domain.ModelRoute{r}, SubscriptionConnectionRouteIDs: []string{r.ID}, BrowserHandoffRouteIDs: []string{r.ID}, BrowserSIWC: siwc, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(context.Context, domain.ModelAcceptanceMaterial) error { t.Error("connector called"); return nil }}
			clock := time.Now()
			options.Now = func() time.Time { return clock }
			switch policy {
			case "missing subscription":
				options.SubscriptionConnectionRouteIDs = nil
			case "duplicate":
				options.BrowserHandoffRouteIDs = append(options.BrowserHandoffRouteIDs, r.ID)
			case "missing SIWC":
				options.BrowserSIWC = nil
			case "production SIWC":
				options.BrowserSIWC = accounts.NewSIWCService(lifecycle)
			case "missing route":
				options.Routes = nil
			}
			svc := NewSimulation(s, options)
			defer svc.Close()
			if err = svc.Recover(); err != nil {
				t.Fatal(err)
			}
			q := domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}
			if policy == "legacy material" {
				o, err := s.ProposeModelAcceptance(a.ID, q, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if _, err = svc.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve"); err != ErrDisabled {
					t.Fatal("legacy policy bypass admitted", err)
				}
			} else if policy != "valid" {
				if svc.SimulationEligible(a.ID) || !svc.OwnsAccount(a.ID) {
					t.Fatal("invalid policy eligibility/ownership")
				}
				if _, err = svc.Propose(a.ID, q); err != ErrDisabled {
					t.Fatal("invalid policy proposal", err)
				}
			} else {
				o, err := svc.Propose(a.ID, q)
				if err != nil {
					t.Fatal(err)
				}
				if o.Material.ConnectionMode != "browser_handoff" || o.Material.LoginTimeoutSeconds != 600 || o.Material.DeadlineSeconds != 30 {
					t.Fatal("policy not frozen")
				}
				for _, phase := range []string{"catalog", "inference"} {
					q.Phase = phase
					if _, err = svc.Propose(a.ID, q); err != ErrDisabled {
						t.Fatal("downstream admitted")
					}
				}
				o, err = svc.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
				if err != nil {
					t.Fatal(err)
				}
				clock = clock.Add(599 * time.Second)
				o, err = svc.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
				if err != nil || o.State != "running" {
					t.Fatal("near-expiry approval could not execute", err)
				}
				_, view, err := svc.Read(a.ID, o.ID)
				if err != nil || view == nil {
					t.Fatal("missing near-expiry session")
				}
				expires, err := time.Parse(time.RFC3339Nano, view.ExpiresAt)
				if err != nil || time.Until(expires) < 598*time.Second {
					t.Fatal("approval expiry shortened human lifetime")
				}
				if _, err = svc.Cancel(a.ID, o.ID, o.Revision, o.MaterialDigest); err != nil {
					t.Fatal(err)
				}
			}
			if len(secrets) != 0 || policy != "valid" && len(svc.sessions) != 0 {
				t.Fatal("policy validation produced effects")
			}
		})
	}
}

func TestBrowserHandoffURLProjectionRejectsUnsafeVariants(t *testing.T) {
	for _, raw := range []string{"https://127.0.0.1:1234/auth/start", "http://localhost:1234/auth/start", "http://127.0.0.1:1234/auth/start?x=1", "http://127.0.0.1:1234/auth/start?", "http://127.0.0.1:1234/auth/start#x", "http://user@127.0.0.1:1234/auth/start", "http://127.0.0.1:01234/auth/start", "http://127.0.0.1:1234/auth/%73tart", "http://127.0.0.1:0/auth/start", "http://127.0.0.1:65536/auth/start", "http://127.0.0.1:1234/auth/callback"} {
		if safeHandoffURL(raw) {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
	if !safeHandoffURL("http://127.0.0.1:1234/auth/start") {
		t.Fatal("safe link rejected")
	}
}
