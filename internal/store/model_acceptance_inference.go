package store

import (
	"database/sql"
	"encoding/hex"
	"foundry/internal/domain"
	"time"
)

func freezeInferenceMaterial(m *domain.ModelAcceptanceMaterial) {
	m.Phase, m.RequestMethod, m.RequestURL = "inference", "POST", domain.OpenAIInferenceURL
	m.Model, m.InferencePrompt = domain.OpenAIInferenceModel, domain.OpenAIInferencePrompt
	m.PromptDigest, m.ResponseByteLimit = domain.AcceptanceDigest(domain.OpenAIInferencePrompt), 1<<20
}

func inferencePrerequisite(tx *sql.Tx, m domain.ModelAcceptanceMaterial) error {
	p, e := acceptanceTx(tx, m.Account.ID, m.PrerequisiteID)
	if e != nil || !validAcceptanceMaterial(p) || domain.AcceptanceDigest(p.Material) != p.MaterialDigest || domain.AcceptanceDigest(p) != m.CatalogDigest || p.Source != "production_catalog" || p.State != "succeeded" || p.Certainty != "completed" || p.Attempts != 1 || p.ClaimSequence < 1 || p.Material.Route != m.Route || p.ResultAccountRevision != m.Account.Revision || p.Material.ConnectionID != m.ConnectionID {
		return ErrConflict
	}
	comparison := m
	comparison.ProductionAdmission = p.Material.ProductionAdmission
	if p.ResultSnapshotDigest != acceptanceSnapshotDigest(comparison) {
		return ErrConflict
	}
	found := false
	for _, entry := range p.Catalog {
		if entry.ID == domain.OpenAIInferenceModel {
			found = true
		}
	}
	if !found {
		return ErrConflict
	}
	var sequence int64
	if e = tx.QueryRow(`SELECT claim_sequence FROM model_acceptance_dispatches WHERE operation_id=?`, p.ID).Scan(&sequence); e != nil || sequence != p.ClaimSequence {
		return ErrConflict
	}
	comparison.PrerequisiteID = m.ConnectionID
	return catalogPrerequisite(tx, comparison)
}

func (s *Store) ProposeProductionInference(account string, q domain.ModelAcceptanceProposal, admission domain.ProductionAdmission, now time.Time) (domain.ModelAcceptanceOperation, error) {
	if !admission.Valid() || admission.Profile != "openai_siwc_inference_v1" || q.Phase != "inference" || q.ConnectionPurpose != "" || q.Model != domain.OpenAIInferenceModel || q.PrerequisiteID == "" {
		return domain.ModelAcceptanceOperation{}, ErrConflict
	}
	return s.proposeModelAcceptance(account, q, "", &admission, now)
}

func (s *Store) ProductionInferenceReadiness(account, route string, admission domain.ProductionAdmission, now time.Time) string {
	if !admission.Valid() || admission.Profile != "openai_siwc_inference_v1" {
		return "admission_unavailable"
	}
	catalogAdmission := admission
	catalogAdmission.Profile = "openai_siwc_catalog_v1"
	if blocker := s.ProductionCatalogReadiness(account, route, catalogAdmission, now); blocker != "" {
		return blocker
	}
	tx, e := s.db.Begin()
	if e != nil {
		return "admission_unavailable"
	}
	defer tx.Rollback()
	m, e := acceptanceGateSnapshot(tx, account, route, true, "", &admission, now)
	if e != nil {
		return "authorization_unavailable"
	}
	rows, e := tx.Query(`SELECT data FROM model_acceptance_operations WHERE account_id=? AND state='succeeded' AND json_extract(data,'$.source')='production_catalog' ORDER BY rowid DESC`, account)
	if e != nil {
		return "prerequisite_unavailable"
	}
	var candidates []domain.ModelAcceptanceOperation
	for rows.Next() {
		p, err := acceptanceRead(rows)
		if err != nil {
			rows.Close()
			return "prerequisite_unavailable"
		}
		candidates = append(candidates, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return "prerequisite_unavailable"
	}
	for _, p := range candidates {
		m.PrerequisiteID, m.ConnectionID, m.CatalogDigest = p.ID, p.Material.ConnectionID, domain.AcceptanceDigest(p)
		if inferencePrerequisite(tx, m) == nil {
			return ""
		}
	}
	return "prerequisite_unavailable"
}

func (s *Store) ReserveProductionInferenceDispatch(claim domain.ModelAcceptanceOperation, admission domain.ProductionAdmission, now time.Time) error {
	if claim.Source != "production_inference" || admission.Profile != "openai_siwc_inference_v1" {
		return ErrConflict
	}
	return s.reserveProductionDispatch(claim, admission, now)
}
func (s *Store) ValidateProductionInferenceDispatch(claim domain.ModelAcceptanceOperation, admission domain.ProductionAdmission, now time.Time) error {
	if claim.Source != "production_inference" || admission.Profile != "openai_siwc_inference_v1" {
		return ErrConflict
	}
	return s.validateProductionDispatch(claim, admission, now)
}

// Credential access has its own material/authorization deadline. Only this
// immutable checkpoint starts the 30-second network window. A reserved dispatch
// alone cannot prove that any network attempt or successful response occurred.
func (s *Store) BeginProductionInferenceSend(claim domain.ModelAcceptanceOperation, admission domain.ProductionAdmission, now time.Time) error {
	if claim.Source != "production_inference" || admission.Profile != "openai_siwc_inference_v1" {
		return ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE model_acceptance_operations SET revision=revision WHERE id=?`, claim.ID); err != nil {
		return err
	}
	if err = validateSingleRequestClaim(tx, claim, admission, now); err != nil {
		return err
	}
	var sequence int64
	if err = tx.QueryRow(`SELECT claim_sequence FROM model_acceptance_dispatches WHERE operation_id=?`, claim.ID).Scan(&sequence); err != nil || sequence != claim.ClaimSequence {
		return ErrConflict
	}
	if _, err = tx.Exec(`INSERT INTO model_acceptance_inference_sends(operation_id,claim_sequence,started_at) VALUES(?,?,?)`, claim.ID, claim.ClaimSequence, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return ErrConflict
	}
	return tx.Commit()
}

func validateInferenceSendWindow(tx *sql.Tx, claim domain.ModelAcceptanceOperation, now time.Time) error {
	var sequence int64
	var raw string
	if err := tx.QueryRow(`SELECT claim_sequence,started_at FROM model_acceptance_inference_sends WHERE operation_id=?`, claim.ID).Scan(&sequence, &raw); err != nil || sequence != claim.ClaimSequence {
		return ErrConflict
	}
	start, err := time.Parse(time.RFC3339Nano, raw)
	claimStart, claimErr := time.Parse(time.RFC3339Nano, claim.StartedAt)
	if err != nil || claimErr != nil || start.Before(claimStart) || now.Before(start) || !start.Add(30*time.Second).After(now) {
		return ErrConflict
	}
	return nil
}

func validDigest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

func validInferenceEvidence(state, certainty, code string, e domain.InferenceEvidence, input, output int64, usage bool) bool {
	if e.RequestID != "" && !domain.ValidModelCatalogIdentifier(e.RequestID) {
		return false
	}
	if domain.SafeInferenceProviderCode(e.ProviderCode) != e.ProviderCode || domain.SafeInferenceProviderParam(e.ProviderParam) != e.ProviderParam {
		return false
	}
	if !usage && (input != 0 || output != 0) {
		return false
	}
	if state == "succeeded" {
		return certainty == "completed" && code == "" && e.TerminalEvent == "response.completed" && e.ResolvedModel == domain.OpenAIInferenceModel && e.OutputMatched != nil && validDigest(e.OutputDigest) && e.ProviderCode == "" && e.ProviderParam == ""
	}
	if e.TerminalEvent != "" && e.TerminalEvent != "response.failed" && e.TerminalEvent != "error" {
		return false
	}
	if certainty != "rejected" && e != (domain.InferenceEvidence{}) {
		return false
	}
	if usage && (state != "failed" || certainty != "rejected" || e.TerminalEvent != "response.failed") {
		return false
	}
	return e.OutputMatched == nil && e.OutputDigest == "" && e.ResolvedModel == "" && ((state == "failed" && (certainty == "rejected" || certainty == "not_sent")) || state == "unknown" && certainty == "unknown") && code != ""
}

type inferenceReceipt struct {
	evidence   domain.InferenceEvidence
	diagnostic *domain.InferenceDiagnostic
}

func (s *Store) FinishProductionInference(claim domain.ModelAcceptanceOperation, admission domain.ProductionAdmission, state, certainty, code string, evidence domain.InferenceEvidence, input, output int64, usage bool, now time.Time, diagnostics ...domain.InferenceDiagnostic) (domain.ModelAcceptanceOperation, error) {
	if claim.Material.InferenceBudget == nil {
		return domain.ModelAcceptanceOperation{}, ErrConflict
	}
	if !admission.Valid() || admission.Profile != "openai_siwc_inference_v1" || claim.Source != "production_inference" || !validAcceptanceMaterial(claim) || claim.Material.ProductionAdmission == nil || *claim.Material.ProductionAdmission != admission {
		return domain.ModelAcceptanceOperation{}, ErrConflict
	}
	receipt := inferenceReceipt{evidence: evidence}
	if len(diagnostics) > 1 {
		return domain.ModelAcceptanceOperation{}, ErrConflict
	}
	if len(diagnostics) == 1 {
		receipt.diagnostic = &diagnostics[0]
	}
	return s.finishModelAcceptance(claim.AccountID, claim.ID, claim.Revision, state, certainty, code, nil, input, output, usage, nil, now, &claim, receipt)
}
