package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestOwnedHandoffWaitJoinsExpiryAndRejectsLateCallback(t *testing.T) {
	lifecycle, db, secrets, _ := setupAccounts(t)
	issuer := newSIWCIssuerFixture(t, "https://api.openai.com/v1")
	account := newSIWCAccount(t, lifecycle, issuer.server.URL+"/v1")
	options := issuer.options()
	options.FlowTimeout = 10 * time.Minute
	s, err := NewSIWCSimulation(lifecycle, options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flow, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(ctx, account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	query := openHandoff(t, flow)
	// Expire only after observing the handoff; CI scheduling must not close the
	// listener before this test reaches the late-callback boundary it exercises.
	cancel()
	terminal, err := s.Wait(flow.OperationID)
	if err != nil || terminal.State != "expired" || terminal.HandoffURL != "" || terminal.ConnectionDiagnostic == nil || terminal.ConnectionDiagnostic.Stage != "session" || terminal.ConnectionDiagnostic.Code != "expired" || terminal.ConnectionDiagnostic.CredentialCommit != "not_committed" || terminal.ConnectionDiagnostic.Cleanup != "not_required" {
		t.Fatalf("terminal=%+v %v", terminal, err)
	}
	if response, err := http.Get(query.Get("redirect_uri") + "?state=" + query.Get("state") + "&code=synthetic&client_id=oaiapp_synthetic"); err == nil {
		response.Body.Close()
		if response.StatusCode == 200 {
			t.Fatal("late callback accepted")
		}
	}
	if issuer.tokenCalls.Load() != 0 || secrets.sets != 0 {
		t.Fatal("expiry caused exchange/credential write")
	}
	if _, err = db.CurrentModelAuthorization(account.ID); err == nil {
		t.Fatal("expiry committed proof")
	}
}

func TestOwnedHandoffExchangeHasThirtySecondBoundInsideHumanWindow(t *testing.T) {
	lifecycle, _, _, _ := setupAccounts(t)
	issuer := newSIWCIssuerFixture(t, "https://api.openai.com/v1")
	account := newSIWCAccount(t, lifecycle, issuer.server.URL+"/v1")
	options := issuer.options()
	options.FlowTimeout = 10 * time.Minute
	s, err := NewSIWCSimulation(lifecycle, options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	deadlines := make(chan time.Duration, 1)
	s.http.Transport = subscriptionRoundTrip(func(req *http.Request) (*http.Response, error) {
		deadline, ok := req.Context().Deadline()
		if !ok {
			deadlines <- 0
		} else {
			deadlines <- time.Until(deadline)
		}
		return nil, errors.New("synthetic offline failure")
	})
	flow, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	query := openHandoff(t, flow)
	response, err := http.Get(query.Get("redirect_uri") + "?" + url.Values{"state": {query.Get("state")}, "code": {"synthetic"}, "client_id": {"oaiapp_synthetic"}}.Encode())
	if err != nil {
		t.Fatal("callback transport unavailable")
	}
	response.Body.Close()
	if remaining := <-deadlines; remaining < 29*time.Second || remaining > 30*time.Second {
		t.Fatalf("wrong network bound: %v", remaining)
	}
	terminal, err := s.Wait(flow.OperationID)
	if err != nil || terminal.State != "failed" || terminal.ConnectionDiagnostic == nil || terminal.ConnectionDiagnostic.Stage != "token_exchange" || terminal.ConnectionDiagnostic.Code != "transport_unknown" {
		t.Fatal("failed exchange not terminal")
	}
}

func TestOwnedHandoffTerminalDuringExchangeWithUnavailableDurableEvidenceIsUnknown(t *testing.T) {
	for _, terminalState := range []string{"canceled", "expired"} {
		t.Run(terminalState, func(t *testing.T) {
			lifecycle, db, _, _ := setupAccounts(t)
			issuer := newSIWCIssuerFixture(t, "https://api.openai.com/v1")
			account := newSIWCAccount(t, lifecycle, issuer.server.URL+"/v1")
			s, err := NewSIWCSimulation(lifecycle, issuer.options())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			tokenStarted := make(chan struct{})
			releaseToken := make(chan struct{})
			s.http.Transport = subscriptionRoundTrip(func(req *http.Request) (*http.Response, error) {
				close(tokenStarted)
				<-releaseToken
				return nil, req.Context().Err()
			})
			var flowContext context.Context = context.Background()
			expire := func() {}
			if terminalState == "expired" {
				flowContext, expire = context.WithCancel(flowContext)
			}
			defer expire()
			flow, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(flowContext, account.ID, account.Revision)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			query := openHandoff(t, flow)
			callbackDone := make(chan struct{})
			go func() {
				defer close(callbackDone)
				response, callbackErr := http.Get(query.Get("redirect_uri") + "?" + url.Values{"state": {query.Get("state")}, "code": {"synthetic"}, "client_id": {"oaiapp_synthetic"}}.Encode())
				if callbackErr == nil {
					response.Body.Close()
				}
			}()
			select {
			case <-tokenStarted:
			case <-time.After(time.Second):
				t.Fatal("token exchange did not start")
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			if terminalState == "canceled" {
				if _, err = s.Cancel(context.Background(), flow.OperationID); err != nil {
					t.Fatal(err)
				}
			} else {
				expire()
				deadline := time.Now().Add(time.Second)
				for {
					status, statusErr := s.Status(flow.OperationID)
					if statusErr != nil {
						t.Fatal(statusErr)
					}
					if status.State == "expired" {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("exchange did not enter expired state")
					}
					time.Sleep(time.Millisecond)
				}
			}
			close(releaseToken)
			terminal, err := s.Wait(flow.OperationID)
			if err != nil || terminal.State != terminalState || terminal.ConnectionDiagnostic == nil || terminal.ConnectionDiagnostic.Stage != "session" || terminal.ConnectionDiagnostic.Code != terminalState || terminal.ConnectionDiagnostic.CredentialCommit != "unknown" || terminal.ConnectionDiagnostic.Cleanup != "unknown" {
				t.Fatalf("terminal=%+v %v", terminal, err)
			}
			select {
			case <-callbackDone:
			case <-time.After(time.Second):
				t.Fatal("callback did not join")
			}
		})
	}
}

func TestOwnedHandoffLateAccountRevalidationWithUnavailableDurableEvidenceIsUnknown(t *testing.T) {
	lifecycle, db, secrets, _ := setupAccounts(t)
	issuer := newSIWCIssuerFixture(t, "https://api.openai.com/v1")
	account := newSIWCAccount(t, lifecycle, issuer.server.URL+"/v1")
	s, err := NewSIWCSimulation(lifecycle, issuer.options())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	flow, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	query := openHandoff(t, flow)
	issuer.mu.Lock()
	issuer.nonce = query.Get("nonce")
	issuer.mu.Unlock()
	transport := s.http.Transport
	closedAfterIntent := false
	s.http.Transport = subscriptionRoundTrip(func(req *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(req)
		if err == nil && req.URL.Path == "/jwks" {
			op, readErr := db.GetModelAuthorizationOperation(flow.OperationID)
			if readErr != nil || op.State != "pending" {
				t.Errorf("durable intent missing before late failure: %+v %v", op, readErr)
			}
			if closeErr := db.Close(); closeErr != nil {
				t.Error(closeErr)
			}
			closedAfterIntent = true
		}
		return response, err
	})
	response, err := http.Get(query.Get("redirect_uri") + "?" + url.Values{"state": {query.Get("state")}, "code": {"synthetic"}, "client_id": {"oaiapp_synthetic"}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	terminal, err := s.Wait(flow.OperationID)
	if err != nil || terminal.State != "failed" || terminal.ConnectionDiagnostic == nil || terminal.ConnectionDiagnostic.Stage != "authorization_intent" || terminal.ConnectionDiagnostic.Code != "auth_invalid" || terminal.ConnectionDiagnostic.CredentialCommit != "unknown" || terminal.ConnectionDiagnostic.Cleanup != "unknown" {
		t.Fatalf("late revalidation diagnostic=%+v err=%v", terminal.ConnectionDiagnostic, err)
	}
	if !closedAfterIntent || issuer.tokenCalls.Load() != 1 || secrets.sets != 0 || secrets.gets != 0 || secrets.deletes != 0 {
		t.Fatal("late failure retried, accessed credentials, or did not reach durable intent")
	}
}
