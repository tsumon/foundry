package engine

import (
	"encoding/json"
	"errors"
	"foundry/internal/domain"
)

func controlledBindingDigest(m domain.Mission) (string, error) {
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		if m.TaskBindings == nil || m.MCPBindings == nil {
			return "", errors.New("missing research bindings")
		}
		return domain.MCPBindingDigest(*m.TaskBindings, *m.MCPBindings)
	}
	return domain.TaskBindingDigest(*m.TaskBindings)
}

// Keep ABv1 bytes and the ordinary authority unchanged. Reuse the existing
// repository/spec/plan canonicalization under a distinct v2 domain separator.
func researchAuthority(m domain.Mission, base string, execute bool) (string, error) {
	if !domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) || m.TaskBindings == nil || m.MCPBindings == nil {
		return "", errors.New("research v2 bindings required")
	}
	binding, err := controlledBindingDigest(m)
	if err != nil {
		return "", err
	}
	copy := m
	copy.ToolMode = domain.ToolModeControlledReadV1
	copy.WorkspaceMode = domain.WorkspaceModeControlledReadV1
	ordinary, err := legacyAuthority(copy, base, execute)
	if err != nil {
		return "", err
	}
	kind := "context"
	if execute {
		kind = "execute"
	}
	return mcpHash([]byte("FOUNDRY research " + kind + " authority v2\x00" + binding + "\x00" + ordinary)), nil
}
func cloneMCPBinding(b domain.MCPTaskBindings) domain.MCPTaskBindings {
	raw, _ := json.Marshal(b)
	var clone domain.MCPTaskBindings
	_ = json.Unmarshal(raw, &clone)
	return clone
}
