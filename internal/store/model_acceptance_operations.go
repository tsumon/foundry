package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"reflect"
	"time"
)

func (s *Store) initializeModelAcceptanceOperations() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS model_acceptance_operations(id TEXT PRIMARY KEY,account_id TEXT NOT NULL REFERENCES model_accounts(id),revision INTEGER NOT NULL,state TEXT NOT NULL, data BLOB NOT NULL);
 CREATE INDEX IF NOT EXISTS acceptance_account_history ON model_acceptance_operations(account_id);
 CREATE TRIGGER IF NOT EXISTS acceptance_no_delete BEFORE DELETE ON model_acceptance_operations BEGIN SELECT RAISE(ABORT,'immutable acceptance history'); END;`)
	return err
}
func acceptanceRead(row modelRowScanner) (domain.ModelAcceptanceOperation, error) {
	var o domain.ModelAcceptanceOperation
	var raw []byte
	if e := row.Scan(&raw); e != nil {
		return o, modelStoreError(e)
	}
	e := json.Unmarshal(raw, &o)
	return o, e
}
func acceptanceTx(tx *sql.Tx, account, id string) (domain.ModelAcceptanceOperation, error) {
	return acceptanceRead(tx.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=? AND account_id=?`, id, account))
}
func (s *Store) GetModelAcceptance(account, id string) (domain.ModelAcceptanceOperation, error) {
	return acceptanceRead(s.db.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=? AND account_id=?`, id, account))
}
func (s *Store) ListModelAcceptance(account string) ([]domain.ModelAcceptanceOperation, error) {
	if _, e := s.GetModelAccount(account); e != nil {
		return nil, e
	}
	rows, e := s.db.Query(`SELECT data FROM model_acceptance_operations WHERE account_id=? ORDER BY rowid DESC`, account)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.ModelAcceptanceOperation{}
	for rows.Next() {
		o, e := acceptanceRead(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func acceptanceWrite(tx *sql.Tx, o *domain.ModelAcceptanceOperation) error {
	old := o.Revision
	o.Revision++
	raw, e := json.Marshal(o)
	if e != nil {
		return e
	}
	res, e := tx.Exec(`UPDATE model_acceptance_operations SET revision=?,state=?,data=? WHERE id=? AND account_id=? AND revision=?`, o.Revision, o.State, raw, o.ID, o.AccountID, old)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// Capture and revalidation use the same SQLite transaction as proposal/claim.
// This projection never reads a secret reference or credential value.
func acceptanceGateSnapshot(tx *sql.Tx, account, route string, connected bool, now time.Time) (domain.ModelAcceptanceMaterial, error) {
	var m domain.ModelAcceptanceMaterial
	a, e := modelAccountTx(tx, account)
	if e != nil {
		return m, e
	}
	r, e := modelRouteTx(tx, route)
	if e != nil {
		return m, e
	}
	if !a.Enabled || r.Environment != "simulation" || r.ExtraUsage != "off" || (r.Adapter != "openai_siwc_responses" && r.Adapter != "kimi_code_chat") || domain.ValidateModelRoute(r, a) != nil {
		return m, ErrConflict
	}
	var unresolved bool
	e = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM model_credential_versions WHERE account_id=? AND (state='pending' OR cleanup_pending=1)) OR EXISTS(SELECT 1 FROM model_authorization_operations WHERE account_id=? AND (state='pending' OR cleanup_pending=1))`, account, account).Scan(&unresolved)
	if e != nil {
		return m, e
	}
	if unresolved {
		return m, ErrConflict
	}
	m.Account, m.Route, m.CredentialVersion = a, r, a.CurrentCredentialVersion
	if a.AuthMethod == "api_key" {
		if a.CurrentCredentialVersion > 0 {
			var state string
			var cleanup bool
			e = tx.QueryRow(`SELECT state,cleanup_pending FROM model_credential_versions WHERE account_id=? AND version=?`, account, a.CurrentCredentialVersion).Scan(&state, &cleanup)
			if e != nil {
				return m, e
			}
			if state != "available" || cleanup {
				return m, ErrConflict
			}
		} else if connected {
			return m, ErrConflict
		}
	} else {
		var auth domain.ModelAuthorization
		var raw []byte
		e = tx.QueryRow(`SELECT binding,state,generation,expires_at FROM model_authorizations WHERE account_id=? ORDER BY rowid DESC LIMIT 1`, account).Scan(&raw, &auth.State, &auth.Generation, &auth.ExpiresAt)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return m, e
		}
		if e == nil {
			if json.Unmarshal(raw, &auth.Binding) != nil {
				return m, ErrConflict
			}
			expiry, x := time.Parse(time.RFC3339Nano, auth.ExpiresAt)
			if auth.State != "available" || x != nil || !expiry.After(now) || !domain.ModelAuthorizationAllowsInference(auth.Binding) || domain.ValidateModelAuthorizationBinding(auth.Binding) != nil {
				return m, ErrConflict
			}
			m.Authorization = &auth
		} else if connected {
			return m, ErrConflict
		}
	}
	return m, nil
}
func acceptanceSnapshotDigest(m domain.ModelAcceptanceMaterial) string {
	return domain.AcceptanceDigest(struct {
		Account       domain.ModelAccount
		Route         domain.ModelRoute
		Credential    int64
		Authorization *domain.ModelAuthorization
	}{m.Account, m.Route, m.CredentialVersion, m.Authorization})
}
func acceptancePrerequisite(tx *sql.Tx, m domain.ModelAcceptanceMaterial) error {
	if m.Phase == "connection" {
		if m.PrerequisiteID != "" || m.Model != "" {
			return ErrConflict
		}
		return nil
	}
	p, e := acceptanceTx(tx, m.Account.ID, m.PrerequisiteID)
	if e != nil {
		return ErrConflict
	}
	want := "connection"
	if m.Phase == "inference" {
		want = "catalog"
	}
	if p.State != "succeeded" || p.Phase != want || p.Material.Route.ID != m.Route.ID || p.ResultAccountRevision != m.Account.Revision || p.ResultSnapshotDigest != acceptanceSnapshotDigest(m) {
		return ErrConflict
	}
	connection := p
	if m.Phase == "catalog" {
		if m.Model != "" || m.ConnectionID != p.ID {
			return ErrConflict
		}
	} else {
		found := false
		for _, v := range p.Catalog {
			if v.ID == m.Model {
				found = true
			}
		}
		if !found || m.ConnectionID != p.Material.ConnectionID {
			return ErrConflict
		}
		connection, e = acceptanceTx(tx, m.Account.ID, m.ConnectionID)
		if e != nil {
			return ErrConflict
		}
	}
	if connection.State != "succeeded" || connection.ResultSnapshotDigest != acceptanceSnapshotDigest(m) {
		return ErrConflict
	}
	// A newer connection (including consumed unknown) revokes old lineage.
	var latest string
	e = tx.QueryRow(`SELECT id FROM model_acceptance_operations WHERE account_id=? AND json_extract(data,'$.phase')='connection' AND json_extract(data,'$.attempts')=1 ORDER BY rowid DESC LIMIT 1`, m.Account.ID).Scan(&latest)
	if e != nil || latest != connection.ID {
		return ErrConflict
	}
	return nil
}
func (s *Store) ProposeModelAcceptance(account string, q domain.ModelAcceptanceProposal, now time.Time) (domain.ModelAcceptanceOperation, error) {
	var o domain.ModelAcceptanceOperation
	if !acceptanceIdentifierPattern.MatchString(account) || !acceptanceIdentifierPattern.MatchString(q.RouteID) || q.ExpectedAccountRevision < 1 || (q.Phase != "connection" && q.Phase != "catalog" && q.Phase != "inference") {
		return o, ErrConflict
	}
	tx, e := s.db.Begin()
	if e != nil {
		return o, e
	}
	defer tx.Rollback()
	// Take the writer lock before deriving any approval material.
	if _, e = tx.Exec(`UPDATE model_accounts SET revision=revision WHERE id=?`, account); e != nil {
		return o, e
	}
	m, e := acceptanceGateSnapshot(tx, account, q.RouteID, q.Phase != "connection", now)
	if e != nil {
		return o, e
	}
	if m.Account.Revision != q.ExpectedAccountRevision {
		return o, ErrConflict
	}
	m.Phase, m.PrerequisiteID, m.Model = q.Phase, q.PrerequisiteID, q.Model
	if m.Phase == "catalog" {
		m.ConnectionID = q.PrerequisiteID
	}
	if m.Phase == "inference" {
		p, e := acceptanceTx(tx, account, q.PrerequisiteID)
		if e != nil {
			return o, ErrConflict
		}
		m.ConnectionID = p.Material.ConnectionID
		m.PromptDigest = domain.AcceptanceDigest([]string{domain.AcceptanceSystem, domain.AcceptancePrompt})
	}
	if e = acceptancePrerequisite(tx, m); e != nil {
		return o, e
	}
	m.CreatedAt = now.UTC().Format(time.RFC3339Nano)
	m.ExpiresAt = now.Add(10 * time.Minute).UTC().Format(time.RFC3339Nano)
	m.DeadlineSeconds = 30
	m.MaxAttempts = 1
	m.DataSummary = "Fixed synthetic local fixture data only; no tools or repository context."
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return o, e
	}
	o = domain.ModelAcceptanceOperation{ID: hex.EncodeToString(nonce), AccountID: account, Revision: 1, Phase: q.Phase, State: "pending", Material: m, MaterialDigest: domain.AcceptanceDigest(m), Source: "local_simulation", Certainty: "not_sent", Catalog: []domain.ModelAcceptanceCatalogEntry{}, Quota: "unknown", RemoteBilling: "unverified", LiveAccess: "blocked"}
	raw, e := json.Marshal(o)
	if e != nil {
		return o, e
	}
	_, e = tx.Exec(`INSERT INTO model_acceptance_operations(id,account_id,revision,state,data) VALUES(?,?,?,?,?)`, o.ID, o.AccountID, o.Revision, o.State, raw)
	if e != nil {
		return o, e
	}
	return o, tx.Commit()
}
func (s *Store) transitionModelAcceptance(account, id string, revision int64, digest, action string, now time.Time) (domain.ModelAcceptanceOperation, error) {
	var o domain.ModelAcceptanceOperation
	tx, e := s.db.Begin()
	if e != nil {
		return o, e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`UPDATE model_acceptance_operations SET revision=revision WHERE id=? AND account_id=?`, id, account); e != nil {
		return o, e
	}
	o, e = acceptanceTx(tx, account, id)
	if e != nil {
		return o, e
	}
	if o.Revision != revision || o.MaterialDigest != digest || domain.AcceptanceDigest(o.Material) != digest {
		return o, ErrConflict
	}
	if action == "execute" {
		if o.State != "approved" || o.Attempts != 0 {
			return o, ErrConflict
		}
		// Keep connection output lineage unambiguous: no second approved
		// snapshot may dispatch while this account has an unfinished effect.
		var running bool
		if e = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM model_acceptance_operations WHERE account_id=? AND state='running')`, account).Scan(&running); e != nil {
			return o, e
		}
		if running {
			return o, ErrConflict
		}
	} else if (action != "approve" && action != "reject") || o.State != "pending" {
		return o, ErrConflict
	}
	expiry, e := time.Parse(time.RFC3339Nano, o.Material.ExpiresAt)
	if e != nil {
		return o, e
	}
	invalid := ""
	if !expiry.After(now) {
		invalid = "expired"
	} else {
		current, e := acceptanceGateSnapshot(tx, account, o.Material.Route.ID, o.Phase != "connection", now)
		if e != nil {
			if !errors.Is(e, ErrConflict) && !errors.Is(e, ErrNotFound) {
				return o, e
			}
			invalid = "invalidated"
		} else if acceptanceSnapshotDigest(current) != acceptanceSnapshotDigest(o.Material) {
			invalid = "invalidated"
		} else if e = acceptancePrerequisite(tx, o.Material); e != nil {
			invalid = "invalidated"
		}
	}
	if invalid != "" {
		o.State = invalid
		if e = acceptanceWrite(tx, &o); e != nil {
			return o, e
		}
		if e = tx.Commit(); e != nil {
			return o, e
		}
		return o, ErrConflict
	}
	switch action {
	case "approve":
		o.State = "approved"
	case "reject":
		o.State = "rejected"
	case "execute":
		o.State = "running"
		o.Attempts = 1
		o.Certainty = "unknown"
		o.StartedAt = now.UTC().Format(time.RFC3339Nano)
	}
	if e = acceptanceWrite(tx, &o); e != nil {
		return o, e
	}
	return o, tx.Commit()
}
func (s *Store) DecideModelAcceptance(account, id string, revision int64, digest, decision string, now time.Time) (domain.ModelAcceptanceOperation, error) {
	return s.transitionModelAcceptance(account, id, revision, digest, decision, now)
}
func (s *Store) ClaimModelAcceptance(account, id string, revision int64, digest string, now time.Time) (domain.ModelAcceptanceOperation, error) {
	return s.transitionModelAcceptance(account, id, revision, digest, "execute", now)
}

// Finish only accepts bounded evidence; it never accepts provider text or output.
func (s *Store) FinishModelAcceptance(account, id string, revision int64, state, certainty, code string, catalog []domain.ModelAcceptanceCatalogEntry, input, output int64, usage bool, now time.Time) (domain.ModelAcceptanceOperation, error) {
	var o domain.ModelAcceptanceOperation
	if state != "succeeded" && state != "failed" && state != "unknown" {
		return o, ErrConflict
	}
	if certainty != "completed" && certainty != "rejected" && certainty != "not_sent" && certainty != "unknown" {
		return o, ErrConflict
	}
	switch code {
	case "", "auth_invalid", "entitlement_denied", "quota_exhausted", "rate_limited", "request_rejected", "server_error", "transport_unknown", "response_incomplete":
	default:
		return o, ErrConflict
	}
	if len(catalog) > 128 || input < 0 || output < 0 {
		return o, ErrConflict
	}
	seen := map[string]bool{}
	for _, m := range catalog {
		if !acceptanceIdentifierPattern.MatchString(m.ID) || seen[m.ID] || m.DisplayName != m.ID {
			return o, ErrConflict
		}
		seen[m.ID] = true
	}
	tx, e := s.db.Begin()
	if e != nil {
		return o, e
	}
	defer tx.Rollback()
	o, e = acceptanceTx(tx, account, id)
	if e != nil {
		return o, e
	}
	if o.State != "running" || o.Revision != revision || o.Attempts != 1 {
		return o, ErrConflict
	}
	if state == "succeeded" {
		m, e := acceptanceGateSnapshot(tx, account, o.Material.Route.ID, true, now)
		if e != nil {
			return o, e
		}
		if o.Phase != "connection" && (!reflect.DeepEqual(m.Account, o.Material.Account) || acceptanceSnapshotDigest(m) != acceptanceSnapshotDigest(o.Material)) {
			return o, ErrConflict
		}
		o.ResultAccountRevision = m.Account.Revision
		o.ResultSnapshotDigest = acceptanceSnapshotDigest(m)
	}
	o.State, o.Certainty, o.Code = state, certainty, code
	o.Catalog = append([]domain.ModelAcceptanceCatalogEntry{}, catalog...)
	o.InputTokens, o.OutputTokens, o.UsageKnown = input, output, usage
	o.FinishedAt = now.UTC().Format(time.RFC3339Nano)
	if e = acceptanceWrite(tx, &o); e != nil {
		return o, e
	}
	return o, tx.Commit()
}
func (s *Store) RecoverModelAcceptance(now time.Time) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	rows, e := tx.Query(`SELECT data FROM model_acceptance_operations WHERE state='running'`)
	if e != nil {
		return e
	}
	items := []domain.ModelAcceptanceOperation{}
	for rows.Next() {
		o, e := acceptanceRead(rows)
		if e != nil {
			rows.Close()
			return e
		}
		items = append(items, o)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, o := range items {
		o.State = "unknown"
		o.Certainty = "unknown"
		o.Code = "transport_unknown"
		o.FinishedAt = now.UTC().Format(time.RFC3339Nano)
		if e = acceptanceWrite(tx, &o); e != nil {
			return e
		}
	}
	return tx.Commit()
}
