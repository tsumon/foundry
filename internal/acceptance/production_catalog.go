package acceptance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"time"
)

// Host-only policy. No transport, origin, credentials or readiness flags are
// accepted through serialized settings or HTTP.
type ProductionCatalogOptions struct {
	Route    domain.ModelRoute
	Resolver model.CredentialResolver
}
type ProductionCatalogStatus struct {
	Enabled   bool
	Blocker   string
	Admission *domain.ProductionAdmission
}
type productionCatalog struct {
	route     domain.ModelRoute
	admission domain.ProductionAdmission
	resolver  model.CredentialResolver
	http      *http.Client
}

func NewProductionCatalog(ledger *store.Store, options ProductionCatalogOptions) *Service {
	s := New(ledger)
	s.catalog = &productionCatalog{route: options.Route, resolver: options.Resolver}
	if options.Route.AccountID != "" {
		s.ownedAccounts[options.Route.AccountID] = true
	}
	saved, e := ledger.GetModelRoute(options.Route.ID)
	a, ae := ledger.GetModelAccount(options.Route.AccountID)
	if e != nil || ae != nil || saved != options.Route || domain.ValidateModelRoute(saved, a) != nil || !a.Enabled || a.Provider != "openai" || a.AuthMethod != "openai_siwc" || a.BillingMode != "subscription" || a.BaseURL != domain.OpenAISubscriptionRequestedResource || saved.Environment != "live" || saved.Adapter != "openai_siwc_responses" || saved.Protocol != "responses" || saved.SpendClass != "subscription_only" || saved.ExtraUsage != "off" || saved.BaseURL != domain.OpenAISubscriptionRequestedResource || options.Resolver == nil {
		return s
	}
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return s
	}
	s.catalog.admission = domain.ProductionAdmission{Generation: hex.EncodeToString(nonce), Profile: "openai_siwc_catalog_v1"}
	// Fresh connections only: no environment proxy, redirect, or reused-connection
	// automatic replay. This client is exclusive to one-request catalog dispatch.
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: false, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
	s.catalog.http = &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s.routes[saved.ID] = saved
	return s
}

func (s *Service) ProductionCatalogStatus(account string) ProductionCatalogStatus {
	status := ProductionCatalogStatus{}
	if s.catalog == nil || !s.OwnsAccount(account) {
		return status
	}
	if !s.catalog.admission.Valid() {
		status.Blocker = "admission_unavailable"
		return status
	}
	copy := s.catalog.admission
	status.Admission = &copy
	if !s.ready.Load() {
		status.Blocker = "admission_unavailable"
		return status
	}
	saved, e := s.store.GetModelRoute(s.catalog.route.ID)
	if e != nil || saved != s.catalog.route {
		status.Blocker = "admission_unavailable"
		return status
	}
	status.Blocker = s.store.ProductionCatalogReadiness(account, s.catalog.route.ID, s.catalog.admission, s.now())
	status.Enabled = status.Blocker == ""
	return status
}

func (s *Service) ownedCatalog(account, id string) (domain.ModelAcceptanceOperation, error) {
	if !s.ready.Load() || !s.OwnsAccount(account) || !s.catalog.admission.Valid() {
		return domain.ModelAcceptanceOperation{}, ErrDisabled
	}
	o, e := s.Get(account, id)
	if e != nil {
		return s.mutationResult(o, e)
	}
	m := o.Material
	if o.Source != "production_catalog" || o.LiveAccess != "catalog_only" || o.Phase != "catalog" || m.ProductionAdmission == nil || *m.ProductionAdmission != s.catalog.admission || !m.ValidProductionCatalog() || !reflect.DeepEqual(m.Route, s.catalog.route) {
		return domain.ModelAcceptanceOperation{}, ErrDisabled
	}
	return o, nil
}

func catalogFailure(code, certainty string) error {
	return &model.ProviderError{Code: code, Certainty: certainty}
}

func (s *Service) executeCatalog(ctx context.Context, o domain.ModelAcceptanceOperation) (domain.ModelAcceptanceOperation, error) {
	if e := s.store.ReserveProductionCatalogDispatch(o, s.catalog.admission, s.now()); e != nil {
		return s.finish(o, catalogFailure("request_rejected", "not_sent"), nil, model.Result{})
	}
	started, _ := time.Parse(time.RFC3339Nano, o.StartedAt)
	expiry, _ := time.Parse(time.RFC3339Nano, o.Material.ExpiresAt)
	deadline := started.Add(30 * time.Second)
	if expiry.Before(deadline) {
		deadline = expiry
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	entries, e := s.requestCatalog(ctx, o)
	if e == nil && ctx.Err() != nil {
		e = catalogFailure("transport_unknown", "unknown")
	}
	return s.finish(o, e, entries, model.Result{})
}

func (s *Service) requestCatalog(ctx context.Context, o domain.ModelAcceptanceOperation) ([]domain.ModelAcceptanceCatalogEntry, error) {
	validate := func() bool {
		return ctx.Err() == nil && s.ready.Load() && s.store.ValidateProductionCatalogDispatch(o, s.catalog.admission, s.now()) == nil
	}
	if !validate() {
		return nil, catalogFailure("request_rejected", "not_sent")
	}
	lease, e := s.catalog.resolver.ResolveModelCredential(ctx, o.Material.Selection())
	if e != nil || lease.Secret == "" || strings.ContainsAny(lease.Secret, "\r\n") || lease.Generation != o.Material.Authorization.Generation {
		return nil, catalogFailure("auth_invalid", "not_sent")
	}
	// Secret reads may block or race account/authorization changes. Recheck the
	// complete frozen snapshot and lineage after leasing, immediately before send.
	if !validate() {
		return nil, catalogFailure("auth_invalid", "not_sent")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, domain.OpenAICatalogURL, nil)
	if e != nil {
		return nil, catalogFailure("request_rejected", "not_sent")
	}
	req.Header.Set("Authorization", "Bearer "+lease.Secret)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Foundry/0.1.0")
	response, e := s.catalog.http.Do(req)
	if e != nil {
		return nil, catalogFailure("transport_unknown", "unknown")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if response.StatusCode < 400 {
			return nil, catalogFailure("response_incomplete", "unknown")
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if err != nil {
			return nil, catalogFailure("transport_unknown", "unknown")
		}
		return nil, model.SubscriptionCatalogFailure(response.StatusCode, body)
	}
	media, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if e != nil || media != "application/json" {
		return nil, catalogFailure("response_incomplete", "unknown")
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if e != nil || len(body) > 1<<20 {
		return nil, catalogFailure("response_incomplete", "unknown")
	}
	return model.DecodeSubscriptionCatalog(body, lease.Secret)
}
