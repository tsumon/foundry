package store

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"foundry/internal/domain"
)

func validHookName(s string) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > 80 {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validHookKind(kind string) bool {
	for _, p := range domain.HookPoints() {
		if p.Kind == kind {
			return true
		}
	}
	return false
}
func validHookTime(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}
func validHookID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16 && strings.ToLower(value) == value
}
func validateHookRule(r domain.HookRule) error {
	if !validHookID(r.ID) || !validHookName(r.Name) || r.Action != "record_activity" || len(r.EventKinds) == 0 || !utf8.ValidString(r.MissionID) || strings.ContainsRune(r.MissionID, 0) {
		return domain.ErrInvalidHook
	}
	seen := map[string]bool{}
	for _, k := range r.EventKinds {
		if !validHookKind(k) || seen[k] {
			return domain.ErrInvalidHook
		}
		seen[k] = true
	}
	return nil
}
func decodeHookRule(data []byte, id string, revision, boundary int64) (domain.HookRule, error) {
	var r domain.HookRule
	if !utf8.Valid(data) {
		return r, errors.New("invalid stored hook UTF-8")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return r, fmt.Errorf("decode stored hook fields: %w", err)
	}
	for _, key := range []string{"id", "revision", "name", "enabled", "event_kinds", "mission_id", "action", "effective_after_seq", "created_at", "updated_at"} {
		value, exists := fields[key]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return r, errors.New("incomplete stored hook rule")
		}
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, fmt.Errorf("decode stored hook: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return r, errors.New("invalid stored hook JSON")
	}
	if validateHookRule(r) != nil || r.ID != id || r.Revision != revision || r.Revision < 1 || r.EffectiveAfterSeq < 0 || (boundary >= 0 && r.EffectiveAfterSeq != boundary) || !validHookTime(r.CreatedAt) || !validHookTime(r.UpdatedAt) || !sort.StringsAreSorted(r.EventKinds) {
		return r, errors.New("invalid stored hook rule")
	}
	return r, nil
}

func (s *Store) SaveHookRule(r domain.HookRule, expectedRevision int64) (domain.HookRule, error) {
	zero := domain.HookRule{}
	if expectedRevision < 0 || validateHookRule(r) != nil {
		return zero, domain.ErrInvalidHook
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	var raw []byte
	var revision int64
	err = tx.QueryRow(`SELECT revision,data FROM hook_rules WHERE id=?`, r.ID).Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		if expectedRevision != 0 {
			return zero, ErrNotFound
		}
	} else if err != nil {
		return zero, err
	} else {
		old, e := decodeHookRule(raw, r.ID, revision, -1)
		if e != nil {
			return zero, e
		}
		if revision != expectedRevision {
			return zero, ErrConflict
		}
		r.CreatedAt = old.CreatedAt
	}
	if revision == 0 {
		var count int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM hook_rules`).Scan(&count); err != nil {
			return zero, err
		}
		if count >= 64 {
			return zero, domain.ErrInvalidHook
		}
		r.CreatedAt = timestamp()
	}
	if r.MissionID != "" {
		if err = hookMissionExists(tx, r.MissionID); err != nil {
			return zero, err
		}
	}
	if err = tx.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events`).Scan(&r.EffectiveAfterSeq); err != nil {
		return zero, err
	}
	r.Revision = revision + 1
	r.UpdatedAt = timestamp()
	r.EventKinds = append([]string(nil), r.EventKinds...)
	sort.Strings(r.EventKinds)
	data, err := json.Marshal(r)
	if err != nil {
		return zero, err
	}
	if revision == 0 {
		_, err = tx.Exec(`INSERT INTO hook_rules(id,revision,data) VALUES(?,?,?)`, r.ID, r.Revision, data)
	} else {
		var result sql.Result
		result, err = tx.Exec(`UPDATE hook_rules SET revision=?,data=? WHERE id=? AND revision=?`, r.Revision, data, r.ID, expectedRevision)
		if err == nil {
			var n int64
			n, err = result.RowsAffected()
			if err == nil && n != 1 {
				return zero, ErrConflict
			}
		}
	}
	if err != nil {
		return zero, err
	}
	if _, err = tx.Exec(`INSERT INTO hook_rule_revisions(id,revision,effective_after_seq,data) VALUES(?,?,?,?)`, r.ID, r.Revision, r.EffectiveAfterSeq, data); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return r, nil
}

func hookMissionExists(q capabilityReader, id string) error {
	var found int
	err := q.QueryRow(`SELECT 1 FROM missions WHERE id=?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *Store) HookRules() ([]domain.HookRule, error) {
	rows, err := s.db.Query(`SELECT id,revision,-1,data FROM hook_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return readHookRules(rows)
}

// Fully consume and close rows before any nested queries: Store has one connection.
func readHookRules(rows *sql.Rows) ([]domain.HookRule, error) {
	defer rows.Close()
	result := make([]domain.HookRule, 0)
	for rows.Next() {
		var id string
		var rev, boundary int64
		var data []byte
		if err := rows.Scan(&id, &rev, &boundary, &data); err != nil {
			return nil, err
		}
		r, err := decodeHookRule(data, id, rev, boundary)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
