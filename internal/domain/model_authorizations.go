package domain

import (
	"errors"
	"reflect"
	"strings"
	"unicode"
)

type ModelAuthorization struct {
	Binding    ModelAuthorizationBinding `json:"binding"`
	State      string                    `json:"state"`
	Generation int64                     `json:"generation"`
	ExpiresAt  string                    `json:"expires_at"`
	SecretRef  string                    `json:"-"`
}

type AuthorizationOperation struct {
	ID              string `json:"id"`
	AccountID       string `json:"account_id"`
	Kind            string `json:"kind"`
	AuthorizationID string `json:"authorization_id"`
	State           string `json:"state"`
	Generation      int64  `json:"generation"`
	SecretRef       string `json:"-"`
	CleanupPending  bool   `json:"cleanup_pending"`
	WriteClaimed    bool   `json:"-"`
}

var ErrInvalidModelAuthorization = errors.New("invalid_model_authorization")

func ValidateModelAuthorizationBinding(b ModelAuthorizationBinding) error {
	for _, value := range []string{b.ID, b.AccountID, b.Issuer, b.Subject, b.ClientID} {
		if strings.TrimSpace(value) == "" || len(value) > 2048 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return ErrInvalidModelAuthorization
		}
	}
	if (b.SpendClass != "subscription_only" && b.SpendClass != "identity_only") || len(b.Scopes) > 64 {
		return ErrInvalidModelAuthorization
	}
	seen := map[string]bool{}
	for _, scope := range b.Scopes {
		if scope == "" || len(scope) > 256 || strings.IndexFunc(scope, unicode.IsSpace) >= 0 || strings.IndexFunc(scope, unicode.IsControl) >= 0 || seen[scope] {
			return ErrInvalidModelAuthorization
		}
		seen[scope] = true
	}
	if b.SpendClass == "identity_only" {
		if !seen["openid"] {
			return ErrInvalidModelAuthorization
		}
		for scope := range seen {
			if scope != "openid" && scope != "profile" && scope != "email" {
				return ErrInvalidModelAuthorization
			}
		}
	}
	return nil
}

func SameModelAuthorizationBinding(a, b ModelAuthorizationBinding) bool {
	return reflect.DeepEqual(a, b)
}

func ModelAuthorizationAllowsInference(b ModelAuthorizationBinding) bool {
	if b.SpendClass != "subscription_only" {
		return false
	}
	invoke, direct := false, false
	for _, scope := range b.Scopes {
		invoke = invoke || scope == "resource.invoke"
		direct = direct || scope == "chatgpt.tokens.use.direct"
	}
	return invoke && direct
}
