package engine

import (
	"context"
	"foundry/internal/domain"
	"foundry/internal/model"
)

func hasAccountProfiles(m domain.Mission) bool {
	if m.ModelSnapshotInvalid {
		return true
	}
	for _, p := range m.Profiles {
		if p.AccountID != "" || p.AccountRevision != 0 || p.CredentialVersion != 0 {
			return true
		}
	}
	return false
}

func (e *Engine) preflightModelRoles(ctx context.Context, m domain.Mission, roles ...domain.Role) error {
	if isC4(m) {
		if err := e.c4Gate(m); err != nil {
			return err
		}
		if !isModelC4(m) {
			return nil
		}
		if _, err := e.currentC4Readiness(m); err != nil {
			return err
		}
	}
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return err
	}
	snapshot, err := domain.EffectiveModelSnapshot(m)
	if err != nil {
		return err
	}
	for _, role := range roles {
		p := m.Profiles[role]
		if domain.IsInferenceSnapshot(m) {
			if err := e.s.CheckModelCall(m.ID, role); err != nil {
				return err
			}
			if preflight, ok := e.gateway.(model.SelectionPreflighter); ok {
				if err := preflight.PreflightModelSelection(ctx, 2, snapshot.Roles[role]); err != nil {
					return err
				}
			} else if p.AccountID != "" {
				return model.ErrAccountInferenceDisabled
			}
			continue
		}
		if p.AccountID == "" {
			continue
		}
		preflight, ok := e.gateway.(model.AccountProfilePreflighter)
		if !ok {
			return model.ErrAccountInferenceDisabled
		}
		if err := e.s.CheckModelCall(m.ID, role); err != nil {
			return err
		}
		if err := preflight.PreflightAccountProfile(ctx, p); err != nil {
			return err
		}
	}
	return nil
}
