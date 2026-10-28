package accounts

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// This double changes only the external credential-store latency/failure;
// the coordinator, durable ledger, signed OAuth exchange and rotation are real.
type renewalCredentialWait struct {
	model.Secrets
	reads  int
	onRead func(int)
}

func (s *renewalCredentialWait) Get(ref string) (string, error) {
	s.reads++
	s.onRead(s.reads)
	return s.Secrets.Get(ref)
}

func renewalWaitFixture(t *testing.T) (*SessionRenewal, *siwcIssuerFixture, domain.ModelAuthorization) {
	t.Helper()
	service, ledger, _, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("resource/model endpoint reached") }))
	t.Cleanup(resource.Close)
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, err := NewSIWCSimulation(service, issuer.options())
	if err != nil {
		t.Fatal(err)
	}
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	before, err := ledger.CurrentModelAuthorization(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c := NewSessionRenewal(siwc, func() time.Time { return now })
	t.Cleanup(c.Close)
	m, err := c.Prepare(account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Approve(account.ID, m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	expiry, _ := time.Parse(time.RFC3339, before.ExpiresAt)
	now = expiry.Add(-5 * time.Minute)
	issuer.mu.Lock()
	issuer.nonce = ""
	issuer.mu.Unlock()
	return c, issuer, before
}

// Regression: either native credential read can wait for the OS longer than
// the network deadline. Neither wait may turn a never-sent refresh into an
// unknown rotation. Use the actual production deadline, not a test-only knob.
func TestSessionRenewalCredentialWaitDoesNotSpendTransportTimeout(t *testing.T) {
	for _, read := range []int{1, 2} {
		t.Run(map[int]string{1: "preflight", 2: "intent-read"}[read], func(t *testing.T) {
			t.Parallel()
			c, issuer, before := renewalWaitFixture(t)
			c.siwc.lifecycle.secrets = &renewalCredentialWait{Secrets: c.siwc.lifecycle.secrets, onRead: func(n int) {
				if n == read {
					time.Sleep(31 * time.Second)
				}
			}}
			c.Tick(context.Background())
			after, err := c.ledger().GetModelAuthorization(before.Binding.ID)
			if err != nil || after.State != "available" || after.Generation != before.Generation+1 {
				t.Fatalf("local wait consumed network window: state=%s generation=%d err=%v", after.State, after.Generation, err)
			}
			r, err := c.ledger().SessionRenewalReceipt(before.Binding.AccountID)
			if err != nil || r == nil || r.Stage != "succeeded" || r.Certainty != "confirmed" {
				t.Fatalf("rotation not confirmed: %+v %v", r, err)
			}
			c.Tick(context.Background())
			if issuer.tokenCalls.Load() != 2 {
				t.Fatal("expected initial sign-in plus exactly one rotation")
			}
		})
	}
}

func TestSessionRenewalCanceledCredentialPreflightKeepsGrantWithoutReplay(t *testing.T) {
	c, issuer, before := renewalWaitFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	secrets := &renewalCredentialWait{Secrets: c.siwc.lifecycle.secrets, onRead: func(int) { cancel() }}
	c.siwc.lifecycle.secrets = secrets
	c.Tick(ctx)
	after, _ := c.ledger().GetModelAuthorization(before.Binding.ID)
	r, _ := c.ledger().SessionRenewalReceipt(before.Binding.AccountID)
	if after.State != "available" || after.Generation != before.Generation || r == nil || r.Certainty != "not_sent" || r.Category != "credential_preflight" {
		t.Fatalf("unsent local cancellation poisoned grant: state=%s receipt=%+v", after.State, r)
	}
	c.Tick(context.Background())
	restarted := NewSessionRenewal(c.siwc, c.now)
	defer restarted.Close()
	restarted.Tick(context.Background())
	if issuer.tokenCalls.Load() != 1 || secrets.reads != 1 {
		t.Fatal("local failure was automatically retried")
	}
}

func TestSessionRenewalDeniedCredentialPreflightKeepsGrantWithoutReplay(t *testing.T) {
	c, issuer, before := renewalWaitFixture(t)
	c.siwc.lifecycle.secrets.(*memorySecrets).getErr = errors.New("synthetic keychain denial")
	c.Tick(context.Background())
	after, _ := c.ledger().GetModelAuthorization(before.Binding.ID)
	r, _ := c.ledger().SessionRenewalReceipt(before.Binding.AccountID)
	if after.State != "available" || r == nil || r.Certainty != "not_sent" || r.Category != "credential_preflight" {
		t.Fatalf("local denial poisoned grant: state=%s receipt=%+v", after.State, r)
	}
	c.Tick(context.Background())
	if issuer.tokenCalls.Load() != 1 {
		t.Fatal("denied preflight sent a request")
	}
}

func TestSessionRenewalCanceledBeforePreflightIsNotSent(t *testing.T) {
	c, issuer, before := renewalWaitFixture(t)
	p, _ := c.ledger().SessionRenewalPolicy(before.Binding.AccountID)
	if err := c.ledger().ClaimSessionRenewal(p, before.Generation, time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.siwc.refreshSession(ctx, before.Binding.ID, p.Material.AccountRevision, p.Revision, c.now)
	var provider *model.ProviderError
	if !errors.As(err, &provider) || provider.Code != "credential_preflight" || provider.Certainty != "not_sent" {
		t.Fatalf("pre-read cancellation misclassified: %v", err)
	}
	if issuer.tokenCalls.Load() != 1 {
		t.Fatal("canceled preflight reached issuer")
	}
}

func TestSessionRenewalRevokedGrantOutranksOldLocalFailure(t *testing.T) {
	c, _, before := renewalWaitFixture(t)
	secrets := c.siwc.lifecycle.secrets.(*memorySecrets)
	secrets.getErr = errors.New("synthetic keychain denial")
	c.Tick(context.Background())
	if _, err := c.siwc.lifecycle.RevokeModelAuthorization(context.Background(), before.Binding.ID); err != nil {
		t.Fatal(err)
	}
	status, err := c.Status(before.Binding.AccountID)
	if err != nil || status.State != "login_required" || status.RegistrationRetained {
		t.Fatalf("revoked grant masked by old local receipt: %+v %v", status, err)
	}
}

func TestSessionRenewalWithdrawalDuringCredentialReadNeverSends(t *testing.T) {
	for _, read := range []int{1, 2} {
		t.Run(map[int]string{1: "preflight", 2: "intent-read"}[read], func(t *testing.T) {
			c, issuer, before := renewalWaitFixture(t)
			policy, _ := c.ledger().SessionRenewalPolicy(before.Binding.AccountID)
			c.siwc.lifecycle.secrets = &renewalCredentialWait{Secrets: c.siwc.lifecycle.secrets, onRead: func(n int) {
				if n == read {
					if err := c.ledger().DisableSessionRenewal(before.Binding.AccountID, policy.Revision); err != nil {
						t.Fatal(err)
					}
				}
			}}
			c.Tick(context.Background())
			c.Tick(context.Background())
			after, _ := c.ledger().GetModelAuthorization(before.Binding.ID)
			policy, _ = c.ledger().SessionRenewalPolicy(before.Binding.AccountID)
			if issuer.tokenCalls.Load() != 1 || policy.Enabled || after.Generation != before.Generation {
				t.Fatal("withdrawal during native read sent or committed a refresh")
			}
		})
	}
}
