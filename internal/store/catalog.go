package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"time"
	"unicode/utf8"

	"foundry/internal/domain"
	"github.com/mattn/go-sqlite3"
)

var ErrCatalogCorrupt = errors.New("catalog storage is corrupt")

func (s *Store) initializeCatalog() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	count, err := catalogTableCount(tx)
	if err != nil {
		return err
	}
	// Existing partial or empty installations are never repaired here. Catalog reads
	// diagnose them, without making catalog health a prerequisite for old V1 reads.
	if count != 0 {
		return tx.Commit()
	}
	if _, err = tx.Exec(`CREATE TABLE catalog (id INTEGER PRIMARY KEY CHECK(id=1),revision INTEGER NOT NULL CHECK(revision>=0),data BLOB NOT NULL);
 CREATE TABLE catalog_events (seq INTEGER PRIMARY KEY AUTOINCREMENT,catalog_revision INTEGER NOT NULL UNIQUE,kind TEXT NOT NULL,entity_ids BLOB NOT NULL,created_at TEXT NOT NULL);`); err != nil {
		return err
	}
	raw, err := json.Marshal(domain.EmptyCatalog())
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO catalog(id,revision,data) VALUES(1,0,?)`, raw); err != nil {
		return err
	}
	return tx.Commit()
}

func catalogTableCount(tx *sql.Tx) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('catalog','catalog_events')`).Scan(&n)
	return n, err
}

func (s *Store) Catalog() (domain.CatalogSnapshot, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.CatalogSnapshot{}, err
	}
	defer tx.Rollback()
	c, _, err := readCatalog(tx)
	if err != nil {
		return domain.CatalogSnapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.CatalogSnapshot{}, err
	}
	return c, nil
}

func (s *Store) CatalogEvents(after int64, limit int) ([]domain.CatalogEvent, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: invalid event pagination", domain.ErrInvalidCatalog)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, events, err := readCatalog(tx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.CatalogEvent, 0)
	for _, event := range events {
		if event.Seq > after {
			result = append(result, event)
			if len(result) == limit {
				break
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func readCatalog(tx *sql.Tx) (domain.CatalogSnapshot, []domain.CatalogEvent, error) {
	fail := func() (domain.CatalogSnapshot, []domain.CatalogEvent, error) {
		return domain.CatalogSnapshot{}, nil, ErrCatalogCorrupt
	}
	count, err := catalogTableCount(tx)
	if err != nil {
		return domain.CatalogSnapshot{}, nil, err
	}
	if count != 2 {
		return fail()
	}
	var revision int64
	var data []byte
	err = tx.QueryRow(`SELECT revision,data FROM catalog WHERE id=1`).Scan(&revision, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return fail()
	}
	if err != nil {
		return domain.CatalogSnapshot{}, nil, catalogReadError(err)
	}
	var rowsCount int
	if err = tx.QueryRow(`SELECT count(*) FROM catalog`).Scan(&rowsCount); err != nil {
		return domain.CatalogSnapshot{}, nil, catalogReadError(err)
	}
	if rowsCount != 1 {
		return fail()
	}
	var c domain.CatalogSnapshot
	if len(data) > 1048576 || decodeStoredJSON(data, &c) != nil || c.Revision != revision || domain.ValidateCatalog(c) != nil {
		return fail()
	}
	if err = checkCatalogMissions(tx, c); err != nil {
		if errors.Is(err, ErrNotFound) {
			return fail()
		}
		return domain.CatalogSnapshot{}, nil, err
	}
	rows, err := tx.Query(`SELECT seq,catalog_revision,kind,entity_ids,created_at FROM catalog_events ORDER BY catalog_revision`)
	if err != nil {
		return domain.CatalogSnapshot{}, nil, catalogReadError(err)
	}
	defer rows.Close()
	events := make([]domain.CatalogEvent, 0)
	var lastSeq int64
	for rows.Next() {
		var e domain.CatalogEvent
		var ids []byte
		if err = rows.Scan(&e.Seq, &e.CatalogRevision, &e.Kind, &ids, &e.CreatedAt); err != nil {
			return fail()
		}
		if e.CatalogRevision != int64(len(events))+1 || e.CatalogRevision > revision || e.Seq <= lastSeq || !catalogKind(e.Kind) || decodeStoredJSON(ids, &e.EntityIDs) != nil || len(e.EntityIDs) == 0 || !validCatalogTime(e.CreatedAt) {
			return fail()
		}
		for i, id := range e.EntityIDs {
			if !catalogID(id) || (i > 0 && e.EntityIDs[i-1] >= id) {
				return fail()
			}
		}
		lastSeq = e.Seq
		events = append(events, e)
	}
	if err = rows.Err(); err != nil {
		return domain.CatalogSnapshot{}, nil, err
	}
	if int64(len(events)) != revision {
		return fail()
	}
	return c, events, nil
}

// Structural/decoding failures are catalog corruption; genuine SQLite I/O,
// locking and connection failures remain storage errors, not input errors.
func catalogReadError(err error) error {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		if sqliteErr.Code == sqlite3.ErrError || sqliteErr.Code == sqlite3.ErrCorrupt || sqliteErr.Code == sqlite3.ErrSchema {
			return ErrCatalogCorrupt
		}
		return err
	}
	if errors.Is(err, sql.ErrConnDone) || errors.Is(err, sql.ErrTxDone) {
		return err
	}
	return ErrCatalogCorrupt
}

func checkCatalogMissions(tx *sql.Tx, c domain.CatalogSnapshot) error {
	for _, link := range c.MissionLinks {
		var exists int
		err := tx.QueryRow(`SELECT 1 FROM missions WHERE id=?`, link.MissionID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpdateCatalog(expected int64, kind string, mutate func(*domain.CatalogSnapshot) ([]string, error)) (domain.CatalogSnapshot, error) {
	zero := domain.CatalogSnapshot{}
	if mutate == nil {
		return zero, fmt.Errorf("%w: mutation is required", domain.ErrInvalidCatalog)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	before, _, err := readCatalog(tx)
	if err != nil {
		return zero, err
	}
	if before.Revision != expected {
		return zero, ErrConflict
	}
	raw, err := json.Marshal(before)
	if err != nil {
		return zero, err
	}
	var after domain.CatalogSnapshot
	if err = json.Unmarshal(raw, &after); err != nil {
		return zero, err
	}
	affected, err := mutate(&after)
	if err != nil {
		return zero, err
	}
	if err = domain.ValidateCatalogChange(before, after, kind); err != nil {
		return zero, err
	}
	if err = checkCatalogMissions(tx, after); err != nil {
		return zero, err
	}
	changed := catalogChanges(before, after)
	if len(changed) == 0 {
		if len(affected) != 0 {
			return zero, fmt.Errorf("%w: no-op must have no affected IDs", domain.ErrInvalidCatalog)
		}
		if err = tx.Commit(); err != nil {
			return zero, err
		}
		return before, nil
	}
	now := timestamp()
	stampCatalog(&after, before, now)
	after.Revision++
	if err = domain.ValidateCatalog(after); err != nil {
		return zero, err
	}
	affected = slices.Clone(affected)
	slices.Sort(affected)
	affected = slices.Compact(affected)
	if !slices.Equal(changed, affected) {
		return zero, fmt.Errorf("%w: affected IDs do not match change", domain.ErrInvalidCatalog)
	}
	raw, err = json.Marshal(after)
	if err != nil {
		return zero, err
	}
	result, err := tx.Exec(`UPDATE catalog SET revision=?,data=? WHERE id=1 AND revision=?`, after.Revision, raw, expected)
	if err != nil {
		return zero, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return zero, err
	}
	if n != 1 {
		return zero, ErrConflict
	}
	ids, err := json.Marshal(affected)
	if err != nil {
		return zero, err
	}
	if _, err = tx.Exec(`INSERT INTO catalog_events(catalog_revision,kind,entity_ids,created_at) VALUES(?,?,?,?)`, after.Revision, kind, ids, now); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return after, nil
}

type catalogRecord struct {
	value any
	meta  *domain.CatalogMeta
}

func catalogRecords(c *domain.CatalogSnapshot) map[string]catalogRecord {
	records := map[string]catalogRecord{}
	for i := range c.Projects {
		v := &c.Projects[i]
		records[v.ID] = catalogRecord{*v, &v.CatalogMeta}
	}
	for i := range c.Targets {
		v := &c.Targets[i]
		records[v.ID] = catalogRecord{*v, &v.CatalogMeta}
	}
	for i := range c.Environments {
		v := &c.Environments[i]
		records[v.ID] = catalogRecord{*v, &v.CatalogMeta}
	}
	for i := range c.Resources {
		v := &c.Resources[i]
		records[v.ID] = catalogRecord{*v, &v.CatalogMeta}
	}
	for i := range c.Bindings {
		v := &c.Bindings[i]
		records[v.ID] = catalogRecord{*v, &v.CatalogMeta}
	}
	return records
}

func catalogChanges(before, after domain.CatalogSnapshot) []string {
	old, new := catalogRecords(&before), catalogRecords(&after)
	ids := []string{}
	for id, v := range old {
		if next, ok := new[id]; !ok || !reflect.DeepEqual(v.value, next.value) {
			ids = append(ids, id)
		}
	}
	for id := range new {
		if _, ok := old[id]; !ok {
			ids = append(ids, id)
		}
	}
	links := map[string]domain.MissionProjectLink{}
	for _, link := range before.MissionLinks {
		links[link.MissionID] = link
	}
	for _, link := range after.MissionLinks {
		prior, ok := links[link.MissionID]
		if !ok || prior != link {
			ids = append(ids, link.MissionID)
		}
		delete(links, link.MissionID)
	}
	for id := range links {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func stampCatalog(after *domain.CatalogSnapshot, before domain.CatalogSnapshot, now string) {
	old := catalogRecords(&before)
	for id, next := range catalogRecords(after) {
		prior, exists := old[id]
		if !exists {
			next.meta.Revision = 1
			next.meta.CreatedAt = now
			next.meta.UpdatedAt = now
		} else if !reflect.DeepEqual(prior.value, next.value) {
			next.meta.Revision++
			next.meta.UpdatedAt = now
		}
	}
	for i := range after.MissionLinks {
		if after.MissionLinks[i].LinkedAt == "" {
			after.MissionLinks[i].LinkedAt = now
		}
	}
}

func catalogKind(kind string) bool {
	switch kind {
	case "project.create", "project.update", "target.create", "target.rename", "environment.create", "environment.rename", "resource.create", "resource.rename", "resource.bind", "resource.unbind", "mission.link", "mission.relink", "mission.unlink":
		return true
	}
	return false
}
func catalogID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func validCatalogTime(value string) bool {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return false
	}
	_, offset := t.Zone()
	return offset == 0
}

// Compare the decoded JSON shape with the typed round trip: this rejects absent,
// null, unknown and mis-cased persisted fields instead of filling Go zero values.
func decodeStoredJSON(raw []byte, out any) error {
	if !utf8.Valid(raw) {
		return ErrCatalogCorrupt
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	original, err := storedJSONValue(dec)
	if err != nil {
		return ErrCatalogCorrupt
	}
	if _, err = dec.Token(); err != io.EOF {
		return ErrCatalogCorrupt
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return ErrCatalogCorrupt
	}
	canonical, err := json.Marshal(out)
	if err != nil {
		return ErrCatalogCorrupt
	}
	otherDec := json.NewDecoder(bytes.NewReader(canonical))
	otherDec.UseNumber()
	other, err := storedJSONValue(otherDec)
	if err != nil || !reflect.DeepEqual(original, other) {
		return ErrCatalogCorrupt
	}
	return nil
}

func storedJSONValue(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		value := map[string]any{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, ErrCatalogCorrupt
			}
			if _, exists := value[name]; exists {
				return nil, ErrCatalogCorrupt
			}
			child, err := storedJSONValue(dec)
			if err != nil {
				return nil, err
			}
			value[name] = child
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return nil, ErrCatalogCorrupt
		}
		return value, nil
	case '[':
		value := []any{}
		for dec.More() {
			child, err := storedJSONValue(dec)
			if err != nil {
				return nil, err
			}
			value = append(value, child)
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim(']') {
			return nil, ErrCatalogCorrupt
		}
		return value, nil
	default:
		return nil, ErrCatalogCorrupt
	}
}
