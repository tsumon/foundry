package engine

import (
	"context"
	"errors"
	"fmt"

	"foundry/internal/domain"
	"foundry/internal/store"
)

// A diagnostic subtype only: callers must still deny all C4 recovery and
// execution. It never authorizes adoption of the installed runtime by a task.
var ErrC4RuntimeMismatch = fmt.Errorf("%w: installed embedded VM differs from frozen task", ErrC4Unavailable)

func CheckCodingRecoveryModelSnapshots(s *store.Store) error {
	missions, err := s.ListMissions()
	if err != nil {
		return err
	}
	for _, m := range missions {
		if !isModelC4(m) || c4Structure(m) != nil || domain.ValidateCodingTrace(m) != nil {
			return ErrC4Unavailable
		}
	}
	return validateRecoveryModelSnapshots(missions)
}

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
		if c4Structure(m) != nil || domain.ValidateMissionModelSnapshot(m) != nil || domain.ValidateCodingTrace(m) != nil {
			return ErrC4Unavailable
		}
		if err = e.ValidateRepositoryImport(m); err != nil {
			return ErrC4Unavailable
		}
		if m.Spec.C4 != nil && m.Spec.C4.SchemaVersion == 2 {
			runtime := e.options.C4Controlled.vmForProfile(*m.Spec.C4)
			if runtime == nil || runtime.ValidateInstalledV2() != nil {
				return ErrC4RuntimeMismatch
			}
			if _, _, _, err = e.resolveV2Material(context.Background(), m); err != nil {
				return ErrC4Unavailable
			}
		}
		if e.c4ControlledGate(m, false) != nil {
			c := e.options.C4Controlled
			// Compare validated, trusted metadata only. No VM/credential access,
			// migration or rewritten readiness/baseline authority is allowed.
			if c.modelBacked && isModelC4(m) && c.source == "embedded_vm_c4_control" && (c.vm != nil && c.vm.Trusted() || c.currentV2 != nil && c.currentV2.Trusted()) && m.Spec.C4.Mode == "embedded_vm" && !c.vmProfileMatches(*m.Spec.C4) {
				return ErrC4RuntimeMismatch
			}
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
