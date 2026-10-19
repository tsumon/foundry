package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"foundry/internal/domain"
	"foundry/internal/mcp"
	"foundry/internal/store"
	"foundry/internal/workspace"
)

func isLocalMCP(m domain.Mission) bool {
	return domain.IsControlledMCPRead(m.WorkspaceMode, m.ToolMode) && m.TaskBindings != nil && m.MCPBindings != nil
}
func mcpHash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// Independent domain separation intentionally leaves every R3 authority byte unchanged.
func mcpAuthority(m domain.Mission, base string) (string, error) {
	if !isLocalMCP(m) {
		return "", errors.New("MCP mode and immutable bindings required")
	}
	binding, err := domain.MCPBindingDigest(*m.TaskBindings, *m.MCPBindings)
	if err != nil {
		return "", err
	}
	digest, err := workspace.Digest(base)
	if err != nil {
		return "", err
	}
	if _, err = workspace.Context(base, m.ContextManifest); err != nil {
		return "", err
	}
	if m.TaskBindings.Git.BaseCommit != m.BaseCommit || m.Budget.MaxCalls != m.Spec.MaxCalls || m.Budget.UsedCalls < 0 || m.Budget.UsedCalls > m.Budget.MaxCalls {
		return "", errors.New("MCP frozen inputs changed")
	}
	inputs, err := workspace.AcceptanceInputs(base, m.Spec.Acceptance)
	if err != nil {
		return "", err
	}
	for _, input := range inputs {
		protected := false
		for _, rule := range m.Spec.Protected {
			protected = protected || workspace.Matches(input, rule)
		}
		if !protected {
			return "", errors.New("MCP acceptance input not protected")
		}
	}
	raw, err := json.Marshal(struct {
		Mode, ToolMode, Binding, Goal, Commit, Base string
		Spec                                        domain.Spec
		Profiles                                    map[domain.Role]domain.Profile
		Manifest                                    []domain.FileMeta
	}{m.WorkspaceMode, m.ToolMode, binding, m.Goal, m.BaseCommit, digest, m.Spec, m.Profiles, m.ContextManifest})
	if err != nil {
		return "", err
	}
	return mcpHash(append([]byte("FOUNDRY MCP context authority v1\x00"), raw...)), nil
}

func (e *Engine) mcpCurrent(m domain.Mission) error {
	if err := e.s.CheckMCPLedgerVersion(m.ID); err != nil {
		return err
	}
	var auth string
	var err error
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		auth, err = authority(m, filepath.Join(e.dir(m.ID), "base"), false)
	} else {
		auth, err = mcpAuthority(m, filepath.Join(e.dir(m.ID), "base"))
	}
	if err != nil {
		return err
	}
	if !hasSeal(m, "snapshot", auth) || !hasSeal(m, "context", auth) {
		return errors.New("current MCP snapshot and context approval required")
	}
	if _, err = validateBoundSkills(m.TaskBindings, "", e.s.GetSkillRevision); err != nil {
		return err
	}
	b := m.MCPBindings
	c, err := e.s.GetMCPConnection(b.ConnectionID)
	if err != nil {
		return err
	}
	if !c.Enabled || c.ID != b.ConnectionID || c.Revision != b.ConnectionRevision || c.Endpoint != b.Endpoint || c.Protocol != b.Protocol || c.CredentialRef != b.CredentialRef || c.CredentialRevision != b.CredentialRevision {
		return store.ErrConflict
	}
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return e.localMCPCurrent(m)
	}
	return nil
}

func (e *Engine) mcpTool(m domain.Mission, name string) (domain.MCPTool, domain.MCPToolBinding, error) {
	if !isLocalMCP(m) {
		return domain.MCPTool{}, domain.MCPToolBinding{}, store.ErrConflict
	}
	b := m.MCPBindings
	s, err := e.s.GetMCPExecutionSnapshot(b.ConnectionID, b.SnapshotID)
	if err != nil {
		return domain.MCPTool{}, domain.MCPToolBinding{}, err
	}
	op, err := e.s.GetDiscovery(b.DiscoveryOperationID)
	if err != nil {
		return domain.MCPTool{}, domain.MCPToolBinding{}, err
	}
	if s.Digest != b.SnapshotDigest || s.ConnectionRevision != b.ConnectionRevision || s.Protocol != b.Protocol || op.SnapshotID != s.ID || op.ConnectionID != b.ConnectionID || op.ConnectionRevision != b.ConnectionRevision || op.Endpoint != b.Endpoint || op.CredentialRevision != b.CredentialRevision || op.Status != "succeeded" {
		return domain.MCPTool{}, domain.MCPToolBinding{}, store.ErrConflict
	}
	for _, bound := range b.Tools {
		if bound.Name != name {
			continue
		}
		for _, tool := range s.Tools {
			if tool.Name != name {
				continue
			}
			output := ""
			if len(tool.OutputSchema) > 0 && string(tool.OutputSchema) != "null" {
				output = mcpHash(tool.OutputSchema)
			}
			if tool.DefinitionDigest == bound.DefinitionDigest && mcpHash(tool.InputSchema) == bound.InputSchemaDigest && output == bound.OutputSchemaDigest {
				return tool, bound, nil
			}
		}
	}
	return domain.MCPTool{}, domain.MCPToolBinding{}, errors.New("tool is not exactly bound")
}

// Read source bytes, not a caller-supplied substitute. This also validates old
// sources without treating their epoch as present permission.
func (e *Engine) mcpSource(m domain.Mission, id string) (domain.MCPProposal, error) {
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return e.researchMCPSource(m, id)
	}
	p := domain.MCPProposal{MissionID: m.ID, SourceInvocationID: id}
	source, err := e.s.GetMCPProposalSource(m.ID, id)
	if err != nil {
		return p, err
	}
	v, err := e.s.GetInvocation(id)
	if err != nil {
		return p, err
	}
	g, err := e.s.GetReadGrant(source.ReadGrantID)
	if err != nil {
		return p, err
	}
	binding, err := domain.MCPBindingDigest(*m.TaskBindings, *m.MCPBindings)
	if err != nil {
		return p, err
	}
	if source.Purpose != "research_turn" || v.Status != "succeeded" || v.Result == nil || v.Kind != "model" || v.Role != domain.Researcher || v.MissionID != m.ID || source.ModelInvocationID != v.ID || source.Epoch != v.Epoch || source.SessionID != v.SessionID || source.ReadGrantID != v.GrantID || source.BindingDigest != v.BindingDigest || source.AuthorityDigest != v.AuthorityDigest || v.BindingDigest != binding || g.MissionID != m.ID || g.Epoch != v.Epoch || g.SessionID != v.SessionID || g.Role != domain.Researcher || g.RequiredState != "planning" || g.BindingDigest != binding || g.AuthorityDigest != v.AuthorityDigest {
		return p, errors.New("invalid MCP source authority")
	}
	if _, err = e.evidenceStore().Read(v.Input); err != nil {
		return p, errors.New("MCP source input integrity failed")
	}
	raw, err := e.evidenceStore().Read(*v.Result)
	if err != nil {
		return p, errors.New("MCP source result integrity failed")
	}
	var output struct {
		ToolID    string          `json:"tool_id"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err = decodeControlled(string(raw), &output); err != nil {
		return p, errors.New("invalid MCP source output")
	}
	tool, bound, err := e.mcpTool(m, output.ToolID)
	if err != nil {
		return p, err
	}
	arguments, err := mcp.ValidateArguments(tool, bound.Policy, output.Arguments)
	if err != nil {
		return p, err
	}
	p.SourceResult = *v.Result
	p.ToolID = output.ToolID
	p.Arguments = arguments
	p.Epoch = v.Epoch
	p.SessionID = v.SessionID
	p.ReadGrantID = g.ID
	p.BindingDigest = binding
	p.AuthorityDigest = v.AuthorityDigest
	return p, nil
}

func (e *Engine) mcpVerifiedProposal(m domain.Mission, id string) (domain.MCPProposal, error) {
	p, err := e.s.GetMCPProposal(m.ID, id)
	if err != nil {
		return p, err
	}
	source, err := e.mcpSource(m, p.SourceInvocationID)
	if err != nil {
		return p, err
	}
	if p.ToolID != source.ToolID || !bytes.Equal(p.Arguments, source.Arguments) || p.SourceResult != source.SourceResult || p.Epoch != source.Epoch || p.SessionID != source.SessionID || p.ReadGrantID != source.ReadGrantID || p.BindingDigest != source.BindingDigest || p.AuthorityDigest != source.AuthorityDigest {
		return p, errors.New("MCP proposal differs from sealed source")
	}
	return p, nil
}

func (e *Engine) mcpCredential(m domain.Mission) (string, error) {
	if m.MCPBindings.CredentialRef == "" {
		return "", nil
	}
	if e.options.MCPSecrets == nil {
		return "", errors.New("MCP credential unavailable")
	}
	credential, err := e.options.MCPSecrets.Get(m.MCPBindings.CredentialRef)
	if err != nil || credential == "" {
		return "", errors.New("MCP credential unavailable")
	}
	return credential, nil
}

// Inspect decoded strings/keys and primitive lexemes, including escaped JSON.
// This is a known-credential reflection guard, not a universal secret detector.
func mcpReflects(raw []byte, secret string) bool {
	if secret == "" {
		return false
	}
	if bytes.Contains(raw, []byte(secret)) {
		return true
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil {
		return true
	}
	var visit func(any) bool
	visit = func(v any) bool {
		switch x := v.(type) {
		case string:
			return strings.Contains(x, secret)
		case json.Number:
			return strings.Contains(string(x), secret)
		case bool:
			if x {
				return strings.Contains("true", secret)
			}
			return strings.Contains("false", secret)
		case nil:
			return strings.Contains("null", secret)
		case []any:
			for _, item := range x {
				if visit(item) {
					return true
				}
			}
		case map[string]any:
			for key, item := range x {
				if strings.Contains(key, secret) || visit(item) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
}
