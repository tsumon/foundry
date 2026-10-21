package domain

type RecoveryCheckStatus string

const (
	RecoveryVerified      RecoveryCheckStatus = "verified"
	RecoveryBlocked       RecoveryCheckStatus = "blocked"
	RecoveryUnknown       RecoveryCheckStatus = "unknown"
	RecoveryNotApplicable RecoveryCheckStatus = "not_applicable"
	RecoveryUnsupported   RecoveryCheckStatus = "unsupported"
)

type RecoveryAction string

const (
	RecoveryInspectRecords   RecoveryAction = "inspect_records"
	RecoveryReplan           RecoveryAction = "replan"
	RecoveryDeliveryDecision RecoveryAction = "delivery_decision"
	RecoveryNewTask          RecoveryAction = "new_task"
)

// RecoveryDiagnostic is a bounded observation, never execution authority. Actions
// identify existing decision surfaces; they do not bypass their live guards.
type RecoveryDiagnostic struct {
	SchemaVersion int              `json:"schema_version"`
	MissionID     string           `json:"mission_id"`
	Revision      int64            `json:"revision"`
	Epoch         int64            `json:"epoch"`
	CheckedAt     string           `json:"checked_at"`
	Stale         bool             `json:"stale"`
	Checks        []RecoveryCheck  `json:"checks"`
	NextActions   []RecoveryAction `json:"next_actions"`
}

type RecoveryRecordRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type RecoveryCheck struct {
	ID      string              `json:"id"`
	Status  RecoveryCheckStatus `json:"status"`
	Code    string              `json:"code"`
	Message string              `json:"message"`
	Records []RecoveryRecordRef `json:"records"`
}
