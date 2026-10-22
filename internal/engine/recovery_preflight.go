package engine

import (
	"errors"

	"foundry/internal/domain"
	"foundry/internal/store"
)

// CheckRecoveryModelSnapshots is read-only and must precede every startup
// component that can mutate durable state or invoke an external dependency.
// A structural failure selects instance-wide diagnostic startup; it does not
// authorize partial engine recovery. Availability (disabled/revoked accounts)
// is deliberately not a structural failure.
func CheckRecoveryModelSnapshots(s *store.Store) error {
	missions, err := s.ListMissions()
	if err != nil {
		return err
	}
	// This is the production startup preflight. Only disposable Go-only runtimes
	// can admit C4; a production process must not mutate even valid C4 records.
	for _, m := range missions {
		if isC4(m) {
			return ErrC4Unavailable
		}
	}
	return validateRecoveryModelSnapshots(missions)
}

// Isolated startup preflight is read-only and never reconciles a legacy runner.
func (e *Engine) CheckC4ControlledRecovery() error {
	if e.options.C4Controlled == nil || e.options.C4 != nil {
		return ErrC4Unavailable
	}
	missions, err := e.s.ListMissions()
	if err != nil {
		return err
	}
	for _, m := range missions {
		if e.c4ControlledGate(m, false) != nil || domain.ValidateCodingTrace(m) != nil {
			return ErrC4Unavailable
		}
		if err = e.ValidateRepositoryImport(m); err != nil {
			return ErrC4Unavailable
		}
	}
	return nil
}

func validateRecoveryModelSnapshots(missions []domain.Mission) error {
	for _, m := range missions {
		if err := c4Structure(m); err != nil {
			return err
		}
		if err := domain.ValidateMissionModelSnapshot(m); err != nil {
			_, bound, _ := domain.BaseWorkspaceMode(m.WorkspaceMode)
			if errors.Is(err, domain.ErrInvalidModelSnapshot) || m.ModelSnapshot != nil || hasAccountProfiles(m) || bound {
				return domain.ErrInvalidModelSnapshot
			}
		}
	}
	return nil
}
