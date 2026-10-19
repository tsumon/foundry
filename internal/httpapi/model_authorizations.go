package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
)

type safeModelAuthorization struct {
	ID         string   `json:"id"`
	AccountID  string   `json:"account_id"`
	Issuer     string   `json:"issuer"`
	Subject    string   `json:"subject"`
	ClientID   string   `json:"client_id"`
	Scopes     []string `json:"scopes"`
	SpendClass string   `json:"spend_class"`
	State      string   `json:"state"`
	Generation int64    `json:"generation"`
	ExpiresAt  string   `json:"expires_at"`
}

type safeRemoteRevocation struct {
	AuthorizationID string `json:"authorization_id"`
	Outcome         string `json:"outcome"`
	UpdatedAt       string `json:"updated_at"`
}

type safeAuthorizationFlow struct {
	OperationID string `json:"operation_id"`
	State       string `json:"state"`
	HandoffURL  string `json:"handoff_url"`
	ExpiresAt   string `json:"expires_at"`
}

type safeAuthorizationRevocation struct {
	AuthorizationID string `json:"authorization_id"`
	LocalState      string `json:"local_state"`
	RemoteState     string `json:"remote_state"`
	UpdatedAt       string `json:"updated_at"`
}

func modelAuthorizationView(a domain.ModelAuthorization) safeModelAuthorization {
	return safeModelAuthorization{
		ID: a.Binding.ID, AccountID: a.Binding.AccountID,
		Issuer: a.Binding.Issuer, Subject: a.Binding.Subject, ClientID: a.Binding.ClientID,
		Scopes: append([]string{}, a.Binding.Scopes...), SpendClass: a.Binding.SpendClass,
		State: a.State, Generation: a.Generation, ExpiresAt: a.ExpiresAt,
	}
}

func authorizationFlowView(flow accounts.SIWCFlow) (safeAuthorizationFlow, bool) {
	view := safeAuthorizationFlow{
		OperationID: flow.OperationID, State: flow.State,
		HandoffURL: flow.HandoffURL, ExpiresAt: flow.ExpiresAt,
	}
	if domain.ValidateModelCredentialOperation(view.OperationID) != nil {
		return safeAuthorizationFlow{}, false
	}
	if _, err := time.Parse(time.RFC3339, view.ExpiresAt); err != nil {
		return safeAuthorizationFlow{}, false
	}
	switch view.State {
	case "waiting", "exchanging", "available", "declined", "failed", "canceled", "expired":
	default:
		return safeAuthorizationFlow{}, false
	}
	if view.HandoffURL == "" {
		return view, view.State != "waiting"
	}
	u, err := url.Parse(view.HandoffURL)
	if err != nil || view.State != "waiting" || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "/auth/start" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return safeAuthorizationFlow{}, false
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || u.Host != "127.0.0.1:"+strconv.Itoa(port) {
		return safeAuthorizationFlow{}, false
	}
	return view, true
}

func authorizationRevocationView(result accounts.SIWCRevokeResult) (safeAuthorizationRevocation, bool) {
	view := safeAuthorizationRevocation{
		AuthorizationID: result.AuthorizationID, LocalState: result.LocalState,
		RemoteState: result.RemoteState, UpdatedAt: result.UpdatedAt,
	}
	if domain.ValidateModelCredentialOperation(view.AuthorizationID) != nil || view.LocalState != "revoked" || view.RemoteState != "confirmed" && view.RemoteState != "unconfirmed" {
		return safeAuthorizationRevocation{}, false
	}
	if view.UpdatedAt != "" {
		if _, err := time.Parse(time.RFC3339, view.UpdatedAt); err != nil {
			return safeAuthorizationRevocation{}, false
		}
	}
	return view, true
}

// authorizationPartialRevocationView preserves only the local denial that the
// requested operation has already committed. A missing receipt can establish
// neither remote confirmation nor a timestamp.
func authorizationPartialRevocationView(requestedID string, result accounts.SIWCRevokeResult) *safeAuthorizationRevocation {
	if result.AuthorizationID != requestedID || result.LocalState != "revoked" {
		return nil
	}
	if result.RemoteState == "" {
		result.RemoteState = "unconfirmed"
	}
	view, valid := authorizationRevocationView(result)
	if !valid {
		return nil
	}
	return &view
}

type authorizationHTTPError struct {
	status  int
	code    string
	message string
}

func classifyModelAuthorizationError(err error) authorizationHTTPError {
	result := authorizationHTTPError{http.StatusServiceUnavailable, "model_authorization_unavailable", "model authorization is unavailable"}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return authorizationHTTPError{http.StatusNotFound, "not_found", "resource not found"}
	case errors.Is(err, store.ErrConflict):
		return authorizationHTTPError{http.StatusConflict, "conflict", "resource revision is stale"}
	case errors.Is(err, accounts.ErrInvalid), errors.Is(err, domain.ErrInvalidModelAuthorization), errors.Is(err, domain.ErrInvalidModelAccount):
		return authorizationHTTPError{http.StatusBadRequest, "invalid_model_authorization", "invalid model authorization request"}
	case errors.Is(err, accounts.ErrInProgress):
		return authorizationHTTPError{http.StatusConflict, "model_authorization_in_progress", "model authorization operation is in progress"}
	case errors.Is(err, accounts.ErrUnavailable), errors.Is(err, store.ErrModelAccountUnavailable), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return result
	}
	var provider *model.ProviderError
	if !errors.As(err, &provider) {
		return result
	}
	switch provider.Code {
	case "live_provider_access_pending_confirmation":
		return authorizationHTTPError{http.StatusConflict, provider.Code, "live provider access is pending confirmation"}
	case "refresh_not_ready":
		return authorizationHTTPError{http.StatusConflict, provider.Code, "model authorization refresh is not ready"}
	case "auth_invalid":
		return authorizationHTTPError{http.StatusUnauthorized, provider.Code, "provider authorization is invalid"}
	case "entitlement_denied":
		return authorizationHTTPError{http.StatusForbidden, provider.Code, "provider entitlement is denied"}
	case "quota_exhausted", "rate_limited":
		return authorizationHTTPError{http.StatusTooManyRequests, provider.Code, "provider authorization request is limited"}
	case "request_rejected":
		return authorizationHTTPError{http.StatusBadRequest, provider.Code, "provider authorization request was rejected"}
	case "server_error", "transport_unknown", "response_incomplete":
		return authorizationHTTPError{http.StatusBadGateway, provider.Code, "provider authorization did not complete"}
	default:
		return result
	}
}

func writeModelAuthorizationError(w http.ResponseWriter, err error, revocation *safeAuthorizationRevocation) {
	mapped := classifyModelAuthorizationError(err)
	body := map[string]any{"error": map[string]string{"code": mapped.code, "message": mapped.message}}
	if revocation != nil {
		body["revocation"] = *revocation
	}
	writeJSON(w, mapped.status, body)
}

func (s *server) modelAuthorizationsRoute(w http.ResponseWriter, r *http.Request) {
	query, ok := strictQuery(r)
	if !ok || len(query) != 0 {
		writeError(w, http.StatusBadRequest, "validation", "model authorization query is invalid")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/model-authorizations")
	if path == "" {
		if r.Method == http.MethodGet {
			s.listModelAuthorizations(w, r)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if path == "/start" && r.Method == http.MethodPost {
		var request struct {
			AccountID               string `json:"account_id"`
			ExpectedAccountRevision int64  `json:"expected_account_revision"`
		}
		if err := decodeAccountJSON(w, r, &request); err != nil {
			writeDecodeError(w, err)
			return
		}
		if request.AccountID == "" || request.ExpectedAccountRevision < 1 {
			writeModelAuthorizationError(w, accounts.ErrInvalid, nil)
			return
		}
		if s.rejectAcceptanceBypass(w, request.AccountID) {
			return
		}
		flow, err := s.siwc.Begin(r.Context(), request.AccountID, request.ExpectedAccountRevision)
		if err != nil {
			writeModelAuthorizationError(w, err, nil)
			return
		}
		view, valid := authorizationFlowView(flow)
		if !valid {
			writeModelAuthorizationError(w, accounts.ErrUnavailable, nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"flow": view})
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 2 && parts[0] == "operations" && r.Method == http.MethodGet {
		s.authorizationFlowStatus(w, parts[1])
		return
	}
	if len(parts) == 3 && parts[0] == "operations" && parts[2] == "cancel" && r.Method == http.MethodPost {
		var request struct{}
		if err := decodeAccountJSON(w, r, &request); err != nil {
			writeDecodeError(w, err)
			return
		}
		if domain.ValidateModelCredentialOperation(parts[1]) != nil {
			writeModelAuthorizationError(w, accounts.ErrInvalid, nil)
			return
		}
		flow, err := s.siwc.Cancel(r.Context(), parts[1])
		if err != nil {
			writeModelAuthorizationError(w, err, nil)
			return
		}
		view, valid := authorizationFlowView(flow)
		if !valid {
			writeModelAuthorizationError(w, accounts.ErrUnavailable, nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"flow": view})
		return
	}
	if len(parts) == 2 && (parts[1] == "refresh" || parts[1] == "revoke") && r.Method == http.MethodPost {
		s.authorizationLifecycleMutation(w, r, parts[0], parts[1])
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "route not found")
}

func (s *server) authorizationFlowStatus(w http.ResponseWriter, operationID string) {
	if domain.ValidateModelCredentialOperation(operationID) != nil {
		writeModelAuthorizationError(w, accounts.ErrInvalid, nil)
		return
	}
	flow, err := s.siwc.Status(operationID)
	if err != nil {
		writeModelAuthorizationError(w, err, nil)
		return
	}
	view, valid := authorizationFlowView(flow)
	if !valid {
		writeModelAuthorizationError(w, accounts.ErrUnavailable, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"flow": view})
}

func (s *server) authorizationLifecycleMutation(w http.ResponseWriter, r *http.Request, authorizationID, action string) {
	if domain.ValidateModelCredentialOperation(authorizationID) != nil {
		writeModelAuthorizationError(w, accounts.ErrInvalid, nil)
		return
	}
	var request struct {
		ExpectedAccountRevision int64 `json:"expected_account_revision"`
	}
	if err := decodeAccountJSON(w, r, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	if request.ExpectedAccountRevision < 1 {
		writeModelAuthorizationError(w, accounts.ErrInvalid, nil)
		return
	}
	if action == "refresh" {
		current, err := s.store.GetModelAuthorization(authorizationID)
		if err != nil {
			writeModelAuthorizationError(w, err, nil)
			return
		}
		if s.rejectAcceptanceBypass(w, current.Binding.AccountID) {
			return
		}
		authorization, err := s.siwc.Refresh(r.Context(), authorizationID, request.ExpectedAccountRevision)
		if err != nil {
			writeModelAuthorizationError(w, err, nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"authorization": modelAuthorizationView(authorization)})
		return
	}
	// Dedicated acceptance accounts belong to a different owned issuer. Keep
	// the existing safety control local-only rather than sending its token to
	// the global SIWC transport. Remote revocation remains unconfirmed.
	revoker := s.siwc
	current, err := s.store.GetModelAuthorization(authorizationID)
	if err != nil {
		writeModelAuthorizationError(w, err, nil)
		return
	}
	if s.acceptance.OwnsAccount(current.Binding.AccountID) {
		revoker = accounts.NewSIWCService(s.accounts)
	}
	result, err := revoker.Revoke(r.Context(), authorizationID, request.ExpectedAccountRevision)
	if err != nil {
		view := authorizationPartialRevocationView(authorizationID, result)
		writeModelAuthorizationError(w, err, view)
		return
	}
	view, valid := authorizationRevocationView(result)
	if !valid || view.AuthorizationID != authorizationID || view.UpdatedAt == "" {
		writeModelAuthorizationError(w, accounts.ErrUnavailable, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revocation": view})
}

func (s *server) listModelAuthorizations(w http.ResponseWriter, r *http.Request) {
	query, ok := strictQuery(r)
	if !ok || len(query) != 0 {
		writeError(w, http.StatusBadRequest, "validation", "model authorization query is invalid")
		return
	}
	authorizations, err := s.store.ListModelAuthorizations()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "model_authorization_storage_unavailable", "model authorization storage is unavailable")
		return
	}
	views := make([]safeModelAuthorization, 0, len(authorizations))
	revocations := make([]safeRemoteRevocation, 0)
	for _, authorization := range authorizations {
		views = append(views, modelAuthorizationView(authorization))
		record, readErr := s.store.GetSIWCRemoteRevocation(authorization.Binding.ID)
		if errors.Is(readErr, store.ErrNotFound) {
			continue
		}
		if readErr != nil {
			writeError(w, http.StatusServiceUnavailable, "model_authorization_storage_unavailable", "model authorization storage is unavailable")
			return
		}
		revocations = append(revocations, safeRemoteRevocation{
			AuthorizationID: record.AuthorizationID, Outcome: record.Outcome, UpdatedAt: record.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authorizations": views, "remote_revocations": revocations,
	})
}
