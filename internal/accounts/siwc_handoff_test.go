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
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	flow, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(ctx, account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	query := openHandoff(t, flow)
	terminal, err := s.Wait(flow.OperationID)
	if err != nil || terminal.State != "expired" || terminal.HandoffURL != "" {
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
	if err != nil || terminal.State != "failed" {
		t.Fatal("failed exchange not terminal")
	}
}
