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
 CREATE TRIGGER IF NOT EXISTS acceptance_no_delete BEFORE DELETE ON model_acceptance_operations BEGIN SELECT RAISE(ABORT,'immutable acceptance history'); END;
 CREATE TABLE IF NOT EXISTS model_acceptance_claims(sequence INTEGER PRIMARY KEY AUTOINCREMENT,operation_id TEXT NOT NULL UNIQUE REFERENCES model_acceptance_operations(id));
 CREATE TRIGGER IF NOT EXISTS acceptance_claim_no_update BEFORE UPDATE ON model_acceptance_claims BEGIN SELECT RAISE(ABORT,'immutable acceptance claim'); END;
 CREATE TRIGGER IF NOT EXISTS acceptance_claim_no_delete BEFORE DELETE ON model_acceptance_claims BEGIN SELECT RAISE(ABORT,'immutable acceptance claim'); END;`)
	if err != nil {
		return err
	}
	if err = s.initializeProductionConnectionDispatch(); err != nil {
		return err
	}
	if err = s.initializeInferenceBudgets(); err != nil {
		return err
	}
	return s.initializeConnectionOrigins()
}
func acceptanceRead(row modelRowScanner) (domain.ModelAcceptanceOperation, error) {
	var o domain.ModelAcceptanceOperation
	var raw []byte
	if e := row.Scan(&raw); e != nil {
		return o, modelStoreError(e)
	}
	// Optional diagnostic evidence must not make an otherwise valid historical
	// receipt unreadable. Decode it separately and never rewrite the stored row.
	projection := struct {
		*domain.ModelAcceptanceOperation
		ConnectionDiagnostic json.RawMessage `json:"connection_diagnostic"`
		InferenceDiagnostic  json.RawMessage `json:"inference_diagnostic"`
	}{ModelAcceptanceOperation: &o}
	if e := json.Unmarshal(raw, &projection); e != nil {
		return o, e
	}
	var diagnostic domain.ConnectionDiagnostic
	if json.Unmarshal(projection.ConnectionDiagnostic, &diagnostic) == nil && validStoredConnectionDiagnostic(o, &diagnostic) {
		o.ConnectionDiagnostic = &diagnostic
	}
	var inference domain.InferenceDiagnostic
	if json.Unmarshal(projection.InferenceDiagnostic, &inference) == nil && inference.Valid() && o.Source == "production_inference" && o.Attempts == 1 && (o.State == "succeeded" || o.State == "failed" || o.State == "unknown") {
		o.InferenceDiagnostic = &inference
	}
	return o, nil
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
func acceptanceGateSnapshot(tx *sql.Tx, account, route string, connected bool, purpose string, admission *domain.ProductionAdmission, now time.Time) (domain.ModelAcceptanceMaterial, error) {
	var m domain.ModelAcceptanceMaterial
	a, e := modelAccountTx(tx, account)
	if e != nil {
		return m, e
	}
	r, e := modelRouteTx(tx, route)
	if e != nil {
		return m, e
	}
	if !a.Enabled || (admission == nil && r.Environment != "simulation") || r.ExtraUsage != "off" || (r.Adapter != "openai_siwc_responses" && r.Adapter != "kimi_code_chat") || domain.ValidateModelRoute(r, a) != nil {
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
	if admission != nil {
		copy := *admission
		m.ProductionAdmission = &copy
		if admission.Profile == "openai_siwc_catalog_v1" || admission.Profile == "openai_siwc_inference_v1" {
			m.Phase, m.RequestMethod, m.RequestURL = "catalog", "GET", domain.OpenAICatalogURL
			if admission.Profile == "openai_siwc_inference_v1" {
				freezeInferenceMaterial(&m)
			}
			m.DeadlineSeconds, m.MaxAttempts = 30, 1
			if purpose != "" || !connected {
				return m, ErrConflict
			}
		} else {
			m.ConnectionMode, m.LoginTimeoutSeconds, m.DeadlineSeconds, m.MaxAttempts = "browser_handoff", 600, 30, 1
			if purpose != "subscription_only" {
				return m, ErrConflict
			}
		}
	}
	if purpose == "identity_only" {
		m.Phase, m.ConnectionPurpose, m.RequestedScopes = "connection", purpose, []string{"openid", "profile", "email"}
		if !m.ValidConnectionPurpose() {
			return m, ErrConflict
		}
	} else if purpose == "subscription_only" {
		m.Phase, m.ConnectionPurpose = "connection", purpose
		m.RequestedScopes = []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}
		m.RequestedResource = domain.OpenAISubscriptionRequestedResource
		if !m.ValidConnectionPurpose() {
			return m, ErrConflict
		}
	} else if purpose != "" {
		return m, ErrConflict
	}
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
			allowed := domain.ModelAuthorizationAllowsInference(auth.Binding)
			if purpose == "identity_only" {
				allowed = auth.Binding.SpendClass == "identity_only"
			} else if purpose == "subscription_only" {
				allowed = auth.Binding.SpendClass == "subscription_only" && reflect.DeepEqual(auth.Binding.Scopes, []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"})
			}
			proofCurrent := auth.State == "available" && expiry.After(now)
			// Explicit reconnection approval may snapshot prior metadata, never
			// permission. Finish and every connected gate still require freshness.
			if purpose != "" && !connected && (auth.State == "available" || auth.State == "revoked") {
				proofCurrent = true
			}
			if !proofCurrent || auth.Binding.AccountID != account || x != nil || !allowed || domain.ValidateModelAuthorizationBinding(auth.Binding) != nil {
				return m, ErrConflict
			}
			if admission != nil && auth.Binding.Issuer != "https://auth.openai.com" {
				return m, ErrConflict
			}
			m.Authorization = &auth
		} else if connected {
			return m, ErrConflict
		}
	}
	if admission != nil && admission.Profile == "openai_siwc_catalog_v1" && !m.ValidProductionCatalog() {
		return m, ErrConflict
	}
	if admission != nil && admission.Profile == "openai_siwc_inference_v1" && !m.ValidProductionInference() {
		return m, ErrConflict
	}
	return m, nil
}
func acceptanceSnapshotDigest(m domain.ModelAcceptanceMaterial) string {
	return domain.AcceptanceDigest(struct {
		Account             domain.ModelAccount
		Route               domain.ModelRoute
		Credential          int64
		Authorization       *domain.ModelAuthorization
		ConnectionPurpose   string                      `json:"connection_purpose,omitempty"`
		RequestedScopes     []string                    `json:"requested_scopes,omitempty"`
		RequestedResource   string                      `json:"requested_resource,omitempty"`
		ProductionAdmission *domain.ProductionAdmission `json:"production_admission,omitempty"`
	}{m.Account, m.Route, m.CredentialVersion, m.Authorization, m.ConnectionPurpose, m.RequestedScopes, m.RequestedResource, m.ProductionAdmission})
}
func acceptancePrerequisite(tx *sql.Tx, m domain.ModelAcceptanceMaterial) error {
	if m.ProductionAdmission != nil && m.ProductionAdmission.Profile == "openai_siwc_inference_v1" {
		return inferencePrerequisite(tx, m)
	}
	if m.ProductionAdmission != nil && m.ProductionAdmission.Profile == "openai_siwc_catalog_v1" {
		return catalogPrerequisite(tx, m)
	}
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
	if p.Material.ConnectionPurpose != "" || p.State != "succeeded" || p.Phase != want || p.Material.Route.ID != m.Route.ID || p.ResultAccountRevision != m.Account.Revision || p.ResultSnapshotDigest != acceptanceSnapshotDigest(m) {
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
	if connection.Material.ConnectionPurpose != "" || connection.State != "succeeded" || connection.ClaimSequence < 1 || connection.ResultSnapshotDigest != acceptanceSnapshotDigest(m) {
		return ErrConflict
	}
	// Dispatch order, not proposal order or wall-clock time, defines lineage.
	// A newer claimed connection (including consumed unknown) revokes it.
	var latest string
	e = tx.QueryRow(`SELECT o.id FROM model_acceptance_claims c JOIN model_acceptance_operations o ON o.id=c.operation_id WHERE o.account_id=? AND json_extract(o.data,'$.phase')='connection' ORDER BY c.sequence DESC LIMIT 1`, m.Account.ID).Scan(&latest)
	if e != nil || latest != connection.ID {
		return ErrConflict
	}
	return nil
}
func (s *Store) ProposeModelAcceptance(account string, q domain.ModelAcceptanceProposal, now time.Time) (domain.ModelAcceptanceOperation, error) {
	return s.ProposeModelAcceptanceWithConnectionMode(account, q, "", now)
}

// ProposeModelAcceptanceWithConnectionMode accepts trusted constructor policy,
// never a field decoded from the public proposal. Historical callers omit it.
func (s *Store) ProposeModelAcceptanceWithConnectionMode(account string, q domain.ModelAcceptanceProposal, mode string, now time.Time) (domain.ModelAcceptanceOperation, error) {
	return s.proposeModelAcceptance(account, q, mode, nil, now)
}

func (s *Store) proposeModelAcceptance(account string, q domain.ModelAcceptanceProposal, mode string, admission *domain.ProductionAdmission, now time.Time) (domain.ModelAcceptanceOperation, error) {
	var o domain.ModelAcceptanceOperation
	productionInference := admission != nil && admission.Profile == "openai_siwc_inference_v1"
	if !productionInference && (q.BudgetMissionID != "" || q.ExpectedMissionRevision != 0) {
		return o, ErrConflict
	}
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
	m, e := acceptanceGateSnapshot(tx, account, q.RouteID, q.Phase != "connection", q.ConnectionPurpose, admission, now)
	if e != nil {
		return o, e
	}
	if m.Account.Revision != q.ExpectedAccountRevision {
		return o, ErrConflict
	}
	m.Phase, m.PrerequisiteID, m.Model = q.Phase, q.PrerequisiteID, q.Model
	if !m.ValidAcceptancePurpose() {
		return o, ErrConflict
	}
	if m.Phase == "catalog" {
		m.ConnectionID = q.PrerequisiteID
	}
	if m.Phase == "inference" {
		p, e := acceptanceTx(tx, account, q.PrerequisiteID)
		if e != nil {
			return o, ErrConflict
		}
		m.ConnectionID = p.Material.ConnectionID
		if admission != nil && admission.Profile == "openai_siwc_inference_v1" {
			m.CatalogDigest = domain.AcceptanceDigest(p)
		} else {
			m.PromptDigest = domain.AcceptanceDigest([]string{domain.AcceptanceSystem, domain.AcceptancePrompt})
		}
	}
	if e = acceptancePrerequisite(tx, m); e != nil {
		return o, e
	}
	if productionInference {
		if e = freezeInferenceBudget(tx, &m, q); e != nil {
			return o, e
		}
	}
	m.CreatedAt = now.UTC().Format(time.RFC3339Nano)
	m.ExpiresAt = now.Add(10 * time.Minute).UTC().Format(time.RFC3339Nano)
	if admission != nil && (admission.Profile == "openai_siwc_catalog_v1" || admission.Profile == "openai_siwc_inference_v1") {
		expiry, err := time.Parse(time.RFC3339Nano, m.Authorization.ExpiresAt)
		if err != nil || !expiry.After(now) {
			return o, ErrConflict
		}
		if expiry.Before(now.Add(10 * time.Minute)) {
			m.ExpiresAt = expiry.UTC().Format(time.RFC3339Nano)
		}
	}
	m.DeadlineSeconds = 30
	m.MaxAttempts = 1
	m.ConnectionMode = mode
	if mode != "" {
		m.LoginTimeoutSeconds = 600
	}
	if !m.ValidAcceptancePurpose() {
		return o, ErrConflict
	}
	m.DataSummary = "Fixed synthetic local fixture data only; no tools or repository context."
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return o, e
	}
	o = domain.ModelAcceptanceOperation{ID: hex.EncodeToString(nonce), AccountID: account, Revision: 1, Phase: q.Phase, State: "pending", Material: m, MaterialDigest: domain.AcceptanceDigest(m), Source: "local_simulation", Certainty: "not_sent", Catalog: []domain.ModelAcceptanceCatalogEntry{}, Quota: "unknown", RemoteBilling: "unverified", LiveAccess: "blocked"}
	if admission != nil {
		o.Source, o.LiveAccess = "production_connection", "connection_only"
		o.Material.DataSummary = "OpenAI subscription connection only; browser account/workspace selection and credential storage. No catalog, inference, refresh, retries or paid fallback."
		if admission.Profile == "openai_siwc_catalog_v1" {
			o.Source, o.LiveAccess = "production_catalog", "catalog_only"
			o.Material.DataSummary = "Single OpenAI model directory GET only. No prompt, code, files or conversation data; no login, credential writes, refresh, inference, retry or paid fallback."
		}
		if admission.Profile == "openai_siwc_inference_v1" {
			o.Source, o.LiveAccess = "production_inference", "inference_once"
			o.Material.DataSummary = "Single fixed synthetic prompt only; no tools, history, files, refresh, retry or paid fallback. No provider token or monetary cap; plan consumption possible; quota unknown."
		}
		o.MaterialDigest = domain.AcceptanceDigest(o.Material)
	}
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
	if o.Revision != revision || o.MaterialDigest != digest || domain.AcceptanceDigest(o.Material) != digest || !validAcceptanceMaterial(o) {
		return o, ErrConflict
	}
	if o.Source == "production_inference" && o.Material.InferenceBudget == nil {
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
		current, e := acceptanceGateSnapshot(tx, account, o.Material.Route.ID, o.Phase != "connection", o.Material.ConnectionPurpose, o.Material.ProductionAdmission, now)
		if e != nil {
			if !errors.Is(e, ErrConflict) && !errors.Is(e, ErrNotFound) {
				return o, e
			}
			invalid = "invalidated"
		} else if acceptanceSnapshotDigest(current) != acceptanceSnapshotDigest(o.Material) {
			invalid = "invalidated"
		} else if e = acceptancePrerequisite(tx, o.Material); e != nil {
			invalid = "invalidated"
		} else if o.Source == "production_inference" && validateInferenceBudget(tx, o, false) != nil {
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
		// The ledger entry and consumed operation commit or roll back together.
		// AUTOINCREMENT is durable across restarts and independent of clock time.
		claim, claimErr := tx.Exec(`INSERT INTO model_acceptance_claims(operation_id) VALUES(?)`, o.ID)
		if claimErr != nil {
			return o, claimErr
		}
		o.ClaimSequence, e = claim.LastInsertId()
		if e != nil {
			return o, e
		}
		if o.Source == "production_inference" {
			if e = debitInferenceBudget(tx, o); e != nil {
				return o, e
			}
		}
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
	return s.finishModelAcceptance(account, id, revision, state, certainty, code, catalog, input, output, usage, nil, now, nil)
}

// FinishModelAcceptanceWithDiagnostic is reserved for the trusted connection
// controller. Public proposal/decision/execution bodies never reach this API.
func (s *Store) FinishModelAcceptanceWithDiagnostic(account, id string, revision int64, state, certainty, code string, catalog []domain.ModelAcceptanceCatalogEntry, input, output int64, usage bool, diagnostic *domain.ConnectionDiagnostic, now time.Time) (domain.ModelAcceptanceOperation, error) {
	return s.finishModelAcceptance(account, id, revision, state, certainty, code, catalog, input, output, usage, diagnostic, now, nil)
}

func (s *Store) finishModelAcceptance(account, id string, revision int64, state, certainty, code string, catalog []domain.ModelAcceptanceCatalogEntry, input, output int64, usage bool, diagnostic *domain.ConnectionDiagnostic, now time.Time, expected *domain.ModelAcceptanceOperation, inference ...inferenceReceipt) (domain.ModelAcceptanceOperation, error) {
	var o domain.ModelAcceptanceOperation
	if state != "succeeded" && state != "failed" && state != "unknown" {
		return o, ErrConflict
	}
	if certainty != "completed" && certainty != "rejected" && certainty != "not_sent" && certainty != "unknown" {
		return o, ErrConflict
	}
	switch code {
	case "", "auth_invalid", "entitlement_denied", "quota_exhausted", "rate_limited", "request_rejected", "server_error", "transport_unknown", "response_incomplete":
	case "credential_access_failed", "credential_wait_expired":
		if expected == nil || expected.Source != "production_inference" || state != "failed" || certainty != "not_sent" {
			return o, ErrConflict
		}
	default:
		return o, ErrConflict
	}
	if len(catalog) > 128 || input < 0 || output < 0 {
		return o, ErrConflict
	}
	seen := map[string]bool{}
	for _, m := range catalog {
		catalogProduction := expected != nil && expected.Source == "production_catalog"
		if (!catalogProduction && (!acceptanceIdentifierPattern.MatchString(m.ID) || m.DisplayName != m.ID)) || (catalogProduction && !domain.ValidModelCatalogIdentifier(m.ID)) || seen[m.ID] || !domain.ValidModelCatalogName(m.DisplayName) {
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
	if o.State != "running" || o.Revision != revision || o.Attempts != 1 || !validAcceptanceMaterial(o) || domain.AcceptanceDigest(o.Material) != o.MaterialDigest {
		return o, ErrConflict
	}
	if diagnostic != nil && !validStoredConnectionDiagnosticForFinish(o, state, diagnostic) {
		return o, ErrConflict
	}
	if o.Material.ProductionAdmission != nil && expected == nil || expected != nil && !reflect.DeepEqual(o, *expected) {
		return o, ErrConflict
	}
	if o.Material.ConnectionPurpose != "" && (len(catalog) != 0 || input != 0 || output != 0 || usage) {
		return o, ErrConflict
	}
	if o.Source == "production_inference" {
		if code == "credential_access_failed" || code == "credential_wait_expired" {
			var sends int
			if len(inference) != 1 || inference[0].diagnostic != nil {
				return o, ErrConflict
			}
			if e = tx.QueryRow(`SELECT count(*) FROM model_acceptance_inference_sends WHERE operation_id=?`, o.ID).Scan(&sends); e != nil || sends != 0 {
				return o, ErrConflict
			}
		}
		if len(inference) != 1 || len(catalog) != 0 || diagnostic != nil || !validInferenceEvidence(state, certainty, code, inference[0].evidence, input, output, usage) {
			return o, ErrConflict
		}
		if inference[0].diagnostic != nil && !inference[0].diagnostic.Valid() {
			return o, ErrConflict
		}
		o.InferenceEvidence = inference[0].evidence
		o.InferenceDiagnostic = inference[0].diagnostic
		if certainty == "rejected" {
			var sequence int64
			if e = tx.QueryRow(`SELECT claim_sequence FROM model_acceptance_dispatches WHERE operation_id=?`, o.ID).Scan(&sequence); e != nil || sequence != o.ClaimSequence {
				return o, ErrConflict
			}
		}
	} else if len(inference) != 0 {
		return o, ErrConflict
	}
	if state == "succeeded" {
		if o.Source == "production_inference" {
			if e = validateInferenceSendWindow(tx, o, now); e != nil {
				return o, e
			}
		}
		if o.Source == "production_catalog" || o.Source == "production_inference" {
			claim := o
			if expected != nil {
				claim = *expected
			}
			if e = validateSingleRequestClaim(tx, claim, *o.Material.ProductionAdmission, now); e != nil {
				return o, e
			}
		}
		if o.Material.ProductionAdmission != nil {
			var sequence int64
			if e = tx.QueryRow(`SELECT claim_sequence FROM model_acceptance_dispatches WHERE operation_id=?`, o.ID).Scan(&sequence); e != nil || sequence != o.ClaimSequence {
				return o, ErrConflict
			}
		}
		m, e := acceptanceGateSnapshot(tx, account, o.Material.Route.ID, true, o.Material.ConnectionPurpose, o.Material.ProductionAdmission, now)
		if e != nil {
			return o, e
		}
		if !reflect.DeepEqual(m.Route, o.Material.Route) || o.Material.ConnectionPurpose != "" && !reflect.DeepEqual(m.Account, o.Material.Account) {
			return o, ErrConflict
		}
		if o.Material.ConnectionPurpose != "" && o.Material.Authorization != nil && m.Authorization.Binding.ID == o.Material.Authorization.Binding.ID {
			return o, ErrConflict
		}
		if o.Phase != "connection" && (!reflect.DeepEqual(m.Account, o.Material.Account) || acceptanceSnapshotDigest(m) != acceptanceSnapshotDigest(o.Material)) {
			return o, ErrConflict
		}
		o.ResultAccountRevision = m.Account.Revision
		o.ResultSnapshotDigest = acceptanceSnapshotDigest(m)
		if o.Source == "production_connection" && certainty == "completed" && code == "" && o.Material.ConnectionPurpose == "subscription_only" {
			if e = recordConnectionOrigin(tx, o.ID, m.Authorization); e != nil {
				return o, e
			}
		}
	}
	o.State, o.Certainty, o.Code = state, certainty, code
	o.Catalog = append([]domain.ModelAcceptanceCatalogEntry{}, catalog...)
	o.InputTokens, o.OutputTokens, o.UsageKnown = input, output, usage
	o.ConnectionDiagnostic = domain.CloneConnectionDiagnostic(diagnostic)
	o.FinishedAt = now.UTC().Format(time.RFC3339Nano)
	if e = acceptanceWrite(tx, &o); e != nil {
		return o, e
	}
	return o, tx.Commit()
}

func validStoredConnectionDiagnosticForFinish(o domain.ModelAcceptanceOperation, state string, diagnostic *domain.ConnectionDiagnostic) bool {
	if diagnostic == nil {
		return true
	}
	if state != "failed" && state != "unknown" {
		return false
	}
	copy := o
	copy.State = state
	return validStoredConnectionDiagnostic(copy, diagnostic)
}

func validStoredConnectionDiagnostic(o domain.ModelAcceptanceOperation, diagnostic *domain.ConnectionDiagnostic) bool {
	if diagnostic == nil || !diagnostic.Valid() || o.Phase != "connection" || o.Material.ConnectionMode != "browser_handoff" || o.Material.ConnectionPurpose != "subscription_only" || (o.State != "failed" && o.State != "unknown") {
		return false
	}
	return true
}

func validAcceptanceMaterial(o domain.ModelAcceptanceOperation) bool {
	m := o.Material
	if m.ProductionAdmission != nil && (m.ProductionAdmission.Profile == "openai_siwc_catalog_v1" || m.ProductionAdmission.Profile == "openai_siwc_inference_v1") {
		created, ce := time.Parse(time.RFC3339Nano, m.CreatedAt)
		expires, ee := time.Parse(time.RFC3339Nano, m.ExpiresAt)
		if (!m.ValidProductionCatalog() && !m.ValidProductionInference()) || ce != nil || ee != nil {
			return false
		}
		authExpiry, ae := time.Parse(time.RFC3339Nano, m.Authorization.ExpiresAt)
		want := created.Add(10 * time.Minute)
		if authExpiry.Before(want) {
			want = authExpiry
		}
		valid := ae == nil && expires.Equal(want) && expires.After(created) && o.AccountID == m.Account.ID && m.ConnectionID != ""
		if m.ValidProductionInference() {
			return valid && o.Source == "production_inference" && o.LiveAccess == "inference_once" && o.Phase == "inference" && m.PrerequisiteID != "" && validDigest(m.CatalogDigest)
		}
		return valid && o.Source == "production_catalog" && o.LiveAccess == "catalog_only" && o.Phase == "catalog" && m.ConnectionID == m.PrerequisiteID
	}
	if m.ProductionAdmission != nil && (o.Source != "production_connection" || o.LiveAccess != "connection_only") || m.ProductionAdmission == nil && o.Source != "local_simulation" {
		return false
	}
	if m.ProductionAdmission != nil {
		created, createdErr := time.Parse(time.RFC3339Nano, m.CreatedAt)
		expires, expiresErr := time.Parse(time.RFC3339Nano, m.ExpiresAt)
		if createdErr != nil || expiresErr != nil || !created.Add(10*time.Minute).Equal(expires) {
			return false
		}
	}
	return o.Phase == m.Phase && (o.Phase == "connection" || o.Phase == "catalog" || o.Phase == "inference") &&
		o.AccountID == m.Account.ID && m.Route.AccountID == m.Account.ID &&
		m.MaxAttempts == 1 && m.DeadlineSeconds > 0 && m.DeadlineSeconds <= 30 && m.ValidConnectionPurpose()
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
		// Legacy unbound inference never gains a debit or new execution/recovery
		// authority. A running row remains an unresolved account-effect fence.
		if o.Source == "production_inference" && o.Material.InferenceBudget == nil {
			continue
		}
		if !validAcceptanceMaterial(o) || domain.AcceptanceDigest(o.Material) != o.MaterialDigest {
			return ErrConflict
		}
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
