package engine

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/sandbox"
	"foundry/internal/store"
	"testing"
)

// Resolve runs after Engine account preflight but before execution approval.
// The real runner is external; this test seam keeps all Store mutations real.
type accountApprovalRunner struct {
	sandbox.Runner
	changeAvailability func() error
}

func (r accountApprovalRunner) Resolve(ctx context.Context, image string) (string, error) {
	resolved, err := r.Runner.Resolve(ctx, image)
	if err == nil {
		err = r.changeAvailability()
	}
	return resolved, err
}

func TestAccountApprovalAvailabilityRaceCreatesNoAuthority(t *testing.T) {
	for _, mode := range []string{"", domain.WorkspaceModeGenerationsV1} {
		for _, change := range []string{"disable", "revoke"} {
			t.Run(mode+"/"+change, func(t *testing.T) {
				e, s, g, runner, old := setup(t, research, plan)
				a := bindSyntheticAccount(t, s)
				e.gateway = syntheticAccountGateway{Gateway: g}
				m, err := e.Create(context.Background(), accountCreateRequest(old, mode))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = e.Plan(context.Background(), m.ID, m.Revision); err != nil {
					t.Fatal(err)
				}
				m = wait(t, s, m.ID, "awaiting_approval")
				before, _ := json.Marshal(m)
				events, _ := s.Events(0, 100)
				e.runner = accountApprovalRunner{Runner: runner, changeAvailability: func() error {
					if change == "disable" {
						a.Enabled = false
						_, err := s.SaveModelAccount(a, a.Revision)
						return err
					}
					_, err := s.RevokeModelCredential(a.ID, 1, a.Revision)
					return err
				}}
				if _, err = e.Approve(context.Background(), m.ID, m.Revision); !errors.Is(err, store.ErrModelAccountUnavailable) {
					t.Fatalf("approval after %s returned %v; must reject before authority commit", change, err)
				}
				got, err := s.GetMission(m.ID)
				if err != nil {
					t.Fatal(err)
				}
				after, _ := json.Marshal(got)
				afterEvents, _ := s.Events(0, 100)
				if string(before) != string(after) || len(events) != len(afterEvents) {
					t.Fatal("denied approval changed approvals, epoch, state, revision, budget or events")
				}
			})
		}
	}
}
