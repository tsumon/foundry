package hooks

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/store"

	_ "github.com/mattn/go-sqlite3"
)

func openHookStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "foundry.db")
	persistent, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.Close() })
	return persistent, path
}

func createHookMission(t *testing.T, persistent *store.Store, id string) domain.Mission {
	t.Helper()
	mission, err := persistent.CreateMission(domain.Mission{
		ID: id, Title: "unchanged", State: "draft",
		Approvals: []domain.Approval{{Kind: "plan", Digest: "sealed-approval", Revision: 1, CreatedAt: "2026-10-18T00:00:00Z"}},
		Budget:    domain.Budget{MaxCalls: 16, UsedCalls: 3, InputTokens: 120, OutputTokens: 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	return mission
}

func saveEnabledRule(t *testing.T, observer *Service, missionID string) domain.HookRule {
	t.Helper()
	rule, err := observer.SaveRule(RuleInput{
		Name: "Record pauses", Enabled: true, EventKinds: []string{"mission.paused"},
		MissionID: missionID, Action: "record_activity",
	})
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func addPauseEvent(t *testing.T, persistent *store.Store, mission domain.Mission) domain.Mission {
	t.Helper()
	next, err := persistent.UpdateMission(mission.ID, mission.Revision, "mission.paused", func(current *domain.Mission) error {
		current.Title = current.Title + "."
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func waitForHookSnapshot(t *testing.T, observer *Service, predicate func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := observer.Snapshot()
		if err == nil && predicate(snapshot) {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	snapshot, err := observer.Snapshot()
	t.Fatalf("hook snapshot condition not reached: snapshot=%+v err=%v", snapshot, err)
	return Snapshot{}
}

func TestServiceStartsImmediatelyProcessesBoundedRecordsAndRestartsWithoutDuplicates(t *testing.T) {
	persistent, _ := openHookStore(t)
	mission := createHookMission(t, persistent, "service-restart")
	observer := New(persistent)
	rule := saveEnabledRule(t, observer, mission.ID)
	mission = addPauseEvent(t, persistent, mission)

	beforeMission, err := persistent.GetMission(mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, err := json.Marshal(beforeMission)
	if err != nil {
		t.Fatal(err)
	}

	initial, err := observer.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if initial.Runtime.State != "not_started" || initial.Runtime.ErrorCode != "" || initial.Processor.PendingEvents != 2 {
		t.Fatalf("initial snapshot=%+v", initial)
	}

	observer.Start()
	observer.Start()
	waitForHookSnapshot(t, observer, func(snapshot Snapshot) bool {
		return snapshot.Runtime.State == "running" && snapshot.Processor.PendingEvents == 0
	})
	response, err := observer.Records(mission.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Records) != 1 || response.Records[0].RuleID != rule.ID || response.Records[0].MissionRevision != mission.Revision {
		t.Fatalf("records=%+v", response.Records)
	}
	if response.Runtime.State != "running" || response.Runtime.LastAttemptAt == "" || response.Runtime.LastSuccessAt == "" || response.Runtime.ErrorCode != "" {
		t.Fatalf("runtime=%+v", response.Runtime)
	}
	afterFirstRun, err := persistent.GetMission(mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterFirstRunJSON, err := json.Marshal(afterFirstRun)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterFirstRunJSON) != string(beforeJSON) {
		t.Fatalf("observer changed mission: before=%s after=%s", beforeJSON, afterFirstRunJSON)
	}
	observer.Close()
	observer.Close()
	stopped, err := observer.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Runtime.State != "stopped" {
		t.Fatalf("runtime after close=%+v", stopped.Runtime)
	}

	mission = addPauseEvent(t, persistent, mission)
	restarted := New(persistent)
	restarted.Start()
	t.Cleanup(restarted.Close)
	waitForHookSnapshot(t, restarted, func(snapshot Snapshot) bool { return snapshot.Processor.PendingEvents == 0 })
	response, err = restarted.Records(mission.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Records) != 2 || response.Records[0].EventSeq == response.Records[1].EventSeq {
		t.Fatalf("restart records=%+v", response.Records)
	}

	afterMission, err := persistent.GetMission(mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, err := json.Marshal(afterMission)
	if err != nil {
		t.Fatal(err)
	}
	// The second source event legitimately changed title/revision; its observation
	// still cannot change approvals, budget, or evidence.
	if string(mustJSON(t, afterMission.Approvals)) != string(mustJSON(t, beforeMission.Approvals)) ||
		string(mustJSON(t, afterMission.Budget)) != string(mustJSON(t, beforeMission.Budget)) ||
		string(mustJSON(t, afterMission.Evidence)) != string(mustJSON(t, beforeMission.Evidence)) {
		t.Fatalf("observer changed protected mission fields: before=%s after=%s", beforeJSON, afterJSON)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestServiceDegradesWithSafeErrorAndRecoversOnNextScan(t *testing.T) {
	persistent, path := openHookStore(t)
	mission := createHookMission(t, persistent, "service-recovery")
	observer := New(persistent)
	saveEnabledRule(t, observer, mission.ID)
	addPauseEvent(t, persistent, mission)

	admin, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	if _, err = admin.Exec(`CREATE TRIGGER hook_fixture_failure BEFORE INSERT ON hook_records BEGIN SELECT RAISE(ABORT, 'raw private database detail'); END`); err != nil {
		t.Fatal(err)
	}

	observer.Start()
	t.Cleanup(observer.Close)
	degraded := waitForHookSnapshot(t, observer, func(snapshot Snapshot) bool {
		return snapshot.Runtime.State == "degraded"
	})
	if degraded.Runtime.ErrorCode != "observation_storage_error" || degraded.Runtime.LastAttemptAt == "" || degraded.Runtime.LastSuccessAt != "" {
		t.Fatalf("degraded runtime=%+v", degraded.Runtime)
	}
	if _, err = admin.Exec(`DROP TRIGGER hook_fixture_failure`); err != nil {
		t.Fatal(err)
	}
	recovered := waitForHookSnapshot(t, observer, func(snapshot Snapshot) bool {
		return snapshot.Runtime.State == "running" && snapshot.Runtime.LastSuccessAt != "" && snapshot.Processor.PendingEvents == 0
	})
	if recovered.Runtime.ErrorCode != "" {
		t.Fatalf("recovered runtime=%+v", recovered.Runtime)
	}
}

func TestServiceRuleInputRequiresServerGeneratedCreateIDAndCASUpdate(t *testing.T) {
	persistent, _ := openHookStore(t)
	observer := New(persistent)

	created, err := observer.SaveRule(RuleInput{Name: "all", Enabled: false, EventKinds: []string{"mission.created"}, Action: "record_activity"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.ID) != 32 || created.Revision != 1 {
		t.Fatalf("created=%+v", created)
	}
	updated, err := observer.SaveRule(RuleInput{ID: created.ID, ExpectedRevision: 1, Name: "all updated", Enabled: true, EventKinds: []string{"mission.created"}, Action: "record_activity"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("updated=%+v", updated)
	}
	if _, err = observer.SaveRule(RuleInput{ID: "0123456789abcdef0123456789abcdef", Name: "client id", EventKinds: []string{"mission.created"}, Action: "record_activity"}); err != domain.ErrInvalidHook {
		t.Fatalf("client-selected create ID error=%v", err)
	}
	if _, err = observer.SaveRule(RuleInput{ExpectedRevision: 1, Name: "missing id", EventKinds: []string{"mission.created"}, Action: "record_activity"}); err != domain.ErrInvalidHook {
		t.Fatalf("missing update ID error=%v", err)
	}
	if _, err = observer.SaveRule(RuleInput{Name: "action denied", EventKinds: []string{"mission.created"}, Action: "webhook"}); err != domain.ErrInvalidHook {
		t.Fatalf("action error=%v", err)
	}
}

func TestServiceCloseWaitsBeforeStoreClose(t *testing.T) {
	persistent, _ := openHookStore(t)
	observer := New(persistent)
	observer.Start()
	observer.Close()
	stopped, err := observer.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Runtime.State != "stopped" || stopped.Runtime.ErrorCode != "" {
		t.Fatalf("close reported a spurious failure: %+v", stopped.Runtime)
	}
	if err := persistent.Close(); err != nil {
		t.Fatal(err)
	}
	observer.Close()
	if _, err := observer.Records("", 0, 50); err == nil {
		t.Fatal("closed storage read unexpectedly succeeded")
	}
}
