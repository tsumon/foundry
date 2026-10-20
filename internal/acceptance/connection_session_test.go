package acceptance

import (
	"context"
	"database/sql"
	"errors"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestBrowserHandoffConstructorPolicyCannotBeForgedOrDowngraded(t *testing.T) {
	for _, policy := range []string{"valid", "missing subscription", "duplicate", "missing SIWC", "production SIWC", "short lifetime", "missing route", "legacy material", "different store"} {
		t.Run(policy, func(t *testing.T) {
			s, err := store.Open(filepath.Join(t.TempDir(), "gate.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			a, err := s.SaveModelAccount(domain.ModelAccount{ID: "browser", Name: "browser", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "http://127.0.0.1:1/v1"}, 0)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.CreateModelRoute(domain.ModelRoute{ID: "browser-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
			if err != nil {
				t.Fatal(err)
			}
			secrets := gateSecrets{}
			lifecycle := accounts.New(s, secrets)
			if policy == "different store" {
				other, err := store.Open(filepath.Join(t.TempDir(), "other.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				lifecycle = accounts.New(other, secrets)
			}
			const origin = "http://127.0.0.1:1"
			flowTimeout := 10 * time.Minute
			if policy == "short lifetime" {
				flowTimeout = time.Minute
			}
			siwc, err := accounts.NewSIWCSimulation(lifecycle, accounts.SIWCOptions{AuthorizationOrigin: origin, TokenOrigin: origin, OIDCOrigin: origin, JWKSOrigin: origin, RevocationOrigin: origin, Issuer: origin, FlowTimeout: flowTimeout})
			if err != nil {
				t.Fatal(err)
			}
			defer siwc.Close()
			options := Options{Routes: []domain.ModelRoute{r}, SubscriptionConnectionRouteIDs: []string{r.ID}, BrowserHandoffRouteIDs: []string{r.ID}, BrowserSIWC: siwc, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(context.Context, domain.ModelAcceptanceMaterial) error { t.Error("connector called"); return nil }}
			clock := time.Now()
			options.Now = func() time.Time { return clock }
			switch policy {
			case "missing subscription":
				options.SubscriptionConnectionRouteIDs = nil
			case "duplicate":
				options.BrowserHandoffRouteIDs = append(options.BrowserHandoffRouteIDs, r.ID)
			case "missing SIWC":
				options.BrowserSIWC = nil
			case "production SIWC":
				options.BrowserSIWC = accounts.NewSIWCService(lifecycle)
			case "missing route":
				options.Routes = nil
			}
			svc := NewSimulation(s, options)
			defer svc.Close()
			if err = svc.Recover(); err != nil {
				t.Fatal(err)
			}
			q := domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}
			if policy == "legacy material" {
				o, err := s.ProposeModelAcceptance(a.ID, q, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if _, err = svc.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve"); err != ErrDisabled {
					t.Fatal("legacy policy bypass admitted", err)
				}
			} else if policy != "valid" {
				if svc.SimulationEligible(a.ID) || !svc.OwnsAccount(a.ID) {
					t.Fatal("invalid policy eligibility/ownership")
				}
				if _, err = svc.Propose(a.ID, q); err != ErrDisabled {
					t.Fatal("invalid policy proposal", err)
				}
			} else {
				o, err := svc.Propose(a.ID, q)
				if err != nil {
					t.Fatal(err)
				}
				if o.Material.ConnectionMode != "browser_handoff" || o.Material.LoginTimeoutSeconds != 600 || o.Material.DeadlineSeconds != 30 {
					t.Fatal("policy not frozen")
				}
				for _, phase := range []string{"catalog", "inference"} {
					q.Phase = phase
					if _, err = svc.Propose(a.ID, q); err != ErrDisabled {
						t.Fatal("downstream admitted")
					}
				}
				o, err = svc.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
				if err != nil {
					t.Fatal(err)
				}
				clock = clock.Add(599 * time.Second)
				o, err = svc.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
				if err != nil || o.State != "running" {
					t.Fatal("near-expiry approval could not execute", err)
				}
				_, view, err := svc.Read(a.ID, o.ID)
				if err != nil || view == nil {
					t.Fatal("missing near-expiry session")
				}
				expires, err := time.Parse(time.RFC3339Nano, view.ExpiresAt)
				if err != nil || time.Until(expires) < 598*time.Second {
					t.Fatal("approval expiry shortened human lifetime")
				}
				if _, err = svc.Cancel(a.ID, o.ID, o.Revision, o.MaterialDigest); err != nil {
					t.Fatal(err)
				}
			}
			if len(secrets) != 0 || policy != "valid" && len(svc.sessions) != 0 {
				t.Fatal("policy validation produced effects")
			}
		})
	}
}

func retainedSessionFixture(t *testing.T) (*Service, domain.ModelAccount, domain.ModelRoute, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "handoff.db")
	ledger, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	a, err := ledger.SaveModelAccount(domain.ModelAccount{ID: "retention", Name: "retention", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "http://127.0.0.1:1/v1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ledger.CreateModelRoute(domain.ModelRoute{ID: "retention-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	const origin = "http://127.0.0.1:1"
	siwc, err := accounts.NewSIWCSimulation(accounts.New(ledger, gateSecrets{}), accounts.SIWCOptions{AuthorizationOrigin: origin, TokenOrigin: origin, OIDCOrigin: origin, JWKSOrigin: origin, RevocationOrigin: origin, Issuer: origin, FlowTimeout: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = siwc.Close() })
	svc := NewSimulation(ledger, Options{Routes: []domain.ModelRoute{r}, SubscriptionConnectionRouteIDs: []string{r.ID}, BrowserHandoffRouteIDs: []string{r.ID}, BrowserSIWC: siwc, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(context.Context, domain.ModelAcceptanceMaterial) error {
		t.Error("legacy connector called")
		return nil
	}})
	t.Cleanup(func() { _ = svc.Close() })
	if err = svc.Recover(); err != nil {
		t.Fatal(err)
	}
	return svc, a, r, path
}

func startRetainedSession(t *testing.T, svc *Service, a domain.ModelAccount, r domain.ModelRoute) domain.ModelAcceptanceOperation {
	t.Helper()
	o, err := svc.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision})
	if err != nil {
		t.Fatal(err)
	}
	o, err = svc.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
	if err != nil {
		t.Fatal(err)
	}
	o, err = svc.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
	if err != nil || o.State != "running" {
		t.Fatal("session did not start", err)
	}
	return o
}

func TestBrowserHandoffTerminalSessionsReleaseOwnedAttempt(t *testing.T) {
	for _, receiptFault := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed receipt", true: "failed receipt"}[receiptFault], func(t *testing.T) {
			svc, a, r, path := retainedSessionFixture(t)
			o := startRetainedSession(t, svc, a, r)
			svc.sessionsMu.Lock()
			captured := svc.sessions[o.ID]
			svc.sessionsMu.Unlock()
			if captured == nil {
				t.Fatal("missing active join handle")
			}
			if receiptFault {
				db, err := sql.Open("sqlite3", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err = db.Exec(`CREATE TRIGGER retention_receipt_fault BEFORE UPDATE ON model_acceptance_operations WHEN OLD.state='running' AND NEW.state!='running' BEGIN SELECT RAISE(ABORT,'synthetic receipt fault'); END`); err != nil {
					t.Fatal(err)
				}
			}
			_, err := svc.Cancel(a.ID, o.ID, o.Revision, o.MaterialDigest)
			if receiptFault && !errors.Is(err, ErrPersistence) || !receiptFault && err != nil {
				t.Fatalf("cancel result=%v", err)
			}
			<-captured.done
			if receiptFault && !errors.Is(captured.err, ErrPersistence) || !receiptFault && captured.err != nil {
				t.Fatal("eviction lost captured waiter result")
			}
			svc.sessionsMu.Lock()
			retained := len(svc.sessions)
			svc.sessionsMu.Unlock()
			if retained != 0 {
				t.Fatalf("terminal controller retains %d owned protocol attempt(s)", retained)
			}
			got, view, readErr := svc.Read(a.ID, o.ID)
			if readErr != nil || view != nil || got.Attempts != 1 {
				t.Fatal("eviction broke durable read reconciliation")
			}
			if receiptFault && got.State != "running" || !receiptFault && got.State != "unknown" {
				t.Fatal("eviction changed durable outcome")
			}
		})
	}
}

func TestBrowserHandoffTerminalEvictionRacesReadCancelAndClose(t *testing.T) {
	for i := 0; i < 8; i++ {
		svc, a, r, _ := retainedSessionFixture(t)
		o := startRetainedSession(t, svc, a, r)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.Cancel(a.ID, o.ID, o.Revision, o.MaterialDigest)
			if err != nil && !errors.Is(err, ErrDisabled) && !errors.Is(err, store.ErrConflict) {
				t.Errorf("cancel race=%v", err)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			if err := svc.Close(); err != nil {
				t.Errorf("close race=%v", err)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			got, view, err := svc.Read(a.ID, o.ID)
			if err != nil || got.State != "running" && view != nil {
				t.Error("read race exposed terminal session")
			}
		}()
		close(start)
		wg.Wait()
		svc.sessionsMu.Lock()
		retained := len(svc.sessions)
		svc.sessionsMu.Unlock()
		if retained != 0 {
			t.Fatalf("joined close retained %d sessions", retained)
		}
		got, view, err := svc.Read(a.ID, o.ID)
		if err != nil || got.State != "unknown" || got.Attempts != 1 || view != nil {
			t.Fatal("close lost durable terminal read")
		}
		if err = svc.Recover(); err != nil {
			t.Fatal(err)
		}
		if _, err = svc.Execute(context.Background(), a.ID, o.ID, got.Revision, got.MaterialDigest); err == nil {
			t.Fatal("eviction enabled replay")
		}
	}
}

func TestBrowserHandoffURLProjectionRejectsUnsafeVariants(t *testing.T) {
	for _, raw := range []string{"https://127.0.0.1:1234/auth/start", "http://localhost:1234/auth/start", "http://127.0.0.1:1234/auth/start?x=1", "http://127.0.0.1:1234/auth/start?", "http://127.0.0.1:1234/auth/start#x", "http://user@127.0.0.1:1234/auth/start", "http://127.0.0.1:01234/auth/start", "http://127.0.0.1:1234/auth/%73tart", "http://127.0.0.1:0/auth/start", "http://127.0.0.1:65536/auth/start", "http://127.0.0.1:1234/auth/callback"} {
		if safeHandoffURL(raw) {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
	if !safeHandoffURL("http://127.0.0.1:1234/auth/start") {
		t.Fatal("safe link rejected")
	}
}
