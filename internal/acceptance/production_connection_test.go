package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestProductionReceiptCannotRebindChangedAdmissionGeneration(t *testing.T) {
	for _, downgrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "generation", true: "downgrade"}[downgrade], func(t *testing.T) {
			svc, db, opts, a, path := productionController(t, func() bool { return true })
			if err := svc.Recover(); err != nil {
				t.Fatal(err)
			}
			o, err := svc.Propose(a.ID, productionProposal(a, opts.Route))
			if err != nil {
				t.Fatal(err)
			}
			o, err = svc.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
			if err != nil {
				t.Fatal(err)
			}
			o, err = db.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			forged := o
			admission := *o.Material.ProductionAdmission
			admission.Generation = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			forged.Material.ProductionAdmission = &admission
			if downgrade {
				forged.Material.ProductionAdmission = nil
				forged.Material.ConnectionPurpose, forged.Material.ConnectionMode, forged.Material.RequestedResource = "", "", ""
				forged.Material.RequestedScopes = nil
				forged.Material.LoginTimeoutSeconds = 0
				forged.Source, forged.LiveAccess = "local_simulation", "blocked"
			}
			forged.MaterialDigest = domain.AcceptanceDigest(forged.Material)
			raw, _ := json.Marshal(forged)
			sqlDB, err := sql.Open("sqlite3", path)
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			if _, err = sqlDB.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, raw, o.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = svc.finish(o, errors.New("synthetic uncertain result"), nil, model.Result{}); err != ErrPersistence {
				t.Fatalf("receipt rebound generation: %v", err)
			}
		})
	}
}

func productionController(t *testing.T, ready func() bool) (*Service, *store.Store, ProductionConnectionOptions, domain.ModelAccount, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a, err := db.SaveModelAccount(domain.ModelAccount{ID: "production", Name: "production", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "https://api.openai.com/v1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := db.CreateModelRoute(domain.ModelRoute{ID: "production-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	siwc := accounts.NewSIWCService(accounts.New(db, gateSecrets{}))
	t.Cleanup(func() { _ = siwc.Close() })
	opts := ProductionConnectionOptions{Route: r, SIWC: siwc, CredentialReady: ready}
	svc := NewProductionConnection(db, opts)
	t.Cleanup(func() { _ = svc.Close() })
	return svc, db, opts, a, path
}

func productionProposal(a domain.ModelAccount, r domain.ModelRoute) domain.ModelAcceptanceProposal {
	return domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}
}

func TestProductionControllerPolicyOwnershipAndGeneration(t *testing.T) {
	for _, policy := range []string{"valid", "default", "missing readiness", "failed readiness", "missing SIWC", "route mismatch", "unknown route", "zero policy"} {
		t.Run(policy, func(t *testing.T) {
			var probes atomic.Int32
			svc, db, opts, a, _ := productionController(t, func() bool { probes.Add(1); return true })
			if probes.Load() != 0 {
				t.Fatal("constructor called readiness")
			}
			switch policy {
			case "default":
				svc = New(db)
			case "missing readiness":
				opts.CredentialReady = nil
			case "failed readiness":
				opts.CredentialReady = func() bool { return false }
			case "missing SIWC":
				opts.SIWC = nil
			case "route mismatch":
				opts.Route.AccountID = "forged"
			case "unknown route":
				opts.Route.ID = "missing"
			case "zero policy":
				opts = ProductionConnectionOptions{}
			}
			if policy != "default" {
				svc = NewProductionConnection(db, opts)
			}
			defer svc.Close()
			if _, err := svc.Propose(a.ID, productionProposal(a, opts.Route)); err != ErrDisabled {
				t.Fatal("enabled before recovery")
			}
			if err := svc.Recover(); err != nil {
				t.Fatal(err)
			}
			if svc.SimulationEligible(a.ID) {
				t.Fatal("production reported as simulation")
			}
			validPolicy := policy == "valid" || policy == "missing readiness" || policy == "failed readiness"
			if (len(svc.Routes(a.ID)) == 1) != validPolicy || (svc.ProductionConnectionStatus(a.ID).Admission != nil) != validPolicy || svc.ProductionConnectionStatus(a.ID).Enabled != (policy == "valid") {
				t.Fatalf("invalid production capability projection: %+v", svc.ProductionConnectionStatus(a.ID))
			}
			if policy != "default" && policy != "zero policy" && !svc.OwnsAccount(a.ID) {
				t.Fatal("invalid policy lost account ownership")
			}
			o, err := svc.Propose(a.ID, productionProposal(a, opts.Route))
			if policy != "valid" {
				if err != ErrDisabled {
					t.Fatalf("invalid policy proposal: %v", err)
				}
				return
			}
			if err != nil || o.Material.ProductionAdmission == nil || o.Source != "production_connection" {
				t.Fatalf("proposal: %+v %v", o, err)
			}
			for _, phase := range []string{"catalog", "inference"} {
				q := productionProposal(a, opts.Route)
				q.Phase = phase
				if _, err := svc.Propose(a.ID, q); err != ErrDisabled {
					t.Fatal("downstream phase admitted")
				}
			}
			other := NewProductionConnection(db, opts)
			defer other.Close()
			if err := other.Recover(); err != nil {
				t.Fatal(err)
			}
			if _, err := other.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve"); err != ErrDisabled {
				t.Fatal("old generation rebound")
			}
		})
	}
}

func TestProductionControllerExecutesOnceAndCancelsDespiteReadinessLoss(t *testing.T) {
	var ready atomic.Bool
	ready.Store(true)
	svc, _, opts, a, _ := productionController(t, ready.Load)
	if err := svc.Recover(); err != nil {
		t.Fatal(err)
	}
	o, err := svc.Propose(a.ID, productionProposal(a, opts.Route))
	if err != nil {
		t.Fatal(err)
	}
	o, err = svc.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
	if err != nil {
		t.Fatal(err)
	}
	ready.Store(false)
	if _, err = svc.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); err != ErrDisabled {
		t.Fatal("stale readiness executed")
	}
	unclaimed, _ := svc.Get(a.ID, o.ID)
	if unclaimed.State != "approved" || unclaimed.Attempts != 0 {
		t.Fatal("preclaim denial consumed approval")
	}
	ready.Store(true)
	approved := o
	o, err = svc.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
	if err != nil || o.State != "running" {
		t.Fatalf("execution: %+v %v", o, err)
	}
	if _, err = svc.Execute(context.Background(), a.ID, approved.ID, approved.Revision, approved.MaterialDigest); err == nil {
		t.Fatal("duplicate execution accepted")
	}
	_, session, err := svc.Read(a.ID, o.ID)
	if err != nil || session == nil || session.HandoffURL == "" {
		t.Fatalf("missing owned handoff: %+v %v", session, err)
	}
	ready.Store(false)
	o, err = svc.Cancel(a.ID, o.ID, o.Revision, o.MaterialDigest)
	if err != nil || o.State != "unknown" || o.Attempts != 1 {
		t.Fatalf("cancellation: %+v %v", o, err)
	}
	_, session, err = svc.Read(a.ID, o.ID)
	if err != nil || session != nil {
		t.Fatal("terminal session retained")
	}
}

func TestProductionControllerReceiptFailureAndInterruptedRecovery(t *testing.T) {
	for _, stage := range []string{"before dispatch", "active shutdown", "receipt failure"} {
		t.Run(stage, func(t *testing.T) {
			svc, db, opts, a, path := productionController(t, func() bool { return true })
			if err := svc.Recover(); err != nil {
				t.Fatal(err)
			}
			o, err := svc.Propose(a.ID, productionProposal(a, opts.Route))
			if err != nil {
				t.Fatal(err)
			}
			o, err = svc.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
			if err != nil {
				t.Fatal(err)
			}
			if stage == "before dispatch" {
				o, err = db.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
			} else {
				o, err = svc.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
			}
			if err != nil {
				t.Fatal(err)
			}
			if stage == "receipt failure" {
				sqlDB, err := sql.Open("sqlite3", path)
				if err != nil {
					t.Fatal(err)
				}
				defer sqlDB.Close()
				if _, err = sqlDB.Exec(`CREATE TRIGGER production_receipt_fault BEFORE UPDATE ON model_acceptance_operations WHEN OLD.state='running' AND NEW.state!='running' BEGIN SELECT RAISE(ABORT,'synthetic fault'); END`); err != nil {
					t.Fatal(err)
				}
				if _, err = svc.Cancel(a.ID, o.ID, o.Revision, o.MaterialDigest); !errors.Is(err, ErrPersistence) {
					t.Fatalf("receipt fault not closed: %v", err)
				}
				if _, err = svc.Propose(a.ID, productionProposal(a, opts.Route)); err != ErrDisabled {
					t.Fatal("receipt failure left admission open")
				}
				if _, err = sqlDB.Exec(`DROP TRIGGER production_receipt_fault`); err != nil {
					t.Fatal(err)
				}
			}
			if err = svc.Close(); err != nil {
				t.Fatal(err)
			}
			fresh := NewProductionConnection(db, opts)
			defer fresh.Close()
			if err = fresh.Recover(); err != nil {
				t.Fatal(err)
			}
			got, session, err := fresh.Read(a.ID, o.ID)
			if err != nil || got.State != "unknown" || got.Attempts != 1 || session != nil {
				t.Fatalf("recovery: %+v %+v %v", got, session, err)
			}
			if _, err = fresh.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); err == nil {
				t.Fatal("interrupted session replayed")
			}
		})
	}
}
