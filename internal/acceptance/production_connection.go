package acceptance

import (
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/store"
)

// ProductionConnectionOptions is host integration, not serialized settings.
// Shipped startup does not supply this policy or a credential-readiness probe.
type ProductionConnectionOptions struct {
	Route           domain.ModelRoute
	SIWC            *accounts.SIWCService
	CredentialReady func() bool
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
