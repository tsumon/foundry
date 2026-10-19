package store

import (
	"errors"
	"foundry/internal/domain"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func gateStore(t *testing.T) (*Store, domain.ModelAccount, domain.ModelRoute) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "fixture-account", Name: "fixture", Provider: "kimi", BaseURL: "http://127.0.0.1:12345/v1", AuthMethod: "api_key", BillingMode: "subscription", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateModelRoute(domain.ModelRoute{ID: "fixture-route", AccountID: a.ID, Adapter: "kimi_code_chat", Protocol: "chat_completions", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "interactive_package", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	return s, a, r
}
func gateProposal(t *testing.T, s *Store, a domain.ModelAccount, r domain.ModelRoute) domain.ModelAcceptanceOperation {
	t.Helper()
	o, err := s.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func TestAcceptanceSingleClaimAndRecovery(t *testing.T) {
	s, a, r := gateStore(t)
	o := gateProposal(t, s, a, r)
	if _, err := s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("unapproved claim: %v", err)
	}
	o, err := s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now()); e == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("claims=%d", wins.Load())
	}
	got, _ := s.GetModelAcceptance(a.ID, o.ID)
	if got.State != "running" || got.Attempts != 1 {
		t.Fatalf("%+v", got)
	}
	if err = s.RecoverModelAcceptance(time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetModelAcceptance(a.ID, o.ID)
	if got.State != "unknown" || got.Attempts != 1 {
		t.Fatalf("%+v", got)
	}
	if _, err = s.ClaimModelAcceptance(a.ID, o.ID, got.Revision, got.MaterialDigest, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("replayed: %v", err)
	}
}
func TestAcceptanceBoundMaterialAndExpiry(t *testing.T) {
	for _, kind := range []string{"digest", "revision", "account", "expiry", "wrong_account"} {
		t.Run(kind, func(t *testing.T) {
			s, a, r := gateStore(t)
			o := gateProposal(t, s, a, r)
			rev, digest, id, now := o.Revision, o.MaterialDigest, a.ID, time.Now()
			switch kind {
			case "digest":
				digest = "forged"
			case "revision":
				rev++
			case "account":
				a.Name = "changed"
				if _, e := s.SaveModelAccount(a, a.Revision); e != nil {
					t.Fatal(e)
				}
			case "expiry":
				now = now.Add(11 * time.Minute)
			case "wrong_account":
				id = "another"
			}
			if _, e := s.DecideModelAcceptance(id, o.ID, rev, digest, "approve", now); e == nil {
				t.Fatal("stale material approved")
			}
		})
	}
}
func TestAcceptanceSeparatePrerequisites(t *testing.T) {
	s, a, r := gateStore(t)
	for _, phase := range []string{"catalog", "inference"} {
		if _, e := s.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: phase, RouteID: r.ID, ExpectedAccountRevision: a.Revision, Model: "unreviewed"}, time.Now()); e == nil {
			t.Fatalf("%s without evidence", phase)
		}
	}
}

func completeGateConnection(t *testing.T, s *Store, a domain.ModelAccount, r domain.ModelRoute) domain.ModelAcceptanceOperation {
	t.Helper()
	o := gateProposal(t, s, a, r)
	o, e := s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: o.ID, SecretRef: "foundry-model-account-" + o.ID}, a.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = s.FinishModelCredential(o.ID, "available"); e != nil {
		t.Fatal(e)
	}
	o, e = s.FinishModelAcceptance(a.ID, o.ID, o.Revision, "succeeded", "completed", "", nil, 0, 0, false, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	return o
}
func TestAcceptanceConnectionLineageAndCatalogModels(t *testing.T) {
	s, a, r := gateStore(t)
	conn := completeGateConnection(t, s, a, r)
	a, _ = s.GetModelAccount(a.ID)
	if conn.ResultAccountRevision != 3 {
		t.Fatalf("connection result revision %d", conn.ResultAccountRevision)
	}
	q := domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: conn.ID}
	cat, e := s.ProposeModelAcceptance(a.ID, q, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	cat, e = s.DecideModelAcceptance(a.ID, cat.ID, cat.Revision, cat.MaterialDigest, "approve", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	cat, e = s.ClaimModelAcceptance(a.ID, cat.ID, cat.Revision, cat.MaterialDigest, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	cat, e = s.FinishModelAcceptance(a.ID, cat.ID, cat.Revision, "succeeded", "completed", "", []domain.ModelAcceptanceCatalogEntry{{ID: "catalog-model", DisplayName: "catalog-model"}}, 0, 0, false, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	q.Phase, q.PrerequisiteID, q.Model = "inference", cat.ID, "not-in-catalog"
	if _, e = s.ProposeModelAcceptance(a.ID, q, time.Now()); e == nil {
		t.Fatal("uncatalogued model accepted")
	}
	q.Model = "catalog-model"
	inference, e := s.ProposeModelAcceptance(a.ID, q, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	_ = completeGateConnection(t, s, a, r)
	if _, e = s.DecideModelAcceptance(a.ID, inference.ID, inference.Revision, inference.MaterialDigest, "approve", time.Now()); e == nil {
		t.Fatal("prior connection lineage survived reconnect")
	}
	a, _ = s.GetModelAccount(a.ID)
	q.ExpectedAccountRevision = a.Revision
	if _, e = s.ProposeModelAcceptance(a.ID, q, time.Now()); e == nil {
		t.Fatal("old catalog reused at new account revision")
	}
}
func TestAcceptanceUnresolvedAndDisabledFailClosed(t *testing.T) {
	for _, kind := range []string{"disabled", "pending", "cleanup", "extra_unknown"} {
		t.Run(kind, func(t *testing.T) {
			s, a, r := gateStore(t)
			switch kind {
			case "disabled":
				a.Enabled = false
				a, _ = s.SaveModelAccount(a, a.Revision)
			case "pending", "cleanup":
				_, e := s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "unresolved", SecretRef: "foundry-model-account-unresolved"}, a.Revision)
				if e != nil {
					t.Fatal(e)
				}
				if kind == "cleanup" {
					if _, e = s.FinishModelCredential("unresolved", "failed"); e != nil {
						t.Fatal(e)
					}
				}
				a, _ = s.GetModelAccount(a.ID)
			case "extra_unknown":
				r.ID = "unknown-route"
				r.ExtraUsage = "unknown"
				r, _ = s.CreateModelRoute(r)
			}
			if _, e := s.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, time.Now()); e == nil {
				t.Fatal("unsafe account proposed")
			}
		})
	}
}
func TestAcceptanceFinishFailureAndRestartNeverReplay(t *testing.T) {
	s, a, r := gateStore(t)
	o := gateProposal(t, s, a, r)
	o, e := s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`CREATE TRIGGER fail_gate_finish BEFORE UPDATE ON model_acceptance_operations WHEN NEW.state!='running' BEGIN SELECT RAISE(ABORT,'synthetic storage fault'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.FinishModelAcceptance(a.ID, o.ID, o.Revision, "unknown", "unknown", "transport_unknown", nil, 0, 0, false, time.Now()); e == nil {
		t.Fatal("completion storage failure hidden")
	}
	got, e := s.GetModelAcceptance(a.ID, o.ID)
	if e != nil || got.State != "running" || got.Attempts != 1 {
		t.Fatalf("consumption lost: %+v %v", got, e)
	}
	if e = s.RecoverModelAcceptance(time.Now()); e == nil {
		t.Fatal("recovery storage failure hidden")
	}
	if _, e = s.db.Exec(`DROP TRIGGER fail_gate_finish`); e != nil {
		t.Fatal(e)
	}
	if e = s.RecoverModelAcceptance(time.Now()); e != nil {
		t.Fatal(e)
	}
	got, _ = s.GetModelAcceptance(a.ID, o.ID)
	if got.State != "unknown" || got.Attempts != 1 {
		t.Fatalf("recovery %+v", got)
	}
}

func TestAcceptanceConnectionRejectsUnresolvedAuthorizationMetadata(t *testing.T) {
	for _, state := range []string{"refresh_unknown", "refreshing"} {
		t.Run(state, func(t *testing.T) {
			s, _, _ := gateStore(t)
			a, e := s.SaveModelAccount(domain.ModelAccount{ID: "oauth", Name: "oauth", Provider: "openai", BaseURL: "http://127.0.0.1:12345/v1", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true}, 0)
			if e != nil {
				t.Fatal(e)
			}
			r, e := s.CreateModelRoute(domain.ModelRoute{ID: "oauth-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.BeginModelAuthorization(a.ID, "auth-op", "sign_in", "auth"); e != nil {
				t.Fatal(e)
			}
			b := domain.ModelAuthorizationBinding{ID: "auth", AccountID: a.ID, Issuer: "http://127.0.0.1:12345", Subject: "synthetic", ClientID: "synthetic", SpendClass: "subscription_only", Scopes: []string{"resource.invoke", "chatgpt.tokens.use.direct"}}
			if _, e = s.CommitModelAuthorization("auth-op", b, "2099-01-01T00:00:00Z"); e != nil {
				t.Fatal(e)
			}
			if _, e = s.db.Exec(`UPDATE model_authorizations SET state=? WHERE id='auth'`, state); e != nil {
				t.Fatal(e)
			}
			if _, e = s.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, time.Now()); e == nil {
				t.Fatal("unresolved authorization allowed connection")
			}
		})
	}
}

func TestAcceptanceAccountCannotDispatchOverlappingSnapshots(t *testing.T) {
	s, a, r := gateStore(t)
	first := gateProposal(t, s, a, r)
	second := gateProposal(t, s, a, r)
	var e error
	first, e = s.DecideModelAcceptance(a.ID, first.ID, first.Revision, first.MaterialDigest, "approve", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	second, e = s.DecideModelAcceptance(a.ID, second.ID, second.Revision, second.MaterialDigest, "approve", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimModelAcceptance(a.ID, first.ID, first.Revision, first.MaterialDigest, time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimModelAcceptance(a.ID, second.ID, second.Revision, second.MaterialDigest, time.Now()); e == nil {
		t.Fatal("overlapping account effects allowed")
	}
	got, _ := s.GetModelAcceptance(a.ID, second.ID)
	if got.Attempts != 0 || got.State != "approved" {
		t.Fatal("blocked overlap consumed permission")
	}
}
