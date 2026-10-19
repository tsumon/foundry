package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"os"
	"strings"
	"testing"
)

func selectionFixture(t *testing.T, s *Service, db *store.Store, oauth bool) domain.RoleModelSelection {
	t.Helper()
	auth, billing, adapter, spend := "api_key", "api_credits", "openai_api_responses", "explicit_api"
	if oauth {
		auth, billing, adapter, spend = "openai_siwc", "subscription", "openai_siwc_responses", "subscription_only"
	}
	a, err := s.Save(domain.ModelAccount{Name: "Synthetic", Provider: "openai", AuthMethod: auth, BillingMode: billing, BaseURL: "http://127.0.0.1:1234/v1", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := db.CreateModelRoute(domain.ModelRoute{ID: "route", AccountID: a.ID, Adapter: adapter, Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: spend, ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	return domain.RoleModelSelection{Profile: domain.Profile{ID: "profile-not-a-secret-ref", AccountID: a.ID, AccountRevision: a.Revision, RouteID: r.ID, Protocol: r.Protocol, BaseURL: r.BaseURL, Model: "fake"}, Account: &a, Route: &r}
}

func TestCredentialBrokerUsesFrozenVersionAndRejectsBeforeSecretRead(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	sel := selectionFixture(t, s, db, false)
	ctx := context.Background()
	first, err := s.AddCredential(ctx, sel.Account.ID, 1, "first", "FAKE-EXACT-OLD")
	if err != nil {
		t.Fatal(err)
	}
	sel.Profile.CredentialVersion = first.Version
	a, _ := db.GetModelAccount(sel.Account.ID)
	if _, err = s.AddCredential(ctx, a.ID, a.Revision, "second", "FAKE-CURRENT-NEW"); err != nil {
		t.Fatal(err)
	}
	lease, err := s.ResolveModelCredential(ctx, sel)
	if err != nil || lease.Secret != "FAKE-EXACT-OLD" || lease.Generation != 1 {
		t.Fatal("frozen credential changed", lease.Generation, err)
	}
	gets := secrets.gets
	a, _ = db.GetModelAccount(a.ID)
	a.Enabled = false
	if _, err = s.Save(a, a.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveModelCredential(ctx, sel); err == nil {
		t.Fatal("stopped account resolved")
	}
	a, _ = db.GetModelAccount(a.ID)
	a.Enabled = true
	if _, err = s.Save(a, a.Revision); err != nil {
		t.Fatal(err)
	}
	a, _ = db.GetModelAccount(a.ID)
	if _, err = s.RevokeCredential(ctx, a.ID, 1, a.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveModelCredential(ctx, sel); err == nil {
		t.Fatal("revoked frozen key followed current")
	}
	if secrets.gets != gets {
		t.Fatal("denial read secrets")
	}
}

func commitAuthorization(t *testing.T, s *Service, sel *domain.RoleModelSelection, op, kind string, scopes []string) domain.ModelAuthorization {
	t.Helper()
	b := domain.ModelAuthorizationBinding{ID: "authorization", AccountID: sel.Account.ID, Issuer: "https://issuer.invalid", Subject: "subject", ClientID: "issued-client", SpendClass: "subscription_only", Scopes: scopes}
	if _, err := s.BeginModelAuthorization(context.Background(), b.AccountID, op, kind, b.ID); err != nil {
		t.Fatal(err)
	}
	a, err := s.CommitModelAuthorization(context.Background(), op, b, ModelTokenBundle{AccessToken: "FAKE-ACCESS-" + op, RefreshToken: "FAKE-REFRESH-" + op, IDToken: "FAKE-ID-" + op, ExpiresAt: "2099-01-01T00:00:00Z", Resource: sel.Route.BaseURL, EarliestRefreshAt: "2098-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	sel.Authorization = &a.Binding
	return a
}

func TestOAuthBrokerRequiresExactAvailableBindingAndScope(t *testing.T) {
	s, db, secrets, path := setupAccounts(t)
	sel := selectionFixture(t, s, db, true)
	ctx := context.Background()
	if _, err := s.ResolveModelCredential(ctx, sel); err == nil {
		t.Fatal("missing binding resolved")
	}
	a := commitAuthorization(t, s, &sel, "login", "sign_in", []string{"openid", "resource.invoke", "chatgpt.tokens.use.direct"})
	lease, err := s.ResolveModelCredential(ctx, sel)
	if err != nil || lease.Secret != "FAKE-ACCESS-login" || lease.Generation != 1 {
		t.Fatal(lease.Generation, err)
	}
	original := *sel.Authorization
	sel.Authorization = &domain.ModelAuthorizationBinding{ID: original.ID, AccountID: original.AccountID}
	gets := secrets.gets
	if _, err = s.ResolveModelCredential(ctx, sel); err == nil {
		t.Fatal("mismatched binding resolved")
	}
	if secrets.gets != gets {
		t.Fatal("mismatch read secrets")
	}
	sel.Authorization = &original
	a = commitAuthorization(t, s, &sel, "refresh", "refresh", original.Scopes)
	lease, err = s.ResolveModelCredential(ctx, sel)
	if err != nil || lease.Generation != 2 || lease.Secret != "FAKE-ACCESS-refresh" {
		t.Fatal(lease.Generation, err)
	}
	raw, _ := json.Marshal([]any{a, sel, lease})
	if strings.Contains(string(raw), "FAKE-") || strings.Contains(string(raw), "foundry-model-authorization-") {
		t.Fatal("secret serialized")
	}
	for _, suffix := range []string{"", "-wal"} {
		contents, err := os.ReadFile(path + suffix)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if strings.Contains(string(contents), "FAKE-") {
			t.Fatal("token written in SQLite")
		}
	}
	if _, err = s.RevokeModelAuthorization(ctx, a.Binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveModelCredential(ctx, sel); err == nil {
		t.Fatal("revoked OAuth resolved")
	}
}

func TestIdentityOnlyGrantPersistsButNeverReadsToken(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	sel := selectionFixture(t, s, db, true)
	a := commitAuthorization(t, s, &sel, "identity", "sign_in", []string{"openid", "profile"})
	if a.State != "available" {
		t.Fatal("identity not saved")
	}
	_, err := s.ResolveModelCredential(context.Background(), sel)
	var denial *model.ProviderError
	if !errors.As(err, &denial) || denial.Code != "entitlement_denied" || denial.Certainty != "not_sent" || secrets.gets != 0 {
		t.Fatal("identity-only inference not safely denied", err)
	}
}

func TestIdentityOnlyBindingCannotStartOrReadRefreshBeforeSecretAccess(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	sel := selectionFixture(t, s, db, true)
	ctx := context.Background()
	binding := domain.ModelAuthorizationBinding{ID: "identity-refresh", AccountID: sel.Account.ID, Issuer: "https://issuer.invalid", Subject: "subject", ClientID: "issued-client", SpendClass: "identity_only", Scopes: []string{"openid"}}
	if _, err := s.BeginModelAuthorization(ctx, binding.AccountID, "identity-sign-in", "sign_in", binding.ID); err != nil {
		t.Fatal(err)
	}
	authorization, err := s.CommitModelAuthorization(ctx, "identity-sign-in", binding, ModelTokenBundle{IDToken: "FAKE-IDENTITY-PROOF", ExpiresAt: "2099-01-01T00:00:00Z", Resource: sel.Route.BaseURL})
	if err != nil {
		t.Fatal(err)
	}
	gets := secrets.gets
	if _, err = s.BeginModelAuthorization(ctx, binding.AccountID, "identity-refresh", "refresh", authorization.Binding.ID); err == nil {
		t.Fatal("identity binding reserved a refresh intent")
	}
	if _, err = s.ReadModelAuthorizationRefresh(ctx, "identity-refresh"); err == nil {
		t.Fatal("identity binding exposed a refresh bundle")
	}
	if secrets.gets != gets {
		t.Fatalf("identity refresh denial read protected storage: %d -> %d", gets, secrets.gets)
	}
}

func TestAuthorizationSecretWriteFailureAndCleanupBlockReadiness(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	sel := selectionFixture(t, s, db, true)
	ctx := context.Background()
	original := commitAuthorization(t, s, &sel, "login", "sign_in", []string{"resource.invoke", "chatgpt.tokens.use.direct"})
	if _, err := s.BeginModelAuthorization(ctx, sel.Account.ID, "refresh", "refresh", original.Binding.ID); err != nil {
		t.Fatal(err)
	}
	secrets.setErr = errors.New("FAKE-TOKEN write error")
	secrets.deleteErr = errors.New("FAKE-TOKEN cleanup error")
	_, err := s.CommitModelAuthorization(ctx, "refresh", original.Binding, ModelTokenBundle{AccessToken: "FAKE-PARTIAL", ExpiresAt: "2099-01-01T00:00:00Z", Resource: sel.Route.BaseURL})
	if err == nil || strings.Contains(err.Error(), "FAKE") {
		t.Fatal("unsafe write failure", err)
	}
	if err = s.Recover(ctx); err == nil {
		t.Fatal("cleanup failure swallowed")
	}
	if _, err = s.ResolveModelCredential(ctx, sel); err == nil {
		t.Fatal("failed refresh resolved")
	}
	secrets.deleteErr = nil
	if err = s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := db.GetModelAuthorization(original.Binding.ID)
	if a.State != "refresh_unknown" {
		t.Fatal(a.State)
	}
}

func TestInterruptedRefreshReopenBlocksLeaseWithoutNetwork(t *testing.T) {
	s, db, secrets, path := setupAccounts(t)
	sel := selectionFixture(t, s, db, true)
	ctx := context.Background()
	a := commitAuthorization(t, s, &sel, "login", "sign_in", []string{"resource.invoke", "chatgpt.tokens.use.direct"})
	if _, err := s.BeginModelAuthorization(ctx, sel.Account.ID, "refresh", "refresh", a.Binding.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered := New(reopened, secrets)
	gets, sets := secrets.gets, secrets.sets
	if err = recovered.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = recovered.ResolveModelCredential(ctx, sel); err == nil {
		t.Fatal("interrupted refresh remained callable")
	}
	if secrets.gets != gets || secrets.sets != sets {
		t.Fatal("recovery read or rewrote token")
	}
	old, _ := reopened.GetModelAuthorization(a.Binding.ID)
	if old.Binding.Subject != "subject" || old.State != "refresh_unknown" {
		t.Fatal(old)
	}
}

func TestRefreshBundleReadRequiresExactDurableIntent(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	sel := selectionFixture(t, s, db, true)
	ctx := context.Background()
	a := commitAuthorization(t, s, &sel, "login", "sign_in", []string{"resource.invoke", "chatgpt.tokens.use.direct"})
	gets := secrets.gets
	if _, err := s.ReadModelAuthorizationRefresh(ctx, "login"); err == nil {
		t.Fatal("non-refresh read accepted")
	}
	if secrets.gets != gets {
		t.Fatal("invalid intent read secret")
	}
	if _, err := s.BeginModelAuthorization(ctx, sel.Account.ID, "refresh", "refresh", a.Binding.ID); err != nil {
		t.Fatal(err)
	}
	b, err := s.ReadModelAuthorizationRefresh(ctx, "refresh")
	if err != nil || b.RefreshToken != "FAKE-REFRESH-login" || b.EarliestRefreshAt != "2098-01-01T00:00:00Z" || b.Resource != sel.Route.BaseURL {
		t.Fatal("wrong refresh bundle", err)
	}
	if _, err = s.ResolveModelCredential(ctx, sel); err == nil {
		t.Fatal("refreshing inferred")
	}
	if err = s.FailModelAuthorization(ctx, "refresh", "refresh_unknown"); err != nil {
		t.Fatal(err)
	}
	gets = secrets.gets
	if _, err = s.ReadModelAuthorizationRefresh(ctx, "refresh"); err == nil {
		t.Fatal("lost-response refresh retried")
	}
	if secrets.gets != gets {
		t.Fatal("unknown refresh read token")
	}
}

func TestAuthorizationCommitFailureAfterSecretWriteRecoversExactRef(t *testing.T) {
	s, db, secrets, path := setupAccounts(t)
	sel := selectionFixture(t, s, db, true)
	ctx := context.Background()
	a := commitAuthorization(t, s, &sel, "login", "sign_in", []string{"resource.invoke", "chatgpt.tokens.use.direct"})
	op, err := s.BeginModelAuthorization(ctx, sel.Account.ID, "refresh", "refresh", a.Binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	secrets.afterSet = func() { _ = db.Close() }
	_, err = s.CommitModelAuthorization(ctx, op.ID, a.Binding, ModelTokenBundle{AccessToken: "FAKE-LOST-COMMIT", ExpiresAt: "2099-01-01T00:00:00Z", Resource: sel.Route.BaseURL})
	if err == nil {
		t.Fatal("closed DB commit succeeded")
	}
	if secrets.values[op.SecretRef] == "" {
		t.Fatal("fixture did not write secret before failure")
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = New(reopened, secrets).Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, exists := secrets.values[op.SecretRef]; exists {
		t.Fatal("abandoned new secret survived")
	}
	if _, err = New(reopened, secrets).ResolveModelCredential(ctx, sel); err == nil {
		t.Fatal("lost commit retained usable authorization")
	}
}

func TestAuthorizationWriteHasOneOwnerAcrossServices(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	sel := selectionFixture(t, s, db, true)
	ctx := context.Background()
	b := domain.ModelAuthorizationBinding{ID: "auth", AccountID: sel.Account.ID, Issuer: "https://issuer.invalid", Subject: "subject", ClientID: "client", SpendClass: "subscription_only"}
	if _, err := s.BeginModelAuthorization(ctx, b.AccountID, "login", "sign_in", b.ID); err != nil {
		t.Fatal(err)
	}
	reached, release := make(chan struct{}), make(chan struct{})
	secrets.afterSet = func() {
		select {
		case <-reached:
			return
		default:
			close(reached)
			<-release
		}
	}
	result := make(chan error, 1)
	go func() {
		_, err := s.CommitModelAuthorization(ctx, "login", b, ModelTokenBundle{AccessToken: "FAKE-ONE", ExpiresAt: "2099-01-01T00:00:00Z", Resource: sel.Route.BaseURL})
		result <- err
	}()
	<-reached
	// A second service must fail before writing the reserved secret reference.
	_, err := New(db, secrets).CommitModelAuthorization(ctx, "login", b, ModelTokenBundle{AccessToken: "FAKE-TWO", ExpiresAt: "2099-01-01T00:00:00Z", Resource: sel.Route.BaseURL})
	close(release)
	if err == nil {
		t.Fatal("second writer accepted")
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if secrets.sets != 1 {
		t.Fatal("multiple secret writers", secrets.sets)
	}
}

type delayedAuthorizationSecrets struct {
	*memorySecrets
	reached chan struct{}
	release chan struct{}
}

func (s *delayedAuthorizationSecrets) Set(ref, value string) error {
	close(s.reached)
	<-s.release
	return s.memorySecrets.Set(ref, value)
}

func TestRevocationDuringTokenWriteKeepsUnresolvedCleanupDurable(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	sel := selectionFixture(t, s, db, true)
	ctx := context.Background()
	a := commitAuthorization(t, s, &sel, "login", "sign_in", []string{"resource.invoke", "chatgpt.tokens.use.direct"})
	op, err := s.BeginModelAuthorization(ctx, sel.Account.ID, "refresh", "refresh", a.Binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	delayed := &delayedAuthorizationSecrets{memorySecrets: secrets, reached: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := New(db, delayed).CommitModelAuthorization(ctx, op.ID, a.Binding, ModelTokenBundle{AccessToken: "FAKE-RACING-WRITE", ExpiresAt: "2099-01-01T00:00:00Z", Resource: sel.Route.BaseURL})
		done <- err
	}()
	<-delayed.reached
	_, revokeErr := s.RevokeModelAuthorization(ctx, a.Binding.ID)
	pending, _ := db.PendingModelAuthorizationCleanup()
	close(delayed.release)
	if err = <-done; err == nil {
		t.Fatal("revoked writer published")
	}
	if revokeErr == nil || len(pending) == 0 {
		t.Fatal("in-flight cleanup falsely completed")
	}
	if err = s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	secrets.mu.Lock()
	_, exists := secrets.values[op.SecretRef]
	secrets.mu.Unlock()
	if exists {
		t.Fatal("late secret write orphaned")
	}
	if _, err = s.ResolveModelCredential(ctx, sel); err == nil {
		t.Fatal("revoked authorization revived")
	}
}

func TestExternalFailureDuringTokenWritePreservesWriterUntilFinalization(t *testing.T) {
	for _, test := range []struct{ kind, state string }{{"sign_in", "failed"}, {"sign_in", "refresh_unknown"}, {"refresh", "failed"}, {"refresh", "refresh_unknown"}} {
		t.Run(test.kind+"/"+test.state, func(t *testing.T) {
			s, db, secrets, _ := setupAccounts(t)
			sel := selectionFixture(t, s, db, true)
			ctx := context.Background()
			b := domain.ModelAuthorizationBinding{ID: "auth", AccountID: sel.Account.ID, Issuer: "https://issuer.invalid", Subject: "subject", ClientID: "client", SpendClass: "subscription_only", Scopes: []string{"resource.invoke", "chatgpt.tokens.use.direct"}}
			if test.kind == "refresh" {
				a := commitAuthorization(t, s, &sel, "initial", "sign_in", b.Scopes)
				b = a.Binding
			}
			op, err := s.BeginModelAuthorization(ctx, b.AccountID, "write", test.kind, b.ID)
			if err != nil {
				t.Fatal(err)
			}
			delayed := &delayedAuthorizationSecrets{memorySecrets: secrets, reached: make(chan struct{}), release: make(chan struct{})}
			done := make(chan error, 1)
			go func() {
				_, err := New(db, delayed).CommitModelAuthorization(ctx, op.ID, b, ModelTokenBundle{AccessToken: "FAKE-DELAYED-FAILURE", ExpiresAt: "2099-01-01T00:00:00Z", Resource: sel.Route.BaseURL})
				done <- err
			}()
			<-delayed.reached
			failErr := s.FailModelAuthorization(ctx, op.ID, test.state)
			during, readErr := db.GetModelAuthorizationOperation(op.ID)
			// Retry from a different Service must not acknowledge the outstanding writer.
			retryErr := New(db, secrets).FailModelAuthorization(ctx, op.ID, "failed")
			close(delayed.release)
			writerErr := <-done
			if !errors.Is(failErr, ErrInProgress) || !errors.Is(retryErr, ErrInProgress) || readErr != nil || !during.WriteClaimed || !during.CleanupPending {
				t.Errorf("external failure released writer: fail=%v retry=%v marker=%+v read=%v", failErr, retryErr, during, readErr)
			}
			if writerErr == nil {
				t.Error("failed token writer published")
			}
			after, err := db.GetModelAuthorizationOperation(op.ID)
			if err != nil || after.WriteClaimed || after.CleanupPending {
				t.Errorf("writer did not finalize cleanup: %+v %v", after, err)
			}
			secrets.mu.Lock()
			_, exists := secrets.values[op.SecretRef]
			secrets.mu.Unlock()
			if exists {
				t.Error("delayed failed token write orphaned after finalized cleanup")
			}
		})
	}
}

func TestHistoricalAuthorizationCannotReactivateOrWriteSecret(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	sel := selectionFixture(t, s, db, true)
	ctx := context.Background()
	a := commitAuthorization(t, s, &sel, "first", "sign_in", []string{"openid"})
	b := a.Binding
	b.ID = "new-binding"
	b.Scopes = []string{"openid", "resource.invoke", "chatgpt.tokens.use.direct"}
	if _, err := s.BeginModelAuthorization(ctx, b.AccountID, "second", "sign_in", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitModelAuthorization(ctx, "second", b, ModelTokenBundle{AccessToken: "FAKE-NEW-BINDING", ExpiresAt: "2099-01-01T00:00:00Z", Resource: sel.Route.BaseURL}); err != nil {
		t.Fatal(err)
	}
	sets := secrets.sets
	if _, err := s.BeginModelAuthorization(ctx, a.Binding.AccountID, "historical", "sign_in", a.Binding.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("historical binding reactivation reserved: %v", err)
	}
	if secrets.sets != sets {
		t.Fatal("historical rejection wrote secret")
	}
	current, err := db.CurrentModelAuthorization(b.AccountID)
	if err != nil || current.Binding.ID != b.ID || current.State != "available" {
		t.Fatal("current selection moved", current, err)
	}
}

type rejectRepeatedAuthorizationDelete struct {
	*memorySecrets
	deleted map[string]bool
}

func (s *rejectRepeatedAuthorizationDelete) Delete(ref string) error {
	if s.deleted[ref] {
		return errors.New("already cleaned reference must not be retried")
	}
	if err := s.memorySecrets.Delete(ref); err != nil {
		return err
	}
	s.deleted[ref] = true
	return nil
}
func TestAuthorizationCleanupOnlyTargetsNewlySupersededReferences(t *testing.T) {
	original, db, secrets, _ := setupAccounts(t)
	once := &rejectRepeatedAuthorizationDelete{memorySecrets: secrets, deleted: map[string]bool{}}
	s := New(db, once)
	sel := selectionFixture(t, original, db, true)
	a := commitAuthorization(t, s, &sel, "first", "sign_in", []string{"resource.invoke", "chatgpt.tokens.use.direct"})
	commitAuthorization(t, s, &sel, "second", "refresh", a.Binding.Scopes)
	commitAuthorization(t, s, &sel, "third", "refresh", a.Binding.Scopes)
	if _, err := s.RevokeModelAuthorization(context.Background(), a.Binding.ID); err != nil {
		t.Fatal("revoke reopened historical cleanup", err)
	}
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal("recovery reopened historical cleanup", err)
	}
	if len(once.deleted) != 3 {
		t.Fatalf("cleanup did not target three actual token refs: %d", len(once.deleted))
	}
}

func TestReauthorizationDoesNotReopenAlreadyCleanedCurrentReference(t *testing.T) {
	original, db, secrets, _ := setupAccounts(t)
	once := &rejectRepeatedAuthorizationDelete{memorySecrets: secrets, deleted: map[string]bool{}}
	s := New(db, once)
	sel := selectionFixture(t, original, db, true)
	a := commitAuthorization(t, s, &sel, "first", "sign_in", []string{"resource.invoke", "chatgpt.tokens.use.direct"})
	if _, err := s.RevokeModelAuthorization(context.Background(), a.Binding.ID); err != nil {
		t.Fatal(err)
	}
	commitAuthorization(t, s, &sel, "reauth", "sign_in", a.Binding.Scopes)
	if len(once.deleted) != 1 {
		t.Fatal("reauth repeated old token cleanup")
	}
}

func TestUnknownRefreshRetainsOldBundleButNeverLeasesOrRetries(t *testing.T) {
	for _, finish := range []string{"revoke", "new_grant"} {
		t.Run(finish, func(t *testing.T) {
			s, db, secrets, _ := setupAccounts(t)
			sel := selectionFixture(t, s, db, true)
			ctx := context.Background()
			a := commitAuthorization(t, s, &sel, "login", "sign_in", []string{"resource.invoke", "chatgpt.tokens.use.direct"})
			op, err := s.BeginModelAuthorization(ctx, a.Binding.AccountID, "refresh", "refresh", a.Binding.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.FailModelAuthorization(ctx, op.ID, "refresh_unknown"); err != nil {
				t.Fatal(err)
			}
			secrets.mu.Lock()
			old := secrets.values[a.SecretRef]
			secrets.mu.Unlock()
			if old == "" {
				t.Fatal("unknown refresh erased old token bundle")
			}
			if _, err = s.ResolveModelCredential(ctx, sel); err == nil {
				t.Fatal("unknown refresh leased old token")
			}
			if _, err = s.ReadModelAuthorizationRefresh(ctx, op.ID); err == nil {
				t.Fatal("unknown refresh retried")
			}
			if _, err = s.BeginModelAuthorization(ctx, a.Binding.AccountID, "retry", "refresh", a.Binding.ID); err == nil {
				t.Fatal("unknown refresh reserved automatic retry")
			}
			if err = s.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			secrets.mu.Lock()
			old = secrets.values[a.SecretRef]
			secrets.mu.Unlock()
			if old == "" {
				t.Fatal("recovery erased retained old bundle")
			}
			if finish == "revoke" {
				if _, err = s.RevokeModelAuthorization(ctx, a.Binding.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				b := a.Binding
				b.ID = "new-grant"
				b.Scopes = append(append([]string{}, b.Scopes...), "openid")
				if _, err = s.BeginModelAuthorization(ctx, b.AccountID, "reauth", "sign_in", b.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = s.CommitModelAuthorization(ctx, "reauth", b, ModelTokenBundle{AccessToken: "FAKE-REAUTHORIZED", ExpiresAt: "2099-01-01T00:00:00Z", Resource: sel.Route.BaseURL}); err != nil {
					t.Fatal(err)
				}
			}
			secrets.mu.Lock()
			_, exists := secrets.values[a.SecretRef]
			secrets.mu.Unlock()
			if exists {
				t.Fatal("explicit replacement/revocation retained obsolete bundle")
			}
		})
	}
}

func TestFailedReauthorizationRetainsPreviousBundleUntilExplicitRevoke(t *testing.T) {
	for _, failure := range []string{"cancel", "partial_write"} {
		t.Run(failure, func(t *testing.T) {
			s, db, secrets, _ := setupAccounts(t)
			sel := selectionFixture(t, s, db, true)
			ctx := context.Background()
			a := commitAuthorization(t, s, &sel, "initial", "sign_in", []string{"resource.invoke", "chatgpt.tokens.use.direct"})
			if _, err := s.BeginModelAuthorization(ctx, a.Binding.AccountID, "refresh", "refresh", a.Binding.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.FailModelAuthorization(ctx, "refresh", "refresh_unknown"); err != nil {
				t.Fatal(err)
			}
			attempt, err := s.BeginModelAuthorization(ctx, a.Binding.AccountID, "reauthorize", "sign_in", a.Binding.ID)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "cancel" {
				if err = s.FailModelAuthorization(ctx, attempt.ID, "failed"); err != nil {
					t.Fatal(err)
				}
			} else {
				secrets.setErr = errors.New("synthetic partial write failure")
				if _, err = s.CommitModelAuthorization(ctx, attempt.ID, a.Binding, ModelTokenBundle{AccessToken: "FAKE-FAILED-REAUTH", ExpiresAt: "2099-01-01T00:00:00Z", Resource: sel.Route.BaseURL}); err == nil {
					t.Fatal("partial write reported success")
				}
			}
			secrets.mu.Lock()
			old := secrets.values[a.SecretRef]
			_, attemptExists := secrets.values[attempt.SecretRef]
			secrets.mu.Unlock()
			if old == "" {
				t.Fatal("failed reauthorization erased previously retained bundle")
			}
			if attemptExists {
				t.Fatal("failed attempt secret survived cleanup")
			}
			if _, err = s.ResolveModelCredential(ctx, sel); err == nil {
				t.Fatal("failed reauthorization restored inference")
			}
			if err = s.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			secrets.mu.Lock()
			old = secrets.values[a.SecretRef]
			secrets.mu.Unlock()
			if old == "" {
				t.Fatal("recovery erased retained bundle after failed reauthorization")
			}
			if _, err = s.RevokeModelAuthorization(ctx, a.Binding.ID); err != nil {
				t.Fatal(err)
			}
			secrets.mu.Lock()
			_, exists := secrets.values[a.SecretRef]
			secrets.mu.Unlock()
			if exists {
				t.Fatal("explicit revoke did not clean retained bundle")
			}
		})
	}
}
