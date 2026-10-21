package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
)

func (s *Store) initializeRepositoryInspections() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS repository_inspections (id TEXT PRIMARY KEY, data BLOB NOT NULL)`)
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
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT data FROM git_repositories WHERE id=?`, v.RepositoryID).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			return zero, ErrNotFound
		} else if err != nil {
			return zero, err
		}
		var repo domain.GitRepository
		if err = json.Unmarshal(raw, &repo); err != nil {
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
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM git_repositories WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.GitRepository{}, ErrNotFound
	}
	if err != nil {
		return domain.GitRepository{}, err
	}
	var repo domain.GitRepository
	if err = json.Unmarshal(raw, &repo); err != nil {
		return domain.GitRepository{}, err
	}
	return repo, nil
}

func (s *Store) GetRepositoryInspection(id string) (domain.RepositoryInspection, error) {
	var raw []byte
	err := s.db.QueryRow(`SELECT CASE WHEN length(data)<=? THEN data ELSE NULL END FROM repository_inspections WHERE id=?`, domain.MaxRepositoryInspectionBytes, id).Scan(&raw)
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
	rows, err := s.db.Query(`SELECT id,CASE WHEN length(data)<=? THEN data ELSE NULL END FROM repository_inspections ORDER BY rowid DESC LIMIT 100`, domain.MaxRepositoryInspectionBytes)
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
