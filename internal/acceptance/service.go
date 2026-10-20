// Package acceptance implements inert, single-use local simulation approvals.
package acceptance

import (
	"context"
	"errors"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

var ErrDisabled = errors.New("acceptance_simulation_disabled")
var ErrPersistence = errors.New("acceptance_record_unavailable")

type Connector func(context.Context, domain.ModelAcceptanceMaterial) error
type Options struct {
	Routes                         []domain.ModelRoute
	IdentityOnlyRouteIDs           []string
	SubscriptionConnectionRouteIDs []string
	BrowserHandoffRouteIDs         []string
	BrowserSIWC                    *accounts.SIWCService
	Router                         *model.Router
	Connect                        Connector
	Now                            func() time.Time
}
type Service struct {
	store              *store.Store
	routes             map[string]domain.ModelRoute
	identityRoutes     map[string]bool
	subscriptionRoutes map[string]bool
	ownedAccounts      map[string]bool
	invalidPolicy      bool
	router             *model.Router
	connect            Connector
	now                func() time.Time
	ready              atomic.Bool
	lifecycle          sync.RWMutex
	browserRoutes      map[string]bool
	browserSIWC        *accounts.SIWCService
	sessionsMu         sync.Mutex
	sessions           map[string]*connectionSession
}

func New(s *store.Store) *Service {
	return &Service{store: s, routes: map[string]domain.ModelRoute{}, identityRoutes: map[string]bool{}, subscriptionRoutes: map[string]bool{}, ownedAccounts: map[string]bool{}, browserRoutes: map[string]bool{}, sessions: map[string]*connectionSession{}, now: time.Now}
}

// NewSimulation only accepts constructor-owned account/route pairs. Recovery is
// explicit and must succeed before this service can mutate or dispatch.
func NewSimulation(s *store.Store, options Options) *Service {
	svc := New(s)
	svc.router = options.Router
	svc.connect = options.Connect
	svc.browserSIWC = options.BrowserSIWC
	if options.Now != nil {
		svc.now = options.Now
	}
	seenRoutes := map[string]bool{}
	hasPurposePolicy := len(options.IdentityOnlyRouteIDs)+len(options.SubscriptionConnectionRouteIDs) != 0
	for _, r := range options.Routes {
		if seenRoutes[r.ID] && hasPurposePolicy {
			svc.invalidPolicy = true
		}
		seenRoutes[r.ID] = true
		svc.ownedAccounts[r.AccountID] = true
		a, e := s.GetModelAccount(r.AccountID)
		saved, re := s.GetModelRoute(r.ID)
		if e == nil && re == nil && reflect.DeepEqual(saved, r) && domain.ValidateModelRoute(r, a) == nil && r.Environment == "simulation" && r.ExtraUsage == "off" && (r.Adapter == "openai_siwc_responses" || r.Adapter == "kimi_code_chat") {
			svc.routes[r.ID] = r
		}
	}
	policyAccounts := map[string]string{}
	registerPolicy := func(ids []string, purpose string, target map[string]bool) {
		for _, id := range ids {
			r, ok := svc.routes[id]
			if !ok || r.Adapter != "openai_siwc_responses" || target[id] || svc.identityRoutes[id] || svc.subscriptionRoutes[id] {
				svc.invalidPolicy = true
			}
			if saved, e := s.GetModelRoute(id); e == nil {
				svc.ownedAccounts[saved.AccountID] = true
			}
			target[id] = true
			if ok {
				if prior := policyAccounts[r.AccountID]; prior != "" {
					svc.invalidPolicy = true
				}
				policyAccounts[r.AccountID] = purpose
			}
		}
	}
	registerPolicy(options.IdentityOnlyRouteIDs, "identity_only", svc.identityRoutes)
	registerPolicy(options.SubscriptionConnectionRouteIDs, "subscription_only", svc.subscriptionRoutes)
	for _, id := range options.BrowserHandoffRouteIDs {
		if !svc.subscriptionRoutes[id] || svc.browserRoutes[id] || !svc.browserSIWC.BrowserHandoffEligible(s) {
			svc.invalidPolicy = true
		}
		if saved, e := s.GetModelRoute(id); e == nil {
			svc.ownedAccounts[saved.AccountID] = true
		}
		svc.browserRoutes[id] = true
	}
	for _, r := range options.Routes {
		if purpose := policyAccounts[r.AccountID]; purpose != "" && (purpose == "identity_only" && !svc.identityRoutes[r.ID] || purpose == "subscription_only" && !svc.subscriptionRoutes[r.ID]) {
			svc.invalidPolicy = true
		}
	}
	return svc
}
func (s *Service) OwnsAccount(account string) bool {
	return s.ownedAccounts[account]
}
func (s *Service) SimulationEligible(account string) bool {
	if s.invalidPolicy || !s.ready.Load() || s.router == nil || s.connect == nil {
		return false
	}
	for _, r := range s.routes {
		if r.AccountID == account {
			return true
		}
	}
	return false
}
func (s *Service) ConnectionPurpose(route string) string {
	if s.identityRoutes[route] {
		return "identity_only"
	}
	if s.subscriptionRoutes[route] {
		return "subscription_only"
	}
	return ""
}
func (s *Service) Routes(account string) []domain.ModelRoute {
	out := []domain.ModelRoute{}
	if s.SimulationEligible(account) {
		for _, r := range s.routes {
			if r.AccountID == account {
				out = append(out, r)
			}
		}
	}
	return out
}
func (s *Service) Recover() error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.ready.Store(false)
	s.stopConnections()
	if e := s.store.RecoverModelAcceptance(s.now()); e != nil {
		return ErrPersistence
	}
	s.ready.Store(true)
	return nil
}
func (s *Service) Get(account, id string) (domain.ModelAcceptanceOperation, error) {
	return s.store.GetModelAcceptance(account, id)
}
func (s *Service) List(account string) ([]domain.ModelAcceptanceOperation, error) {
	return s.store.ListModelAcceptance(account)
}

func (s *Service) mutationResult(o domain.ModelAcceptanceOperation, e error) (domain.ModelAcceptanceOperation, error) {
	if e != nil && !errors.Is(e, store.ErrConflict) && !errors.Is(e, store.ErrNotFound) && !errors.Is(e, ErrDisabled) {
		s.ready.Store(false)
		return domain.ModelAcceptanceOperation{}, ErrPersistence
	}
	return o, e
}
func (s *Service) Propose(account string, q domain.ModelAcceptanceProposal) (domain.ModelAcceptanceOperation, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if !s.SimulationEligible(account) || s.routes[q.RouteID].AccountID != account {
		return domain.ModelAcceptanceOperation{}, ErrDisabled
	}
	if q.ConnectionPurpose != s.ConnectionPurpose(q.RouteID) || (s.identityRoutes[q.RouteID] || s.subscriptionRoutes[q.RouteID]) && q.Phase != "connection" {
		return domain.ModelAcceptanceOperation{}, ErrDisabled
	}
	return s.mutationResult(s.store.ProposeModelAcceptanceWithConnectionMode(account, q, s.ConnectionMode(q.RouteID), s.now()))
}
func (s *Service) owned(account, id string) (domain.ModelAcceptanceOperation, error) {
	if !s.SimulationEligible(account) {
		return domain.ModelAcceptanceOperation{}, ErrDisabled
	}
	o, e := s.Get(account, id)
	if e != nil {
		return s.mutationResult(o, e)
	}
	r, ok := s.routes[o.Material.Route.ID]
	if !ok || r.AccountID != account || !reflect.DeepEqual(r, o.Material.Route) || o.Phase != o.Material.Phase || !o.Material.ValidConnectionPurpose() || o.Material.ConnectionMode != s.ConnectionMode(r.ID) || o.Material.ConnectionPurpose != s.ConnectionPurpose(r.ID) || (s.identityRoutes[r.ID] || s.subscriptionRoutes[r.ID]) && o.Phase != "connection" {
		return domain.ModelAcceptanceOperation{}, ErrDisabled
	}
	return o, nil
}
func (s *Service) Decide(account, id string, revision int64, digest, decision string) (domain.ModelAcceptanceOperation, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if _, e := s.owned(account, id); e != nil {
		return domain.ModelAcceptanceOperation{}, e
	}
	return s.mutationResult(s.store.DecideModelAcceptance(account, id, revision, digest, decision, s.now()))
}
func (s *Service) Execute(ctx context.Context, account, id string, revision int64, digest string) (domain.ModelAcceptanceOperation, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if _, e := s.owned(account, id); e != nil {
		return domain.ModelAcceptanceOperation{}, e
	}
	o, e := s.store.ClaimModelAcceptance(account, id, revision, digest, s.now())
	if e != nil {
		return s.mutationResult(domain.ModelAcceptanceOperation{}, e)
	}
	if o.Material.ConnectionMode == "browser_handoff" {
		return s.startConnection(o)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(o.Material.DeadlineSeconds)*time.Second)
	defer cancel()
	var result model.Result
	catalog := []domain.ModelAcceptanceCatalogEntry{}
	switch o.Phase {
	case "connection":
		e = s.connect(ctx, o.Material)
	case "catalog":
		var models []model.CatalogModel
		models, e = s.router.ProbeSelection(ctx, 2, o.Material.Selection())
		if e == nil {
			if len(models) > 128 {
				e = &model.ProviderError{Code: "response_incomplete", Certainty: "unknown"}
			} else {
				for _, m := range models {
					if domain.ValidateModelCredentialOperation(m.ID) != nil {
						e = &model.ProviderError{Code: "response_incomplete", Certainty: "unknown"}
						break
					}
					catalog = append(catalog, domain.ModelAcceptanceCatalogEntry{ID: m.ID, DisplayName: m.ID})
				}
			}
		}
	case "inference":
		selection := o.Material.Selection()
		result, e = s.router.Complete(ctx, model.Request{SnapshotSchema: 2, Selection: &selection, Profile: selection.Profile, System: domain.AcceptanceSystem, Prompt: domain.AcceptancePrompt, MaxOutputTokens: 128})
	}
	if e == nil {
		e = ctx.Err()
	}
	return s.finish(o, e, catalog, result)
}

func (s *Service) finish(o domain.ModelAcceptanceOperation, e error, catalog []domain.ModelAcceptanceCatalogEntry, result model.Result) (domain.ModelAcceptanceOperation, error) {
	state, certainty, code := "succeeded", "completed", ""
	if e != nil {
		state, certainty, code = "unknown", "unknown", "transport_unknown"
		var pe *model.ProviderError
		if errors.As(e, &pe) {
			code = pe.Error()
			switch code {
			case "auth_invalid", "entitlement_denied", "quota_exhausted", "rate_limited", "request_rejected", "server_error", "transport_unknown", "response_incomplete":
			default:
				code = "transport_unknown"
			}
			if pe.Certainty == "rejected" || pe.Certainty == "not_sent" {
				state, certainty = "failed", pe.Certainty
			}
		}
		catalog = nil
		result = model.Result{}
	}
	saved, writeErr := s.store.FinishModelAcceptance(o.AccountID, o.ID, o.Revision, state, certainty, code, catalog, result.InputTokens, result.OutputTokens, result.UsageKnown, s.now())
	if writeErr != nil {
		s.ready.Store(false)
		_, _ = s.store.FinishModelAcceptance(o.AccountID, o.ID, o.Revision, "unknown", "unknown", "transport_unknown", nil, 0, 0, false, s.now())
		return domain.ModelAcceptanceOperation{}, ErrPersistence
	}
	return saved, nil
}
