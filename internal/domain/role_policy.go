package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidRolePolicy = errors.New("invalid_role_policy")

const (
	RolePolicyPreferenceCandidateOrder    = "candidate_order"
	RolePolicyPreferenceSubscriptionFirst = "subscription_first"

	RolePolicySpendSubscriptionOnly   = "subscription_only"
	RolePolicySpendInteractivePackage = "interactive_package"
	RolePolicySpendExplicitAPI        = "explicit_api"
	RolePolicySpendUnknown            = "unknown"

	RolePolicyReasonProfileMissing           = "profile_missing"
	RolePolicyReasonProfileChanged           = "profile_changed"
	RolePolicyReasonCapabilityMissing        = "required_capability_missing"
	RolePolicyReasonSpendClassNotAllowed     = "spend_class_not_allowed"
	RolePolicyReasonAccountDisabled          = "account_disabled"
	RolePolicyReasonRouteInvalid             = "route_invalid"
	RolePolicyReasonCredentialUnavailable    = "credential_unavailable"
	RolePolicyReasonAuthorizationUnavailable = "authorization_unavailable"
	RolePolicyReasonExtraUsageNotOff         = "extra_usage_not_off"
	RolePolicyReasonEvidenceInvalid          = "evidence_invalid"
	RolePolicyReasonSelectionInvalid         = "selection_invalid"

	RolePolicyNoticeLiveClosed    = "production_live_gate_closed"
	RolePolicyNoticeLegacyUnknown = "credentials_and_quota_unknown"
)

var rolePolicyRoles = [...]Role{Main, Researcher, Planner, Worker, Debugger, QA, Auditor}

// RolePolicyRoles returns the only roles accepted by role-policy persistence.
// A copy prevents callers from changing the process-wide iteration order.
func RolePolicyRoles() []Role {
	return append([]Role(nil), rolePolicyRoles[:]...)
}

type RolePolicyCandidateInput struct {
	ProfileID            string   `json:"profile_id"`
	Capabilities         []string `json:"capabilities"`
	Rationale            string   `json:"rationale"`
	EvidenceInvocationID string   `json:"evidence_invocation_id,omitempty"`
}

type RolePolicyRoleInput struct {
	Candidates           []RolePolicyCandidateInput `json:"candidates"`
	RequiredCapabilities []string                   `json:"required_capabilities"`
	AllowedSpendClasses  []string                   `json:"allowed_spend_classes"`
	Preference           string                     `json:"preference"`
}

type RolePolicyInput struct {
	Roles map[Role]RolePolicyRoleInput `json:"roles"`
}

type RolePolicyCandidate struct {
	RolePolicyCandidateInput
	ProfileDigest string `json:"profile_digest"`
}

type RolePolicyRole struct {
	Candidates           []RolePolicyCandidate `json:"candidates"`
	RequiredCapabilities []string              `json:"required_capabilities"`
	AllowedSpendClasses  []string              `json:"allowed_spend_classes"`
	Preference           string                `json:"preference"`
}

type RoleModelPolicy struct {
	Revision  int64                   `json:"revision"`
	CreatedAt string                  `json:"created_at"`
	Roles     map[Role]RolePolicyRole `json:"roles"`
}

type RolePolicyEvidence struct {
	InvocationID string `json:"invocation_id"`
	MissionID    string `json:"mission_id"`
	Role         Role   `json:"role"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code,omitempty"`
	Environment  string `json:"environment"`
	FinishedAt   string `json:"finished_at"`
}

type RolePolicyCandidateResult struct {
	ProfileID       string              `json:"profile_id"`
	ProfileDigest   string              `json:"profile_digest"`
	SpendClass      string              `json:"spend_class"`
	Eligible        bool                `json:"eligible"`
	Reasons         []string            `json:"reasons"`
	Capabilities    []string            `json:"capabilities"`
	Rationale       string              `json:"rationale"`
	Evidence        *RolePolicyEvidence `json:"evidence,omitempty"`
	ExecutionNotice string              `json:"execution_notice"`
}

type RolePolicyRoleResult struct {
	SelectedProfileID    string                      `json:"selected_profile_id"`
	RequiredCapabilities []string                    `json:"required_capabilities"`
	Candidates           []RolePolicyCandidateResult `json:"candidates"`
}

type RolePolicyProposal struct {
	ID             string                        `json:"id"`
	PolicyRevision int64                         `json:"policy_revision"`
	CreatedAt      string                        `json:"created_at"`
	SettingsDigest string                        `json:"settings_digest"`
	InputsDigest   string                        `json:"inputs_digest"`
	Digest         string                        `json:"digest"`
	Confirmable    bool                          `json:"confirmable"`
	Roles          map[Role]RolePolicyRoleResult `json:"roles"`
}

type RolePolicyDecision struct {
	ID          string             `json:"id"`
	ProposalID  string             `json:"proposal_id"`
	ConfirmedAt string             `json:"confirmed_at"`
	Proposal    RolePolicyProposal `json:"proposal"`
}

type RolePolicyState struct {
	Policy         RoleModelPolicy     `json:"policy"`
	ProfilesDigest string              `json:"profiles_digest"`
	LatestProposal *RolePolicyProposal `json:"latest_proposal,omitempty"`
	ActiveDecision *RolePolicyDecision `json:"active_decision,omitempty"`
}

// MissionRolePolicySnapshot freezes why a confirmed role-policy decision
// selected the seven concrete profiles used by a mission. It is provenance,
// not execution authority; Profiles and ModelSnapshot remain authoritative.
type MissionRolePolicySnapshot struct {
	SchemaVersion        int                           `json:"schema_version"`
	DecisionID           string                        `json:"decision_id"`
	PolicyRevision       int64                         `json:"policy_revision"`
	RecommendationDigest string                        `json:"recommendation_digest"`
	ConfirmedAt          string                        `json:"confirmed_at"`
	Digest               string                        `json:"digest"`
	Roles                map[Role]RolePolicyRoleResult `json:"roles"`
}

// MissionRolePolicySnapshotDigest excludes the digest field itself and uses a
// domain separator distinct from recommendations and execution authorities.
func MissionRolePolicySnapshotDigest(snapshot MissionRolePolicySnapshot) (string, error) {
	raw, err := json.Marshal(struct {
		SchemaVersion        int                           `json:"schema_version"`
		DecisionID           string                        `json:"decision_id"`
		PolicyRevision       int64                         `json:"policy_revision"`
		RecommendationDigest string                        `json:"recommendation_digest"`
		ConfirmedAt          string                        `json:"confirmed_at"`
		Roles                map[Role]RolePolicyRoleResult `json:"roles"`
	}{snapshot.SchemaVersion, snapshot.DecisionID, snapshot.PolicyRevision, snapshot.RecommendationDigest, snapshot.ConfirmedAt, snapshot.Roles})
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("FOUNDRY mission role model policy v1"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(raw)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ValidateMissionRolePolicySnapshot accepts nil for legacy/manual missions and
// otherwise requires the exact selected candidate for every frozen role.
func ValidateMissionRolePolicySnapshot(m Mission) error {
	if m.ModelPolicySnapshotInvalid {
		return ErrInvalidModelSnapshot
	}
	snapshot := m.ModelPolicySnapshot
	if snapshot == nil {
		return nil
	}
	if snapshot.SchemaVersion != 1 || !safeRolePolicyIdentifier(snapshot.DecisionID) || snapshot.PolicyRevision < 1 || !rolePolicyLowerHex(snapshot.RecommendationDigest, 64) || !rolePolicyLowerHex(snapshot.Digest, 64) || len(snapshot.Roles) != len(rolePolicyRoles) || len(m.Profiles) != len(rolePolicyRoles) {
		return ErrInvalidModelSnapshot
	}
	if _, err := time.Parse(time.RFC3339Nano, snapshot.ConfirmedAt); err != nil {
		return ErrInvalidModelSnapshot
	}
	input := RolePolicyInput{Roles: make(map[Role]RolePolicyRoleInput, len(rolePolicyRoles))}
	for _, role := range rolePolicyRoles {
		result, ok := snapshot.Roles[role]
		profile, profileOK := m.Profiles[role]
		if !ok || !profileOK || result.SelectedProfileID != profile.ID || len(result.Candidates) != 1 {
			return ErrInvalidModelSnapshot
		}
		candidate := result.Candidates[0]
		profileDigest, err := RolePolicyProfileDigest(profile)
		if err != nil || candidate.ProfileID != result.SelectedProfileID || candidate.ProfileDigest != profileDigest || !candidate.Eligible || candidate.Reasons == nil || len(candidate.Reasons) != 0 || candidate.Capabilities == nil || result.RequiredCapabilities == nil || !validRolePolicySpendClass(candidate.SpendClass) || (candidate.ExecutionNotice != RolePolicyNoticeLiveClosed && candidate.ExecutionNotice != RolePolicyNoticeLegacyUnknown) {
			return ErrInvalidModelSnapshot
		}
		if profile.AccountID == "" && (candidate.SpendClass != RolePolicySpendUnknown || candidate.ExecutionNotice != RolePolicyNoticeLegacyUnknown) || profile.AccountID != "" && candidate.ExecutionNotice != RolePolicyNoticeLiveClosed {
			return ErrInvalidModelSnapshot
		}
		for _, required := range result.RequiredCapabilities {
			if !rolePolicyContainsValue(candidate.Capabilities, required) {
				return ErrInvalidModelSnapshot
			}
		}
		candidateInput := RolePolicyCandidateInput{ProfileID: candidate.ProfileID, Capabilities: candidate.Capabilities, Rationale: candidate.Rationale}
		if candidate.Evidence != nil {
			if !validRolePolicySnapshotEvidence(*candidate.Evidence, role) {
				return ErrInvalidModelSnapshot
			}
			candidateInput.EvidenceInvocationID = candidate.Evidence.InvocationID
		}
		input.Roles[role] = RolePolicyRoleInput{
			Candidates: []RolePolicyCandidateInput{candidateInput}, RequiredCapabilities: result.RequiredCapabilities,
			AllowedSpendClasses: []string{candidate.SpendClass}, Preference: RolePolicyPreferenceCandidateOrder,
		}
	}
	for role := range snapshot.Roles {
		if !isRolePolicyRole(role) {
			return ErrInvalidModelSnapshot
		}
	}
	if ValidateRolePolicyInput(input) != nil {
		return ErrInvalidModelSnapshot
	}
	digest, err := MissionRolePolicySnapshotDigest(*snapshot)
	if err != nil || digest != snapshot.Digest {
		return ErrInvalidModelSnapshot
	}
	return nil
}

func rolePolicyLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func validRolePolicySnapshotEvidence(evidence RolePolicyEvidence, role Role) bool {
	if !safeRolePolicyIdentifier(evidence.InvocationID) || !safeRolePolicyIdentifier(evidence.MissionID) || evidence.Role != role {
		return false
	}
	switch evidence.Status {
	case "completed":
		if evidence.ErrorCode != "" {
			return false
		}
	case "failed", "outcome_unknown", "interrupted_before_dispatch":
		if !validRolePolicyEvidenceErrorCode(evidence.ErrorCode) {
			return false
		}
	default:
		return false
	}
	if evidence.Environment != "live" && evidence.Environment != "simulation" && evidence.Environment != "unknown" {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, evidence.FinishedAt)
	return err == nil
}

func rolePolicyContainsValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func validRolePolicyEvidenceErrorCode(code string) bool {
	if code == "" {
		return true
	}
	switch code {
	case "auth_invalid", "entitlement_denied", "quota_exhausted", "rate_limited", "request_rejected", "server_error", "transport_unknown", "model_transport_unknown", "response_incomplete", "live_provider_access_pending_confirmation", "dispatch_interrupted", "stale_dispatch", "result_seal_failed", "interrupted_before_dispatch", "other_failure":
		return true
	default:
		return false
	}
}

func RolePolicyProfileDigest(p Profile) (string, error) {
	values := []string{p.ID, p.Kind, p.Protocol, p.BaseURL, p.Model, p.AccountID, p.RouteID}
	for _, value := range values {
		if !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return "", ErrInvalidRolePolicy
		}
	}
	if strings.TrimSpace(p.ID) == "" || len(p.ID) > 256 || strings.TrimSpace(p.Model) == "" || len(p.Model) > 512 || (p.Protocol != "chat_completions" && p.Protocol != "responses" && p.Protocol != "messages") || (p.RouteID != "" && p.AccountID == "") {
		return "", ErrInvalidRolePolicy
	}
	if _, _, err := ParseProviderBaseURL(p.BaseURL); err != nil {
		return "", ErrInvalidRolePolicy
	}
	raw, err := json.Marshal(struct {
		ID        string `json:"id"`
		Kind      string `json:"kind"`
		Protocol  string `json:"protocol"`
		BaseURL   string `json:"base_url"`
		Model     string `json:"model"`
		AccountID string `json:"account_id"`
		RouteID   string `json:"route_id"`
	}{p.ID, p.Kind, p.Protocol, p.BaseURL, p.Model, p.AccountID, p.RouteID})
	if err != nil {
		return "", fmt.Errorf("%w: encode profile", ErrInvalidRolePolicy)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func ValidateRolePolicyInput(input RolePolicyInput) error {
	if len(input.Roles) != len(rolePolicyRoles) {
		return ErrInvalidRolePolicy
	}
	for _, role := range rolePolicyRoles {
		value, ok := input.Roles[role]
		if !ok || len(value.Candidates) > 8 || !validRolePolicyPreference(value.Preference) {
			return ErrInvalidRolePolicy
		}
		if err := validateUniqueRolePolicyValues(value.RequiredCapabilities, validRolePolicyCapability); err != nil {
			return err
		}
		if err := validateUniqueRolePolicyValues(value.AllowedSpendClasses, validRolePolicySpendClass); err != nil {
			return err
		}
		seenProfiles := map[string]struct{}{}
		for _, candidate := range value.Candidates {
			if !safeRolePolicyIdentifier(candidate.ProfileID) || strings.TrimSpace(candidate.Rationale) == "" || !utf8.ValidString(candidate.Rationale) || utf8.RuneCountInString(candidate.Rationale) > 500 || strings.IndexFunc(candidate.Rationale, unicode.IsControl) >= 0 {
				return ErrInvalidRolePolicy
			}
			if candidate.EvidenceInvocationID != "" && !safeRolePolicyIdentifier(candidate.EvidenceInvocationID) {
				return ErrInvalidRolePolicy
			}
			if _, exists := seenProfiles[candidate.ProfileID]; exists {
				return ErrInvalidRolePolicy
			}
			seenProfiles[candidate.ProfileID] = struct{}{}
			if err := validateUniqueRolePolicyValues(candidate.Capabilities, validRolePolicyCapability); err != nil {
				return err
			}
		}
	}
	for role := range input.Roles {
		if !isRolePolicyRole(role) {
			return ErrInvalidRolePolicy
		}
	}
	return nil
}

func isRolePolicyRole(role Role) bool {
	for _, allowed := range rolePolicyRoles {
		if role == allowed {
			return true
		}
	}
	return false
}

func safeRolePolicyIdentifier(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value) && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsSpace) < 0 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validateUniqueRolePolicyValues(values []string, valid func(string) bool) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !valid(value) {
			return ErrInvalidRolePolicy
		}
		if _, exists := seen[value]; exists {
			return ErrInvalidRolePolicy
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validRolePolicyCapability(value string) bool {
	switch value {
	case "structured_output", "reasoning", "coding", "long_context", "review", "concise_explanation":
		return true
	default:
		return false
	}
}

func validRolePolicySpendClass(value string) bool {
	switch value {
	case RolePolicySpendSubscriptionOnly, RolePolicySpendInteractivePackage, RolePolicySpendExplicitAPI, RolePolicySpendUnknown:
		return true
	default:
		return false
	}
}

func validRolePolicyPreference(value string) bool {
	return value == RolePolicyPreferenceCandidateOrder || value == RolePolicyPreferenceSubscriptionFirst
}
