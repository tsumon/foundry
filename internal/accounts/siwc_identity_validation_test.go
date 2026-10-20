package accounts

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Literal expectations catch rejection of optional standards metadata and
// acceptance of ambiguous or conflicting identity proofs. All keys are local.
func TestSIWCIDTokenOptionalMetadataAndBoundaries(t *testing.T) {
	lifecycle, _, _, _ := setupAccounts(t)
	issuer := newSIWCIssuerFixture(t, "http://127.0.0.1:1/v1")
	s, err := NewSIWCSimulation(lifecycle, issuer.options())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		name, check         string
		header, claims, key map[string]any
		omitHeader, omitKey string
		duplicate           bool
	}{
		{name: "scalar"},
		{name: "singleton audience", claims: map[string]any{"aud": []string{"oaiapp_synthetic"}}},
		{name: "omitted typ", omitHeader: "typ"},
		{name: "omitted alg", omitKey: "alg"},
		{name: "omitted use", omitKey: "use"},
		{name: "verification operation", key: map[string]any{"key_ops": []string{"verify"}}},
		{name: "signing and verification operations", key: map[string]any{"key_ops": []string{"sign", "verify"}}},
		{name: "authorized party", claims: map[string]any{"azp": "oaiapp_synthetic"}},
		{name: "none", check: "jwt_header", header: map[string]any{"alg": "none"}},
		{name: "symmetric", check: "jwt_header", header: map[string]any{"alg": "HS256"}},
		{name: "wrong typ", check: "jwt_header", header: map[string]any{"typ": "JOSE"}},
		{name: "null typ", check: "jwt_header", header: map[string]any{"typ": nil}},
		{name: "empty typ", check: "jwt_header", header: map[string]any{"typ": ""}},
		{name: "typed typ", check: "jwt_header", header: map[string]any{"typ": 12}},
		{name: "critical header", check: "jwt_header", header: map[string]any{"crit": []string{"extension"}}},
		{name: "null critical header", check: "jwt_header", header: map[string]any{"crit": nil}},
		{name: "issuer", check: "issuer", claims: map[string]any{"iss": "https://foreign.invalid"}},
		{name: "audience", check: "audience", claims: map[string]any{"aud": "dynamic_agent_client"}},
		{name: "resource audience", check: "audience", claims: map[string]any{"aud": "http://127.0.0.1:1/v1"}},
		{name: "extra audience", check: "audience", claims: map[string]any{"aud": []string{"oaiapp_synthetic", "other"}}},
		{name: "empty audience", check: "audience", claims: map[string]any{"aud": []string{}}},
		{name: "mixed audience", check: "audience", claims: map[string]any{"aud": []any{"oaiapp_synthetic", 1}}},
		{name: "null audience", check: "audience", claims: map[string]any{"aud": nil}},
		{name: "wrong azp", check: "audience", claims: map[string]any{"azp": "other"}},
		{name: "null azp", check: "audience", claims: map[string]any{"azp": nil}},
		{name: "empty azp", check: "audience", claims: map[string]any{"azp": ""}},
		{name: "typed azp", check: "audience", claims: map[string]any{"azp": []string{"oaiapp_synthetic"}}},
		{name: "subject", check: "subject", claims: map[string]any{"sub": "unsafe\nsubject"}},
		{name: "empty subject", check: "subject", claims: map[string]any{"sub": ""}},
		{name: "nonce", check: "nonce", claims: map[string]any{"nonce": "other"}},
		{name: "expiry", check: "expiry", claims: map[string]any{"exp": 1}},
		{name: "null expiry", check: "expiry", claims: map[string]any{"exp": nil}},
		{name: "string expiry", check: "expiry", claims: map[string]any{"exp": "9999999999"}},
		{name: "not before", check: "not_before", claims: map[string]any{"nbf": time.Now().Add(time.Hour).Unix()}},
		{name: "null not before", check: "not_before", claims: map[string]any{"nbf": nil}},
		{name: "key alg", check: "signing_key_invalid", key: map[string]any{"alg": "HS256"}},
		{name: "key null alg", check: "signing_key_invalid", key: map[string]any{"alg": nil}},
		{name: "key empty alg", check: "signing_key_invalid", key: map[string]any{"alg": ""}},
		{name: "key typed alg", check: "signing_key_invalid", key: map[string]any{"alg": 1}},
		{name: "key use", check: "signing_key_invalid", key: map[string]any{"use": "enc"}},
		{name: "key null use", check: "signing_key_invalid", key: map[string]any{"use": nil}},
		{name: "key empty use", check: "signing_key_invalid", key: map[string]any{"use": ""}},
		{name: "key type", check: "signing_key_invalid", key: map[string]any{"kty": "EC"}},
		{name: "key operations", check: "signing_key_invalid", key: map[string]any{"key_ops": []string{"encrypt"}}},
		{name: "key conflicting operations", check: "signing_key_invalid", key: map[string]any{"key_ops": []string{"verify", "decrypt"}}},
		{name: "key sign only", check: "signing_key_invalid", key: map[string]any{"key_ops": []string{"sign"}}},
		{name: "key mixed operations", check: "signing_key_invalid", key: map[string]any{"key_ops": []any{"verify", 1}}},
		{name: "key empty operations", check: "signing_key_invalid", key: map[string]any{"key_ops": []string{}}},
		{name: "key null operations", check: "signing_key_invalid", key: map[string]any{"key_ops": nil}},
		{name: "key duplicate operations", check: "signing_key_invalid", key: map[string]any{"key_ops": []string{"verify", "verify"}}},
		{name: "weak modulus padded to 256 bytes", check: "signing_key_invalid", key: map[string]any{"n": base64.RawURLEncoding.EncodeToString(append(make([]byte, 255), 3))}},
		{name: "malformed modulus", check: "signing_key_invalid", key: map[string]any{"n": "!invalid"}},
		{name: "duplicate key", check: "signing_key_invalid", duplicate: true},
		{name: "key not found", check: "signing_key_not_found", key: map[string]any{"kid": "other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := map[string]any{"alg": "RS256", "kid": "key-1", "typ": "JWT"}
			claims := map[string]any{"iss": issuer.server.URL, "sub": "synthetic-subject", "aud": "oaiapp_synthetic", "nonce": "synthetic-nonce", "exp": time.Now().Add(time.Hour).Unix()}
			for k, v := range tc.header {
				header[k] = v
			}
			delete(header, tc.omitHeader)
			for k, v := range tc.claims {
				claims[k] = v
			}
			issuer.jwksResponse = func(key map[string]any) any {
				for k, v := range tc.key {
					key[k] = v
				}
				delete(key, tc.omitKey)
				keys := []any{key}
				if tc.duplicate {
					keys = append(keys, key)
				}
				return map[string]any{"keys": keys}
			}
			_, err := s.validateSIWCIDToken(context.Background(), issuer.server.URL+"/jwks", issuer.signedToken(header, claims), "oaiapp_synthetic", "synthetic-nonce")
			if tc.check == "" {
				if err != nil {
					t.Fatalf("supported proof rejected: %v", err)
				}
				return
			}
			assertIdentityCheck(t, s, err, tc.check, "auth_invalid")
		})
	}
}

func assertIdentityCheck(t *testing.T, s *SIWCService, err error, check, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("invalid proof accepted; want %s", check)
	}
	d := s.siwcConnectionDiagnostic(nil, "identity_validation", "auth_invalid", err)
	raw, _ := json.Marshal(d)
	var record map[string]any
	_ = json.Unmarshal(raw, &record)
	if record["identity_check"] != check || d.Code != code {
		t.Fatalf("diagnostic=%s; want identity_check=%s code=%s", raw, check, code)
	}
}

func TestSIWCIdentityMalformedProofAndFetchEvidence(t *testing.T) {
	lifecycle, _, _, _ := setupAccounts(t)
	issuer := newSIWCIssuerFixture(t, "http://127.0.0.1:1/v1")
	s, _ := NewSIWCSimulation(lifecycle, issuer.options())
	defer s.Close()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"key-1","typ":"JWT"}`))
	validPayload, _ := json.Marshal(map[string]any{"iss": issuer.server.URL, "sub": "subject", "aud": "oaiapp_synthetic", "nonce": "nonce", "exp": time.Now().Add(time.Hour).Unix()})
	for _, tc := range []struct{ name, payload, check string }{
		{"malformed", "{", "claims_format"},
		{"trailing", string(validPayload) + ` {}`, "claims_format"},
		{"null", "null", "claims_format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unsigned := header + "." + base64.RawURLEncoding.EncodeToString([]byte(tc.payload))
			digest := sha256.Sum256([]byte(unsigned))
			sig, err := rsa.SignPKCS1v15(rand.Reader, issuer.key, crypto.SHA256, digest[:])
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.validateSIWCIDToken(context.Background(), issuer.server.URL+"/jwks", unsigned+"."+base64.RawURLEncoding.EncodeToString(sig), "oaiapp_synthetic", "nonce")
			assertIdentityCheck(t, s, err, tc.check, "auth_invalid")
		})
	}
	_, err := s.validateSIWCIDToken(context.Background(), issuer.server.URL+"/jwks", "not-a-token", "oaiapp_synthetic", "nonce")
	assertIdentityCheck(t, s, err, "token_format", "auth_invalid")
	token := issuer.idToken("oaiapp_synthetic", "nonce", "subject", time.Now().Add(time.Hour))
	parts := strings.Split(token, ".")
	parts[2] = base64.RawURLEncoding.EncodeToString(make([]byte, 256))
	_, err = s.validateSIWCIDToken(context.Background(), issuer.server.URL+"/jwks", strings.Join(parts, "."), "oaiapp_synthetic", "nonce")
	assertIdentityCheck(t, s, err, "signature", "auth_invalid")
	s.http.Transport = subscriptionRoundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("SYNTHETIC-PRIVATE-TRANSPORT") })
	_, err = s.validateSIWCIDToken(context.Background(), issuer.server.URL+"/jwks", token, "oaiapp_synthetic", "nonce")
	assertIdentityCheck(t, s, err, "signing_key_fetch", "transport_unknown")
}

func TestSubscriptionOptionalIdentityProofAndFailureConsumption(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "compatible", true: "nonce failure"}[invalid], func(t *testing.T) {
			lifecycle, db, secrets, _ := setupAccounts(t)
			issuer := newSIWCIssuerFixture(t, "http://127.0.0.1:1/v1")
			account := newSIWCAccount(t, lifecycle, issuer.server.URL+"/v1")
			s, _ := NewSIWCSimulation(lifecycle, issuer.options())
			defer s.Close()
			issuer.jwksResponse = func(key map[string]any) any {
				delete(key, "alg")
				delete(key, "use")
				return map[string]any{"keys": []any{key}}
			}
			issuer.identityResponse = func(client, nonce string) map[string]any {
				if invalid {
					nonce = "wrong-synthetic-nonce"
				}
				return map[string]any{"access_token": "SYNTHETIC-ACCESS", "refresh_token": "SYNTHETIC-REFRESH", "token_type": "Bearer", "expires_in": 3600, "scope": strings.Join(requestedSIWCScopes, " "), "id_token": issuer.signedToken(map[string]any{"alg": "RS256", "kid": "key-1"}, map[string]any{"iss": issuer.server.URL, "aud": []string{client}, "azp": client, "sub": "synthetic-subject", "nonce": nonce, "exp": time.Now().Add(time.Hour).Unix()})}
			}
			resourceCalls := 0
			base := s.http.Transport
			if base == nil {
				base = http.DefaultTransport
			}
			s.http.Transport = subscriptionRoundTrip(func(r *http.Request) (*http.Response, error) {
				if strings.HasPrefix(r.URL.Path, "/v1") {
					resourceCalls++
				}
				return base.RoundTrip(r)
			})
			flow, stop, err := s.BeginOwnedSubscriptionConnectionSimulation(context.Background(), account.ID, account.Revision)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			query := openHandoff(t, flow)
			issuer.mu.Lock()
			issuer.nonce = query.Get("nonce")
			issuer.mu.Unlock()
			callback := query.Get("redirect_uri") + "?" + url.Values{"code": {"synthetic-code"}, "state": {query.Get("state")}, "client_id": {"oaiapp_synthetic"}}.Encode()
			response, err := http.Get(callback)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			stop()
			status, err := s.Status(flow.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if invalid {
				raw, _ := json.Marshal(status.ConnectionDiagnostic)
				var record map[string]any
				_ = json.Unmarshal(raw, &record)
				if record["identity_check"] != "nonce" || record["credential_commit"] != "not_committed" || record["cleanup"] != "completed" || secrets.sets != 0 || len(secrets.values) != 0 {
					t.Fatalf("failed proof evidence=%s writes=%d", raw, secrets.sets)
				}
				if _, err = db.CurrentModelAuthorization(account.ID); err == nil {
					t.Fatal("invalid proof committed")
				}
			} else {
				current, err := db.CurrentModelAuthorization(account.ID)
				if err != nil || current.State != "available" || current.Binding.Subject != "synthetic-subject" {
					t.Fatalf("compatible proof did not commit: %+v %v", current, err)
				}
			}
			if replay, err := http.Get(callback); err == nil {
				replay.Body.Close()
				if replay.StatusCode == 200 {
					t.Fatal("replay accepted")
				}
			}
			if _, err = s.Status(flow.OperationID); err != nil {
				t.Fatal(err)
			}
			if issuer.tokenCalls.Load() != 1 || resourceCalls != 0 {
				t.Fatalf("exchanges=%d resource calls=%d", issuer.tokenCalls.Load(), resourceCalls)
			}
		})
	}
}
