package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func validRolePolicyInput() RolePolicyInput {
	roles := map[Role]RolePolicyRoleInput{}
	for _, role := range RolePolicyRoles() {
		roles[role] = RolePolicyRoleInput{
			Candidates: []RolePolicyCandidateInput{{
				ProfileID:    "local",
				Capabilities: []string{"coding", "reasoning"},
				Rationale:    "User declaration; not independently verified.",
			}},
			RequiredCapabilities: []string{"coding"},
			AllowedSpendClasses:  []string{"unknown"},
			Preference:           "candidate_order",
		}
	}
	return RolePolicyInput{Roles: roles}
}

func TestValidateRolePolicyInputRejectsIncompleteAndUnknownRoles(t *testing.T) {
	input := validRolePolicyInput()
	delete(input.Roles, Auditor)
	if err := ValidateRolePolicyInput(input); err == nil {
		t.Fatal("incomplete role map accepted")
	}
	input = validRolePolicyInput()
	input.Roles[Role("release")] = RolePolicyRoleInput{Preference: "candidate_order"}
	if err := ValidateRolePolicyInput(input); err == nil {
		t.Fatal("unknown role accepted")
	}
	if got := RolePolicyRoles(); len(got) != 7 || got[0] != Main || got[6] != Auditor {
		t.Fatalf("safe role iteration = %#v", got)
	}
}

func TestRolePolicyRejectsInventedCapability(t *testing.T) {
	input := validRolePolicyInput()
	role := input.Roles[Main]
	role.Candidates[0].Capabilities = []string{"unlimited_tool_authority"}
	input.Roles[Main] = role
	if ValidateRolePolicyInput(input) == nil {
		t.Fatal("invented capability accepted")
	}
	role = input.Roles[Main]
	role.Candidates[0].Capabilities = []string{"coding"}
	role.RequiredCapabilities = []string{"network_access"}
	input.Roles[Main] = role
	if ValidateRolePolicyInput(input) == nil {
		t.Fatal("invented required capability accepted")
	}
}

func TestValidateRolePolicyInputRejectsMalformedCandidatesAndEnums(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RolePolicyInput)
	}{
		{"missing profile", func(i *RolePolicyInput) { r := i.Roles[Main]; r.Candidates[0].ProfileID = ""; i.Roles[Main] = r }},
		{"overlong profile", func(i *RolePolicyInput) {
			r := i.Roles[Main]
			r.Candidates[0].ProfileID = strings.Repeat("p", 257)
			i.Roles[Main] = r
		}},
		{"empty rationale", func(i *RolePolicyInput) { r := i.Roles[Main]; r.Candidates[0].Rationale = " \n "; i.Roles[Main] = r }},
		{"overlong rationale", func(i *RolePolicyInput) {
			r := i.Roles[Main]
			r.Candidates[0].Rationale = strings.Repeat("测", 501)
			i.Roles[Main] = r
		}},
		{"duplicate candidate", func(i *RolePolicyInput) {
			r := i.Roles[Main]
			r.Candidates = append(r.Candidates, r.Candidates[0])
			i.Roles[Main] = r
		}},
		{"too many candidates", func(i *RolePolicyInput) {
			r := i.Roles[Main]
			r.Candidates = make([]RolePolicyCandidateInput, 9)
			for n := range r.Candidates {
				r.Candidates[n] = RolePolicyCandidateInput{ProfileID: string(rune('a' + n)), Capabilities: []string{"coding"}, Rationale: "declared"}
			}
			i.Roles[Main] = r
		}},
		{"duplicate capability", func(i *RolePolicyInput) {
			r := i.Roles[Main]
			r.Candidates[0].Capabilities = []string{"coding", "coding"}
			i.Roles[Main] = r
		}},
		{"unknown spend", func(i *RolePolicyInput) {
			r := i.Roles[Main]
			r.AllowedSpendClasses = []string{"free"}
			i.Roles[Main] = r
		}},
		{"duplicate spend", func(i *RolePolicyInput) {
			r := i.Roles[Main]
			r.AllowedSpendClasses = []string{"unknown", "unknown"}
			i.Roles[Main] = r
		}},
		{"unknown preference", func(i *RolePolicyInput) { r := i.Roles[Main]; r.Preference = "cheapest"; i.Roles[Main] = r }},
		{"overlong evidence id", func(i *RolePolicyInput) {
			r := i.Roles[Main]
			r.Candidates[0].EvidenceInvocationID = strings.Repeat("e", 257)
			i.Roles[Main] = r
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validRolePolicyInput()
			tt.mutate(&input)
			if err := ValidateRolePolicyInput(input); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	if err := ValidateRolePolicyInput(validRolePolicyInput()); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
}

func TestRolePolicyProfileDigestTracksSemanticsButNotPresentationOrCredentialRotation(t *testing.T) {
	base := Profile{ID: "p", Name: "Original", Kind: "openai", Protocol: "responses", BaseURL: "https://example.com/v1", Model: "m", HasKey: true, AccountID: "a", RouteID: "r", AccountRevision: 2, CredentialVersion: 3}
	want, err := RolePolicyProfileDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	cosmetic := base
	cosmetic.Name = "Renamed"
	cosmetic.HasKey = false
	cosmetic.AccountRevision = 99
	cosmetic.CredentialVersion = 100
	got, err := RolePolicyProfileDigest(cosmetic)
	if err != nil || got != want {
		t.Fatalf("cosmetic/credential fields changed digest: %q %v, want %q", got, err, want)
	}
	for name, mutate := range map[string]func(*Profile){
		"id":       func(p *Profile) { p.ID = "other" },
		"kind":     func(p *Profile) { p.Kind = "other" },
		"protocol": func(p *Profile) { p.Protocol = "chat_completions" },
		"base url": func(p *Profile) { p.BaseURL = "https://other.invalid/v1" },
		"model":    func(p *Profile) { p.Model = "other" },
		"account":  func(p *Profile) { p.AccountID = "other" },
		"route":    func(p *Profile) { p.RouteID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			digest, err := RolePolicyProfileDigest(changed)
			if err != nil || digest == want {
				t.Fatalf("semantic change digest = %q, %v", digest, err)
			}
		})
	}
}

func TestRolePolicyDTOsUseSnakeCaseAndOptionalEvidence(t *testing.T) {
	result := RolePolicyCandidateResult{ProfileID: "p", ProfileDigest: strings.Repeat("a", 64), SpendClass: "unknown", Eligible: true, Reasons: []string{}, Capabilities: []string{"coding"}, Rationale: "declared", ExecutionNotice: "credentials_and_quota_unknown"}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"profile_id":"p","profile_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","spend_class":"unknown","eligible":true,"reasons":[],"capabilities":["coding"],"rationale":"declared","execution_notice":"credentials_and_quota_unknown"}`
	if string(raw) != want {
		t.Fatalf("JSON = %s\nwant = %s", raw, want)
	}
}
