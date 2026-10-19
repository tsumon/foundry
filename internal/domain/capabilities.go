package domain

import (
	"encoding/json"
	"errors"
)

const MCPProtocol = "2025-11-25"

var ErrInvalidCapability = errors.New("invalid capability")

// Capability metadata is untrusted descriptive data, never execution authority.
type MCPConnection struct {
	ID                 string `json:"id"`
	Revision           int64  `json:"revision"`
	Name               string `json:"name"`
	Endpoint           string `json:"endpoint"`
	Protocol           string `json:"protocol"`
	Enabled            bool   `json:"enabled"`
	CredentialRef      string `json:"credential_ref"`
	CredentialRevision int64  `json:"credential_revision"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

type MCPTool struct {
	Name             string          `json:"name"`
	Title            string          `json:"title"`
	Description      string          `json:"description"`
	InputSchema      json.RawMessage `json:"input_schema"`
	OutputSchema     json.RawMessage `json:"output_schema"`
	Annotations      json.RawMessage `json:"annotations"`
	RawDefinition    json.RawMessage `json:"raw_definition,omitempty"`
	DefinitionDigest string          `json:"definition_digest"`
}

type MCPToolSnapshot struct {
	ID                 string    `json:"id"`
	ConnectionID       string    `json:"connection_id"`
	ConnectionRevision int64     `json:"connection_revision"`
	Protocol           string    `json:"protocol"`
	ServerName         string    `json:"server_name"`
	ServerVersion      string    `json:"server_version"`
	Tools              []MCPTool `json:"tools"`
	Digest             string    `json:"digest"`
	ObservedAt         string    `json:"observed_at"`
	ExecutionVersion   int       `json:"execution_version,omitempty"`
}

type DiscoveryBudget struct {
	DeadlineSeconds       int   `json:"deadline_seconds"`
	RequestTimeoutSeconds int   `json:"request_timeout_seconds"`
	CleanupTimeoutSeconds int   `json:"cleanup_timeout_seconds"`
	MaxRequests           int   `json:"max_requests"`
	MaxPages              int   `json:"max_pages"`
	MaxResponseBytes      int64 `json:"max_response_bytes"`
	MaxTotalResponseBytes int64 `json:"max_total_response_bytes"`
	MaxEventBytes         int64 `json:"max_event_bytes"`
	MaxNotifications      int   `json:"max_notifications"`
	MaxTools              int   `json:"max_tools"`
	MaxSchemaBytes        int   `json:"max_schema_bytes"`
	MaxJSONDepth          int   `json:"max_json_depth"`
	MaxCursorBytes        int   `json:"max_cursor_bytes"`
}

func FixedDiscoveryBudget() DiscoveryBudget {
	return DiscoveryBudget{DeadlineSeconds: 15, RequestTimeoutSeconds: 5, CleanupTimeoutSeconds: 1, MaxRequests: 13, MaxPages: 10, MaxResponseBytes: 1 << 20, MaxTotalResponseBytes: 4 << 20, MaxEventBytes: 1 << 20, MaxNotifications: 64, MaxTools: 500, MaxSchemaBytes: 64 << 10, MaxJSONDepth: 32, MaxCursorBytes: 4096}
}

type DiscoveryOperation struct {
	ID                 string          `json:"id"`
	ConnectionID       string          `json:"connection_id"`
	ConnectionRevision int64           `json:"connection_revision"`
	Endpoint           string          `json:"endpoint"`
	CredentialRevision int64           `json:"credential_revision"`
	Consent            bool            `json:"consent"`
	AuthorizedAt       string          `json:"authorized_at"`
	Status             string          `json:"status"`
	Budget             DiscoveryBudget `json:"budget"`
	Requests           *int            `json:"requests"`
	ResponseBytes      *int64          `json:"response_bytes"`
	SnapshotID         string          `json:"snapshot_id"`
	ErrorCode          string          `json:"error_code"`
	CreatedAt          string          `json:"created_at"`
	FinishedAt         string          `json:"finished_at"`
}

type SkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type SkillRevision struct {
	ID          string      `json:"id"`
	Revision    int64       `json:"revision"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Source      string      `json:"source"`
	Files       []SkillFile `json:"files"`
	Digest      string      `json:"digest"`
	CreatedAt   string      `json:"created_at"`
}

type GitRepository struct {
	ID        string `json:"id"`
	Revision  int64  `json:"revision"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	ProjectID string `json:"project_id"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type GitObservation struct {
	ID                 string `json:"id"`
	RepositoryID       string `json:"repository_id"`
	RepositoryRevision int64  `json:"repository_revision"`
	Root               string `json:"root"`
	Branch             string `json:"branch"`
	HEAD               string `json:"head"`
	Detached           bool   `json:"detached"`
	Unborn             bool   `json:"unborn"`
	CheckedAt          string `json:"checked_at"`
	Status             string `json:"status"`
	ErrorCode          string `json:"error_code"`
	WorkingTreeStatus  string `json:"working_tree_status"`
}

type BuiltinCapability struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	AgentCallable bool   `json:"agent_callable"`
}

type CapabilitySnapshot struct {
	Connections         []MCPConnection      `json:"connections"`
	Snapshots           []MCPToolSnapshot    `json:"snapshots"`
	Discoveries         []DiscoveryOperation `json:"discoveries"`
	Skills              []SkillRevision      `json:"skills"`
	Repositories        []GitRepository      `json:"repositories"`
	GitObservations     []GitObservation     `json:"git_observations"`
	BuiltinCapabilities []BuiltinCapability  `json:"builtin_capabilities"`
	ToolExecution       bool                 `json:"tool_execution"`
	SkillExecution      bool                 `json:"skill_execution"`
	GitWrite            bool                 `json:"git_write"`
}
