package store

import (
	"encoding/json"
	"fmt"
	"foundry/internal/domain"
	"strings"
	"testing"
	"time"
)

func catalogRenewalAdmission() domain.ProductionAdmission {
	return domain.ProductionAdmission{Generation: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Profile: "openai_siwc_catalog_v1"}
}

func TestProductionCatalogRenewalOriginPersistenceAndLegacy(t *testing.T) {
	for _, mode := range []string{"new_connection", "legacy_exact", "legacy_unprovable"} {
		t.Run(mode, func(t *testing.T) {
			s, _, a, r, connection := catalogFixture(t)
			p := enableCatalogRenewal(t, s, a)
			if mode == "legacy_exact" {
				removeCatalogOrigin(t, s)
			}
			renewCatalogAuthorization(t, s, p)
			if mode == "legacy_unprovable" {
				removeCatalogOrigin(t, s)
				renewCatalogAuthorization(t, s, p)
			}
			var filename, name string
			var seq int
			if err := s.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &filename); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(filename)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			got := reopened.ProductionCatalogReadiness(a.ID, r.ID, catalogRenewalAdmission(), time.Now())
			if mode == "legacy_unprovable" {
				if got != "prerequisite_unavailable" {
					t.Fatal("unprovable legacy history adopted", got)
				}
				return
			}
			if got != "" {
				t.Fatal("durable verified lineage unavailable", got)
			}
			var raw string
			if err = reopened.db.QueryRow(`SELECT authorization FROM model_acceptance_connection_origins WHERE operation_id=?`, connection.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(raw, "secret") || strings.Contains(raw, "foundry-model-authorization-") {
				t.Fatal("secret reference captured")
			}
			if _, err = reopened.db.Exec(`UPDATE model_acceptance_connection_origins SET authorization='{}' WHERE operation_id=?`, connection.ID); err == nil {
				t.Fatal("origin mutable")
			}
			if _, err = reopened.db.Exec(`DELETE FROM model_acceptance_connection_origins WHERE operation_id=?`, connection.ID); err == nil {
				t.Fatal("origin deletable")
			}
		})
	}
}

// Renewal only preserves the connection prerequisite, never a request approval.
func TestProductionCatalogRenewalInvalidatesEveryPinnedStage(t *testing.T) {
	for _, stage := range []string{"decision", "claim", "reservation", "validation", "completion", "selection"} {
		t.Run(stage, func(t *testing.T) {
			s, c, a, r, connection := catalogFixture(t)
			p := enableCatalogRenewal(t, s, a)
			renewCatalogAuthorization(t, s, p)
			admission := catalogRenewalAdmission()
			o, err := c.ProposeProductionCatalog(a.ID, domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: connection.ID}, admission, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if stage != "decision" {
				o, err = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
				if err != nil {
					t.Fatal(err)
				}
			}
			if stage != "decision" && stage != "claim" {
				o, err = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if stage != "reservation" {
					if err = c.ReserveProductionCatalogDispatch(o, admission, time.Now()); err != nil {
						t.Fatal(err)
					}
				}
			}
			if stage == "selection" {
				o, err = c.FinishProductionCatalog(o, "succeeded", "completed", "", []domain.ModelAcceptanceCatalogEntry{{ID: "synthetic", DisplayName: "Synthetic"}}, time.Now())
				if err != nil {
					t.Fatal(err)
				}
			}
			var before string
			if err = s.db.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=?`, o.ID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			renewCatalogAuthorization(t, s, p)
			switch stage {
			case "decision":
				_, err = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
			case "claim":
				_, err = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
			case "reservation":
				err = c.ReserveProductionCatalogDispatch(o, admission, time.Now())
			case "validation":
				err = c.ValidateProductionCatalogDispatch(o, admission, time.Now())
			case "completion":
				_, err = c.FinishProductionCatalog(o, "succeeded", "completed", "", nil, time.Now())
			case "selection":
				err = s.PutProfile(domain.Profile{ID: "stale-catalog", Name: "Synthetic", Kind: "direct", AccountID: a.ID, RouteID: r.ID, Protocol: r.Protocol, BaseURL: r.BaseURL, Model: "synthetic", CatalogOperationID: o.ID})
			}
			if err == nil {
				t.Fatal("renewal adopted pinned old catalog authority")
			}
			var after string
			if err = s.db.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=?`, o.ID).Scan(&after); err != nil {
				t.Fatal(err)
			}
			// Decision/claim intentionally persist a stale terminal material;
			// completed receipts and already consumed claims remain untouched.
			if stage != "decision" && stage != "claim" && before != after {
				t.Fatal("stale catalog modified")
			}
		})
	}
}

func renewalSQL(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// Simulate a pre-upgrade database, not a supported mutation of an origin.
func removeCatalogOrigin(t *testing.T, s *Store) {
	t.Helper()
	renewalSQL(t, s, `DROP TRIGGER acceptance_origin_no_delete`)
	renewalSQL(t, s, `DELETE FROM model_acceptance_connection_origins`)
}

func TestProductionCatalogLegacyOriginBeforeExpiredRotation(t *testing.T) {
	s, _, a, r, connection := catalogFixture(t)
	removeCatalogOrigin(t, s)
	p := enableCatalogRenewal(t, s, a)
	auth, err := s.CurrentModelAuthorization(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The claim occurs after access expiry; renewal consent remains enabled.
	if err = s.ClaimSessionRenewal(p, auth.Generation, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err = s.db.QueryRow(`SELECT authorization FROM model_acceptance_connection_origins WHERE operation_id=?`, connection.ID).Scan(&raw); err != nil {
		t.Fatalf("exact legacy result must anchor even after access expiry: %v", err)
	}
	var origin domain.ModelAuthorization
	if json.Unmarshal(raw, &origin) != nil || origin.ExpiresAt != auth.ExpiresAt || origin.Generation != 1 {
		t.Fatal("legacy expiry was fabricated")
	}
	if got := s.ProductionCatalogReadiness(a.ID, r.ID, catalogRenewalAdmission(), time.Now().Add(2*time.Hour)); got != "authorization_expired" {
		t.Fatal("expired current authorization admitted", got)
	}
}

func TestProductionCatalogRenewalRejectsBrokenProof(t *testing.T) {
	for _, change := range []string{"missing_origin", "corrupt_origin", "wrong_expiry", "wrong_binding", "missing_edge", "unknown", "failed", "waiting", "dispatched", "category", "skipped", "wrong_account", "missing_finish", "uncommitted", "unclean_retirement", "cleanup_pending", "writer_pending", "new_pending", "current_dispatched", "changed_account", "changed_route", "new_registration", "new_connection", "missing_claim", "expired"} {
		t.Run(change, func(t *testing.T) {
			s, c, a, r, connection := catalogFixture(t)
			p := enableCatalogRenewal(t, s, a)
			renewCatalogAuthorization(t, s, p)
			renewCatalogAuthorization(t, s, p)
			now := time.Now()
			switch change {
			case "missing_origin":
				removeCatalogOrigin(t, s)
			case "corrupt_origin", "wrong_expiry", "wrong_binding":
				renewalSQL(t, s, `DROP TRIGGER acceptance_origin_no_update`)
				if change == "corrupt_origin" {
					renewalSQL(t, s, `UPDATE model_acceptance_connection_origins SET authorization='{}'`)
				}
				if change == "wrong_expiry" {
					renewalSQL(t, s, `UPDATE model_acceptance_connection_origins SET authorization=json_set(authorization,'$.expires_at','2099-01-01T00:00:00Z')`)
				}
				if change == "wrong_binding" {
					renewalSQL(t, s, `UPDATE model_acceptance_connection_origins SET authorization=json_set(authorization,'$.binding.subject','other')`)
				}
			case "missing_edge":
				renewalSQL(t, s, `DELETE FROM session_renewal_dispatches WHERE generation=1`)
			case "unknown":
				renewalSQL(t, s, `UPDATE session_renewal_dispatches SET certainty='unknown' WHERE generation=1`)
			case "failed":
				renewalSQL(t, s, `UPDATE session_renewal_dispatches SET stage='recovery_required' WHERE generation=1`)
			case "waiting", "dispatched":
				renewalSQL(t, s, `UPDATE session_renewal_dispatches SET stage=? WHERE generation=1`, change)
			case "category":
				renewalSQL(t, s, `UPDATE session_renewal_dispatches SET category='renewal_failed' WHERE generation=1`)
			case "skipped":
				renewalSQL(t, s, `UPDATE session_renewal_dispatches SET new_generation=3 WHERE generation=1`)
			case "wrong_account":
				renewalSQL(t, s, `UPDATE session_renewal_dispatches SET account_id='different' WHERE generation=1`)
			case "missing_finish":
				renewalSQL(t, s, `UPDATE session_renewal_dispatches SET finished_at='' WHERE generation=1`)
			case "uncommitted":
				renewalSQL(t, s, `UPDATE model_authorization_operations SET state='failed' WHERE generation=2`)
			case "unclean_retirement":
				renewalSQL(t, s, `UPDATE model_authorization_operations SET state='available' WHERE generation=1`)
			case "cleanup_pending":
				renewalSQL(t, s, `UPDATE model_authorization_operations SET cleanup_pending=1 WHERE generation=1`)
			case "writer_pending":
				renewalSQL(t, s, `UPDATE model_authorization_operations SET write_claimed=1 WHERE generation=2`)
			case "new_pending":
				if _, err := s.BeginModelAuthorization(a.ID, "next-pending", "refresh", "catalog-proof"); err != nil {
					t.Fatal(err)
				}
			case "current_dispatched":
				if err := s.ClaimSessionRenewal(p, 3, now); err != nil {
					t.Fatal(err)
				}
			case "changed_account":
				a.Name = "different"
				var err error
				a, err = s.SaveModelAccount(a, a.Revision)
				if err != nil {
					t.Fatal(err)
				}
			case "changed_route":
				r.ID = "other-route"
				var err error
				r, err = s.CreateModelRoute(r)
				if err != nil {
					t.Fatal(err)
				}
			case "new_registration":
				auth, err := s.CurrentModelAuthorization(a.ID)
				if err != nil {
					t.Fatal(err)
				}
				auth.Binding.ID = "different-registration"
				if _, err = s.BeginModelAuthorization(a.ID, "different-signin", "sign_in", auth.Binding.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = s.CommitModelAuthorization("different-signin", auth.Binding, auth.ExpiresAt); err != nil {
					t.Fatal(err)
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
			case "new_connection":
				runningProduction(t, s, s, a, r)
			case "missing_claim":
				renewalSQL(t, s, `DROP TRIGGER acceptance_dispatch_no_delete`)
				renewalSQL(t, s, `DELETE FROM model_acceptance_dispatches WHERE operation_id=?`, connection.ID)
				renewalSQL(t, s, `DROP TRIGGER acceptance_claim_no_delete`)
				renewalSQL(t, s, `DELETE FROM model_acceptance_claims WHERE operation_id=?`, connection.ID)
			case "expired":
				now = now.Add(5 * time.Hour)
			}
			if got := s.ProductionCatalogReadiness(a.ID, r.ID, catalogRenewalAdmission(), now); got == "" {
				t.Fatal("broken lineage became ready")
			}
			if _, err := c.ProposeProductionCatalog(a.ID, domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: connection.ID}, catalogRenewalAdmission(), now); err == nil {
				t.Fatal("broken lineage proposed catalog")
			}
		})
	}
}

func enableCatalogRenewal(t *testing.T, s *Store, a domain.ModelAccount) domain.SessionRenewalPolicy {
	t.Helper()
	auth, err := s.CurrentModelAuthorization(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.PrepareSessionRenewal(domain.SessionRenewalMaterial{ID: "catalog-renewal", AccountID: a.ID, AccountRevision: a.Revision, Binding: auth.Binding, Resource: a.BaseURL, ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApproveSessionRenewal(a.ID, m.ID, m.Revision, time.Now()); err != nil {
		t.Fatal(err)
	}
	p, err := s.SessionRenewalPolicy(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func renewCatalogAuthorization(t *testing.T, s *Store, p domain.SessionRenewalPolicy) {
	t.Helper()
	a, err := s.CurrentModelAuthorization(p.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimSessionRenewal(p, a.Generation, time.Now()); err != nil {
		t.Fatal(err)
	}
	op, err := s.BeginModelAuthorization(p.AccountID, fmt.Sprintf("catalog-refresh-%d", a.Generation), "refresh", a.Binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.CommitModelAuthorizationSchedule(op.ID, a.Binding, time.Now().Add(time.Duration(a.Generation+1)*time.Hour).Format(time.RFC3339), "", p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingModelAuthorizationCleanup()
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range pending {
		if err = s.CompleteModelAuthorizationCleanup(old.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.FinishSessionRenewal(a.Binding.ID, a.Generation, next.Generation, "succeeded", "", "confirmed", time.Now()); err != nil {
		t.Fatal(err)
	}
}

// Catches rejection of a verified successor or accidental approval inheritance.
func TestProductionCatalogAfterConfirmedSessionRenewal(t *testing.T) {
	for _, rotations := range []int{1, 3} {
		t.Run(fmt.Sprint(rotations), func(t *testing.T) {
			s, c, a, r, connection := catalogFixture(t)
			var before string
			if err := s.db.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=?`, connection.ID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			p := enableCatalogRenewal(t, s, a)
			for range rotations {
				renewCatalogAuthorization(t, s, p)
			}
			admission := domain.ProductionAdmission{Generation: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Profile: "openai_siwc_catalog_v1"}
			if got := s.ProductionCatalogReadiness(a.ID, r.ID, admission, time.Now()); got != "" {
				t.Fatalf("confirmed renewal must preserve connection prerequisite: %s", got)
			}
			o, err := c.ProposeProductionCatalog(a.ID, domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: connection.ID}, admission, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if o.State != "pending" || o.Attempts != 0 || o.Material.Authorization.Generation != int64(rotations+1) {
				t.Fatalf("new independent material: %+v", o)
			}
			if _, err = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now()); err == nil {
				t.Fatal("renewal approved a catalog request")
			}
			o, err = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			o, err = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err = c.ReserveProductionCatalogDispatch(o, admission, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err = c.ValidateProductionCatalogDispatch(o, admission, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err = c.FinishProductionCatalog(o, "succeeded", "completed", "", []domain.ModelAcceptanceCatalogEntry{{ID: "synthetic", DisplayName: "Synthetic"}}, time.Now()); err != nil {
				t.Fatal(err)
			}
			var after string
			if err = s.db.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=?`, connection.ID).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatal("original connection was rewritten")
			}
		})
	}
}
