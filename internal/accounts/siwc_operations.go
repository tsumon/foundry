package accounts

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
)

const (
	siwcAuthorizePath = "/api/accounts/authorize"
	siwcTokenPath     = "/api/accounts/oauth/token"
	siwcCallbackPath  = "/auth/callback"
	siwcHandoffPath   = "/auth/start"
)

var siwcRequestedScopes = []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}

var siwcIdentityRequestedScopes = []string{"openid", "profile", "email"}

const (
	siwcPurposeSubscription = "subscription_only"
	siwcPurposeIdentity     = "identity_only"
)

func randomSIWCValue(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *SIWCService) beginSimulation(ctx, flowParent context.Context, accountID string, expectedRevision int64, purpose string, ownedStop *func()) (SIWCFlow, error) {
	if purpose != siwcPurposeSubscription && purpose != siwcPurposeIdentity {
		return SIWCFlow{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return SIWCFlow{}, safeError(err)
	}
	account, err := s.lifecycle.store.GetModelAccount(accountID)
	if err != nil || account.Revision != expectedRevision || !account.Enabled || account.Provider != "openai" || account.AuthMethod != "openai_siwc" || account.BillingMode != "subscription" || !validSIWCResource(account.BaseURL) {
		return SIWCFlow{}, ErrInvalid
	}
	clientID := "dynamic_agent_client"
	current, currentErr := s.lifecycle.store.CurrentModelAuthorization(accountID)
	if currentErr == nil {
		if current.Binding.AccountID != accountID || current.Binding.Issuer != s.options.Issuer || current.Binding.ClientID == "" || purpose == siwcPurposeIdentity && current.Binding.SpendClass != siwcPurposeIdentity {
			return SIWCFlow{}, ErrInvalid
		}
		clientID = current.Binding.ClientID
	} else if !errors.Is(currentErr, store.ErrNotFound) {
		return SIWCFlow{}, safeError(currentErr)
	}
	if err = ctx.Err(); err != nil {
		return SIWCFlow{}, safeError(err)
	}
	hostID, err := s.lifecycle.store.GetOrCreateSIWCHostID()
	if err != nil {
		return SIWCFlow{}, safeError(err)
	}
	operationID, err := opaqueID("siwc-signin-")
	if err != nil {
		return SIWCFlow{}, safeError(err)
	}
	authorizationID, err := opaqueID("siwc-authorization-")
	if err != nil {
		return SIWCFlow{}, safeError(err)
	}
	state, err := randomSIWCValue(32)
	if err != nil {
		return SIWCFlow{}, safeError(err)
	}
	nonce, err := randomSIWCValue(32)
	if err != nil {
		return SIWCFlow{}, safeError(err)
	}
	verifier, err := randomSIWCValue(48)
	if err != nil {
		return SIWCFlow{}, safeError(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return SIWCFlow{}, ErrUnavailable
	}
	host := listener.Addr().String()
	redirectURI := "http://" + host + siwcCallbackPath
	expires := time.Now().UTC().Add(s.options.FlowTimeout)
	flowContext, cancel := context.WithDeadline(flowParent, expires)
	attempt := &siwcAttempt{
		public: SIWCFlow{OperationID: operationID, State: "waiting", HandoffURL: "http://" + host + siwcHandoffPath, ExpiresAt: expires.Format(time.RFC3339)},
		cancel: cancel, listener: listener, account: account, authorizationID: authorizationID,
		state: state, nonce: nonce, verifier: verifier, redirectURI: redirectURI, clientID: clientID, purpose: purpose,
	}
	challenge := sha256.Sum256([]byte(verifier))
	requestedScopes := siwcRequestedScopes
	if purpose == siwcPurposeIdentity {
		requestedScopes = siwcIdentityRequestedScopes
	}
	query := url.Values{
		"client_id":             {clientID},
		"ext_agent_host_id":     {hostID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(requestedScopes, " ")},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge_method": {"S256"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
	}
	if purpose != siwcPurposeIdentity {
		query.Set("resource", account.BaseURL)
	}
	if clientID == "dynamic_agent_client" {
		query.Set("agent_name_hint", "FOUNDRY")
	}
	attempt.authorizationURL = s.options.AuthorizationOrigin + siwcAuthorizePath + "?" + query.Encode()
	mux := http.NewServeMux()
	mux.HandleFunc(siwcHandoffPath, func(w http.ResponseWriter, r *http.Request) { s.handleSIWCHandoff(attempt, host, w, r) })
	mux.HandleFunc(siwcCallbackPath, func(w http.ResponseWriter, r *http.Request) { s.handleSIWCCallback(flowContext, attempt, host, w, r) })
	attempt.server = &http.Server{
		Handler: mux, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Second, MaxHeaderBytes: 32 << 10,
		ErrorLog: log.New(io.Discard, "", 0),
	}

	s.mu.Lock()
	if len(s.flows) >= 64 {
		for id, existing := range s.flows {
			if existing.public.State != "waiting" && existing.public.State != "exchanging" {
				delete(s.flows, id)
				if len(s.flows) < 64 {
					break
				}
			}
		}
		if len(s.flows) >= 64 {
			s.mu.Unlock()
			stopSIWCAttempt(attempt, false)
			return SIWCFlow{}, ErrInProgress
		}
	}
	s.flows[operationID] = attempt
	result := attempt.public
	s.mu.Unlock()

	if ownedStop != nil {
		*ownedStop = func() { s.stopOwnedSIWCAttempt(attempt) }
	}
	go func() { _ = attempt.server.Serve(listener) }()
	go func() {
		<-flowContext.Done()
		s.mu.Lock()
		expired := false
		if attempt.public.State == "waiting" || attempt.public.State == "exchanging" {
			attempt.public.State = "expired"
			attempt.public.HandoffURL = ""
			expired = true
		}
		s.mu.Unlock()
		if expired {
			stopSIWCAttempt(attempt, false)
		}
	}()
	return result, nil
}

func validSIWCResource(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Port() != ""
}

func (s *SIWCService) handleSIWCHandoff(attempt *siwcAttempt, host string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.Host != host || r.URL.Path != siwcHandoffPath || r.URL.RawQuery != "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	waiting := attempt.public.State == "waiting"
	location := attempt.authorizationURL
	s.mu.Unlock()
	if !waiting {
		http.Error(w, "authorization unavailable", http.StatusGone)
		return
	}
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusFound)
}

func singleSIWCQuery(raw string, allowed map[string]bool) (url.Values, bool) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, false
	}
	for key, list := range values {
		if !allowed[key] || len(list) != 1 {
			return nil, false
		}
	}
	return values, true
}

func (s *SIWCService) handleSIWCCallback(ctx context.Context, attempt *siwcAttempt, host string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.Host != host || r.URL.Path != siwcCallbackPath {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	values, ok := singleSIWCQuery(r.URL.RawQuery, map[string]bool{"code": true, "state": true, "client_id": true, "scope": true, "error": true, "error_description": true})
	if !ok || values.Get("state") == "" || values.Get("state") != attempt.state {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	hasCode, hasError := values.Has("code"), values.Has("error")
	if hasCode == hasError || hasError && (values.Get("error") == "" || values.Has("client_id") || values.Has("scope")) || hasCode && values.Has("error_description") {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if attempt.public.State != "waiting" {
		s.mu.Unlock()
		http.Error(w, "callback consumed", http.StatusConflict)
		return
	}
	if values.Get("error") != "" {
		attempt.public.State = "declined"
		attempt.public.HandoffURL = ""
		s.mu.Unlock()
		stopSIWCAttempt(attempt, true)
		w.WriteHeader(http.StatusOK)
		return
	}
	code := values.Get("code")
	clientID := values.Get("client_id")
	if !safeSIWCCallbackValue(code, 8192) || len(values.Get("scope")) > 4096 ||
		attempt.clientID == "dynamic_agent_client" && (!safeSIWCCallbackValue(clientID, 2048) || clientID == "dynamic_agent_client") ||
		attempt.clientID != "dynamic_agent_client" && clientID != "" && clientID != attempt.clientID {
		attempt.public.State = "failed"
		attempt.public.HandoffURL = ""
		s.mu.Unlock()
		stopSIWCAttempt(attempt, true)
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	if attempt.clientID != "dynamic_agent_client" {
		clientID = attempt.clientID
	}
	attempt.public.State = "exchanging"
	attempt.public.HandoffURL = ""
	attempt.exchangeDone = make(chan struct{})
	s.mu.Unlock()
	defer close(attempt.exchangeDone)

	err := s.exchangeSIWCCode(ctx, attempt, code, clientID, values.Get("scope"), values.Has("scope"))
	s.mu.Lock()
	if attempt.public.State != "canceled" && attempt.public.State != "expired" {
		if err == nil {
			attempt.public.State = "available"
		} else {
			attempt.public.State = "failed"
		}
	}
	s.mu.Unlock()
	stopSIWCAttempt(attempt, true)
	if err != nil {
		http.Error(w, "authorization failed", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Authorization complete. You may close this window."))
}

func safeSIWCCallbackValue(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func (s *SIWCService) refreshSimulation(ctx context.Context, authorizationID string, expectedAccountRevision int64) (result domain.ModelAuthorization, resultErr error) {
	if err := ctx.Err(); err != nil {
		return result, safeError(err)
	}
	authorization, err := s.lifecycle.store.GetModelAuthorization(authorizationID)
	if err != nil || authorization.State != "available" || authorization.Binding.Issuer != s.options.Issuer {
		return result, ErrUnavailable
	}
	if authorization.Binding.SpendClass == siwcPurposeIdentity {
		return result, siwcFailure("entitlement_denied", "not_sent")
	}
	if !hasSIWCScope(authorization.Binding.Scopes, "offline_access") {
		return result, siwcFailure("entitlement_denied", "not_sent")
	}
	account, err := s.lifecycle.store.GetModelAccount(authorization.Binding.AccountID)
	if err != nil || account.Revision != expectedAccountRevision || !account.Enabled || account.Provider != "openai" || account.AuthMethod != "openai_siwc" || account.BaseURL == "" {
		return result, ErrUnavailable
	}
	current, err := s.lifecycle.store.CurrentModelAuthorization(account.ID)
	if err != nil || current.Binding.ID != authorizationID || current.State != "available" {
		return result, ErrUnavailable
	}
	if err = s.preflightSIWCRefresh(ctx, authorization, account); err != nil {
		return result, err
	}
	operationID, err := opaqueID("siwc-refresh-")
	if err != nil {
		return result, safeError(err)
	}
	if _, err = s.lifecycle.BeginModelAuthorization(ctx, account.ID, operationID, "refresh", authorizationID); err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = s.lifecycle.FailModelAuthorization(context.Background(), operationID, "refresh_unknown")
		}
	}()
	previous, err := s.lifecycle.ReadModelAuthorizationRefresh(ctx, operationID)
	if err != nil || previous.RefreshToken == "" || previous.Resource != account.BaseURL {
		return result, ErrUnavailable
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {authorization.Binding.ClientID},
		"refresh_token": {previous.RefreshToken},
		"resource":      {previous.Resource},
	}
	tokens, err := s.requestSIWCToken(ctx, form, siwcPurposeSubscription)
	if err != nil {
		if terminalSIWCRefreshError(err) {
			if _, revokeErr := s.lifecycle.RevokeModelAuthorization(context.Background(), authorizationID); revokeErr != nil {
				return result, revokeErr
			}
			committed = true
		}
		return result, err
	}
	if tokens.RefreshToken == "" {
		return result, siwcFailure("response_incomplete", "unknown")
	}
	orderedScopes, err := validateSIWCGrant(tokens.Scope, "", authorization.Binding.Scopes)
	if err != nil {
		return result, siwcFailure("entitlement_denied", "rejected")
	}
	discovery, err := s.fetchSIWCDiscovery(ctx)
	if err != nil {
		return result, err
	}
	claims, err := s.validateSIWCIDToken(ctx, discovery.JWKSURI, tokens.IDToken, authorization.Binding.ClientID, "")
	if err != nil || claims.Issuer != authorization.Binding.Issuer || claims.Subject != authorization.Binding.Subject {
		return result, siwcFailure("auth_invalid", "rejected")
	}
	binding := authorization.Binding
	binding.Scopes = orderedScopes
	if err = validateSIWCMetadataNoTokenReflection(binding, tokens); err != nil {
		return result, err
	}
	if err = s.revalidateSIWCAccount(account); err != nil {
		return result, err
	}
	expiresAt := time.Now().UTC().Add(time.Duration(tokens.ExpiresIn) * time.Second).Format(time.RFC3339)
	result, err = s.lifecycle.CommitModelAuthorization(ctx, operationID, binding, ModelTokenBundle{
		AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, IDToken: tokens.IDToken,
		ExpiresAt: expiresAt, EarliestRefreshAt: tokens.EarliestRefreshAt, Resource: previous.Resource,
	})
	if err != nil {
		return domain.ModelAuthorization{}, err
	}
	committed = true
	return result, nil
}

func terminalSIWCRefreshError(err error) bool {
	var provider *model.ProviderError
	if !errors.As(err, &provider) || provider.HTTPStatus != http.StatusBadRequest {
		return false
	}
	switch provider.ProviderCode {
	case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
		return true
	default:
		return false
	}
}

// preflightSIWCRefresh reads only the scheduling field from the exact current
// bundle while holding the lifecycle mutex. A clearly early request remains
// available and never creates a durable refresh intent or reaches the issuer.
func (s *SIWCService) preflightSIWCRefresh(ctx context.Context, expected domain.ModelAuthorization, expectedAccount domain.ModelAccount) error {
	s.lifecycle.mu.Lock()
	defer s.lifecycle.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return safeError(err)
	}
	current, err := s.lifecycle.store.CurrentModelAuthorization(expectedAccount.ID)
	if err != nil || current.State != "available" || current.Binding.ID != expected.Binding.ID || current.Generation != expected.Generation || current.SecretRef != expected.SecretRef {
		return ErrUnavailable
	}
	account, err := s.lifecycle.store.GetModelAccount(expectedAccount.ID)
	if err != nil || account.Revision != expectedAccount.Revision || !account.Enabled || account.BaseURL != expectedAccount.BaseURL || s.lifecycle.secrets == nil {
		return ErrUnavailable
	}
	raw, err := s.lifecycle.secrets.Get(current.SecretRef)
	if err != nil {
		return safeError(err)
	}
	var schedule struct {
		ExpiresAt         string `json:"expires_at"`
		EarliestRefreshAt string `json:"earliest_refresh_at"`
		Resource          string `json:"resource"`
	}
	if json.Unmarshal([]byte(raw), &schedule) != nil || schedule.Resource != account.BaseURL || schedule.ExpiresAt != current.ExpiresAt {
		return ErrUnavailable
	}
	if schedule.EarliestRefreshAt != "" {
		earliest, parseErr := time.Parse(time.RFC3339, schedule.EarliestRefreshAt)
		if parseErr != nil {
			return ErrUnavailable
		}
		if time.Now().UTC().Before(earliest) {
			return siwcFailure("refresh_not_ready", "not_sent")
		}
	}
	latest, err := s.lifecycle.store.CurrentModelAuthorization(expectedAccount.ID)
	if err != nil || latest.State != "available" || latest.Binding.ID != current.Binding.ID || latest.Generation != current.Generation || latest.SecretRef != current.SecretRef {
		return ErrUnavailable
	}
	return nil
}

func (s *SIWCService) revoke(ctx context.Context, authorizationID string, expectedAccountRevision int64) (SIWCRevokeResult, error) {
	var zero SIWCRevokeResult
	if s == nil || s.lifecycle == nil {
		return zero, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return zero, safeError(err)
	}
	authorization, err := s.lifecycle.store.GetModelAuthorization(authorizationID)
	if err != nil {
		return zero, safeError(err)
	}
	account, err := s.lifecycle.store.GetModelAccount(authorization.Binding.AccountID)
	if err != nil || account.Revision != expectedAccountRevision || account.AuthMethod != "openai_siwc" {
		return zero, ErrInvalid
	}
	if authorization.State == "revoked" {
		winner, claimErr := s.lifecycle.store.ClaimSIWCRemoteRevocation(authorizationID)
		if claimErr != nil {
			return zero, safeError(claimErr)
		}
		result, receiptErr := s.siwcExistingRevokeResult(authorizationID)
		if receiptErr != nil {
			return result, receiptErr
		}
		// Winning a previously absent claim proves there is no active sender
		// relying on the old token. Recovery may now finish local cleanup.
		if winner {
			s.lifecycle.mu.Lock()
			cleanupErr := s.lifecycle.cleanupAuthorizations(ctx)
			s.lifecycle.mu.Unlock()
			if cleanupErr != nil {
				return result, cleanupErr
			}
		}
		if !s.simulation {
			return result, liveSIWCDenial()
		}
		return result, nil
	}
	if !s.simulation {
		revoked, revokeErr := s.lifecycle.RevokeModelAuthorization(ctx, authorizationID)
		if revokeErr != nil {
			if revoked.Binding.ID == authorizationID && revoked.State == "revoked" {
				result := SIWCRevokeResult{AuthorizationID: authorizationID, LocalState: "revoked", RemoteState: "unconfirmed"}
				if receipt, receiptErr := s.lifecycle.store.GetSIWCRemoteRevocation(authorizationID); receiptErr == nil {
					result.RemoteState = receipt.Outcome
					result.UpdatedAt = receipt.UpdatedAt
				}
				return result, revokeErr
			}
			return zero, revokeErr
		}
		receipt, recordErr := s.lifecycle.store.RecordSIWCRemoteRevocation(authorizationID, "unconfirmed")
		result := SIWCRevokeResult{AuthorizationID: authorizationID, LocalState: "revoked", RemoteState: receipt.Outcome, UpdatedAt: receipt.UpdatedAt}
		if recordErr != nil {
			return result, safeError(recordErr)
		}
		return result, liveSIWCDenial()
	}
	s.lifecycle.mu.Lock()
	// The claim and local denial commit atomically. Only the durable winner
	// may read the token and attempt provider revocation.
	latest, winner, claimErr := s.lifecycle.store.ClaimAndRevokeSIWCRemoteAuthorization(authorizationID)
	if claimErr != nil {
		s.lifecycle.mu.Unlock()
		return zero, safeError(claimErr)
	}
	if !winner {
		s.lifecycle.mu.Unlock()
		return s.siwcExistingRevokeResult(authorizationID)
	}
	var revokeErr error
	var bundle storedModelTokenBundle
	if revokeErr == nil && s.lifecycle.secrets != nil {
		raw, getErr := s.lifecycle.secrets.Get(latest.SecretRef)
		if getErr != nil || json.Unmarshal([]byte(raw), &bundle) != nil || bundle.RefreshToken == "" || bundle.Resource != account.BaseURL {
			revokeErr = ErrUnavailable
		}
	}
	s.lifecycle.mu.Unlock()
	if revokeErr != nil {
		result, recordErr := s.finishSIWCRevoke(ctx, authorizationID, "unconfirmed")
		if recordErr != nil {
			return result, recordErr
		}
		return result, safeError(revokeErr)
	}

	discovery, remoteErr := s.fetchSIWCDiscovery(ctx)
	if remoteErr == nil {
		form := url.Values{"token": {bundle.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {authorization.Binding.ClientID}}
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, discovery.RevocationEndpoint, strings.NewReader(form.Encode()))
		if requestErr != nil {
			remoteErr = siwcFailure("request_rejected", "not_sent")
		} else {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response, sendErr := s.http.Do(request)
			if sendErr != nil {
				remoteErr = siwcFailure("transport_unknown", "unknown")
			} else {
				body, bodyErr := io.ReadAll(io.LimitReader(response.Body, 1025))
				response.Body.Close()
				if bodyErr != nil || len(body) > 1024 {
					remoteErr = siwcFailure("transport_unknown", "unknown")
				} else if response.StatusCode != http.StatusOK || len(bytes.TrimSpace(body)) != 0 {
					remoteErr = siwcFailure("server_error", "rejected")
				}
			}
		}
	}
	outcome := "confirmed"
	if remoteErr != nil {
		outcome = "unconfirmed"
	}
	result, finishErr := s.finishSIWCRevoke(ctx, authorizationID, outcome)
	if finishErr != nil {
		return result, finishErr
	}
	return result, remoteErr
}

func (s *SIWCService) siwcExistingRevokeResult(authorizationID string) (SIWCRevokeResult, error) {
	receipt, err := s.lifecycle.store.GetSIWCRemoteRevocation(authorizationID)
	if errors.Is(err, store.ErrNotFound) {
		receipt, err = s.lifecycle.store.RecordSIWCRemoteRevocation(authorizationID, "unconfirmed")
	}
	result := SIWCRevokeResult{AuthorizationID: authorizationID, LocalState: "revoked", RemoteState: receipt.Outcome, UpdatedAt: receipt.UpdatedAt}
	if err != nil {
		return result, safeError(err)
	}
	return result, nil
}

func (s *SIWCService) finishSIWCRevoke(ctx context.Context, authorizationID, outcome string) (SIWCRevokeResult, error) {
	receipt, err := s.lifecycle.store.RecordSIWCRemoteRevocation(authorizationID, outcome)
	result := SIWCRevokeResult{AuthorizationID: authorizationID, LocalState: "revoked", RemoteState: receipt.Outcome, UpdatedAt: receipt.UpdatedAt}
	if err != nil {
		return result, safeError(err)
	}
	s.lifecycle.mu.Lock()
	cleanupErr := s.lifecycle.cleanupAuthorizations(ctx)
	s.lifecycle.mu.Unlock()
	if cleanupErr != nil {
		return result, cleanupErr
	}
	return result, nil
}
