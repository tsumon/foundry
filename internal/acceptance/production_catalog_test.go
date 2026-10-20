package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type catalogResolver struct {
	reads      atomic.Int32
	generation int64
	fail       bool
	after      func()
}

func (r *catalogResolver) ResolveModelCredential(context.Context, domain.RoleModelSelection) (model.CredentialLease, error) {
	r.reads.Add(1)
	if r.after != nil {
		r.after()
	}
	if r.fail {
		return model.CredentialLease{}, errors.New("synthetic secret failure")
	}
	return model.CredentialLease{Secret: "synthetic-catalog-secret", Generation: r.generation}, nil
}

type catalogTransport func(*http.Request) (*http.Response, error)

func (f catalogTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func catalogController(t *testing.T) (*Service, *store.Store, domain.ModelAccount, domain.ModelRoute, domain.ModelAcceptanceOperation, *catalogResolver, *atomic.Int32) {
	t.Helper()
	s, db, a, r, c, resolver, calls, _ := catalogControllerWithPath(t)
	return s, db, a, r, c, resolver, calls
}
func catalogControllerWithPath(t *testing.T) (*Service, *store.Store, domain.ModelAccount, domain.ModelRoute, domain.ModelAcceptanceOperation, *catalogResolver, *atomic.Int32, string) {
	t.Helper()
	prior, db, opts, a, path := productionController(t, func() bool { return true })
	if e := prior.Recover(); e != nil {
		t.Fatal(e)
	}
	o, e := prior.Propose(a.ID, productionProposal(a, opts.Route))
	if e != nil {
		t.Fatal(e)
	}
	o, e = prior.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
	if e != nil {
		t.Fatal(e)
	}
	o, e = db.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = db.ReserveProductionConnectionDispatch(o, *o.Material.ProductionAdmission, time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = db.BeginModelAuthorization(a.ID, "catalog-signin", "sign_in", "catalog-proof"); e != nil {
		t.Fatal(e)
	}
	b := domain.ModelAuthorizationBinding{ID: "catalog-proof", AccountID: a.ID, Issuer: "https://auth.openai.com", Subject: "synthetic", ClientID: "oaiapp_synthetic", SpendClass: "subscription_only", Scopes: []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}}
	auth, e := db.CommitModelAuthorization("catalog-signin", b, time.Now().Add(time.Hour).Format(time.RFC3339))
	if e != nil {
		t.Fatal(e)
	}
	connection, e := db.FinishProductionConnection(o, "succeeded", "completed", "", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	a, e = db.GetModelAccount(a.ID)
	if e != nil {
		t.Fatal(e)
	}
	resolver := &catalogResolver{generation: auth.Generation}
	calls := &atomic.Int32{}
	svc := NewProductionCatalog(db, ProductionCatalogOptions{Route: opts.Route, Resolver: resolver})
	svc.catalog.http.Transport = catalogTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.String() != "https://api.openai.com/v1/models" || r.Body != nil || r.Header.Get("Authorization") != "Bearer synthetic-catalog-secret" {
			t.Error("request authority exceeded")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"models":[{"slug":"z-model","display_name":"Z model","visibility":"list"},{"slug":"hidden","display_name":"Hidden","visibility":"hidden"},{"slug":"a-model","display_name":"A model","visibility":"list"}]}`)), Request: r}, nil
	})
	if e = svc.Recover(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { svc.Close() })
	return svc, db, a, opts.Route, connection, resolver, calls, path
}

func proposeCatalog(t *testing.T, s *Service, a domain.ModelAccount, r domain.ModelRoute, c domain.ModelAcceptanceOperation) domain.ModelAcceptanceOperation {
	t.Helper()
	o, e := s.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: c.ID})
	if e != nil {
		t.Fatal(e)
	}
	return o
}
func approveCatalog(t *testing.T, s *Service, o domain.ModelAcceptanceOperation) domain.ModelAcceptanceOperation {
	t.Helper()
	o, e := s.Decide(o.AccountID, o.ID, o.Revision, o.MaterialDigest, "approve")
	if e != nil {
		t.Fatal(e)
	}
	return o
}

func TestProductionCatalogPreparationAndSingleDispatch(t *testing.T) {
	s, _, a, r, c, resolver, calls := catalogController(t)
	if !s.ProductionCatalogStatus(a.ID).Enabled || s.ProductionConnectionStatus(a.ID).Enabled || s.SimulationEligible(a.ID) {
		t.Fatal("wrong mode")
	}
	o := proposeCatalog(t, s, a, r, c)
	if o.State != "pending" || o.Source != "production_catalog" || resolver.reads.Load() != 0 || calls.Load() != 0 {
		t.Fatal("preparation touched credentials/network")
	}
	if _, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e == nil {
		t.Fatal("pending dispatched")
	}
	o = approveCatalog(t, s, o)
	if resolver.reads.Load() != 0 || calls.Load() != 0 {
		t.Fatal("approval touched credentials/network")
	}
	var wg sync.WaitGroup
	var winners atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
			if e == nil {
				winners.Add(1)
				if got.State != "succeeded" || len(got.Catalog) != 2 || got.Catalog[0].DisplayName != "Z model" || got.Catalog[1].ID != "a-model" {
					t.Errorf("bad catalog: %+v", got)
				}
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 || resolver.reads.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("counts winners=%d reads=%d requests=%d", winners.Load(), resolver.reads.Load(), calls.Load())
	}
}

func TestProductionCatalogNoDispatchAfterLeaseChange(t *testing.T) {
	for _, kind := range []string{"generation", "account", "expiry", "secret failure", "authorization generation", "authorization expiry", "authorization binding"} {
		t.Run(kind, func(t *testing.T) {
			s, db, a, r, c, resolver, calls := catalogController(t)
			o := approveCatalog(t, s, proposeCatalog(t, s, a, r, c))
			switch kind {
			case "generation":
				resolver.generation++
			case "account":
				resolver.after = func() {
					a.Name = "changed"
					if _, e := db.SaveModelAccount(a, a.Revision); e != nil {
						t.Error(e)
					}
				}
			case "expiry":
				resolver.after = func() { s.now = func() time.Time { return time.Now().Add(time.Hour) } }
			case "secret failure":
				resolver.fail = true
			case "authorization generation", "authorization expiry", "authorization binding":
				resolver.after = func() {
					auth, e := db.GetModelAuthorization("catalog-proof")
					if e != nil {
						t.Fatal(e)
					}
					opKind := "refresh"
					if kind == "authorization binding" {
						opKind = "sign_in"
						auth.Binding.ID = "new-proof"
					}
					if _, e = db.BeginModelAuthorization(a.ID, "changed-authorization", opKind, auth.Binding.ID); e != nil {
						t.Fatal(e)
					}
					if kind == "authorization expiry" {
						auth.ExpiresAt = time.Now().Add(2 * time.Hour).Format(time.RFC3339)
					}
					if _, e = db.CommitModelAuthorization("changed-authorization", auth.Binding, auth.ExpiresAt); e != nil {
						t.Fatal(e)
					}
				}
			}
			got, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
			if e != nil || got.State != "failed" || got.Certainty != "not_sent" || calls.Load() != 0 || resolver.reads.Load() != 1 {
				t.Fatalf("unsafe lease change: %+v %v calls=%d", got, e, calls.Load())
			}
		})
	}
}

func TestProductionCatalogResponsesAreBoundedSanitizedAndNeverRetried(t *testing.T) {
	cases := []struct {
		name                     string
		status                   int
		media, body, state, code string
	}{
		{"empty", 200, "application/json", `{"models":[]}`, "succeeded", ""},
		{"versioned slug", 200, "application/json", `{"models":[{"slug":"gpt-5.1","display_name":"GPT 5.1","visibility":"list"}]}`, "succeeded", ""},
		{"redirect", 302, "application/json", `{}`, "unknown", "response_incomplete"},
		{"auth", 401, "application/json", `synthetic-catalog-secret`, "failed", "auth_invalid"},
		{"entitlement", 403, "application/json", `synthetic-catalog-secret`, "failed", "entitlement_denied"},
		{"rate", 429, "application/json", `synthetic-catalog-secret`, "failed", "rate_limited"},
		{"quota", 429, "application/json", `{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"synthetic-catalog-secret"}}`, "failed", "quota_exhausted"},
		{"server", 500, "application/json", `synthetic-catalog-secret`, "failed", "server_error"},
		{"malformed", 200, "application/json", `{"models":`, "unknown", "response_incomplete"},
		{"wrong shape", 200, "application/json", `{"data":[]}`, "unknown", "response_incomplete"},
		{"media", 200, "text/html", `{"models":[]}`, "unknown", "response_incomplete"},
		{"duplicate", 200, "application/json", `{"models":[{"slug":"a","display_name":"A","visibility":"list"},{"slug":"a","display_name":"A","visibility":"list"}]}`, "unknown", "response_incomplete"},
		{"name", 200, "application/json", `{"models":[{"slug":"a","display_name":"<script>","visibility":"list"}]}`, "unknown", "response_incomplete"},
		{"echo", 200, "application/json", `{"models":[{"slug":"a","display_name":"synthetic-catalog-secret","visibility":"list"}]}`, "unknown", "response_incomplete"},
		{"hidden echo", 200, "application/json", `{"models":[{"slug":"a","display_name":"synthetic-catalog-secret","visibility":"hidden"}]}`, "unknown", "response_incomplete"},
		{"unicode corruption", 200, "application/json", "{\"models\":[{\"slug\":\"a\",\"display_name\":\"\xff\",\"visibility\":\"list\"}]}", "unknown", "response_incomplete"},
		{"oversized", 200, "application/json", strings.Repeat(" ", 1<<20) + `{"models":[]}`, "unknown", "response_incomplete"},
	}
	many := []string{}
	for i := 0; i < 129; i++ {
		many = append(many, fmt.Sprintf(`{"slug":"m%d","display_name":"Model","visibility":"list"}`, i))
	}
	cases = append(cases, struct {
		name                     string
		status                   int
		media, body, state, code string
	}{"too many", 200, "application/json", `{"models":[` + strings.Join(many, ",") + `]}`, "unknown", "response_incomplete"})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, a, r, c, resolver, calls := catalogController(t)
			s.catalog.http.Transport = catalogTransport(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{tc.media}, "Location": []string{"https://untrusted.example/models"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: req}, nil
			})
			o := approveCatalog(t, s, proposeCatalog(t, s, a, r, c))
			got, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
			if e != nil || got.State != tc.state || got.Code != tc.code || calls.Load() != 1 || resolver.reads.Load() != 1 {
				t.Fatalf("result state=%s code=%s error=%v reads=%d requests=%d", got.State, got.Code, e, resolver.reads.Load(), calls.Load())
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "synthetic-catalog-secret") {
				t.Fatal("credential leaked")
			}
			if _, e = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e == nil || calls.Load() != 1 {
				t.Fatal("replayed")
			}
		})
	}
}

func TestProductionCatalogFrozenAuthorityAndRecovery(t *testing.T) {
	for _, change := range []string{"stale process", "cross account", "wrong phase", "connection profile", "source", "account changed", "approval expired", "execution expired", "newer failed connection", "unresolved auth", "crash"} {
		t.Run(change, func(t *testing.T) {
			s, db, a, r, c, resolver, calls, path := catalogControllerWithPath(t)
			o := proposeCatalog(t, s, a, r, c)
			switch change {
			case "stale process":
				fresh := NewProductionCatalog(db, ProductionCatalogOptions{Route: r, Resolver: resolver})
				if e := fresh.Recover(); e != nil {
					t.Fatal(e)
				}
				s = fresh
			case "cross account":
				a.ID = "other"
			case "wrong phase", "connection profile", "source":
				switch change {
				case "wrong phase":
					o.Material.Phase = "inference"
					o.Phase = "inference"
				case "connection profile":
					o.Material.ProductionAdmission.Profile = "openai_siwc_subscription_v1"
				case "source":
					o.Source = "local_simulation"
				}
				o.MaterialDigest = domain.AcceptanceDigest(o.Material)
				raw, _ := json.Marshal(o)
				sqlDB, e := sql.Open("sqlite3", path)
				if e != nil {
					t.Fatal(e)
				}
				defer sqlDB.Close()
				if _, e = sqlDB.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, raw, o.ID); e != nil {
					t.Fatal(e)
				}
			case "account changed":
				a.Name = "changed"
				if _, e := db.SaveModelAccount(a, a.Revision); e != nil {
					t.Fatal(e)
				}
			case "approval expired":
				s.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
			case "newer failed connection":
				admission := *c.Material.ProductionAdmission
				n, e := db.ProposeProductionConnection(a.ID, productionProposal(a, r), admission, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				n, e = db.DecideModelAcceptance(a.ID, n.ID, n.Revision, n.MaterialDigest, "approve", time.Now())
				if e != nil {
					t.Fatal(e)
				}
				n, e = db.ClaimModelAcceptance(a.ID, n.ID, n.Revision, n.MaterialDigest, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = db.FinishProductionConnection(n, "failed", "not_sent", "request_rejected", time.Now()); e != nil {
					t.Fatal(e)
				}
			case "unresolved auth":
				if _, e := db.BeginModelAuthorization(a.ID, "pending-other", "sign_in", "other-proof"); e != nil {
					t.Fatal(e)
				}
			case "execution expired", "crash":
				o = approveCatalog(t, s, o)
				if change == "execution expired" {
					s.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
				} else {
					var e error
					o, e = db.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
					if e != nil {
						t.Fatal(e)
					}
					if e = db.ReserveProductionCatalogDispatch(o, s.catalog.admission, time.Now()); e != nil {
						t.Fatal(e)
					}
					if e = s.Recover(); e != nil {
						t.Fatal(e)
					}
					got, _ := s.Get(a.ID, o.ID)
					if got.State != "unknown" || got.Attempts != 1 {
						t.Fatal("crash not conserved")
					}
				}
			}
			if change != "execution expired" && change != "crash" {
				if _, e := s.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve"); e == nil {
					t.Fatal("stale approval accepted")
				}
			} else {
				if _, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e == nil {
					t.Fatal("stale execution accepted")
				}
			}
			if calls.Load() != 0 || resolver.reads.Load() != 0 {
				t.Fatal("authority failure touched external boundary")
			}
		})
	}
}

func TestProductionCatalogReceiptFailureClosesAdmission(t *testing.T) {
	s, _, a, r, c, resolver, calls, path := catalogControllerWithPath(t)
	o := approveCatalog(t, s, proposeCatalog(t, s, a, r, c))
	sqlDB, e := sql.Open("sqlite3", path)
	if e != nil {
		t.Fatal(e)
	}
	defer sqlDB.Close()
	if _, e = sqlDB.Exec(`CREATE TRIGGER catalog_receipt_fault BEFORE UPDATE ON model_acceptance_operations WHEN OLD.state='running' AND NEW.state!='running' BEGIN SELECT RAISE(ABORT,'synthetic fault'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e != ErrPersistence {
		t.Fatalf("receipt failure not closed: %v", e)
	}
	if s.ProductionCatalogStatus(a.ID).Enabled || calls.Load() != 1 || resolver.reads.Load() != 1 {
		t.Fatal("unsafe receipt failure")
	}
	if _, e = sqlDB.Exec(`DROP TRIGGER catalog_receipt_fault`); e != nil {
		t.Fatal(e)
	}
	if e = s.Recover(); e != nil {
		t.Fatal(e)
	}
	got, e := s.Get(a.ID, o.ID)
	if e != nil || got.State != "unknown" || got.Attempts != 1 {
		t.Fatalf("lost receipt recovery: %+v %v", got, e)
	}
	if _, e = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e == nil || calls.Load() != 1 || resolver.reads.Load() != 1 {
		t.Fatal("receipt failure replayed")
	}
}

type catalogMemorySecrets struct {
	t          *testing.T
	ref, value string
	reads      int
}

func (s *catalogMemorySecrets) Get(ref string) (string, error) {
	s.reads++
	if ref != s.ref {
		s.t.Fatal("wrong credential leased")
	}
	return s.value, nil
}
func (s *catalogMemorySecrets) Set(string, string) error {
	s.t.Fatal("catalog wrote a credential")
	return nil
}
func (s *catalogMemorySecrets) Delete(string) error {
	s.t.Fatal("catalog deleted a credential")
	return nil
}

func TestProductionCatalogUsesExactStoredCredentialWithoutRefresh(t *testing.T) {
	s, db, a, r, c, _, calls := catalogController(t)
	auth, e := db.GetModelAuthorization("catalog-proof")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]string{"access_token": "synthetic-catalog-secret", "refresh_token": "unused-synthetic-refresh", "expires_at": auth.ExpiresAt, "resource": "https://api.openai.com/v1"})
	secrets := &catalogMemorySecrets{t: t, ref: auth.SecretRef, value: string(raw)}
	s.catalog.resolver = accounts.New(db, secrets)
	o := approveCatalog(t, s, proposeCatalog(t, s, a, r, c))
	if secrets.reads != 0 {
		t.Fatal("preparation read secret")
	}
	got, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
	if e != nil || got.State != "succeeded" || secrets.reads != 1 || calls.Load() != 1 {
		t.Fatalf("exact credential execution: state=%s err=%v reads=%d requests=%d", got.State, e, secrets.reads, calls.Load())
	}
}

func TestProductionCatalogDeniedPreparationAndTransportFailures(t *testing.T) {
	s, db, a, r, c, resolver, calls := catalogController(t)
	for _, q := range []domain.ModelAcceptanceProposal{
		{Phase: "connection", RouteID: r.ID, ExpectedAccountRevision: a.Revision},
		{Phase: "inference", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: c.ID},
		{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision},
		{Phase: "catalog", RouteID: "other", ExpectedAccountRevision: a.Revision, PrerequisiteID: c.ID},
		{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: c.ID, Model: "model"},
	} {
		if _, e := s.Propose(a.ID, q); e == nil {
			t.Fatal("unauthorized proposal accepted")
		}
	}
	closed := New(db)
	if e := closed.Recover(); e != nil {
		t.Fatal(e)
	}
	if _, e := closed.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: c.ID}); e != ErrDisabled {
		t.Fatal("default allowed catalog")
	}
	if calls.Load() != 0 || resolver.reads.Load() != 0 {
		t.Fatal("denial performed effects")
	}
	s.catalog.http.Transport = catalogTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("synthetic-catalog-secret")
	})
	o := approveCatalog(t, s, proposeCatalog(t, s, a, r, c))
	got, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
	if e != nil || got.State != "unknown" || got.Code != "transport_unknown" || calls.Load() != 1 || resolver.reads.Load() != 1 {
		t.Fatal("transport failure retried or misclassified")
	}
}

func TestProductionCatalogCannotAdoptChangesAfterPossibleDispatch(t *testing.T) {
	for _, change := range []string{"expiry", "account", "interrupted"} {
		t.Run(change, func(t *testing.T) {
			s, db, a, r, c, resolver, calls := catalogController(t)
			o := approveCatalog(t, s, proposeCatalog(t, s, a, r, c))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.catalog.http.Transport = catalogTransport(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				switch change {
				case "expiry":
					s.now = func() time.Time { return time.Now().Add(time.Hour) }
				case "account":
					a.Name = "changed after send"
					if _, e := db.SaveModelAccount(a, a.Revision); e != nil {
						t.Fatal(e)
					}
				case "interrupted":
					cancel()
					return nil, ctx.Err()
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"models":[]}`)), Request: req}, nil
			})
			got, e := s.Execute(ctx, a.ID, o.ID, o.Revision, o.MaterialDigest)
			if change == "interrupted" {
				if e != nil || got.State != "unknown" {
					t.Fatalf("interruption result: %s %v", got.State, e)
				}
			} else {
				if e != ErrPersistence {
					t.Fatalf("changed authority completed: %v", e)
				}
				got, e = s.Get(a.ID, o.ID)
				if e != nil || got.State != "unknown" {
					t.Fatal("changed authority not conserved as unknown")
				}
			}
			if _, e = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e == nil || calls.Load() != 1 || resolver.reads.Load() != 1 {
				t.Fatal("possible dispatch replayed")
			}
		})
	}
}
