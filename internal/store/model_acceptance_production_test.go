package store

import (
	"encoding/json"
	"foundry/internal/domain"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type productionLedger interface {
	ProposeProductionConnection(string, domain.ModelAcceptanceProposal, domain.ProductionAdmission, time.Time) (domain.ModelAcceptanceOperation, error)
	ReserveProductionConnectionDispatch(domain.ModelAcceptanceOperation, domain.ProductionAdmission, time.Time) error
}

func productionStore(t *testing.T) (*Store, productionLedger, domain.ModelAccount, domain.ModelRoute) {
	t.Helper()
	s, _, _ := gateStore(t)
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "production", Name: "production", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "https://api.openai.com/v1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateModelRoute(domain.ModelRoute{ID: "production-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	p, ok := any(s).(productionLedger)
	if !ok {
		t.Fatal("production connection ledger boundary not implemented")
	}
	return s, p, a, r
}

func productionAdmission() domain.ProductionAdmission {
	return domain.ProductionAdmission{Generation: "0123456789abcdef0123456789abcdef", Profile: "openai_siwc_subscription_v1"}
}

func runningProduction(t *testing.T, s *Store, p productionLedger, a domain.ModelAccount, r domain.ModelRoute) domain.ModelAcceptanceOperation {
	t.Helper()
	o, err := p.ProposeProductionConnection(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, productionAdmission(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if o.Source != "production_connection" || o.Material.ProductionAdmission == nil || o.LiveAccess != "connection_only" {
		t.Fatalf("incorrect production provenance: %+v", o)
	}
	o, err = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestProductionLedgerOnlyExactConnectionAndOneDispatch(t *testing.T) {
	s, p, a, r := productionStore(t)
	q := domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}
	for _, mode := range []string{"", "browser_handoff"} {
		if _, err := s.ProposeModelAcceptanceWithConnectionMode(a.ID, q, mode, time.Now()); err == nil {
			t.Fatal("generic helper minted production permission")
		}
	}
	for _, phase := range []string{"catalog", "inference"} {
		q.Phase = phase
		if _, err := p.ProposeProductionConnection(a.ID, q, productionAdmission(), time.Now()); err == nil {
			t.Fatal("nonconnection production proposal")
		}
	}
	o := runningProduction(t, s, p, a, r)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p.ReserveProductionConnectionDispatch(o, productionAdmission(), time.Now()) == nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("dispatch winners=%d", winners.Load())
	}
	if err := s.RecoverModelAcceptance(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := p.ReserveProductionConnectionDispatch(o, productionAdmission(), time.Now()); err == nil {
		t.Fatal("recovered claim dispatched")
	}
	got, err := s.GetModelAcceptance(a.ID, o.ID)
	if err != nil || got.State != "unknown" || got.Attempts != 1 || got.Source != "production_connection" {
		t.Fatalf("bad recovery: %+v %v", got, err)
	}
}

func TestProductionDispatchRejectsStaleOrForgedClaims(t *testing.T) {
	for _, field := range []string{"generation", "revision", "phase", "scope", "claim", "route", "account revision", "expired login", "source", "metadata"} {
		t.Run(field, func(t *testing.T) {
			s, p, a, r := productionStore(t)
			o := runningProduction(t, s, p, a, r)
			admission := productionAdmission()
			now := time.Now()
			switch field {
			case "generation":
				admission.Generation = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			case "revision":
				o.Revision++
			case "phase":
				o.Phase = "catalog"
			case "scope":
				o.Material.RequestedScopes = []string{"openid"}
			case "claim":
				o.ClaimSequence++
			case "route":
				o.Material.Route.ID = "other"
			case "account revision":
				a.Name = "changed"
				if _, err := s.SaveModelAccount(a, a.Revision); err != nil {
					t.Fatal(err)
				}
			case "expired login":
				now = now.Add(601 * time.Second)
			case "source", "metadata":
				if field == "source" {
					o.Source = "local_simulation"
				} else {
					o.Material.ProductionAdmission.Profile = "forged"
					o.MaterialDigest = domain.AcceptanceDigest(o.Material)
				}
				raw, _ := json.Marshal(o)
				if _, err := s.db.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, raw, o.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.ReserveProductionConnectionDispatch(o, admission, now); err == nil {
				t.Fatal("invalid claim dispatched")
			}
		})
	}
}

func TestProductionMetadataValidatedOnAllLedgerTransitions(t *testing.T) {
	for _, action := range []string{"approve", "execute", "finish", "recover"} {
		t.Run(action, func(t *testing.T) {
			s, p, a, r := productionStore(t)
			o, err := p.ProposeProductionConnection(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, productionAdmission(), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if action != "approve" {
				o, err = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
				if err != nil {
					t.Fatal(err)
				}
			}
			if action == "finish" || action == "recover" {
				o, err = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
				if err != nil {
					t.Fatal(err)
				}
			}
			o.Material.ProductionAdmission.Generation = "forged"
			o.MaterialDigest = domain.AcceptanceDigest(o.Material)
			raw, _ := json.Marshal(o)
			if _, err = s.db.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, raw, o.ID); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "approve":
				_, err = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
			case "execute":
				_, err = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
			case "finish":
				_, err = s.FinishProductionConnection(o, "unknown", "unknown", "transport_unknown", time.Now())
			case "recover":
				err = s.RecoverModelAcceptance(time.Now())
			}
			if err == nil {
				t.Fatal("forged admission metadata accepted")
			}
		})
	}
}

func TestProductionReceiptRequiresDispatchAndFreshOfficialProof(t *testing.T) {
	for _, dispatched := range []bool{false, true} {
		t.Run(map[bool]string{false: "unreserved", true: "reserved"}[dispatched], func(t *testing.T) {
			s, p, a, r := productionStore(t)
			o := runningProduction(t, s, p, a, r)
			if dispatched {
				if err := p.ReserveProductionConnectionDispatch(o, productionAdmission(), time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.BeginModelAuthorization(a.ID, "production-signin", "sign_in", "production-proof"); err != nil {
				t.Fatal(err)
			}
			binding := domain.ModelAuthorizationBinding{ID: "production-proof", AccountID: a.ID, Issuer: "https://auth.openai.com", Subject: "synthetic", ClientID: "oaiapp_synthetic", SpendClass: "subscription_only", Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}}
			if _, err := s.CommitModelAuthorization("production-signin", binding, time.Now().Add(time.Hour).Format(time.RFC3339)); err != nil {
				t.Fatal(err)
			}
			_, err := s.FinishProductionConnection(o, "succeeded", "completed", "", time.Now())
			if (err == nil) != dispatched {
				t.Fatalf("unreserved dispatch accepted or reserved rejected: %v", err)
			}
		})
	}
}

func TestProductionApprovalCannotExtendFrozenExpiry(t *testing.T) {
	s, p, a, r := productionStore(t)
	o, err := p.ProposeProductionConnection(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, productionAdmission(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o.Material.ExpiresAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	o.MaterialDigest = domain.AcceptanceDigest(o.Material)
	raw, _ := json.Marshal(o)
	if _, err = s.db.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, raw, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now()); err == nil {
		t.Fatal("extended production approval lifetime accepted")
	}
}
