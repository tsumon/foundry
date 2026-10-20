package accounts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"foundry/internal/domain"
	"foundry/internal/store"
	"reflect"
	"time"
)

// ProductionAdmissionPreparation describes the shipped default-disabled
// implementation, never the eligibility of an admitted host embedding.
func (s *SIWCService) ProductionAdmissionPreparation() *domain.OpenAIProductionAdmissionPreparation {
	if s == nil {
		return nil
	}
	return &domain.OpenAIProductionAdmissionPreparation{State: "implemented_disabled", Validation: "local_simulation_only", NativeKeychain: "not_checked", OperatorActivation: "unavailable"}
}

// ProductionConnectionAdmission is an in-process, constructor-owned policy.
// Its private generation is never reconstructed from an operation or settings.
// CredentialReady is trusted host code: it must return false for missing,
// failed or stale readiness. Nothing here probes the host or imports evidence.
type ProductionConnectionAdmission struct {
	siwc            *SIWCService
	ledger          *store.Store
	route           domain.ModelRoute
	metadata        domain.ProductionAdmission
	credentialReady func() bool
	owned           []string
	valid           bool
}

func NewProductionConnectionAdmission(siwc *SIWCService, ledger *store.Store, route domain.ModelRoute, credentialReady func() bool) *ProductionConnectionAdmission {
	p := &ProductionConnectionAdmission{siwc: siwc, ledger: ledger, route: route, credentialReady: credentialReady}
	if route.AccountID != "" {
		p.owned = append(p.owned, route.AccountID)
	}
	if ledger == nil {
		return p
	}
	saved, err := ledger.GetModelRoute(route.ID)
	if err == nil && saved.AccountID != route.AccountID {
		p.owned = append(p.owned, saved.AccountID)
	}
	if err != nil || !reflect.DeepEqual(saved, route) || siwc == nil || siwc.simulation || siwc.lifecycle == nil || siwc.lifecycle.store != ledger || !siwc.validSubscriptionProfile() || siwc.options.FlowTimeout != 10*time.Minute {
		return p
	}
	a, err := ledger.GetModelAccount(route.AccountID)
	if err != nil || !siwc.subscriptionAccountEligible(a) || domain.ValidateModelRoute(route, a) != nil || route.Environment != "live" || route.ExtraUsage != "off" || route.Adapter != "openai_siwc_responses" || route.SpendClass != "subscription_only" {
		return p
	}
	var generation [16]byte
	if _, err = rand.Read(generation[:]); err != nil {
		return p
	}
	p.metadata = domain.ProductionAdmission{Generation: hex.EncodeToString(generation[:]), Profile: "openai_siwc_subscription_v1"}
	p.valid = true
	return p
}

func (p *ProductionConnectionAdmission) Metadata() domain.ProductionAdmission {
	if p == nil {
		return domain.ProductionAdmission{}
	}
	return p.metadata
}
func (p *ProductionConnectionAdmission) OwnedAccounts() []string {
	if p == nil {
		return nil
	}
	return append([]string(nil), p.owned...)
}
func (p *ProductionConnectionAdmission) Eligible(ledger *store.Store) bool {
	return p != nil && p.valid && p.ledger == ledger && p.credentialReady != nil && p.credentialReady()
}

// OwnsClaim deliberately does not call readiness: cancellation and receipt
// recording must remain available after a host prerequisite is lost.
func (p *ProductionConnectionAdmission) OwnsClaim(ledger *store.Store, claim domain.ModelAcceptanceOperation) bool {
	return p != nil && p.valid && p.ledger == ledger && claim.Source == "production_connection" && claim.AccountID == p.route.AccountID &&
		reflect.DeepEqual(claim.Material.Route, p.route) && claim.Material.ValidConnectionPurpose() && claim.Material.ProductionAdmission != nil && *claim.Material.ProductionAdmission == p.metadata
}

func (p *ProductionConnectionAdmission) Begin(ctx context.Context, claim domain.ModelAcceptanceOperation) (SIWCFlow, func(), error) {
	if p == nil || ctx.Err() != nil || !p.Eligible(p.ledger) || !p.OwnsClaim(p.ledger, claim) {
		return SIWCFlow{}, nil, liveSIWCDenial()
	}
	if err := p.ledger.ReserveProductionConnectionDispatch(claim, p.metadata, time.Now()); err != nil {
		return SIWCFlow{}, nil, liveSIWCDenial()
	}
	// A lost prerequisite after reservation consumes this dispatch; it never
	// reopens it, even if readiness subsequently recovers.
	if ctx.Err() != nil || !p.Eligible(p.ledger) {
		return SIWCFlow{}, nil, liveSIWCDenial()
	}
	started, err := time.Parse(time.RFC3339Nano, claim.StartedAt)
	if err != nil {
		return SIWCFlow{}, nil, liveSIWCDenial()
	}
	owned, cancel := context.WithDeadline(ctx, started.Add(600*time.Second))
	var stop func()
	flow, err := p.siwc.beginSIWCConnection(owned, owned, claim.AccountID, claim.Material.Account.Revision, siwcPurposeSubscription, officialSIWCResource, &stop)
	if err != nil {
		cancel()
		return SIWCFlow{}, nil, err
	}
	return flow, func() { cancel(); stop() }, nil
}
