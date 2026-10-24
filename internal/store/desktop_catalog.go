package store

import (
	"database/sql"
	"foundry/internal/domain"
	"time"
)

// catalogSelectionTx reads durable, independently approved evidence in the
// caller's snapshot. It does not authorize inference or access secret storage.
func catalogSelectionTx(tx *sql.Tx, p domain.Profile, now time.Time) (domain.ModelCatalogProvenance, error) {
	var proof domain.ModelCatalogProvenance
	o, e := acceptanceTx(tx, p.AccountID, p.CatalogOperationID)
	if e != nil || !validAcceptanceMaterial(o) || o.Source != "production_catalog" || o.Phase != "catalog" || o.State != "succeeded" || o.Certainty != "completed" || o.Attempts != 1 || o.ClaimSequence < 1 || domain.AcceptanceDigest(o.Material) != o.MaterialDigest || o.Material.Route.ID != p.RouteID || o.Material.Route.BaseURL != p.BaseURL || o.Material.Route.Protocol != p.Protocol || o.Material.Authorization == nil {
		return proof, ErrConflict
	}
	found := false
	for _, entry := range o.Catalog {
		if entry.ID == p.Model {
			found = true
		}
	}
	if !found {
		return proof, ErrConflict
	}
	current, e := acceptanceGateSnapshot(tx, p.AccountID, p.RouteID, true, "", o.Material.ProductionAdmission, now)
	if e != nil || o.ResultAccountRevision != current.Account.Revision || o.ResultSnapshotDigest != acceptanceSnapshotDigest(current) || acceptanceSnapshotDigest(o.Material) != acceptanceSnapshotDigest(current) {
		return proof, ErrConflict
	}
	var sequence int64
	if e = tx.QueryRow(`SELECT claim_sequence FROM model_acceptance_dispatches WHERE operation_id=?`, o.ID).Scan(&sequence); e != nil || sequence != o.ClaimSequence {
		return proof, ErrConflict
	}
	current.ConnectionID, current.PrerequisiteID = o.Material.ConnectionID, o.Material.ConnectionID
	if catalogPrerequisite(tx, current) != nil {
		return proof, ErrConflict
	}
	return domain.ModelCatalogProvenance{OperationID: o.ID, ReceiptDigest: domain.AcceptanceDigest(o), AuthorizationGeneration: o.Material.Authorization.Generation, ExpiresAt: o.Material.Authorization.ExpiresAt}, nil
}

type DesktopCatalogChoice struct {
	OperationID string `json:"operation_id"`
	RouteID     string `json:"route_id"`
	Model       string `json:"model"`
	DisplayName string `json:"display_name"`
	ExpiresAt   string `json:"expires_at"`
}

// DesktopCatalogChoices filters recorded receipts against current lineage.
// Empty results mean unavailable/expired, not a provider refresh instruction.
func (s *Store) DesktopCatalogChoices(account string) ([]DesktopCatalogChoice, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	rows, e := tx.Query(`SELECT data FROM model_acceptance_operations WHERE account_id=? AND state='succeeded' AND json_extract(data,'$.source')='production_catalog' ORDER BY rowid DESC`, account)
	if e != nil {
		return nil, e
	}
	var receipts []domain.ModelAcceptanceOperation
	for rows.Next() {
		o, e := acceptanceRead(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		receipts = append(receipts, o)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	choices := []DesktopCatalogChoice{}
	seen := map[string]bool{}
	for _, o := range receipts {
		for _, entry := range o.Catalog {
			key := o.Material.Route.ID + "/" + entry.ID
			if seen[key] {
				continue
			}
			p := domain.Profile{AccountID: account, RouteID: o.Material.Route.ID, BaseURL: o.Material.Route.BaseURL, Protocol: o.Material.Route.Protocol, Model: entry.ID, CatalogOperationID: o.ID}
			proof, e := catalogSelectionTx(tx, p, time.Now())
			if e != nil {
				continue
			}
			seen[key] = true
			choices = append(choices, DesktopCatalogChoice{OperationID: o.ID, RouteID: p.RouteID, Model: entry.ID, DisplayName: entry.DisplayName, ExpiresAt: proof.ExpiresAt})
		}
	}
	return choices, nil
}
