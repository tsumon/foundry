package engine

import (
	"context"
	"testing"
	"time"
)

type cleanupGate struct {
	*fakeRunner
	engine  *Engine
	entered chan bool
	release chan struct{}
}

func (r *cleanupGate) Reconcile(context.Context) error {
	// This observation is ordered after the worker finalizer and before cleanup
	// returns; the cleanup owner must still hold the controller's active slot.
	r.entered <- r.engine.active != nil
	<-r.release
	return nil
}

func TestPauseRetainsSlotThroughCleanup(t *testing.T) {
	e, s, g, r, old := setup(t, research, plan, edit, research, plan)
	next, err := e.Create(context.Background(), CreateRequest{Title: "next", Goal: "next mission", RepoPath: old.RepoPath, Spec: old.Spec})
	if err != nil {
		t.Fatal(err)
	}
	e.Plan(context.Background(), old.ID, old.Revision)
	old = wait(t, s, old.ID, "awaiting_approval")
	g.entered = make(chan struct{}, 1)
	g.release = make(chan struct{})
	gate := &cleanupGate{fakeRunner: r, engine: e, entered: make(chan bool, 1), release: make(chan struct{})}
	e.runner = gate
	if _, err = e.Approve(context.Background(), old.ID, old.Revision); err != nil {
		t.Fatal(err)
	}
	<-g.entered
	old, _ = s.GetMission(old.ID)
	paused := make(chan error, 1)
	go func() { _, err := e.Pause(context.Background(), old.ID, old.Revision); paused <- err }()
	wait(t, s, old.ID, "pausing")
	close(g.release)
	if held := <-gate.entered; !held {
		t.Error("pause released serialization slot before owner-wide cleanup")
	}
	started := make(chan error, 1)
	go func() { _, err := e.Plan(context.Background(), next.ID, next.Revision); started <- err }()
	select {
	case err := <-started:
		if err == nil {
			t.Error("next mission started during pause cleanup")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("start was not rejected while cleanup held the active slot")
	}
	close(gate.release)
	if err := <-paused; err != nil {
		t.Fatal(err)
	}
	wait(t, s, old.ID, "paused")
	latest, _ := s.GetMission(next.ID)
	if latest.State != "draft" {
		t.Errorf("concurrent start escaped pause serialization: %s", latest.State)
	}
}

func TestSecondPauseCannotClobberFirst(t *testing.T) {
	e, s, g, _, m := setup(t, research, plan)
	g.entered = make(chan struct{}, 1)
	g.release = make(chan struct{})
	e.Plan(context.Background(), m.ID, m.Revision)
	<-g.entered
	m, _ = s.GetMission(m.ID)
	first := make(chan error, 1)
	go func() { _, err := e.Pause(context.Background(), m.ID, m.Revision); first <- err }()
	pausing := wait(t, s, m.ID, "pausing")
	second := make(chan error, 1)
	go func() { _, err := e.Pause(context.Background(), m.ID, pausing.Revision); second <- err }()
	var secondErr error
	returned := false
	select {
	case secondErr = <-second:
		returned = true
	case <-time.After(200 * time.Millisecond):
	}
	close(g.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if !returned {
		secondErr = <-second
	}
	if secondErr == nil {
		t.Error("second pause accepted ownership and finalized the same operation twice")
	}
	latest, _ := s.GetMission(m.ID)
	if latest.State != "paused" || latest.Epoch != pausing.Epoch {
		t.Errorf("duplicate pause changed final state/epoch: %+v", latest)
	}
}
