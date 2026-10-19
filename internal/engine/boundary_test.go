package engine

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestMissionDataCannotWriteIntoSourceRepository(t *testing.T) {
	e, _, _, _, m := setup(t)
	e.options.DataDir = filepath.Join(m.RepoPath, "owned-data")
	if _, err := e.Create(context.Background(), CreateRequest{Title: "source overlap", Goal: "must remain readonly", RepoPath: m.RepoPath, Spec: m.Spec}); err == nil {
		t.Fatal("data directory overlaps source")
	}
	if _, err := os.Stat(e.options.DataDir); !os.IsNotExist(err) {
		t.Fatal("source was modified by rejected create")
	}
}
func TestBudgetCeilingCannotDriftFromApprovedSpec(t *testing.T) {
	e, s, _, _, m := setup(t, research, plan)
	m, err := s.UpdateMission(m.ID, m.Revision, "fixture.drift", func(m *domain.Mission) error { m.Budget.MaxCalls = 100; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("unapproved call limit accepted")
	}
}
func TestPauseCannotClaimStoppedWhenCleanupFails(t *testing.T) {
	e, s, g, r, m := setup(t, research, plan, edit)
	e.Plan(context.Background(), m.ID, m.Revision)
	m = wait(t, s, m.ID, "awaiting_approval")
	g.entered = make(chan struct{}, 1)
	g.release = make(chan struct{})
	r.reconcileErr = errors.New("Docker cleanup unavailable")
	e.Approve(context.Background(), m.ID, m.Revision)
	<-g.entered
	m, _ = s.GetMission(m.ID)
	done := make(chan error, 1)
	go func() { _, err := e.Pause(context.Background(), m.ID, m.Revision); done <- err }()
	wait(t, s, m.ID, "pausing")
	close(g.release)
	err := <-done
	latest, _ := s.GetMission(m.ID)
	if err == nil || latest.State != "blocked" {
		t.Fatalf("cleanup failure claimed stopped: %s %v", latest.State, err)
	}
	used := latest.Budget.UsedCalls
	e.Plan(context.Background(), latest.ID, latest.Revision)
	latest = wait(t, s, latest.ID, "blocked")
	if latest.Budget.UsedCalls != used {
		t.Fatal("replan bought calls before orphan cleanup")
	}
}
