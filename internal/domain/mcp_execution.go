package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
)

const (
	ToolModeControlledMCPReadV1      = "controlled_mcp_read_v1"
	WorkspaceModeControlledMCPReadV1 = "generations_controlled_mcp_read_v1"
)

type MCPBudget struct {
	MaxCalls              int   `json:"max_calls"`
	MaxArgumentBytes      int   `json:"max_argument_bytes"`
	MaxResultBytes        int   `json:"max_result_bytes"`
	MaxTotalResultBytes   int64 `json:"max_total_result_bytes"`
	DeadlineSeconds       int   `json:"deadline_seconds"`
	MaxRequests           int   `json:"max_requests"`
	MaxPages              int   `json:"max_pages"`
	MaxTools              int   `json:"max_tools"`
	MaxResponseBytes      int64 `json:"max_response_bytes"`
	MaxTotalResponseBytes int64 `json:"max_total_response_bytes"`
	MaxEventBytes         int64 `json:"max_event_bytes"`
	MaxNotifications      int   `json:"max_notifications"`
	RequestTimeoutSeconds int   `json:"request_timeout_seconds"`
	CleanupTimeoutSeconds int   `json:"cleanup_timeout_seconds"`
}

func FixedMCPBudget() MCPBudget {
	return MCPBudget{3, 8 << 10, 64 << 10, 192 << 10, 30, 14, 10, 500, 1 << 20, 4 << 20, 1 << 20, 64, 5, 1}
}

type MCPToolBinding struct {
	Name               string          `json:"name"`
	DefinitionDigest   string          `json:"definition_digest"`
	InputSchemaDigest  string          `json:"input_schema_digest"`
	OutputSchemaDigest string          `json:"output_schema_digest,omitempty"`
	Policy             json.RawMessage `json:"policy"`
}

type MCPTaskBindings struct {
	ConnectionID         string           `json:"connection_id"`
	ConnectionRevision   int64            `json:"connection_revision"`
	Endpoint             string           `json:"endpoint"`
	Protocol             string           `json:"protocol"`
	CredentialRef        string           `json:"credential_ref"`
	CredentialRevision   int64            `json:"credential_revision"`
	DiscoveryOperationID string           `json:"discovery_operation_id"`
	SnapshotID           string           `json:"snapshot_id"`
	SnapshotDigest       string           `json:"snapshot_digest"`
	Tools                []MCPToolBinding `json:"tools"`
	Budget               MCPBudget        `json:"budget"`
}

// MCPBindingDigest leaves the R3 digest and serialization untouched. Policies
// are frozen bytes; semantic validation belongs to the execution validator.
func MCPBindingDigest(repo TaskBindings, remote MCPTaskBindings) (string, error) {
	repositoryDigest, err := TaskBindingDigest(repo)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(remote.Endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return "", errors.New("MCP execution requires literal-loopback HTTP")
	}
	if !controlledText(remote.ConnectionID, 256) || remote.ConnectionRevision < 1 || remote.Protocol != MCPProtocol || !controlledText(remote.DiscoveryOperationID, 256) || !controlledText(remote.SnapshotID, 256) || !controlledHex(remote.SnapshotDigest, 64) || remote.CredentialRevision < 0 || (remote.CredentialRef != "" && (!controlledText(remote.CredentialRef, 256) || remote.CredentialRevision < 1)) || remote.Budget != FixedMCPBudget() || len(remote.Tools) < 1 || len(remote.Tools) > 3 {
		return "", errors.New("invalid MCP binding")
	}
	seen := map[string]bool{}
	for _, tool := range remote.Tools {
		if !controlledText(tool.Name, 256) || seen[tool.Name] || !controlledHex(tool.DefinitionDigest, 64) || !controlledHex(tool.InputSchemaDigest, 64) || (tool.OutputSchemaDigest != "" && !controlledHex(tool.OutputSchemaDigest, 64)) || len(tool.Policy) == 0 || len(tool.Policy) > 64<<10 || !reviewedMCPPolicy(tool.Policy) {
			return "", errors.New("invalid MCP tool binding")
		}
		seen[tool.Name] = true
	}
	raw, err := json.Marshal(struct {
		RepositoryDigest string          `json:"repository_digest"`
		MCP              MCPTaskBindings `json:"mcp"`
	}{repositoryDigest, remote})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Full schema semantics are independently checked by mcp.ValidateArguments at
// every execution boundary. This fence never derives local review from tools.
func reviewedMCPPolicy(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 3 {
		return false
	}
	var version string
	var reviewed bool
	var schema map[string]json.RawMessage
	if json.Unmarshal(fields["policy_version"], &version) != nil || version != "mcp_read_v1" || json.Unmarshal(fields["reviewed"], &reviewed) != nil || !reviewed || json.Unmarshal(fields["schema"], &schema) != nil {
		return false
	}
	var typ string
	var properties map[string]json.RawMessage
	var additional bool
	return json.Unmarshal(schema["type"], &typ) == nil && typ == "object" && json.Unmarshal(schema["properties"], &properties) == nil && properties != nil && string(schema["additionalProperties"]) == "false" && json.Unmarshal(schema["additionalProperties"], &additional) == nil && !additional
}

type MCPProposalSource struct {
	Version           int    `json:"version"`
	MissionID         string `json:"mission_id"`
	ModelInvocationID string `json:"model_invocation_id"`
	Purpose           string `json:"purpose"`
	Epoch             int64  `json:"epoch"`
	SessionID         string `json:"session_id"`
	ReadGrantID       string `json:"read_grant_id"`
	BindingDigest     string `json:"binding_digest"`
	AuthorityDigest   string `json:"authority_digest"`
	CreatedAt         string `json:"created_at"`
}

type MCPProposal struct {
	Version            int              `json:"version"`
	ID                 string           `json:"id"`
	MissionID          string           `json:"mission_id"`
	Epoch              int64            `json:"epoch"`
	SessionID          string           `json:"session_id"`
	SourceInvocationID string           `json:"source_invocation_id"`
	SourceResult       EvidenceEnvelope `json:"source_result"`
	ReadGrantID        string           `json:"read_grant_id"`
	BindingDigest      string           `json:"binding_digest"`
	AuthorityDigest    string           `json:"authority_digest"`
	ToolID             string           `json:"tool_id"`
	Arguments          json.RawMessage  `json:"arguments"`
	Digest             string           `json:"digest"`
	CreatedAt          string           `json:"created_at"`
}

type MCPCallGrant struct {
	Version          int              `json:"version"`
	ID               string           `json:"id"`
	MissionID        string           `json:"mission_id"`
	ProposalID       string           `json:"proposal_id"`
	ProposalDigest   string           `json:"proposal_digest"`
	RequestID        string           `json:"request_id"`
	ExpectedRevision int64            `json:"expected_revision"`
	Epoch            int64            `json:"epoch"`
	SessionID        string           `json:"session_id"`
	BindingDigest    string           `json:"binding_digest"`
	AuthorityDigest  string           `json:"authority_digest"`
	Binding          MCPTaskBindings  `json:"binding"`
	Profiles         map[Role]Profile `json:"profiles"`
	CreatedAt        string           `json:"created_at"`
}

type MCPInvocation struct {
	Version             int               `json:"version"`
	ID                  string            `json:"id"`
	MissionID           string            `json:"mission_id"`
	Epoch               int64             `json:"epoch"`
	SessionID           string            `json:"session_id"`
	Ordinal             int64             `json:"ordinal"`
	ProposalID          string            `json:"proposal_id"`
	GrantID             string            `json:"grant_id"`
	BindingDigest       string            `json:"binding_digest"`
	AuthorityDigest     string            `json:"authority_digest"`
	ReservedResultBytes int64             `json:"reserved_result_bytes"`
	Status              string            `json:"status"`
	Phase               string            `json:"phase"`
	Result              *EvidenceEnvelope `json:"result,omitempty"`
	ErrorCode           string            `json:"error_code,omitempty"`
	Requests            int               `json:"requests"`
	RequestBytes        int64             `json:"request_bytes"`
	ResponseBytes       int64             `json:"response_bytes"`
	DurationMillis      int64             `json:"duration_millis"`
	CreatedAt           string            `json:"created_at"`
	DispatchedAt        string            `json:"dispatched_at,omitempty"`
	FinishedAt          string            `json:"finished_at,omitempty"`
}

type MCPCallOutcome struct {
	Status         string            `json:"status"`
	Result         *EvidenceEnvelope `json:"result,omitempty"`
	ErrorCode      string            `json:"error_code,omitempty"`
	Requests       int               `json:"requests"`
	RequestBytes   int64             `json:"request_bytes"`
	ResponseBytes  int64             `json:"response_bytes"`
	DurationMillis int64             `json:"duration_millis"`
}
