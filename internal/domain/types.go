package domain

type Role string

const (
	Main       Role = "main"
	Researcher Role = "researcher"
	Planner    Role = "planner"
	Worker     Role = "worker"
	Debugger   Role = "debugger"
	QA         Role = "qa"
	Auditor    Role = "auditor"
)

type Profile struct {
	RouteID           string `json:"route_id,omitempty"`
	AccountID         string `json:"account_id,omitempty"`
	AccountRevision   int64  `json:"account_revision,omitempty"`
	CredentialVersion int64  `json:"credential_version,omitempty"`
	ID                string `json:"id"`
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	Protocol          string `json:"protocol"`
	BaseURL           string `json:"base_url"`
	Model             string `json:"model"`
	HasKey            bool   `json:"has_key"`
}

type Settings struct {
	DefaultProfileID      string          `json:"default_profile_id"`
	RoleBindings          map[Role]string `json:"role_bindings"`
	ModelPolicyDecisionID string          `json:"model_policy_decision_id,omitempty"`
}

type Spec struct {
	Scope      []string `json:"scope"`
	Protected  []string `json:"protected"`
	Acceptance []string `json:"acceptance"`
	Image      string   `json:"image"`
	ImageID    string   `json:"image_id"`
	ProfileID  string   `json:"profile_id"`
	Research   bool     `json:"research"`
	MaxCalls   int      `json:"max_calls"`
}

type FileMeta struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type Step struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	DependsOn   []string `json:"depends_on"`
	Files       []string `json:"files"`
	Status      string   `json:"status"`
}

type Plan struct {
	Adjustments []PlanAdjustment `json:"adjustments,omitempty"`
	Revision    int64            `json:"revision"`
	Summary     string           `json:"summary"`
	Steps       []Step           `json:"steps"`
}

type Finding struct {
	Severity string              `json:"severity"`
	Summary  string              `json:"summary"`
	Paths    []string            `json:"paths"`
	Sources  []ResearchSourceRef `json:"sources,omitempty"`
}

type Research struct {
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
	Unknowns []string  `json:"unknowns"`
}

type Change struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type CommandResult struct {
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
}

type Verification struct {
	ID                 string          `json:"id"`
	CandidateDigest    string          `json:"candidate_digest"`
	ImageID            string          `json:"image_id"`
	CreatedAt          string          `json:"created_at"`
	AcceptanceRevision int64           `json:"acceptance_revision"`
	Results            []CommandResult `json:"results"`
	Passed             bool            `json:"passed"`
	SourceVersion      int             `json:"source_version,omitempty"`
	SourceKind         string          `json:"source_kind,omitempty"`
}

// EffectiveSource never upgrades legacy or unsupported provenance. Raw fields
// remain unchanged on decode/encode so historical material seals stay valid.
func (v Verification) EffectiveSource() string {
	if v.SourceVersion == 1 {
		switch v.SourceKind {
		case "synthetic_stub", "synthetic_content_check", "isolated_execution":
			return v.SourceKind
		}
	}
	return "unknown"
}

func (v Verification) SourceLabel() string {
	switch v.EffectiveSource() {
	case "synthetic_stub":
		return "流程模拟（未执行命令）"
	case "synthetic_content_check":
		return "内容检查模拟（未执行命令）"
	case "isolated_execution":
		return "隔离命令执行"
	default:
		return "来源未记录"
	}
}

func (v Verification) OutcomeLabel() string {
	prefix := "验证记录"
	switch v.EffectiveSource() {
	case "synthetic_stub", "synthetic_content_check":
		prefix = "模拟验证"
	case "isolated_execution":
		prefix = "验收命令"
	}
	if v.Passed {
		return prefix + "通过"
	}
	return prefix + "未通过"
}

type Review struct {
	CodingVersion   int                 `json:"coding_version,omitempty"`
	PlanRevision    int64               `json:"plan_revision,omitempty"`
	TraceDigest     string              `json:"trace_digest,omitempty"`
	BindingDigest   string              `json:"binding_digest,omitempty"`
	Checks          []CodingReviewCheck `json:"checks,omitempty"`
	Role            Role                `json:"role"`
	Passed          bool                `json:"passed"`
	Summary         string              `json:"summary"`
	Findings        []Finding           `json:"findings"`
	CandidateDigest string              `json:"candidate_digest"`
	EvidenceDigest  string              `json:"evidence_digest,omitempty"`
}

type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

type Approval struct {
	PlanRevision int64  `json:"plan_revision,omitempty"`
	Kind         string `json:"kind"`
	Digest       string `json:"digest"`
	CreatedAt    string `json:"created_at"`
	Revision     int64  `json:"revision"`
}

type Budget struct {
	MaxCalls     int   `json:"max_calls"`
	UsedCalls    int   `json:"used_calls"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	UsageUnknown bool  `json:"usage_unknown"`
}

type Delivery struct {
	OperationID     string `json:"operation_id,omitempty"`
	PatchSHA256     string `json:"patch_sha256,omitempty"`
	ReportSHA256    string `json:"report_sha256,omitempty"`
	PatchPath       string `json:"patch_path"`
	ReportPath      string `json:"report_path"`
	CandidateDigest string `json:"candidate_digest"`
	CreatedAt       string `json:"created_at"`
}

type Mission struct {
	ReplanContext      *FailureBundle  `json:"replan_context,omitempty"`
	CodingVersion      int             `json:"coding_version,omitempty"`
	CodingAttempts     []CodingAttempt `json:"coding_attempts,omitempty"`
	CodingTraceInvalid bool            `json:"-"`
	// ModelPolicySnapshotInvalid is a read-only decode diagnostic. It never
	// authorizes repair or serialization of malformed persisted provenance.
	ModelPolicySnapshotInvalid bool                       `json:"-"`
	ModelPolicySnapshot        *MissionRolePolicySnapshot `json:"model_policy_snapshot,omitempty"`
	// ModelSnapshotInvalid is a read-only decode diagnostic. It never authorizes
	// repair or serialization of malformed persisted snapshot bytes.
	ModelSnapshotInvalid bool                  `json:"-"`
	ModelSnapshot        *MissionModelSnapshot `json:"model_snapshot,omitempty"`
	WorkspaceMode        string                `json:"workspace_mode,omitempty"`
	CandidateGeneration  *GenerationRef        `json:"candidate_generation,omitempty"`
	ToolMode             string                `json:"tool_mode,omitempty"`
	TaskBindings         *TaskBindings         `json:"task_bindings,omitempty"`
	MCPBindings          *MCPTaskBindings      `json:"mcp_bindings,omitempty"`
	ID                   string                `json:"id"`
	Title                string                `json:"title"`
	Goal                 string                `json:"goal"`
	RepoPath             string                `json:"repo_path"`
	BaseCommit           string                `json:"base_commit"`
	State                string                `json:"state"`
	Stage                string                `json:"stage"`
	Revision             int64                 `json:"revision"`
	Epoch                int64                 `json:"epoch"`
	Spec                 Spec                  `json:"spec"`
	Profiles             map[Role]Profile      `json:"profiles"`
	ContextManifest      []FileMeta            `json:"context_manifest"`
	Research             *Research             `json:"research"`
	Plan                 *Plan                 `json:"plan"`
	Messages             []Message             `json:"messages"`
	Changes              []Change              `json:"changes"`
	Evidence             []Verification        `json:"evidence"`
	Reviews              []Review              `json:"reviews"`
	Approvals            []Approval            `json:"approvals"`
	Budget               Budget                `json:"budget"`
	CandidateDigest      string                `json:"candidate_digest"`
	Error                string                `json:"error"`
	CreatedAt            string                `json:"created_at"`
	UpdatedAt            string                `json:"updated_at"`
	Delivery             *Delivery             `json:"delivery"`
	RunArchives          []RunArchive          `json:"run_archives,omitempty"`
}

// RunArchive is historical data, never current passing delivery evidence.
// Version-one replans bind its exact digest as failure material, not approval.
type RunArchive struct {
	CodingAttempts      []CodingAttempt `json:"coding_attempts,omitempty"`
	CandidateGeneration *GenerationRef  `json:"candidate_generation,omitempty"`
	Epoch               int64           `json:"epoch"`
	PlanRevision        int64           `json:"plan_revision"`
	Revision            int64           `json:"revision"`
	ArchivedAt          string          `json:"archived_at"`
	State               string          `json:"state"`
	Error               string          `json:"error"`
	ImageID             string          `json:"image_id"`
	CandidateDigest     string          `json:"candidate_digest"`
	Plan                *Plan           `json:"plan"`
	Research            *Research       `json:"research"`
	Changes             []Change        `json:"changes"`
	Evidence            []Verification  `json:"evidence"`
	Reviews             []Review        `json:"reviews"`
	Delivery            *Delivery       `json:"delivery"`
}

type Event struct {
	Seq       int64  `json:"seq"`
	MissionID string `json:"mission_id"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"created_at"`
	Revision  int64  `json:"revision"`
}
