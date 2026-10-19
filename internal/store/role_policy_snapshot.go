package store

import (
	"database/sql"
	"errors"

	"foundry/internal/domain"
)

// ResolveMissionModelSelection resolves execution profiles and their optional
// confirmed-policy provenance from one SQLite snapshot.
func (s *Store) ResolveMissionModelSelection(requestedDefault string) (string, map[domain.Role]domain.Profile, *domain.MissionModelSnapshot, *domain.MissionRolePolicySnapshot, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", nil, nil, nil, err
	}
	defer tx.Rollback()
	id, profiles, modelSnapshot, policySnapshot, err := resolveMissionModelSelectionTx(tx, requestedDefault)
	if err != nil {
		return "", nil, nil, nil, err
	}
	if err = tx.Commit(); err != nil {
		return "", nil, nil, nil, err
	}
	return id, profiles, modelSnapshot, policySnapshot, nil
}

func resolveMissionModelSelectionTx(tx *sql.Tx, requestedDefault string) (string, map[domain.Role]domain.Profile, *domain.MissionModelSnapshot, *domain.MissionRolePolicySnapshot, error) {
	settings, err := rolePolicySettingsTx(tx)
	if err != nil {
		return "", nil, nil, nil, err
	}
	if requestedDefault == "" {
		requestedDefault = settings.DefaultProfileID
	}
	if settings.ModelPolicyDecisionID == "" {
		profiles, snapshot, resolveErr := resolveSettingsRoleModelsTx(tx, settings, requestedDefault)
		return requestedDefault, profiles, snapshot, nil, resolveErr
	}
	decision, err := rolePolicyDecisionTx(tx, settings.ModelPolicyDecisionID)
	if err != nil {
		return "", nil, nil, nil, err
	}
	selected, err := selectedRolePolicyProfiles(decision.Proposal)
	if err != nil {
		return "", nil, nil, nil, err
	}
	for _, role := range domain.RolePolicyRoles() {
		if settings.RoleBindings[role] != selected[role] {
			return "", nil, nil, nil, ErrConflict
		}
	}
	profiles, modelSnapshot, err := resolveExplicitRoleModelsTx(tx, selected)
	if err != nil {
		return "", nil, nil, nil, err
	}
	policySnapshot, err := missionRolePolicySnapshot(decision, profiles)
	if err != nil {
		return "", nil, nil, nil, err
	}
	return requestedDefault, profiles, modelSnapshot, policySnapshot, nil
}

func resolveSettingsRoleModelsTx(tx *sql.Tx, settings domain.Settings, requestedDefault string) (map[domain.Role]domain.Profile, *domain.MissionModelSnapshot, error) {
	roleProfileIDs := map[domain.Role]string{}
	for _, role := range domain.RolePolicyRoles() {
		id := settings.RoleBindings[role]
		if id == "" {
			id = requestedDefault
		}
		roleProfileIDs[role] = id
	}
	return resolveExplicitRoleModelsTx(tx, roleProfileIDs)
}

func selectedRolePolicyProfiles(proposal domain.RolePolicyProposal) (map[domain.Role]string, error) {
	if !proposal.Confirmable || len(proposal.Roles) != len(domain.RolePolicyRoles()) {
		return nil, domain.ErrInvalidModelSnapshot
	}
	selected := make(map[domain.Role]string, len(domain.RolePolicyRoles()))
	for _, role := range domain.RolePolicyRoles() {
		result, ok := proposal.Roles[role]
		if !ok || result.SelectedProfileID == "" {
			return nil, domain.ErrInvalidModelSnapshot
		}
		found := false
		for _, candidate := range result.Candidates {
			if candidate.ProfileID == result.SelectedProfileID && candidate.Eligible {
				if found {
					return nil, domain.ErrInvalidModelSnapshot
				}
				found = true
			}
		}
		if !found {
			return nil, domain.ErrInvalidModelSnapshot
		}
		selected[role] = result.SelectedProfileID
	}
	return selected, nil
}

func missionRolePolicySnapshot(decision domain.RolePolicyDecision, profiles map[domain.Role]domain.Profile) (*domain.MissionRolePolicySnapshot, error) {
	snapshot := &domain.MissionRolePolicySnapshot{
		SchemaVersion: 1, DecisionID: decision.ID, PolicyRevision: decision.Proposal.PolicyRevision,
		RecommendationDigest: decision.Proposal.Digest, ConfirmedAt: decision.ConfirmedAt,
		Roles: make(map[domain.Role]domain.RolePolicyRoleResult, len(domain.RolePolicyRoles())),
	}
	for _, role := range domain.RolePolicyRoles() {
		result := decision.Proposal.Roles[role]
		var selected *domain.RolePolicyCandidateResult
		for index := range result.Candidates {
			candidate := result.Candidates[index]
			if candidate.ProfileID == result.SelectedProfileID && candidate.Eligible {
				if selected != nil {
					return nil, domain.ErrInvalidModelSnapshot
				}
				copyCandidate := candidate
				copyCandidate.Reasons = append([]string{}, candidate.Reasons...)
				copyCandidate.Capabilities = append([]string{}, candidate.Capabilities...)
				if candidate.Evidence != nil {
					copyEvidence := *candidate.Evidence
					copyCandidate.Evidence = &copyEvidence
				}
				selected = &copyCandidate
			}
		}
		if selected == nil {
			return nil, domain.ErrInvalidModelSnapshot
		}
		snapshot.Roles[role] = domain.RolePolicyRoleResult{
			SelectedProfileID:    result.SelectedProfileID,
			RequiredCapabilities: append([]string{}, result.RequiredCapabilities...),
			Candidates:           []domain.RolePolicyCandidateResult{*selected},
		}
	}
	var err error
	snapshot.Digest, err = domain.MissionRolePolicySnapshotDigest(*snapshot)
	if err != nil {
		return nil, err
	}
	mission := domain.Mission{Profiles: profiles, ModelPolicySnapshot: snapshot}
	if err = domain.ValidateMissionRolePolicySnapshot(mission); err != nil {
		return nil, errors.Join(domain.ErrInvalidModelSnapshot, err)
	}
	return snapshot, nil
}
