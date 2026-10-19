package engine

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/store"
	"testing"
)

func TestControlledRecoveryDoesNotRewriteFutureWorkspace(t *testing.T) {
	e, s, _, _, m := controlledSetup(t)
	m.ID = "future-controlled"
	m.WorkspaceMode = "future-generations"
	m.State = "running"
	m = seedPersistedMission(t, e, m)
	var err error
	before, _ := json.Marshal(m)
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(got)
	if string(before) != string(after) {
		t.Fatal("unsupported workspace was rewritten")
	}
}

// Durable crash fixtures use real reservations/claims, without a live provider.
func unfinishedMainFixture(t *testing.T, e *Engine, s *store.Store, m domain.Mission, state string, claimed bool) (domain.Mission, domain.Invocation) {
	t.Helper()
	var err error
	m, err = e.resetGeneration(m)
	if err != nil {
		t.Fatal(err)
	}
	m, err = e.grantPlanningRead(m)
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.UpdateMission(m.ID, m.Revision, "fixture.idle", func(current *domain.Mission) error { current.State = state; return nil })
	if err != nil {
		t.Fatal(err)
	}
	op := &operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}
	m, binding, auth, err := e.controlledAuthority(op, domain.Main)
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.invocationIntent(m, domain.Main, "model", "model.complete", binding, auth)
	if err != nil {
		t.Fatal(err)
	}
	v.Input, err = e.seal(v, "model_input", []byte(`{}`), "application/json", false)
	if err != nil {
		t.Fatal(err)
	}
	m, v, err = s.BeginInvocation(v, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		v, err = s.ClaimInvocation(v.ID, v.Epoch)
		if err != nil {
			t.Fatal(err)
		}
	}
	return m, v
}

func TestControlledRecoveryFencesInterruptedIdleMain(t *testing.T) {
	for _, state := range []string{"awaiting_approval", "awaiting_delivery", "paused", "blocked", "delivered"} {
		for _, claimed := range []bool{false, true} {
			t.Run(state+map[bool]string{false: "/pending", true: "/claimed"}[claimed], func(t *testing.T) {
				e, s, _, _, m := controlledSetup(t)
				m, v := unfinishedMainFixture(t, e, s, m, state, claimed)
				if err := e.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
				got, err := s.GetMission(m.ID)
				if err != nil || got.State != "blocked" || got.Epoch != m.Epoch+1 {
					t.Fatalf("idle Main not fenced: state=%s epoch=%d before=%d err=%v", got.State, got.Epoch, m.Epoch, err)
				}
				call, err := s.GetInvocation(v.ID)
				want := "interrupted_before_dispatch"
				if claimed {
					want = "outcome_unknown"
				}
				if err != nil || call.Status != want || call.Result != nil {
					t.Fatal(call, err)
				}
			})
		}
	}
}

func TestControlledRecoveryPreservesFutureInvocationRows(t *testing.T) {
	for _, kind := range []string{"pending", "claimed", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			e, s, _, _, m := controlledSetup(t)
			m, v := unfinishedMainFixture(t, e, s, m, "awaiting_approval", kind == "claimed")
			db := fixtureSQL(t, e)
			if _, err := db.Exec(`DROP TRIGGER immutable_workspace_mode`); err != nil {
				t.Fatal(err)
			}
			_, err := db.Exec(`UPDATE missions SET data=json_set(data,'$.workspace_mode','future-generations') WHERE id=?`, m.ID)
			if err != nil {
				t.Fatal(err)
			}
			m, err = s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "corrupt" {
				if _, err = db.Exec(`DROP TRIGGER invocation_immutable_intent; DROP TRIGGER invocation_transition; UPDATE invocations SET intent='{}' WHERE id=?`, v.ID); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := json.Marshal(m)
			var beforeRow, afterRow string
			query := `SELECT json_array(id,mission_id,epoch,session_id,ordinal,kind,role,name,binding_digest,authority_digest,grant_id,hex(intent),status,hex(result),error_code,reserved_result_bytes,created_at,dispatched_at,finished_at,usage_known,input_tokens,output_tokens,(SELECT count(*) FROM invocation_transitions WHERE invocation_id=?)) FROM invocations WHERE id=?`
			if err = db.QueryRow(query, v.ID, v.ID).Scan(&beforeRow); err != nil {
				t.Fatal(err)
			}
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(got)
			if err = db.QueryRow(query, v.ID, v.ID).Scan(&afterRow); err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) || beforeRow != afterRow {
				t.Fatalf("unsupported mission/invocation rewritten: missionEqual=%t rowEqual=%t", string(before) == string(after), beforeRow == afterRow)
			}
		})
	}
}

func TestControlledRecoveryFencesSQLIntentsBeforeCorruptionIsolation(t *testing.T) {
	for _, state := range []string{"planning", "awaiting_approval"} {
		t.Run(state, func(t *testing.T) {
			e, s, _, _, m := controlledSetup(t)
			m, v := unfinishedMainFixture(t, e, s, m, state, true)
			missions, err := s.ListMissions()
			if err != nil {
				t.Fatal(err)
			}
			var other domain.Mission
			for _, candidate := range missions {
				if candidate.ID != m.ID {
					other = candidate
				}
			}
			beforeOther, _ := json.Marshal(other)
			db := fixtureSQL(t, e)
			if _, err = db.Exec(`DROP TRIGGER invocation_immutable_intent; DROP TRIGGER invocation_transition; UPDATE invocations SET intent=json_set(intent,'$.mission_id',?) WHERE id=?`, other.ID, v.ID); err != nil {
				t.Fatal(err)
			}
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetMission(m.ID)
			if err != nil || got.State != "blocked" || got.Epoch != m.Epoch+1 {
				t.Fatalf("corrupt SQL owner not fenced: %+v %v", got, err)
			}
			other, err = s.GetMission(other.ID)
			if err != nil {
				t.Fatal(err)
			}
			afterOther, _ := json.Marshal(other)
			if string(beforeOther) != string(afterOther) {
				t.Fatal("untrusted intent ownership crossed mission boundary")
			}
		})
	}
}

func TestControlledPauseLateResponseThenReplan(t *testing.T) {
	e, s, g, _, m := controlledSetup(t, controlledFinal, controlledRead, controlledFinal, plan)
	g.entered = make(chan struct{}, 1)
	g.release = make(chan struct{})
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	<-g.entered
	m, _ = s.GetMission(m.ID)
	done := make(chan error, 1)
	go func() { _, err := e.Pause(context.Background(), m.ID, m.Revision); done <- err }()
	wait(t, s, m.ID, "pausing")
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	calls, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(calls) != 1 || calls[0].Status != "outcome_unknown" || calls[0].Result != nil || m.Research != nil || m.Plan != nil || m.State != "paused" {
		t.Fatal(m.State, m.Error, calls, err)
	}
	g.entered = nil
	g.release = nil
	if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	if m.State != "awaiting_approval" || m.Budget.UsedCalls != 4 || len(m.RunArchives) != 1 {
		t.Fatal("stale active row blocked replan", m.State, m.Error)
	}
}

func TestControlledRecoveryFencesClaimsAndNeverReplays(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "claimed"}[claimed], func(t *testing.T) {
			e, s, g, _, m := controlledSetup(t)
			var err error
			m, err = e.resetGeneration(m)
			if err != nil {
				t.Fatal(err)
			}
			m, err = e.grantPlanningRead(m)
			if err != nil {
				t.Fatal(err)
			}
			op := &operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}
			m, binding, auth, err := e.controlledAuthority(op, domain.Researcher)
			if err != nil {
				t.Fatal(err)
			}
			v, err := e.invocationIntent(m, domain.Researcher, "model", "model.complete", binding, auth)
			if err != nil {
				t.Fatal(err)
			}
			v.Input, err = e.seal(v, "model_input", []byte(`{}`), "application/json", false)
			if err != nil {
				t.Fatal(err)
			}
			_, v, err = s.BeginInvocation(v, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if claimed {
				if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
					t.Fatal(err)
				}
			}
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetMission(m.ID)
			call, err := s.GetInvocation(v.ID)
			want := "interrupted_before_dispatch"
			if claimed {
				want = "outcome_unknown"
			}
			if err != nil || got.State != "blocked" || got.Epoch <= m.Epoch || call.Status != want || len(g.roles) != 0 {
				t.Fatal(got.State, got.Epoch, call.Status, err)
			}
		})
	}
}

func TestControlledUnknownModelTransportConsumesReservation(t *testing.T) {
	e, s, _, _, m := controlledSetup(t)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	calls, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(calls) != 1 || calls[0].Status != "outcome_unknown" || m.Budget.UsedCalls != 1 || !m.Budget.UsageUnknown {
		t.Fatal(m.Budget, calls, err)
	}
}
