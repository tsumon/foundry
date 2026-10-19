package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"foundry/internal/domain"
)

var (
	ErrRolePolicyValidation = errors.New("role_policy_validation")
	ErrRolePolicyStale      = errors.New("role_policy_stale")
	ErrRolePolicyIncomplete = errors.New("role_policy_incomplete")
)

const rolePolicyStoredSchema = 1

type storedRoleModelPolicy struct {
	SchemaVersion int                    `json:"schema_version"`
	Policy        domain.RoleModelPolicy `json:"policy"`
}

type storedRolePolicyProposal struct {
	SchemaVersion int                       `json:"schema_version"`
	Proposal      domain.RolePolicyProposal `json:"proposal"`
}

type storedRolePolicyDecision struct {
	SchemaVersion int                       `json:"schema_version"`
	Decision      domain.RolePolicyDecision `json:"decision"`
}

type rolePolicyCandidateMaterial struct {
	Role          domain.Role                    `json:"role"`
	ProfileID     string                         `json:"profile_id"`
	StoredDigest  string                         `json:"stored_digest"`
	CurrentDigest string                         `json:"current_digest"`
	Account       *rolePolicyAccountMaterial     `json:"account,omitempty"`
	Route         *domain.ModelRoute             `json:"route,omitempty"`
	Credential    *domain.ModelCredentialVersion `json:"credential,omitempty"`
	Authorization *domain.ModelAuthorization     `json:"authorization,omitempty"`
	Evidence      *domain.RolePolicyEvidence     `json:"evidence,omitempty"`
}

type rolePolicyAccountMaterial struct {
	ID                       string `json:"id"`
	Revision                 int64  `json:"revision"`
	Provider                 string `json:"provider"`
	BaseURL                  string `json:"base_url"`
	AuthMethod               string `json:"auth_method"`
	BillingMode              string `json:"billing_mode"`
	Enabled                  bool   `json:"enabled"`
	CurrentCredentialVersion int64  `json:"current_credential_version"`
}

type rolePolicyEvaluation struct {
	SettingsDigest string
	InputsDigest   string
	Roles          map[domain.Role]domain.RolePolicyRoleResult
	Selected       map[domain.Role]string
	Confirmable    bool
}

func (s *Store) GetRolePolicyState() (domain.RolePolicyState, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.RolePolicyState{}, err
	}
	defer tx.Rollback()
	policy, err := latestRoleModelPolicyTx(tx)
	if err != nil {
		return domain.RolePolicyState{}, err
	}
	profilesDigest, err := rolePolicyProfilesDigestTx(tx)
	if err != nil {
		return domain.RolePolicyState{}, err
	}
	state := domain.RolePolicyState{Policy: policy, ProfilesDigest: profilesDigest}
	proposal, err := latestRolePolicyProposalTx(tx)
	if err == nil {
		state.LatestProposal = &proposal
	} else if !errors.Is(err, ErrNotFound) {
		return domain.RolePolicyState{}, err
	}
	settings, err := rolePolicySettingsTx(tx)
	if err != nil {
		return domain.RolePolicyState{}, err
	}
	if settings.ModelPolicyDecisionID != "" {
		decision, readErr := rolePolicyDecisionTx(tx, settings.ModelPolicyDecisionID)
		if readErr != nil {
			return domain.RolePolicyState{}, readErr
		}
		state.ActiveDecision = &decision
	}
	if err = tx.Commit(); err != nil {
		return domain.RolePolicyState{}, err
	}
	return state, nil
}

func (s *Store) SaveRoleModelPolicy(input domain.RolePolicyInput, expected int64, profilesDigest string) (domain.RoleModelPolicy, error) {
	if expected < 0 || len(profilesDigest) != 64 || domain.ValidateRolePolicyInput(input) != nil {
		return domain.RoleModelPolicy{}, ErrRolePolicyValidation
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.RoleModelPolicy{}, err
	}
	defer tx.Rollback()
	currentProfilesDigest, err := rolePolicyProfilesDigestTx(tx)
	if err != nil {
		return domain.RoleModelPolicy{}, err
	}
	var current int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(revision),0) FROM role_model_policies`).Scan(&current); err != nil {
		return domain.RoleModelPolicy{}, err
	}
	if current != expected || currentProfilesDigest != profilesDigest {
		return domain.RoleModelPolicy{}, ErrRolePolicyStale
	}
	policy := domain.RoleModelPolicy{Revision: current + 1, CreatedAt: timestamp(), Roles: map[domain.Role]domain.RolePolicyRole{}}
	for _, role := range domain.RolePolicyRoles() {
		inputRole := input.Roles[role]
		storedRole := domain.RolePolicyRole{
			Candidates: []domain.RolePolicyCandidate{}, RequiredCapabilities: append([]string{}, inputRole.RequiredCapabilities...),
			AllowedSpendClasses: append([]string{}, inputRole.AllowedSpendClasses...), Preference: inputRole.Preference,
		}
		for _, candidate := range inputRole.Candidates {
			profile, readErr := rolePolicyProfileTx(tx, candidate.ProfileID)
			if errors.Is(readErr, ErrNotFound) {
				return domain.RoleModelPolicy{}, fmt.Errorf("%w: profile unavailable", ErrRolePolicyValidation)
			}
			if readErr != nil {
				return domain.RoleModelPolicy{}, readErr
			}
			digest, digestErr := domain.RolePolicyProfileDigest(profile)
			if digestErr != nil {
				return domain.RoleModelPolicy{}, fmt.Errorf("%w: invalid profile", ErrRolePolicyValidation)
			}
			candidate.Capabilities = append([]string{}, candidate.Capabilities...)
			storedRole.Candidates = append(storedRole.Candidates, domain.RolePolicyCandidate{RolePolicyCandidateInput: candidate, ProfileDigest: digest})
		}
		policy.Roles[role] = storedRole
	}
	raw, err := json.Marshal(storedRoleModelPolicy{SchemaVersion: rolePolicyStoredSchema, Policy: policy})
	if err != nil {
		return domain.RoleModelPolicy{}, err
	}
	if _, err = tx.Exec(`INSERT INTO role_model_policies(revision,created_at,data) VALUES(?,?,?)`, policy.Revision, policy.CreatedAt, raw); err != nil {
		if isConstraint(err) {
			return domain.RoleModelPolicy{}, ErrRolePolicyStale
		}
		return domain.RoleModelPolicy{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.RoleModelPolicy{}, err
	}
	return policy, nil
}

func (s *Store) ProposeRoleModels(proposalID string) (domain.RolePolicyProposal, error) {
	if !safeRolePolicyStoreID(proposalID) {
		return domain.RolePolicyProposal{}, ErrRolePolicyValidation
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.RolePolicyProposal{}, err
	}
	defer tx.Rollback()
	policy, err := latestRoleModelPolicyTx(tx)
	if err != nil {
		return domain.RolePolicyProposal{}, err
	}
	if policy.Revision == 0 {
		return domain.RolePolicyProposal{}, ErrRolePolicyIncomplete
	}
	evaluation, err := evaluateRolePolicyTx(tx, policy)
	if err != nil {
		return domain.RolePolicyProposal{}, err
	}
	proposal := domain.RolePolicyProposal{
		ID: proposalID, PolicyRevision: policy.Revision, CreatedAt: timestamp(), SettingsDigest: evaluation.SettingsDigest,
		InputsDigest: evaluation.InputsDigest, Confirmable: evaluation.Confirmable, Roles: evaluation.Roles,
	}
	proposal.Digest, err = rolePolicyProposalDigest(proposal)
	if err != nil {
		return domain.RolePolicyProposal{}, err
	}
	raw, err := json.Marshal(storedRolePolicyProposal{SchemaVersion: rolePolicyStoredSchema, Proposal: proposal})
	if err != nil {
		return domain.RolePolicyProposal{}, err
	}
	if _, err = tx.Exec(`INSERT INTO role_model_proposals(id,policy_revision,created_at,settings_digest,inputs_digest,digest,data) VALUES(?,?,?,?,?,?,?)`, proposal.ID, proposal.PolicyRevision, proposal.CreatedAt, proposal.SettingsDigest, proposal.InputsDigest, proposal.Digest, raw); err != nil {
		if isConstraint(err) {
			return domain.RolePolicyProposal{}, ErrRolePolicyStale
		}
		return domain.RolePolicyProposal{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.RolePolicyProposal{}, err
	}
	return proposal, nil
}

func (s *Store) ConfirmRoleModelProposal(id, digest, decisionID string) (domain.RolePolicyDecision, domain.Settings, bool, error) {
	if !safeRolePolicyStoreID(id) || !safeRolePolicyStoreID(decisionID) || len(digest) != 64 {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, ErrRolePolicyValidation
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, err
	}
	defer tx.Rollback()
	settings, err := rolePolicySettingsTx(tx)
	if err != nil {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, err
	}
	if existing, readErr := rolePolicyDecisionByProposalTx(tx, id); readErr == nil {
		return existing, settings, false, nil
	} else if !errors.Is(readErr, ErrNotFound) {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, readErr
	}
	proposal, err := rolePolicyProposalTx(tx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return domain.RolePolicyDecision{}, domain.Settings{}, false, ErrRolePolicyStale
		}
		return domain.RolePolicyDecision{}, domain.Settings{}, false, err
	}
	if proposal.Digest != digest || !proposal.Confirmable {
		if !proposal.Confirmable {
			return domain.RolePolicyDecision{}, domain.Settings{}, false, ErrRolePolicyIncomplete
		}
		return domain.RolePolicyDecision{}, domain.Settings{}, false, ErrRolePolicyStale
	}
	policy, err := latestRoleModelPolicyTx(tx)
	if err != nil {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, err
	}
	if policy.Revision != proposal.PolicyRevision {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, ErrRolePolicyStale
	}
	evaluation, err := evaluateRolePolicyTx(tx, policy)
	if err != nil {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, err
	}
	if !evaluation.Confirmable || evaluation.SettingsDigest != proposal.SettingsDigest || evaluation.InputsDigest != proposal.InputsDigest || !reflect.DeepEqual(evaluation.Roles, proposal.Roles) {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, ErrRolePolicyStale
	}
	validSelection, err := validateRolePolicySelectionTx(tx, evaluation.Selected)
	if err != nil {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, err
	}
	if !validSelection {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, ErrRolePolicyIncomplete
	}
	decision := domain.RolePolicyDecision{ID: decisionID, ProposalID: proposal.ID, ConfirmedAt: timestamp(), Proposal: proposal}
	decisionRaw, err := json.Marshal(storedRolePolicyDecision{SchemaVersion: rolePolicyStoredSchema, Decision: decision})
	if err != nil {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, err
	}
	if _, err = tx.Exec(`INSERT INTO role_model_decisions(id,proposal_id,confirmed_at,data) VALUES(?,?,?,?)`, decision.ID, decision.ProposalID, decision.ConfirmedAt, decisionRaw); err != nil {
		if isConstraint(err) {
			return domain.RolePolicyDecision{}, domain.Settings{}, false, ErrRolePolicyStale
		}
		return domain.RolePolicyDecision{}, domain.Settings{}, false, err
	}
	settings.RoleBindings = make(map[domain.Role]string, len(evaluation.Selected))
	for role, profileID := range evaluation.Selected {
		settings.RoleBindings[role] = profileID
	}
	settings.ModelPolicyDecisionID = decision.ID
	settingsRaw, err := json.Marshal(settings)
	if err != nil {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, err
	}
	if _, err = tx.Exec(`INSERT INTO settings(id,data) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, settingsRaw); err != nil {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return domain.RolePolicyDecision{}, domain.Settings{}, false, err
	}
	return decision, settings, true, nil
}

func evaluateRolePolicyTx(tx *sql.Tx, policy domain.RoleModelPolicy) (rolePolicyEvaluation, error) {
	settings, err := rolePolicySettingsTx(tx)
	if err != nil {
		return rolePolicyEvaluation{}, err
	}
	settingsDigest, err := rolePolicyHash("FOUNDRY role model settings v1", settings)
	if err != nil {
		return rolePolicyEvaluation{}, err
	}
	profilesDigest, err := rolePolicyProfilesDigestTx(tx)
	if err != nil {
		return rolePolicyEvaluation{}, err
	}
	result := rolePolicyEvaluation{SettingsDigest: settingsDigest, Roles: map[domain.Role]domain.RolePolicyRoleResult{}, Selected: map[domain.Role]string{}, Confirmable: true}
	materials := make([]rolePolicyCandidateMaterial, 0)
	for _, role := range domain.RolePolicyRoles() {
		policyRole, ok := policy.Roles[role]
		if !ok {
			return rolePolicyEvaluation{}, errors.New("stored role policy is incomplete")
		}
		roleResult := domain.RolePolicyRoleResult{RequiredCapabilities: append([]string{}, policyRole.RequiredCapabilities...), Candidates: []domain.RolePolicyCandidateResult{}}
		for _, candidate := range policyRole.Candidates {
			candidateResult, material, evalErr := evaluateRolePolicyCandidateTx(tx, role, policyRole, candidate)
			if evalErr != nil {
				return rolePolicyEvaluation{}, evalErr
			}
			roleResult.Candidates = append(roleResult.Candidates, candidateResult)
			materials = append(materials, material)
		}
		roleResult.SelectedProfileID = selectRolePolicyCandidate(policyRole.Preference, roleResult.Candidates)
		if roleResult.SelectedProfileID == "" {
			result.Confirmable = false
		} else {
			result.Selected[role] = roleResult.SelectedProfileID
		}
		result.Roles[role] = roleResult
	}
	if result.Confirmable {
		validSelection, resolveErr := validateRolePolicySelectionTx(tx, result.Selected)
		if resolveErr != nil {
			return rolePolicyEvaluation{}, resolveErr
		}
		if !validSelection {
			result.Confirmable = false
			for _, role := range domain.RolePolicyRoles() {
				roleResult := result.Roles[role]
				for index := range roleResult.Candidates {
					if roleResult.Candidates[index].ProfileID == roleResult.SelectedProfileID {
						roleResult.Candidates[index].Eligible = false
						roleResult.Candidates[index].Reasons = appendRolePolicyReason(roleResult.Candidates[index].Reasons, domain.RolePolicyReasonSelectionInvalid)
					}
				}
				roleResult.SelectedProfileID = ""
				result.Roles[role] = roleResult
			}
			result.Selected = map[domain.Role]string{}
		}
	}
	result.InputsDigest, err = rolePolicyHash("FOUNDRY role model inputs v1", struct {
		PolicyRevision int64                                       `json:"policy_revision"`
		SettingsDigest string                                      `json:"settings_digest"`
		ProfilesDigest string                                      `json:"profiles_digest"`
		Roles          map[domain.Role]domain.RolePolicyRoleResult `json:"roles"`
		Materials      []rolePolicyCandidateMaterial               `json:"materials"`
	}{policy.Revision, settingsDigest, profilesDigest, result.Roles, materials})
	return result, err
}

func evaluateRolePolicyCandidateTx(tx *sql.Tx, role domain.Role, policyRole domain.RolePolicyRole, candidate domain.RolePolicyCandidate) (domain.RolePolicyCandidateResult, rolePolicyCandidateMaterial, error) {
	result := domain.RolePolicyCandidateResult{
		ProfileID: candidate.ProfileID, ProfileDigest: candidate.ProfileDigest, SpendClass: domain.RolePolicySpendUnknown,
		Reasons: []string{}, Capabilities: append([]string{}, candidate.Capabilities...), Rationale: candidate.Rationale, ExecutionNotice: domain.RolePolicyNoticeLegacyUnknown,
	}
	material := rolePolicyCandidateMaterial{Role: role, ProfileID: candidate.ProfileID, StoredDigest: candidate.ProfileDigest}
	profile, err := rolePolicyProfileTx(tx, candidate.ProfileID)
	if errors.Is(err, ErrNotFound) {
		result.Reasons = append(result.Reasons, domain.RolePolicyReasonProfileMissing)
		return result, material, nil
	}
	if err != nil {
		return result, material, err
	}
	currentDigest, err := domain.RolePolicyProfileDigest(profile)
	if err != nil {
		return result, material, err
	}
	material.CurrentDigest = currentDigest
	if currentDigest != candidate.ProfileDigest {
		result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonProfileChanged)
	}
	for _, required := range policyRole.RequiredCapabilities {
		if !containsRolePolicyValue(candidate.Capabilities, required) {
			result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonCapabilityMissing)
		}
	}
	if profile.AccountID != "" {
		result.ExecutionNotice = domain.RolePolicyNoticeLiveClosed
		account, accountErr := modelAccountTx(tx, profile.AccountID)
		if errors.Is(accountErr, ErrNotFound) {
			result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonCredentialUnavailable)
		} else if accountErr != nil {
			return result, material, accountErr
		} else {
			if domain.ValidateModelAccount(account) != nil {
				return result, material, errors.New("stored model account is invalid")
			}
			material.Account = &rolePolicyAccountMaterial{account.ID, account.Revision, account.Provider, account.BaseURL, account.AuthMethod, account.BillingMode, account.Enabled, account.CurrentCredentialVersion}
			if !account.Enabled {
				result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonAccountDisabled)
			}
			if profile.RouteID != "" {
				route, routeErr := modelRouteTx(tx, profile.RouteID)
				if routeErr != nil && !errors.Is(routeErr, ErrNotFound) {
					return result, material, routeErr
				}
				if routeErr != nil || domain.ValidateModelRoute(route, account) != nil || route.Protocol != profile.Protocol || route.BaseURL != profile.BaseURL {
					result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonRouteInvalid)
				} else {
					material.Route = &route
					result.SpendClass = route.SpendClass
					if route.ExtraUsage != "off" {
						result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonExtraUsageNotOff)
					}
				}
			}
			if account.AuthMethod == "api_key" {
				if account.CurrentCredentialVersion < 1 {
					result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonCredentialUnavailable)
				} else {
					credential, credentialErr := modelCredentialTx(tx, account.ID, account.CurrentCredentialVersion)
					if credentialErr != nil && !errors.Is(credentialErr, ErrNotFound) {
						return result, material, credentialErr
					}
					if credentialErr != nil || credential.State != "available" || credential.CleanupPending {
						result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonCredentialUnavailable)
					} else {
						material.Credential = &credential
					}
				}
			} else if account.AuthMethod == "openai_siwc" {
				authorization, authorizationErr := currentModelAuthorizationTx(tx, account.ID)
				if authorizationErr != nil && !errors.Is(authorizationErr, ErrNotFound) {
					return result, material, authorizationErr
				}
				if authorizationErr == nil {
					material.Authorization = &authorization
				}
			}
			selection, resolveErr := resolveModelProfileTx(tx, profile.ID)
			if resolveErr != nil {
				if !rolePolicyExpectedUnavailability(resolveErr) {
					return result, material, resolveErr
				}
				if account.AuthMethod == "openai_siwc" {
					result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonAuthorizationUnavailable)
				} else {
					result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonCredentialUnavailable)
				}
			} else if selection.Route != nil {
				if _, _, metadataErr := resolveModelCredentialMetadataReadTx(tx, selection, true); metadataErr != nil {
					if !rolePolicyExpectedUnavailability(metadataErr) {
						return result, material, metadataErr
					}
					if account.AuthMethod == "openai_siwc" {
						result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonAuthorizationUnavailable)
					} else {
						result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonCredentialUnavailable)
					}
				}
			}
		}
	}
	if !containsRolePolicyValue(policyRole.AllowedSpendClasses, result.SpendClass) {
		result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonSpendClassNotAllowed)
	}
	if candidate.EvidenceInvocationID != "" {
		evidence, valid, evidenceErr := rolePolicyEvidenceTx(tx, candidate.EvidenceInvocationID, role, currentDigest)
		if evidenceErr != nil {
			return result, material, evidenceErr
		}
		if !valid {
			result.Reasons = appendRolePolicyReason(result.Reasons, domain.RolePolicyReasonEvidenceInvalid)
		} else {
			result.Evidence = evidence
			material.Evidence = evidence
		}
	}
	result.Eligible = len(result.Reasons) == 0
	return result, material, nil
}

// Only explicit domain availability/compatibility failures are exclusions.
// SQL, scanning, decoding and unknown errors must abort the entire operation.
func rolePolicyExpectedUnavailability(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrModelAccountUnavailable) ||
		errors.Is(err, ErrModelEntitlementDenied) || errors.Is(err, domain.ErrInvalidModelSnapshot)
}

func validateRolePolicySelectionTx(tx *sql.Tx, selected map[domain.Role]string) (bool, error) {
	_, _, err := resolveExplicitRoleModelsTx(tx, selected)
	if err == nil {
		return true, nil
	}
	if rolePolicyExpectedUnavailability(err) {
		return false, nil
	}
	return false, err
}

func selectRolePolicyCandidate(preference string, candidates []domain.RolePolicyCandidateResult) string {
	if preference == domain.RolePolicyPreferenceSubscriptionFirst {
		for _, candidate := range candidates {
			if candidate.Eligible && (candidate.SpendClass == domain.RolePolicySpendSubscriptionOnly || candidate.SpendClass == domain.RolePolicySpendInteractivePackage) {
				return candidate.ProfileID
			}
		}
	}
	for _, candidate := range candidates {
		if candidate.Eligible {
			return candidate.ProfileID
		}
	}
	return ""
}

func latestRoleModelPolicyTx(tx *sql.Tx) (domain.RoleModelPolicy, error) {
	var revision int64
	var createdAt string
	var raw []byte
	err := tx.QueryRow(`SELECT revision,created_at,data FROM role_model_policies ORDER BY revision DESC LIMIT 1`).Scan(&revision, &createdAt, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return emptyRoleModelPolicy(), nil
	}
	if err != nil {
		return domain.RoleModelPolicy{}, err
	}
	policy, err := decodeStoredRoleModelPolicy(raw)
	if err != nil || policy.Revision != revision || policy.CreatedAt != createdAt {
		if err != nil {
			return domain.RoleModelPolicy{}, err
		}
		return domain.RoleModelPolicy{}, errors.New("stored role policy columns disagree")
	}
	return policy, nil
}

func emptyRoleModelPolicy() domain.RoleModelPolicy {
	policy := domain.RoleModelPolicy{Roles: map[domain.Role]domain.RolePolicyRole{}}
	for _, role := range domain.RolePolicyRoles() {
		policy.Roles[role] = domain.RolePolicyRole{Candidates: []domain.RolePolicyCandidate{}, RequiredCapabilities: []string{}, AllowedSpendClasses: []string{}, Preference: domain.RolePolicyPreferenceCandidateOrder}
	}
	return policy
}

func rolePolicyProfileTx(tx *sql.Tx, id string) (domain.Profile, error) {
	var raw []byte
	if err := tx.QueryRow(`SELECT data FROM profiles WHERE id=?`, id).Scan(&raw); err != nil {
		return domain.Profile{}, modelStoreError(err)
	}
	var profile domain.Profile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return domain.Profile{}, err
	}
	return profile, nil
}

func rolePolicyProfilesDigestTx(tx *sql.Tx) (string, error) {
	rows, err := tx.Query(`SELECT data FROM profiles ORDER BY id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	type entry struct{ ID, Digest string }
	entries := []entry{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return "", err
		}
		var profile domain.Profile
		if err = json.Unmarshal(raw, &profile); err != nil {
			return "", err
		}
		digest, digestErr := domain.RolePolicyProfileDigest(profile)
		if digestErr != nil {
			return "", digestErr
		}
		entries = append(entries, entry{profile.ID, digest})
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return rolePolicyHash("FOUNDRY role model profiles v1", entries)
}

func rolePolicySettingsTx(tx *sql.Tx) (domain.Settings, error) {
	var raw []byte
	err := tx.QueryRow(`SELECT data FROM settings WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Settings{RoleBindings: map[domain.Role]string{}}, nil
	}
	if err != nil {
		return domain.Settings{}, err
	}
	var settings domain.Settings
	if err = json.Unmarshal(raw, &settings); err != nil {
		return domain.Settings{}, err
	}
	if settings.RoleBindings == nil {
		settings.RoleBindings = map[domain.Role]string{}
	}
	return settings, nil
}

func latestRolePolicyProposalTx(tx *sql.Tx) (domain.RolePolicyProposal, error) {
	var id string
	if err := tx.QueryRow(`SELECT id FROM role_model_proposals ORDER BY rowid DESC LIMIT 1`).Scan(&id); err != nil {
		return domain.RolePolicyProposal{}, modelStoreError(err)
	}
	return rolePolicyProposalTx(tx, id)
}

func rolePolicyProposalTx(tx *sql.Tx, id string) (domain.RolePolicyProposal, error) {
	var policyRevision int64
	var createdAt, settingsDigest, inputsDigest, digest string
	var raw []byte
	err := tx.QueryRow(`SELECT policy_revision,created_at,settings_digest,inputs_digest,digest,data FROM role_model_proposals WHERE id=?`, id).Scan(&policyRevision, &createdAt, &settingsDigest, &inputsDigest, &digest, &raw)
	if err != nil {
		return domain.RolePolicyProposal{}, modelStoreError(err)
	}
	var stored storedRolePolicyProposal
	if err = decodeStrictRolePolicyJSON(raw, &stored); err != nil || stored.SchemaVersion != rolePolicyStoredSchema {
		return domain.RolePolicyProposal{}, errors.New("unsupported or corrupt stored role policy proposal")
	}
	proposal := stored.Proposal
	want, digestErr := rolePolicyProposalDigest(proposal)
	if digestErr != nil || proposal.ID != id || proposal.PolicyRevision != policyRevision || proposal.CreatedAt != createdAt || proposal.SettingsDigest != settingsDigest || proposal.InputsDigest != inputsDigest || proposal.Digest != digest || want != digest || len(proposal.Roles) != len(domain.RolePolicyRoles()) {
		return domain.RolePolicyProposal{}, errors.New("stored role policy proposal failed integrity check")
	}
	return proposal, nil
}

func rolePolicyDecisionTx(tx *sql.Tx, id string) (domain.RolePolicyDecision, error) {
	return scanRolePolicyDecision(tx, `SELECT proposal_id,confirmed_at,data FROM role_model_decisions WHERE id=?`, id, id)
}

func rolePolicyDecisionByProposalTx(tx *sql.Tx, proposalID string) (domain.RolePolicyDecision, error) {
	var id string
	if err := tx.QueryRow(`SELECT id FROM role_model_decisions WHERE proposal_id=?`, proposalID).Scan(&id); err != nil {
		return domain.RolePolicyDecision{}, modelStoreError(err)
	}
	return rolePolicyDecisionTx(tx, id)
}

func scanRolePolicyDecision(tx *sql.Tx, query string, arg any, expectedID string) (domain.RolePolicyDecision, error) {
	var proposalID, confirmedAt string
	var raw []byte
	if err := tx.QueryRow(query, arg).Scan(&proposalID, &confirmedAt, &raw); err != nil {
		return domain.RolePolicyDecision{}, modelStoreError(err)
	}
	var stored storedRolePolicyDecision
	if err := decodeStrictRolePolicyJSON(raw, &stored); err != nil || stored.SchemaVersion != rolePolicyStoredSchema {
		return domain.RolePolicyDecision{}, errors.New("unsupported or corrupt stored role policy decision")
	}
	decision := stored.Decision
	proposal, err := rolePolicyProposalTx(tx, proposalID)
	if err != nil || decision.ID != expectedID || decision.ProposalID != proposalID || decision.ConfirmedAt != confirmedAt || !reflect.DeepEqual(decision.Proposal, proposal) {
		return domain.RolePolicyDecision{}, errors.New("stored role policy decision failed integrity check")
	}
	return decision, nil
}

func decodeStoredRoleModelPolicy(raw []byte) (domain.RoleModelPolicy, error) {
	var stored storedRoleModelPolicy
	if err := decodeStrictRolePolicyJSON(raw, &stored); err != nil || stored.SchemaVersion != rolePolicyStoredSchema {
		return domain.RoleModelPolicy{}, errors.New("unsupported or corrupt stored role policy")
	}
	policy := stored.Policy
	if policy.Revision < 1 || policy.CreatedAt == "" || len(policy.Roles) != len(domain.RolePolicyRoles()) {
		return domain.RoleModelPolicy{}, errors.New("invalid stored role policy")
	}
	input := domain.RolePolicyInput{Roles: map[domain.Role]domain.RolePolicyRoleInput{}}
	for _, role := range domain.RolePolicyRoles() {
		storedRole, ok := policy.Roles[role]
		if !ok {
			return domain.RoleModelPolicy{}, errors.New("invalid stored role policy roles")
		}
		inputRole := domain.RolePolicyRoleInput{Candidates: []domain.RolePolicyCandidateInput{}, RequiredCapabilities: storedRole.RequiredCapabilities, AllowedSpendClasses: storedRole.AllowedSpendClasses, Preference: storedRole.Preference}
		for _, candidate := range storedRole.Candidates {
			if !lowerHex(candidate.ProfileDigest, 64) {
				return domain.RoleModelPolicy{}, errors.New("invalid stored role policy digest")
			}
			inputRole.Candidates = append(inputRole.Candidates, candidate.RolePolicyCandidateInput)
		}
		input.Roles[role] = inputRole
	}
	if domain.ValidateRolePolicyInput(input) != nil {
		return domain.RoleModelPolicy{}, errors.New("invalid stored role policy input")
	}
	return policy, nil
}

func rolePolicyProposalDigest(proposal domain.RolePolicyProposal) (string, error) {
	proposal.Digest = ""
	return rolePolicyHash("FOUNDRY role model recommendation v1", struct {
		ID             string                                      `json:"id"`
		PolicyRevision int64                                       `json:"policy_revision"`
		CreatedAt      string                                      `json:"created_at"`
		SettingsDigest string                                      `json:"settings_digest"`
		InputsDigest   string                                      `json:"inputs_digest"`
		Confirmable    bool                                        `json:"confirmable"`
		Roles          map[domain.Role]domain.RolePolicyRoleResult `json:"roles"`
	}{proposal.ID, proposal.PolicyRevision, proposal.CreatedAt, proposal.SettingsDigest, proposal.InputsDigest, proposal.Confirmable, proposal.Roles})
}

func rolePolicyHash(prefix string, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(prefix))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(raw)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func decodeStrictRolePolicyJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing stored role policy data")
	}
	return nil
}

func safeRolePolicyStoreID(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value) && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsSpace) < 0 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func containsRolePolicyValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func appendRolePolicyReason(values []string, value string) []string {
	if containsRolePolicyValue(values, value) {
		return values
	}
	return append(values, value)
}
