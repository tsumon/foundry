package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"strings"
	"time"
)

// ModelTokenBundle is backend-only and is encoded solely into the existing
// secret backend, never SQLite or an HTTP DTO. Do not marshal it for UI/logs.
type ModelTokenBundle struct {
	AccessToken       string `json:"-"`
	RefreshToken      string `json:"-"`
	IDToken           string `json:"-"`
	ExpiresAt         string `json:"-"`
	EarliestRefreshAt string `json:"-"`
	Resource          string `json:"-"`
}
type storedModelTokenBundle struct {
	AccessToken       string `json:"access_token"`
	RefreshToken      string `json:"refresh_token"`
	IDToken           string `json:"id_token"`
	ExpiresAt         string `json:"expires_at"`
	EarliestRefreshAt string `json:"earliest_refresh_at"`
	Resource          string `json:"resource"`
}

func bundleForStorage(b ModelTokenBundle) storedModelTokenBundle {
	return storedModelTokenBundle{b.AccessToken, b.RefreshToken, b.IDToken, b.ExpiresAt, b.EarliestRefreshAt, b.Resource}
}

func (s *Service) BeginModelAuthorization(ctx context.Context, accountID, operationID, kind, authorizationID string) (domain.AuthorizationOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.AuthorizationOperation{}, safeError(err)
	}
	if s.secrets == nil {
		return domain.AuthorizationOperation{}, ErrUnavailable
	}
	o, err := s.store.BeginModelAuthorization(accountID, operationID, kind, authorizationID)
	return o, safeError(err)
}

func (s *Service) CommitModelAuthorization(ctx context.Context, operationID string, b domain.ModelAuthorizationBinding, bundle ModelTokenBundle) (domain.ModelAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var zero domain.ModelAuthorization
	o, err := s.store.GetModelAuthorizationOperation(operationID)
	if err != nil {
		return zero, safeError(err)
	}
	if o.State != "pending" || o.AccountID != b.AccountID || o.AuthorizationID != b.ID {
		return zero, store.ErrConflict
	}
	// Validate all non-secret metadata before attempting the external write.
	account, err := s.store.GetModelAccount(b.AccountID)
	if err != nil {
		return zero, safeError(err)
	}
	expiry, expiryErr := time.Parse(time.RFC3339, bundle.ExpiresAt)
	identityOnly := b.SpendClass == siwcPurposeIdentity
	invalidBundle := bundle.AccessToken == "" || len(bundle.AccessToken) > 65536 || strings.ContainsAny(bundle.AccessToken, "\r\n")
	if identityOnly {
		invalidBundle = bundle.AccessToken != "" || bundle.RefreshToken != "" || bundle.IDToken == "" || len(bundle.IDToken) > 65536 || strings.ContainsAny(bundle.IDToken, "\r\n") || bundle.EarliestRefreshAt != ""
	}
	if domain.ValidateModelAuthorizationBinding(b) != nil || invalidBundle || expiryErr != nil || !expiry.After(time.Now()) || bundle.Resource != account.BaseURL {
		return zero, ErrInvalid
	}
	if bundle.EarliestRefreshAt != "" {
		if _, err = time.Parse(time.RFC3339, bundle.EarliestRefreshAt); err != nil {
			return zero, ErrInvalid
		}
	}
	if err = s.store.ClaimModelAuthorizationWrite(operationID); err != nil {
		return zero, safeError(err)
	}
	if err = ctx.Err(); err != nil {
		return zero, s.failAuthorization(o.ID, "failed", err)
	}
	if s.secrets == nil {
		return zero, s.failAuthorization(o.ID, "failed", ErrUnavailable)
	}
	encoded, err := json.Marshal(bundleForStorage(bundle))
	if err != nil {
		return zero, s.failAuthorization(o.ID, "failed", err)
	}
	if err = s.secrets.Set(o.SecretRef, string(encoded)); err != nil {
		return zero, s.failAuthorization(o.ID, "failed", err)
	}
	if err = ctx.Err(); err != nil {
		return zero, s.failAuthorization(o.ID, "failed", err)
	}
	result, err := s.store.CommitModelAuthorization(operationID, b, bundle.ExpiresAt)
	if err != nil {
		return zero, s.failAuthorization(o.ID, "failed", err)
	}
	if err = s.cleanupAuthorizations(ctx); err != nil {
		return zero, err
	}
	return result, nil
}

func (s *Service) failAuthorization(operationID, state string, cause error) error {
	if err := s.store.FinishModelAuthorizationWriteFailure(operationID, state); err != nil {
		return safeError(err)
	}
	if err := s.cleanupAuthorizations(context.Background()); err != nil {
		return err
	}
	return safeError(cause)
}
func (s *Service) FailModelAuthorization(ctx context.Context, operationID, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.FailModelAuthorization(operationID, state); err != nil {
		return safeError(err)
	}
	return s.cleanupAuthorizations(ctx)
}
func (s *Service) RevokeModelAuthorization(ctx context.Context, id string) (domain.ModelAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.ModelAuthorization{}, safeError(err)
	}
	a, err := s.store.RevokeModelAuthorization(id)
	if err != nil {
		return a, safeError(err)
	}
	if err = s.cleanupAuthorizations(ctx); err != nil {
		return a, err
	}
	return a, nil
}
func (s *Service) cleanupAuthorizations(ctx context.Context) error {
	pending, err := s.store.PendingModelAuthorizationCleanup()
	if err != nil {
		return safeError(err)
	}
	for _, o := range pending {
		// A failed/revoked writer may still complete its external Set. Keep its exact
		// cleanup intent until the writer finalizes, or startup recovery runs.
		if o.WriteClaimed {
			return ErrInProgress
		}
		if err = ctx.Err(); err != nil {
			return safeError(err)
		}
		if s.secrets == nil {
			return ErrUnavailable
		}
		if err = s.secrets.Delete(o.SecretRef); err != nil {
			return safeError(err)
		}
		if err = s.store.CompleteModelAuthorizationCleanup(o.ID); err != nil {
			return safeError(err)
		}
	}
	return nil
}

func credentialDenial(err error) error {
	code := "auth_invalid"
	if errors.Is(err, store.ErrModelEntitlementDenied) {
		code = "entitlement_denied"
	}
	return &model.ProviderError{Code: code, Certainty: "not_sent"}
}

func (s *Service) ResolveModelCredential(ctx context.Context, sel domain.RoleModelSelection) (model.CredentialLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var zero model.CredentialLease
	if err := ctx.Err(); err != nil {
		return zero, credentialDenial(err)
	}
	key, auth, err := s.store.ResolveModelCredentialMetadata(sel)
	if err != nil {
		return zero, credentialDenial(err)
	}
	if s.secrets == nil {
		return zero, credentialDenial(ErrUnavailable)
	}
	ref, generation := key.SecretRef, key.Version
	if auth.Binding.ID != "" {
		ref, generation = auth.SecretRef, auth.Generation
	}
	secret, err := s.secrets.Get(ref)
	if err != nil || secret == "" {
		return zero, credentialDenial(err)
	}
	if auth.Binding.ID != "" {
		var bundle storedModelTokenBundle
		if json.Unmarshal([]byte(secret), &bundle) != nil || bundle.AccessToken == "" || bundle.Resource != sel.Route.BaseURL || bundle.ExpiresAt != auth.ExpiresAt {
			return zero, credentialDenial(ErrInvalid)
		}
		secret = bundle.AccessToken
	}
	// Recheck after the secret read to close a concurrent service revoke/refresh.
	nextKey, nextAuth, err := s.store.ResolveModelCredentialMetadata(sel)
	if err != nil || nextKey.SecretRef != key.SecretRef || nextAuth.SecretRef != auth.SecretRef {
		return zero, credentialDenial(err)
	}
	if err = ctx.Err(); err != nil {
		return zero, credentialDenial(err)
	}
	return model.CredentialLease{Secret: secret, Generation: generation}, nil
}

var _ model.CredentialResolver = (*Service)(nil)

// ReadModelAuthorizationRefresh reads only the previous generation named by a
// durable refreshing intent. It is deliberately separate from inference leases.
func (s *Service) ReadModelAuthorizationRefresh(ctx context.Context, operationID string) (ModelTokenBundle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var zero ModelTokenBundle
	if err := ctx.Err(); err != nil {
		return zero, safeError(err)
	}
	op, err := s.store.GetModelAuthorizationOperation(operationID)
	if err != nil || op.Kind != "refresh" || op.State != "pending" || op.WriteClaimed {
		return zero, ErrUnavailable
	}
	auth, err := s.store.GetModelAuthorization(op.AuthorizationID)
	if err != nil || auth.State != "refreshing" || auth.Binding.AccountID != op.AccountID || auth.Generation >= op.Generation {
		return zero, ErrUnavailable
	}
	account, err := s.store.GetModelAccount(op.AccountID)
	if err != nil || !account.Enabled || s.secrets == nil {
		return zero, ErrUnavailable
	}
	raw, err := s.secrets.Get(auth.SecretRef)
	if err != nil {
		return zero, safeError(err)
	}
	var b storedModelTokenBundle
	if json.Unmarshal([]byte(raw), &b) != nil || b.RefreshToken == "" || b.Resource != account.BaseURL || b.ExpiresAt != auth.ExpiresAt {
		return zero, ErrUnavailable
	}
	latest, err := s.store.GetModelAuthorizationOperation(operationID)
	if err != nil || latest.State != "pending" || latest.WriteClaimed {
		return zero, ErrUnavailable
	}
	return ModelTokenBundle{b.AccessToken, b.RefreshToken, b.IDToken, b.ExpiresAt, b.EarliestRefreshAt, b.Resource}, nil
}
