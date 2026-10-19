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

const identityGateAccount = "acceptance-gate-openai-identity"

func identityGateProposal(t *testing.T, r *acceptanceRuntime) domain.ModelAcceptanceOperation {
	t.Helper()
	a, err := r.store.GetModelAccount(identityGateAccount)
	if err != nil {
		t.Fatalf("dedicated identity account unavailable: %v", err)
	}
	raw, _ := json.Marshal(map[string]any{"phase": "connection", "route_id": a.ID + "-route", "expected_account_revision": a.Revision, "connection_purpose": "identity_only"})
	var q domain.ModelAcceptanceProposal
	_ = json.Unmarshal(raw, &q)
	o, err := r.gates.Propose(a.ID, q)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// Removing purpose propagation must fail at the owned issuer, not silently mint
// a model credential; dropping the durable claim must permit duplicate calls.
func TestIdentityGateSeparateApprovalConcurrentExecutionAndDownstreamDenial(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	secrets := &identityGateSecrets{ephemeralSecrets: ephemeralSecrets{values: map[string]string{}}}
	service := accounts.New(r.store, secrets)
	_ = r.gateSIWC.Close()
	var setupErr error
	r.gateSIWC, setupErr = accounts.NewSIWCSimulation(service, r.gateProtocol.siwcOptions())
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	o := identityGateProposal(t, r)
	raw, _ := json.Marshal(o.Material)
	if !strings.Contains(string(raw), `"connection_purpose":"identity_only"`) || !strings.Contains(string(raw), `"requested_scopes":["openid","profile","email"]`) {
		t.Fatalf("identity purpose absent from immutable approval: %s", raw)
	}
	if _, err := r.gates.Execute(context.Background(), identityGateAccount, o.ID, o.Revision, o.MaterialDigest); err == nil {
		t.Fatal("unapproved identity execution")
	}
	if _, err := r.gates.Decide(identityGateAccount, o.ID, o.Revision, strings.Repeat("0", 64), "approve"); err == nil {
		t.Fatal("wrong digest approved")
	}
	var err error
	o, err = r.gates.Decide(identityGateAccount, o.ID, o.Revision, o.MaterialDigest, "approve")
	if err != nil {
		t.Fatal(err)
	}
	r.gateProtocol.mu.Lock()
	inert := r.gateProtocol.tokenCalls == 0 && len(r.gateProtocol.requests) == 0 && r.gateProtocol.modelRequests == 0
	r.gateProtocol.mu.Unlock()
	if !inert || secrets.gets.Load() != 0 || secrets.sets.Load() != 0 {
		t.Fatal("approval contacted provider or secrets")
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, e := r.gates.Execute(context.Background(), identityGateAccount, o.ID, o.Revision, o.MaterialDigest); e == nil && got.State == "succeeded" {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	got, err := r.gates.Get(identityGateAccount, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wins.Load() != 1 || got.State != "succeeded" || got.Attempts != 1 || got.ClaimSequence < 1 || got.Quota != "unknown" || got.RemoteBilling != "unverified" || got.LiveAccess != "blocked" {
		t.Fatalf("unsafe identity receipt: wins=%d %+v", wins.Load(), got)
	}
	auth, err := r.store.CurrentModelAuthorization(identityGateAccount)
	if err != nil || auth.Binding.SpendClass != "identity_only" || !reflect.DeepEqual(auth.Binding.Scopes, []string{"openid"}) {
		t.Fatalf("identity evidence=%+v err=%v", auth, err)
	}
	a, _ := r.store.GetModelAccount(identityGateAccount)
	secretReads := secrets.gets.Load()
	secretWrites := secrets.sets.Load()
	for _, phase := range []string{"catalog", "inference"} {
		if _, err = r.gates.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: phase, RouteID: a.ID + "-route", ExpectedAccountRevision: a.Revision, PrerequisiteID: got.ID}); err == nil {
			t.Fatalf("identity allowed %s", phase)
		}
	}
	if _, err = r.gateSIWC.Refresh(context.Background(), auth.Binding.ID, a.Revision); err == nil {
		t.Fatal("identity refreshed")
	}
	selection := got.Material.Selection()
	selection.Authorization = &auth.Binding
	if _, err = service.ResolveModelCredential(context.Background(), selection); err == nil {
		t.Fatal("identity leased model credentials")
	}
	if secrets.gets.Load() != secretReads || secrets.sets.Load() != secretWrites || secretWrites != 1 {
		t.Fatal("denied identity action touched secrets")
	}
	secrets.mu.Lock()
	proof := ""
	for _, value := range secrets.values {
		var bundle map[string]string
		if json.Unmarshal([]byte(value), &bundle) != nil {
			t.Fatal("invalid protected bundle")
		}
		if bundle["access_token"] != "" || bundle["refresh_token"] != "" || bundle["id_token"] == "" {
			t.Fatal("identity storage retained model credential")
		}
		proof = bundle["id_token"]
	}
	secrets.mu.Unlock()
	raw, _ = json.Marshal(got)
	if strings.Contains(string(raw), proof) || strings.Contains(string(raw), "secret_ref") || strings.Contains(string(raw), "handoff_url") {
		t.Fatal("secret escaped receipt")
	}
	db, e := sql.Open("sqlite3", filepath.Join(r.Root, "foundry.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var persisted string
	if e = db.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=?`, got.ID).Scan(&persisted); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(persisted, proof) {
		t.Fatal("identity proof escaped protected storage")
	}
	r.gateProtocol.mu.Lock()
	defer r.gateProtocol.mu.Unlock()
	if r.gateProtocol.tokenCalls != 1 || r.gateProtocol.modelRequests != 0 || r.gateProtocol.directories != 0 || len(r.gateProtocol.modelHits) != 0 || len(r.gateProtocol.tokens) != 0 || len(r.gateProtocol.refreshes) != 0 || r.gateProtocol.invalid != "" {
		t.Fatal("identity path minted model credentials or contacted downstream endpoint")
	}
}

func TestIdentityFixtureCountsRejectedModelRequests(t *testing.T) {
	f, e := newProtocolFixture()
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	response, e := http.Get(f.modelBaseURL() + "/models")
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("unauthorized model boundary accepted")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.modelRequests != 1 || f.directories != 0 {
		t.Fatal("endpoint counter hid rejected transport")
	}
}

type identityGateSecrets struct {
	ephemeralSecrets
	gets, sets atomic.Int32
}

func (s *identityGateSecrets) Get(id string) (string, error) {
	s.gets.Add(1)
	return s.ephemeralSecrets.Get(id)
}
func (s *identityGateSecrets) Set(id, value string) error {
	s.sets.Add(1)
	return s.ephemeralSecrets.Set(id, value)
}

func approvedIdentityGate(t *testing.T, r *acceptanceRuntime) domain.ModelAcceptanceOperation {
	t.Helper()
	o := identityGateProposal(t, r)
	o, e := r.gates.Decide(identityGateAccount, o.ID, o.Revision, o.MaterialDigest, "approve")
	if e != nil {
		t.Fatal(e)
	}
	return o
}

func TestIdentityGateLostFinishRestartUnknownWithoutReplay(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	o := approvedIdentityGate(t, r)
	path := filepath.Join(r.Root, "foundry.db")
	db, e := sql.Open("sqlite3", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`CREATE TRIGGER identity_finish_fault BEFORE UPDATE ON model_acceptance_operations WHEN OLD.state='running' AND NEW.state!='running' BEGIN SELECT RAISE(ABORT,'SYNTHETIC-PRIVATE-FAULT'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = r.gates.Execute(context.Background(), identityGateAccount, o.ID, o.Revision, o.MaterialDigest); e != acceptance.ErrPersistence {
		t.Fatalf("finish fault=%v", e)
	}
	got, e := r.gates.Get(identityGateAccount, o.ID)
	if e != nil || got.State != "running" || got.Attempts != 1 || r.gateProtocol.tokenCount() != 1 || r.gates.SimulationEligible(identityGateAccount) {
		t.Fatal("lost finish was not durably consumed")
	}
	if _, e = db.Exec(`DROP TRIGGER identity_finish_fault`); e != nil {
		t.Fatal(e)
	}
	if e = r.store.Close(); e != nil {
		t.Fatal(e)
	}
	r.store, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer r.store.Close()
	route, e := r.store.GetModelRoute(identityGateAccount + "-route")
	if e != nil {
		t.Fatal(e)
	}
	var replays atomic.Int32
	r.gates = acceptance.NewSimulation(r.store, acceptance.Options{Routes: []domain.ModelRoute{route}, IdentityOnlyRouteIDs: []string{route.ID}, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Connect: func(context.Context, domain.ModelAcceptanceMaterial) error { replays.Add(1); return nil }})
	if e = r.gates.Recover(); e != nil {
		t.Fatal(e)
	}
	recovered, e := r.gates.Get(identityGateAccount, o.ID)
	if e != nil || recovered.State != "unknown" || recovered.ClaimSequence != got.ClaimSequence || recovered.Attempts != 1 {
		t.Fatal("restart lost consumed unknown")
	}
	if _, e = r.gates.Execute(context.Background(), identityGateAccount, o.ID, recovered.Revision, recovered.MaterialDigest); e == nil || replays.Load() != 0 || r.gateProtocol.tokenCount() != 1 {
		t.Fatal("recovery replayed identity authorization")
	}
}

func TestIdentityGateDeadlineStopsOwnedCallback(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	entered, canceled := make(chan struct{}), make(chan struct{})
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if e := req.ParseForm(); e != nil {
			return
		}
		close(entered)
		<-req.Context().Done()
		close(canceled)
	}))
	defer token.Close()
	secrets := &identityGateSecrets{ephemeralSecrets: ephemeralSecrets{values: map[string]string{}}}
	opts := r.gateProtocol.siwcOptions()
	opts.TokenOrigin = token.URL
	_ = r.gateSIWC.Close()
	var e error
	r.gateSIWC, e = accounts.NewSIWCSimulation(accounts.New(r.store, secrets), opts)
	if e != nil {
		t.Fatal(e)
	}
	o := approvedIdentityGate(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	got, e := r.gates.Execute(ctx, identityGateAccount, o.ID, o.Revision, o.MaterialDigest)
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	default:
		t.Fatal("identity token endpoint not reached")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("owned callback still active after deadline")
	}
	assertCanceledGateConnection(t, r, o, got)
	if secrets.sets.Load() != 0 || secrets.gets.Load() != 0 {
		t.Fatal("canceled identity request touched secrets")
	}
}

func TestIdentityGateCancellationJoinsProofWrite(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	secrets := &blockedGateSecrets{ephemeralSecrets: ephemeralSecrets{values: map[string]string{}}, entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	unblock := func() { once.Do(func() { close(secrets.release) }) }
	defer unblock()
	_ = r.gateSIWC.Close()
	var e error
	r.gateSIWC, e = accounts.NewSIWCSimulation(accounts.New(r.store, secrets), r.gateProtocol.siwcOptions())
	if e != nil {
		t.Fatal(e)
	}
	o := approvedIdentityGate(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan domain.ModelAcceptanceOperation, 1)
	go func() {
		got, _ := r.gates.Execute(ctx, identityGateAccount, o.ID, o.Revision, o.MaterialDigest)
		done <- got
	}()
	select {
	case <-secrets.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("identity proof write not reached")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("identity gate returned before proof write joined")
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	select {
	case got := <-done:
		assertCanceledGateConnection(t, r, o, got)
	case <-time.After(3 * time.Second):
		t.Fatal("identity proof write not joined")
	}
	secrets.mu.Lock()
	defer secrets.mu.Unlock()
	if len(secrets.values) != 0 {
		t.Fatal("canceled proof not cleaned by exact reference")
	}
}

func TestIdentityGateRejectsBroaderResponseBeforePersistence(t *testing.T) {
	for _, field := range []string{"refresh_token", "scope"} {
		t.Run(field, func(t *testing.T) {
			r := startProtocolAcceptanceForTest(t)
			var calls atomic.Int32
			token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				rec := httptest.NewRecorder()
				r.gateProtocol.handleToken(rec, req)
				var payload map[string]any
				if json.Unmarshal(rec.Body.Bytes(), &payload) != nil {
					t.Error("invalid fixture response")
					w.WriteHeader(500)
					return
				}
				if field == "scope" {
					payload[field] = "openid offline_access"
				} else {
					payload[field] = "SYNTHETIC-BROADENED-SECRET"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(payload)
			}))
			defer token.Close()
			secrets := &identityGateSecrets{ephemeralSecrets: ephemeralSecrets{values: map[string]string{}}}
			opts := r.gateProtocol.siwcOptions()
			opts.TokenOrigin = token.URL
			_ = r.gateSIWC.Close()
			var e error
			r.gateSIWC, e = accounts.NewSIWCSimulation(accounts.New(r.store, secrets), opts)
			if e != nil {
				t.Fatal(e)
			}
			o := approvedIdentityGate(t, r)
			got, e := r.gates.Execute(context.Background(), identityGateAccount, o.ID, o.Revision, o.MaterialDigest)
			if e != nil || got.State == "succeeded" || got.Attempts != 1 || calls.Load() != 1 || secrets.sets.Load() != 0 || secrets.gets.Load() != 0 {
				t.Fatalf("broader grant persisted: %+v %v", got, e)
			}
			if _, e = r.store.CurrentModelAuthorization(identityGateAccount); e == nil {
				t.Fatal("broadened response created authorization")
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "SYNTHETIC-BROADENED-SECRET") {
				t.Fatal("provider response leaked")
			}
		})
	}
}

func TestIdentityGateExplicitFreshReauthentication(t *testing.T) {
	for _, prior := range []string{"available", "expired", "revoked"} {
		t.Run(prior, func(t *testing.T) {
			r := startProtocolAcceptanceForTest(t)
			o := approvedIdentityGate(t, r)
			got, e := r.gates.Execute(context.Background(), identityGateAccount, o.ID, o.Revision, o.MaterialDigest)
			if e != nil || got.State != "succeeded" {
				t.Fatalf("initial identity=%+v %v", got, e)
			}
			auth, e := r.store.CurrentModelAuthorization(identityGateAccount)
			if e != nil {
				t.Fatal(e)
			}
			if prior == "expired" {
				// Expiry is signed at issuance; advance the acceptance clock, not the proof.
				expiry, e := time.Parse(time.RFC3339, auth.ExpiresAt)
				if e != nil {
					t.Fatal(e)
				}
				route, _ := r.store.GetModelRoute(identityGateAccount + "-route")
				r.gates = acceptance.NewSimulation(r.store, acceptance.Options{Routes: []domain.ModelRoute{route}, IdentityOnlyRouteIDs: []string{route.ID}, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Now: func() time.Time { return expiry.Add(time.Minute) }, Connect: func(ctx context.Context, m domain.ModelAcceptanceMaterial) error {
					return completeOwnedIdentityFixtureAuthorization(ctx, r.gateSIWC, m.Account, "identity")
				}})
				if e = r.gates.Recover(); e != nil {
					t.Fatal(e)
				}
			}
			if prior == "revoked" {
				revoked, _ := r.gateSIWC.Revoke(context.Background(), auth.Binding.ID, 1)
				if revoked.LocalState != "revoked" {
					t.Fatal("identity local revocation failed")
				}
			}
			next := approvedIdentityGate(t, r)
			if next.Material.Authorization == nil || next.Material.Authorization.Binding.ID != auth.Binding.ID {
				t.Fatal("reauth approval lost prior immutable metadata")
			}
			if prior == "expired" {
				// No new signed proof may complete against a future clock beyond its expiry.
				got, e = r.gates.Execute(context.Background(), identityGateAccount, next.ID, next.Revision, next.MaterialDigest)
				if e != acceptance.ErrPersistence || got.State == "succeeded" {
					t.Fatal("expired proof accepted as successful identity")
				}
			} else {
				got, e = r.gates.Execute(context.Background(), identityGateAccount, next.ID, next.Revision, next.MaterialDigest)
				if e != nil || got.State != "succeeded" {
					t.Fatalf("fresh explicit identity=%+v %v", got, e)
				}
				latest, e := r.store.CurrentModelAuthorization(identityGateAccount)
				if e != nil || latest.Binding.ID == auth.Binding.ID {
					t.Fatal("reauth did not create fresh identity generation")
				}
			}
			if r.gateProtocol.tokenCount() != 2 {
				t.Fatal("fresh approval did not dispatch exactly one new code exchange")
			}
		})
	}
}
