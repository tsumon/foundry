package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"foundry/internal/domain"
)

func hookRule() domain.HookRule {
	return domain.HookRule{ID: strings.Repeat("a", 32), Name: "计划观察", Enabled: true, EventKinds: []string{"plan.revised"}, Action: "record_activity"}
}
func hookMission(t *testing.T, s *Store, id string) domain.Mission {
	t.Helper()
	m, e := s.CreateMission(domain.Mission{ID: id, Title: "before", State: "draft", Approvals: []domain.Approval{{Kind: "plan", Digest: "fixture-approval-seal", Revision: 1, CreatedAt: "2026-10-18T00:00:00Z"}}})
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func hookEvent(t *testing.T, s *Store, id, kind string) {
	t.Helper()
	m, e := s.GetMission(id)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.UpdateMission(id, m.Revision, kind, func(m *domain.Mission) error { m.Title = "updated"; return nil })
	if e != nil {
		t.Fatal(e)
	}
}
func hookProcess(t *testing.T, s *Store, limit int) domain.HookProcessorState {
	t.Helper()
	state, e := s.ProcessHookEvents(context.Background(), limit)
	if e != nil {
		t.Fatal(e)
	}
	return state
}

// Catches using the latest rule or latest mission instead of immutable event-time facts.
func TestHooksDelayedProjectionUsesHistoricalRevisions(t *testing.T) {
	s := openTestStore(t)
	hookMission(t, s, "m1")
	r, e := s.SaveHookRule(hookRule(), 0)
	if e != nil {
		t.Fatal(e)
	}
	if r.Revision != 1 || r.EffectiveAfterSeq != 1 {
		t.Fatalf("rule: %+v", r)
	}
	hookEvent(t, s, "m1", "plan.revised")
	r.Enabled = false
	r.Name = "disabled later"
	r, e = s.SaveHookRule(r, r.Revision)
	if e != nil {
		t.Fatal(e)
	}
	hookEvent(t, s, "m1", "plan.revised")
	r.Enabled = true
	r.Name = "new version"
	r, e = s.SaveHookRule(r, r.Revision)
	if e != nil {
		t.Fatal(e)
	}
	// Same boundary: highest revision wins for the next event.
	r.Name = "last version"
	r, e = s.SaveHookRule(r, r.Revision)
	if e != nil {
		t.Fatal(e)
	}
	hookEvent(t, s, "m1", "plan.revised")
	hookEvent(t, s, "m1", "mission.paused")
	var before []byte
	if e = s.db.QueryRow(`SELECT data FROM missions WHERE id='m1'`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	events, e := s.Events(0, 100)
	if e != nil {
		t.Fatal(e)
	}
	st := hookProcess(t, s, 100)
	if st.Cursor != 5 || st.PendingEvents != 0 || st.LastProcessedAt == "" {
		t.Fatalf("state: %+v", st)
	}
	page, e := s.HookRecords("m1", 0, 50)
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Records) != 2 {
		t.Fatalf("records %+v", page)
	}
	newer, older := page.Records[0], page.Records[1]
	if newer.RuleRevision != 4 || newer.RuleName != "last version" || newer.EventSeq != 4 || older.RuleRevision != 1 || older.RuleName != "计划观察" || older.MissionRevision != 2 || older.EventCreatedAt != events[1].CreatedAt || older.HandlerVersion != "record_activity/v1" || older.Status != "recorded" {
		t.Fatalf("historical records %+v", page)
	}
	var after []byte
	if e = s.db.QueryRow(`SELECT data FROM missions WHERE id='m1'`).Scan(&after); e != nil {
		t.Fatal(e)
	}
	afterEvents, e := s.Events(0, 100)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(before, after) || !reflect.DeepEqual(events, afterEvents) {
		t.Fatal("observer mutated mission/approvals/source events")
	}
	previous := st
	st = hookProcess(t, s, 100)
	if !reflect.DeepEqual(previous, st) {
		t.Fatalf("idle processing changed state %+v %+v", previous, st)
	}
}

// Catches partial record/cursor commits and coupling projection failure to source commits.
func TestHooksFailureRollsBackBatchOnly(t *testing.T) {
	s := openTestStore(t)
	hookMission(t, s, "m1")
	if _, e := s.SaveHookRule(hookRule(), 0); e != nil {
		t.Fatal(e)
	}
	hookProcess(t, s, 100)
	hookEvent(t, s, "m1", "plan.revised")
	hookEvent(t, s, "m1", "plan.revised")
	before, e := s.HookState()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`CREATE TRIGGER fail_hook_record BEFORE INSERT ON hook_records WHEN NEW.event_seq=3 BEGIN SELECT RAISE(ABORT,'fixture'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ProcessHookEvents(context.Background(), 100); e == nil {
		t.Fatal("expected insert failure")
	}
	after, e := s.HookState()
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("cursor advanced %+v %+v", before, after)
	}
	p, e := s.HookRecords("", 0, 50)
	if e != nil || len(p.Records) != 0 {
		t.Fatalf("partial records %+v %v", p, e)
	}
	m, e := s.GetMission("m1")
	if e != nil || m.Revision != 3 {
		t.Fatalf("source lost %+v %v", m, e)
	}
	if _, e = s.db.Exec(`DROP TRIGGER fail_hook_record`); e != nil {
		t.Fatal(e)
	}
	hookProcess(t, s, 100)
	hookProcess(t, s, 100)
	p, e = s.HookRecords("", 0, 50)
	if e != nil || len(p.Records) != 2 {
		t.Fatalf("recovery %+v %v", p, e)
	}
}

func TestHooksRuleValidationCASAndCap(t *testing.T) {
	s := openTestStore(t)
	cases := []struct {
		name   string
		change func(*domain.HookRule)
	}{
		{"empty name", func(r *domain.HookRule) { r.Name = "" }}, {"long name", func(r *domain.HookRule) { r.Name = strings.Repeat("界", 81) }}, {"control", func(r *domain.HookRule) { r.Name = "a\nb" }}, {"utf8", func(r *domain.HookRule) { r.Name = string([]byte{255}) }}, {"empty events", func(r *domain.HookRule) { r.EventKinds = nil }}, {"duplicate", func(r *domain.HookRule) { r.EventKinds = []string{"plan.ready", "plan.ready"} }}, {"unknown", func(r *domain.HookRule) { r.EventKinds = []string{"shell.run"} }}, {"action", func(r *domain.HookRule) { r.Action = "exec" }}, {"id", func(r *domain.HookRule) { r.ID = "bad" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := hookRule()
			c.change(&r)
			if _, e := s.SaveHookRule(r, 0); !errors.Is(e, domain.ErrInvalidHook) {
				t.Fatalf("got %v", e)
			}
		})
	}
	r := hookRule()
	r.MissionID = "missing"
	if _, e := s.SaveHookRule(r, 0); !errors.Is(e, ErrNotFound) {
		t.Fatalf("mission: %v", e)
	}
	if _, e := s.SaveHookRule(hookRule(), 1); !errors.Is(e, ErrNotFound) {
		t.Fatalf("missing rule: %v", e)
	}
	r = hookRule()
	r.Name = strings.Repeat("界", 80)
	r.EventKinds = []string{"plan.revised", "mission.created"}
	r, e := s.SaveHookRule(r, 0)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(r.EventKinds, []string{"mission.created", "plan.revised"}) {
		t.Fatal("not canonical")
	}
	if _, e = s.SaveHookRule(r, 0); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale: %v", e)
	}
	for i := 1; i < 64; i++ {
		next := hookRule()
		next.ID = fmt.Sprintf("%032x", i)
		if _, e = s.SaveHookRule(next, 0); e != nil {
			t.Fatal(e)
		}
	}
	extra := hookRule()
	extra.ID = strings.Repeat("b", 32)
	if _, e = s.SaveHookRule(extra, 0); !errors.Is(e, domain.ErrInvalidHook) {
		t.Fatalf("cap: %v", e)
	}
	r.Name = "edit at cap"
	if _, e = s.SaveHookRule(r, r.Revision); e != nil {
		t.Fatal(e)
	}
}

func TestHooksPagesFiltersBatchesAndConcurrency(t *testing.T) {
	s := openTestStore(t)
	rules, e := s.HookRules()
	if e != nil || rules == nil || len(rules) != 0 {
		t.Fatalf("empty rules %v %v", rules, e)
	}
	p, e := s.HookRecords("", 0, 50)
	if e != nil || p.Records == nil || len(p.Records) != 0 {
		t.Fatalf("empty page %+v %v", p, e)
	}
	hookMission(t, s, "m1")
	hookMission(t, s, "m2")
	r := hookRule()
	r.MissionID = "m1"
	if _, e = s.SaveHookRule(r, 0); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		hookEvent(t, s, "m1", "plan.revised")
		hookEvent(t, s, "m2", "plan.revised")
	}
	st := hookProcess(t, s, 3)
	if st.Cursor != 3 || st.PendingEvents != 9 {
		t.Fatalf("batch %+v", st)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, e := s.ProcessHookEvents(ctx, 3); e != nil {
				t.Error(e)
			}
			if _, e := s.HookRules(); e != nil {
				t.Error(e)
			}
			if _, e := s.HookState(); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	p, e = s.HookRecords("m1", 0, 2)
	if e != nil || len(p.Records) != 2 || p.NextBefore != p.Records[1].ID {
		t.Fatalf("page1 %+v %v", p, e)
	}
	q, e := s.HookRecords("m1", p.NextBefore, 2)
	if e != nil || len(q.Records) != 2 || q.Records[0].ID >= p.NextBefore {
		t.Fatalf("page2 %+v %v", q, e)
	}
	last, e := s.HookRecords("m1", q.NextBefore, 2)
	if e != nil || len(last.Records) != 1 || last.NextBefore != 0 {
		t.Fatalf("last %+v %v", last, e)
	}
	other, e := s.HookRecords("m2", 0, 50)
	if e != nil || len(other.Records) != 0 {
		t.Fatalf("filter %+v %v", other, e)
	}
	if _, e = s.HookRecords("missing", 0, 50); !errors.Is(e, ErrNotFound) {
		t.Fatalf("missing mission %v", e)
	}
}

func TestHooksUpgradeSkipsHistoryOnlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, e := sql.Open("sqlite3", path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`CREATE TABLE missions(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,state TEXT NOT NULL,updated_at TEXT NOT NULL,data BLOB NOT NULL); CREATE TABLE events(seq INTEGER PRIMARY KEY AUTOINCREMENT,mission_id TEXT NOT NULL,kind TEXT NOT NULL,created_at TEXT NOT NULL,revision INTEGER NOT NULL); INSERT INTO missions VALUES('m1',1,'draft','2026-10-18T00:00:00Z','{"id":"m1","revision":1,"state":"draft"}'); INSERT INTO events VALUES(7,'m1','mission.created','2026-10-18T00:00:00Z',1)`)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	st, e := s.HookState()
	if e != nil || st.InitializedAfterSeq != 7 || st.Cursor != 7 || st.LastProcessedAt != "" {
		t.Fatalf("install %+v %v", st, e)
	}
	if _, e = s.SaveHookRule(hookRule(), 0); e != nil {
		t.Fatal(e)
	}
	hookEvent(t, s, "m1", "plan.revised")
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	st, e = s.HookState()
	if e != nil || st.Cursor != 7 || st.PendingEvents != 1 {
		t.Fatalf("reopen %+v %v", st, e)
	}
	hookProcess(t, s, 100)
	p, e := s.HookRecords("", 0, 50)
	if e != nil || len(p.Records) != 1 || p.Records[0].EventSeq != 8 {
		t.Fatalf("resume %+v %v", p, e)
	}
}

func TestHooksCorruptStoredContentFailsClosed(t *testing.T) {
	for _, target := range []string{"current", "historical", "record"} {
		t.Run(target, func(t *testing.T) {
			s := openTestStore(t)
			hookMission(t, s, "m1")
			if _, e := s.SaveHookRule(hookRule(), 0); e != nil {
				t.Fatal(e)
			}
			hookEvent(t, s, "m1", "plan.revised")
			switch target {
			case "current":
				if _, e := s.db.Exec(`UPDATE hook_rules SET data=json_set(data,'$.event_kinds',json('["invalid"]'))`); e != nil {
					t.Fatal(e)
				}
				if _, e := s.HookRules(); e == nil {
					t.Fatal("corrupt current rule accepted")
				}
			case "historical":
				if _, e := s.db.Exec(`DROP TRIGGER hook_revision_no_update; UPDATE hook_rule_revisions SET data=json_set(data,'$.action','shell')`); e != nil {
					t.Fatal(e)
				}
				if _, e := s.ProcessHookEvents(context.Background(), 100); e == nil {
					t.Fatal("corrupt historical rule accepted")
				}
				st, e := s.HookState()
				if e != nil || st.Cursor != 0 {
					t.Fatalf("corruption advanced %+v %v", st, e)
				}
			case "record":
				hookProcess(t, s, 100)
				if _, e := s.db.Exec(`DROP TRIGGER hook_record_no_update; UPDATE hook_records SET status='success'`); e != nil {
					t.Fatal(e)
				}
				if _, e := s.HookRecords("", 0, 50); e == nil {
					t.Fatal("corrupt record accepted")
				}
			}
		})
	}
}

// Missing/null boolean fields must not silently turn corrupt rules into disabled rules.
func TestHooksStoredRuleRequiresCompleteTypedFields(t *testing.T) {
	for _, expression := range []string{`json_remove(data,'$.enabled')`, `json_set(data,'$.enabled',NULL)`, `json_set(data,'$.created_at','not-a-time')`, `'{broken'`} {
		t.Run(expression, func(t *testing.T) {
			s := openTestStore(t)
			if _, e := s.SaveHookRule(hookRule(), 0); e != nil {
				t.Fatal(e)
			}
			if _, e := s.db.Exec(`UPDATE hook_rules SET data=` + expression); e != nil {
				t.Fatal(e)
			}
			if _, e := s.HookRules(); e == nil {
				t.Fatal("invalid persisted rule accepted")
			}
		})
	}
}

func TestHooksBatchCeilingCancellationAndIdleNoWrite(t *testing.T) {
	s := openTestStore(t)
	hookMission(t, s, "m1")
	for i := 0; i < 101; i++ {
		hookEvent(t, s, "m1", "unobserved.event")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.ProcessHookEvents(ctx, 100); e == nil {
		t.Fatal("cancelled scan succeeded")
	}
	st := hookProcess(t, s, 1000)
	if st.Cursor != 100 || st.PendingEvents != 2 {
		t.Fatalf("unbounded batch %+v", st)
	}
	st = hookProcess(t, s, 100)
	if st.Cursor != 102 || st.PendingEvents != 0 {
		t.Fatalf("tail %+v", st)
	}
	if _, e := s.db.Exec(`CREATE TRIGGER forbid_idle_write BEFORE UPDATE ON hook_processor_state BEGIN SELECT RAISE(ABORT,'idle write'); END`); e != nil {
		t.Fatal(e)
	}
	if next := hookProcess(t, s, 100); !reflect.DeepEqual(st, next) {
		t.Fatal("idle state changed")
	}
}

func TestHooksHistoryAndRecordsAreAppendOnly(t *testing.T) {
	s := openTestStore(t)
	hookMission(t, s, "m1")
	if _, e := s.SaveHookRule(hookRule(), 0); e != nil {
		t.Fatal(e)
	}
	hookEvent(t, s, "m1", "plan.revised")
	hookProcess(t, s, 100)
	for _, query := range []string{`UPDATE hook_rule_revisions SET revision=2`, `DELETE FROM hook_rule_revisions`, `DELETE FROM hook_rules`, `UPDATE hook_records SET rule_name='tampered'`, `DELETE FROM hook_records`} {
		if _, e := s.db.Exec(query); e == nil {
			t.Fatalf("history mutation accepted: %s", query)
		}
	}
}
