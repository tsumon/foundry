package store

import (
	"database/sql"
	"errors"
	"foundry/internal/domain"
	"time"
)

var ErrModelEntitlementDenied = errors.New("model_entitlement_denied")

// ResolveModelCredentialMetadata validates the complete frozen route and exact
// credential identity in one read transaction. It never reads secret contents.
func (s *Store) ResolveModelCredentialMetadata(sel domain.RoleModelSelection) (domain.ModelCredentialVersion, domain.ModelAuthorization, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ModelCredentialVersion{}, domain.ModelAuthorization{}, err
	}
	defer tx.Rollback()
	return resolveModelCredentialMetadataTx(tx, sel)
}

func resolveModelCredentialMetadataTx(tx *sql.Tx, sel domain.RoleModelSelection) (domain.ModelCredentialVersion, domain.ModelAuthorization, error) {
	return resolveModelCredentialMetadataReadTx(tx, sel, false)
}

// Policy evaluation must distinguish unreadable storage from an unavailable
// candidate. Existing execution callers retain their fail-closed denial API.
func resolveModelCredentialMetadataReadTx(tx *sql.Tx, sel domain.RoleModelSelection, strictReadErrors bool) (domain.ModelCredentialVersion, domain.ModelAuthorization, error) {
	var key domain.ModelCredentialVersion
	var auth domain.ModelAuthorization
	deny := func() (domain.ModelCredentialVersion, domain.ModelAuthorization, error) {
		return key, auth, ErrModelAccountUnavailable
	}
	p := sel.Profile
	if sel.Account == nil || sel.Route == nil || p.AccountID == "" || p.AccountID != sel.Account.ID || p.RouteID != sel.Route.ID || p.BaseURL != sel.Route.BaseURL || p.Protocol != sel.Route.Protocol || p.HasKey {
		return deny()
	}
	a, err := modelAccountTx(tx, p.AccountID)
	if strictReadErrors && err != nil && !errors.Is(err, ErrNotFound) {
		return key, auth, err
	}
	if err != nil || !a.Enabled {
		return deny()
	}
	frozen := sel.Account
	if a.Provider != frozen.Provider || a.AuthMethod != frozen.AuthMethod || a.BillingMode != frozen.BillingMode || a.BaseURL != frozen.BaseURL {
		return deny()
	}
	route, err := modelRouteTx(tx, p.RouteID)
	if strictReadErrors && err != nil && !errors.Is(err, ErrNotFound) {
		return key, auth, err
	}
	if err != nil || route != *sel.Route || domain.ValidateModelRoute(route, a) != nil {
		return deny()
	}
	if route.SpendClass != "explicit_api" && route.ExtraUsage != "off" {
		return key, auth, ErrModelEntitlementDenied
	}
	if a.AuthMethod == "api_key" {
		if p.CredentialVersion < 1 || sel.Authorization != nil {
			return deny()
		}
		key, err = modelCredentialTx(tx, a.ID, p.CredentialVersion)
		if strictReadErrors && err != nil && !errors.Is(err, ErrNotFound) {
			return key, auth, err
		}
		if err != nil || key.State != "available" || key.CleanupPending {
			return deny()
		}
		return key, auth, nil
	}
	if p.CredentialVersion != 0 || sel.Authorization == nil {
		return deny()
	}
	auth, err = modelAuthorizationTx(tx, sel.Authorization.ID)
	if strictReadErrors && err != nil && !errors.Is(err, ErrNotFound) {
		return key, auth, err
	}
	if err != nil || auth.Binding.AccountID != a.ID || !domain.SameModelAuthorizationBinding(auth.Binding, *sel.Authorization) || auth.State != "available" {
		return deny()
	}
	expiry, err := time.Parse(time.RFC3339, auth.ExpiresAt)
	if err != nil || !expiry.After(time.Now()) {
		return deny()
	}
	var unresolved int
	err = tx.QueryRow(`SELECT count(*) FROM model_authorization_operations WHERE account_id=? AND (state='pending' OR cleanup_pending=1)`, a.ID).Scan(&unresolved)
	if strictReadErrors && err != nil {
		return key, auth, err
	}
	if err != nil || unresolved != 0 {
		return deny()
	}
	if !domain.ModelAuthorizationAllowsInference(auth.Binding) {
		return key, auth, ErrModelEntitlementDenied
	}
	return key, auth, nil
}
