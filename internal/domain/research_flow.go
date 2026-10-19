package domain

const (
	ToolModeControlledMCPReadV2      = "controlled_mcp_read_v2"
	WorkspaceModeControlledMCPReadV2 = "generations_controlled_mcp_read_v2"
)

// IsControlledMCPRead requires a matching version pair, including for drafts.
func IsControlledMCPRead(workspaceMode, toolMode string) bool {
	return workspaceMode == WorkspaceModeControlledMCPReadV1 && toolMode == ToolModeControlledMCPReadV1 || IsResearchFlowV2(workspaceMode, toolMode)
}

func IsResearchFlowV2(workspaceMode, toolMode string) bool {
	base, _, err := BaseWorkspaceMode(workspaceMode)
	if err != nil {
		return false
	}
	workspaceMode = base
	return workspaceMode == WorkspaceModeControlledMCPReadV2 && toolMode == ToolModeControlledMCPReadV2
}

// ResearchSourceRef is an MCP citation, never an alternate repository path.
type ResearchSourceRef struct {
	Kind         string `json:"kind"`
	InvocationID string `json:"invocation_id"`
	Digest       string `json:"digest"`
}

type ResearchObservationRef struct {
	Kind         string           `json:"kind"`
	InvocationID string           `json:"invocation_id"`
	Result       EvidenceEnvelope `json:"result"`
}

type ResearchTurnApplication struct {
	Version            int              `json:"version"`
	MissionID          string           `json:"mission_id"`
	Epoch              int64            `json:"epoch"`
	SessionID          string           `json:"session_id"`
	SourceInvocationID string           `json:"source_invocation_id"`
	SourceResult       EvidenceEnvelope `json:"source_result"`
	Kind               string           `json:"kind"`
	TargetID           string           `json:"target_id"`
	Research           *Research        `json:"research,omitempty"`
	CreatedAt          string           `json:"created_at"`
}

type ResearchConsumption struct {
	Version              int                    `json:"version"`
	MissionID            string                 `json:"mission_id"`
	Epoch                int64                  `json:"epoch"`
	SessionID            string                 `json:"session_id"`
	Observation          ResearchObservationRef `json:"observation"`
	ConsumerInvocationID string                 `json:"consumer_invocation_id"`
	CreatedAt            string                 `json:"created_at"`
}

type ResearchPlannerCheckpoint struct {
	Version             int    `json:"version"`
	MissionID           string `json:"mission_id"`
	Epoch               int64  `json:"epoch"`
	SessionID           string `json:"session_id"`
	SourceInvocationID  string `json:"source_invocation_id"`
	PlannerInvocationID string `json:"planner_invocation_id"`
	CreatedAt           string `json:"created_at"`
}

type ResearchPlanApplication struct {
	Version             int              `json:"version"`
	MissionID           string           `json:"mission_id"`
	Epoch               int64            `json:"epoch"`
	SessionID           string           `json:"session_id"`
	PlannerInvocationID string           `json:"planner_invocation_id"`
	PlannerResult       EvidenceEnvelope `json:"planner_result"`
	Plan                Plan             `json:"plan"`
	Proposal            Approval         `json:"proposal"`
	CreatedAt           string           `json:"created_at"`
}
