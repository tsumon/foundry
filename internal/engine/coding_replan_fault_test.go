package engine

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"testing"
)

func TestCodingReplanExhaustedBudgetDoesNotReset(t *testing.T) {
	e, m := failedCodingRun(t)
	m, err := e.s.UpdateMission(m.ID, m.Revision, "fixture.exhausted", func(m *domain.Mission) error { m.Budget.UsedCalls = 16; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		finishCoding(e)
		t.Fatal("exhausted replan reset the run")
	}
	got, _ := e.s.GetMission(m.ID)
	if *got.CandidateGeneration != *m.CandidateGeneration || len(got.RunArchives) != 0 {
		t.Fatal("exhausted budget mutated run")
	}
}

func TestCodingReplanFreezeResetFaultsAreAtomic(t *testing.T) {
	for _, condition := range []string{`BEFORE UPDATE ON local_operations WHEN NEW.status='prepared'`, `BEFORE INSERT ON events WHEN NEW.kind='mission.planning'`} {
		t.Run(condition, func(t *testing.T) {
			e, m := failedCodingRun(t)
			db := failureDB(t, e)
			if _, err := db.Exec(`CREATE TRIGGER replan_failure ` + condition + ` BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
				t.Fatal(err)
			}
			_, err := e.Plan(context.Background(), m.ID, m.Revision)
			if err == nil {
				finishCoding(e)
				t.Fatal("fault did not stop reset")
			}
			got, _ := e.s.GetMission(m.ID)
			if *got.CandidateGeneration != *m.CandidateGeneration || got.ReplanContext != nil || len(got.RunArchives) != 0 || got.Budget != m.Budget {
				t.Fatal("partial reset committed")
			}
			if _, err = db.Exec(`DROP TRIGGER replan_failure`); err != nil {
				t.Fatal(err)
			}
			restart := New(e.s, e.gateway, Options{DataDir: e.options.DataDir, Runner: e.runner})
			if err = restart.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ = e.s.GetMission(m.ID)
			if *got.CandidateGeneration != *m.CandidateGeneration || got.Budget != m.Budget {
				t.Fatal("recovery replayed")
			}
		})
	}
}

func TestCodingReplanTamperingFailsClosed(t *testing.T) {
	for _, kind := range []string{"bundle", "archive", "candidate", "missing", "version", "foreign", "source"} {
		t.Run(kind, func(t *testing.T) {
			e, m := failedCodingRun(t)
			e.gateway = &failurePlanner{candidate: m.CandidateGeneration.ID}
			if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = e.s.GetMission(m.ID)
			switch kind {
			case "bundle":
				m.ReplanContext.Failures[0].Summary = "changed"
			case "archive":
				m.RunArchives[0].Evidence[0].Results[0].Output = "changed"
			case "candidate":
				m.ReplanContext.CandidateGeneration.ID = "foreign"
			case "missing":
				m.ReplanContext = nil
			case "version":
				m.ReplanContext.SchemaVersion = 2
			case "foreign":
				m.ReplanContext.MissionID = "other"
			case "source":
				m.ReplanContext.Failures[0].VerificationID = "foreign"
			}
			raw, _ := json.Marshal(m)
			db := failureDB(t, e)
			if _, err := db.Exec(`UPDATE missions SET data=? WHERE id=?`, raw, m.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Approve(context.Background(), m.ID, m.Revision); err == nil {
				t.Fatal("tampered material approved")
			}
			got, _ := e.s.GetMission(m.ID)
			if got.Budget != m.Budget || got.State != "awaiting_approval" {
				t.Fatal("tampering caused dispatch")
			}
		})
	}
}

func TestCodingReplanAfterEpochFencedPauseRetainsFailureRun(t *testing.T) {
	e, m := failedCodingRun(t)
	// Recovery/pause fencing advances the mission epoch, not the executed run.
	m, err := e.s.UpdateMission(m.ID, m.Revision, "fixture.paused", func(m *domain.Mission) error { m.Epoch++; m.State = "paused"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	e.gateway = &failurePlanner{candidate: m.CandidateGeneration.ID}
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	got, _ := e.s.GetMission(m.ID)
	if got.State != "awaiting_approval" || got.ReplanContext == nil {
		t.Fatal("fenced failure lost")
	}
}

func TestCodingReplanLatePlannerAndRepeatedCASNeverReplay(t *testing.T) {
	e, m := failedCodingRun(t)
	g := &scripted{outputs: []string{plan}, entered: make(chan struct{}, 1), release: make(chan struct{})}
	e.gateway = g
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	<-g.entered
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("repeated request accepted")
	}
	current, _ := e.s.GetMission(m.ID)
	paused := make(chan error, 1)
	go func() { _, err := e.Pause(context.Background(), m.ID, current.Revision); paused <- err }()
	wait(t, e.s, m.ID, "pausing")
	close(g.release)
	if err := <-paused; err != nil {
		t.Fatal(err)
	}
	got, _ := e.s.GetMission(m.ID)
	if got.Plan != nil || got.ReplanContext == nil || got.Budget.UsedCalls != m.Budget.UsedCalls+1 {
		t.Fatal("late planner changed proposal")
	}
	restart := New(e.s, g, Options{DataDir: e.options.DataDir, Runner: e.runner})
	if err := restart.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(g.roles) != 1 {
		t.Fatal("planner replayed")
	}
}

func TestCodingReplanFreezeRejectsMaterialChangedBeforeSelection(t *testing.T) {
	e, m := failedCodingRun(t)
	db := failureDB(t, e)
	if _, err := db.Exec(`CREATE TRIGGER replan_change AFTER UPDATE ON local_operations WHEN NEW.status='prepared' BEGIN UPDATE missions SET data=json_set(data,'$.evidence[0].results[0].output','changed after freeze') WHERE id=NEW.mission_id; END`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		finishCoding(e)
		t.Fatal("stale freeze overwrote changed failure material")
	}
	got, _ := e.s.GetMission(m.ID)
	if *got.CandidateGeneration != *m.CandidateGeneration || len(got.RunArchives) != 0 {
		t.Fatal("stale freeze selected")
	}
}
