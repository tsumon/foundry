package acceptance

import (
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"sync"
	"time"
)

// DesktopOptions is constructor-only host authority. Probe must succeed only
// after a fresh diagnostic and confirmed exact-item cleanup. No renderer or
// persisted setting supplies these dependencies.
type DesktopOptions struct {
	SIWC     *accounts.SIWCService
	Resolver model.CredentialResolver
	Probe    func() error
	Now      func() time.Time
}
type DesktopStatus struct {
	Phase           string `json:"phase"`
	RouteID         string `json:"route_id,omitempty"`
	Blocker         string `json:"blocker,omitempty"`
	ConnectionReady bool   `json:"connection_ready"`
	CatalogReady    bool   `json:"catalog_ready"`
}

// DesktopCoordinator owns a single immutable service at a time. Its lock spans
// every delegated request, including catalog dispatch. Browser sessions outlive
// Execute, so phase replacement additionally refuses any owned active session.
type DesktopCoordinator struct {
	mu                             sync.Mutex
	store                          *store.Store
	options                        DesktopOptions
	service                        *Service
	phase, account, route, blocker string
	closed                         bool
}

func NewDesktopCoordinator(s *store.Store, o DesktopOptions) *DesktopCoordinator {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &DesktopCoordinator{store: s, options: o, service: New(s), phase: "unprepared"}
}

// WithService borrows the service only for the synchronous callback. Callers
// must not retain it. Close and phase changes wait until the borrow has ended.
func (c *DesktopCoordinator) WithService(f func(*Service)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f(c.service)
}
func (c *DesktopCoordinator) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return c.service.Close()
}
func (c *DesktopCoordinator) Status(account string) DesktopStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := DesktopStatus{Phase: "unprepared"}
	if c.closed {
		v.Blocker = "admission_unavailable"
		return v
	}
	if account != c.account {
		return v
	}
	v.Phase, v.RouteID, v.Blocker = c.phase, c.route, c.blocker
	v.ConnectionReady = c.service.ProductionConnectionStatus(account).Enabled
	catalog := c.service.ProductionCatalogStatus(account)
	v.CatalogReady = catalog.Enabled
	if v.Blocker == "" && c.phase == "catalog" {
		v.Blocker = catalog.Blocker
	}
	if v.Blocker == "" && c.phase == "connection" && !v.ConnectionReady {
		v.Blocker = "readiness_expired"
	}
	return v
}
func (c *DesktopCoordinator) Prepare(account, phase, route string, revision int64) error {
	// Concurrent preparation never queues another native probe or new authority.
	if !c.mu.TryLock() {
		return store.ErrConflict
	}
	defer c.mu.Unlock()
	if c.closed || c.blocker != "" || (phase != "connection" && phase != "catalog") {
		return ErrDisabled
	}
	c.service.sessionsMu.Lock()
	active := len(c.service.sessions) > 0
	c.service.sessionsMu.Unlock()
	if active {
		return store.ErrConflict
	}
	a, e := c.store.GetModelAccount(account)
	if e != nil || a.Revision != revision {
		return store.ErrConflict
	}
	r, e := c.store.GetModelRoute(route)
	if e != nil || r.AccountID != account || !a.Enabled || a.Provider != "openai" || a.AuthMethod != "openai_siwc" || a.BillingMode != "subscription" || domain.ValidateModelRoute(r, a) != nil || r.Environment != "live" || r.BaseURL != domain.OpenAISubscriptionRequestedResource || r.Adapter != "openai_siwc_responses" || r.ExtraUsage != "off" {
		return ErrDisabled
	}
	// Retire earlier authority before creating the next generation. Recover only
	// metadata; no login, refresh, catalog query or credential reconciliation.
	if e = c.service.Close(); e != nil {
		return e
	}
	c.service = New(c.store)
	c.account, c.route, c.phase = account, route, "unprepared"
	var next *Service
	if phase == "connection" {
		var expiry time.Time
		next = NewProductionConnection(c.store, ProductionConnectionOptions{Route: r, SIWC: c.options.SIWC, CredentialReady: func() bool { return !expiry.IsZero() && c.options.Now().Before(expiry) }})
		if next.ProductionConnectionStatus(account).Admission == nil || c.options.Probe == nil {
			next.Close()
			return ErrDisabled
		}
		if e = c.options.Probe(); e != nil {
			next.Close()
			c.blocker = "diagnostic_failed"
			return ErrDisabled
		}
		expiry = c.options.Now().Add(10 * time.Minute)
	} else {
		next = NewProductionCatalog(c.store, ProductionCatalogOptions{Route: r, Resolver: c.options.Resolver})
		if next.ProductionCatalogStatus(account).Admission == nil {
			next.Close()
			return ErrDisabled
		}
	}
	next.now = c.options.Now
	if e = next.Recover(); e != nil {
		next.Close()
		c.blocker = "admission_unavailable"
		return e
	}
	c.service, c.phase = next, phase
	return nil
}

// Handoff admits only the currently owned waiting operation's queryless
// link. The desktop process also enforces a one-launch budget for this URL.
func (c *DesktopCoordinator) Handoff(raw string) bool {
	if !safeHandoffURL(raw) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.phase != "connection" {
		return false
	}
	items, e := c.service.List(c.account)
	if e != nil {
		return false
	}
	for _, o := range items {
		if o.State == "running" {
			_, session, e := c.service.Read(c.account, o.ID)
			if e == nil && session != nil && session.State == "waiting" && session.HandoffURL == raw {
				return true
			}
		}
	}
	return false
}
