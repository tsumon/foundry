package model

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"foundry/internal/domain"
)

// RouterOptions pins individually owned simulation origins. Production task
// transport additionally requires a trusted, invocation-bound engine callback;
// an endpoint, selection or environment flag alone never enables it.
type RouterOptions struct {
	SimulationOrigins []string
	// CodingAdmission is a trusted engine callback, never request/config data.
	// It validates a durable claimed invocation and the exact sealed input.
	CodingAdmission func(context.Context, Request) error
}

type Router struct {
	legacy          Gateway
	resolver        CredentialResolver
	origins         map[string]bool
	http            *http.Client
	codingAdmission func(context.Context, Request) error
	dispatchMu      sync.Mutex
	dispatched      map[string]bool
}

func NewRouter(legacy Gateway, resolver CredentialResolver, options RouterOptions) *Router {
	r := &Router{legacy: legacy, resolver: resolver, origins: make(map[string]bool), http: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errProviderRedirect }}}
	r.codingAdmission, r.dispatched = options.CodingAdmission, make(map[string]bool)
	// Disable environment proxies: even test credentials must only reach the pinned listener.
	// No reused connections: net/http otherwise has implicit stale-connection
	// replay paths, including GET discovery. Never inherit mutable global hooks.
	r.http.Transport = &http.Transport{DialContext: (&net.Dialer{Timeout: 30 * time.Second}).DialContext, DisableKeepAlives: true, ResponseHeaderTimeout: 90 * time.Second}
	for _, origin := range options.SimulationOrigins {
		if validSimulationOrigin(origin) {
			r.origins[origin] = true
		}
	}
	return r
}

// SimulationEnabled reports constructor-pinned simulation availability for
// capability displays. It neither permits live access nor probes any endpoint.
func (r *Router) SimulationEnabled() bool { return len(r.origins) > 0 }

func validSimulationOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port > 0 && port <= 65535 && origin == "http://127.0.0.1:"+strconv.Itoa(port)
}

// PreflightAccountProfile preserves the old account-v1 denial interface.
func (r *Router) PreflightAccountProfile(_ context.Context, p domain.Profile) error {
	if p.AccountID != "" || p.RouteID != "" || p.AccountRevision != 0 || p.CredentialVersion != 0 {
		return ErrAccountInferenceDisabled
	}
	return nil
}

// PreflightModelSelection performs no secret reads or network I/O. The resolver
// independently rechecks current account/authorization state when leasing.
func (r *Router) PreflightModelSelection(ctx context.Context, schema int, s domain.RoleModelSelection) error {
	if ctx.Err() != nil {
		return providerFailure("request_rejected", "not_sent")
	}
	p := s.Profile
	if p.AccountID == "" {
		if p.RouteID != "" || p.AccountRevision != 0 || p.CredentialVersion != 0 || s.Account != nil || s.Route != nil || s.Authorization != nil {
			return providerFailure("request_rejected", "not_sent")
		}
		return nil
	}
	if schema != 2 || s.Account == nil || s.Route == nil {
		return providerFailure("request_rejected", "not_sent")
	}
	a, route := s.Account, s.Route
	// Gate live traffic before credentials, regardless of configuration validity.
	if route.Environment == "live" && r.codingAdmission == nil {
		return providerFailure("live_provider_access_pending_confirmation", "not_sent")
	}
	if p.ID == "" || strings.TrimSpace(p.Model) == "" || p.HasKey || a.ID != p.AccountID || a.Revision < 1 || a.Revision != p.AccountRevision || p.BaseURL != a.BaseURL || p.CredentialVersion != a.CurrentCredentialVersion || p.RouteID != route.ID || p.Protocol != route.Protocol || domain.ValidateModelRoute(*route, *a) != nil {
		return providerFailure("request_rejected", "not_sent")
	}
	if r.codingAdmission != nil && a.Provider != "openai" {
		return providerFailure("request_rejected", "not_sent")
	}
	if route.Adapter == "openai_siwc_responses" && route.Environment != "live" && OpenAIPreparationEndpointPolicy(route.BaseURL) != "owned_simulation_required" {
		return providerFailure("request_rejected", "not_sent")
	}
	u, err := url.Parse(route.BaseURL)
	if err != nil || route.Environment != "live" && !r.origins[u.Scheme+"://"+u.Host] || strings.Contains(u.Path, "..") || strings.Contains(u.Path, "//") {
		return providerFailure("request_rejected", "not_sent")
	}
	if !a.Enabled {
		return providerFailure("entitlement_denied", "not_sent")
	}
	if route.SpendClass != "explicit_api" && route.ExtraUsage != "off" {
		return providerFailure("entitlement_denied", "not_sent")
	}
	if a.AuthMethod == "api_key" {
		if p.CredentialVersion < 1 || s.Authorization != nil {
			return providerFailure("request_rejected", "not_sent")
		}
		return nil
	}
	b := s.Authorization
	if p.CredentialVersion != 0 || b == nil || b.ID == "" || b.AccountID != a.ID || b.Issuer == "" || b.Subject == "" || b.ClientID == "" || b.SpendClass != route.SpendClass {
		return providerFailure("entitlement_denied", "not_sent")
	}
	scopes := map[string]bool{}
	for _, scope := range b.Scopes {
		scopes[scope] = true
	}
	if !scopes["resource.invoke"] || !scopes["chatgpt.tokens.use.direct"] {
		return providerFailure("entitlement_denied", "not_sent")
	}
	return nil
}

func (r *Router) Complete(ctx context.Context, request Request) (Result, error) {
	if r.codingAdmission != nil {
		if request.InvocationID == "" || request.Profile.AccountID == "" || r.codingAdmission(ctx, request) != nil {
			return Result{}, providerFailure("request_rejected", "not_sent")
		}
		r.dispatchMu.Lock()
		used := r.dispatched[request.InvocationID]
		if !used {
			r.dispatched[request.InvocationID] = true
		}
		r.dispatchMu.Unlock()
		if used {
			return Result{}, providerFailure("request_rejected", "not_sent")
		}
		ctx = context.WithValue(ctx, codingRequestKey{}, request)
	}
	if request.Selection != nil && request.Selection.Profile != request.Profile {
		return Result{}, providerFailure("request_rejected", "not_sent")
	}
	if request.Profile.AccountID == "" {
		selection := domain.RoleModelSelection{Profile: request.Profile}
		if request.Selection != nil {
			selection = *request.Selection
		}
		if err := r.PreflightModelSelection(ctx, request.SnapshotSchema, selection); err != nil {
			return Result{}, err
		}
		if r.legacy == nil {
			return Result{}, providerFailure("request_rejected", "not_sent")
		}
		return r.legacy.Complete(ctx, request)
	}
	if request.Selection == nil {
		return Result{}, providerFailure("request_rejected", "not_sent")
	}
	s := *request.Selection
	if err := r.PreflightModelSelection(ctx, request.SnapshotSchema, s); err != nil {
		return Result{}, err
	}
	if request.MaxOutputTokens < 1 {
		return Result{}, providerFailure("request_rejected", "not_sent")
	}
	endpoint, payload := routedPayload(request, s.Route.Adapter)
	response, generation, err := r.send(ctx, s, http.MethodPost, endpoint, payload)
	if err != nil {
		return Result{CredentialGeneration: generation}, err
	}
	defer response.Body.Close()
	result, err := r.decodeResponse(response.Response, s.Route.Adapter)
	if p, ok := err.(*ProviderError); ok && p.ResponseDiagnostic.Valid() && response.transport.Valid() {
		p.ResponseDiagnostic.Transport = response.transport
	}
	result.CredentialGeneration = generation
	return result, err
}
