package store

import (
	"database/sql"
	"errors"

	"foundry/internal/domain"
)

// rolePolicyEvidenceTx reads only ledger/envelope metadata. It never opens a
// model input or output payload file and never treats completion as quality.
func rolePolicyEvidenceTx(tx *sql.Tx, invocationID string, role domain.Role, profileDigest string) (*domain.RolePolicyEvidence, bool, error) {
	v, err := scanInvocation(tx.QueryRow(invocationSelect+` WHERE id=?`, invocationID))
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if v.Kind != "model" || v.Role != role || (v.Status != "succeeded" && v.Status != "failed" && v.Status != "outcome_unknown" && v.Status != "interrupted_before_dispatch") {
		return nil, false, nil
	}
	var missionRaw []byte
	if err = tx.QueryRow(`SELECT data FROM missions WHERE id=?`, v.MissionID).Scan(&missionRaw); errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	mission, err := decodeMission(missionRaw)
	if err != nil {
		return nil, false, err
	}
	if err = domain.ValidateMissionModelSnapshot(mission); err != nil {
		return nil, false, err
	}
	profile, ok := mission.Profiles[role]
	if !ok {
		return nil, false, nil
	}
	missionDigest, err := domain.RolePolicyProfileDigest(profile)
	if err != nil {
		return nil, false, err
	}
	if missionDigest != profileDigest {
		return nil, false, nil
	}
	status := v.Status
	if status == "succeeded" {
		status = "completed"
	}
	environment := "unknown"
	if mission.ModelSnapshot != nil {
		if selection, found := mission.ModelSnapshot.Roles[role]; found && selection.Route != nil {
			environment = selection.Route.Environment
		}
	}
	evidence := &domain.RolePolicyEvidence{
		InvocationID: v.ID,
		MissionID:    v.MissionID,
		Role:         v.Role,
		Status:       status,
		ErrorCode:    rolePolicyEvidenceErrorCode(v.ErrorCode),
		Environment:  environment,
		FinishedAt:   v.FinishedAt,
	}
	return evidence, true, nil
}

func rolePolicyEvidenceErrorCode(code string) string {
	if code == "" {
		return ""
	}
	switch code {
	case "auth_invalid", "entitlement_denied", "quota_exhausted", "rate_limited", "request_rejected", "server_error", "transport_unknown", "model_transport_unknown", "response_incomplete", "live_provider_access_pending_confirmation", "dispatch_interrupted", "stale_dispatch", "result_seal_failed", "interrupted_before_dispatch":
		return code
	default:
		return "other_failure"
	}
}
