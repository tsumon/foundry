package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"foundry/internal/acceptance"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const subscriptionGateAccount = "acceptance-gate-openai-subscription"

func subscriptionGateProposal(t *testing.T, r *acceptanceRuntime) domain.ModelAcceptanceOperation {
	t.Helper()
	a, err := r.store.GetModelAccount(subscriptionGateAccount)
	if err != nil {
		t.Fatalf("dedicated subscription account unavailable: %v", err)
	}
	o, err := r.gates.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: a.ID + "-route", ExpectedAccountRevision: a.Revision})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestSubscriptionGateExecutesOneAudienceBoundProtocolExchange(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	o := subscriptionGateProposal(t, r)
	raw, _ := json.Marshal(o.Material)
	if !strings.Contains(string(raw), `"connection_purpose":"subscription_only"`) ||
		!strings.Contains(string(raw), `"requested_scopes":["openid","profile","email","offline_access","resource.invoke","chatgpt.tokens.use.direct"]`) ||
		!strings.Contains(string(raw), `"requested_resource":"https://api.openai.com/v1"`) {
		t.Fatalf("subscription contract absent from approval: %s", raw)
	}
	if _, err := r.gates.Execute(context.Background(), subscriptionGateAccount, o.ID, o.Revision, o.MaterialDigest); err == nil {
		t.Fatal("pending subscription operation executed")
	}
	o, err := r.gates.Decide(subscriptionGateAccount, o.ID, o.Revision, o.MaterialDigest, "approve")
	if err != nil {
		t.Fatal(err)
	}
	r.gateProtocol.mu.Lock()
	inert := r.gateProtocol.tokenCalls == 0 && len(r.gateProtocol.requests) == 0 && r.gateProtocol.modelRequests == 0
	r.gateProtocol.mu.Unlock()
	if !inert {
		t.Fatal("approval performed protocol work")
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, executeErr := r.gates.Execute(context.Background(), subscriptionGateAccount, o.ID, o.Revision, o.MaterialDigest); executeErr == nil && got.State == "succeeded" {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	got, err := r.gates.Get(subscriptionGateAccount, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wins.Load() != 1 || got.State != "succeeded" || got.Attempts != 1 || got.Quota != "unknown" || got.RemoteBilling != "unverified" || got.LiveAccess != "blocked" || len(got.Catalog) != 0 || got.UsageKnown || got.InputTokens != 0 || got.OutputTokens != 0 {
		t.Fatalf("unsafe subscription receipt: wins=%d %+v", wins.Load(), got)
	}
	auth, err := r.store.CurrentModelAuthorization(subscriptionGateAccount)
	wantScopes := []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}
	if err != nil || auth.Binding.SpendClass != "subscription_only" || !reflect.DeepEqual(auth.Binding.Scopes, wantScopes) {
		t.Fatalf("subscription evidence=%+v err=%v", auth, err)
	}
	r.gateProtocol.mu.Lock()
	defer r.gateProtocol.mu.Unlock()
	resources := map[string]bool{}
	for _, request := range r.gateProtocol.requests {
		resources[request.resource] = true
	}
	if r.gateProtocol.tokenCalls != 1 || !resources["https://api.openai.com/v1"] || len(resources) != 1 || r.gateProtocol.modelRequests != 0 || r.gateProtocol.directories != 0 || len(r.gateProtocol.modelHits) != 0 || r.gateProtocol.refreshCalls != 0 || len(r.gateProtocol.refreshes) != 1 || r.gateProtocol.invalid != "" {
		t.Fatalf("unexpected protocol effects: token=%d resources=%v model=%d directory=%d refresh_calls=%d refresh_grants=%d invalid=%q", r.gateProtocol.tokenCalls, resources, r.gateProtocol.modelRequests, r.gateProtocol.directories, r.gateProtocol.refreshCalls, len(r.gateProtocol.refreshes), r.gateProtocol.invalid)
	}
}

func TestSubscriptionGateRejectsOtherPurposesAndPhasesBeforeEffects(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	a, err := r.store.GetModelAccount(subscriptionGateAccount)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []domain.ModelAcceptanceProposal{
		{Phase: "connection", RouteID: a.ID + "-route", ExpectedAccountRevision: a.Revision},
		{Phase: "connection", ConnectionPurpose: "identity_only", RouteID: a.ID + "-route", ExpectedAccountRevision: a.Revision},
		{Phase: "catalog", ConnectionPurpose: "subscription_only", RouteID: a.ID + "-route", ExpectedAccountRevision: a.Revision},
		{Phase: "inference", ConnectionPurpose: "subscription_only", RouteID: a.ID + "-route", ExpectedAccountRevision: a.Revision},
	} {
		if _, err = r.gates.Propose(a.ID, q); err == nil {
			t.Fatalf("unsafe proposal accepted: %+v", q)
		}
	}
	r.gateProtocol.mu.Lock()
	defer r.gateProtocol.mu.Unlock()
	if r.gateProtocol.tokenCalls != 0 || r.gateProtocol.modelRequests != 0 || len(r.gateProtocol.requests) != 0 {
		t.Fatal("denied proposal produced effects")
	}
}

func TestSubscriptionGateDenialIsTerminalWithoutTokenExchange(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	route, err := r.store.GetModelRoute(subscriptionGateAccount + "-route")
	if err != nil {
		t.Fatal(err)
	}
	r.gates = acceptance.NewSimulation(r.store, acceptance.Options{Routes: []domain.ModelRoute{route}, SubscriptionConnectionRouteIDs: []string{route.ID}, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(ctx context.Context, m domain.ModelAcceptanceMaterial) error {
		if err := completeOwnedSubscriptionConnectionAuthorization(ctx, r.gateSIWC, m.Account, "decline"); err != nil {
			return err
		}
		return &model.ProviderError{Code: "entitlement_denied", Certainty: "rejected"}
	}})
	if err = r.gates.Recover(); err != nil {
		t.Fatal(err)
	}
	o := subscriptionGateProposal(t, r)
	o, err = r.gates.Decide(subscriptionGateAccount, o.ID, o.Revision, o.MaterialDigest, "approve")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.gates.Execute(context.Background(), subscriptionGateAccount, o.ID, o.Revision, o.MaterialDigest)
	if err != nil || got.State != "failed" || got.Certainty != "rejected" || got.Code != "entitlement_denied" || got.Attempts != 1 {
		t.Fatalf("dishonest denial: %+v %v", got, err)
	}
	if _, err = r.gates.Execute(context.Background(), subscriptionGateAccount, o.ID, got.Revision, got.MaterialDigest); err == nil {
		t.Fatal("denied connection replayed")
	}
	r.gateProtocol.mu.Lock()
	defer r.gateProtocol.mu.Unlock()
	if r.gateProtocol.tokenCalls != 0 || r.gateProtocol.refreshCalls != 0 || r.gateProtocol.modelRequests != 0 {
		t.Fatal("denial reached a disallowed exchange")
	}
}

func TestSubscriptionGateLostFinishRestartsUnknownWithoutReplay(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	o := subscriptionGateProposal(t, r)
	var err error
	o, err = r.gates.Decide(subscriptionGateAccount, o.ID, o.Revision, o.MaterialDigest, "approve")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.Root, "foundry.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER subscription_finish_fault BEFORE UPDATE ON model_acceptance_operations WHEN OLD.state='running' AND NEW.state!='running' BEGIN SELECT RAISE(ABORT,'SYNTHETIC-PRIVATE-FAULT'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.gates.Execute(context.Background(), subscriptionGateAccount, o.ID, o.Revision, o.MaterialDigest); err != acceptance.ErrPersistence {
		t.Fatalf("finish fault=%v", err)
	}
	consumed, err := r.gates.Get(subscriptionGateAccount, o.ID)
	if err != nil || consumed.State != "running" || consumed.Attempts != 1 || consumed.ClaimSequence < 1 || r.gateProtocol.tokenCount() != 1 {
		t.Fatalf("lost finish was not consumed: %+v %v", consumed, err)
	}
	if _, err = db.Exec(`DROP TRIGGER subscription_finish_fault`); err != nil {
		t.Fatal(err)
	}
	if err = r.store.Close(); err != nil {
		t.Fatal(err)
	}
	r.store, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.store.Close()
	route, err := r.store.GetModelRoute(subscriptionGateAccount + "-route")
	if err != nil {
		t.Fatal(err)
	}
	var replays atomic.Int32
	r.gates = acceptance.NewSimulation(r.store, acceptance.Options{Routes: []domain.ModelRoute{route}, SubscriptionConnectionRouteIDs: []string{route.ID}, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(context.Context, domain.ModelAcceptanceMaterial) error { replays.Add(1); return nil }})
	if err = r.gates.Recover(); err != nil {
		t.Fatal(err)
	}
	recovered, err := r.gates.Get(subscriptionGateAccount, o.ID)
	if err != nil || recovered.State != "unknown" || recovered.Certainty != "unknown" || recovered.Attempts != 1 || recovered.ClaimSequence != consumed.ClaimSequence {
		t.Fatalf("restart evidence=%+v %v", recovered, err)
	}
	if _, err = r.gates.Execute(context.Background(), subscriptionGateAccount, o.ID, recovered.Revision, recovered.MaterialDigest); err == nil || replays.Load() != 0 || r.gateProtocol.tokenCount() != 1 {
		t.Fatal("restart replayed subscription authorization")
	}
}

func TestSubscriptionGateCancellationAndTimeoutConsumeWithoutReplay(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			r := startProtocolAcceptanceForTest(t)
			entered, canceled := make(chan struct{}), make(chan struct{})
			var tokenCalls atomic.Int32
			token := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
				if err := req.ParseForm(); err != nil {
					t.Error(err)
					return
				}
				if req.PostForm.Get("resource") != domain.OpenAISubscriptionRequestedResource || req.PostForm.Get("grant_type") != "authorization_code" {
					t.Errorf("subscription token contract=%v", req.PostForm)
				}
				tokenCalls.Add(1)
				close(entered)
				<-req.Context().Done()
				close(canceled)
			}))
			defer token.Close()
			options := r.gateProtocol.siwcOptions()
			options.TokenOrigin = token.URL
			_ = r.gateSIWC.Close()
			var err error
			r.gateSIWC, err = accounts.NewSIWCSimulation(accounts.New(r.store, &ephemeralSecrets{values: map[string]string{}}), options)
			if err != nil {
				t.Fatal(err)
			}
			o := subscriptionGateProposal(t, r)
			o, err = r.gates.Decide(subscriptionGateAccount, o.ID, o.Revision, o.MaterialDigest, "approve")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "timeout" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
			}
			defer cancel()
			done := make(chan domain.ModelAcceptanceOperation, 1)
			go func() {
				got, _ := r.gates.Execute(ctx, subscriptionGateAccount, o.ID, o.Revision, o.MaterialDigest)
				done <- got
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("subscription token exchange not reached")
			}
			if mode == "cancel" {
				cancel()
			}
			var got domain.ModelAcceptanceOperation
			select {
			case got = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("subscription cancellation did not terminate")
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("subscription gate returned before token request canceled")
			}
			assertCanceledGateConnection(t, r, o, got)
			r.gateProtocol.mu.Lock()
			defer r.gateProtocol.mu.Unlock()
			if tokenCalls.Load() != 1 || r.gateProtocol.refreshCalls != 0 || r.gateProtocol.modelRequests != 0 || r.gateProtocol.directories != 0 {
				t.Fatal("canceled subscription continued to downstream effects")
			}
		})
	}
}
