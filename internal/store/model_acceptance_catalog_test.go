package store

import (
	"foundry/internal/domain"
	"testing"
	"time"
)

type catalogLedger interface {
	ProposeProductionCatalog(string, domain.ModelAcceptanceProposal, domain.ProductionAdmission, time.Time) (domain.ModelAcceptanceOperation, error)
	ReserveProductionCatalogDispatch(domain.ModelAcceptanceOperation, domain.ProductionAdmission, time.Time) error
	ValidateProductionCatalogDispatch(domain.ModelAcceptanceOperation, domain.ProductionAdmission, time.Time) error
	FinishProductionCatalog(domain.ModelAcceptanceOperation, string, string, string, []domain.ModelAcceptanceCatalogEntry, time.Time) (domain.ModelAcceptanceOperation, error)
}

func catalogFixture(t *testing.T, ttl ...time.Duration) (*Store, catalogLedger, domain.ModelAccount, domain.ModelRoute, domain.ModelAcceptanceOperation) {
	t.Helper()
	s, p, a, r := productionStore(t)
	c, ok := any(s).(catalogLedger)
	if !ok {
		t.Fatal("independent production catalog ledger is missing")
	}
	o := runningProduction(t, s, p, a, r)
	if e := p.ReserveProductionConnectionDispatch(o, productionAdmission(), time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.BeginModelAuthorization(a.ID, "catalog-signin", "sign_in", "catalog-proof"); e != nil {
		t.Fatal(e)
	}
	binding := domain.ModelAuthorizationBinding{ID: "catalog-proof", AccountID: a.ID, Issuer: "https://auth.openai.com", Subject: "synthetic", ClientID: "oaiapp_synthetic", SpendClass: "subscription_only", Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}}
	duration := time.Hour
	if len(ttl) > 0 {
		duration = ttl[0]
	}
	if _, e := s.CommitModelAuthorization("catalog-signin", binding, time.Now().Add(duration).Format(time.RFC3339)); e != nil {
		t.Fatal(e)
	}
	o, e := s.FinishProductionConnection(o, "succeeded", "completed", "", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	a, e = s.GetModelAccount(a.ID)
	if e != nil {
		t.Fatal(e)
	}
	return s, c, a, r, o
}

func TestProductionCatalogExpiryAndDispatchRevalidation(t *testing.T) {
	for _, stage := range []string{"proposal", "decision", "reservation", "after lease", "completion"} {
		t.Run(stage, func(t *testing.T) {
			s, c, a, r, connection := catalogFixture(t, 5*time.Minute)
			admission := domain.ProductionAdmission{Generation: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Profile: "openai_siwc_catalog_v1"}
			q := domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: connection.ID}
			now := time.Now()
			expired := now.Add(6 * time.Minute)
			if stage == "proposal" {
				if _, e := c.ProposeProductionCatalog(a.ID, q, admission, expired); e == nil {
					t.Fatal("expired credential proposed")
				}
				return
			}
			o, e := c.ProposeProductionCatalog(a.ID, q, admission, now)
			if e != nil {
				t.Fatal(e)
			}
			materialExpiry, _ := time.Parse(time.RFC3339Nano, o.Material.ExpiresAt)
			authExpiry, _ := time.Parse(time.RFC3339Nano, o.Material.Authorization.ExpiresAt)
			if !materialExpiry.Equal(authExpiry) {
				t.Fatal("approval outlives credential")
			}
			if stage == "decision" {
				if _, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", expired); e == nil {
					t.Fatal("expired approval accepted")
				}
				return
			}
			o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
			if e != nil {
				t.Fatal(e)
			}
			o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now)
			if e != nil {
				t.Fatal(e)
			}
			if stage == "reservation" {
				if e = c.ReserveProductionCatalogDispatch(o, admission, expired); e == nil {
					t.Fatal("expired reservation")
				}
				return
			}
			if e = c.ReserveProductionCatalogDispatch(o, admission, now); e != nil {
				t.Fatal(e)
			}
			if stage == "after lease" {
				if e = c.ValidateProductionCatalogDispatch(o, admission, expired); e == nil {
					t.Fatal("expired dispatch validation")
				}
				return
			}
			if _, e = c.FinishProductionCatalog(o, "succeeded", "completed", "", nil, now.Add(31*time.Second)); e == nil {
				t.Fatal("success adopted after request deadline")
			}
		})
	}
}

func TestProductionCatalogIndependentLedger(t *testing.T) {
	s, c, a, r, connection := catalogFixture(t)
	admission := domain.ProductionAdmission{Generation: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Profile: "openai_siwc_catalog_v1"}
	q := domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: connection.ID}
	o, e := c.ProposeProductionCatalog(a.ID, q, admission, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if o.State != "pending" || o.Source != "production_catalog" || o.LiveAccess != "catalog_only" || o.Material.ConnectionMode != "" || o.Material.Authorization == nil {
		t.Fatalf("bad material: %+v", o)
	}
	if _, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now()); e == nil {
		t.Fatal("pending consumed")
	}
	o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = c.ValidateProductionCatalogDispatch(o, admission, time.Now()); e == nil {
		t.Fatal("unreserved claim validated")
	}
	if e = c.ReserveProductionCatalogDispatch(o, admission, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e = c.ReserveProductionCatalogDispatch(o, admission, time.Now()); e == nil {
		t.Fatal("reservation replay")
	}
	if e = c.ValidateProductionCatalogDispatch(o, admission, time.Now()); e != nil {
		t.Fatal(e)
	}
	got, e := c.FinishProductionCatalog(o, "succeeded", "completed", "", []domain.ModelAcceptanceCatalogEntry{{ID: "alpha", DisplayName: "Alpha model"}, {ID: "beta", DisplayName: "Beta model"}}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if got.Catalog[0].DisplayName != "Alpha model" || got.Catalog[1].ID != "beta" || got.Attempts != 1 {
		t.Fatalf("bad result: %+v", got)
	}
}
