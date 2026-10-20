package accounts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/store"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func productionAdapter(t *testing.T, ready func() bool) (*SIWCService, *store.Store, *memorySecrets, *ProductionConnectionAdmission, domain.ModelAccount, domain.ModelRoute) {
	t.Helper()
	lifecycle, db, secrets, _ := setupAccounts(t)
	a := newSIWCAccount(t, lifecycle, "https://api.openai.com/v1")
	r, err := db.CreateModelRoute(domain.ModelRoute{ID: "production-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSIWCService(lifecycle)
	t.Cleanup(func() { _ = s.Close() })
	s.http.Transport = subscriptionRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected network")
		return nil, errors.New("offline")
	})
	return s, db, secrets, NewProductionConnectionAdmission(s, db, r, ready), a, r
}

func productionClaim(t *testing.T, db *store.Store, p *ProductionConnectionAdmission, a domain.ModelAccount, r domain.ModelRoute) domain.ModelAcceptanceOperation {
	t.Helper()
	o, err := db.ProposeProductionConnection(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, p.Metadata(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err = db.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err = db.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestProductionAdapterRejectsReadinessLossAndStaleGenerationBeforeEffects(t *testing.T) {
	for _, failure := range []string{"missing readiness", "lost readiness", "readiness lost after reservation", "stale generation", "wrong revision", "wrong scope", "recovered", "expired"} {
		t.Run(failure, func(t *testing.T) {
			var calls atomic.Int32
			ready := func() bool { return calls.Add(1) < 2 }
			if failure != "readiness lost after reservation" {
				ready = func() bool { return true }
			}
			s, db, secrets, p, a, r := productionAdapter(t, ready)
			o := productionClaim(t, db, p, a, r)
			switch failure {
			case "missing readiness":
				p = NewProductionConnectionAdmission(s, db, r, nil)
			case "lost readiness":
				p.credentialReady = func() bool { return false }
			case "stale generation":
				p = NewProductionConnectionAdmission(s, db, r, func() bool { return true })
			case "wrong revision":
				o.Revision++
			case "wrong scope":
				o.Material.RequestedScopes = []string{"openid"}
			case "recovered":
				if err := db.RecoverModelAcceptance(time.Now()); err != nil {
					t.Fatal(err)
				}
			case "expired":
				o.StartedAt = time.Now().Add(-601 * time.Second).Format(time.RFC3339Nano)
			}
			if _, stop, err := p.Begin(context.Background(), o); err == nil || stop != nil {
				t.Fatal("invalid adapter start accepted")
			}
			if len(s.flows) != 0 || secrets.sets+secrets.gets+secrets.deletes != 0 {
				t.Fatal("denial performed effects")
			}
			if failure == "readiness lost after reservation" {
				p.credentialReady = func() bool { return true }
				if _, stop, err := p.Begin(context.Background(), o); err == nil || stop != nil {
					t.Fatal("consumed reservation retried")
				}
			}
		})
	}
}

func TestProductionAdapterDurableDispatchWinsOnceAndCancellationJoins(t *testing.T) {
	s, db, secrets, p, a, r := productionAdapter(t, func() bool { return true })
	o := productionClaim(t, db, p, a, r)
	var wg sync.WaitGroup
	flows := make(chan SIWCFlow, 8)
	stops := make(chan func(), 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			flow, stop, err := p.Begin(context.Background(), o)
			if err == nil {
				flows <- flow
				stops <- stop
			}
		}()
	}
	wg.Wait()
	if len(flows) != 1 || len(stops) != 1 {
		t.Fatalf("dispatch winners=%d", len(flows))
	}
	flow := <-flows
	expires, err := time.Parse(time.RFC3339, flow.ExpiresAt)
	if err != nil || time.Until(expires) < 598*time.Second || time.Until(expires) > 600*time.Second {
		t.Fatal("incorrect production login lifetime")
	}
	(<-stops)()
	terminal, err := s.Wait(flow.OperationID)
	if err != nil || terminal.State != "canceled" || terminal.HandoffURL != "" {
		t.Fatalf("cancel did not join: %+v %v", terminal, err)
	}
	if secrets.sets+secrets.gets+secrets.deletes != 0 {
		t.Fatal("cancel-before-callback touched credentials")
	}
	if err = db.RecoverModelAcceptance(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, stop, err := p.Begin(context.Background(), o); err == nil || stop != nil {
		t.Fatal("recovered adapter replayed")
	}
}

func TestProductionAdapterTimeoutAndOldSimulationClaimCannotReplay(t *testing.T) {
	s, db, secrets, p, a, r := productionAdapter(t, func() bool { return true })
	o := productionClaim(t, db, p, a, r)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	flow, stop, err := p.Begin(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	terminal, err := s.Wait(flow.OperationID)
	if err != nil || terminal.State != "expired" || terminal.HandoffURL != "" {
		t.Fatalf("timeout: %+v %v", terminal, err)
	}
	stop()
	if _, _, err = p.Begin(context.Background(), o); err == nil {
		t.Fatal("timed-out adapter replayed")
	}
	if secrets.sets+secrets.gets+secrets.deletes != 0 {
		t.Fatal("timeout touched credentials")
	}

	sim, err := db.SaveModelAccount(domain.ModelAccount{ID: "simulation", Name: "simulation", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "http://127.0.0.1:12345/v1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	simRoute, err := db.CreateModelRoute(domain.ModelRoute{ID: "simulation-route", AccountID: sim.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: sim.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := db.ProposeModelAcceptanceWithConnectionMode(sim.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: simRoute.ID, ExpectedAccountRevision: sim.Revision}, "browser_handoff", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	old, err = db.DecideModelAcceptance(sim.ID, old.ID, old.Revision, old.MaterialDigest, "approve", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	old, err = db.ClaimModelAcceptance(sim.ID, old.ID, old.Revision, old.MaterialDigest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.Begin(context.Background(), old); err == nil {
		t.Fatal("old simulation claim entered production adapter")
	}
	if err = db.ReserveProductionConnectionDispatch(old, p.Metadata(), time.Now()); err == nil {
		t.Fatal("simulation reserved a production dispatch")
	}
}

// This is the real admitted adapter and protocol core, with only the transport
// and credential device replaced. No request can reach the official endpoints.
func TestProductionAdmissionOfficialCoreOfflineSuccessAndCredentialFailure(t *testing.T) {
	for _, failure := range []string{"none", "credential write", "credential cleanup"} {
		t.Run(failure, func(t *testing.T) {
			s, db, secrets, p, a, r := productionAdapter(t, func() bool { return true })
			issuer := newSIWCIssuerFixture(t, a.BaseURL)
			if failure != "none" {
				secrets.setErr = errors.New("synthetic write failure")
			}
			if failure == "credential cleanup" {
				secrets.deleteErr = errors.New("synthetic cleanup failure")
			}
			var query url.Values
			var requests atomic.Int32
			s.http.Transport = subscriptionRoundTrip(func(req *http.Request) (*http.Response, error) {
				requests.Add(1)
				var body any
				switch req.Method + " " + req.URL.String() {
				case "POST https://auth.openai.com/api/accounts/oauth/token":
					_ = req.ParseForm()
					if req.PostForm.Get("resource") != "https://api.openai.com/v1" || req.PostForm.Get("code_verifier") == "" || req.PostForm.Get("client_id") != "oaiapp_synthetic" {
						t.Error("bad production token request")
					}
					body = map[string]any{"access_token": "SYNTHETIC-PROD-ACCESS", "refresh_token": "SYNTHETIC-PROD-REFRESH", "token_type": "Bearer", "expires_in": 3600, "scope": "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct", "id_token": issuer.signedToken(map[string]any{"alg": "RS256", "kid": "key-1", "typ": "JWT"}, map[string]any{"iss": "https://auth.openai.com", "aud": "oaiapp_synthetic", "sub": "synthetic-production", "nonce": query.Get("nonce"), "exp": time.Now().Add(time.Hour).Unix()})}
				case "GET https://auth.openai.com/.well-known/openid-configuration":
					body = map[string]string{"issuer": "https://auth.openai.com", "jwks_uri": "https://auth.openai.com/jwks"}
				case "GET https://auth.openai.com/jwks":
					body = map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "key-1", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(issuer.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(issuer.key.E)).Bytes())}}}
				default:
					t.Errorf("unexpected request %s", req.URL)
					return nil, errors.New("offline")
				}
				raw, _ := json.Marshal(body)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw))), Request: req}, nil
			})
			o := productionClaim(t, db, p, a, r)
			flow, stop, err := p.Begin(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			query = openHandoff(t, flow)
			if query.Get("scope") != "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct" || query.Get("resource") != "https://api.openai.com/v1" {
				t.Fatal("bad production authorization request")
			}
			resp, err := http.Get(query.Get("redirect_uri") + "?" + url.Values{"code": {"synthetic-code"}, "state": {query.Get("state")}, "client_id": {"oaiapp_synthetic"}}.Encode())
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			terminal, err := s.Wait(flow.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			stop()
			if requests.Load() != 3 {
				t.Fatalf("protocol requests=%d", requests.Load())
			}
			if failure == "none" {
				if terminal.State != "available" || resp.StatusCode != 200 {
					t.Fatalf("admitted core failed: %+v", terminal)
				}
				finished, err := db.FinishProductionConnection(o, "succeeded", "completed", "", time.Now())
				if err != nil || finished.Source != "production_connection" || finished.State != "succeeded" {
					t.Fatalf("receipt: %+v %v", finished, err)
				}
			} else {
				if terminal.State != "failed" || resp.StatusCode == 200 {
					t.Fatal("credential failure accepted")
				}
				current, _ := db.CurrentModelAuthorization(a.ID)
				if current.State == "available" {
					t.Fatal("failed credentials active")
				}
				pending, err := db.PendingModelAuthorizationCleanup()
				if err != nil || (failure == "credential cleanup") != (len(pending) > 0) {
					t.Fatalf("cleanup journal=%+v %v", pending, err)
				}
			}
			if _, _, err = p.Begin(context.Background(), o); err == nil {
				t.Fatal("terminal dispatch replayed")
			}
			if _, err = s.Begin(context.Background(), a.ID, a.Revision); err == nil {
				t.Fatal("public Begin enabled")
			}
			if _, err = s.Refresh(context.Background(), "forged", a.Revision); err == nil {
				t.Fatal("refresh enabled")
			}
		})
	}
}
