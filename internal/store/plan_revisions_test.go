package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func planFixture(t *testing.T, s *Store) domain.Mission {
	t.Helper()
	m, e := s.CreateMission(domain.Mission{ID: "plans", State: "awaiting_approval", Epoch: 2, Plan: &domain.Plan{Revision: 7, Summary: "original", Steps: []domain.Step{{ID: "one", Title: "first", Description: "edit", Files: []string{"src/a"}, DependsOn: []string{}, Status: "done"}}}, Approvals: []domain.Approval{{Kind: "snapshot", Digest: "frozen"}}, Budget: domain.Budget{MaxCalls: 16, UsedCalls: 2}})
	if e != nil {
		t.Fatal(e)
	}
	return m
}

func resealPlan(m *domain.Mission) error {
	m.Approvals = append(m.Approvals, domain.Approval{Kind: "plan_proposal", Digest: strings.Repeat("d", 64), Revision: m.Plan.Revision, PlanRevision: m.Plan.Revision})
	return nil
}

// Catches ledger/mission drift, accidental inclusion of execution status, and
// in-place canonicalization of the caller's plan slices.
func TestRevisePlanAppendsCanonicalHistory(t *testing.T) {
	s := openTestStore(t)
	m := planFixture(t, s)
	next := domain.Plan{Revision: 700, Summary: "revised", Steps: []domain.Step{{ID: "two", Title: "second", Description: "more", Files: []string{"src/b"}, DependsOn: []string{}, Status: "done"}}}
	got, record, e := s.RevisePlan(m.ID, m.Revision, 7, "split work", next, resealPlan)
	if e != nil {
		t.Fatal(e)
	}
	if got.Revision != 2 || got.Epoch != 2 || got.Plan.Revision != 8 || got.Plan.Steps[0].Status != "pending" || got.Budget != m.Budget || next.Steps[0].Status != "done" || next.Revision != 700 {
		t.Fatalf("revision or caller mutation: %+v %+v", got, next)
	}
	history, e := s.ListPlanRevisions(m.ID)
	if e != nil || len(history) != 2 {
		t.Fatalf("history=%+v %v", history, e)
	}
	bodies := []string{`{"revision":7,"summary":"original","steps":[{"id":"one","title":"first","description":"edit","depends_on":[],"files":["src/a"],"status":""}]}`, `{"revision":8,"summary":"revised","steps":[{"id":"two","title":"second","description":"more","depends_on":[],"files":["src/b"],"status":""}]}`}
	for i, h := range history {
		raw, e := json.Marshal(h.Plan)
		sum := sha256.Sum256([]byte(bodies[i]))
		if e != nil || string(raw) != bodies[i] || h.PlanDigest != hex.EncodeToString(sum[:]) {
			t.Fatalf("canonical history=%+v raw=%s %v", h, raw, e)
		}
	}
	if history[0].Source != "planner" || history[0].MissionRevision != 1 || record.Source != "user" || record.ParentPlanRevision != 7 || record.MissionRevision != 2 || record.Reason != "split work" || record.CreatedAt == "" {
		t.Fatalf("metadata=%+v", history)
	}
	next.Summary = "third"
	got, _, e = s.RevisePlan(m.ID, got.Revision, 8, "clarify", next, resealPlan)
	if e != nil {
		t.Fatal(e)
	}
	history, e = s.ListPlanRevisions(m.ID)
	if e != nil || len(history) != 3 || history[2].ParentPlanRevision != 8 || got.Plan.Revision != 9 {
		t.Fatalf("second=%+v %v", history, e)
	}
	for _, query := range []string{`UPDATE plan_revisions SET data='{}'`, `DELETE FROM plan_revisions`} {
		if _, e = s.db.Exec(query); e == nil {
			t.Fatal("history rewrite accepted")
		}
	}
	events, e := s.Events(0, 100)
	if e != nil || len(events) != 3 || events[2].Kind != "plan.revised" {
		t.Fatalf("events=%+v %v", events, e)
	}
	if _, e = s.ListPlanRevisions("missing"); !errors.Is(e, ErrNotFound) {
		t.Fatalf("missing history=%v", e)
	}
}

func TestRevisePlanGuardsCASStateFrozenInputsAndNoop(t *testing.T) {
	for _, field := range []string{"mission_revision", "stored_revision", "plan_revision", "state", "scope", "budget", "epoch", "candidate", "approval", "plan", "noop", "reason", "active"} {
		t.Run(field, func(t *testing.T) {
			s := openTestStore(t)
			m := planFixture(t, s)
			expectedMission, expectedPlan := m.Revision, int64(7)
			next := *m.Plan
			next.Summary = "new"
			reason := "reason"
			seal := resealPlan
			switch field {
			case "mission_revision":
				expectedMission++
			case "stored_revision":
				m.Revision = 99
				if _, e := s.db.Exec(`UPDATE missions SET data=json_set(data,'$.revision',99) WHERE id=?`, m.ID); e != nil {
					t.Fatal(e)
				}
			case "plan_revision":
				expectedPlan++
			case "state":
				var e error
				m, e = s.UpdateMission(m.ID, m.Revision, "fixture.running", func(m *domain.Mission) error { m.State = "running"; return nil })
				if e != nil {
					t.Fatal(e)
				}
				expectedMission = m.Revision
			case "scope":
				seal = func(m *domain.Mission) error { m.Spec.Scope = []string{"*"}; return resealPlan(m) }
			case "budget":
				seal = func(m *domain.Mission) error { m.Budget.UsedCalls++; return resealPlan(m) }
			case "epoch":
				seal = func(m *domain.Mission) error { m.Epoch++; return resealPlan(m) }
			case "candidate":
				seal = func(m *domain.Mission) error { m.CandidateDigest = "changed"; return resealPlan(m) }
			case "approval":
				seal = func(m *domain.Mission) error { m.Approvals[0].Digest = "changed"; return resealPlan(m) }
			case "plan":
				seal = func(m *domain.Mission) error { m.Plan.Summary = "changed in callback"; return resealPlan(m) }
			case "noop":
				next.Summary = m.Plan.Summary
			case "reason":
				reason = "  "
			case "active":
				_, e := s.db.Exec(`INSERT INTO local_operations(id,mission_id,intent,status,updated_at) VALUES(?,?,'{}','pending','fixture')`, operationID, m.ID)
				if e != nil {
					t.Fatal(e)
				}
			}
			if _, _, e := s.RevisePlan(m.ID, expectedMission, expectedPlan, reason, next, seal); e == nil {
				t.Fatal("invalid revision accepted")
			}
			got, e := s.GetMission(m.ID)
			if e != nil || !reflect.DeepEqual(got, m) {
				t.Fatalf("mission changed: %+v %v", got, e)
			}
			history, e := s.ListPlanRevisions(m.ID)
			if e != nil || len(history) != 0 {
				t.Fatalf("history changed=%+v %v", history, e)
			}
		})
	}
}

func TestRevisePlanRollback(t *testing.T) {
	for _, boundary := range []string{"insert", "event", "commit"} {
		t.Run(boundary, func(t *testing.T) {
			s := openTestStore(t)
			m := planFixture(t, s)
			next := *m.Plan
			next.Summary = "new"
			switch boundary {
			case "insert":
				_, e := s.db.Exec(`CREATE TRIGGER fixture_insert BEFORE INSERT ON plan_revisions WHEN NEW.plan_revision=8 BEGIN SELECT RAISE(ABORT,'fixture insert failure'); END`)
				if e != nil {
					t.Fatal(e)
				}
			case "event":
				_, e := s.db.Exec(`CREATE TRIGGER fixture_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'fixture event failure'); END`)
				if e != nil {
					t.Fatal(e)
				}
			case "commit":
				installDeferredFailure(t, s, "plan_revisions")
			}
			if _, _, e := s.RevisePlan(m.ID, m.Revision, 7, "reason", next, resealPlan); e == nil {
				t.Fatal("failure accepted")
			}
			got, e := s.GetMission(m.ID)
			if e != nil || !reflect.DeepEqual(got, m) {
				t.Fatalf("mission changed=%+v %v", got, e)
			}
			history, e := s.ListPlanRevisions(m.ID)
			if e != nil || len(history) != 0 {
				t.Fatalf("history changed=%+v %v", history, e)
			}
			events, e := s.Events(0, 100)
			if e != nil || len(events) != 1 {
				t.Fatalf("events changed=%+v %v", events, e)
			}
		})
	}
}

func TestRevisePlanRefusesMismatchedExistingHistory(t *testing.T) {
	s := openTestStore(t)
	m := planFixture(t, s)
	bad := domain.PlanRevision{MissionID: m.ID, PlanRevision: 7, Epoch: m.Epoch, Plan: domain.Plan{Revision: 7, Summary: "forged"}, PlanDigest: strings.Repeat("0", 64)}
	raw, e := json.Marshal(bad)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`INSERT INTO plan_revisions VALUES(?,?,?)`, m.ID, 7, raw); e != nil {
		t.Fatal(e)
	}
	next := *m.Plan
	next.Summary = "new"
	if _, _, e = s.RevisePlan(m.ID, m.Revision, 7, "reason", next, resealPlan); e == nil {
		t.Fatal("mismatched history accepted")
	}
	got, e := s.GetMission(m.ID)
	if e != nil || !reflect.DeepEqual(got, m) {
		t.Fatal("mismatched history mutated mission")
	}
}
