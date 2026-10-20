package acceptance

import (
	"context"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// ConnectionSession is an ephemeral, sanitized projection, never ledger data.
// OperationID is the acceptance id, not the private protocol attempt id.
type ConnectionSession struct {
	OperationID string `json:"operation_id"`
	AccountID   string `json:"account_id"`
	State       string `json:"state"`
	ExpiresAt   string `json:"expires_at"`
	HandoffURL  string `json:"handoff_url,omitempty"`
}

type connectionSession struct {
	mu              sync.Mutex // serializes cancellation admission and the sole receipt writer
	claim           domain.ModelAcceptanceOperation
	flow            accounts.SIWCFlow
	stop            func()
	done            chan struct{}
	err             error
	cancelRequested bool
}

func (s *Service) ConnectionMode(route string) string {
	if s.browserRoutes[route] {
		return "browser_handoff"
	}
	return ""
}

func (s *Service) startConnection(o domain.ModelAcceptanceOperation) (domain.ModelAcceptanceOperation, error) {
	// Request cancellation cannot own a human-login session. Constructor policy
	// has already been checked and the durable claim consumed before this call.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	flow, stop, err := s.browserSIWC.BeginOwnedSubscriptionConnectionSimulation(ctx, o.AccountID, o.Material.Account.Revision)
	if err != nil {
		cancel()
		return s.finish(o, err, nil, model.Result{})
	}
	session := &connectionSession{claim: o, flow: flow, done: make(chan struct{})}
	session.stop = func() { cancel(); stop() }
	s.sessionsMu.Lock()
	s.sessions[o.ID] = session
	s.sessionsMu.Unlock()
	go func() {
		terminal, waitErr := s.browserSIWC.Wait(flow.OperationID)
		session.stop() // joins callback cleanup before any receipt can be written
		session.mu.Lock()
		defer session.mu.Unlock()
		defer close(session.done)
		if waitErr == nil {
			switch terminal.State {
			case "available":
			case "declined":
				waitErr = &model.ProviderError{Code: "entitlement_denied", Certainty: "rejected"}
			default:
				waitErr = &model.ProviderError{Code: "transport_unknown", Certainty: "unknown"}
			}
		}
		if session.cancelRequested {
			waitErr = &model.ProviderError{Code: "transport_unknown", Certainty: "unknown"}
		}
		_, session.err = s.finish(o, waitErr, nil, model.Result{})
		// Only active work belongs in the controller. Keep captured waiters
		// valid, but release the stop closure (and its private SIWC attempt)
		// after both callback cleanup and the sole receipt writer have joined.
		s.sessionsMu.Lock()
		if s.sessions[o.ID] == session {
			delete(s.sessions, o.ID)
		}
		s.sessionsMu.Unlock()
	}()
	return o, nil
}

func safeHandoffURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "/auth/start" || u.RawPath != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port > 0 && port <= 65535 && raw == "http://127.0.0.1:"+strconv.Itoa(port)+"/auth/start"
}

// Read returns a consistent operation/session pair. A terminal, missing or
// recovered session never restores a link from durable history.
func (s *Service) Read(account, id string) (domain.ModelAcceptanceOperation, *ConnectionSession, error) {
	s.sessionsMu.Lock()
	session := s.sessions[id]
	s.sessionsMu.Unlock()
	if session != nil {
		session.mu.Lock()
		defer session.mu.Unlock()
	}
	o, err := s.Get(account, id)
	if err != nil || session == nil || o.State != "running" || !s.ready.Load() || session.claim.AccountID != account || session.claim.ClaimSequence != o.ClaimSequence || session.claim.Revision != o.Revision || session.claim.MaterialDigest != o.MaterialDigest {
		return o, nil, err
	}
	flow, err := s.browserSIWC.Status(session.flow.OperationID)
	if err != nil {
		return o, nil, nil
	}
	view := &ConnectionSession{OperationID: id, AccountID: account, State: flow.State, ExpiresAt: flow.ExpiresAt}
	expires, parseErr := time.Parse(time.RFC3339Nano, flow.ExpiresAt)
	if parseErr != nil {
		return o, nil, nil
	}
	if flow.State == "waiting" && expires.After(time.Now()) && safeHandoffURL(flow.HandoffURL) {
		view.HandoffURL = flow.HandoffURL
	}
	return o, view, nil
}

func (s *Service) Cancel(account, id string, revision int64, digest string) (domain.ModelAcceptanceOperation, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if _, err := s.owned(account, id); err != nil {
		return domain.ModelAcceptanceOperation{}, err
	}
	s.sessionsMu.Lock()
	session := s.sessions[id]
	s.sessionsMu.Unlock()
	if session == nil {
		return domain.ModelAcceptanceOperation{}, store.ErrConflict
	}
	session.mu.Lock()
	o, err := s.Get(account, id)
	if err != nil || session.cancelRequested || o.State != "running" || o.Revision != revision || o.Revision != session.claim.Revision || o.MaterialDigest != digest || digest != session.claim.MaterialDigest || domain.AcceptanceDigest(o.Material) != digest || o.ClaimSequence != session.claim.ClaimSequence || session.claim.AccountID != account {
		session.mu.Unlock()
		if err != nil {
			return s.mutationResult(o, err)
		}
		return domain.ModelAcceptanceOperation{}, store.ErrConflict
	}
	session.cancelRequested = true
	session.stop()
	session.mu.Unlock()
	<-session.done
	if session.err != nil {
		return domain.ModelAcceptanceOperation{}, session.err
	}
	return s.Get(account, id)
}

func (s *Service) stopConnections() {
	s.sessionsMu.Lock()
	active := make([]*connectionSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		active = append(active, session)
	}
	s.sessionsMu.Unlock()
	for _, session := range active {
		session.stop()
	}
	for _, session := range active {
		<-session.done
	}
}

// Close must precede account/protocol/storage shutdown. Recover uses the same
// join boundary before reconciling interrupted durable claims.
func (s *Service) Close() error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.ready.Store(false)
	s.stopConnections()
	return nil
}
