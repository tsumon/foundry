package store

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func confirmDistinctRolePolicy(t *testing.T, s *Store, suffix string, expected int64) (domain.RolePolicyDecision, map[domain.Role]string) {
	t.Helper()
	input := domain.RolePolicyInput{Roles: map[domain.Role]domain.RolePolicyRoleInput{}}
	want := map[domain.Role]string{}
	for index, role := range domain.RolePolicyRoles() {
		id := fmt.Sprintf("%s-%d-%s", suffix, index, role)
		if err := s.PutProfile(policyProfile(id, "model-"+id)); err != nil {
			t.Fatal(err)
		}
		want[role] = id
		input.Roles[role] = domain.RolePolicyRoleInput{
			Candidates:           []domain.RolePolicyCandidateInput{{ProfileID: id, Capabilities: []string{"coding", "reasoning"}, Rationale: "User declaration; not independently verified."}},
			RequiredCapabilities: []string{"coding"}, AllowedSpendClasses: []string{domain.RolePolicySpendUnknown}, Preference: domain.RolePolicyPreferenceCandidateOrder,
		}
	}
	savePolicy(t, s, input, expected)
	proposal, err := s.ProposeRoleModels("proposal-" + suffix)
	if err != nil || !proposal.Confirmable {
		t.Fatalf("proposal = %+v, %v", proposal, err)
	}
	decision, _, applied, err := s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "decision-"+suffix)
	if err != nil || !applied {
		t.Fatalf("confirmation = %+v, applied=%v, err=%v", decision, applied, err)
	}
	return decision, want
}

func TestMissionRolePolicyResolutionFreezesOnlySevenSelectedCandidates(t *testing.T) {
	s := openTestStore(t)
	if err := s.PutProfile(policyProfile("manual", "manual-model")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSettings(domain.Settings{DefaultProfileID: "manual", RoleBindings: map[domain.Role]string{}}); err != nil {
		t.Fatal(err)
	}
	manualDefault, manualProfiles, manualModel, manualPolicy, err := s.ResolveMissionModelSelection("")
	if err != nil || manualPolicy != nil || manualModel != nil || manualDefault != "manual" {
		t.Fatalf("manual resolution = %q %+v %+v %+v %v", manualDefault, manualProfiles, manualModel, manualPolicy, err)
	}
	manual, err := s.CreateMission(domain.Mission{ID: "manual-mission", State: "draft", Spec: domain.Spec{ProfileID: manualDefault}, Profiles: manualProfiles, Budget: domain.Budget{MaxCalls: 16}, Approvals: []domain.Approval{{Kind: "fixture", Digest: "manual"}}})
	if err != nil {
		t.Fatal(err)
	}

	decision, want := confirmDistinctRolePolicy(t, s, "one", 0)
	resolvedDefault, profiles, modelSnapshot, policySnapshot, err := s.ResolveMissionModelSelection("manual")
	if err != nil || resolvedDefault != "manual" || modelSnapshot != nil || policySnapshot == nil {
		t.Fatalf("policy resolution = %q model=%+v policy=%+v err=%v", resolvedDefault, modelSnapshot, policySnapshot, err)
	}
	if policySnapshot.DecisionID != decision.ID || policySnapshot.PolicyRevision != decision.Proposal.PolicyRevision || policySnapshot.RecommendationDigest != decision.Proposal.Digest || policySnapshot.ConfirmedAt != decision.ConfirmedAt || len(policySnapshot.Roles) != 7 {
		t.Fatalf("snapshot provenance = %+v", policySnapshot)
	}
	for _, role := range domain.RolePolicyRoles() {
		result := policySnapshot.Roles[role]
		if profiles[role].ID != want[role] || result.SelectedProfileID != want[role] || len(result.Candidates) != 1 || result.Candidates[0].ProfileID != want[role] {
			t.Fatalf("role %s escaped confirmed selection: profile=%+v result=%+v", role, profiles[role], result)
		}
	}
	created, err := s.CreateMission(domain.Mission{ID: "policy-mission", State: "draft", Spec: domain.Spec{ProfileID: resolvedDefault}, Profiles: profiles, ModelSnapshot: modelSnapshot, ModelPolicySnapshot: policySnapshot, Budget: domain.Budget{MaxCalls: 16}, Approvals: []domain.Approval{{Kind: "fixture", Digest: "policy"}}})
	if err != nil {
		t.Fatal(err)
	}
	var manualRaw, policyRaw []byte
	if err = s.db.QueryRow(`SELECT data FROM missions WHERE id=?`, manual.ID).Scan(&manualRaw); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT data FROM missions WHERE id=?`, created.ID).Scan(&policyRaw); err != nil {
		t.Fatal(err)
	}
	confirmDistinctRolePolicy(t, s, "two", 1)
	var afterManualRaw, afterPolicyRaw []byte
	_ = s.db.QueryRow(`SELECT data FROM missions WHERE id=?`, manual.ID).Scan(&afterManualRaw)
	_ = s.db.QueryRow(`SELECT data FROM missions WHERE id=?`, created.ID).Scan(&afterPolicyRaw)
	if !bytes.Equal(manualRaw, afterManualRaw) || !bytes.Equal(policyRaw, afterPolicyRaw) {
		t.Fatal("later confirmation rewrote historical mission bytes")
	}
	historical, err := s.GetMission(created.ID)
	if err != nil || historical.ModelPolicySnapshot.DecisionID != decision.ID || !reflect.DeepEqual(historical.Approvals, created.Approvals) || historical.Budget != created.Budget {
		t.Fatalf("historical mission changed = %+v, %v", historical, err)
	}
}

func TestMissionRolePolicyCreateRechecksActiveSettingsAndProfileSemantics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		drift func(*testing.T, *Store, map[domain.Role]domain.Profile)
	}{
		{"settings", func(t *testing.T, s *Store, _ map[domain.Role]domain.Profile) {
			if err := s.PutSettings(domain.Settings{DefaultProfileID: "manual", RoleBindings: map[domain.Role]string{}}); err != nil {
				t.Fatal(err)
			}
		}},
		{"profile semantics", func(t *testing.T, s *Store, profiles map[domain.Role]domain.Profile) {
			profile := profiles[domain.Worker]
			profile.Model = "changed-after-resolution"
			if err := s.PutProfile(profile); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			_ = s.PutProfile(policyProfile("manual", "manual"))
			_ = s.PutSettings(domain.Settings{DefaultProfileID: "manual", RoleBindings: map[domain.Role]string{}})
			confirmDistinctRolePolicy(t, s, strings.ReplaceAll(tc.name, " ", "-"), 0)
			def, profiles, modelSnapshot, policySnapshot, err := s.ResolveMissionModelSelection("manual")
			if err != nil {
				t.Fatal(err)
			}
			tc.drift(t, s, profiles)
			_, err = s.CreateMission(domain.Mission{ID: "stale-" + tc.name, State: "draft", Spec: domain.Spec{ProfileID: def}, Profiles: profiles, ModelSnapshot: modelSnapshot, ModelPolicySnapshot: policySnapshot})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("stale create error = %v, want ErrConflict", err)
			}
		})
	}
}

func TestMissionRolePolicyIsImmutableButHistoricalDecisionNeedNotStayActive(t *testing.T) {
	s := openTestStore(t)
	_ = s.PutProfile(policyProfile("manual", "manual"))
	_ = s.PutSettings(domain.Settings{DefaultProfileID: "manual", RoleBindings: map[domain.Role]string{}})
	confirmDistinctRolePolicy(t, s, "history", 0)
	def, profiles, modelSnapshot, policySnapshot, err := s.ResolveMissionModelSelection("untrusted-override")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.CreateMission(domain.Mission{ID: "historical-policy", State: "draft", Spec: domain.Spec{ProfileID: def}, Profiles: profiles, ModelSnapshot: modelSnapshot, ModelPolicySnapshot: policySnapshot})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutSettings(domain.Settings{DefaultProfileID: "manual", RoleBindings: map[domain.Role]string{}}); err != nil {
		t.Fatal(err)
	}
	_, _, _, afterManual, err := s.ResolveMissionModelSelection("")
	if err != nil || afterManual != nil {
		t.Fatalf("manual save retained active provenance: %+v %v", afterManual, err)
	}
	m, err = s.UpdateMission(m.ID, m.Revision, "historical.local", func(m *domain.Mission) error { m.Title = "still valid"; return nil })
	if err != nil {
		t.Fatalf("inactive historical decision blocked local update: %v", err)
	}
	if _, err = s.UpdateMission(m.ID, m.Revision, "tamper", func(m *domain.Mission) error { m.ModelPolicySnapshot = nil; return nil }); err == nil {
		t.Fatal("policy provenance removal was allowed")
	}
}

func TestMissionRolePolicyMalformedPersistedTypeUsesInvalidSnapshotDiagnostic(t *testing.T) {
	s := openTestStore(t)
	raw := []byte(`{"id":"malformed-policy","state":"draft","revision":1,"model_policy_snapshot":"not-an-object"}`)
	if _, err := s.db.Exec(`INSERT INTO missions(id,revision,state,updated_at,data) VALUES(?,?,?,?,?)`, "malformed-policy", 1, "draft", "2026-10-19T00:00:00Z", raw); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetMission("malformed-policy")
	if err != nil || !m.ModelPolicySnapshotInvalid || m.ModelPolicySnapshot != nil {
		t.Fatalf("diagnostic mission = %+v, %v", m, err)
	}
	if _, err = s.UpdateMission(m.ID, m.Revision, "repair", func(m *domain.Mission) error { m.Title = "rewrite"; return nil }); !errors.Is(err, domain.ErrInvalidModelSnapshot) {
		t.Fatalf("malformed snapshot update = %v", err)
	}
}

func TestMissionRolePolicyKeepsProvenanceAcrossCredentialRotation(t *testing.T) {
	t.Run("api key version", func(t *testing.T) {
		s := openTestStore(t)
		profile, account := rolePolicyAPIProfile(t, s, "policy-rotation", "off")
		savePolicy(t, s, rolePolicyInput(profile.ID, domain.RolePolicySpendExplicitAPI), 0)
		proposal, err := s.ProposeRoleModels("key-rotation-proposal")
		if err != nil || !proposal.Confirmable {
			t.Fatalf("proposal = %+v, %v", proposal, err)
		}
		if _, _, applied, err := s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "key-rotation-decision"); err != nil || !applied {
			t.Fatalf("confirmation applied=%v err=%v", applied, err)
		}
		def, profiles, beforeModel, beforePolicy, err := s.ResolveMissionModelSelection("")
		if err != nil || beforeModel == nil || beforeModel.Roles[domain.Worker].Profile.CredentialVersion != 1 {
			t.Fatalf("before rotation = %+v, %v", beforeModel, err)
		}
		first, err := s.CreateMission(domain.Mission{ID: "before-key-rotation", State: "draft", WorkspaceMode: "account_snapshot_v2", Spec: domain.Spec{ProfileID: def}, Profiles: profiles, ModelSnapshot: beforeModel, ModelPolicySnapshot: beforePolicy})
		if err != nil {
			t.Fatal(err)
		}
		account = snapshotVersion(t, s, account, "policy-rotated-key")
		_, profiles, afterModel, afterPolicy, err := s.ResolveMissionModelSelection("")
		if err != nil || afterModel.Roles[domain.Worker].Profile.CredentialVersion != 2 || afterPolicy.Digest != beforePolicy.Digest {
			t.Fatalf("after rotation model=%+v policy=%+v err=%v", afterModel, afterPolicy, err)
		}
		if _, err = s.CreateMission(domain.Mission{ID: "after-key-rotation", State: "draft", WorkspaceMode: "account_snapshot_v2", Spec: domain.Spec{ProfileID: def}, Profiles: profiles, ModelSnapshot: afterModel, ModelPolicySnapshot: afterPolicy}); err != nil {
			t.Fatal(err)
		}
		historical, _ := s.GetMission(first.ID)
		if historical.ModelSnapshot.Roles[domain.Worker].Profile.CredentialVersion != 1 {
			t.Fatal("historical key version followed rotation")
		}
	})

	t.Run("token generation", func(t *testing.T) {
		s := openTestStore(t)
		binding := authorizationFixture(t, s)
		route, err := s.CreateModelRoute(domain.ModelRoute{ID: "policy-token-route", AccountID: binding.AccountID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: "http://127.0.0.1:1234/v1", Environment: "simulation", SpendClass: domain.RolePolicySpendSubscriptionOnly, ExtraUsage: "off"})
		if err != nil {
			t.Fatal(err)
		}
		profile := domain.Profile{ID: "policy-token-profile", AccountID: binding.AccountID, RouteID: route.ID, Protocol: route.Protocol, BaseURL: route.BaseURL, Model: "synthetic"}
		if err = s.PutProfile(profile); err != nil {
			t.Fatal(err)
		}
		if _, err = s.BeginModelAuthorization(binding.AccountID, "policy-token-login", "sign_in", binding.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CommitModelAuthorization("policy-token-login", binding, "2099-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		savePolicy(t, s, rolePolicyInput(profile.ID, domain.RolePolicySpendSubscriptionOnly), 0)
		proposal, err := s.ProposeRoleModels("token-rotation-proposal")
		if err != nil || !proposal.Confirmable {
			t.Fatalf("proposal = %+v, %v", proposal, err)
		}
		if _, _, applied, err := s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "token-rotation-decision"); err != nil || !applied {
			t.Fatalf("confirmation applied=%v err=%v", applied, err)
		}
		_, _, beforeModel, beforePolicy, err := s.ResolveMissionModelSelection("")
		if err != nil || beforeModel == nil || beforeModel.Roles[domain.Worker].Authorization == nil {
			t.Fatalf("before refresh = %+v, %v", beforeModel, err)
		}
		if _, err = s.BeginModelAuthorization(binding.AccountID, "policy-token-refresh", "refresh", binding.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CommitModelAuthorization("policy-token-refresh", binding, "2099-02-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		_, _, afterModel, afterPolicy, err := s.ResolveMissionModelSelection("")
		if err != nil || afterModel.Digest != beforeModel.Digest || afterPolicy.Digest != beforePolicy.Digest {
			t.Fatalf("refresh changed frozen identity: before=%+v after=%+v policy=%+v err=%v", beforeModel, afterModel, afterPolicy, err)
		}
	})
}
