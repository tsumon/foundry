package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/domain"
)

// Removing metadata blockers or adding any write to inspection must fail these tests.
func TestModelAcceptanceCredentialMetadata(t *testing.T) {
	for _, state := range []string{"available", "missing", "pending", "revoked", "cleanup", "cleanup_with_current", "rotation", "disabled", "no_routes", "invalid_route", "unknown_extra"} {
		t.Run(state, func(t *testing.T) {
			s := openTestStore(t)
			a, err := s.SaveModelAccount(domain.ModelAccount{ID: "acceptance", Name: "Synthetic", Provider: "kimi", BaseURL: "http://127.0.0.1:1234/v1", AuthMethod: "api_key", BillingMode: "subscription", Enabled: state != "disabled"}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if state != "missing" {
				if _, err = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "fake", SecretRef: "foundry-model-account-fake"}, 1); err != nil {
					t.Fatal(err)
				}
				if state != "pending" {
					finish := "available"
					if state == "cleanup" {
						finish = "failed"
					}
					if _, err = s.FinishModelCredential("fake", finish); err != nil {
						t.Fatal(err)
					}
				}
				if state == "revoked" {
					if _, err = s.RevokeModelCredential(a.ID, 1, 3); err != nil {
						t.Fatal(err)
					}
				}
				if state == "rotation" || state == "cleanup_with_current" {
					if _, err = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "rotation", SecretRef: "foundry-model-account-rotation"}, 3); err != nil {
						t.Fatal(err)
					}
					if state == "cleanup_with_current" {
						if _, err = s.FinishModelCredential("rotation", "failed"); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if state != "no_routes" {
				adapter, extra := "kimi_code_chat", "off"
				if state == "invalid_route" {
					adapter = "https://private.invalid/secret"
				}
				if state == "unknown_extra" {
					extra = "unknown"
				}
				_, err = s.db.Exec(`INSERT INTO model_routes VALUES('route',? ,?,'chat_completions',?,'simulation','interactive_package',?,'now')`, a.ID, adapter, a.BaseURL, extra)
				if err != nil {
					t.Fatal(err)
				}
			}
			before := acceptanceSnapshot(t, s)
			report, err := s.ReadModelAccountAcceptance(context.Background(), a.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "blocked"
			if state == "available" {
				want = "ready"
			}
			if report.Checks[0].Phase != "local_metadata" || report.Checks[0].State != want {
				t.Fatalf("local metadata: %+v", report)
			}
			wantReasons := map[string][]string{"available": {}, "missing": {"credential_metadata_unavailable"}, "pending": {"credential_metadata_unavailable"}, "revoked": {"credential_metadata_unavailable"}, "cleanup": {"credential_metadata_unavailable"}, "cleanup_with_current": {"credential_metadata_unavailable"}, "rotation": {"credential_metadata_unavailable"}, "disabled": {"account_disabled"}, "no_routes": {"route_missing"}, "invalid_route": {"route_invalid"}, "unknown_extra": {"extra_usage_local_setting_unknown"}}
			if !reflect.DeepEqual(report.Checks[0].ReasonCodes, wantReasons[state]) {
				t.Fatalf("reasons=%v", report.Checks[0].ReasonCodes)
			}
			assertAcceptanceClosed(t, report)
			if state == "no_routes" && (report.Routes == nil || len(report.Routes) != 0) {
				t.Fatalf("routes=%+v", report.Routes)
			}
			if state != "no_routes" && (len(report.Routes) != 1 || report.Routes[0].LocalMetadata != want || report.Routes[0].RemoteExtraUsage != "unverified") {
				t.Fatalf("routes=%+v", report.Routes)
			}
			raw, _ := json.Marshal(report)
			for _, secret := range []string{"secret_ref", "foundry-model-account", "127.0.0.1", "private.invalid", "base_url"} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("unsafe projection: %s", raw)
				}
			}
			if after := acceptanceSnapshot(t, s); !reflect.DeepEqual(before, after) {
				t.Fatal("inspection changed database", before, after)
			}
			second, err := s.ReadModelAccountAcceptance(context.Background(), a.ID)
			if err != nil || !reflect.DeepEqual(report, second) {
				t.Fatal("repeated inspection changed", second, err)
			}
		})
	}
}

func TestModelAcceptanceAuthorizationMetadata(t *testing.T) {
	for _, state := range []string{"available", "missing", "expired", "refreshing", "refresh_unknown", "revoked", "identity_only"} {
		t.Run(state, func(t *testing.T) {
			s := openTestStore(t)
			b := authorizationFixture(t, s)
			if state != "missing" {
				if state == "identity_only" {
					b.Scopes = []string{"openid"}
				}
				if _, err := s.BeginModelAuthorization(b.AccountID, "login", "sign_in", b.ID); err != nil {
					t.Fatal(err)
				}
				expiry := "2099-01-01T00:00:00Z"
				if state == "expired" {
					expiry = "2000-01-01T00:00:00Z"
				}
				if _, err := s.CommitModelAuthorization("login", b, expiry); err != nil {
					t.Fatal(err)
				}
				if state == "refreshing" || state == "refresh_unknown" {
					if _, err := s.BeginModelAuthorization(b.AccountID, "refresh", "refresh", b.ID); err != nil {
						t.Fatal(err)
					}
				}
				if state == "refresh_unknown" {
					if err := s.FailModelAuthorization("refresh", "refresh_unknown"); err != nil {
						t.Fatal(err)
					}
				}
				if state == "revoked" {
					if _, err := s.RevokeModelAuthorization(b.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := s.CreateModelRoute(domain.ModelRoute{ID: "oauth-route", AccountID: b.AccountID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: "http://127.0.0.1:1234/v1", Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"}); err != nil {
				t.Fatal(err)
			}
			before := acceptanceSnapshot(t, s)
			report, err := s.ReadModelAccountAcceptance(context.Background(), b.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			want := "blocked"
			if state == "available" {
				want = "ready"
			}
			if report.Checks[0].State != want || report.Routes[0].LocalMetadata != want {
				t.Fatalf("report=%+v", report)
			}
			wantReasons := map[string][]string{"available": {}, "missing": {"authorization_unavailable"}, "expired": {"authorization_expired"}, "refreshing": {"authorization_unavailable"}, "refresh_unknown": {"authorization_unavailable"}, "revoked": {"authorization_unavailable"}, "identity_only": {"inference_scope_missing"}}
			if !reflect.DeepEqual(report.Checks[0].ReasonCodes, wantReasons[state]) {
				t.Fatalf("reasons=%v", report.Checks[0].ReasonCodes)
			}
			assertAcceptanceClosed(t, report)
			if !reflect.DeepEqual(before, acceptanceSnapshot(t, s)) {
				t.Fatal("inspection mutated authorization or database")
			}
		})
	}
}

func TestModelAcceptanceReadFailuresDoNotRepairSchema(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.ReadModelAccountAcceptance(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown account=%v", err)
	}
	b := authorizationFixture(t, s)
	if _, err := s.db.Exec(`DROP TABLE model_authorizations`); err != nil {
		t.Fatal(err)
	}
	before := acceptanceSnapshot(t, s)
	if _, err := s.ReadModelAccountAcceptance(context.Background(), b.AccountID); err == nil {
		t.Fatal("missing schema became healthy")
	}
	if !reflect.DeepEqual(before, acceptanceSnapshot(t, s)) {
		t.Fatal("read repaired schema")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadModelAccountAcceptance(context.Background(), b.AccountID); err == nil {
		t.Fatal("closed storage became healthy")
	}
}

// A permissive legacy identifier must never be reflected by the safe diagnostic DTO.
func TestModelAcceptanceRejectsUnsafePersistedIdentifiers(t *testing.T) {
	for _, field := range []string{"account", "route"} {
		for _, id := range []string{"https://private.invalid/secret", "private.invalid", "unsafe id", "unsafe\nsecret", "账户", "unsafe%2Fsecret"} {
			t.Run(field+"/"+id, func(t *testing.T) {
				s := openTestStore(t)
				accountID, routeID := "acceptance", "route"
				if field == "account" {
					accountID = id
				} else {
					routeID = id
				}
				a, err := s.SaveModelAccount(domain.ModelAccount{ID: accountID, Name: "Synthetic", Provider: "kimi", BaseURL: "http://127.0.0.1:1234/v1", AuthMethod: "api_key", BillingMode: "subscription", Enabled: true}, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "fake", SecretRef: "foundry-model-account-fake"}, 1); err != nil {
					t.Fatal(err)
				}
				if _, err = s.FinishModelCredential("fake", "available"); err != nil {
					t.Fatal(err)
				}
				if _, err = s.CreateModelRoute(domain.ModelRoute{ID: routeID, AccountID: a.ID, Adapter: "kimi_code_chat", Protocol: "chat_completions", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "interactive_package", ExtraUsage: "off"}); err != nil {
					t.Fatal(err)
				}
				before := acceptanceSnapshot(t, s)
				for attempt := 0; attempt < 2; attempt++ {
					report, readErr := s.ReadModelAccountAcceptance(context.Background(), a.ID)
					if readErr == nil {
						t.Errorf("unsafe %s identifier produced a report: %+v", field, report)
					}
					if !reflect.DeepEqual(report, domain.ModelAccountAcceptance{}) {
						t.Error("unsafe snapshot returned partial data")
					}
					if readErr != nil && strings.Contains(readErr.Error(), id) {
						t.Error("unsafe identifier reflected in error")
					}
				}
				if !reflect.DeepEqual(before, acceptanceSnapshot(t, s)) {
					t.Fatal("rejected diagnostic mutated storage")
				}
			})
		}
	}
}

func TestModelAcceptanceAcceptsSafeIdentifierBoundaries(t *testing.T) {
	for _, id := range []string{"A", strings.Repeat("a", 253) + "Z_-"} {
		t.Run(id, func(t *testing.T) {
			s := openTestStore(t)
			a, err := s.SaveModelAccount(domain.ModelAccount{ID: id, Name: "Synthetic", Provider: "kimi", BaseURL: "http://127.0.0.1:1234/v1", AuthMethod: "api_key", BillingMode: "subscription", Enabled: true}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.CreateModelRoute(domain.ModelRoute{ID: id, AccountID: a.ID, Adapter: "kimi_code_chat", Protocol: "chat_completions", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "interactive_package", ExtraUsage: "off"}); err != nil {
				t.Fatal(err)
			}
			report, err := s.ReadModelAccountAcceptance(context.Background(), a.ID)
			if err != nil || report.AccountID != id || len(report.Routes) != 1 || report.Routes[0].ID != id {
				t.Fatal(report, err)
			}
		})
	}
}

func acceptanceSnapshot(t *testing.T, s *Store) []string {
	t.Helper()
	// total_changes catches even no-op writes; schema_version catches lazy DDL.
	result := []string{}
	for _, q := range []string{`PRAGMA schema_version`, `SELECT total_changes()`, `SELECT group_concat(id || ':' || revision || ':' || current_credential_version) FROM model_accounts`, `SELECT count(*) FROM events`, `SELECT count(*) FROM model_authorization_operations`, `SELECT count(*) FROM read_grants`, `SELECT count(*) FROM invocations`} {
		var value any
		if err := s.db.QueryRow(q).Scan(&value); err != nil {
			t.Fatal(q, err)
		}
		raw, _ := json.Marshal(value)
		result = append(result, string(raw))
	}
	return result
}

func assertAcceptanceClosed(t *testing.T, r domain.ModelAccountAcceptance) {
	t.Helper()
	if r.Scope != "account_routes_only" || r.LiveAccess != "blocked" || r.Quota != "unknown" || len(r.Checks) != 5 || r.Routes == nil {
		t.Fatalf("unsafe report=%+v", r)
	}
	for _, c := range r.Checks[1:4] {
		if c.State != "blocked" || !reflect.DeepEqual(c.ReasonCodes, []string{"live_provider_access_pending_confirmation"}) {
			t.Fatalf("live check=%+v", c)
		}
	}
	if r.Checks[4].Phase != "task" || r.Checks[4].State != "not_evaluated" {
		t.Fatalf("task check=%+v", r.Checks[4])
	}
}
