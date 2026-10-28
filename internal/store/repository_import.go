package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"path/filepath"
)

func (s *Store) initializeRepositoryInspections() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS repository_inspections (id TEXT PRIMARY KEY, data BLOB NOT NULL); CREATE TABLE IF NOT EXISTS dependency_admissions (digest TEXT PRIMARY KEY, data BLOB NOT NULL)`)
	return err
}

// SaveRepositoryInspection is insert-only. For registered sources, checking the
// exact path/revision and inserting the receipt share one SQLite transaction.
func (s *Store) SaveRepositoryInspection(v domain.RepositoryInspection, sourcePath string) (domain.RepositoryInspection, error) {
	return s.SaveRepositoryInspectionContext(context.Background(), v, sourcePath)
}

// The inspection engine supplies its original whole-operation deadline, including
// connection-pool waits. Caller cancellation can be journaled within that deadline.
func (s *Store) SaveRepositoryInspectionContext(ctx context.Context, v domain.RepositoryInspection, sourcePath string) (domain.RepositoryInspection, error) {
	zero := domain.RepositoryInspection{}
	if err := domain.ValidateRepositoryInspection(v); err != nil {
		return zero, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	if v.RepositoryID != "" {
		repo, err := readGitRepositoryForInspection(ctx, tx, v.RepositoryID)
		if err != nil {
			return zero, err
		}
		if repo.Revision != v.RepositoryRevision || repo.Path != sourcePath {
			return zero, domain.ImportSourceChanged
		}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return zero, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO repository_inspections(id,data) VALUES(?,?)`, v.ID, raw); err != nil {
		return zero, capabilityWriteError(err)
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return v, nil
}

func (s *Store) GetGitRepositoryForInspection(ctx context.Context, id string) (domain.GitRepository, error) {
	return readGitRepositoryForInspection(ctx, s.db, id)
}

type inspectionRegistryReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Both admission lookup and insertion recheck bind strictly decoded payloads to
// authoritative SQLite columns. Bound transfer before decoding damaged data.
func readGitRepositoryForInspection(ctx context.Context, q inspectionRegistryReader, id string) (domain.GitRepository, error) {
	var raw []byte
	var rowID string
	var revision int64
	err := q.QueryRowContext(ctx, `SELECT id,revision,CASE WHEN length(data)<=65536 THEN data ELSE NULL END FROM git_repositories WHERE id=?`, id).Scan(&rowID, &revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.GitRepository{}, ErrNotFound
	}
	if err != nil {
		return domain.GitRepository{}, err
	}
	var repo domain.GitRepository
	if decodeStoredJSON(raw, &repo) != nil || rowID != id || repo.ID != rowID || !catalogID(repo.ID) || revision < 1 || repo.Revision != revision || !validCapabilityText(repo.Name, 200, true) || !validCapabilityText(repo.Path, 4096, true) || !filepath.IsAbs(repo.Path) || repo.ProjectID != "" && !catalogID(repo.ProjectID) || !validCatalogTime(repo.CreatedAt) || !validCatalogTime(repo.UpdatedAt) {
		return domain.GitRepository{}, domain.ImportInvalidRecord
	}
	return repo, nil
}

func (s *Store) GetRepositoryInspection(id string) (domain.RepositoryInspection, error) {
	var raw []byte
	err := s.db.QueryRow(`SELECT CASE WHEN length(data)<=? THEN data ELSE NULL END FROM repository_inspections WHERE id=?`, domain.MaxRepositoryInspectionBytesV2, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RepositoryInspection{}, ErrNotFound
	}
	if err != nil {
		return domain.RepositoryInspection{}, err
	}
	var v domain.RepositoryInspection
	if decodeStoredJSON(raw, &v) != nil {
		return domain.RepositoryInspection{}, domain.ImportInvalidRecord
	}
	if v.ID != id {
		return domain.RepositoryInspection{}, domain.ImportInvalidRecord
	}
	if err = domain.ValidateRepositoryInspection(v); err != nil {
		return domain.RepositoryInspection{}, err
	}
	return v, nil
}

// Stored history is bounded and never causes a source reread.
func (s *Store) ListRepositoryInspections() ([]domain.RepositoryInspection, error) {
	rows, err := s.db.Query(`SELECT id,CASE WHEN length(data)<=? THEN data ELSE NULL END FROM repository_inspections ORDER BY rowid DESC LIMIT 100`, domain.MaxRepositoryInspectionBytesV2)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RepositoryInspection, 0)
	for rows.Next() {
		var id string
		var raw []byte
		var v domain.RepositoryInspection
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		if decodeStoredJSON(raw, &v) != nil {
			return nil, domain.ImportInvalidRecord
		}
		if v.ID != id {
			return nil, domain.ImportInvalidRecord
		}
		if err = domain.ValidateRepositoryInspection(v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
