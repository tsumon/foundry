package accounts

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"foundry/internal/domain"
	"foundry/internal/model"
)

const siwcJSONLimit = 1 << 20

type siwcDiscovery struct {
	Issuer             string `json:"issuer"`
	JWKSURI            string `json:"jwks_uri"`
	RevocationEndpoint string `json:"revocation_endpoint"`
}

type siwcTokenResponse struct {
	AccessToken       string
	RefreshToken      string
	IDToken           string
	TokenType         string
	ExpiresIn         int64
	Scope             string
	EarliestRefreshAt string
}

type siwcJWTClaims struct {
	Issuer    string
	Audience  string
	Subject   string
	Nonce     string
	Expires   int64
	NotBefore int64
}

func siwcFailure(code, certainty string) *model.ProviderError {
	return &model.ProviderError{Code: code, Certainty: certainty}
}

// validateSIWCMetadataNoTokenReflection is an exact-value boundary check, not
// generic content inspection. Provider-controlled identity claims and scopes
// may be persisted as non-secret metadata, so none may equal a token returned
// by the same exchange.
func validateSIWCMetadataNoTokenReflection(binding domain.ModelAuthorizationBinding, tokens siwcTokenResponse) error {
	metadata := []string{binding.ID, binding.AccountID, binding.Issuer, binding.Subject, binding.ClientID, binding.SpendClass}
	metadata = append(metadata, binding.Scopes...)
	for _, token := range []string{tokens.AccessToken, tokens.RefreshToken, tokens.IDToken} {
		if token == "" {
			continue
		}
		for _, value := range metadata {
			if value == token {
				return siwcFailure("auth_invalid", "rejected")
			}
		}
	}
	return nil
}

func (s *SIWCService) revalidateSIWCAccount(expected domain.ModelAccount) error {
	current, err := s.lifecycle.store.GetModelAccount(expected.ID)
	if err != nil || current.Revision != expected.Revision || !current.Enabled || current.Provider != "openai" || current.AuthMethod != "openai_siwc" || current.BillingMode != "subscription" || current.BaseURL != expected.BaseURL {
		return ErrUnavailable
	}
	return nil
}

func (s *SIWCService) exchangeSIWCCode(ctx context.Context, attempt *siwcAttempt, code, clientID, callbackScope string) (resultErr error) {
	if _, err := s.lifecycle.BeginModelAuthorization(ctx, attempt.account.ID, attempt.public.OperationID, "sign_in", attempt.authorizationID); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = s.lifecycle.FailModelAuthorization(context.Background(), attempt.public.OperationID, "failed")
		}
	}()
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"code_verifier": {attempt.verifier},
		"redirect_uri":  {attempt.redirectURI},
	}
	if attempt.purpose != siwcPurposeIdentity {
		form.Set("resource", attempt.account.BaseURL)
	}
	tokens, err := s.requestSIWCToken(ctx, form, attempt.purpose)
	if err != nil {
		return err
	}
	var scopes []string
	if attempt.purpose == siwcPurposeIdentity {
		if tokens.RefreshToken != "" {
			return siwcFailure("entitlement_denied", "rejected")
		}
		scopes, err = validateSIWCIdentityGrant(tokens.Scope, callbackScope)
	} else {
		scopes, err = validateSIWCGrant(tokens.Scope, callbackScope, nil)
	}
	if err != nil {
		return err
	}
	if attempt.purpose != siwcPurposeIdentity && hasSIWCScope(scopes, "offline_access") && tokens.RefreshToken == "" {
		return siwcFailure("response_incomplete", "unknown")
	}
	discovery, err := s.fetchSIWCDiscovery(ctx)
	if err != nil {
		return err
	}
	claims, err := s.validateSIWCIDToken(ctx, discovery.JWKSURI, tokens.IDToken, clientID, attempt.nonce)
	if err != nil {
		return err
	}
	if current, currentErr := s.lifecycle.store.CurrentModelAuthorization(attempt.account.ID); currentErr == nil {
		if current.Binding.Issuer != claims.Issuer || current.Binding.Subject != claims.Subject || current.Binding.ClientID != clientID || current.Binding.SpendClass != attempt.purpose {
			return siwcFailure("auth_invalid", "rejected")
		}
	}
	binding := domain.ModelAuthorizationBinding{ID: attempt.authorizationID, AccountID: attempt.account.ID, Issuer: claims.Issuer, Subject: claims.Subject, ClientID: clientID, SpendClass: attempt.purpose, Scopes: scopes}
	if err = validateSIWCMetadataNoTokenReflection(binding, tokens); err != nil {
		return err
	}
	if err = s.revalidateSIWCAccount(attempt.account); err != nil {
		return err
	}
	expiresAt := time.Now().UTC().Add(time.Duration(tokens.ExpiresIn) * time.Second).Format(time.RFC3339)
	bundle := ModelTokenBundle{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, IDToken: tokens.IDToken, ExpiresAt: expiresAt, EarliestRefreshAt: tokens.EarliestRefreshAt, Resource: attempt.account.BaseURL}
	if attempt.purpose == siwcPurposeIdentity {
		// Any optional access token was reflection-checked above, then discarded.
		bundle.AccessToken = ""
		bundle.RefreshToken = ""
		bundle.EarliestRefreshAt = ""
		bundle.ExpiresAt = time.Unix(claims.Expires, 0).UTC().Format(time.RFC3339)
	}
	_, err = s.lifecycle.CommitModelAuthorization(ctx, attempt.public.OperationID, binding, bundle)
	if err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SIWCService) requestSIWCToken(ctx context.Context, form url.Values, purpose string) (siwcTokenResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.options.TokenOrigin+siwcTokenPath, strings.NewReader(form.Encode()))
	if err != nil {
		return siwcTokenResponse{}, siwcFailure("request_rejected", "not_sent")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := s.http.Do(request)
	if err != nil {
		return siwcTokenResponse{}, siwcFailure("transport_unknown", "unknown")
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, siwcJSONLimit+1))
	if readErr != nil || len(body) > siwcJSONLimit {
		return siwcTokenResponse{}, siwcFailure("transport_unknown", "unknown")
	}
	if response.StatusCode != http.StatusOK {
		provider := siwcFailure("auth_invalid", "rejected")
		provider.HTTPStatus = response.StatusCode
		var envelope struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &envelope) == nil && (envelope.Error == "invalid_client" || envelope.Error == "invalid_grant" || envelope.Error == "invalid_refresh_token" || envelope.Error == "token_expired" || envelope.Error == "refresh_token_expired" || envelope.Error == "refresh_token_invalidated" || envelope.Error == "refresh_token_reused") {
			provider.ProviderCode = envelope.Error
		}
		if response.StatusCode >= 500 {
			provider.Code = "server_error"
		}
		return siwcTokenResponse{}, provider
	}
	if !isSIWCJSON(response.Header.Get("Content-Type")) {
		return siwcTokenResponse{}, siwcFailure("response_incomplete", "unknown")
	}
	var raw struct {
		AccessToken       string          `json:"access_token"`
		RefreshToken      string          `json:"refresh_token"`
		IDToken           string          `json:"id_token"`
		TokenType         string          `json:"token_type"`
		ExpiresIn         json.Number     `json:"expires_in"`
		Scope             string          `json:"scope"`
		EarliestRefreshAt json.RawMessage `json:"earliest_refresh_at"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&raw) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return siwcTokenResponse{}, siwcFailure("response_incomplete", "unknown")
	}
	if purpose == siwcPurposeIdentity {
		if !safeSIWCToken(raw.IDToken) || raw.RefreshToken != "" || raw.AccessToken != "" && !safeSIWCToken(raw.AccessToken) || raw.TokenType != "" && raw.TokenType != "Bearer" || len(raw.Scope) > 4096 || len(raw.EarliestRefreshAt) != 0 && !bytes.Equal(raw.EarliestRefreshAt, []byte("null")) {
			return siwcTokenResponse{}, siwcFailure("response_incomplete", "unknown")
		}
		return siwcTokenResponse{AccessToken: raw.AccessToken, IDToken: raw.IDToken, TokenType: raw.TokenType, Scope: raw.Scope}, nil
	}
	expires, err := strconv.ParseInt(string(raw.ExpiresIn), 10, 64)
	if err != nil || expires < 1 || expires > 86400 || !safeSIWCToken(raw.AccessToken) || raw.RefreshToken != "" && !safeSIWCToken(raw.RefreshToken) || !safeSIWCToken(raw.IDToken) || raw.TokenType != "Bearer" {
		return siwcTokenResponse{}, siwcFailure("response_incomplete", "unknown")
	}
	earliest, err := parseSIWCEarliest(raw.EarliestRefreshAt)
	if err != nil {
		return siwcTokenResponse{}, siwcFailure("response_incomplete", "unknown")
	}
	return siwcTokenResponse{AccessToken: raw.AccessToken, RefreshToken: raw.RefreshToken, IDToken: raw.IDToken, TokenType: raw.TokenType, ExpiresIn: expires, Scope: raw.Scope, EarliestRefreshAt: earliest}, nil
}

// validateSIWCIdentityGrant accepts only identity scopes. The provider can
// omit both explicit scope fields; the signed ID token then proves openid only.
func validateSIWCIdentityGrant(tokenScope, callbackScope string) ([]string, error) {
	parse := func(raw string) (map[string]bool, error) {
		if raw == "" {
			return nil, nil
		}
		items := strings.Fields(raw)
		if len(items) == 0 || len(items) > len(siwcIdentityRequestedScopes) {
			return nil, ErrInvalid
		}
		allowed := make(map[string]bool, len(siwcIdentityRequestedScopes))
		for _, scope := range siwcIdentityRequestedScopes {
			allowed[scope] = true
		}
		result := make(map[string]bool, len(items))
		for _, scope := range items {
			if !allowed[scope] || result[scope] {
				return nil, ErrInvalid
			}
			result[scope] = true
		}
		if !result["openid"] {
			return nil, ErrInvalid
		}
		return result, nil
	}
	grant, err := parse(tokenScope)
	if err != nil {
		return nil, err
	}
	callback, err := parse(callbackScope)
	if err != nil {
		return nil, err
	}
	if grant != nil && callback != nil && !sameSIWCScopeSet(grant, callback) {
		return nil, ErrInvalid
	}
	if grant == nil {
		grant = callback
	}
	if grant == nil {
		return []string{"openid"}, nil
	}
	ordered := make([]string, 0, len(grant))
	for _, scope := range siwcIdentityRequestedScopes {
		if grant[scope] {
			ordered = append(ordered, scope)
		}
	}
	return ordered, nil
}

func hasSIWCScope(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func parseSIWCEarliest(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&number) == nil {
		seconds, err := strconv.ParseInt(string(number), 10, 64)
		if err == nil && seconds > 0 {
			return time.Unix(seconds, 0).UTC().Format(time.RFC3339), nil
		}
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		parsed, err := time.Parse(time.RFC3339, value)
		if err == nil {
			return parsed.UTC().Format(time.RFC3339), nil
		}
	}
	return "", errors.New("invalid earliest refresh")
}

func safeSIWCToken(value string) bool {
	return value != "" && len(value) <= 65536 && !strings.ContainsAny(value, "\r\n\x00")
}

func isSIWCJSON(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == "application/json"
}

func validateSIWCGrant(tokenScope, callbackScope string, frozen []string) ([]string, error) {
	parse := func(raw string) (map[string]bool, error) {
		items := strings.Fields(raw)
		if len(items) == 0 || len(items) > len(siwcRequestedScopes) {
			return nil, ErrInvalid
		}
		result := make(map[string]bool, len(items))
		allowed := make(map[string]bool, len(siwcRequestedScopes))
		for _, scope := range siwcRequestedScopes {
			allowed[scope] = true
		}
		for _, scope := range items {
			if !allowed[scope] || result[scope] {
				return nil, ErrInvalid
			}
			result[scope] = true
		}
		if !result["openid"] || !result["profile"] || !result["email"] {
			return nil, ErrInvalid
		}
		return result, nil
	}
	grant, err := parse(tokenScope)
	if err != nil {
		return nil, err
	}
	if callbackScope != "" {
		callback, callbackErr := parse(callbackScope)
		if callbackErr != nil || !sameSIWCScopeSet(grant, callback) {
			return nil, ErrInvalid
		}
	}
	if frozen != nil {
		frozenSet := make(map[string]bool, len(frozen))
		for _, scope := range frozen {
			frozenSet[scope] = true
		}
		if !sameSIWCScopeSet(grant, frozenSet) {
			return nil, ErrInvalid
		}
		return append([]string(nil), frozen...), nil
	}
	ordered := make([]string, 0, len(grant))
	for _, scope := range siwcRequestedScopes {
		if grant[scope] {
			ordered = append(ordered, scope)
		}
	}
	return ordered, nil
}

func sameSIWCScopeSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if !b[key] {
			return false
		}
	}
	return true
}

func (s *SIWCService) fetchSIWCDiscovery(ctx context.Context) (siwcDiscovery, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.options.OIDCOrigin+"/.well-known/openid-configuration", nil)
	response, err := s.http.Do(request)
	if err != nil {
		return siwcDiscovery{}, siwcFailure("transport_unknown", "unknown")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !isSIWCJSON(response.Header.Get("Content-Type")) {
		return siwcDiscovery{}, siwcFailure("auth_invalid", "rejected")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, siwcJSONLimit+1))
	if err != nil || len(body) > siwcJSONLimit {
		return siwcDiscovery{}, siwcFailure("response_incomplete", "unknown")
	}
	var discovery siwcDiscovery
	decoder := json.NewDecoder(bytes.NewReader(body))
	if decoder.Decode(&discovery) != nil || decoder.Decode(&struct{}{}) != io.EOF || discovery.Issuer != s.options.Issuer || !urlAtSIWCOrigin(discovery.JWKSURI, s.options.JWKSOrigin) || !urlAtSIWCOrigin(discovery.RevocationEndpoint, s.options.RevocationOrigin) {
		return siwcDiscovery{}, siwcFailure("auth_invalid", "rejected")
	}
	return discovery, nil
}

func urlAtSIWCOrigin(raw, origin string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" || strings.Contains(u.Path, "..") || strings.Contains(u.Path, "//") {
		return false
	}
	return u.Scheme+"://"+u.Host == origin
}

func (s *SIWCService) validateSIWCIDToken(ctx context.Context, jwksURI, token, clientID, nonce string) (siwcJWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 65536 {
		return siwcJWTClaims{}, siwcFailure("auth_invalid", "rejected")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return siwcJWTClaims{}, siwcFailure("auth_invalid", "rejected")
	}
	var header struct{ Alg, Kid, Typ string }
	if json.Unmarshal(headerBytes, &header) != nil || header.Alg != "RS256" || header.Kid == "" || header.Typ != "JWT" {
		return siwcJWTClaims{}, siwcFailure("auth_invalid", "rejected")
	}
	key, err := s.fetchSIWCKey(ctx, jwksURI, header.Kid)
	if err != nil {
		return siwcJWTClaims{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return siwcJWTClaims{}, siwcFailure("auth_invalid", "rejected")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return siwcJWTClaims{}, siwcFailure("auth_invalid", "rejected")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return siwcJWTClaims{}, siwcFailure("auth_invalid", "rejected")
	}
	var raw struct {
		Issuer    string      `json:"iss"`
		Audience  string      `json:"aud"`
		Subject   string      `json:"sub"`
		Nonce     string      `json:"nonce"`
		Expires   json.Number `json:"exp"`
		NotBefore json.Number `json:"nbf"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if decoder.Decode(&raw) != nil {
		return siwcJWTClaims{}, siwcFailure("auth_invalid", "rejected")
	}
	expires, err := strconv.ParseInt(string(raw.Expires), 10, 64)
	if err != nil {
		return siwcJWTClaims{}, siwcFailure("auth_invalid", "rejected")
	}
	var notBefore int64
	if raw.NotBefore != "" {
		notBefore, err = strconv.ParseInt(string(raw.NotBefore), 10, 64)
		if err != nil {
			return siwcJWTClaims{}, siwcFailure("auth_invalid", "rejected")
		}
	}
	now := time.Now().Unix()
	if raw.Issuer != s.options.Issuer || raw.Audience != clientID || raw.Subject == "" || len(raw.Subject) > 2048 || strings.ContainsAny(raw.Subject, "\r\n\x00") || raw.Nonce != nonce || expires <= now || notBefore > now {
		return siwcJWTClaims{}, siwcFailure("auth_invalid", "rejected")
	}
	return siwcJWTClaims{Issuer: raw.Issuer, Audience: raw.Audience, Subject: raw.Subject, Nonce: raw.Nonce, Expires: expires, NotBefore: notBefore}, nil
}

func (s *SIWCService) fetchSIWCKey(ctx context.Context, jwksURI, kid string) (*rsa.PublicKey, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	response, err := s.http.Do(request)
	if err != nil {
		return nil, siwcFailure("transport_unknown", "unknown")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !isSIWCJSON(response.Header.Get("Content-Type")) {
		return nil, siwcFailure("auth_invalid", "rejected")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, siwcJSONLimit+1))
	if err != nil || len(body) > siwcJSONLimit {
		return nil, siwcFailure("response_incomplete", "unknown")
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if json.Unmarshal(body, &set) != nil || len(set.Keys) > 64 {
		return nil, siwcFailure("auth_invalid", "rejected")
	}
	var selected *rsa.PublicKey
	for _, candidate := range set.Keys {
		if candidate.Kid != kid || candidate.Kty != "RSA" || candidate.Alg != "RS256" || candidate.Use != "sig" {
			continue
		}
		nBytes, nErr := base64.RawURLEncoding.DecodeString(candidate.N)
		eBytes, eErr := base64.RawURLEncoding.DecodeString(candidate.E)
		if nErr != nil || eErr != nil || len(nBytes) < 256 || len(eBytes) == 0 || len(eBytes) > 4 {
			return nil, siwcFailure("auth_invalid", "rejected")
		}
		e := 0
		for _, value := range eBytes {
			e = e<<8 | int(value)
		}
		if e < 3 || e%2 == 0 || selected != nil {
			return nil, siwcFailure("auth_invalid", "rejected")
		}
		selected = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}
	}
	if selected == nil {
		return nil, siwcFailure("auth_invalid", "rejected")
	}
	return selected, nil
}
