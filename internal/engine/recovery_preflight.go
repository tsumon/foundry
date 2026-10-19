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
	return validateRecoveryModelSnapshots(missions)
}

func validateRecoveryModelSnapshots(missions []domain.Mission) error {
	for _, m := range missions {
		if err := domain.ValidateMissionModelSnapshot(m); err != nil {
			_, bound, _ := domain.BaseWorkspaceMode(m.WorkspaceMode)
			if errors.Is(err, domain.ErrInvalidModelSnapshot) || m.ModelSnapshot != nil || hasAccountProfiles(m) || bound {
				return domain.ErrInvalidModelSnapshot
			}
		}
	}
	return nil
}
