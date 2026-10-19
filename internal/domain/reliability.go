package domain

const WorkspaceModeGenerationsV1 = "generations_v1"

type GenerationRef struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// PreparedExport describes output to verify, not permission to read arbitrary paths.
type PreparedExport struct {
	PatchPath    string `json:"patch_path"`
	ReportPath   string `json:"report_path"`
	PatchSHA256  string `json:"patch_sha256"`
	ReportSHA256 string `json:"report_sha256"`
	ReportInput  string `json:"report_input"`
}

type LocalOperationResult struct {
	Generation *GenerationRef  `json:"generation,omitempty"`
	Changes    []Change        `json:"changes,omitempty"`
	Export     *PreparedExport `json:"export,omitempty"`
}

// LocalOperation is a durable local intent, never execution authority. Report
// input is fixed in the pending record before any export filesystem dispatch.
type LocalOperation struct {
	ID               string                `json:"id"`
	MissionID        string                `json:"mission_id"`
	Kind             string                `json:"kind"`
	Status           string                `json:"status"`
	ExpectedRevision int64                 `json:"expected_revision"`
	Epoch            int64                 `json:"epoch"`
	PlanRevision     int64                 `json:"plan_revision"`
	Before           GenerationRef         `json:"before"`
	BaseDigest       string                `json:"base_digest"`
	IntentDigest     string                `json:"intent_digest"`
	ApprovalDigest   string                `json:"approval_digest"`
	ExportReport     string                `json:"export_report,omitempty"`
	Result           *LocalOperationResult `json:"result,omitempty"`
	Error            string                `json:"error,omitempty"`
	CreatedAt        string                `json:"created_at"`
	UpdatedAt        string                `json:"updated_at"`
}

// PlanRevision is append-only history; execution must use the current proposal.
type PlanRevision struct {
	MissionID          string `json:"mission_id"`
	PlanRevision       int64  `json:"plan_revision"`
	ParentPlanRevision int64  `json:"parent_plan_revision"`
	Epoch              int64  `json:"epoch"`
	MissionRevision    int64  `json:"mission_revision"`
	Source             string `json:"source"`
	Reason             string `json:"reason"`
	Plan               Plan   `json:"plan"`
	PlanDigest         string `json:"plan_digest"`
	CreatedAt          string `json:"created_at"`
}
