package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"foundry/internal/acceptance"
	"foundry/internal/domain"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Test-only split-layer browser harness. The production coordinator handles
// preparation/proposal/decisions. A deliberately SYNTHETIC execution adapter
// exercises durable claims/receipts; it is NOT an OAuth protocol test or an
// end-to-end production-provider acceptance. Real protocol tests stay in
// internal/accounts with the signed offline issuer and private transport shim.
// No fixture switches, transport overrides or adapters enter the shipped app.
func TestDesktopBrowserFixture(t *testing.T) {
	if os.Getenv("FOUNDRY_TEST_DESKTOP_BROWSER") != "1" {
		t.Skip("explicit local browser fixture only")
	}
	f, a, route, _ := operatorHTTPFixture(t)
	s := f.handler.(*server)
	a.Name = "SYNTHETIC OpenAI demo — no provider or Keychain"
	var err error
	a, err = f.store.SaveModelAccount(a, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	s.codingWorkbench = true
	s.webDir, err = filepath.Abs("../../web/dist")
	if err != nil {
		t.Fatal(err)
	}
	s.desktopAccounts = acceptance.NewDesktopCoordinator(f.store, acceptance.DesktopOptions{SIWC: s.siwc, Resolver: s.accounts, Probe: func() error { return nil }})
	defer s.desktopAccounts.Close()
	var mu sync.Mutex
	var pending *domain.ModelAcceptanceOperation
	var expires string
	localLogin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if pending == nil {
			w.WriteHeader(410)
			fmt.Fprint(w, "SYNTHETIC login is no longer waiting.")
			return
		}
		if r.Method == "GET" && r.URL.Path == "/auth/start" && r.URL.RawQuery == "" {
			fmt.Fprint(w, `<!doctype html><title>SYNTHETIC local consent</title><h1>SYNTHETIC ONLY — no real sign-in</h1><p>Local fixture consent. No credentials, provider calls, native store, quota or inference. This is a test adapter, not the signed SIWC protocol.</p><form action="/synthetic-consent" method="post"><button>Confirm synthetic account / workspace</button></form>`)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/synthetic-consent" {
			w.WriteHeader(404)
			return
		}
		claim := *pending
		binding := domain.ModelAuthorizationBinding{ID: "synthetic-browser-binding", AccountID: a.ID, Issuer: "https://auth.openai.com", Subject: "synthetic-local-only", ClientID: "oaiapp_synthetic", SpendClass: "subscription_only", Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}}
		_, e := f.store.BeginModelAuthorization(a.ID, "synthetic-browser-signin", "sign_in", binding.ID)
		if e == nil {
			_, e = f.store.CommitModelAuthorization("synthetic-browser-signin", binding, time.Now().Add(time.Hour).Format(time.RFC3339))
		}
		if e == nil {
			_, e = f.store.FinishProductionConnection(claim, "succeeded", "completed", "", time.Now())
		}
		if e != nil {
			w.WriteHeader(409)
			fmt.Fprint(w, "Synthetic fixture stopped; no retry.")
			return
		}
		pending = nil
		fmt.Fprint(w, "<h1>SYNTHETIC consent recorded</h1><p>Return to the workbench and explicitly read the connection status. No real authorization or model call occurred.</p>")
	}))
	defer localLogin.Close()
	var app *httptest.Server
	app = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Keep all normal session and same-origin protections for the adapter too.
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/model-accounts/"), "/")
		if strings.HasPrefix(r.URL.Path, "/api/model-accounts/") && len(parts) >= 3 && parts[1] == "acceptance-operations" && r.Host == s.host && s.authenticated(r) {
			if r.Method == "GET" && len(parts) == 3 {
				mu.Lock()
				defer mu.Unlock()
				o, e := f.store.GetModelAcceptance(parts[0], parts[2])
				if e != nil {
					writeAcceptanceError(w, e)
					return
				}
				var session *acceptance.ConnectionSession
				if pending != nil && pending.ID == o.ID && o.State == "running" {
					session = &acceptance.ConnectionSession{OperationID: o.ID, AccountID: o.AccountID, State: "waiting", ExpiresAt: expires, HandoffURL: localLogin.URL + "/auth/start"}
				}
				writeAcceptanceOperation(w, o, session)
				return
			}
			if r.Method == "POST" && len(parts) == 4 && parts[3] == "execute" && s.validMutation(r) {
				mu.Lock()
				defer mu.Unlock()
				var q struct {
					Revision int64  `json:"expected_revision"`
					Digest   string `json:"material_digest"`
				}
				if e := decodeAccountJSON(w, r, &q); e != nil {
					writeDecodeError(w, e)
					return
				}
				o, e := f.store.ClaimModelAcceptance(parts[0], parts[2], q.Revision, q.Digest, time.Now())
				if e != nil {
					writeAcceptanceError(w, e)
					return
				}
				if o.Source == "production_connection" {
					e = f.store.ReserveProductionConnectionDispatch(o, *o.Material.ProductionAdmission, time.Now())
					if e != nil {
						writeAcceptanceError(w, e)
						return
					}
					pending = &o
					expires = time.Now().Add(10 * time.Minute).Format(time.RFC3339Nano)
					writeAcceptanceOperation(w, o, &acceptance.ConnectionSession{OperationID: o.ID, AccountID: o.AccountID, State: "waiting", ExpiresAt: expires, HandoffURL: localLogin.URL + "/auth/start"})
					return
				}
				if o.Source == "production_catalog" {
					e = f.store.ReserveProductionCatalogDispatch(o, *o.Material.ProductionAdmission, time.Now())
					if e == nil {
						o, e = f.store.FinishProductionCatalog(o, "succeeded", "completed", "", []domain.ModelAcceptanceCatalogEntry{{ID: "synthetic-model", DisplayName: "SYNTHETIC model — no inference"}}, time.Now())
					}
					if e != nil {
						writeAcceptanceError(w, e)
						return
					}
					writeAcceptanceOperation(w, o, nil)
					return
				}
				writeError(w, 403, "fixture_closed", "synthetic adapter only")
				return
			}
		}
		if r.URL.Path == "/api/desktop/openai-handoff" && r.Method == "POST" && s.authenticated(r) && s.validMutation(r) {
			mu.Lock()
			defer mu.Unlock()
			var q struct {
				URL string `json:"url"`
			}
			if e := decodeAccountJSON(w, r, &q); e != nil {
				writeDecodeError(w, e)
				return
			}
			if pending == nil || q.URL != localLogin.URL+"/auth/start" {
				writeError(w, 403, "handoff_unavailable", "fixture stopped")
				return
			}
			writeJSON(w, 200, map[string]bool{"allowed": true})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/" {
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, r)
			for key, values := range rr.Header() {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.Header().Del("Content-Length")
			w.WriteHeader(rr.Code)
			fmt.Fprint(w, strings.Replace(rr.Body.String(), "<body>", "<body><p role=note>SYNTHETIC local UI fixture — native diagnostic and provider execution are replaced. No real sign-in, Keychain, model use, VM or Docker.</p>", 1))
			return
		}
		s.ServeHTTP(w, r)
	}))
	app.Start()
	defer app.Close()
	s.host = strings.TrimPrefix(app.URL, "http://")
	s.origin = app.URL
	details, _ := json.Marshal(map[string]string{"url": app.URL, "account": a.ID, "route": route.ID, "acceptance": "SYNTHETIC split-layer only; native probe and provider execution replaced; no VM/Docker"})
	fmt.Println("DESKTOP_BROWSER_FIXTURE " + string(details))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(25 * time.Minute):
	}
}
