package acceptance

import (
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/store"
)

// ProductionConnectionOptions is host integration, not serialized settings.
// Only explicit paired startup opt-in supplies this policy and a fresh lease.
type ProductionConnectionOptions struct {
	Route           domain.ModelRoute
	SIWC            *accounts.SIWCService
	CredentialReady func() bool
}

// ProductionConnectionStatus separates immutable provenance from current
// mutation authority. Expired readiness never hides receipts or their route.
func (s *Service) ProductionConnectionStatus(account string) domain.ProductionConnectionStatus {
	status := domain.ProductionConnectionStatus{}
	if s.production == nil || !s.OwnsAccount(account) {
		return status
	}
	metadata := s.production.Metadata()
	if metadata.Generation == "" {
		return status
	}
	status.Admission = &metadata
	status.Enabled = s.ready.Load() && s.production.Eligible(s.store)
	return status
}

// NewProductionConnection owns exactly one immutable route. Every construction
// creates a fresh random admission generation; Recover is still required.
func NewProductionConnection(ledger *store.Store, options ProductionConnectionOptions) *Service {
	s := New(ledger)
	s.production = accounts.NewProductionConnectionAdmission(options.SIWC, ledger, options.Route, options.CredentialReady)
	for _, id := range s.production.OwnedAccounts() {
		s.ownedAccounts[id] = true
	}
	s.routes[options.Route.ID] = options.Route
	s.subscriptionRoutes[options.Route.ID] = true
	s.browserRoutes[options.Route.ID] = true
	s.browserSIWC = options.SIWC
	return s
}
