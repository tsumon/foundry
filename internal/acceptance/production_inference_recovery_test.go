package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/store"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProductionInferenceStoredCredentialNoRefreshOrWrites(t *testing.T) {
	s, db, a, r, c, _, calls, _ := inferenceController(t)
	auth, e := db.GetModelAuthorization("catalog-proof")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]string{"access_token": "synthetic-catalog-secret", "refresh_token": "unused-synthetic-refresh", "expires_at": auth.ExpiresAt, "resource": "https://api.openai.com/v1"})
	secrets := &catalogMemorySecrets{t: t, ref: auth.SecretRef, value: string(raw)}
	s.inference.resolver = accounts.New(db, secrets)
	o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
	if secrets.reads != 0 {
		t.Fatal("preparation read secrets")
	}
	got, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
	if e != nil || got.State != "succeeded" || secrets.reads != 1 || calls.Load() != 1 {
		t.Fatalf("stored credential: %+v %v", got, e)
	}
}

func TestProductionInferenceDefaultOffAndNoScopeExpansion(t *testing.T) {
	s, db, a, r, c, resolver, calls, _ := inferenceController(t)
	closed := New(db)
	if e := closed.Recover(); e != nil {
		t.Fatal(e)
	}
	q := domain.ModelAcceptanceProposal{Phase: "inference", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: c.ID, Model: "gpt-6.1-sol"}
	if _, e := closed.Propose(a.ID, q); e != ErrDisabled {
		t.Fatal("default enabled")
	}
	for _, change := range []string{"phase", "model", "route", "account", "purpose", "prerequisite", "revision"} {
		copy := q
		account := a.ID
		switch change {
		case "phase":
			copy.Phase = "catalog"
		case "model":
			copy.Model = "other"
		case "route":
			copy.RouteID = "other"
		case "account":
			account = "other"
		case "purpose":
			copy.ConnectionPurpose = "subscription_only"
		case "prerequisite":
			copy.PrerequisiteID = ""
		case "revision":
			copy.ExpectedAccountRevision++
		}
		if _, e := s.Propose(account, copy); e == nil {
			t.Fatalf("accepted %s", change)
		}
	}
	if calls.Load() != 0 || resolver.reads.Load() != 0 {
		t.Fatal("denial had effects")
	}
}

func TestProductionInferenceReopenMarksAbandonedClaimUnknown(t *testing.T) {
	s, db, a, r, c, resolver, calls, path := inferenceController(t)
	o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
	running, e := db.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = db.ReserveProductionInferenceDispatch(running, s.inference.admission, time.Now()); e != nil {
		t.Fatal(e)
	}
	s.Close()
	db.Close()
	reopened, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	fresh := NewProductionInference(reopened, ProductionInferenceOptions{Route: r, Resolver: resolver})
	defer fresh.Close()
	if e = fresh.Recover(); e != nil {
		t.Fatal(e)
	}
	got, e := fresh.Get(a.ID, o.ID)
	if e != nil || got.State != "unknown" || got.Certainty != "unknown" || got.Attempts != 1 || got.ClaimSequence != running.ClaimSequence {
		t.Fatalf("restart: %+v %v", got, e)
	}
	if _, e = fresh.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e == nil || calls.Load() != 0 || resolver.reads.Load() != 0 {
		t.Fatal("restart replayed")
	}
}

func TestProductionInferenceNoDispatchAfterLeaseChange(t *testing.T) {
	for _, kind := range []string{"generation", "account", "expiry", "secret failure", "authorization generation", "authorization expiry", "authorization binding"} {
		t.Run(kind, func(t *testing.T) {
			s, db, a, r, c, resolver, calls, _ := inferenceController(t)
			o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
			switch kind {
			case "generation":
				resolver.generation++
			case "account":
				resolver.after = func() {
					a.Name = "changed"
					if _, e := db.SaveModelAccount(a, a.Revision); e != nil {
						t.Error(e)
					}
				}
			case "expiry":
				resolver.after = func() { s.now = func() time.Time { return time.Now().Add(time.Hour) } }
			case "secret failure":
				resolver.fail = true
			case "authorization generation", "authorization expiry", "authorization binding":
				resolver.after = func() {
					auth, e := db.GetModelAuthorization("catalog-proof")
					if e != nil {
						t.Fatal(e)
					}
					opKind := "refresh"
					if kind == "authorization binding" {
						opKind = "sign_in"
						auth.Binding.ID = "new-proof"
					}
					if _, e = db.BeginModelAuthorization(a.ID, "changed-authorization", opKind, auth.Binding.ID); e != nil {
						t.Fatal(e)
					}
					if kind == "authorization expiry" {
						auth.ExpiresAt = time.Now().Add(2 * time.Hour).Format(time.RFC3339)
					}
					if _, e = db.CommitModelAuthorization("changed-authorization", auth.Binding, auth.ExpiresAt); e != nil {
						t.Fatal(e)
					}
				}
			}
			got, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
			if e != nil || got.State != "failed" || got.Certainty != "not_sent" || calls.Load() != 0 || resolver.reads.Load() != 1 {
				t.Fatalf("unsafe lease change: %+v %v calls=%d", got, e, calls.Load())
			}
		})
	}
}

func TestProductionInferenceReceiptFailureClosesAdmission(t *testing.T) {
	s, _, a, r, c, resolver, calls, path := inferenceController(t)
	o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
	sqlDB, e := sql.Open("sqlite3", path)
	if e != nil {
		t.Fatal(e)
	}
	defer sqlDB.Close()
	if _, e = sqlDB.Exec(`CREATE TRIGGER catalog_receipt_fault BEFORE UPDATE ON model_acceptance_operations WHEN OLD.state='running' AND NEW.state!='running' BEGIN SELECT RAISE(ABORT,'synthetic fault'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e != ErrPersistence {
		t.Fatalf("receipt failure not closed: %v", e)
	}
	if s.ProductionInferenceStatus(a.ID).Enabled || calls.Load() != 1 || resolver.reads.Load() != 1 {
		t.Fatal("unsafe receipt failure")
	}
	if _, e = sqlDB.Exec(`DROP TRIGGER catalog_receipt_fault`); e != nil {
		t.Fatal(e)
	}
	if e = s.Recover(); e != nil {
		t.Fatal(e)
	}
	got, e := s.Get(a.ID, o.ID)
	if e != nil || got.State != "unknown" || got.Attempts != 1 {
		t.Fatalf("lost receipt recovery: %+v %v", got, e)
	}
	if _, e = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e == nil || calls.Load() != 1 || resolver.reads.Load() != 1 {
		t.Fatal("receipt failure replayed")
	}
}

func TestProductionInferenceCannotAdoptChangesAfterPossibleDispatch(t *testing.T) {
	for _, change := range []string{"expiry", "account", "interrupted"} {
		t.Run(change, func(t *testing.T) {
			s, db, a, r, c, resolver, calls, _ := inferenceController(t)
			o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.inference.http.Transport = catalogTransport(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				switch change {
				case "expiry":
					s.now = func() time.Time { return time.Now().Add(time.Hour) }
				case "account":
					a.Name = "changed after send"
					if _, e := db.SaveModelAccount(a, a.Revision); e != nil {
						t.Fatal(e)
					}
				case "interrupted":
					cancel()
					return nil, ctx.Err()
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(inferenceCompletion)), Request: req}, nil
			})
			got, e := s.Execute(ctx, a.ID, o.ID, o.Revision, o.MaterialDigest)
			if change == "interrupted" {
				if e != nil || got.State != "unknown" {
					t.Fatalf("interruption result: %s %v", got.State, e)
				}
			} else {
				if e != ErrPersistence {
					t.Fatalf("changed authority completed: %v", e)
				}
				got, e = s.Get(a.ID, o.ID)
				if e != nil || got.State != "unknown" {
					t.Fatal("changed authority not conserved as unknown")
				}
			}
			if _, e = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e == nil || calls.Load() != 1 || resolver.reads.Load() != 1 {
				t.Fatal("possible dispatch replayed")
			}
		})
	}
}
