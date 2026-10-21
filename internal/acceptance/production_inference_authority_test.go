package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"foundry/internal/domain"
	"testing"
	"time"
)

func TestProductionInferenceFrozenAuthorityAndRecovery(t *testing.T) {
	for _, change := range []string{"stale process", "cross account", "wrong phase", "connection profile", "source", "account changed", "approval expired", "execution expired", "newer failed connection", "unresolved auth", "crash"} {
		t.Run(change, func(t *testing.T) {
			s, db, a, r, c, resolver, calls, path := inferenceController(t)
			o := proposeInference(t, s, a, r, c)
			switch change {
			case "stale process":
				fresh := NewProductionInference(db, ProductionInferenceOptions{Route: r, Resolver: resolver})
				if e := fresh.Recover(); e != nil {
					t.Fatal(e)
				}
				s = fresh
			case "cross account":
				a.ID = "other"
			case "wrong phase", "connection profile", "source":
				switch change {
				case "wrong phase":
					o.Material.Phase = "catalog"
					o.Phase = "catalog"
				case "connection profile":
					o.Material.ProductionAdmission.Profile = "openai_siwc_subscription_v1"
				case "source":
					o.Source = "local_simulation"
				}
				o.MaterialDigest = domain.AcceptanceDigest(o.Material)
				raw, _ := json.Marshal(o)
				sqlDB, e := sql.Open("sqlite3", path)
				if e != nil {
					t.Fatal(e)
				}
				defer sqlDB.Close()
				if _, e = sqlDB.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, raw, o.ID); e != nil {
					t.Fatal(e)
				}
			case "account changed":
				a.Name = "changed"
				if _, e := db.SaveModelAccount(a, a.Revision); e != nil {
					t.Fatal(e)
				}
			case "approval expired":
				s.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
			case "newer failed connection":
				connection, e := db.GetModelAcceptance(a.ID, c.Material.ConnectionID)
				if e != nil {
					t.Fatal(e)
				}
				admission := *connection.Material.ProductionAdmission
				n, e := db.ProposeProductionConnection(a.ID, productionProposal(a, r), admission, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				n, e = db.DecideModelAcceptance(a.ID, n.ID, n.Revision, n.MaterialDigest, "approve", time.Now())
				if e != nil {
					t.Fatal(e)
				}
				n, e = db.ClaimModelAcceptance(a.ID, n.ID, n.Revision, n.MaterialDigest, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = db.FinishProductionConnection(n, "failed", "not_sent", "request_rejected", time.Now()); e != nil {
					t.Fatal(e)
				}
			case "unresolved auth":
				if _, e := db.BeginModelAuthorization(a.ID, "pending-other", "sign_in", "other-proof"); e != nil {
					t.Fatal(e)
				}
			case "execution expired", "crash":
				o = approveCatalog(t, s, o)
				if change == "execution expired" {
					s.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
				} else {
					var e error
					o, e = db.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
					if e != nil {
						t.Fatal(e)
					}
					if e = db.ReserveProductionInferenceDispatch(o, s.inference.admission, time.Now()); e != nil {
						t.Fatal(e)
					}
					if e = s.Recover(); e != nil {
						t.Fatal(e)
					}
					got, _ := s.Get(a.ID, o.ID)
					if got.State != "unknown" || got.Attempts != 1 {
						t.Fatal("crash not conserved")
					}
				}
			}
			if change != "execution expired" && change != "crash" {
				if _, e := s.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve"); e == nil {
					t.Fatal("stale approval accepted")
				}
			} else {
				if _, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e == nil {
					t.Fatal("stale execution accepted")
				}
			}
			if calls.Load() != 0 || resolver.reads.Load() != 0 {
				t.Fatal("authority failure touched external boundary")
			}
		})
	}
}
