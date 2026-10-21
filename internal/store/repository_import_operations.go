package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"reflect"
)

func (s *Store) initializeRepositoryImports() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS repository_import_operations (id TEXT PRIMARY KEY, inspection_id TEXT NOT NULL UNIQUE, mission_id TEXT NOT NULL UNIQUE, revision INTEGER NOT NULL, status TEXT NOT NULL, data BLOB NOT NULL, FOREIGN KEY(inspection_id) REFERENCES repository_inspections(id))`)
	return err
}

func readRepositoryImport(ctx context.Context, q inspectionRegistryReader, key, value string) (domain.RepositoryImportOperation, error) {
	var o domain.RepositoryImportOperation
	var id, inspectionID, missionID, status string
	var revision int64
	var raw []byte
	// key is supplied only by the fixed internal callers below.
	err := q.QueryRowContext(ctx, `SELECT id,inspection_id,mission_id,revision,status,CASE WHEN length(data)<=65536 THEN data ELSE NULL END FROM repository_import_operations WHERE `+key+`=?`, value).Scan(&id, &inspectionID, &missionID, &revision, &status, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrNotFound
	}
	if err != nil {
		return o, err
	}
	if decodeStoredJSON(raw, &o) != nil || domain.ValidateRepositoryImportOperation(o) != nil || o.ID != id || o.InspectionID != inspectionID || o.MissionID != missionID || o.Status != status || o.Revision != revision {
		return domain.RepositoryImportOperation{}, domain.ImportInvalidBinding
	}
	return o, nil
}
func (s *Store) GetRepositoryImportOperation(id string) (domain.RepositoryImportOperation, error) {
	return readRepositoryImport(context.Background(), s.db, "id", id)
}
func (s *Store) GetRepositoryImportByInspection(id string) (domain.RepositoryImportOperation, error) {
	return readRepositoryImport(context.Background(), s.db, "inspection_id", id)
}
func (s *Store) GetRepositoryImportByMission(id string) (domain.RepositoryImportOperation, error) {
	return readRepositoryImport(context.Background(), s.db, "mission_id", id)
}
func (s *Store) ListRepositoryImportOperations() ([]domain.RepositoryImportOperation, error) {
	rows, err := s.db.Query(`SELECT id FROM repository_import_operations ORDER BY rowid DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]domain.RepositoryImportOperation, 0, len(ids))
	for _, id := range ids {
		o, err := s.GetRepositoryImportOperation(id)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func readImportInspection(ctx context.Context, q inspectionRegistryReader, id string) (domain.RepositoryInspection, error) {
	var v domain.RepositoryInspection
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT CASE WHEN length(data)<=? THEN data ELSE NULL END FROM repository_inspections WHERE id=?`, domain.MaxRepositoryInspectionBytes, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if decodeStoredJSON(raw, &v) != nil || v.ID != id || domain.ValidateRepositoryInspection(v) != nil || v.Status != "ready" {
		return v, domain.ImportInvalidRecord
	}
	return v, nil
}
func recheckImportRegistry(ctx context.Context, tx *sql.Tx, v domain.RepositoryInspection, sourcePath string) error {
	if v.RepositoryID == "" {
		return nil
	}
	repo, err := readGitRepositoryForInspection(ctx, tx, v.RepositoryID)
	if err != nil {
		return err
	}
	if repo.Revision != v.RepositoryRevision || repo.Path != sourcePath {
		return domain.ImportSourceChanged
	}
	return nil
}
func (s *Store) BeginRepositoryImport(ctx context.Context, o domain.RepositoryImportOperation, v domain.RepositoryInspection, sourcePath string) (domain.RepositoryImportOperation, error) {
	zero := domain.RepositoryImportOperation{}
	o.Status = "pending"
	o.Stage = "reserve"
	o.Code = ""
	o.Revision = 1
	o.CreatedAt = timestamp()
	o.UpdatedAt = o.CreatedAt
	if domain.ValidateRepositoryImportOperation(o) != nil || o.InspectionID != v.ID {
		return zero, domain.ImportInvalidBinding
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	saved, err := readImportInspection(ctx, tx, v.ID)
	if err != nil {
		return zero, err
	}
	if !reflect.DeepEqual(v, saved) {
		return zero, domain.ImportInvalidRecord
	}
	if err = recheckImportRegistry(ctx, tx, v, sourcePath); err != nil {
		return zero, err
	}
	raw, _ := json.Marshal(o)
	if _, err = tx.ExecContext(ctx, `INSERT INTO repository_import_operations(id,inspection_id,mission_id,revision,status,data) VALUES(?,?,?,?,?,?)`, o.ID, o.InspectionID, o.MissionID, o.Revision, o.Status, raw); err != nil {
		return zero, capabilityWriteError(err)
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return o, nil
}

func writeRepositoryImport(tx *sql.Tx, before, after domain.RepositoryImportOperation) error {
	if domain.ValidateRepositoryImportOperation(after) != nil {
		return domain.ImportInvalidBinding
	}
	raw, _ := json.Marshal(after)
	result, err := tx.Exec(`UPDATE repository_import_operations SET revision=?,status=?,data=? WHERE id=? AND revision=? AND status=?`, after.Revision, after.Status, raw, before.ID, before.Revision, before.Status)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) changeRepositoryImport(o domain.RepositoryImportOperation, mutate func(*domain.RepositoryImportOperation)) (domain.RepositoryImportOperation, error) {
	zero := domain.RepositoryImportOperation{}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	current, err := readRepositoryImport(context.Background(), tx, "id", o.ID)
	if err != nil {
		return zero, err
	}
	if !reflect.DeepEqual(current, o) || o.Status != "pending" && o.Status != "prepared" {
		return zero, ErrConflict
	}
	mutate(&current)
	current.Revision++
	current.UpdatedAt = timestamp()
	if current.Binding != nil {
		v, err := readImportInspection(context.Background(), tx, current.InspectionID)
		if err != nil {
			return zero, err
		}
		if err = validateImportReceiptBinding(v, *current.Binding); err != nil {
			return zero, err
		}
	}
	if err = writeRepositoryImport(tx, o, current); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return current, nil
}
func (s *Store) PrepareRepositoryImport(o domain.RepositoryImportOperation, b domain.RepositoryImportBinding, g domain.GenerationRef) (domain.RepositoryImportOperation, error) {
	if o.Status != "pending" {
		return domain.RepositoryImportOperation{}, ErrConflict
	}
	return s.changeRepositoryImport(o, func(o *domain.RepositoryImportOperation) {
		o.Status = "prepared"
		o.Stage = "prepare"
		o.Binding = &b
		o.Generation = &g
	})
}
func (s *Store) FailRepositoryImport(o domain.RepositoryImportOperation, stage string, code domain.ImportError) (domain.RepositoryImportOperation, error) {
	return s.changeRepositoryImport(o, func(o *domain.RepositoryImportOperation) { o.Status = "failed"; o.Stage = stage; o.Code = code })
}
func validateImportReceiptBinding(v domain.RepositoryInspection, b domain.RepositoryImportBinding) error {
	if domain.ValidateRepositoryImportBinding(b) != nil || v.ID != b.InspectionID || v.Digest != b.InspectionDigest || v.Root != b.Root || v.Commit != b.Commit || domain.RepositoryImportSpecDigest(v.Spec) != b.ImportSpecDigest {
		return domain.ImportInvalidBinding
	}
	return nil
}
func validateMissionImport(ctx context.Context, q inspectionRegistryReader, m domain.Mission) error {
	if err := domain.ValidateMissionRepositoryImport(m); err != nil {
		return err
	}
	o, err := readRepositoryImport(ctx, q, "mission_id", m.ID)
	if errors.Is(err, ErrNotFound) && m.ImportVersion == 0 {
		return nil
	}
	if err != nil {
		return domain.ImportInvalidBinding
	}
	if o.Status != "committed" || m.ImportVersion != 1 || !reflect.DeepEqual(o.Binding, m.RepositoryImport) {
		return domain.ImportInvalidBinding
	}
	v, err := readImportInspection(ctx, q, o.InspectionID)
	if err != nil {
		return domain.ImportInvalidBinding
	}
	return validateImportReceiptBinding(v, *o.Binding)
}

// This read-only relational check also detects stripping BOTH mission fields.
func (s *Store) ValidateMissionRepositoryImport(m domain.Mission) error {
	return validateMissionImport(context.Background(), s.db, m)
}
func (s *Store) CommitRepositoryImport(m domain.Mission, o domain.RepositoryImportOperation, sourcePath string) (domain.Mission, error) {
	return s.createMission(m, &o, sourcePath)
}

func commitRepositoryImportTx(tx *sql.Tx, m domain.Mission, o domain.RepositoryImportOperation, sourcePath string) error {
	current, err := readRepositoryImport(context.Background(), tx, "id", o.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, o) || o.Status != "prepared" || o.MissionID != m.ID || domain.ValidateMissionRepositoryImport(m) != nil || m.ImportVersion != 1 || !reflect.DeepEqual(o.Binding, m.RepositoryImport) || !reflect.DeepEqual(o.Generation, m.CandidateGeneration) || m.CandidateDigest != o.Binding.BaseDigest {
		return domain.ImportInvalidBinding
	}
	v, err := readImportInspection(context.Background(), tx, o.InspectionID)
	if err != nil {
		return err
	}
	if err = validateImportReceiptBinding(v, *o.Binding); err != nil {
		return err
	}
	if err = recheckImportRegistry(context.Background(), tx, v, sourcePath); err != nil {
		return err
	}
	current.Status = "committed"
	current.Stage = "complete"
	current.Revision++
	current.UpdatedAt = timestamp()
	return writeRepositoryImport(tx, o, current)
}
