package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"reflect"
	"time"
)

// ResolveMissionModels reads all selections from one SQLite snapshot. The
// returned concrete profiles, not global settings, are execution inputs.
func (s *Store) ResolveMissionModels(requestedDefault string) (string, map[domain.Role]domain.Profile, *domain.MissionModelSnapshot, error) {
	id, profiles, snapshot, _, err := s.ResolveMissionModelSelection(requestedDefault)
	return id, profiles, snapshot, err
}

// resolveExplicitRoleModelsTx validates a complete role/profile combination
// without consulting or temporarily changing global settings. Policy preview,
// confirmation, and mission resolution share this exact compatibility fence.
func resolveExplicitRoleModelsTx(tx *sql.Tx, roleProfileIDs map[domain.Role]string) (map[domain.Role]domain.Profile, *domain.MissionModelSnapshot, error) {
	if len(roleProfileIDs) != len(domain.RolePolicyRoles()) {
		return nil, nil, domain.ErrInvalidModelSnapshot
	}
	profiles := map[domain.Role]domain.Profile{}
	snapshot := &domain.MissionModelSnapshot{SchemaVersion: 1, Roles: map[domain.Role]domain.RoleModelSelection{}}
	bound := false
	for _, role := range domain.RolePolicyRoles() {
		id, ok := roleProfileIDs[role]
		if !ok || id == "" {
			return nil, nil, domain.ErrInvalidModelSnapshot
		}
		selection, resolveErr := resolveModelProfileTx(tx, id)
		if resolveErr != nil {
			return nil, nil, resolveErr
		}
		if selection.Account != nil {
			bound = true
		}
		if selection.Route != nil {
			snapshot.SchemaVersion = 2
		}
		profiles[role] = selection.Profile
		snapshot.Roles[role] = selection
	}
	if !bound {
		return profiles, nil, nil
	}
	for _, selection := range snapshot.Roles {
		if selection.Account == nil {
			continue
		}
		if snapshot.SchemaVersion == 2 && selection.Route == nil {
			return nil, nil, domain.ErrInvalidModelSnapshot
		}
		if selection.Account.AuthMethod == "openai_siwc" && (snapshot.SchemaVersion != 2 || selection.Profile.CredentialVersion != 0) {
			return nil, nil, domain.ErrInvalidModelSnapshot
		}
	}
	var err error
	snapshot.Digest, err = domain.ModelSnapshotDigest(*snapshot)
	return profiles, snapshot, err
}

// ResolveModelProfile resolves precisely the requested saved profile in one
// transaction; global default/role settings cannot redirect an explicit probe.
func (s *Store) ResolveModelProfile(id string) (domain.RoleModelSelection, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.RoleModelSelection{}, err
	}
	defer tx.Rollback()
	return resolveModelProfileTx(tx, id)
}

func resolveModelProfileTx(tx *sql.Tx, id string) (domain.RoleModelSelection, error) {
	var selection domain.RoleModelSelection
	var raw []byte
	if err := tx.QueryRow(`SELECT data FROM profiles WHERE id=?`, id).Scan(&raw); err != nil {
		return selection, modelStoreError(err)
	}
	var p domain.Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		return selection, err
	}
	if p.Model == "" || (p.Protocol != "chat_completions" && p.Protocol != "responses" && !(p.RouteID != "" && p.Protocol == "messages")) || p.AccountRevision != 0 || p.CredentialVersion != 0 || (p.RouteID != "" && p.AccountID == "") {
		return selection, domain.ErrInvalidModelSnapshot
	}
	if _, _, err := domain.ParseProviderBaseURL(p.BaseURL); err != nil {
		return selection, err
	}
	if p.AccountID != "" {
		a, err := modelAccountTx(tx, p.AccountID)
		if err != nil {
			return selection, err
		}
		if a.BaseURL != p.BaseURL || p.HasKey {
			return selection, domain.ErrInvalidModelSnapshot
		}
		p.AccountRevision = a.Revision
		p.CredentialVersion = a.CurrentCredentialVersion
		selection.Account = &a
		if p.RouteID != "" {
			r, err := modelRouteTx(tx, p.RouteID)
			if err != nil {
				return selection, err
			}
			if domain.ValidateModelRoute(r, a) != nil || r.Protocol != p.Protocol {
				return selection, domain.ErrInvalidModelSnapshot
			}
			selection.Route = &r
			if a.AuthMethod == "openai_siwc" {
				authorization, err := currentModelAuthorizationTx(tx, a.ID)
				if err == nil {
					selection.Authorization = &authorization.Binding
				} else if !errors.Is(err, ErrNotFound) {
					return selection, err
				}
			}
		}
		if a.AuthMethod == "openai_siwc" && (selection.Route == nil || p.CredentialVersion != 0) {
			return selection, domain.ErrInvalidModelSnapshot
		}
		if p.CredentialVersion > 0 {
			v, err := modelCredentialTx(tx, a.ID, p.CredentialVersion)
			if err != nil {
				return selection, err
			}
			if v.State != "available" {
				return selection, ErrModelAccountUnavailable
			}
		}
	}
	selection.Profile = p
	if p.CatalogOperationID != "" {
		proof, err := catalogSelectionTx(tx, p, time.Now())
		if err != nil {
			return selection, err
		}
		selection.Catalog = &proof
	}
	return selection, nil
}

func modelCredentialTx(tx *sql.Tx, id string, version int64) (domain.ModelCredentialVersion, error) {
	return scanModelCredential(tx.QueryRow(`SELECT `+modelCredentialColumns+` FROM model_credential_versions WHERE account_id=? AND version=?`, id, version))
}

func validateNewModelSnapshotTx(tx *sql.Tx, m domain.Mission) error {
	if m.ModelSnapshot == nil && m.ModelPolicySnapshot == nil {
		settings, err := rolePolicySettingsTx(tx)
		if err != nil {
			return err
		}
		if settings.ModelPolicyDecisionID == "" {
			return nil
		}
	}
	_, profiles, snapshot, policySnapshot, err := resolveMissionModelSelectionTx(tx, m.Spec.ProfileID)
	if err != nil {
		return ErrConflict
	}
	if policySnapshot != nil || m.ModelPolicySnapshot != nil {
		if !reflect.DeepEqual(profiles, m.Profiles) || !reflect.DeepEqual(snapshot, m.ModelSnapshot) || !reflect.DeepEqual(policySnapshot, m.ModelPolicySnapshot) {
			return ErrConflict
		}
		return nil
	}
	if m.ModelSnapshot != nil && (!reflect.DeepEqual(profiles, m.Profiles) || !reflect.DeepEqual(snapshot, m.ModelSnapshot)) {
		return ErrConflict
	}
	return nil
}

var ErrModelAccountUnavailable = errors.New("model_account_unavailable")

// CheckModelCall is an early UX/action check only. Reservation and claim repeat
// it inside their own write transactions to close the concurrent revocation gap.
func (s *Store) CheckModelCall(id string, role domain.Role) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	m, err := readMissionRow(tx, id)
	if err != nil {
		return err
	}
	return guardModelCallTx(tx, m, role)
}

// guardModelCallTx checks current availability of the exact frozen version.
// Account revisions and current-version pointers may legitimately advance.
func guardModelCallTx(tx *sql.Tx, m domain.Mission, role domain.Role) error {
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return err
	}
	if m.ModelSnapshot == nil {
		return nil
	}
	p, ok := m.Profiles[role]
	if !ok {
		return errors.New("model role is not configured")
	}
	if p.AccountID == "" {
		return nil
	}
	if m.ModelSnapshot.SchemaVersion == 2 {
		_, _, err := resolveModelCredentialMetadataTx(tx, m.ModelSnapshot.Roles[role])
		return err
	}
	a, err := modelAccountTx(tx, p.AccountID)
	if err != nil {
		return ErrModelAccountUnavailable
	}
	if !a.Enabled || a.BaseURL != p.BaseURL || p.CredentialVersion < 1 {
		return ErrModelAccountUnavailable
	}
	v, err := modelCredentialTx(tx, p.AccountID, p.CredentialVersion)
	if err != nil || v.State != "available" {
		return ErrModelAccountUnavailable
	}
	return nil
}

// ReserveModelCall atomically acquires ordinary-call dispatch authority and
// consumes its budget. A generic mission event cannot substitute for this.
func (s *Store) ReserveModelCall(id string, expected int64, role domain.Role) (domain.Mission, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Mission{}, err
	}
	defer tx.Rollback()
	m, err := updateMissionTx(tx, id, expected, "call.reserved", func(m *domain.Mission) error {
		if domain.IsInferenceSnapshot(*m) {
			return errors.New("schema2 model calls require durable invocation")
		}
		if err := guardModelCallTx(tx, *m, role); err != nil {
			return err
		}
		if m.Budget.UsedCalls >= m.Budget.MaxCalls {
			return errors.New("model call budget exhausted")
		}
		m.Budget.UsedCalls++
		m.Budget.UsageUnknown = true
		return nil
	})
	if err != nil {
		return domain.Mission{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Mission{}, err
	}
	return m, nil
}
