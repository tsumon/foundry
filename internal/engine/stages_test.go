package engine

import (
	"context"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/store"
)

type stageObservingRunner struct {
	*fakeRunner
	store  *store.Store
	stages chan string
}

func (r *stageObservingRunner) Run(ctx context.Context, id, candidate, image, command string) (domain.CommandResult, error) {
	m, err := r.store.GetMission(id)
	if err != nil {
		return domain.CommandResult{}, err
	}
	r.stages <- m.Stage
	return r.fakeRunner.Run(ctx, id, candidate, image, command)
}

func TestLifecycleUsesCanonicalFiveStages(t *testing.T) {
	e, s, _, runner, m := setup(t, research, plan, edit, review, review)
	stages := make(chan string, 1)
	e.runner = &stageObservingRunner{fakeRunner: runner, store: s, stages: stages}
	assertStage := func(m domain.Mission, want string) {
		t.Helper()
		if m.Stage != want {
			t.Errorf("state %s: stage=%q, want %q", m.State, m.Stage, want)
		}
	}
	assertStage(m, "brief")
	m, err := e.Plan(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	assertStage(m, "plan")
	m = wait(t, s, m.ID, "awaiting_approval")
	assertStage(m, "plan")
	m, err = e.Approve(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	assertStage(m, "build")
	m = wait(t, s, m.ID, "awaiting_delivery")
	if got := <-stages; got != "verify" {
		t.Errorf("executing acceptance stage=%q, want verify", got)
	}
	assertStage(m, "deliver")
	m, err = e.Deliver(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	assertStage(m, "deliver")
}
