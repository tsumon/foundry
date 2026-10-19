package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"foundry/internal/domain"
)

func rolePolicyInput(profileID string, spend ...string) domain.RolePolicyInput {
	roles := map[domain.Role]domain.RolePolicyRoleInput{}
	for _, role := range domain.RolePolicyRoles() {
		roles[role] = domain.RolePolicyRoleInput{
			Candidates:           []domain.RolePolicyCandidateInput{{ProfileID: profileID, Capabilities: []string{"coding", "reasoning"}, Rationale: "User declaration; not independently verified."}},
			RequiredCapabilities: []string{"coding"}, AllowedSpendClasses: append([]string(nil), spend...), Preference: domain.RolePolicyPreferenceCandidateOrder,
		}
	}
	return domain.RolePolicyInput{Roles: roles}
}

func policyProfile(id, model string) domain.Profile {
	return domain.Profile{ID: id, Name: "Synthetic " + id, Kind: "local", Protocol: "responses", BaseURL: "http://127.0.0.1:1234/v1", Model: model}
}

func savePolicy(t *testing.T, s *Store, input domain.RolePolicyInput, expected int64) domain.RoleModelPolicy {
	t.Helper()
	state, err := s.GetRolePolicyState()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.SaveRoleModelPolicy(input, expected, state.ProfilesDigest)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestRolePolicyStateStartsWithExactEmptySevenRolesAndPoliciesAppend(t *testing.T) {
	s := openTestStore(t)
	if err := s.PutProfile(policyProfile("local", "m1")); err != nil {
		t.Fatal(err)
	}
	state, err := s.GetRolePolicyState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Policy.Revision != 0 || state.Policy.CreatedAt != "" || len(state.Policy.Roles) != 7 || len(state.ProfilesDigest) != 64 || state.LatestProposal != nil || state.ActiveDecision != nil {
		t.Fatalf("initial state = %+v", state)
	}
	for _, role := range domain.RolePolicyRoles() {
		got := state.Policy.Roles[role]
		if got.Preference != domain.RolePolicyPreferenceCandidateOrder || got.Candidates == nil || got.RequiredCapabilities == nil || got.AllowedSpendClasses == nil || len(got.Candidates)+len(got.RequiredCapabilities)+len(got.AllowedSpendClasses) != 0 {
			t.Fatalf("initial %s = %#v", role, got)
		}
	}
	first := savePolicy(t, s, rolePolicyInput("local", domain.RolePolicySpendUnknown), 0)
	second := savePolicy(t, s, rolePolicyInput("local", domain.RolePolicySpendUnknown, domain.RolePolicySpendExplicitAPI), 1)
	if first.Revision != 1 || second.Revision != 2 || first.CreatedAt == "" || second.CreatedAt == "" {
		t.Fatalf("revisions = %+v %+v", first, second)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM role_model_policies`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("policy history count = %d, %v", count, err)
	}
	if _, err := s.db.Exec(`UPDATE role_model_policies SET created_at='changed' WHERE revision=1`); err == nil {
		t.Fatal("append-only policy updated")
	}
	if _, err := s.SaveRoleModelPolicy(rolePolicyInput("local", domain.RolePolicySpendUnknown), 1, state.ProfilesDigest); !errors.Is(err, ErrRolePolicyStale) {
		t.Fatalf("stale save = %v", err)
	}
	if _, err := s.SaveRoleModelPolicy(rolePolicyInput("local", domain.RolePolicySpendUnknown), 2, strings.Repeat("0", 64)); !errors.Is(err, ErrRolePolicyStale) {
		t.Fatalf("stale profile digest save = %v", err)
	}
}

func TestRolePolicyUnknownSpendMustBeExplicitlyAllowedAndManualSettingsClearDecision(t *testing.T) {
	s := openTestStore(t)
	if err := s.PutProfile(policyProfile("legacy", "m")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSettings(domain.Settings{DefaultProfileID: "legacy", RoleBindings: map[domain.Role]string{}}); err != nil {
		t.Fatal(err)
	}
	savePolicy(t, s, rolePolicyInput("legacy", domain.RolePolicySpendExplicitAPI), 0)
	denied, err := s.ProposeRoleModels("proposal-denied")
	if err != nil {
		t.Fatal(err)
	}
	if denied.Confirmable || denied.Roles[domain.Main].SelectedProfileID != "" || !reflect.DeepEqual(denied.Roles[domain.Main].Candidates[0].Reasons, []string{domain.RolePolicyReasonSpendClassNotAllowed}) {
		t.Fatalf("unknown fee escaped explicit allowlist: %+v", denied.Roles[domain.Main])
	}
	savePolicy(t, s, rolePolicyInput("legacy", domain.RolePolicySpendUnknown), 1)
	proposal, err := s.ProposeRoleModels("proposal-allowed")
	if err != nil || !proposal.Confirmable || proposal.Roles[domain.Main].SelectedProfileID != "legacy" {
		t.Fatalf("allowed proposal = %+v, %v", proposal, err)
	}
	decision, settings, applied, err := s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "decision-one")
	if err != nil || !applied || decision.ID != "decision-one" || settings.ModelPolicyDecisionID != "decision-one" || len(settings.RoleBindings) != 7 {
		t.Fatalf("confirmation = %+v %+v %v %v", decision, settings, applied, err)
	}
	again, current, applied, err := s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "ignored-second-id")
	if err != nil || applied || again.ID != decision.ID || current.ModelPolicyDecisionID != decision.ID {
		t.Fatalf("duplicate confirmation = %+v %+v %v %v", again, current, applied, err)
	}
	if err := s.PutSettings(domain.Settings{DefaultProfileID: "legacy", RoleBindings: map[domain.Role]string{domain.Main: "legacy"}, ModelPolicyDecisionID: "forged"}); err != nil {
		t.Fatal(err)
	}
	manual, err := s.GetSettings()
	if err != nil || manual.ModelPolicyDecisionID != "" {
		t.Fatalf("generic settings retained policy marker: %+v %v", manual, err)
	}
	state, err := s.GetRolePolicyState()
	if err != nil || state.ActiveDecision != nil {
		t.Fatalf("manual settings retained active decision: %+v %v", state.ActiveDecision, err)
	}
	if _, _, applied, err = s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "third-id"); err != nil || applied {
		t.Fatalf("duplicate confirmation reapplied after manual settings: applied=%v err=%v", applied, err)
	}
}

func TestRolePolicyProposalDetectsPolicyProfileSettingsAndAccountDrift(t *testing.T) {
	t.Run("policy", func(t *testing.T) {
		s := openTestStore(t)
		_ = s.PutProfile(policyProfile("p", "m"))
		savePolicy(t, s, rolePolicyInput("p", domain.RolePolicySpendUnknown), 0)
		proposal, _ := s.ProposeRoleModels("policy-drift")
		savePolicy(t, s, rolePolicyInput("p", domain.RolePolicySpendUnknown), 1)
		if _, _, _, err := s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "d"); !errors.Is(err, ErrRolePolicyStale) {
			t.Fatalf("policy drift = %v", err)
		}
	})
	t.Run("profile", func(t *testing.T) {
		s := openTestStore(t)
		_ = s.PutProfile(policyProfile("p", "m"))
		savePolicy(t, s, rolePolicyInput("p", domain.RolePolicySpendUnknown), 0)
		proposal, _ := s.ProposeRoleModels("profile-drift")
		_ = s.PutProfile(policyProfile("p", "changed"))
		if _, _, _, err := s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "d"); !errors.Is(err, ErrRolePolicyStale) {
			t.Fatalf("profile drift = %v", err)
		}
	})
	t.Run("settings", func(t *testing.T) {
		s := openTestStore(t)
		_ = s.PutProfile(policyProfile("p", "m"))
		savePolicy(t, s, rolePolicyInput("p", domain.RolePolicySpendUnknown), 0)
		proposal, _ := s.ProposeRoleModels("settings-drift")
		_ = s.PutSettings(domain.Settings{DefaultProfileID: "p", RoleBindings: map[domain.Role]string{}})
		if _, _, _, err := s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "d"); !errors.Is(err, ErrRolePolicyStale) {
			t.Fatalf("settings drift = %v", err)
		}
	})
	t.Run("account", func(t *testing.T) {
		s := openTestStore(t)
		profile, account := rolePolicyAPIProfile(t, s, "drift", "off")
		savePolicy(t, s, rolePolicyInput(profile.ID, domain.RolePolicySpendExplicitAPI), 0)
		proposal, err := s.ProposeRoleModels("account-drift")
		if err != nil || !proposal.Confirmable {
			t.Fatalf("initial account proposal = %+v %v", proposal, err)
		}
		account.Enabled = false
		if _, err = s.SaveModelAccount(account, account.Revision); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err = s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "d"); !errors.Is(err, ErrRolePolicyStale) {
			t.Fatalf("account drift = %v", err)
		}
	})
}

func rolePolicyAPIProfile(t *testing.T, s *Store, suffix, extraUsage string) (domain.Profile, domain.ModelAccount) {
	t.Helper()
	baseURL := "http://127.0.0.1:1234/v1"
	account, err := s.SaveModelAccount(domain.ModelAccount{ID: "account-" + suffix, Name: "Synthetic", Provider: "openai", BaseURL: baseURL, AuthMethod: "api_key", BillingMode: "metered_api", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	account = snapshotVersion(t, s, account, "credential-"+suffix)
	route, err := s.CreateModelRoute(domain.ModelRoute{ID: "route-" + suffix, AccountID: account.ID, Adapter: "openai_api_responses", Protocol: "responses", BaseURL: baseURL, Environment: "simulation", SpendClass: domain.RolePolicySpendExplicitAPI, ExtraUsage: extraUsage})
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.Profile{ID: "profile-" + suffix, Name: "Synthetic", Kind: "openai", AccountID: account.ID, RouteID: route.ID, Protocol: route.Protocol, BaseURL: baseURL, Model: "synthetic"}
	if err := s.PutProfile(profile); err != nil {
		t.Fatal(err)
	}
	return profile, account
}

func TestRolePolicyAccountAvailabilityAndExtraUsageFailClosed(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *Store) string
		want    string
	}{
		{"disabled", func(t *testing.T, s *Store) string {
			p, a := rolePolicyAPIProfile(t, s, "disabled", "off")
			a.Enabled = false
			if _, err := s.SaveModelAccount(a, a.Revision); err != nil {
				t.Fatal(err)
			}
			return p.ID
		}, domain.RolePolicyReasonAccountDisabled},
		{"revoked credential", func(t *testing.T, s *Store) string {
			p, a := rolePolicyAPIProfile(t, s, "revoked", "off")
			if _, err := s.RevokeModelCredential(a.ID, a.CurrentCredentialVersion, a.Revision); err != nil {
				t.Fatal(err)
			}
			return p.ID
		}, domain.RolePolicyReasonCredentialUnavailable},
		{"missing account", func(t *testing.T, s *Store) string {
			p := policyProfile("missing-account", "m")
			p.AccountID = "absent"
			if err := s.PutProfile(p); err != nil {
				t.Fatal(err)
			}
			return p.ID
		}, domain.RolePolicyReasonCredentialUnavailable},
		{"extra usage unknown", func(t *testing.T, s *Store) string {
			p, _ := rolePolicyAPIProfile(t, s, "extra", "unknown")
			return p.ID
		}, domain.RolePolicyReasonExtraUsageNotOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTestStore(t)
			profileID := tt.prepare(t, s)
			savePolicy(t, s, rolePolicyInput(profileID, domain.RolePolicySpendExplicitAPI, domain.RolePolicySpendUnknown), 0)
			proposal, err := s.ProposeRoleModels("p-" + strings.ReplaceAll(tt.name, " ", "-"))
			if err != nil {
				t.Fatal(err)
			}
			candidate := proposal.Roles[domain.Main].Candidates[0]
			if candidate.Eligible || !containsString(candidate.Reasons, tt.want) {
				t.Fatalf("candidate = %+v, want reason %q", candidate, tt.want)
			}
		})
	}
}

func rolePolicyAuthorizationProfile(t *testing.T, s *Store) domain.Profile {
	t.Helper()
	account, err := s.SaveModelAccount(domain.ModelAccount{ID: "auth-account", Name: "Synthetic", Provider: "openai", BaseURL: "http://127.0.0.1:1234/v1", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	route, err := s.CreateModelRoute(domain.ModelRoute{ID: "auth-route", AccountID: account.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: account.BaseURL, Environment: "simulation", SpendClass: domain.RolePolicySpendSubscriptionOnly, ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginModelAuthorization(account.ID, "auth-op", "sign_in", "auth-binding"); err != nil {
		t.Fatal(err)
	}
	binding := domain.ModelAuthorizationBinding{ID: "auth-binding", AccountID: account.ID, Issuer: "https://issuer.invalid", Subject: "synthetic-subject", ClientID: "synthetic-client", SpendClass: domain.RolePolicySpendSubscriptionOnly, Scopes: []string{"resource.invoke", "chatgpt.tokens.use.direct"}}
	if _, err = s.CommitModelAuthorization("auth-op", binding, "2099-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	profile := domain.Profile{ID: "auth-profile", Name: "Synthetic", Kind: "openai", AccountID: account.ID, RouteID: route.ID, Protocol: route.Protocol, BaseURL: account.BaseURL, Model: "synthetic"}
	if err = s.PutProfile(profile); err != nil {
		t.Fatal(err)
	}
	return profile
}

// A read failure must not silently redirect any role to its second candidate.
func TestRolePolicyStorageFailuresAbortProposalAndConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, fault   string
		authorization bool
	}{
		{"route query", `DROP TABLE model_routes`, false},
		{"route scan", `ALTER TABLE model_routes RENAME TO unreadable_routes; CREATE VIEW model_routes AS SELECT id,account_id,adapter,protocol,base_url,environment,spend_class,extra_usage,NULL AS created_at FROM unreadable_routes`, false},
		{"credential query", `DROP TABLE model_credential_versions`, false},
		{"credential scan", `ALTER TABLE model_credential_versions RENAME TO unreadable_credentials; CREATE VIEW model_credential_versions AS SELECT account_id,version,operation_id,state,secret_ref,'invalid' AS cleanup_pending,created_at,updated_at FROM unreadable_credentials`, false},
		{"authorization query", `DROP TABLE model_authorizations`, true},
		{"authorization decode", `DROP TRIGGER model_authorization_binding_immutable; UPDATE model_authorizations SET binding='{'`, true},
		{"authorization invalid format", `DROP TRIGGER model_authorization_binding_immutable; UPDATE model_authorizations SET binding='{}'`, true},
		{"authorization operation query", `DROP TABLE model_authorization_operations`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			var first domain.Profile
			if tc.authorization {
				first = rolePolicyAuthorizationProfile(t, s)
			} else {
				first, _ = rolePolicyAPIProfile(t, s, "first", "off")
			}
			if err := s.PutProfile(policyProfile("second", "legacy")); err != nil {
				t.Fatal(err)
			}
			if err := s.PutSettings(domain.Settings{DefaultProfileID: "second", RoleBindings: map[domain.Role]string{}}); err != nil {
				t.Fatal(err)
			}
			input := rolePolicyInput(first.ID, domain.RolePolicySpendExplicitAPI, domain.RolePolicySpendSubscriptionOnly, domain.RolePolicySpendUnknown)
			for _, role := range domain.RolePolicyRoles() {
				value := input.Roles[role]
				value.Candidates = append(value.Candidates, domain.RolePolicyCandidateInput{ProfileID: "second", Capabilities: []string{"coding"}, Rationale: "Synthetic second choice"})
				input.Roles[role] = value
			}
			savePolicy(t, s, input, 0)
			proposal, err := s.ProposeRoleModels("before-fault")
			if err != nil || !proposal.Confirmable {
				t.Fatalf("fixture proposal: confirmable=%v err=%v", proposal.Confirmable, err)
			}
			before, err := s.GetSettings()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(tc.fault); err != nil {
				t.Fatal(err)
			}
			_, proposalErr := s.ProposeRoleModels("after-fault")
			if proposalErr == nil {
				t.Error("storage failure persisted another recommendation")
			}
			_, _, applied, confirmErr := s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "after-fault-decision")
			if confirmErr == nil || errors.Is(confirmErr, ErrRolePolicyStale) || errors.Is(confirmErr, ErrRolePolicyIncomplete) || applied {
				t.Errorf("storage failure did not remain an internal error: applied=%v err=%v", applied, confirmErr)
			}
			after, err := s.GetSettings()
			if err != nil {
				t.Fatal(err)
			}
			var proposals, decisions int
			if err = s.db.QueryRow(`SELECT count(*) FROM role_model_proposals`).Scan(&proposals); err != nil {
				t.Fatal(err)
			}
			if err = s.db.QueryRow(`SELECT count(*) FROM role_model_decisions`).Scan(&decisions); err != nil {
				t.Fatal(err)
			}
			if proposals != 1 || decisions != 0 || !reflect.DeepEqual(before, after) {
				t.Errorf("storage failure mutated records: proposals=%d decisions=%d settingsChanged=%v", proposals, decisions, !reflect.DeepEqual(before, after))
			}
		})
	}
}

func TestRolePolicyExpectedAbsentMetadataRemainsAnExclusion(t *testing.T) {
	for _, tc := range []struct {
		name, mutation, reason string
		authorization          bool
	}{
		{"route", `DROP TRIGGER model_route_no_delete; DELETE FROM model_routes`, domain.RolePolicyReasonRouteInvalid, false},
		{"credential", `DROP TRIGGER model_credential_no_delete; DELETE FROM model_credential_versions`, domain.RolePolicyReasonCredentialUnavailable, false},
		{"authorization", `DROP TRIGGER model_authorization_no_delete; DELETE FROM model_authorizations`, domain.RolePolicyReasonAuthorizationUnavailable, true},
		{"invalid route structure", `DROP TRIGGER model_route_no_update; UPDATE model_routes SET protocol='unsupported'`, domain.RolePolicyReasonRouteInvalid, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			var profile domain.Profile
			if tc.authorization {
				profile = rolePolicyAuthorizationProfile(t, s)
			} else {
				profile, _ = rolePolicyAPIProfile(t, s, "absent", "off")
			}
			savePolicy(t, s, rolePolicyInput(profile.ID, domain.RolePolicySpendExplicitAPI, domain.RolePolicySpendSubscriptionOnly, domain.RolePolicySpendUnknown), 0)
			if _, err := s.db.Exec(tc.mutation); err != nil {
				t.Fatal(err)
			}
			proposal, err := s.ProposeRoleModels("expected-exclusion")
			if err != nil {
				t.Fatalf("expected unavailable metadata became fatal: %v", err)
			}
			candidate := proposal.Roles[domain.Main].Candidates[0]
			if proposal.Confirmable || candidate.Eligible || !containsString(candidate.Reasons, tc.reason) {
				t.Fatal("expected metadata absence did not exclude the candidate")
			}
		})
	}
}

func TestRolePolicyStrictMetadataReadsPreserveNonPolicyDenialBehavior(t *testing.T) {
	for _, tc := range []struct {
		name, fault   string
		authorization bool
	}{
		{"account", `ALTER TABLE model_accounts RENAME TO unreadable_accounts; CREATE VIEW model_accounts AS SELECT id,NULL AS revision,name,provider,base_url,auth_method,billing_mode,enabled,current_credential_version,created_at,updated_at FROM unreadable_accounts`, false},
		{"route", `DROP TABLE model_routes`, false},
		{"credential", `DROP TABLE model_credential_versions`, false},
		{"authorization decode", `DROP TRIGGER model_authorization_binding_immutable; UPDATE model_authorizations SET binding='{'`, true},
		{"authorization operation", `DROP TABLE model_authorization_operations`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			var profile domain.Profile
			if tc.authorization {
				profile = rolePolicyAuthorizationProfile(t, s)
			} else {
				profile, _ = rolePolicyAPIProfile(t, s, "strict", "off")
			}
			selection, err := s.ResolveModelProfile(profile.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(tc.fault); err != nil {
				t.Fatal(err)
			}
			tx, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, _, err = resolveModelCredentialMetadataReadTx(tx, selection, true); err == nil || rolePolicyExpectedUnavailability(err) {
				t.Fatal("strict metadata read erased an unexpected storage failure")
			}
			if _, _, err = resolveModelCredentialMetadataTx(tx, selection); !errors.Is(err, ErrModelAccountUnavailable) {
				t.Fatal("non-policy execution denial contract changed")
			}
		})
	}
}

func TestRolePolicyRouteLessAPIKeyAccountRequiresAvailableCredential(t *testing.T) {
	s := openTestStore(t)
	account, err := s.SaveModelAccount(domain.ModelAccount{
		ID: "route-less-account", Name: "Synthetic empty account", Provider: "custom",
		BaseURL: "https://example.com/v1", AuthMethod: "api_key", BillingMode: "unknown", Enabled: true,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if account.CurrentCredentialVersion != 0 {
		t.Fatalf("fixture unexpectedly has credential version %d", account.CurrentCredentialVersion)
	}
	profile := domain.Profile{ID: "route-less-profile", Name: "Synthetic", Kind: "custom", AccountID: account.ID, Protocol: "responses", BaseURL: account.BaseURL, Model: "synthetic"}
	if err = s.PutProfile(profile); err != nil {
		t.Fatal(err)
	}
	savePolicy(t, s, rolePolicyInput(profile.ID, domain.RolePolicySpendUnknown), 0)
	proposal, err := s.ProposeRoleModels("route-less-missing-credential")
	if err != nil {
		t.Fatal(err)
	}
	candidate := proposal.Roles[domain.Main].Candidates[0]
	if candidate.Eligible || !containsString(candidate.Reasons, domain.RolePolicyReasonCredentialUnavailable) || proposal.Confirmable {
		t.Fatalf("missing schema-1 credential became confirmable: proposal=%+v candidate=%+v", proposal, candidate)
	}
	if _, _, _, err = s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "route-less-decision"); !errors.Is(err, ErrRolePolicyIncomplete) {
		t.Fatalf("incomplete route-less proposal confirmation = %v", err)
	}
}

func TestRolePolicySubscriptionPreferenceNeverEscapesSpendAllowlist(t *testing.T) {
	s := openTestStore(t)
	baseURL := "http://127.0.0.1:1234/v1"
	account, err := s.SaveModelAccount(domain.ModelAccount{ID: "subscription-account", Name: "Synthetic", Provider: "openai", BaseURL: baseURL, AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	route, err := s.CreateModelRoute(domain.ModelRoute{ID: "subscription-route", AccountID: account.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: baseURL, Environment: "simulation", SpendClass: domain.RolePolicySpendSubscriptionOnly, ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginModelAuthorization(account.ID, "subscription-op", "sign_in", "subscription-auth"); err != nil {
		t.Fatal(err)
	}
	binding := domain.ModelAuthorizationBinding{ID: "subscription-auth", AccountID: account.ID, Issuer: "https://issuer.invalid", Subject: "synthetic-subject", ClientID: "synthetic-client", SpendClass: domain.RolePolicySpendSubscriptionOnly, Scopes: []string{"resource.invoke", "chatgpt.tokens.use.direct"}}
	if _, err = s.CommitModelAuthorization("subscription-op", binding, "2099-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	subscription := domain.Profile{ID: "subscription", Name: "Synthetic subscription", Kind: "openai", AccountID: account.ID, RouteID: route.ID, Protocol: route.Protocol, BaseURL: baseURL, Model: "synthetic"}
	if err = s.PutProfile(subscription); err != nil {
		t.Fatal(err)
	}
	explicit, _ := rolePolicyAPIProfile(t, s, "allowed-explicit", "off")
	input := rolePolicyInput(subscription.ID, domain.RolePolicySpendExplicitAPI)
	for _, role := range domain.RolePolicyRoles() {
		value := input.Roles[role]
		value.Preference = domain.RolePolicyPreferenceSubscriptionFirst
		value.Candidates = append(value.Candidates, domain.RolePolicyCandidateInput{ProfileID: explicit.ID, Capabilities: []string{"coding", "reasoning"}, Rationale: "User declaration; not independently verified."})
		input.Roles[role] = value
	}
	savePolicy(t, s, input, 0)
	proposal, err := s.ProposeRoleModels("subscription-allowlist")
	if err != nil {
		t.Fatal(err)
	}
	main := proposal.Roles[domain.Main]
	if !proposal.Confirmable || main.SelectedProfileID != explicit.ID || main.Candidates[0].Eligible || !containsString(main.Candidates[0].Reasons, domain.RolePolicyReasonSpendClassNotAllowed) {
		t.Fatalf("subscription preference escaped allowlist: %+v", main)
	}
}

func TestRolePolicyEvidenceRequiresMatchingTerminalModelInvocationAndMissionProfile(t *testing.T) {
	s := openTestStore(t)
	profile := policyProfile("evidence-profile", "m")
	if err := s.PutProfile(profile); err != nil {
		t.Fatal(err)
	}
	m, binding := controlledStoreMission(t, s, "evidence-mission")
	m, err := s.UpdateMission(m.ID, m.Revision, "fixture.profile", func(m *domain.Mission) error {
		m.Profiles = map[domain.Role]domain.Profile{domain.Researcher: profile}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	m, grant := controlledGrant(t, s, m, binding)
	intent := modelInvocation("evidence-invocation", m, grant, binding)
	m, invocation, err := s.BeginInvocation(intent, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(invocation.ID, invocation.Epoch); err != nil {
		t.Fatal(err)
	}
	output := controlledEnvelope("model_output", invocation.ID, m.ID, binding, `{}`)
	if _, err = s.FinishInvocation(invocation.ID, invocation.Epoch, "succeeded", &output, "", domain.CallUsage{}); err != nil {
		t.Fatal(err)
	}
	input := rolePolicyInput(profile.ID, domain.RolePolicySpendUnknown)
	role := input.Roles[domain.Researcher]
	role.Candidates[0].EvidenceInvocationID = invocation.ID
	input.Roles[domain.Researcher] = role
	savePolicy(t, s, input, 0)
	proposal, err := s.ProposeRoleModels("matching-evidence")
	if err != nil {
		t.Fatal(err)
	}
	result := proposal.Roles[domain.Researcher].Candidates[0]
	if !result.Eligible || result.Evidence == nil || result.Evidence.InvocationID != invocation.ID || result.Evidence.MissionID != m.ID || result.Evidence.Status != "completed" || result.Evidence.Environment != "unknown" {
		t.Fatalf("matching evidence = %+v", result)
	}
	role = input.Roles[domain.Worker]
	role.Candidates[0].EvidenceInvocationID = invocation.ID
	input.Roles[domain.Worker] = role
	savePolicy(t, s, input, 1)
	forged, err := s.ProposeRoleModels("forged-evidence")
	if err != nil {
		t.Fatal(err)
	}
	worker := forged.Roles[domain.Worker].Candidates[0]
	if worker.Eligible || worker.Evidence != nil || !containsString(worker.Reasons, domain.RolePolicyReasonEvidenceInvalid) {
		t.Fatalf("forged evidence accepted: %+v", worker)
	}
}

func TestRolePolicyRejectsNonterminalToolAndSemanticMismatchEvidence(t *testing.T) {
	for _, kind := range []string{"nonterminal", "tool", "semantic mismatch"} {
		t.Run(kind, func(t *testing.T) {
			s := openTestStore(t)
			profile := policyProfile("evidence-profile", "current-model")
			if err := s.PutProfile(profile); err != nil {
				t.Fatal(err)
			}
			m, binding := controlledStoreMission(t, s, "evidence-mission")
			m, err := s.UpdateMission(m.ID, m.Revision, "fixture.profile", func(m *domain.Mission) error {
				frozen := profile
				if kind == "semantic mismatch" {
					frozen.Model = "different-historical-model"
				}
				m.Profiles = map[domain.Role]domain.Profile{domain.Researcher: frozen}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			m, grant := controlledGrant(t, s, m, binding)
			intent := modelInvocation("negative-evidence", m, grant, binding)
			if kind == "tool" {
				intent = repositoryInvocation(intent.ID, m, grant, binding)
			}
			m, invocation, err := s.BeginInvocation(intent, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if kind != "nonterminal" {
				if _, err = s.ClaimInvocation(invocation.ID, invocation.Epoch); err != nil {
					t.Fatal(err)
				}
				outputKind := "model_output"
				if kind == "tool" {
					outputKind = "tool_observation"
				}
				output := controlledEnvelope(outputKind, invocation.ID, m.ID, binding, `{}`)
				if _, err = s.FinishInvocation(invocation.ID, invocation.Epoch, "succeeded", &output, "", domain.CallUsage{}); err != nil {
					t.Fatal(err)
				}
			}
			input := rolePolicyInput(profile.ID, domain.RolePolicySpendUnknown)
			role := input.Roles[domain.Researcher]
			role.Candidates[0].EvidenceInvocationID = invocation.ID
			input.Roles[domain.Researcher] = role
			savePolicy(t, s, input, 0)
			proposal, err := s.ProposeRoleModels("negative-evidence-proposal")
			if err != nil {
				t.Fatal(err)
			}
			candidate := proposal.Roles[domain.Researcher].Candidates[0]
			if candidate.Eligible || candidate.Evidence != nil || !reflect.DeepEqual(candidate.Reasons, []string{domain.RolePolicyReasonEvidenceInvalid}) || proposal.Confirmable || proposal.Roles[domain.Researcher].SelectedProfileID != "" {
				t.Fatal("invalid evidence acquired eligibility or a frozen receipt")
			}
		})
	}
}

func TestRolePolicyCombinationReadFailureIsNotAnExclusion(t *testing.T) {
	s := openTestStore(t)
	if err := s.PutProfile(policyProfile("combination", "m")); err != nil {
		t.Fatal(err)
	}
	selected := map[domain.Role]string{}
	for _, role := range domain.RolePolicyRoles() {
		selected[role] = "combination"
	}
	if _, err := s.db.Exec(`DROP TABLE profiles`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	valid, err := validateRolePolicySelectionTx(tx, selected)
	if valid || err == nil {
		t.Fatal("combination read failure became a normal selection exclusion")
	}
}

func TestRolePolicyMixedSnapshotSchemasRemainASelectionExclusion(t *testing.T) {
	s := openTestStore(t)
	routed, _ := rolePolicyAPIProfile(t, s, "mixed", "off")
	legacy := routed
	legacy.ID, legacy.RouteID = "route-less-mixed", ""
	if err := s.PutProfile(legacy); err != nil {
		t.Fatal(err)
	}
	input := rolePolicyInput(routed.ID, domain.RolePolicySpendExplicitAPI, domain.RolePolicySpendUnknown)
	main := input.Roles[domain.Main]
	main.Candidates[0].ProfileID = legacy.ID
	input.Roles[domain.Main] = main
	savePolicy(t, s, input, 0)
	proposal, err := s.ProposeRoleModels("mixed-schemas")
	if err != nil {
		t.Fatalf("incompatible selection became a storage error: %v", err)
	}
	if proposal.Confirmable {
		t.Fatal("mixed schemas became confirmable")
	}
	for _, role := range domain.RolePolicyRoles() {
		result := proposal.Roles[role]
		if result.SelectedProfileID != "" || result.Candidates[0].Eligible || !reflect.DeepEqual(result.Candidates[0].Reasons, []string{domain.RolePolicyReasonSelectionInvalid}) {
			t.Fatalf("role %s lost finite selection exclusion", role)
		}
	}
}

func TestRolePolicyConfirmationIsAtomicConcurrentAndReopensCommittedHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foundry.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutProfile(policyProfile("p", "m")); err != nil {
		t.Fatal(err)
	}
	state, _ := s.GetRolePolicyState()
	if _, err = s.SaveRoleModelPolicy(rolePolicyInput("p", domain.RolePolicySpendUnknown), 0, state.ProfilesDigest); err != nil {
		t.Fatal(err)
	}
	proposal, err := s.ProposeRoleModels("concurrent")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	type outcome struct {
		decision domain.RolePolicyDecision
		applied  bool
		err      error
	}
	results := make(chan outcome, 2)
	for _, id := range []string{"decision-a", "decision-b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			d, _, applied, e := s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, id)
			results <- outcome{d, applied, e}
		}(id)
	}
	wg.Wait()
	close(results)
	appliedCount := 0
	var decisionID string
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.applied {
			appliedCount++
			decisionID = result.decision.ID
		}
	}
	if appliedCount != 1 {
		t.Fatalf("applied confirmations = %d", appliedCount)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reopened, err := s.GetRolePolicyState()
	if err != nil || reopened.Policy.Revision != 1 || reopened.LatestProposal == nil || reopened.LatestProposal.ID != proposal.ID || reopened.ActiveDecision == nil || reopened.ActiveDecision.ID != decisionID {
		t.Fatalf("reopened state = %+v, %v", reopened, err)
	}
}

func TestRolePolicyConfirmationFailureRollsBackDecisionAndSettings(t *testing.T) {
	s := openTestStore(t)
	_ = s.PutProfile(policyProfile("p", "m"))
	_ = s.PutSettings(domain.Settings{DefaultProfileID: "p", RoleBindings: map[domain.Role]string{}})
	savePolicy(t, s, rolePolicyInput("p", domain.RolePolicySpendUnknown), 0)
	proposal, err := s.ProposeRoleModels("fault")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetSettings()
	if _, err = s.db.Exec(`CREATE TRIGGER fail_policy_settings BEFORE UPDATE ON settings BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "fault-decision"); err == nil {
		t.Fatal("injected SQL failure reported success")
	}
	after, _ := s.GetSettings()
	var decisions int
	if err = s.db.QueryRow(`SELECT count(*) FROM role_model_decisions`).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || decisions != 0 {
		t.Fatalf("partial confirmation persisted: before=%+v after=%+v decisions=%d", before, after, decisions)
	}
}

func TestRolePolicyStoredCorruptionFailsClosedAndReadsDoNotRecreateSchema(t *testing.T) {
	t.Run("corrupt", func(t *testing.T) {
		s := openTestStore(t)
		_ = s.PutProfile(policyProfile("p", "m"))
		savePolicy(t, s, rolePolicyInput("p", domain.RolePolicySpendUnknown), 0)
		if _, err := s.db.Exec(`DROP TRIGGER role_model_policy_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE role_model_policies SET data='{"schema_version":99,"policy":{}}' WHERE revision=1`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetRolePolicyState(); err == nil {
			t.Fatal("unsupported stored policy became empty policy")
		}
	})
	t.Run("read does not initialize", func(t *testing.T) {
		s := openTestStore(t)
		for _, table := range []string{"role_model_decisions", "role_model_proposals", "role_model_policies"} {
			if _, err := s.db.Exec(`DROP TABLE ` + table); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.GetRolePolicyState(); err == nil {
			t.Fatal("missing schema read succeeded")
		}
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name LIKE 'role_model_%'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("read recreated optional schema: count=%d err=%v", count, err)
		}
	})
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
