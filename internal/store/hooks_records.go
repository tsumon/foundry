package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"foundry/internal/domain"
)

func hookState(q capabilityReader) (domain.HookProcessorState, error) {
	var st domain.HookProcessorState
	err := q.QueryRow(`SELECT initialized_after_seq,cursor,last_processed_at,(SELECT COALESCE(MAX(seq),0) FROM events),(SELECT COUNT(*) FROM events WHERE seq>h.cursor) FROM hook_processor_state h WHERE id=1`).Scan(&st.InitializedAfterSeq, &st.Cursor, &st.LastProcessedAt, &st.LatestEventSeq, &st.PendingEvents)
	if err != nil {
		return st, err
	}
	if st.InitializedAfterSeq < 0 || st.Cursor < st.InitializedAfterSeq || st.Cursor > st.LatestEventSeq || st.PendingEvents < 0 || (st.LastProcessedAt != "" && !validHookTime(st.LastProcessedAt)) {
		return st, errors.New("invalid stored hook processor state")
	}
	return st, nil
}
func (s *Store) HookState() (domain.HookProcessorState, error) { return hookState(s.db) }

func validHookRecord(r domain.HookRecord) bool {
	return r.ID > 0 && r.EventSeq > 0 && strings.TrimSpace(r.MissionID) != "" && r.MissionRevision > 0 && validHookKind(r.EventKind) && validHookTime(r.EventCreatedAt) && validHookID(r.RuleID) && r.RuleRevision > 0 && validHookName(r.RuleName) && r.Action == "record_activity" && r.HandlerVersion == "record_activity/v1" && r.Status == "recorded" && validHookTime(r.ProcessedAt)
}

func (s *Store) HookRecords(missionID string, before int64, limit int) (domain.HookRecordPage, error) {
	page := domain.HookRecordPage{Records: make([]domain.HookRecord, 0)}
	if before < 0 || limit < 0 {
		return page, domain.ErrInvalidHook
	}
	if limit == 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if missionID != "" {
		if err := hookMissionExists(s.db, missionID); err != nil {
			return page, err
		}
	}
	rows, err := s.db.Query(`SELECT id,event_seq,mission_id,mission_revision,event_kind,event_created_at,rule_id,rule_revision,rule_name,action,handler_version,status,processed_at FROM hook_records WHERE (?='' OR mission_id=?) AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?`, missionID, missionID, before, before, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var r domain.HookRecord
		if err = rows.Scan(&r.ID, &r.EventSeq, &r.MissionID, &r.MissionRevision, &r.EventKind, &r.EventCreatedAt, &r.RuleID, &r.RuleRevision, &r.RuleName, &r.Action, &r.HandlerVersion, &r.Status, &r.ProcessedAt); err != nil {
			return domain.HookRecordPage{}, err
		}
		if !validHookRecord(r) {
			return domain.HookRecordPage{}, errors.New("invalid stored hook record")
		}
		page.Records = append(page.Records, r)
	}
	if err = rows.Err(); err != nil {
		return domain.HookRecordPage{}, err
	}
	if len(page.Records) > limit {
		page.Records = page.Records[:limit]
		page.NextBefore = page.Records[limit-1].ID
	}
	return page, nil
}

// ProcessHookEvents observes committed events in a separate bounded transaction.
// No Mission is loaded or mutated, and results never re-enter the source log.
func (s *Store) ProcessHookEvents(ctx context.Context, limit int) (domain.HookProcessorState, error) {
	zero := domain.HookProcessorState{}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	st, err := hookState(tx)
	if err != nil {
		return zero, err
	}
	events, err := hookEvents(ctx, tx, st.Cursor, limit)
	if err != nil {
		return zero, err
	}
	if len(events) == 0 {
		return st, nil
	}
	processedAt := timestamp()
	for _, event := range events {
		if event.Revision < 1 || strings.TrimSpace(event.MissionID) == "" || !validHookTime(event.CreatedAt) {
			return zero, errors.New("invalid stored hook source event")
		}
		// Highest revision below the event watermark wins, including revisions
		// saved at the same boundary. Disabled versions still supersede old ones.
		rows, e := tx.QueryContext(ctx, `SELECT r.id,r.revision,r.effective_after_seq,r.data FROM hook_rule_revisions r WHERE r.effective_after_seq<? AND NOT EXISTS (SELECT 1 FROM hook_rule_revisions n WHERE n.id=r.id AND n.effective_after_seq<? AND n.revision>r.revision) ORDER BY r.id`, event.Seq, event.Seq)
		if e != nil {
			return zero, e
		}
		rules, e := readHookRules(rows)
		if e != nil {
			return zero, e
		}
		for _, r := range rules {
			if !r.Enabled || (r.MissionID != "" && r.MissionID != event.MissionID) {
				continue
			}
			matches := false
			for _, kind := range r.EventKinds {
				if kind == event.Kind {
					matches = true
					break
				}
			}
			if !matches {
				continue
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO hook_records(event_seq,mission_id,mission_revision,event_kind,event_created_at,rule_id,rule_revision,rule_name,action,handler_version,status,processed_at) VALUES(?,?,?,?,?,?,?,?,?,'record_activity/v1','recorded',?) ON CONFLICT(event_seq,rule_id) DO NOTHING`, event.Seq, event.MissionID, event.Revision, event.Kind, event.CreatedAt, r.ID, r.Revision, r.Name, r.Action, processedAt)
			if err != nil {
				return zero, err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE hook_processor_state SET cursor=?,last_processed_at=? WHERE id=1`, events[len(events)-1].Seq, processedAt); err != nil {
		return zero, err
	}
	st, err = hookState(tx)
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return st, nil
}

func hookEvents(ctx context.Context, tx *sql.Tx, cursor int64, limit int) ([]domain.Event, error) {
	rows, err := tx.QueryContext(ctx, `SELECT seq,mission_id,kind,created_at,revision FROM events WHERE seq>? ORDER BY seq LIMIT ?`, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]domain.Event, 0, limit)
	for rows.Next() {
		var e domain.Event
		if err = rows.Scan(&e.Seq, &e.MissionID, &e.Kind, &e.CreatedAt, &e.Revision); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
