package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func controlledStoreMission(t *testing.T, s *Store, id string) (domain.Mission, string) {
	t.Helper()
	bindings := domain.TaskBindings{
		Git:           domain.GitSourceBinding{RepositoryID: "repo", RepositoryRevision: 1, ObservationID: "obs", Root: "/tmp/repo", BaseCommit: strings.Repeat("a", 40)},
		PolicyVersion: "repo_read_v1",
		Tools:         []string{"repo.list", "repo.read", "repo.search"},
		Budget:        domain.FixedReadBudget(),
	}
	digest, err := domain.TaskBindingDigest(bindings)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.CreateMission(domain.Mission{ID: id, WorkspaceMode: domain.WorkspaceModeControlledReadV1, ToolMode: domain.ToolModeControlledReadV1, TaskBindings: &bindings, State: "planning", Stage: "plan", Epoch: 1, Budget: domain.Budget{MaxCalls: 16}})
	if err != nil {
		t.Fatal(err)
	}
	return m, digest
}

func controlledEnvelope(kind, invocationID, missionID, bindingDigest, body string) domain.EvidenceEnvelope {
	sum := sha256.Sum256([]byte(body))
	return domain.EvidenceEnvelope{SchemaVersion: 1, Kind: kind, InvocationID: invocationID, MissionID: missionID, Epoch: 1, Producer: "controller", CapturedAt: "2026-10-18T00:00:00Z", Digest: hex.EncodeToString(sum[:]), Bytes: int64(len(body)), ContentType: "application/json", BindingDigest: bindingDigest}
}

func controlledGrant(t *testing.T, s *Store, m domain.Mission, bindingDigest string) (domain.Mission, domain.ReadGrant) {
	t.Helper()
	g := domain.ReadGrant{ID: "grant-" + m.ID, MissionID: m.ID, Epoch: m.Epoch, SessionID: "research-session", Role: domain.Researcher, RequiredState: "planning", BindingDigest: bindingDigest, AuthorityDigest: strings.Repeat("b", 64)}
	next, saved, err := s.CreateReadGrant(g, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return next, saved
}

func repositoryInvocation(id string, m domain.Mission, grant domain.ReadGrant, bindingDigest string) domain.Invocation {
	return domain.Invocation{ID: id, MissionID: m.ID, Epoch: m.Epoch, SessionID: "research-session", Kind: "repository", Role: domain.Researcher, Name: "repo.read", BindingDigest: bindingDigest, AuthorityDigest: grant.AuthorityDigest, GrantID: grant.ID, Input: controlledEnvelope("model_input", id, m.ID, bindingDigest, `{}`)}
}

func modelInvocation(id string, m domain.Mission, grant domain.ReadGrant, bindingDigest string) domain.Invocation {
	return domain.Invocation{ID: id, MissionID: m.ID, Epoch: m.Epoch, SessionID: "research-session", Kind: "model", Role: domain.Researcher, Name: "model.complete", BindingDigest: bindingDigest, AuthorityDigest: grant.AuthorityDigest, GrantID: grant.ID, Input: controlledEnvelope("model_input", id, m.ID, bindingDigest, `{}`)}
}

// Removing the replay check would reserve quota and emit a second mission update.
func TestBeginInvocationDuplicateReservesOnceAndChangedIntentConflicts(t *testing.T) {
	s := openTestStore(t)
	m, bindingDigest := controlledStoreMission(t, s, "duplicate")
	m, grant := controlledGrant(t, s, m, bindingDigest)
	v := modelInvocation("inv-duplicate", m, grant, bindingDigest)

	reserved, first, err := s.BeginInvocation(v, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if first.Ordinal != 1 || first.ReservedResultBytes != 0 || reserved.Budget.UsedCalls != 1 || !reserved.Budget.UsageUnknown {
		t.Fatalf("first reservation = mission %+v invocation %+v", reserved, first)
	}
	replayedMission, replayed, err := s.BeginInvocation(v, m.Revision)
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if replayed.ID != first.ID || replayed.Ordinal != first.Ordinal || replayedMission.Revision != reserved.Revision || replayedMission.Budget.UsedCalls != 1 {
		t.Fatalf("replay created new reservation: mission=%+v invocation=%+v", replayedMission, replayed)
	}
	rows, err := s.ListInvocations(m.ID, 0, 30)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}

	changed := v
	changed.Input.Digest = strings.Repeat("c", 64)
	if _, _, err := s.BeginInvocation(changed, reserved.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed intent error = %v, want ErrConflict", err)
	}
	forged := v
	forged.Status = "succeeded"
	if _, _, err := s.BeginInvocation(forged, reserved.Revision); err == nil {
		t.Fatal("duplicate request accepted caller-provided terminal state")
	}
}

// Changing ClaimInvocation to accept dispatching rows permits a second executor attempt.
func TestClaimInvocationOnlyOnce(t *testing.T) {
	s := openTestStore(t)
	m, bindingDigest := controlledStoreMission(t, s, "claim")
	m, grant := controlledGrant(t, s, m, bindingDigest)
	m, v, err := s.BeginInvocation(repositoryInvocation("inv-claim", m, grant, bindingDigest), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimInvocation(v.ID, m.Epoch)
	if err != nil || claimed.Status != "dispatching" || claimed.DispatchedAt == "" {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if _, err := s.ClaimInvocation(v.ID, m.Epoch); !errors.Is(err, ErrConflict) {
		t.Fatalf("second claim error = %v, want ErrConflict", err)
	}
}

func TestStaleClaimCanOnlyFinalizeUnknownWithoutResultOrRefund(t *testing.T) {
	s := openTestStore(t)
	m, binding := controlledStoreMission(t, s, "stale-finalize")
	m, g := controlledGrant(t, s, m, binding)
	m, v, err := s.BeginInvocation(modelInvocation("stale-call", m, g, binding), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	m, err = s.UpdateMission(m.ID, m.Revision, "fixture.pause", func(m *domain.Mission) error { m.Epoch++; m.State = "paused"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	result := controlledEnvelope("model_output", v.ID, m.ID, binding, `{}`)
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "succeeded", &result, "", domain.CallUsage{}); err == nil {
		t.Fatal("late output attached")
	}
	final, err := s.FinishInvocation(v.ID, v.Epoch, "outcome_unknown", nil, "stale_dispatch", domain.CallUsage{})
	if err != nil || final.Status != "outcome_unknown" || final.Result != nil {
		t.Fatal(final, err)
	}
	m, _ = s.GetMission(m.ID)
	if m.Budget.UsedCalls != 1 || m.State != "paused" {
		t.Fatal("finalization changed authority/refunded")
	}
}

func TestControlledRolePhaseAllowsRepairAndAuthorizedIdleMain(t *testing.T) {
	for _, tc := range []struct {
		role  domain.Role
		state string
	}{{domain.Debugger, "verifying"}, {domain.Main, "paused"}, {domain.Main, "blocked"}, {domain.Main, "delivered"}} {
		t.Run(string(tc.role)+tc.state, func(t *testing.T) {
			s := openTestStore(t)
			m, binding := controlledStoreMission(t, s, "role-phase")
			m, err := s.UpdateMission(m.ID, m.Revision, "fixture.phase", func(m *domain.Mission) error { m.State = tc.state; return nil })
			if err != nil {
				t.Fatal(err)
			}
			v := modelInvocation("phase-call", m, domain.ReadGrant{AuthorityDigest: strings.Repeat("b", 64)}, binding)
			v.Role = tc.role
			v.GrantID = ""
			if _, _, err = s.BeginInvocation(v, m.Revision); err != nil {
				t.Fatal("legitimate phase rejected", err)
			}
		})
	}
}

// Finish must bind evidence/usage atomically and refuse a result after pause wins the race.
func TestFinishInvocationPreservesUsageAndRejectsStaleState(t *testing.T) {
	t.Run("known usage", func(t *testing.T) {
		s := openTestStore(t)
		m, bindingDigest := controlledStoreMission(t, s, "finish-usage")
		m, grant := controlledGrant(t, s, m, bindingDigest)
		m, v, err := s.BeginInvocation(modelInvocation("inv-finish", m, grant, bindingDigest), m.Revision)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ClaimInvocation(v.ID, m.Epoch); err != nil {
			t.Fatal(err)
		}
		result := controlledEnvelope("model_output", v.ID, m.ID, bindingDigest, `{"kind":"final"}`)
		finished, err := s.FinishInvocation(v.ID, m.Epoch, "succeeded", &result, "", domain.CallUsage{UsageKnown: true, InputTokens: 11, OutputTokens: 7})
		if err != nil {
			t.Fatal(err)
		}
		if finished.Status != "succeeded" || finished.Result == nil || !finished.UsageKnown || finished.InputTokens != 11 || finished.OutputTokens != 7 {
			t.Fatalf("finished invocation = %+v", finished)
		}
		stored, err := s.GetMission(m.ID)
		if err != nil || stored.Budget.InputTokens != 11 || stored.Budget.OutputTokens != 7 || stored.Budget.UsageUnknown {
			t.Fatalf("finished budget = %+v, %v", stored.Budget, err)
		}
	})

	t.Run("pause wins", func(t *testing.T) {
		s := openTestStore(t)
		m, bindingDigest := controlledStoreMission(t, s, "finish-paused")
		m, grant := controlledGrant(t, s, m, bindingDigest)
		m, v, err := s.BeginInvocation(repositoryInvocation("inv-paused", m, grant, bindingDigest), m.Revision)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ClaimInvocation(v.ID, m.Epoch); err != nil {
			t.Fatal(err)
		}
		current, err := s.GetMission(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.UpdateMission(m.ID, current.Revision, "mission.paused", func(m *domain.Mission) error { m.State = "paused"; return nil }); err != nil {
			t.Fatal(err)
		}
		result := controlledEnvelope("tool_observation", v.ID, m.ID, bindingDigest, `{"path":"src/a.ts"}`)
		if _, err = s.FinishInvocation(v.ID, m.Epoch, "succeeded", &result, "", domain.CallUsage{}); !errors.Is(err, ErrConflict) {
			t.Fatalf("late finish error = %v, want ErrConflict", err)
		}
		stored, err := s.GetInvocation(v.ID)
		if err != nil || stored.Status != "dispatching" || stored.Result != nil {
			t.Fatalf("late result leaked: %+v %v", stored, err)
		}
	})
}

// A caller retry after a durable finish must be a readback, never a second finish.
func TestFinishInvocationExactTerminalRetryIsIdempotentAndConflictsDoNotOverwrite(t *testing.T) {
	s := openTestStore(t)
	m, bindingDigest := controlledStoreMission(t, s, "finish-retry")
	m, grant := controlledGrant(t, s, m, bindingDigest)
	m, v, err := s.BeginInvocation(modelInvocation("inv-finish-retry", m, grant, bindingDigest), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(v.ID, m.Epoch); err != nil {
		t.Fatal(err)
	}
	result := controlledEnvelope("model_output", v.ID, m.ID, bindingDigest, `{"kind":"final"}`)
	usage := domain.CallUsage{UsageKnown: true, InputTokens: 11, OutputTokens: 7}
	finished, err := s.FinishInvocation(v.ID, m.Epoch, "succeeded", &result, "", usage)
	if err != nil {
		t.Fatal(err)
	}
	missionAfterFinish, err := s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	eventsAfterFinish, err := s.Events(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var transitionsAfterFinish int
	if err = s.db.QueryRow(`SELECT count(*) FROM invocation_transitions WHERE invocation_id=?`, v.ID).Scan(&transitionsAfterFinish); err != nil {
		t.Fatal(err)
	}

	replayed, err := s.FinishInvocation(v.ID, m.Epoch, "succeeded", &result, "", usage)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, finished) {
		t.Fatalf("exact retry changed terminal invocation:\nfirst=%+v\nretry=%+v", finished, replayed)
	}

	changedResult := result
	changedResult.Digest = strings.Repeat("c", 64)
	for _, conflict := range []struct {
		name, status, safeError string
		result                  *domain.EvidenceEnvelope
		usage                   domain.CallUsage
	}{
		{name: "result", status: "succeeded", result: &changedResult, usage: usage},
		{name: "usage", status: "succeeded", result: &result, usage: domain.CallUsage{UsageKnown: true, InputTokens: 12, OutputTokens: 7}},
		{name: "status", status: "outcome_unknown", safeError: "retry_conflict", usage: domain.CallUsage{}},
	} {
		t.Run(conflict.name, func(t *testing.T) {
			if _, finishErr := s.FinishInvocation(v.ID, m.Epoch, conflict.status, conflict.result, conflict.safeError, conflict.usage); !errors.Is(finishErr, ErrConflict) {
				t.Fatalf("conflicting retry error = %v, want ErrConflict", finishErr)
			}
		})
	}

	stored, err := s.GetInvocation(v.ID)
	if err != nil || !reflect.DeepEqual(stored, finished) {
		t.Fatalf("terminal invocation overwritten: %+v, %v", stored, err)
	}
	missionAfterRetries, err := s.GetMission(m.ID)
	if err != nil || !reflect.DeepEqual(missionAfterRetries, missionAfterFinish) {
		t.Fatalf("terminal retry changed mission: %+v, %v", missionAfterRetries, err)
	}
	eventsAfterRetries, err := s.Events(0, 100)
	if err != nil || len(eventsAfterRetries) != len(eventsAfterFinish) {
		t.Fatalf("terminal retry changed events: before=%d after=%d err=%v", len(eventsAfterFinish), len(eventsAfterRetries), err)
	}
	var transitionsAfterRetries, invocationRows int
	if err = s.db.QueryRow(`SELECT count(*) FROM invocation_transitions WHERE invocation_id=?`, v.ID).Scan(&transitionsAfterRetries); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM invocations WHERE id=?`, v.ID).Scan(&invocationRows); err != nil {
		t.Fatal(err)
	}
	if transitionsAfterRetries != transitionsAfterFinish || invocationRows != 1 {
		t.Fatalf("terminal retry added ledger rows: transitions %d -> %d, invocations=%d", transitionsAfterFinish, transitionsAfterRetries, invocationRows)
	}
}

// A real deferred foreign-key failure rejects COMMIT and must not publish the result.
func TestFinishInvocationCommitFailureKeepsClaimedStateUntilRecovery(t *testing.T) {
	s := openTestStore(t)
	m, bindingDigest := controlledStoreMission(t, s, "finish-commit-failure")
	m, grant := controlledGrant(t, s, m, bindingDigest)
	m, v, err := s.BeginInvocation(modelInvocation("inv-finish-commit-failure", m, grant, bindingDigest), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(v.ID, m.Epoch); err != nil {
		t.Fatal(err)
	}
	missionBeforeFinish, err := s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	eventsBeforeFinish, err := s.Events(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var transitionsBeforeFinish int
	if err = s.db.QueryRow(`SELECT count(*) FROM invocation_transitions WHERE invocation_id=?`, v.ID).Scan(&transitionsBeforeFinish); err != nil {
		t.Fatal(err)
	}
	installDeferredFailure(t, s, "invocation_transitions")
	result := controlledEnvelope("model_output", v.ID, m.ID, bindingDigest, `{"kind":"final"}`)
	if _, err = s.FinishInvocation(v.ID, m.Epoch, "succeeded", &result, "", domain.CallUsage{UsageKnown: true, InputTokens: 11, OutputTokens: 7}); err == nil || !strings.Contains(err.Error(), "commit invocation finish") || !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("deferred finish COMMIT error = %v, want invocation-finish commit provenance and deferred foreign-key cause", err)
	}

	claimed, err := s.GetInvocation(v.ID)
	if err != nil || claimed.Status != "dispatching" || claimed.Result != nil || claimed.FinishedAt != "" || claimed.UsageKnown || claimed.InputTokens != 0 || claimed.OutputTokens != 0 {
		t.Fatalf("rejected COMMIT published finish: %+v, %v", claimed, err)
	}
	missionAfterFailure, err := s.GetMission(m.ID)
	if err != nil || !reflect.DeepEqual(missionAfterFailure, missionBeforeFinish) || missionAfterFailure.Budget.UsedCalls != 1 {
		t.Fatalf("rejected COMMIT changed mission/reservation: %+v, %v", missionAfterFailure, err)
	}
	eventsAfterFailure, err := s.Events(0, 100)
	if err != nil || len(eventsAfterFailure) != len(eventsBeforeFinish) {
		t.Fatalf("rejected COMMIT changed events: before=%d after=%d err=%v", len(eventsBeforeFinish), len(eventsAfterFailure), err)
	}
	var transitionsAfterFailure int
	if err = s.db.QueryRow(`SELECT count(*) FROM invocation_transitions WHERE invocation_id=?`, v.ID).Scan(&transitionsAfterFailure); err != nil {
		t.Fatal(err)
	}
	if transitionsAfterFailure != transitionsBeforeFinish {
		t.Fatalf("rejected COMMIT changed transitions: before=%d after=%d", transitionsBeforeFinish, transitionsAfterFailure)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fixture_commit`); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverInvocations(m.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.GetInvocation(v.ID)
	if err != nil || recovered.Status != "outcome_unknown" || recovered.Result != nil || recovered.ErrorCode != "outcome_unknown_after_restart" {
		t.Fatalf("recovery inferred success: %+v, %v", recovered, err)
	}
	missionAfterRecovery, err := s.GetMission(m.ID)
	if err != nil || missionAfterRecovery.Budget.UsedCalls != 1 || missionAfterRecovery.Budget.InputTokens != 0 || missionAfterRecovery.Budget.OutputTokens != 0 {
		t.Fatalf("recovery refunded or attached usage: %+v, %v", missionAfterRecovery.Budget, err)
	}
}

// A grant for another session cannot authorize this invocation even when all digests match.
func TestBeginInvocationChecksGrantSessionRoleAndState(t *testing.T) {
	s := openTestStore(t)
	m, bindingDigest := controlledStoreMission(t, s, "grant-boundary")
	m, grant := controlledGrant(t, s, m, bindingDigest)
	v := repositoryInvocation("inv-wrong-session", m, grant, bindingDigest)
	v.SessionID = "another-session"
	if _, _, err := s.BeginInvocation(v, m.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong session error = %v, want ErrConflict", err)
	}
}

// Store quotas are lifetime/session authority, not advisory engine counters.
func TestInvocationBudgetsAreEnforcedByDurableRows(t *testing.T) {
	t.Run("mission lifetime model calls", func(t *testing.T) {
		s := openTestStore(t)
		m, bindingDigest := controlledStoreMission(t, s, "model-quota")
		m, err := s.UpdateMission(m.ID, m.Revision, "fixture.limit", func(m *domain.Mission) error { m.Budget.MaxCalls = 1; return nil })
		if err != nil {
			t.Fatal(err)
		}
		m, grant := controlledGrant(t, s, m, bindingDigest)
		m, first, err := s.BeginInvocation(modelInvocation("model-one", m, grant, bindingDigest), m.Revision)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ClaimInvocation(first.ID, m.Epoch); err != nil {
			t.Fatal(err)
		}
		if _, err = s.FinishInvocation(first.ID, m.Epoch, "failed", nil, "fixture_failure", domain.CallUsage{}); err != nil {
			t.Fatal(err)
		}
		m, _ = s.GetMission(m.ID)
		if _, _, err = s.BeginInvocation(modelInvocation("model-two", m, grant, bindingDigest), m.Revision); err == nil {
			t.Fatal("second lifetime model call exceeded MaxCalls")
		}
	})

	t.Run("Researcher session turns", func(t *testing.T) {
		s := openTestStore(t)
		m, bindingDigest := controlledStoreMission(t, s, "turn-quota")
		m, grant := controlledGrant(t, s, m, bindingDigest)
		for i := 0; i < 4; i++ {
			id := fmt.Sprintf("turn-%d", i)
			var v domain.Invocation
			var err error
			m, v, err = s.BeginInvocation(modelInvocation(id, m, grant, bindingDigest), m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ClaimInvocation(v.ID, m.Epoch); err != nil {
				t.Fatal(err)
			}
			if _, err = s.FinishInvocation(v.ID, m.Epoch, "failed", nil, "fixture_failure", domain.CallUsage{}); err != nil {
				t.Fatal(err)
			}
			m, _ = s.GetMission(m.ID)
		}
		if _, _, err := s.BeginInvocation(modelInvocation("turn-five", m, grant, bindingDigest), m.Revision); err == nil {
			t.Fatal("fifth Researcher turn in one session accepted")
		}
	})

	t.Run("repository lifetime reservation", func(t *testing.T) {
		s := openTestStore(t)
		m, bindingDigest := controlledStoreMission(t, s, "repo-quota")
		m, grant := controlledGrant(t, s, m, bindingDigest)
		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("repo-%d", i)
			var v domain.Invocation
			var err error
			m, v, err = s.BeginInvocation(repositoryInvocation(id, m, grant, bindingDigest), m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if v.ReservedResultBytes != 64<<10 {
				t.Fatalf("reservation = %d", v.ReservedResultBytes)
			}
			if _, err = s.ClaimInvocation(v.ID, m.Epoch); err != nil {
				t.Fatal(err)
			}
			if _, err = s.FinishInvocation(v.ID, m.Epoch, "failed", nil, "fixture_failure", domain.CallUsage{}); err != nil {
				t.Fatal(err)
			}
			m, _ = s.GetMission(m.ID)
		}
		if _, _, err := s.BeginInvocation(repositoryInvocation("repo-four", m, grant, bindingDigest), m.Revision); err == nil {
			t.Fatal("fourth lifetime repository reservation accepted")
		}
	})
}

// Separating mission budget, invocation and event writes would leak a partial reservation.
func TestBeginInvocationReservationIsAtomic(t *testing.T) {
	s := openTestStore(t)
	m, bindingDigest := controlledStoreMission(t, s, "atomic")
	m, grant := controlledGrant(t, s, m, bindingDigest)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_invocation_insert BEFORE INSERT ON invocations BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	model := domain.Invocation{ID: "inv-atomic", MissionID: m.ID, Epoch: m.Epoch, SessionID: "research-session", Kind: "model", Role: domain.Researcher, Name: "model.complete", BindingDigest: bindingDigest, AuthorityDigest: grant.AuthorityDigest, GrantID: grant.ID, Input: controlledEnvelope("model_input", "inv-atomic", m.ID, bindingDigest, `{}`)}
	if _, _, err := s.BeginInvocation(model, m.Revision); err == nil {
		t.Fatal("injected insert failure succeeded")
	}
	stored, err := s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != m.Revision || stored.Budget.UsedCalls != 0 || stored.Budget.UsageUnknown {
		t.Fatalf("partial budget reservation: %+v", stored.Budget)
	}
	if _, err := s.GetInvocation(model.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial invocation: %v", err)
	}
}

// A deferred commit failure must roll back the mission budget, row, transition and event together.
func TestBeginInvocationCommitFailureRollsBackReservation(t *testing.T) {
	s := openTestStore(t)
	m, bindingDigest := controlledStoreMission(t, s, "commit-failure")
	m, grant := controlledGrant(t, s, m, bindingDigest)
	installDeferredFailure(t, s, "invocations")
	v := modelInvocation("inv-commit-failure", m, grant, bindingDigest)
	if _, _, err := s.BeginInvocation(v, m.Revision); err == nil {
		t.Fatal("deferred commit failure succeeded")
	}
	stored, err := s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != m.Revision || stored.Budget.UsedCalls != 0 || stored.Budget.UsageUnknown {
		t.Fatalf("failed commit leaked reservation: revision=%d budget=%+v", stored.Revision, stored.Budget)
	}
	if _, err := s.GetInvocation(v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed commit leaked invocation: %v", err)
	}
}

// Recovery must classify persisted intent without ever granting a new claim.
func TestRecoverInvocationsClassifiesEveryPersistedState(t *testing.T) {
	s := openTestStore(t)
	m, bindingDigest := controlledStoreMission(t, s, "recover")
	m, grant := controlledGrant(t, s, m, bindingDigest)

	m, pending, err := s.BeginInvocation(repositoryInvocation("inv-pending", m, grant, bindingDigest), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	// Only one active call is allowed, so make the next one claimable by finishing this one.
	claimed, err := s.ClaimInvocation(pending.ID, m.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishInvocation(claimed.ID, m.Epoch, "failed", nil, "fixture_failure", domain.CallUsage{}); err != nil {
		t.Fatal(err)
	}
	m, err = s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, dispatching, err := s.BeginInvocation(repositoryInvocation("inv-dispatching", m, grant, bindingDigest), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(dispatching.ID, m.Epoch); err != nil {
		t.Fatal(err)
	}
	// Simulate another pending row after the crash without violating the one-active index.
	if _, err = s.db.Exec(`DROP INDEX one_active_invocation`); err != nil {
		t.Fatal(err)
	}
	m, err = s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, unclaimed, err := s.BeginInvocation(repositoryInvocation("inv-unclaimed", m, grant, bindingDigest), m.Revision)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RecoverInvocations(m.ID); err != nil {
		t.Fatal(err)
	}
	gotPending, _ := s.GetInvocation(unclaimed.ID)
	gotDispatching, _ := s.GetInvocation(dispatching.ID)
	gotTerminal, _ := s.GetInvocation(pending.ID)
	if gotPending.Status != "interrupted_before_dispatch" || gotDispatching.Status != "outcome_unknown" || gotTerminal.Status != "failed" {
		t.Fatalf("recovered statuses: pending=%q dispatching=%q terminal=%q", gotPending.Status, gotDispatching.Status, gotTerminal.Status)
	}
}

// One corrupt intent must block only its trusted mission and not hide or stop other recovery.
func TestRecoverInvocationsIsolatesCorruptRows(t *testing.T) {
	s := openTestStore(t)
	badMission, badDigest := controlledStoreMission(t, s, "corrupt-ledger")
	badMission, badGrant := controlledGrant(t, s, badMission, badDigest)
	_, bad, err := s.BeginInvocation(repositoryInvocation("inv-corrupt", badMission, badGrant, badDigest), badMission.Revision)
	if err != nil {
		t.Fatal(err)
	}
	goodMission, goodDigest := controlledStoreMission(t, s, "healthy-ledger")
	goodMission, goodGrant := controlledGrant(t, s, goodMission, goodDigest)
	_, good, err := s.BeginInvocation(repositoryInvocation("inv-healthy", goodMission, goodGrant, goodDigest), goodMission.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER invocation_immutable_intent; DROP TRIGGER invocation_transition; PRAGMA ignore_check_constraints=ON; UPDATE invocations SET intent='{' WHERE id=?`, bad.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInvocations(badMission.ID); err != nil {
		t.Fatal(err)
	}
	untouched, err := s.GetInvocation(good.ID)
	if err != nil || untouched.Status != "authorized_pending" {
		t.Fatalf("scoped recovery touched another mission: %+v, %v", untouched, err)
	}
	if err := s.RecoverInvocations(goodMission.ID); err != nil {
		t.Fatal(err)
	}
	blocked, err := s.GetMission(badMission.ID)
	if err != nil || blocked.State != "blocked" || !strings.Contains(blocked.Error, "invocation ledger") {
		t.Fatalf("corrupt mission = %+v, %v", blocked, err)
	}
	if _, err := s.GetInvocation(bad.ID); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("corrupt detail error = %v", err)
	}
	healthy, err := s.GetInvocation(good.ID)
	if err != nil || healthy.Status != "interrupted_before_dispatch" {
		t.Fatalf("healthy recovery = %+v, %v", healthy, err)
	}
}

// Filtering recovery to active statuses would leave malformed terminal history
// undetected and its SQL-owned mission incorrectly usable.
func TestRecoverInvocationsIsolatesCorruptTerminalRows(t *testing.T) {
	statuses := []string{"succeeded", "failed", "outcome_unknown", "interrupted_before_dispatch"}
	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			s := openTestStore(t)
			badMission, bad := terminalInvocationFixture(t, s, "bad-"+status, "bad-inv-"+status, status)
			_, control := terminalInvocationFixture(t, s, "control-"+status, "control-inv-"+status, status)

			healthyMission, healthyDigest := controlledStoreMission(t, s, "healthy-"+status)
			healthyMission, healthyGrant := controlledGrant(t, s, healthyMission, healthyDigest)
			_, healthy, err := s.BeginInvocation(repositoryInvocation("healthy-inv-"+status, healthyMission, healthyGrant, healthyDigest), healthyMission.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`DROP TRIGGER invocation_immutable_intent; DROP TRIGGER invocation_transition; PRAGMA ignore_check_constraints=ON; UPDATE invocations SET intent='{' WHERE id=?`, bad.ID); err != nil {
				t.Fatal(err)
			}

			for _, id := range []string{badMission.ID, healthyMission.ID, control.MissionID} {
				if err := s.RecoverInvocations(id); err != nil {
					t.Fatal(err)
				}
			}
			blocked, err := s.GetMission(badMission.ID)
			if err != nil || blocked.State != "blocked" || !strings.Contains(blocked.Error, "invocation ledger") {
				t.Fatalf("corrupt terminal mission = %+v, %v", blocked, err)
			}
			healthyAfter, err := s.GetInvocation(healthy.ID)
			if err != nil || healthyAfter.Status != "interrupted_before_dispatch" {
				t.Fatalf("healthy active recovery = %+v, %v", healthyAfter, err)
			}
			controlAfter, err := s.GetInvocation(control.ID)
			if err != nil || !sameJSON(controlAfter, control) {
				t.Fatalf("valid terminal row changed: before=%+v after=%+v err=%v", control, controlAfter, err)
			}
		})
	}
}

func terminalInvocationFixture(t *testing.T, s *Store, missionID, invocationID, status string) (domain.Mission, domain.Invocation) {
	t.Helper()
	m, bindingDigest := controlledStoreMission(t, s, missionID)
	m, grant := controlledGrant(t, s, m, bindingDigest)
	m, v, err := s.BeginInvocation(repositoryInvocation(invocationID, m, grant, bindingDigest), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if status == "interrupted_before_dispatch" {
		if err := s.RecoverInvocations(m.ID); err != nil {
			t.Fatal(err)
		}
		v, err = s.GetInvocation(v.ID)
		if err != nil {
			t.Fatal(err)
		}
		return m, v
	}
	if _, err = s.ClaimInvocation(v.ID, m.Epoch); err != nil {
		t.Fatal(err)
	}
	var result *domain.EvidenceEnvelope
	safeError := "fixture_failure"
	if status == "succeeded" {
		envelope := controlledEnvelope("tool_observation", v.ID, m.ID, bindingDigest, `{"ok":true}`)
		result = &envelope
		safeError = ""
	}
	v, err = s.FinishInvocation(v.ID, m.Epoch, status, result, safeError, domain.CallUsage{})
	if err != nil {
		t.Fatal(err)
	}
	return m, v
}

// Task bindings are authority, not mutable mission presentation state.
func TestControlledMissionBindingsAreImmutable(t *testing.T) {
	s := openTestStore(t)
	m, _ := controlledStoreMission(t, s, "immutable")
	_, err := s.UpdateMission(m.ID, m.Revision, "fixture.mutate", func(m *domain.Mission) error {
		m.TaskBindings.Git.BaseCommit = strings.Repeat("c", 40)
		return nil
	})
	if err == nil {
		t.Fatal("Go mutation changed task bindings")
	}
	if _, err := s.db.Exec(`UPDATE missions SET data=json_remove(data,'$.task_bindings') WHERE id=?`, m.ID); err == nil {
		t.Fatal("old writer stripped task bindings")
	}
}
