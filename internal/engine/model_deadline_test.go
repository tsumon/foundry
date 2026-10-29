package engine

import (
	"context"
	"foundry/internal/model"
	"testing"
	"testing/synctest"
	"time"
)

func TestControlledModelReasoningDeadline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		delay time.Duration
		state string
		calls int
	}{
		{"slow completed response", 95 * time.Second, "awaiting_approval", 3},
		{"bounded timeout no replay", 6 * time.Minute, "blocked", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, s, g, _, m := controlledSetup(t, controlledRead, controlledFinal, plan)
				calls := 0
				e.gateway = controlledGatewayFunc(func(ctx context.Context, r model.Request) (model.Result, error) {
					calls++
					if calls == 1 {
						select {
						case <-time.After(tc.delay):
						case <-ctx.Done():
							return model.Result{}, ctx.Err()
						}
					}
					return g.Complete(ctx, r)
				})
				start := time.Now()
				if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
					t.Fatal(err)
				}
				m = finishControlled(t, e, s, m.ID)
				if m.State != tc.state || calls != tc.calls || m.Budget.UsedCalls != tc.calls {
					t.Fatalf("state=%s calls=%d budget=%d error=%s", m.State, calls, m.Budget.UsedCalls, m.Error)
				}
				if tc.state == "blocked" {
					if time.Since(start) != 5*time.Minute {
						t.Fatalf("deadline=%s", time.Since(start))
					}
					inv, err := s.ListInvocations(m.ID, 0, 100)
					if err != nil || len(inv) != 1 || inv[0].Status != "outcome_unknown" || !m.Budget.UsageUnknown {
						t.Fatal("timeout lost unknown outcome or consumed budget", inv, err)
					}
				}
			})
		})
	}
}
