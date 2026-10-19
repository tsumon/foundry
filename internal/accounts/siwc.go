package accounts

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"foundry/internal/domain"
	"foundry/internal/model"
)

const (
	officialSIWCOrigin   = "https://auth.openai.com"
	officialSIWCIssuer   = "https://auth.openai.com"
	officialSIWCResource = "https://api.openai.com/v1"
)

var errSIWCRedirect = errors.New("siwc_redirect_rejected")

// SIWCOptions exists only for isolated, constructor-pinned loopback
// simulations. It deliberately has no live-access boolean.
type SIWCOptions struct {
	AuthorizationOrigin string
	TokenOrigin         string
	OIDCOrigin          string
	JWKSOrigin          string
	RevocationOrigin    string
	Issuer              string
	FlowTimeout         time.Duration
}

// SIWCFlow is the complete safe coordination DTO. OAuth state, nonce, PKCE,
// authorization URLs, callbacks and tokens remain private.
type SIWCFlow struct {
	OperationID string `json:"operation_id"`
	State       string `json:"state"`
	HandoffURL  string `json:"handoff_url"`
	ExpiresAt   string `json:"expires_at"`
}

type SIWCRevokeResult struct {
	AuthorizationID string `json:"authorization_id"`
	LocalState      string `json:"local_state"`
	RemoteState     string `json:"remote_state"`
	UpdatedAt       string `json:"updated_at"`
}

type siwcAttempt struct {
	public           SIWCFlow
	cancel           context.CancelFunc
	listener         net.Listener
	server           *http.Server
	authorizationURL string
	account          domain.ModelAccount
	authorizationID  string
	state            string
	nonce            string
	verifier         string
	redirectURI      string
	clientID         string
	purpose          string
	exchangeDone     chan struct{}
}

func stopSIWCAttempt(attempt *siwcAttempt, graceful bool) {
	attempt.cancel()
	if graceful && attempt.server != nil {
		go func() {
			shutdownContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := attempt.server.Shutdown(shutdownContext); err != nil {
				_ = attempt.server.Close()
			}
		}()
		return
	}
	if attempt.listener != nil {
		_ = attempt.listener.Close()
	}
	if attempt.server != nil {
		_ = attempt.server.Close()
	}
}

type SIWCService struct {
	lifecycle  *Service
	options    SIWCOptions
	simulation bool
	http       *http.Client
	mu         sync.Mutex
	flows      map[string]*siwcAttempt
}

func newSIWCHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: 20 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errSIWCRedirect },
	}
}

func NewSIWCService(lifecycle *Service) *SIWCService {
	return &SIWCService{
		lifecycle: lifecycle,
		options: SIWCOptions{
			AuthorizationOrigin: officialSIWCOrigin,
			TokenOrigin:         officialSIWCOrigin,
			OIDCOrigin:          officialSIWCOrigin,
			JWKSOrigin:          officialSIWCOrigin,
			RevocationOrigin:    officialSIWCOrigin,
			Issuer:              officialSIWCIssuer,
			FlowTimeout:         5 * time.Minute,
		},
		http:  newSIWCHTTPClient(),
		flows: make(map[string]*siwcAttempt),
	}
}

func NewSIWCSimulation(lifecycle *Service, options SIWCOptions) (*SIWCService, error) {
	if lifecycle == nil || !validSIWCLoopbackOrigin(options.AuthorizationOrigin) ||
		!validSIWCLoopbackOrigin(options.TokenOrigin) ||
		!validSIWCLoopbackOrigin(options.OIDCOrigin) ||
		!validSIWCLoopbackOrigin(options.JWKSOrigin) ||
		!validSIWCLoopbackOrigin(options.RevocationOrigin) ||
		!validSIWCLoopbackOrigin(options.Issuer) ||
		options.FlowTimeout < 10*time.Millisecond || options.FlowTimeout > 10*time.Minute {
		return nil, ErrInvalid
	}
	return &SIWCService{lifecycle: lifecycle, options: options, simulation: true, http: newSIWCHTTPClient(), flows: make(map[string]*siwcAttempt)}, nil
}

func validSIWCLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port > 0 && port <= 65535 && origin == "http://127.0.0.1:"+strconv.Itoa(port)
}

func (s *SIWCService) SimulationEnabled() bool { return s != nil && s.simulation }

// LegacySimulationEligible is the side-effect-free portion of the legacy
// subscription SIWC start guard. HTTP adds its owned-account and recovery
// gates before exposing this as an affirmative capability.
func (s *SIWCService) LegacySimulationEligible(account domain.ModelAccount) bool {
	return s != nil && s.simulation && account.Enabled && account.Provider == "openai" && account.AuthMethod == "openai_siwc" && account.BillingMode == "subscription" && validSIWCResource(account.BaseURL)
}

func liveSIWCDenial() error {
	return &model.ProviderError{Code: "live_provider_access_pending_confirmation", Certainty: "not_sent"}
}

func (s *SIWCService) Begin(ctx context.Context, accountID string, expectedAccountRevision int64) (SIWCFlow, error) {
	if s == nil || !s.simulation {
		return SIWCFlow{}, liveSIWCDenial()
	}
	return s.beginSimulation(ctx, context.Background(), accountID, expectedAccountRevision, siwcPurposeSubscription, nil)
}

// BeginIdentity starts only the constructor-pinned local identity simulation.
// Live identity connection remains independently blocked before any effects.
func (s *SIWCService) BeginIdentity(ctx context.Context, accountID string, expectedAccountRevision int64) (SIWCFlow, error) {
	if s == nil || !s.simulation {
		return SIWCFlow{}, liveSIWCDenial()
	}
	return s.beginSimulation(ctx, context.Background(), accountID, expectedAccountRevision, siwcPurposeIdentity, nil)
}

// BeginOwnedSimulation binds a local, programmatically driven flow to its
// execution context. The caller must defer the returned stop function, which
// cancels and joins any accepted callback, including local credential cleanup.
// Ordinary browser Begin deliberately retains its independent flow lifetime.
func (s *SIWCService) BeginOwnedSimulation(ctx context.Context, accountID string, expectedAccountRevision int64) (SIWCFlow, func(), error) {
	if s == nil || !s.simulation {
		return SIWCFlow{}, nil, liveSIWCDenial()
	}
	var stop func()
	flow, err := s.beginSimulation(ctx, ctx, accountID, expectedAccountRevision, siwcPurposeSubscription, &stop)
	return flow, stop, err
}

// BeginOwnedIdentitySimulation is the context-owned identity-only counterpart
// to BeginOwnedSimulation. Its stop function cancels and joins callbacks.
func (s *SIWCService) BeginOwnedIdentitySimulation(ctx context.Context, accountID string, expectedAccountRevision int64) (SIWCFlow, func(), error) {
	if s == nil || !s.simulation {
		return SIWCFlow{}, nil, liveSIWCDenial()
	}
	var stop func()
	flow, err := s.beginSimulation(ctx, ctx, accountID, expectedAccountRevision, siwcPurposeIdentity, &stop)
	return flow, stop, err
}

func (s *SIWCService) stopOwnedSIWCAttempt(attempt *siwcAttempt) {
	s.mu.Lock()
	if attempt.public.State == "waiting" || attempt.public.State == "exchanging" {
		attempt.public.State = "canceled"
		attempt.public.HandoffURL = ""
	}
	// Callback admission and capture of its completion channel use the same
	// lock: no credential-writing callback can start after this point.
	done := attempt.exchangeDone
	s.mu.Unlock()
	stopSIWCAttempt(attempt, false)
	if done != nil {
		<-done
	}
}

func (s *SIWCService) Status(operationID string) (SIWCFlow, error) {
	if s == nil {
		return SIWCFlow{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt, ok := s.flows[operationID]
	if !ok {
		return SIWCFlow{}, ErrUnavailable
	}
	return attempt.public, nil
}

func (s *SIWCService) Cancel(ctx context.Context, operationID string) (SIWCFlow, error) {
	if err := ctx.Err(); err != nil {
		return SIWCFlow{}, safeError(err)
	}
	s.mu.Lock()
	attempt, ok := s.flows[operationID]
	if !ok {
		s.mu.Unlock()
		return SIWCFlow{}, ErrUnavailable
	}
	if attempt.public.State == "waiting" || attempt.public.State == "exchanging" {
		attempt.public.State = "canceled"
		attempt.public.HandoffURL = ""
		stopSIWCAttempt(attempt, false)
	}
	result := attempt.public
	s.mu.Unlock()
	return result, nil
}

// Close cancels every active local browser handoff. It is safe to call more
// than once and never performs provider network work.
func (s *SIWCService) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	active := make([]*siwcAttempt, 0, len(s.flows))
	for _, attempt := range s.flows {
		if attempt.public.State == "waiting" || attempt.public.State == "exchanging" {
			attempt.public.State = "canceled"
			attempt.public.HandoffURL = ""
			active = append(active, attempt)
		}
	}
	s.mu.Unlock()
	for _, attempt := range active {
		stopSIWCAttempt(attempt, false)
	}
	return nil
}

func (s *SIWCService) Refresh(ctx context.Context, authorizationID string, expectedAccountRevision int64) (domain.ModelAuthorization, error) {
	if s == nil || !s.simulation {
		return domain.ModelAuthorization{}, liveSIWCDenial()
	}
	return s.refreshSimulation(ctx, authorizationID, expectedAccountRevision)
}

func (s *SIWCService) Revoke(ctx context.Context, authorizationID string, expectedAccountRevision int64) (SIWCRevokeResult, error) {
	return s.revoke(ctx, authorizationID, expectedAccountRevision)
}
