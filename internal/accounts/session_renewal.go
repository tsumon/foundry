package accounts

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"sync"
	"time"
)

type renewalWork struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// SessionRenewal is owned by the trusted desktop lifetime, never an HTTP
// scheduler. Construction and reads perform no credential or provider I/O.
type SessionRenewal struct {
	siwc   *SIWCService
	now    func() time.Time
	mu     sync.Mutex
	active map[string]*renewalWork
	closed bool
}

func NewSessionRenewal(s *SIWCService, now func() time.Time) *SessionRenewal {
	if s == nil || s.lifecycle == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &SessionRenewal{siwc: s, now: now, active: map[string]*renewalWork{}}
}
func (c *SessionRenewal) ledger() *store.Store { return c.siwc.lifecycle.store }
func (c *SessionRenewal) eligible(a domain.ModelAccount, b domain.ModelAuthorization) bool {
	s := c.siwc
	if !a.Enabled || a.Provider != "openai" || a.AuthMethod != "openai_siwc" || a.BillingMode != "subscription" || b.State != "available" || b.Binding.SpendClass != "subscription_only" || b.Binding.AccountID != a.ID || b.Binding.Issuer != s.options.Issuer || !hasSIWCScope(b.Binding.Scopes, "offline_access") {
		return false
	}
	if s.simulation {
		return validSIWCLoopbackOrigin(s.options.TokenOrigin) && validSIWCResource(a.BaseURL)
	}
	return a.BaseURL == officialSIWCResource && b.Binding.Issuer == officialSIWCIssuer && validSubscriptionClientID(b.Binding.ClientID) && s.options.TokenOrigin == officialSIWCOrigin && s.options.OIDCOrigin == officialSIWCOrigin && s.options.JWKSOrigin == officialSIWCOrigin
}
func (c *SessionRenewal) due(a domain.ModelAuthorization) (time.Time, error) {
	expiry, err := time.Parse(time.RFC3339, a.ExpiresAt)
	if err != nil {
		return time.Time{}, domain.ErrSessionRenewal
	}
	due := expiry.Add(-5 * time.Minute)
	raw, err := c.ledger().SessionRenewalEarliest(a.Binding.ID, a.Generation)
	if err != nil {
		return time.Time{}, err
	}
	if raw != "" {
		earliest, e := time.Parse(time.RFC3339, raw)
		if e != nil {
			return time.Time{}, domain.ErrSessionRenewal
		}
		if earliest.After(due) {
			due = earliest
		}
	}
	return due, nil
}
func (c *SessionRenewal) Status(id string) (domain.SessionRenewalStatus, error) {
	result := domain.SessionRenewalStatus{AccountID: id, State: "disabled", AccessState: "unavailable"}
	a, err := c.ledger().GetModelAccount(id)
	if err != nil {
		return result, err
	}
	result.AccountRevision = a.Revision
	p, err := c.ledger().SessionRenewalPolicy(id)
	if err != nil {
		return result, err
	}
	result.Revision = p.Revision
	result.Enabled = p.Enabled
	if p.Material.ID != "" {
		m := p.Material
		result.Material = &m
	}
	b, err := c.ledger().CurrentModelAuthorization(id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return result, err
	}
	result.RegistrationRetained = b.Binding.ID != "" && b.State != "revoked"
	if b.State == "available" {
		result.AccessState = "expired"
		expiry, e := time.Parse(time.RFC3339, b.ExpiresAt)
		if e == nil && c.now().Before(expiry) {
			result.AccessState = "usable"
		}
	}
	receipt, err := c.ledger().SessionRenewalReceipt(id)
	if err != nil {
		return result, err
	}
	result.Receipt = receipt
	if p.Enabled {
		result.State = "enabled"
		if !c.eligible(a, b) || a.Revision != p.Material.AccountRevision || !domain.SameModelAuthorizationBinding(b.Binding, p.Material.Binding) {
			result.State = "blocked"
		}
	}
	if b.State == "revoked" {
		result.State = "login_required"
	}
	if b.State == "refresh_unknown" || b.State == "failed" {
		result.State = "recovery_required"
	}
	if receipt != nil && receipt.AuthorizationID == b.Binding.ID {
		if receipt.Stage == "dispatched" {
			result.State = "in_progress"
		} else if b.State != "revoked" && receipt.Stage != "succeeded" && receipt.Stage != "waiting" {
			result.State = receipt.Stage
		}
	}
	if b.State == "available" {
		due, e := c.due(b)
		if e == nil {
			result.DueAt = due.UTC().Format(time.RFC3339)
		} else if p.Enabled {
			result.State = "blocked"
		}
	}
	return result, nil
}
func (c *SessionRenewal) Prepare(id string, expectedAccountRevision int64) (domain.SessionRenewalMaterial, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var m domain.SessionRenewalMaterial
	if c.closed {
		return m, domain.ErrSessionRenewal
	}
	a, err := c.ledger().GetModelAccount(id)
	if err != nil {
		return m, err
	}
	if a.Revision != expectedAccountRevision {
		return m, store.ErrConflict
	}
	b, err := c.ledger().CurrentModelAuthorization(id)
	if err != nil {
		return m, err
	}
	if !c.eligible(a, b) {
		return m, domain.ErrSessionRenewal
	}
	if _, err = c.due(b); err != nil {
		return m, err
	}
	materialID, err := opaqueID("renewal-")
	if err != nil {
		return m, err
	}
	m = domain.SessionRenewalMaterial{ID: materialID, AccountID: id, AccountRevision: a.Revision, Binding: b.Binding, Resource: a.BaseURL, TokenEndpoint: c.siwc.options.TokenOrigin + siwcTokenPath, ExpiresAt: c.now().UTC().Add(10 * time.Minute).Format(time.RFC3339Nano), CredentialStore: "system credential store", Scope: "Credential renewal only. No model calls, catalog, task continuation, budget, VM, or paid API fallback.", Schedule: "Every 30 seconds while desktop is running; expiry minus five minutes, respecting provider earliest refresh time.", FailureBehavior: "No automatic retry. Unknown or failed rotation requires explicit recovery or sign-in."}
	return c.ledger().PrepareSessionRenewal(m)
}
func (c *SessionRenewal) Approve(id, materialID string, revision int64) (domain.SessionRenewalStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return domain.SessionRenewalStatus{}, domain.ErrSessionRenewal
	}
	a, err := c.ledger().GetModelAccount(id)
	if err != nil {
		return domain.SessionRenewalStatus{}, err
	}
	b, err := c.ledger().CurrentModelAuthorization(id)
	if err != nil || !c.eligible(a, b) {
		return domain.SessionRenewalStatus{}, domain.ErrSessionRenewal
	}
	if err = c.ledger().ApproveSessionRenewal(id, materialID, revision, c.now()); err != nil {
		return domain.SessionRenewalStatus{}, err
	}
	return c.Status(id)
}
func (c *SessionRenewal) Disable(id string, revision int64) (domain.SessionRenewalStatus, error) {
	c.mu.Lock()
	err := c.ledger().DisableSessionRenewal(id, revision)
	work := c.active[id]
	if err == nil && work != nil {
		work.cancel()
	}
	c.mu.Unlock()
	if err != nil {
		return domain.SessionRenewalStatus{}, err
	}
	if work != nil {
		<-work.done
	}
	return c.Status(id)
}
func (c *SessionRenewal) Tick(ctx context.Context) {
	policies, err := c.ledger().EnabledSessionRenewals()
	if err != nil {
		return
	}
	for _, p := range policies {
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		if c.active[p.AccountID] != nil {
			c.mu.Unlock()
			continue
		}
		a, e := c.ledger().GetModelAccount(p.AccountID)
		b, e2 := c.ledger().CurrentModelAuthorization(p.AccountID)
		if e != nil || e2 != nil || !c.eligible(a, b) || a.Revision != p.Material.AccountRevision || !domain.SameModelAuthorizationBinding(b.Binding, p.Material.Binding) {
			c.mu.Unlock()
			continue
		}
		due, e := c.due(b)
		if e != nil || c.now().Before(due) {
			c.mu.Unlock()
			continue
		}
		if e = c.ledger().ClaimSessionRenewal(p, b.Generation, c.now()); e != nil {
			c.mu.Unlock()
			continue
		}
		// Native credential access may wait for the OS. Its wait must not use
		// the network deadline; refreshSession starts that deadline at exchange.
		workCtx, cancel := context.WithCancel(ctx)
		work := &renewalWork{cancel: cancel, done: make(chan struct{})}
		c.active[p.AccountID] = work
		c.mu.Unlock()
		next, refreshErr := c.siwc.refreshSession(workCtx, b.Binding.ID, a.Revision, p.Revision, c.now)
		cancel()
		stage, category, certainty := "succeeded", "", "confirmed"
		if refreshErr != nil {
			stage, category, certainty = "recovery_required", "renewal_failed", "unknown"
			var provider *model.ProviderError
			if errors.As(refreshErr, &provider) {
				if provider.Certainty == "not_sent" || provider.Certainty == "rejected" {
					certainty = provider.Certainty
				}
				if provider.HTTPStatus >= 500 {
					certainty = "unknown"
				}
				if provider.Code == "refresh_not_ready" && provider.Certainty == "not_sent" {
					stage, category = "waiting", "refresh_not_ready"
				}
				if provider.Code == "credential_preflight" && provider.Certainty == "not_sent" {
					category = "credential_preflight"
				}
			}
			latest, e := c.ledger().GetModelAuthorization(b.Binding.ID)
			if e == nil && latest.State == "revoked" {
				stage, category, certainty = "login_required", "auth_invalid", "rejected"
			}
			if e == nil && latest.State == "available" && latest.Generation > b.Generation {
				next = latest
				certainty = "confirmed"
			}
		}
		_ = c.ledger().FinishSessionRenewal(b.Binding.ID, b.Generation, next.Generation, stage, category, certainty, c.now())
		c.mu.Lock()
		delete(c.active, p.AccountID)
		close(work.done)
		c.mu.Unlock()
	}
}
func (c *SessionRenewal) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Tick(ctx)
		}
	}
}
func (c *SessionRenewal) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	var works []*renewalWork
	for _, work := range c.active {
		work.cancel()
		works = append(works, work)
	}
	c.mu.Unlock()
	for _, work := range works {
		<-work.done
	}
}
