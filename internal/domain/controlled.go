package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	ToolModeControlledReadV1      = "controlled_read_v1"
	WorkspaceModeControlledReadV1 = "generations_controlled_read_v1"
)

func UsesGenerations(mode string) bool {
	base, _, err := BaseWorkspaceMode(mode)
	if err != nil {
		return false
	}
	mode = base
	return mode == WorkspaceModeGenerationsV1 || mode == WorkspaceModeControlledReadV1 || mode == WorkspaceModeControlledMCPReadV2
}

type SkillBinding struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
	Name     string `json:"name"`
	Role     Role   `json:"role"`
}

type GitSourceBinding struct {
	RepositoryID       string `json:"repository_id"`
	RepositoryRevision int64  `json:"repository_revision"`
	ObservationID      string `json:"observation_id"`
	Root               string `json:"root"`
	BaseCommit         string `json:"base_commit"`
}

type ReadBudget struct {
	MaxTurns            int   `json:"max_turns"`
	MaxCalls            int   `json:"max_calls"`
	MaxArgumentBytes    int   `json:"max_argument_bytes"`
	MaxResultBytes      int   `json:"max_result_bytes"`
	DeadlineSeconds     int   `json:"deadline_seconds"`
	MaxTotalResultBytes int64 `json:"max_total_result_bytes"`
}

type TaskBindings struct {
	Git           GitSourceBinding `json:"git"`
	Skills        []SkillBinding   `json:"skills"`
	PolicyVersion string           `json:"policy_version"`
	Tools         []string         `json:"tools"`
	Budget        ReadBudget       `json:"budget"`
}

type CapabilitySelection struct {
	RepositoryID       string         `json:"repository_id"`
	RepositoryRevision int64          `json:"repository_revision"`
	ObservationID      string         `json:"observation_id"`
	Skills             []SkillBinding `json:"skills"`
}

type ReadGrant struct {
	ID              string `json:"id"`
	MissionID       string `json:"mission_id"`
	Epoch           int64  `json:"epoch"`
	SessionID       string `json:"session_id"`
	Role            Role   `json:"role"`
	RequiredState   string `json:"required_state"`
	BindingDigest   string `json:"binding_digest"`
	AuthorityDigest string `json:"authority_digest"`
	CreatedAt       string `json:"created_at"`
}

type EvidenceEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	InvocationID  string `json:"invocation_id"`
	MissionID     string `json:"mission_id"`
	Epoch         int64  `json:"epoch"`
	Producer      string `json:"producer"`
	CapturedAt    string `json:"captured_at"`
	Digest        string `json:"digest"`
	Bytes         int64  `json:"bytes"`
	ContentType   string `json:"content_type"`
	BindingDigest string `json:"binding_digest"`
	Truncated     bool   `json:"truncated"`
}

type Invocation struct {
	ID                  string            `json:"id"`
	MissionID           string            `json:"mission_id"`
	Epoch               int64             `json:"epoch"`
	SessionID           string            `json:"session_id"`
	Ordinal             int64             `json:"ordinal"`
	Kind                string            `json:"kind"`
	Role                Role              `json:"role"`
	Name                string            `json:"name"`
	BindingDigest       string            `json:"binding_digest"`
	AuthorityDigest     string            `json:"authority_digest"`
	GrantID             string            `json:"grant_id,omitempty"`
	Input               EvidenceEnvelope  `json:"input"`
	Result              *EvidenceEnvelope `json:"result,omitempty"`
	Status              string            `json:"status"`
	ErrorCode           string            `json:"error_code,omitempty"`
	ReservedResultBytes int64             `json:"reserved_result_bytes"`
	CreatedAt           string            `json:"created_at"`
	DispatchedAt        string            `json:"dispatched_at,omitempty"`
	FinishedAt          string            `json:"finished_at,omitempty"`
	UsageKnown          bool              `json:"usage_known"`
	InputTokens         int64             `json:"input_tokens"`
	OutputTokens        int64             `json:"output_tokens"`
	ModelReceipt        *ModelCallReceipt `json:"model_receipt,omitempty"`
}

type CallUsage struct {
	UsageKnown   bool              `json:"usage_known"`
	InputTokens  int64             `json:"input_tokens"`
	OutputTokens int64             `json:"output_tokens"`
	ModelReceipt *ModelCallReceipt `json:"model_receipt,omitempty"`
}

// Non-authoritative, bounded provider outcome metadata; never an input grant.
type ModelCallReceipt struct {
	RequestID            string `json:"request_id,omitempty"`
	ResolvedModel        string `json:"resolved_model,omitempty"`
	CredentialGeneration int64  `json:"credential_generation,omitempty"`
	HTTPStatus           int    `json:"http_status,omitempty"`
	ProviderCode         string `json:"provider_code,omitempty"`
	ProviderParam        string `json:"provider_param,omitempty"`
	BodyShape            string `json:"body_shape,omitempty"`
	RetryAfterSeconds    int    `json:"retry_after_seconds,omitempty"`
	Certainty            string `json:"certainty"`
}

type InvocationDetail struct {
	Invocation      Invocation `json:"invocation"`
	InputPreview    string     `json:"input_preview"`
	ResultPreview   string     `json:"result_preview,omitempty"`
	InputTruncated  bool       `json:"input_truncated"`
	ResultTruncated bool       `json:"result_truncated"`
}

func FixedReadBudget() ReadBudget {
	return ReadBudget{
		MaxTurns:            4,
		MaxCalls:            3,
		MaxArgumentBytes:    8 << 10,
		MaxResultBytes:      64 << 10,
		DeadlineSeconds:     5,
		MaxTotalResultBytes: 192 << 10,
	}
}

func TaskBindingDigest(bindings TaskBindings) (string, error) {
	if err := validateTaskBindings(bindings); err != nil {
		return "", err
	}
	canonical := bindings
	if canonical.Skills == nil {
		canonical.Skills = []SkillBinding{}
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func validateTaskBindings(bindings TaskBindings) error {
	git := bindings.Git
	if !controlledText(git.RepositoryID, 256) || git.RepositoryRevision < 1 || !controlledText(git.ObservationID, 256) || !filepath.IsAbs(git.Root) || !controlledText(git.Root, 4096) || !controlledHex(git.BaseCommit, 40, 64) {
		return errors.New("invalid Git source binding")
	}
	if bindings.PolicyVersion != "repo_read_v1" || bindings.Budget != FixedReadBudget() {
		return errors.New("invalid controlled-read policy or budget")
	}
	wantTools := []string{"repo.list", "repo.read", "repo.search"}
	if len(bindings.Tools) != len(wantTools) {
		return errors.New("invalid controlled-read tools")
	}
	for i := range wantTools {
		if bindings.Tools[i] != wantTools[i] {
			return errors.New("invalid controlled-read tools")
		}
	}
	if len(bindings.Skills) > 3 {
		return errors.New("too many Skill bindings")
	}
	seen := make(map[string]bool, len(bindings.Skills))
	for _, skill := range bindings.Skills {
		key := skill.ID + "\x00" + string(skill.Role)
		if !controlledText(skill.ID, 256) || skill.Revision < 1 || !controlledHex(skill.Digest, 64) || !controlledText(skill.Name, 256) || !supportedRole(skill.Role) || seen[key] {
			return errors.New("invalid Skill binding")
		}
		seen[key] = true
	}
	return nil
}

func supportedRole(role Role) bool {
	switch role {
	case Main, Researcher, Planner, Worker, Debugger, QA, Auditor:
		return true
	default:
		return false
	}
}

func controlledText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max && !strings.ContainsRune(value, 0) && !strings.ContainsFunc(value, unicode.IsControl)
}

func controlledHex(value string, lengths ...int) bool {
	validLength := false
	for _, length := range lengths {
		if len(value) == length {
			validLength = true
			break
		}
	}
	if !validLength || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
