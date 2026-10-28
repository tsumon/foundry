package store

import (
	"encoding/json"
	"foundry/internal/domain"
	"testing"
	"time"
)

func completedDesktopCatalog(t *testing.T, models ...string) (*Store, domain.ModelAccount, domain.ModelRoute, domain.ModelAcceptanceOperation) {
	t.Helper()
	model := "gpt-5.1"
	if len(models) > 0 {
		model = models[0]
	}
	s, c, a, r, connection := catalogFixture(t)
	admission := domain.ProductionAdmission{Generation: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Profile: "openai_siwc_catalog_v1"}
	now := time.Now()
	o, e := c.ProposeProductionCatalog(a.ID, domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: connection.ID}, admission, now)
	if e != nil {
		t.Fatal(e)
	}
	o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
	if e != nil {
		t.Fatal(e)
	}
	o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.ReserveProductionCatalogDispatch(o, admission, now); e != nil {
		t.Fatal(e)
	}
	o, e = c.FinishProductionCatalog(o, "succeeded", "completed", "", []domain.ModelAcceptanceCatalogEntry{{ID: model, DisplayName: "Synthetic model"}}, now)
	if e != nil {
		t.Fatal(e)
	}
	return s, a, r, o
}

func TestDesktopCatalogSelectionBindsEvidenceAndNeverRetargetsSnapshot(t *testing.T) {
	s, a, r, o := completedDesktopCatalog(t)
	p := domain.Profile{ID: "catalog-choice", Name: "Synthetic selection", Kind: "direct", Protocol: r.Protocol, BaseURL: r.BaseURL, AccountID: a.ID, RouteID: r.ID, Model: "gpt-5.1", CatalogOperationID: o.ID}
	if e := s.PutProfile(p); e != nil {
		t.Fatal(e)
	}
	selection, e := s.ResolveModelProfile(p.ID)
	if e != nil || selection.Catalog == nil || selection.Catalog.OperationID != o.ID || selection.Catalog.AuthorizationGeneration != o.Material.Authorization.Generation || selection.Catalog.ExpiresAt != o.Material.Authorization.ExpiresAt {
		t.Fatalf("selection missing provenance: %+v %v", selection, e)
	}
	frozen, _ := json.Marshal(selection)
	if e = s.PutSettings(domain.Settings{DefaultProfileID: p.ID, RoleBindings: map[domain.Role]string{}}); e != nil {
		t.Fatal(e)
	}
	_, _, snapshot, e := s.ResolveMissionModels("")
	if e != nil {
		t.Fatal(e)
	}
	before, _ := json.Marshal(snapshot)
	p.Model = "not-in-catalog"
	if e = s.PutProfile(p); e == nil {
		t.Fatal("unknown model saved")
	}
	p.Model = "gpt-5.1"
	p.AccountID = "other"
	if e = s.PutProfile(p); e == nil {
		t.Fatal("cross account saved")
	}
	p.AccountID = a.ID
	p.RouteID = "other"
	if e = s.PutProfile(p); e == nil {
		t.Fatal("wrong route saved")
	}
	after, _ := json.Marshal(snapshot)
	if string(before) != string(after) {
		t.Fatal("old task snapshot rewritten")
	}
	auth, e := s.GetModelAuthorization("catalog-proof")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.BeginModelAuthorization(a.ID, "new-generation", "refresh", auth.Binding.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CommitModelAuthorization("new-generation", auth.Binding, auth.ExpiresAt); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ResolveModelProfile("catalog-choice"); e == nil {
		t.Fatal("new generation adopted stale catalog")
	}
	if _, _, e = s.ResolveModelCredentialMetadata(selection); e == nil {
		t.Fatal("old task silently leased new generation")
	}
	current, _ := json.Marshal(selection)
	if string(frozen) != string(current) {
		t.Fatal("old selection retargeted")
	}
}

func TestDesktopCatalogRejectsStaleReceipt(t *testing.T) {
	for _, change := range []string{"account", "expiry", "digest", "dispatch", "new-connection", "uncertain-cleanup"} {
		t.Run(change, func(t *testing.T) {
			s, a, r, o := completedDesktopCatalog(t)
			p := domain.Profile{ID: "catalog-choice", Protocol: r.Protocol, BaseURL: r.BaseURL, AccountID: a.ID, RouteID: r.ID, Model: "gpt-5.1", CatalogOperationID: o.ID}
			switch change {
			case "account":
				a.Name = "changed"
				if _, e := s.SaveModelAccount(a, a.Revision); e != nil {
					t.Fatal(e)
				}
			case "expiry":
				tx, e := s.db.Begin()
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				if _, e = catalogSelectionTx(tx, p, time.Now().Add(2*time.Hour)); e == nil {
					t.Fatal("expired authorization selected")
				}
				return
			case "digest":
				o.Catalog[0].ID = "forged"
				raw, _ := json.Marshal(o)
				if _, e := s.db.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, raw, o.ID); e != nil {
					t.Fatal(e)
				}
			case "dispatch":
				if _, e := s.db.Exec(`DROP TRIGGER acceptance_dispatch_no_delete`); e != nil {
					t.Fatal(e)
				}
				if _, e := s.db.Exec(`DELETE FROM model_acceptance_dispatches WHERE operation_id=?`, o.ID); e != nil {
					t.Fatal(e)
				}
			case "new-connection":
				n, e := s.ProposeProductionConnection(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, productionAdmission(), time.Now())
				if e != nil {
					t.Fatal(e)
				}
				n, e = s.DecideModelAcceptance(a.ID, n.ID, n.Revision, n.MaterialDigest, "approve", time.Now())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.ClaimModelAcceptance(a.ID, n.ID, n.Revision, n.MaterialDigest, time.Now()); e != nil {
					t.Fatal(e)
				}
			case "uncertain-cleanup":
				if _, e := s.BeginModelAuthorization(a.ID, "unresolved", "sign_in", "other-proof"); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.PutProfile(p); e == nil {
				t.Fatal("stale catalog selected")
			}
		})
	}
}
