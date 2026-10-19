package store

import (
	"database/sql"
	"foundry/internal/domain"
)

const modelRouteColumns = `id,account_id,adapter,protocol,base_url,environment,spend_class,extra_usage,created_at`

func (s *Store) initializeModelRoutes() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS model_routes (
 id TEXT PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 256), account_id TEXT NOT NULL REFERENCES model_accounts(id),
 adapter TEXT NOT NULL, protocol TEXT NOT NULL, base_url TEXT NOT NULL,
 environment TEXT NOT NULL CHECK(environment IN ('live','simulation')),
 spend_class TEXT NOT NULL CHECK(spend_class IN ('explicit_api','interactive_package','subscription_only')),
 extra_usage TEXT NOT NULL CHECK(extra_usage IN ('off','unknown')), created_at TEXT NOT NULL)`,
		`CREATE TRIGGER IF NOT EXISTS model_route_no_update BEFORE UPDATE ON model_routes BEGIN SELECT RAISE(ABORT,'immutable model route'); END`,
		`CREATE TRIGGER IF NOT EXISTS model_route_no_delete BEFORE DELETE ON model_routes BEGIN SELECT RAISE(ABORT,'immutable model route'); END`,
	} {
		if _, err = tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanModelRoute(row modelRowScanner) (domain.ModelRoute, error) {
	var r domain.ModelRoute
	err := row.Scan(&r.ID, &r.AccountID, &r.Adapter, &r.Protocol, &r.BaseURL, &r.Environment, &r.SpendClass, &r.ExtraUsage, &r.CreatedAt)
	return r, modelStoreError(err)
}

func modelRouteTx(tx *sql.Tx, id string) (domain.ModelRoute, error) {
	return scanModelRoute(tx.QueryRow(`SELECT `+modelRouteColumns+` FROM model_routes WHERE id=?`, id))
}

// IDs originate at the service/API boundary, never in model output. A route
// has no update/delete API: changes require a new route and a fresh snapshot.
func (s *Store) CreateModelRoute(r domain.ModelRoute) (domain.ModelRoute, error) {
	return s.createModelRoute(r, 0)
}

// CreateModelRouteAtAccountRevision acquires the account row as a writer before
// validating the immutable route. The no-op compare-and-swap prevents an
// account mutation from racing a route created from stale browser state.
func (s *Store) CreateModelRouteAtAccountRevision(r domain.ModelRoute, expectedRevision int64) (domain.ModelRoute, error) {
	if expectedRevision < 1 {
		return domain.ModelRoute{}, ErrConflict
	}
	return s.createModelRoute(r, expectedRevision)
}

func (s *Store) createModelRoute(r domain.ModelRoute, expectedRevision int64) (domain.ModelRoute, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ModelRoute{}, err
	}
	defer tx.Rollback()
	if expectedRevision > 0 {
		result, err := tx.Exec(`UPDATE model_accounts SET revision=revision WHERE id=? AND revision=?`, r.AccountID, expectedRevision)
		if err != nil {
			return domain.ModelRoute{}, modelStoreError(err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return domain.ModelRoute{}, err
		}
		if changed != 1 {
			return domain.ModelRoute{}, ErrConflict
		}
	}
	a, err := modelAccountTx(tx, r.AccountID)
	if err != nil {
		return domain.ModelRoute{}, err
	}
	if err = domain.ValidateModelRoute(r, a); err != nil {
		return domain.ModelRoute{}, err
	}
	r.CreatedAt = timestamp()
	if _, err = tx.Exec(`INSERT INTO model_routes (`+modelRouteColumns+`) VALUES(?,?,?,?,?,?,?,?,?)`, r.ID, r.AccountID, r.Adapter, r.Protocol, r.BaseURL, r.Environment, r.SpendClass, r.ExtraUsage, r.CreatedAt); err != nil {
		return domain.ModelRoute{}, modelStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return domain.ModelRoute{}, err
	}
	return r, nil
}

func (s *Store) GetModelRoute(id string) (domain.ModelRoute, error) {
	return scanModelRoute(s.db.QueryRow(`SELECT `+modelRouteColumns+` FROM model_routes WHERE id=?`, id))
}

func (s *Store) ListModelRoutes() ([]domain.ModelRoute, error) {
	rows, err := s.db.Query(`SELECT ` + modelRouteColumns + ` FROM model_routes ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	routes := make([]domain.ModelRoute, 0)
	for rows.Next() {
		r, err := scanModelRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, r)
	}
	return routes, rows.Err()
}
