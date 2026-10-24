package store

import (
	"foundry/internal/domain"
	"testing"
	"time"
)

func TestSessionRenewalStoreClaimAndWithdrawalFence(t *testing.T) {
	s := openTestStore(t)
	b := authorizationFixture(t, s)
	b.Scopes = append(b.Scopes, "offline_access")
	op, err := s.BeginModelAuthorization(b.AccountID, "renewal-signin", "sign_in", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CommitModelAuthorization(op.ID, b, "2099-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.PrepareSessionRenewal(domain.SessionRenewalMaterial{ID: "material", AccountID: b.AccountID, AccountRevision: 1, Binding: b, Resource: "http://127.0.0.1:1234/v1", ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApproveSessionRenewal(b.AccountID, m.ID, m.Revision, time.Now()); err != nil {
		t.Fatal(err)
	}
	p, _ := s.SessionRenewalPolicy(b.AccountID)
	if err = s.ClaimSessionRenewal(p, a.Generation, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimSessionRenewal(p, a.Generation, time.Now()); err == nil {
		t.Fatal("duplicate generation claim")
	}
	next, err := s.BeginModelAuthorization(b.AccountID, "renewal-refresh", "refresh", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DisableSessionRenewal(b.AccountID, p.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitModelAuthorizationSchedule(next.ID, b, "2099-01-02T00:00:00Z", "", p.Revision); err == nil {
		t.Fatal("withdrawn consent committed")
	}
	latest, _ := s.SessionRenewalPolicy(b.AccountID)
	if latest.Enabled {
		t.Fatal("withdrawal resurrected")
	}
}

func TestSessionRenewalRecoveryDoesNotReplayDispatch(t *testing.T) {
	s := openTestStore(t)
	b := authorizationFixture(t, s)
	b.Scopes = append(b.Scopes, "offline_access")
	op, _ := s.BeginModelAuthorization(b.AccountID, "renewal-signin", "sign_in", b.ID)
	a, err := s.CommitModelAuthorization(op.ID, b, "2099-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.PrepareSessionRenewal(domain.SessionRenewalMaterial{ID: "material", AccountID: b.AccountID, AccountRevision: 1, Binding: b, Resource: "http://127.0.0.1:1234/v1", ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApproveSessionRenewal(b.AccountID, m.ID, m.Revision, time.Now()); err != nil {
		t.Fatal(err)
	}
	p, _ := s.SessionRenewalPolicy(b.AccountID)
	if err = s.ClaimSessionRenewal(p, a.Generation, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverSessionRenewals(); err != nil {
		t.Fatal(err)
	}
	receipt, _ := s.SessionRenewalReceipt(b.AccountID)
	current, _ := s.GetModelAuthorization(b.ID)
	if receipt.Stage != "recovery_required" || current.State != "refresh_unknown" {
		t.Fatalf("crash state: %+v %+v", receipt, current)
	}
	if err = s.ClaimSessionRenewal(p, a.Generation, time.Now()); err == nil {
		t.Fatal("crashed dispatch replayed")
	}
}

func TestSessionRenewalRecoveryRecognizesCommittedGeneration(t *testing.T) {
	s := openTestStore(t)
	b := authorizationFixture(t, s)
	b.Scopes = append(b.Scopes, "offline_access")
	op, _ := s.BeginModelAuthorization(b.AccountID, "renewal-signin", "sign_in", b.ID)
	a, err := s.CommitModelAuthorization(op.ID, b, "2099-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.PrepareSessionRenewal(domain.SessionRenewalMaterial{ID: "material", AccountID: b.AccountID, AccountRevision: 1, Binding: b, Resource: "http://127.0.0.1:1234/v1", ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApproveSessionRenewal(b.AccountID, m.ID, m.Revision, time.Now()); err != nil {
		t.Fatal(err)
	}
	p, _ := s.SessionRenewalPolicy(b.AccountID)
	if err = s.ClaimSessionRenewal(p, a.Generation, time.Now()); err != nil {
		t.Fatal(err)
	}
	next, err := s.BeginModelAuthorization(b.AccountID, "renewal-refresh", "refresh", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitModelAuthorizationSchedule(next.ID, b, "2099-01-02T00:00:00Z", "", p.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverSessionRenewals(); err != nil {
		t.Fatal(err)
	}
	receipt, _ := s.SessionRenewalReceipt(b.AccountID)
	if receipt.NewGeneration != 2 || receipt.Certainty != "confirmed" || receipt.Stage != "recovery_required" {
		t.Fatalf("commit evidence lost: %+v", receipt)
	}
}
