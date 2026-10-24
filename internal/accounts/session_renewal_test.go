package accounts

import (
	"context"
	"database/sql"
	"errors"
	"foundry/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSessionRenewalLegacyEarliestWaitsWithoutConsumingGeneration(t *testing.T) {
	service, ledger, _, path := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	issuer.earliestRefreshAt = time.Now().Add(2 * time.Hour).Unix()
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	// Simulate a pre-upgrade generation, whose schedule exists only in its bundle.
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err = raw.Exec(`DELETE FROM session_renewal_schedule`); err != nil {
		t.Fatal(err)
	}
	before, _ := ledger.CurrentModelAuthorization(account.ID)
	now := time.Now()
	c := NewSessionRenewal(siwc, func() time.Time { return now })
	defer c.Close()
	m, err := c.Prepare(account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Approve(account.ID, m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	expiry, _ := time.Parse(time.RFC3339, before.ExpiresAt)
	now = expiry.Add(-5 * time.Minute)
	c.Tick(context.Background())
	c.Tick(context.Background())
	after, _ := ledger.GetModelAuthorization(before.Binding.ID)
	if after.State != "available" || issuer.tokenCalls.Load() != 1 {
		t.Fatal("known early time consumed grant or sent request")
	}
	now = time.Unix(issuer.earliestRefreshAt, 0)
	issuer.mu.Lock()
	issuer.nonce = ""
	issuer.mu.Unlock()
	c.Tick(context.Background())
	after, _ = ledger.GetModelAuthorization(before.Binding.ID)
	if after.Generation != before.Generation+1 || issuer.tokenCalls.Load() != 2 {
		t.Fatal("permitted later refresh failed")
	}
}

func TestSessionRenewalFailuresNeverReplay(t *testing.T) {
	for _, name := range []string{"subject", "client", "scopes", "resource", "invalid_grant", "truncated", "server", "missing_token", "secret_get", "secret_set", "secret_delete", "metadata_commit", "revoked", "earliest"} {
		t.Run(name, func(t *testing.T) {
			service, ledger, secrets, path := setupAccounts(t)
			resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("model/resource called") }))
			defer resource.Close()
			account := newSIWCAccount(t, service, resource.URL+"/v1")
			issuer := newSIWCIssuerFixture(t, account.BaseURL)
			if name == "earliest" {
				issuer.earliestRefreshAt = time.Now().Add(2 * time.Hour).Unix()
			}
			siwc, _ := NewSIWCSimulation(service, issuer.options())
			completeInitialSIWCSignIn(t, siwc, account, issuer)
			before, _ := ledger.CurrentModelAuthorization(account.ID)
			now := time.Now()
			c := NewSessionRenewal(siwc, func() time.Time { return now })
			defer c.Close()
			m, err := c.Prepare(account.ID, account.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Approve(account.ID, m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			expiry, _ := time.Parse(time.RFC3339, before.ExpiresAt)
			now = expiry.Add(-5 * time.Minute)
			issuer.mu.Lock()
			issuer.nonce = ""
			issuer.mu.Unlock()
			switch name {
			case "subject":
				issuer.subject = "changed"
			case "client":
				issuer.identityResponse = func(client, nonce string) map[string]any {
					return map[string]any{"access_token": "synthetic-next", "refresh_token": "synthetic-next-refresh", "id_token": issuer.idToken("wrong-client", nonce, issuer.subject, time.Now().Add(time.Hour)), "token_type": "Bearer", "expires_in": 3600, "scope": "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"}
				}
			case "scopes":
				issuer.scopes = append(issuer.scopes, "extra")
			case "resource":
				raw := secrets.values[before.SecretRef]
				secrets.values[before.SecretRef] = strings.Replace(raw, account.BaseURL, "https://other.invalid/v1", 1)
			case "invalid_grant":
				issuer.tokenStatus = 400
				issuer.tokenError = "invalid_grant"
			case "truncated":
				issuer.dropToken = true
			case "server":
				issuer.tokenStatus = 503
				issuer.tokenError = "server_error"
			case "missing_token":
				secrets.values[before.SecretRef] = "{}"
			case "secret_set":
				secrets.setErr = errors.New("synthetic fault")
			case "secret_get":
				secrets.getErr = errors.New("synthetic fault")
			case "secret_delete":
				secrets.deleteErr = errors.New("synthetic fault")
			case "metadata_commit":
				raw, e := sql.Open("sqlite3", path)
				if e != nil {
					t.Fatal(e)
				}
				defer raw.Close()
				if _, e = raw.Exec(`CREATE TRIGGER synthetic_renewal_commit_fault BEFORE UPDATE ON model_authorizations WHEN NEW.generation>OLD.generation BEGIN SELECT RAISE(ABORT,'synthetic commit fault'); END`); e != nil {
					t.Fatal(e)
				}
			case "revoked":
				if _, err = service.RevokeModelAuthorization(context.Background(), before.Binding.ID); err != nil {
					t.Fatal(err)
				}
			}
			calls := issuer.tokenCalls.Load()
			c.Tick(context.Background())
			c.Tick(context.Background())
			after, _ := ledger.GetModelAuthorization(before.Binding.ID)
			expectedCalls := int32(1)
			if name == "resource" || name == "missing_token" || name == "secret_get" || name == "revoked" || name == "earliest" {
				expectedCalls = 0
			}
			if issuer.tokenCalls.Load()-calls != expectedCalls {
				t.Fatalf("requests=%d", issuer.tokenCalls.Load()-calls)
			}
			if name != "earliest" && name != "secret_delete" && after.State == "available" {
				t.Fatal("failure left credentials available")
			}
			receipt, _ := ledger.SessionRenewalReceipt(account.ID)
			if name == "server" && (receipt == nil || receipt.Certainty != "unknown") {
				t.Fatal("server failure certainty understated")
			}
			if name == "secret_delete" && (receipt == nil || receipt.NewGeneration != before.Generation+1 || receipt.Certainty != "confirmed") {
				t.Fatal("committed replacement lost in cleanup failure")
			}
			restarted := NewSessionRenewal(siwc, func() time.Time { return now })
			defer restarted.Close()
			restarted.Tick(context.Background())
			if issuer.tokenCalls.Load()-calls != expectedCalls {
				t.Fatal("restart replayed rotating token")
			}
		})
	}
}

func TestSessionRenewalTwoStoresWithdrawalFencesCommit(t *testing.T) {
	service, ledger, secrets, path := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	before, _ := ledger.CurrentModelAuthorization(account.ID)
	now := time.Now()
	c := NewSessionRenewal(siwc, func() time.Time { return now })
	defer c.Close()
	m, _ := c.Prepare(account.ID, account.Revision)
	approved, err := c.Approve(account.ID, m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	expiry, _ := time.Parse(time.RFC3339, before.ExpiresAt)
	now = expiry.Add(-5 * time.Minute)
	issuer.mu.Lock()
	issuer.nonce = ""
	issuer.mu.Unlock()
	sent, release := make(chan struct{}), make(chan struct{})
	issuer.onToken = func(form url.Values) {
		if form.Get("grant_type") == "refresh_token" {
			close(sent)
			<-release
		}
	}
	done := make(chan struct{})
	go func() { c.Tick(context.Background()); close(done) }()
	<-sent
	other, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	secondSIWC, _ := NewSIWCSimulation(New(other, secrets), issuer.options())
	second := NewSessionRenewal(secondSIWC, func() time.Time { return now })
	defer second.Close()
	second.Tick(context.Background())
	status, err := second.Disable(account.ID, approved.Revision)
	if err != nil || status.Enabled || status.State != "in_progress" {
		t.Fatalf("remote withdrawal: %+v %v", status, err)
	}
	if _, err = second.Prepare(account.ID, account.Revision); err == nil {
		t.Fatal("inflight consent replaced")
	}
	close(release)
	<-done
	status, err = second.Status(account.ID)
	if err != nil || status.Enabled || status.State != "recovery_required" {
		t.Fatalf("final withdrawal: %+v %v", status, err)
	}
	after, _ := ledger.GetModelAuthorization(before.Binding.ID)
	if after.Generation != before.Generation || after.State != "refresh_unknown" {
		t.Fatalf("withdrawn commit survived: %+v", after)
	}
	second.Tick(context.Background())
	if issuer.tokenCalls.Load() != 2 {
		t.Fatal("duplicate dispatch")
	}
}

func TestSessionRenewalDisabledHasNoEffects(t *testing.T) {
	service, _, secrets, _ := setupAccounts(t)
	account := newSIWCAccount(t, service, "https://api.openai.com/v1")
	c := NewSessionRenewal(NewSIWCService(service), time.Now)
	defer c.Close()
	status, err := c.Status(account.ID)
	if err != nil || status.Enabled {
		t.Fatalf("default: %+v %v", status, err)
	}
	c.Tick(context.Background())
	if secrets.gets != 0 || secrets.sets != 0 || secrets.deletes != 0 {
		t.Fatal("disabled renewal touched secrets")
	}
}

func TestSessionRenewalApprovalRotationAndDuplicateTick(t *testing.T) {
	service, ledger, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("resource invoked") }))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	before, _ := ledger.CurrentModelAuthorization(account.ID)
	now := time.Now()
	c := NewSessionRenewal(siwc, func() time.Time { return now })
	defer c.Close()
	gets, sets, deletes := secrets.gets, secrets.sets, secrets.deletes
	if _, err := c.Prepare(account.ID, account.Revision+1); err == nil {
		t.Fatal("stale account prepared")
	}
	material, err := c.Prepare(account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Approve(account.ID, material.ID, material.Revision+1); err == nil {
		t.Fatal("stale approval enabled renewal")
	}
	if secrets.gets != gets || secrets.sets != sets || secrets.deletes != deletes {
		t.Fatal("prepare read credentials")
	}
	status, err := c.Approve(account.ID, material.ID, material.Revision)
	if err != nil || !status.Enabled {
		t.Fatalf("approve: %+v %v", status, err)
	}
	if _, err = c.Prepare(account.ID, account.Revision); err == nil {
		t.Fatal("enabled consent silently replaced by preparation")
	}
	expiry, _ := time.Parse(time.RFC3339, before.ExpiresAt)
	now = expiry.Add(-5 * time.Minute)
	issuer.mu.Lock()
	issuer.nonce = ""
	issuer.mu.Unlock()
	calls := issuer.tokenCalls.Load()
	c.Tick(context.Background())
	c.Tick(context.Background())
	after, _ := ledger.GetModelAuthorization(before.Binding.ID)
	if after.Binding.ID != before.Binding.ID || after.Generation != before.Generation+1 {
		t.Fatalf("rotation: %+v", after)
	}
	if issuer.tokenCalls.Load()-calls != 1 {
		t.Fatal("duplicate refresh dispatched")
	}
	if issuer.lastToken.Has("scope") {
		t.Fatal("refresh requested scope expansion")
	}
	status, _ = c.Status(account.ID)
	if _, err := c.Disable(account.ID, status.Revision); err != nil {
		t.Fatal(err)
	}
	c.Tick(context.Background())
	status, _ = c.Status(account.ID)
	if status.Enabled {
		t.Fatal("withdrawal resurrected")
	}
	if _, err = c.Approve(account.ID, material.ID, status.Revision); err == nil {
		t.Fatal("withdrawn material reused without fresh preparation")
	}
}

func TestSessionRenewalMaterialExpires(t *testing.T) {
	service, _, _, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	now := time.Now()
	c := NewSessionRenewal(siwc, func() time.Time { return now })
	defer c.Close()
	material, err := c.Prepare(account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	if _, err = c.Approve(account.ID, material.ID, material.Revision); err == nil {
		t.Fatal("expired material accepted")
	}
	now = now.Add(-10 * time.Minute)
	approved, err := c.Approve(account.ID, material.ID, material.Revision)
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := c.Disable(account.ID, approved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Approve(account.ID, material.ID, disabled.Revision); err == nil {
		t.Fatal("withdrawn material reused without fresh preparation")
	}
}

func TestSessionRenewalLocalWithdrawalCancelsAndJoins(t *testing.T) {
	service, ledger, _, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	before, _ := ledger.CurrentModelAuthorization(account.ID)
	now := time.Now()
	c := NewSessionRenewal(siwc, func() time.Time { return now })
	defer c.Close()
	m, err := c.Prepare(account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := c.Approve(account.ID, m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	expiry, _ := time.Parse(time.RFC3339, before.ExpiresAt)
	now = expiry.Add(-5 * time.Minute)
	sent, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	issuer.onToken = func(form url.Values) {
		if form.Get("grant_type") == "refresh_token" {
			close(sent)
			<-release
		}
	}
	done := make(chan struct{})
	go func() { c.Tick(context.Background()); close(done) }()
	select {
	case <-sent:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh never sent")
	}
	status, err := c.Disable(account.ID, approved.Revision)
	if err != nil || status.Enabled || status.State != "recovery_required" {
		t.Fatalf("withdrawal: %+v %v", status, err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disable did not join")
	}
	after, _ := ledger.GetModelAuthorization(before.Binding.ID)
	if after.State != "refresh_unknown" || after.Generation != before.Generation {
		t.Fatal("canceled request advanced credentials")
	}
}

func TestSessionRenewalMaterialCannotAdoptChangedOrOtherAccount(t *testing.T) {
	service, _, secrets, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	completeInitialSIWCSignIn(t, siwc, account, issuer)
	c := NewSessionRenewal(siwc, time.Now)
	defer c.Close()
	m, err := c.Prepare(account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	other := newSIWCAccount(t, service, resource.URL+"/v1")
	gets, sets, deletes := secrets.gets, secrets.sets, secrets.deletes
	if _, err = c.Approve(other.ID, m.ID, m.Revision); err == nil {
		t.Fatal("cross-account material accepted")
	}
	account.Name = "renamed"
	if _, err = service.Save(account, account.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Approve(account.ID, m.ID, m.Revision); err == nil {
		t.Fatal("changed account adopted")
	}
	c.Tick(context.Background())
	if secrets.gets != gets || secrets.sets != sets || secrets.deletes != deletes || issuer.tokenCalls.Load() != 1 {
		t.Fatal("invalid material caused effects")
	}
}
