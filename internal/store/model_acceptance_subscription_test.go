package store

import (
	"foundry/internal/domain"
	"reflect"
	"testing"
	"time"
)

func subscriptionGateStore(t *testing.T) (*Store, domain.ModelAccount, domain.ModelRoute) {
	t.Helper()
	s, _, _ := gateStore(t)
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "subscription", Name: "subscription", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "http://127.0.0.1:12345/v1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateModelRoute(domain.ModelRoute{ID: "subscription-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	return s, a, r
}

func persistSubscriptionGateAuthorization(t *testing.T, s *Store, a domain.ModelAccount, id, state string, expires time.Time) domain.ModelAuthorization {
	t.Helper()
	if _, err := s.BeginModelAuthorization(a.ID, id+"-op", "sign_in", id); err != nil {
		t.Fatal(err)
	}
	b := domain.ModelAuthorizationBinding{ID: id, AccountID: a.ID, Issuer: "http://127.0.0.1:12345", Subject: "synthetic", ClientID: "synthetic", SpendClass: "subscription_only", Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}}
	auth, err := s.CommitModelAuthorization(id+"-op", b, expires.UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	if state == "revoked" {
		auth, err = s.RevokeModelAuthorization(id)
		if err != nil {
			t.Fatal(err)
		}
	}
	pending, err := s.PendingModelAuthorizationCleanup()
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range pending {
		if err = s.CompleteModelAuthorizationCleanup(op.ID); err != nil {
			t.Fatal(err)
		}
	}
	return auth
}

func TestSubscriptionStoreDerivesPermissionAndBindsResource(t *testing.T) {
	s, a, r := subscriptionGateStore(t)
	o, err := s.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}
	if !reflect.DeepEqual(o.Material.RequestedScopes, want) || o.Material.RequestedResource != "https://api.openai.com/v1" {
		t.Fatalf("derived permission=%v resource=%q", o.Material.RequestedScopes, o.Material.RequestedResource)
	}
	o, err = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o.Material.RequestedResource = "https://api.openai.com/v2"
	if _, err = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, domain.AcceptanceDigest(o.Material), time.Now()); err == nil {
		t.Fatal("changed resource reused approval")
	}
}

func TestSubscriptionFinishRequiresFreshProofAndRejectsModelEvidence(t *testing.T) {
	for _, prior := range []string{"none", "available", "expired", "revoked"} {
		t.Run(prior, func(t *testing.T) {
			s, a, r := subscriptionGateStore(t)
			if prior != "none" {
				expires := time.Now().Add(time.Hour)
				if prior == "expired" {
					expires = time.Now().Add(-time.Hour)
				}
				persistSubscriptionGateAuthorization(t, s, a, "prior", map[bool]string{true: "revoked", false: "available"}[prior == "revoked"], expires)
			}
			o, err := s.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			o, err = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			o, err = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.FinishModelAcceptance(a.ID, o.ID, o.Revision, "succeeded", "completed", "", nil, 0, 0, false, time.Now()); err == nil {
				t.Fatal("old or missing proof completed fresh connection")
			}
			fresh := persistSubscriptionGateAuthorization(t, s, a, "fresh", "available", time.Now().Add(time.Hour))
			if o.Material.Authorization != nil && fresh.Binding.ID == o.Material.Authorization.Binding.ID {
				t.Fatal("fixture did not create fresh binding")
			}
			if _, err = s.FinishModelAcceptance(a.ID, o.ID, o.Revision, "succeeded", "completed", "", []domain.ModelAcceptanceCatalogEntry{{ID: "model", DisplayName: "model"}}, 0, 0, false, time.Now()); err == nil {
				t.Fatal("subscription receipt accepted catalog output")
			}
			if _, err = s.FinishModelAcceptance(a.ID, o.ID, o.Revision, "succeeded", "completed", "", nil, 0, 0, false, time.Now()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSubscriptionRefreshGenerationCannotProveNewConnection(t *testing.T) {
	s, a, r := subscriptionGateStore(t)
	prior := persistSubscriptionGateAuthorization(t, s, a, "prior", "available", time.Now().Add(time.Hour))
	o, err := s.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginModelAuthorization(a.ID, "refresh-op", "refresh", prior.Binding.ID); err != nil {
		t.Fatal(err)
	}
	refreshed, err := s.CommitModelAuthorization("refresh-op", prior.Binding, time.Now().Add(2*time.Hour).UTC().Format(time.RFC3339))
	if err != nil || refreshed.Generation <= prior.Generation {
		t.Fatalf("fixture refresh=%+v %v", refreshed, err)
	}
	pending, err := s.PendingModelAuthorizationCleanup()
	if err != nil {
		t.Fatal(err)
	}
	for _, cleanup := range pending {
		if err = s.CompleteModelAuthorizationCleanup(cleanup.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.FinishModelAcceptance(a.ID, o.ID, o.Revision, "succeeded", "completed", "", nil, 0, 0, false, time.Now()); err == nil {
		t.Fatal("refreshed old binding proved a new connection")
	}
}
