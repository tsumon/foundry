package store

import (
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"path/filepath"
	"strings"
	"testing"
)

func authorizationFixture(t *testing.T, s *Store) domain.ModelAuthorizationBinding {
	t.Helper()
	_, err := s.SaveModelAccount(domain.ModelAccount{ID: "oauth", Name: "Synthetic", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", BaseURL: "http://127.0.0.1:1234/v1", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return domain.ModelAuthorizationBinding{ID: "auth", AccountID: "oauth", Issuer: "https://issuer.invalid", Subject: "subject", ClientID: "issued-client", SpendClass: "subscription_only", Scopes: []string{"openid", "resource.invoke", "chatgpt.tokens.use.direct"}}
}

func TestAuthorizationRefreshIntentSerializesAndFreezesIdentity(t *testing.T) {
	s := openTestStore(t)
	b := authorizationFixture(t, s)
	op, err := s.BeginModelAuthorization(b.AccountID, "sign-in", "sign_in", b.ID)
	if err != nil || op.Generation != 1 || op.SecretRef == "" {
		t.Fatal(op, err)
	}
	a, err := s.CommitModelAuthorization(op.ID, b, "2099-01-01T00:00:00Z")
	if err != nil || a.State != "available" {
		t.Fatal(a, err)
	}
	next, err := s.BeginModelAuthorization(b.AccountID, "refresh", "refresh", b.ID)
	if err != nil || next.Generation != 2 || next.SecretRef == op.SecretRef {
		t.Fatal(next, err)
	}
	if _, err = s.BeginModelAuthorization(b.AccountID, "other", "refresh", b.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("parallel refresh reserved", err)
	}
	if _, err = s.BeginModelAuthorization(b.AccountID, "refresh", "refresh", b.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate intent acquired second owner", err)
	}
	bad := b
	bad.Subject = "another"
	if _, err = s.CommitModelAuthorization(next.ID, bad, "2099-01-01T00:00:00Z"); err == nil {
		t.Fatal("identity overwritten")
	}
	bad = b
	bad.Scopes = []string{"openid"}
	if _, err = s.CommitModelAuthorization(next.ID, bad, "2099-01-01T00:00:00Z"); err == nil {
		t.Fatal("immutable grant overwritten")
	}
	a, err = s.CommitModelAuthorization(next.ID, b, "2099-02-01T00:00:00Z")
	if err != nil || a.Generation != 2 || a.Binding.Subject != "subject" {
		t.Fatal(a, err)
	}
	raw, _ := json.Marshal([]any{op, next, a})
	if strings.Contains(string(raw), op.SecretRef) || strings.Contains(string(raw), next.SecretRef) {
		t.Fatal("private refs exposed")
	}
	if _, err = s.db.Exec(`UPDATE model_authorizations SET binding='{}' WHERE id='auth'`); err == nil {
		t.Fatal("binding mutable through SQL")
	}
}

func TestAuthorizationRecoveryAcrossReopenKeepsIdentityButDenies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b := authorizationFixture(t, s)
	if _, err = s.BeginModelAuthorization(b.AccountID, "login", "sign_in", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitModelAuthorization("login", b, "2099-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginModelAuthorization(b.AccountID, "refresh", "refresh", b.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.RecoverModelAuthorizations(); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetModelAuthorization(b.ID)
	if err != nil || a.State != "refresh_unknown" || a.Binding.Subject != b.Subject || a.Generation != 1 {
		t.Fatal(a, err)
	}
	if _, err = s.BeginModelAuthorization(b.AccountID, "retry", "refresh", b.ID); err == nil {
		t.Fatal("unknown refresh retried")
	}
	pending, err := s.PendingModelAuthorizationCleanup()
	if err != nil || len(pending) != 1 {
		t.Fatal("missing exact cleanup intents", pending, err)
	}
}

func TestAuthorizationReauthorizationRequiresSameRegistrationAndNewGrantBinding(t *testing.T) {
	s := openTestStore(t)
	b := authorizationFixture(t, s)
	b.Scopes = []string{"openid"}
	if _, err := s.BeginModelAuthorization(b.AccountID, "identity", "sign_in", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitModelAuthorization("identity", b, "2099-01-01T00:00:00Z"); err != nil {
		t.Fatal("identity-only rejected", err)
	}
	if _, err := s.RevokeModelAuthorization(b.ID); err != nil {
		t.Fatal(err)
	}
	b.ID = "new-auth"
	b.Scopes = append(b.Scopes, "resource.invoke", "chatgpt.tokens.use.direct")
	if _, err := s.BeginModelAuthorization(b.AccountID, "reauth", "sign_in", b.ID); err != nil {
		t.Fatal(err)
	}
	wrong := b
	wrong.ClientID = "different-client"
	if _, err := s.CommitModelAuthorization("reauth", wrong, "2099-01-01T00:00:00Z"); err == nil {
		t.Fatal("registration changed")
	}
	if _, err := s.CommitModelAuthorization("reauth", b, "2099-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	old, _ := s.GetModelAuthorization("auth")
	current, _ := s.CurrentModelAuthorization(b.AccountID)
	if old.State != "revoked" || len(old.Binding.Scopes) != 1 || current.Binding.ID != "new-auth" {
		t.Fatal(old, current)
	}
}

func TestOAuthSnapshotFreezesBindingNotGenerationAndProfileResolutionIgnoresSettings(t *testing.T) {
	s := openTestStore(t)
	b := authorizationFixture(t, s)
	r, err := s.CreateModelRoute(domain.ModelRoute{ID: "route", AccountID: b.AccountID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: "http://127.0.0.1:1234/v1", Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutProfile(domain.Profile{ID: "oauth-profile", AccountID: b.AccountID, RouteID: r.ID, Protocol: r.Protocol, BaseURL: r.BaseURL, Model: "fake"}); err != nil {
		t.Fatal(err)
	}
	if err = s.PutSettings(domain.Settings{DefaultProfileID: "oauth-profile"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginModelAuthorization(b.AccountID, "login", "sign_in", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitModelAuthorization("login", b, "2099-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	m := routedMission(t, s, "frozen-auth")
	if m.ModelSnapshot.Roles[domain.Worker].Authorization == nil {
		t.Fatal("binding omitted")
	}
	m, err = s.CreateMission(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckModelCall(m.ID, domain.Worker); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginModelAuthorization(b.AccountID, "refresh", "refresh", b.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckModelCall(m.ID, domain.Worker); err == nil {
		t.Fatal("refreshing call allowed")
	}
	if _, err = s.CommitModelAuthorization("refresh", b, "2099-02-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteModelAuthorizationCleanup("login"); err != nil {
		t.Fatal(err)
	}
	_, _, next, err := s.ResolveMissionModels("")
	if err != nil || next.Digest != m.ModelSnapshot.Digest {
		t.Fatal("generation changed frozen authority", err)
	}
	if err = s.CheckModelCall(m.ID, domain.Worker); err != nil {
		t.Fatal("renewed binding unavailable", err)
	}
	if err = s.PutSettings(domain.Settings{DefaultProfileID: "unrelated"}); err != nil {
		t.Fatal(err)
	}
	selected, err := s.ResolveModelProfile("oauth-profile")
	if err != nil || selected.Authorization.ID != b.ID || selected.Route.ID != r.ID {
		t.Fatal("profile resolution followed settings", selected, err)
	}
	if _, err = s.RevokeModelAuthorization(b.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckModelCall(m.ID, domain.Worker); err == nil {
		t.Fatal("revoked call allowed")
	}
	if _, err = s.GetMission(m.ID); err != nil {
		t.Fatal("revocation corrupted history", err)
	}
}

func TestOAuthAccountCannotAcceptAPIKeyCredential(t *testing.T) {
	s := openTestStore(t)
	b := authorizationFixture(t, s)
	if _, err := s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: b.AccountID, OperationID: "key", SecretRef: "foundry-model-account-fake"}, 1); err == nil {
		t.Fatal("API key added to OAuth account")
	}
}

func TestAuthorizationRevocationIsRecordedAndCannotRestoreOldGeneration(t *testing.T) {
	s := openTestStore(t)
	b := authorizationFixture(t, s)
	if _, err := s.BeginModelAuthorization(b.AccountID, "login", "sign_in", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitModelAuthorization("login", b, "2099-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeModelAuthorization(b.ID); err != nil {
		t.Fatal(err)
	}
	var revokes int
	if err := s.db.QueryRow(`SELECT count(*) FROM model_authorization_operations WHERE kind='revoke' AND authorization_id='auth' AND state='revoked'`).Scan(&revokes); err != nil || revokes != 1 {
		t.Fatal("missing durable revoke intent", revokes, err)
	}
	for _, query := range []string{`UPDATE model_authorizations SET state='available' WHERE id='auth'`, `UPDATE model_authorizations SET secret_ref='other' WHERE id='auth'`, `UPDATE model_authorizations SET generation=0 WHERE id='auth'`} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatal("historical token state restored")
		}
	}
	list, err := s.ListModelAuthorizations()
	if err != nil || len(list) != 1 || list[0].State != "revoked" {
		t.Fatal(list, err)
	}
}
