package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/acceptance"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const handoffAccount = "acceptance-gate-openai-handoff"

func TestBrowserHandoffRequiresIndependentApprovalAndReturnsRunning(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	a, err := r.store.GetModelAccount(handoffAccount)
	if err != nil {
		t.Fatalf("browser handoff fixture missing: %v", err)
	}
	o, err := r.gates.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: a.ID + "-route", ExpectedAccountRevision: a.Revision})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(o.Material)
	var material map[string]any
	_ = json.Unmarshal(raw, &material)
	if material["connection_mode"] != "browser_handoff" || material["login_timeout_seconds"] != float64(600) || material["deadline_seconds"] != float64(30) {
		t.Fatalf("handoff material=%s", raw)
	}
	if _, err = r.gates.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); err == nil {
		t.Fatal("pending operation executed")
	}
	o, err = r.gates.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
	if err != nil {
		t.Fatal(err)
	}
	if r.gateProtocol.tokenCount() != 0 {
		t.Fatal("approval exchanged code")
	}
	ctx, disconnect := context.WithCancel(context.Background())
	o, err = r.gates.Execute(ctx, a.ID, o.ID, o.Revision, o.MaterialDigest)
	disconnect()
	if err != nil || o.State != "running" || o.Attempts != 1 {
		t.Fatalf("execute did not hand off: %+v %v", o, err)
	}
	if _, err = r.gates.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); err == nil {
		t.Fatal("replay accepted")
	}
	_, session, err := r.gates.Read(a.ID, o.ID)
	if err != nil || session == nil || session.State != "waiting" || !literal127URL(session.HandoffURL, "/auth/start") {
		t.Fatalf("request disconnect canceled session: %+v %v", session, err)
	}
	expires, _ := time.Parse(time.RFC3339Nano, session.ExpiresAt)
	if lifetime := time.Until(expires); lifetime < 598*time.Second || lifetime > 600*time.Second {
		t.Fatalf("wrong human lifetime: %v", lifetime)
	}
}

func beginHandoff(t *testing.T, r *acceptanceRuntime) domain.ModelAcceptanceOperation {
	t.Helper()
	a, err := r.store.GetModelAccount(handoffAccount)
	if err != nil {
		t.Fatal(err)
	}
	o, err := r.gates.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: a.ID + "-route", ExpectedAccountRevision: a.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, session, err := r.gates.Read(a.ID, o.ID); err != nil || session != nil {
		t.Fatal("proposal started session")
	}
	o, err = r.gates.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
	if err != nil {
		t.Fatal(err)
	}
	if _, session, err := r.gates.Read(a.ID, o.ID); err != nil || session != nil {
		t.Fatal("approval started session")
	}
	o, err = r.gates.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
	if err != nil || o.State != "running" {
		t.Fatalf("execute=%s %v", o.State, err)
	}
	return o
}

// Human browser actions are driven only by the test, never the controller.
func handoffCallback(t *testing.T, r *acceptanceRuntime, o domain.ModelAcceptanceOperation, outcome string) string {
	t.Helper()
	_, session, err := r.gates.Read(handoffAccount, o.ID)
	if err != nil || session == nil {
		t.Fatal("missing session", err)
	}
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(session.HandoffURL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	authorization := response.Header.Get("Location")
	response, err = client.Get(authorization)
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	action := htmlFormAction(string(page), outcome)
	if action == "" {
		t.Fatal("missing synthetic consent action")
	}
	u, _ := url.Parse(authorization)
	u.Path, u.RawQuery = action, ""
	response, err = client.Post(u.String(), "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.Header.Get("Location")
}

func TestBrowserHandoffConsentDeclineAndCancelAreSingleAttempt(t *testing.T) {
	for _, outcome := range []string{"approve", "decline", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			r := startProtocolAcceptanceForTest(t)
			o := beginHandoff(t, r)
			_, session, _ := r.gates.Read(handoffAccount, o.ID)
			handoff := session.HandoffURL
			for _, bad := range []struct {
				account  string
				revision int64
				digest   string
			}{{subscriptionGateAccount, o.Revision, o.MaterialDigest}, {handoffAccount, o.Revision - 1, o.MaterialDigest}, {handoffAccount, o.Revision, strings.Repeat("a", 64)}} {
				if _, err := r.gates.Cancel(bad.account, o.ID, bad.revision, bad.digest); err == nil {
					t.Fatal("forged cancellation accepted")
				}
			}
			if outcome == "cancel" {
				var err error
				o, err = r.gates.Cancel(handoffAccount, o.ID, o.Revision, o.MaterialDigest)
				if err != nil || o.State != "unknown" {
					t.Fatalf("cancel=%s %v", o.State, err)
				}
			} else {
				callback := handoffCallback(t, r, o, outcome)
				response, err := http.Get(callback)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != 200 {
					t.Fatalf("callback=%d", response.StatusCode)
				}
				// Close joins the already completed protocol and its receipt writer.
				if err = r.gates.Close(); err != nil {
					t.Fatal(err)
				}
				o, err = r.gates.Get(handoffAccount, o.ID)
				if err != nil {
					t.Fatal(err)
				}
				want := "succeeded"
				if outcome == "decline" {
					want = "failed"
				}
				if o.State != want {
					t.Fatalf("receipt=%+v", o)
				}
				if response, err = http.Get(callback); err == nil {
					response.Body.Close()
					if response.StatusCode == 200 {
						t.Fatal("duplicate callback accepted")
					}
				}
			}
			if _, session, err := r.gates.Read(handoffAccount, o.ID); err != nil || session != nil {
				t.Fatal("terminal session retained handoff")
			}
			if _, err := r.gates.Cancel(handoffAccount, o.ID, o.Revision, o.MaterialDigest); err == nil {
				t.Fatal("cancel replay accepted")
			}
			if response, err := http.Get(handoff); err == nil {
				response.Body.Close()
				if response.StatusCode == 302 {
					t.Fatal("terminal handoff still usable")
				}
			}
			proof, err := r.store.CurrentModelAuthorization(handoffAccount)
			if outcome == "approve" && (err != nil || proof.State != "available" || proof.Binding.SpendClass != "subscription_only") {
				t.Fatal("fresh subscription proof missing", err)
			}
			if outcome != "approve" && err == nil {
				t.Fatal("non-consent persisted proof")
			}
			r.gateProtocol.mu.Lock()
			defer r.gateProtocol.mu.Unlock()
			wantTokens := 0
			if outcome == "approve" {
				wantTokens = 1
			}
			if r.gateProtocol.tokenCalls != wantTokens || r.gateProtocol.modelRequests != 0 || r.gateProtocol.refreshCalls != 0 {
				t.Fatal("unexpected provider effects")
			}
		})
	}
}

func TestBrowserHandoffCancelJoinsBlockedExchange(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	entered, disconnected, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = req.ParseForm()
		close(entered)
		select {
		case <-req.Context().Done():
			close(disconnected)
		case <-release:
			r.gateProtocol.handleToken(w, req)
		}
	}))
	defer token.Close()
	defer once.Do(func() { close(release) })
	opts := r.gateProtocol.siwcOptions()
	opts.FlowTimeout, opts.TokenOrigin = 10*time.Minute, token.URL
	_ = r.gates.Close()
	_ = r.browserSIWC.Close()
	var err error
	r.browserSIWC, err = accounts.NewSIWCSimulation(accounts.New(r.store, &ephemeralSecrets{values: map[string]string{}}), opts)
	if err != nil {
		t.Fatal(err)
	}
	route, _ := r.store.GetModelRoute(handoffAccount + "-route")
	r.gates = acceptance.NewSimulation(r.store, acceptance.Options{Routes: []domain.ModelRoute{route}, SubscriptionConnectionRouteIDs: []string{route.ID}, BrowserHandoffRouteIDs: []string{route.ID}, BrowserSIWC: r.browserSIWC, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(context.Context, domain.ModelAcceptanceMaterial) error {
		t.Error("legacy connector called")
		return nil
	}})
	if err = r.gates.Recover(); err != nil {
		t.Fatal(err)
	}
	o := beginHandoff(t, r)
	callback := handoffCallback(t, r, o, "approve")
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := http.Get(callback)
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("exchange not reached")
	}
	o, err = r.gates.Cancel(handoffAccount, o.ID, o.Revision, o.MaterialDigest)
	if err != nil || o.State != "unknown" {
		t.Fatalf("cancel=%+v %v", o, err)
	}
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("cancel returned with exchange active")
	}
	<-done
	if _, err = r.store.CurrentModelAuthorization(handoffAccount); err == nil {
		t.Fatal("late proof committed")
	}
}

func TestBrowserHandoffConcurrentCancelHasOneWinner(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	o := beginHandoff(t, r)
	var winners atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := r.gates.Cancel(handoffAccount, o.ID, o.Revision, o.MaterialDigest); err == nil {
				winners.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("cancellation replay admitted %d winners", winners.Load())
	}
}

func TestBrowserHandoffReceiptFailureClosesGateAndRecoveryConsumes(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	o := beginHandoff(t, r)
	db, err := sql.Open("sqlite3", filepath.Join(r.Root, "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER handoff_finish_fault BEFORE UPDATE ON model_acceptance_operations WHEN OLD.state='running' AND NEW.state!='running' BEGIN SELECT RAISE(ABORT,'synthetic fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.gates.Cancel(handoffAccount, o.ID, o.Revision, o.MaterialDigest); err != acceptance.ErrPersistence {
		t.Fatalf("receipt failure=%v", err)
	}
	if r.gates.SimulationEligible(handoffAccount) {
		t.Fatal("failed receipt left mutation gate open")
	}
	got, session, err := r.gates.Read(handoffAccount, o.ID)
	if err != nil || got.State != "running" || got.Attempts != 1 || session != nil {
		t.Fatal("failed receipt restored handoff")
	}
	if _, err = db.Exec(`DROP TRIGGER handoff_finish_fault`); err != nil {
		t.Fatal(err)
	}
	if err = r.gates.Recover(); err != nil {
		t.Fatal(err)
	}
	got, session, err = r.gates.Read(handoffAccount, o.ID)
	if err != nil || got.State != "unknown" || got.Attempts != 1 || got.ClaimSequence != o.ClaimSequence || session != nil {
		t.Fatal("recovery lost consumed evidence")
	}
	if _, err = r.gates.Execute(context.Background(), handoffAccount, o.ID, got.Revision, got.MaterialDigest); err == nil {
		t.Fatal("recovery restarted OAuth")
	}
	if r.gateProtocol.tokenCount() != 0 {
		t.Fatal("recovery exchanged a token")
	}
}

type failingHandoffSecrets struct{}

func (failingHandoffSecrets) Get(string) (string, error) {
	return "", errors.New("synthetic unavailable")
}
func (failingHandoffSecrets) Set(string, string) error { return errors.New("synthetic unavailable") }
func (failingHandoffSecrets) Delete(string) error      { return errors.New("synthetic cleanup unavailable") }

func TestBrowserHandoffCredentialFailureRetainsCleanupWithoutSuccess(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	_ = r.gates.Close()
	_ = r.browserSIWC.Close()
	options := r.gateProtocol.siwcOptions()
	options.FlowTimeout = 10 * time.Minute
	var err error
	r.browserSIWC, err = accounts.NewSIWCSimulation(accounts.New(r.store, failingHandoffSecrets{}), options)
	if err != nil {
		t.Fatal(err)
	}
	route, _ := r.store.GetModelRoute(handoffAccount + "-route")
	r.gates = acceptance.NewSimulation(r.store, acceptance.Options{Routes: []domain.ModelRoute{route}, SubscriptionConnectionRouteIDs: []string{route.ID}, BrowserHandoffRouteIDs: []string{route.ID}, BrowserSIWC: r.browserSIWC, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(context.Context, domain.ModelAcceptanceMaterial) error { return nil }})
	if err = r.gates.Recover(); err != nil {
		t.Fatal(err)
	}
	o := beginHandoff(t, r)
	response, err := http.Get(handoffCallback(t, r, o, "approve"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == 200 {
		t.Fatal("credential failure reported callback success")
	}
	_ = r.gates.Close()
	got, session, err := r.gates.Read(handoffAccount, o.ID)
	if err != nil || got.State != "unknown" || got.Attempts != 1 || session != nil {
		t.Fatalf("credential failure receipt=%+v %v", got, err)
	}
	if _, err = r.store.CurrentModelAuthorization(handoffAccount); err == nil {
		t.Fatal("incomplete credential write reported authorization")
	}
	pending, err := r.store.PendingModelAuthorizationCleanup()
	if err != nil || len(pending) != 1 {
		t.Fatal("lost recoverable credential cleanup intent", err)
	}
	if r.gateProtocol.tokenCount() != 1 {
		t.Fatal("credential failure retried exchange")
	}
}
