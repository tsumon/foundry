package store

import (
	"database/sql"
	"foundry/internal/domain"
	"reflect"
)

func (s *Store) initializeInferenceBudgets() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS model_acceptance_inference_budgets(operation_id TEXT PRIMARY KEY REFERENCES model_acceptance_claims(operation_id), mission_id TEXT NOT NULL REFERENCES missions(id), revision INTEGER NOT NULL, binding_digest TEXT NOT NULL);
CREATE TRIGGER IF NOT EXISTS inference_budget_no_update BEFORE UPDATE ON model_acceptance_inference_budgets BEGIN SELECT RAISE(ABORT,'immutable inference budget'); END;
CREATE TRIGGER IF NOT EXISTS inference_budget_no_delete BEFORE DELETE ON model_acceptance_inference_budgets BEGIN SELECT RAISE(ABORT,'immutable inference budget'); END;`)
	return err
}

// This is a model-only diagnostic. Never require or run a VM for a draft that
// was not dispatched. Terminal unknown invocations remain consumed history.
func inferenceBudgetSnapshot(tx *sql.Tx, id string, material domain.ModelAcceptanceMaterial) (domain.Mission, *domain.MissionModelSnapshot, error) {
	m, err := readMissionRow(tx, id)
	if err != nil {
		return m, nil, err
	}
	if domain.ValidateC4Mission(m) != nil || domain.ValidateCodingTrace(m) != nil {
		return m, nil, ErrConflict
	}
	if (m.State != "paused" && m.State != "blocked") || m.Budget.UsedCalls < 0 || m.Budget.MaxCalls < 1 {
		return m, nil, ErrConflict
	}
	for _, o := range m.C4Operations {
		if o.Dispatch != nil && o.Result == nil {
			return m, nil, ErrConflict
		}
	}
	for _, o := range m.C4Controls {
		if o.Dispatch != nil && o.Result == nil {
			return m, nil, ErrConflict
		}
	}
	for _, o := range m.CodingAttempts {
		if o.Status == "running" {
			return m, nil, ErrConflict
		}
	}
	var active bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM invocations WHERE mission_id=? AND status IN ('authorized_pending','dispatching')) OR EXISTS(SELECT 1 FROM local_operations WHERE mission_id=? AND status IN ('pending','prepared'))`, id, id).Scan(&active); err != nil {
		return m, nil, err
	}
	if active {
		return m, nil, ErrConflict
	}
	snapshot, err := domain.EffectiveModelSnapshot(m)
	if err != nil || snapshot == nil || snapshot.SchemaVersion != 2 {
		return m, nil, ErrConflict
	}
	for _, selection := range snapshot.Roles {
		if selection.Account != nil && selection.Route != nil && selection.Authorization != nil && material.Authorization != nil && reflect.DeepEqual(*selection.Account, material.Account) && *selection.Route == material.Route && domain.SameModelAuthorizationBinding(*selection.Authorization, material.Authorization.Binding) && selection.Profile.Model == domain.OpenAIInferenceModel && selection.Profile.AccountID == material.Account.ID && selection.Profile.RouteID == material.Route.ID {
			return m, snapshot, nil
		}
	}
	return m, nil, ErrConflict
}

func freezeInferenceBudget(tx *sql.Tx, material *domain.ModelAcceptanceMaterial, q domain.ModelAcceptanceProposal) error {
	if q.BudgetMissionID == "" || q.ExpectedMissionRevision < 1 {
		return ErrConflict
	}
	m, snapshot, err := inferenceBudgetSnapshot(tx, q.BudgetMissionID, *material)
	if err != nil {
		return err
	}
	b := &domain.InferenceBudgetBinding{SchemaVersion: 1, MissionID: m.ID, Revision: m.Revision, Epoch: m.Epoch, SnapshotDigest: snapshot.Digest, UsedCalls: m.Budget.UsedCalls, MaxCalls: m.Budget.MaxCalls}
	if m.Revision != q.ExpectedMissionRevision || !b.Valid() {
		return ErrConflict
	}
	material.InferenceBudget = b
	return nil
}

func validateInferenceBudget(tx *sql.Tx, o domain.ModelAcceptanceOperation, claimed bool) error {
	b := o.Material.InferenceBudget
	if b == nil || !b.Valid() {
		return ErrConflict
	}
	m, snapshot, err := inferenceBudgetSnapshot(tx, b.MissionID, o.Material)
	if err != nil {
		return err
	}
	revision, used := b.Revision, b.UsedCalls
	if claimed {
		var owner, digest string
		var debitRevision int64
		if err = tx.QueryRow(`SELECT mission_id,revision,binding_digest FROM model_acceptance_inference_budgets WHERE operation_id=?`, o.ID).Scan(&owner, &debitRevision, &digest); err != nil {
			return ErrConflict
		}
		revision++
		used++
		if owner != b.MissionID || debitRevision != revision || digest != domain.AcceptanceDigest(b) || !m.Budget.UsageUnknown {
			return ErrConflict
		}
	}
	if m.Revision != revision || m.Epoch != b.Epoch || snapshot.Digest != b.SnapshotDigest || m.Budget.UsedCalls != used || m.Budget.MaxCalls != b.MaxCalls {
		return ErrConflict
	}
	return nil
}

// Caller holds the SQLite writer and inserts the unique acceptance claim first.
// A failure anywhere rolls back claim, mission CAS, audit event and reservation.
func debitInferenceBudget(tx *sql.Tx, o domain.ModelAcceptanceOperation) error {
	if err := validateInferenceBudget(tx, o, false); err != nil {
		return err
	}
	b := o.Material.InferenceBudget
	m, err := updateMissionTx(tx, b.MissionID, b.Revision, "inference.diagnostic_reserved", func(m *domain.Mission) error {
		if m.Budget.UsedCalls >= m.Budget.MaxCalls {
			return ErrConflict
		}
		m.Budget.UsedCalls++
		m.Budget.UsageUnknown = true
		return nil
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO model_acceptance_inference_budgets(operation_id,mission_id,revision,binding_digest) VALUES(?,?,?,?)`, o.ID, m.ID, m.Revision, domain.AcceptanceDigest(b))
	return err
}
