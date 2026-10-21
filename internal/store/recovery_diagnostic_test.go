package store

import (
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"reflect"
	"strings"
	"testing"
)

func TestRecoveryDiagnosticStoreBoundsAndIdentity(t *testing.T) {
	for _, kind := range []string{"mission_identity", "mission_revision", "operation_identity", "operation_sql_owner", "operation_intent_bound", "operation_result_bound"} {
		t.Run(kind, func(t *testing.T) {
			s := openTestStore(t)
			m, op, result := operationFixture(t, s)
			if _, err := s.BeginLocalOperation(op); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PrepareLocalOperation(op.ID, result); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RecoveryMission(m.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.LatestLocalOperation(m.ID); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "mission_identity":
				_, err = s.db.Exec(`UPDATE missions SET data=json_set(data,'$.id','foreign') WHERE id=?`, m.ID)
			case "mission_revision":
				_, err = s.db.Exec(`UPDATE missions SET revision=revision+1 WHERE id=?`, m.ID)
			default:
				if _, err = s.db.Exec(`DROP TRIGGER immutable_operation_intent; DROP TRIGGER operation_transition`); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "operation_identity":
					_, err = s.db.Exec(`UPDATE local_operations SET intent=json_set(intent,'$.mission_id','foreign') WHERE id=?`, op.ID)
				case "operation_sql_owner":
					if _, err = s.CreateMission(domain.Mission{ID: "foreign"}); err != nil {
						t.Fatal(err)
					}
					_, err = s.db.Exec(`UPDATE local_operations SET mission_id='foreign' WHERE id=?`, op.ID)
				case "operation_intent_bound":
					_, err = s.db.Exec(`UPDATE local_operations SET intent=json_set(intent,'$.padding',?) WHERE id=?`, strings.Repeat("x", 16<<20), op.ID)
				case "operation_result_bound":
					_, err = s.db.Exec(`UPDATE local_operations SET result=json_set(result,'$.padding',?) WHERE id=?`, strings.Repeat("x", 32<<20), op.ID)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(kind, "mission_") {
				_, err = s.RecoveryMission(m.ID)
			} else if kind == "operation_sql_owner" {
				_, err = s.RecoveryLocalOperation(op.ID)
			} else {
				_, err = s.LatestLocalOperation(m.ID)
			}
			if err == nil {
				t.Fatal("corrupt or oversized record accepted")
			}
		})
	}
}

func TestRecoveryDiagnosticInvocationReadNeverTransitions(t *testing.T) {
	s := openTestStore(t)
	m, digest := controlledStoreMission(t, s, "diagnostic")
	m, grant := controlledGrant(t, s, m, digest)
	m, v, err := s.BeginInvocation(modelInvocation("inv-diagnostic", m, grant, digest), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.RecoveryInvocations(m.ID)
	if err != nil || len(rows) != 1 || rows[0].Status != "authorized_pending" {
		t.Fatal(rows, err)
	}
	before, _ := json.Marshal(rows)
	again, err := s.RecoveryInvocations(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(again)
	if string(before) != string(after) {
		t.Fatal("diagnosis changed invocation")
	}
	if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	rows, err = s.RecoveryInvocations(m.ID)
	if err != nil || rows[0].Status != "dispatching" {
		t.Fatal(rows, err)
	}
	stored, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || !reflect.DeepEqual(rows, stored) {
		t.Fatal(err, "read projection lost immutable evidence")
	}
	if _, err = s.db.Exec(`DROP TRIGGER invocation_immutable_intent; DROP TRIGGER invocation_transition`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE invocations SET intent=json_set(intent,'$.padding',?) WHERE id=?`, strings.Repeat("x", 262144), v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecoveryInvocations(m.ID); err == nil {
		t.Fatal("oversized invocation accepted")
	}
	if _, err = s.RecoveryMission("absent"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
