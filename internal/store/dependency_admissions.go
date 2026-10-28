package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/dependencyadmission"
	"foundry/internal/domain"
	"reflect"
)

const MaxDependencyAdmissionBytes = 4 << 20

func (s *Store) SaveDependencyAdmission(a dependencyadmission.AdmissionReceipt) error {
	raw, err := json.Marshal(a)
	if err != nil || len(raw) > MaxDependencyAdmissionBytes || a.Validate() != nil {
		return domain.ImportDependencies
	}
	_, err = s.db.Exec(`INSERT INTO dependency_admissions(digest,data) VALUES(?,?) ON CONFLICT(digest) DO NOTHING`, a.Digest, raw)
	if err != nil {
		return err
	}
	saved, err := s.GetDependencyAdmission(a.Digest)
	if err != nil || !reflect.DeepEqual(a, saved) {
		return domain.ImportDependencies
	}
	return nil
}
func readDependencyAdmission(ctx context.Context, q inspectionRegistryReader, digest string) (dependencyadmission.AdmissionReceipt, error) {
	var raw []byte
	var a dependencyadmission.AdmissionReceipt
	err := q.QueryRowContext(ctx, `SELECT CASE WHEN length(data)<=? THEN data ELSE NULL END FROM dependency_admissions WHERE digest=?`, MaxDependencyAdmissionBytes, digest).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	if decodeStoredJSON(raw, &a) != nil || a.Validate() != nil || a.Digest != digest {
		return dependencyadmission.AdmissionReceipt{}, domain.ImportDependencies
	}
	return a, nil
}
func (s *Store) GetDependencyAdmission(digest string) (dependencyadmission.AdmissionReceipt, error) {
	return readDependencyAdmission(context.Background(), s.db, digest)
}
