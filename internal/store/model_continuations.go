package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"foundry/internal/domain"
	"reflect"
	"time"
)

func continuationEligibleTx(tx *sql.Tx, m domain.Mission) error {
	if m.CodingVersion != 1 || m.ToolMode != "" || !domain.IsInferenceSnapshot(m) || (m.State != "blocked" && m.State != "paused") || m.Plan != nil || len(m.Changes) != 0 || len(m.CodingAttempts) != 0 {
		return ErrConflict
	}
	for _, a := range m.Approvals {
		if a.Kind == "execute" {
			return ErrConflict
		}
	}
	for _, o := range m.C4Operations {
		if o.Dispatch != nil && o.Result == nil {
			return ErrConflict
		}
	}
	for _, o := range m.C4Controls {
		if o.Dispatch != nil && o.Result == nil {
			return ErrConflict
		}
	}
	var active bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM invocations WHERE mission_id=? AND status IN ('authorized_pending','dispatching')) OR EXISTS(SELECT 1 FROM local_operations WHERE mission_id=? AND status IN ('pending','prepared'))`, m.ID, m.ID).Scan(&active); err != nil {
		return err
	}
	if active {
		return ErrConflict
	}
	return nil
}

func continuationSelectionTx(tx *sql.Tx, m domain.Mission, authorizationID, catalogID string, now time.Time) (domain.MissionModelSnapshot, []domain.Role, domain.ModelAuthorization, error) {
	var snapshot domain.MissionModelSnapshot
	var zero domain.ModelAuthorization
	if domain.ValidateModelCredentialOperation(authorizationID) != nil || domain.ValidateModelCredentialOperation(catalogID) != nil {
		return snapshot, nil, zero, ErrConflict
	}
	current, err := domain.EffectiveModelSnapshot(m)
	if err != nil || current == nil {
		return snapshot, nil, zero, ErrConflict
	}
	raw, err := json.Marshal(current)
	if err != nil {
		return snapshot, nil, zero, err
	}
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return snapshot, nil, zero, err
	}
	auth, err := modelAuthorizationTx(tx, authorizationID)
	if err != nil || auth.State != "available" {
		return snapshot, nil, zero, ErrConflict
	}
	latest, err := currentModelAuthorizationTx(tx, auth.Binding.AccountID)
	if err != nil || !reflect.DeepEqual(latest, auth) {
		return snapshot, nil, zero, ErrConflict
	}
	expiry, err := time.Parse(time.RFC3339Nano, auth.ExpiresAt)
	if err != nil || !expiry.After(now) {
		return snapshot, nil, zero, ErrConflict
	}
	account, err := modelAccountTx(tx, auth.Binding.AccountID)
	if err != nil {
		return snapshot, nil, zero, err
	}
	roles := []domain.Role{}
	for _, role := range domain.RolePolicyRoles() {
		selection := snapshot.Roles[role]
		if selection.Account == nil || selection.Account.ID != account.ID {
			continue
		}
		selection.Account = &account
		selection.Authorization = &auth.Binding
		selection.Profile.AccountRevision = account.Revision
		selection.Profile.CatalogOperationID = catalogID
		proof, err := catalogSelectionTx(tx, selection.Profile, now)
		if err != nil {
			return snapshot, nil, zero, err
		}
		selection.Catalog = &proof
		if proof.AuthorizationGeneration != auth.Generation || proof.ExpiresAt != auth.ExpiresAt {
			return snapshot, nil, zero, ErrConflict
		}
		if _, _, err = resolveModelCredentialMetadataTx(tx, selection); err != nil {
			return snapshot, nil, zero, err
		}
		snapshot.Roles[role] = selection
		roles = append(roles, role)
	}
	snapshot.Digest, err = domain.ModelSnapshotDigest(snapshot)
	if err != nil {
		return snapshot, nil, zero, err
	}
	if err = domain.ValidateModelContinuationReplacement(*current, snapshot, roles); err != nil {
		return snapshot, nil, zero, ErrConflict
	}
	return snapshot, roles, auth, nil
}

func (s *Store) PrepareModelContinuation(id string, revision int64, authorizationID, catalogID string, now time.Time) (domain.Mission, error) {
	return s.modelContinuationMutation(id, revision, "model_continuation.prepared", func(tx *sql.Tx, m *domain.Mission) error {
		snapshot, roles, auth, err := continuationSelectionTx(tx, *m, authorizationID, catalogID, now)
		if err != nil {
			return err
		}
		current, err := domain.EffectiveModelSnapshot(*m)
		if err != nil {
			return err
		}
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return err
		}
		expiry, _ := time.Parse(time.RFC3339Nano, auth.ExpiresAt)
		if limit := now.Add(10 * time.Minute); limit.Before(expiry) {
			expiry = limit
		}
		c := domain.ModelContinuation{SchemaVersion: 1, ID: "continuation-" + hex.EncodeToString(random[:]), MissionID: id, PreparedRevision: revision, PredecessorDigest: current.Digest, Replacement: snapshot, AuthorizationGeneration: auth.Generation, AuthorizationExpiresAt: auth.ExpiresAt, AffectedRoles: roles, CreatedAt: now.UTC().Format(time.RFC3339Nano), ExpiresAt: expiry.UTC().Format(time.RFC3339Nano)}
		c.Digest = domain.ModelContinuationDigest(c)
		m.ModelContinuations = append(m.ModelContinuations, c)
		return nil
	})
}

func (s *Store) ApproveModelContinuation(id, continuationID string, revision int64, digest string, now time.Time) (domain.Mission, error) {
	return s.decideModelContinuation(id, continuationID, revision, digest, now, false)
}
func (s *Store) ApplyModelContinuation(id, continuationID string, revision int64, digest string, now time.Time) (domain.Mission, error) {
	return s.decideModelContinuation(id, continuationID, revision, digest, now, true)
}
func (s *Store) decideModelContinuation(id, continuationID string, revision int64, digest string, now time.Time, apply bool) (domain.Mission, error) {
	kind := "model_continuation.approved"
	if apply {
		kind = "model_continuation.applied"
	}
	return s.modelContinuationMutation(id, revision, kind, func(tx *sql.Tx, m *domain.Mission) error {
		if len(m.ModelContinuations) == 0 {
			return ErrConflict
		}
		c := &m.ModelContinuations[len(m.ModelContinuations)-1]
		expected := c.PreparedRevision + 1
		if apply {
			expected++
		}
		expiry, err := time.Parse(time.RFC3339Nano, c.ExpiresAt)
		created, _ := time.Parse(time.RFC3339Nano, c.CreatedAt)
		if err != nil || !expiry.After(now) || now.Before(created) || c.ID != continuationID || c.Digest != digest || revision != expected || c.Application != nil || (!apply && c.Approval != nil) || (apply && c.Approval == nil) {
			return ErrConflict
		}
		first := c.Replacement.Roles[c.AffectedRoles[0]]
		snapshot, roles, auth, err := continuationSelectionTx(tx, *m, first.Authorization.ID, first.Catalog.OperationID, now)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(snapshot, c.Replacement) || !reflect.DeepEqual(roles, c.AffectedRoles) || auth.Generation != c.AuthorizationGeneration || auth.ExpiresAt != c.AuthorizationExpiresAt {
			return ErrConflict
		}
		fact := &domain.ModelContinuationFact{Revision: revision + 1, Digest: digest, CreatedAt: now.UTC().Format(time.RFC3339Nano)}
		if apply {
			c.Application = fact
		} else {
			c.Approval = fact
		}
		return nil
	})
}

func (s *Store) modelContinuationMutation(id string, revision int64, kind string, mutate func(*sql.Tx, *domain.Mission) error) (domain.Mission, error) {
	if revision < 1 {
		return domain.Mission{}, ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Mission{}, err
	}
	defer tx.Rollback()
	// Acquire the writer before reading mission and account/catalog authority.
	if _, err = tx.Exec(`UPDATE missions SET revision=revision WHERE id=?`, id); err != nil {
		return domain.Mission{}, err
	}
	m, err := updateMissionTxAuthorized(tx, id, revision, kind, true, func(m *domain.Mission) error {
		if err := continuationEligibleTx(tx, *m); err != nil {
			return err
		}
		return mutate(tx, m)
	})
	if err != nil {
		return domain.Mission{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Mission{}, err
	}
	return m, nil
}
