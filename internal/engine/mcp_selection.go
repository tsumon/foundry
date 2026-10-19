package engine

import (
	"errors"
	"foundry/internal/domain"
	"foundry/internal/store"
	"reflect"
)

type MCPSelectedTool struct {
	Name             string `json:"name"`
	DefinitionDigest string `json:"definition_digest"`
	PolicyDigest     string `json:"policy_digest"`
}

// MCPSelection is identifiers/revisions/digests only, never policy or endpoint input.
type MCPSelection struct {
	ConnectionID       string            `json:"connection_id"`
	ConnectionRevision int64             `json:"connection_revision"`
	SnapshotID         string            `json:"snapshot_id"`
	SnapshotDigest     string            `json:"snapshot_digest"`
	Tools              []MCPSelectedTool `json:"tools"`
}
type LocalMCPSelectionView struct {
	Enabled   bool             `json:"enabled"`
	Eligible  bool             `json:"eligible"`
	Reason    string           `json:"reason,omitempty"`
	TestOnly  bool             `json:"test_only"`
	Endpoint  string           `json:"endpoint,omitempty"`
	Selection *MCPSelection    `json:"selection,omitempty"`
	Budget    domain.MCPBudget `json:"budget"`
}

func selectionOf(b domain.MCPTaskBindings) *MCPSelection {
	s := &MCPSelection{ConnectionID: b.ConnectionID, ConnectionRevision: b.ConnectionRevision, SnapshotID: b.SnapshotID, SnapshotDigest: b.SnapshotDigest, Tools: []MCPSelectedTool{}}
	for _, t := range b.Tools {
		s.Tools = append(s.Tools, MCPSelectedTool{t.Name, t.DefinitionDigest, mcpHash(t.Policy)})
	}
	return s
}
func (e *Engine) LocalMCPTestSelection() (LocalMCPSelectionView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v := LocalMCPSelectionView{TestOnly: true, Budget: domain.FixedMCPBudget()}
	d := e.options.LocalMCPTest
	v.Enabled = d != nil
	if d == nil || d.Alive == nil || !d.Alive() {
		v.Reason = "controller-owned local test service disabled"
		return v, nil
	}
	if e.options.DisableControlledReadCreation {
		v.Reason = "controlled read creation disabled"
		return v, nil
	}
	if err := e.localMCPCurrent(domain.Mission{MCPBindings: &d.Binding}); err != nil {
		if errors.Is(err, store.ErrConflict) {
			v.Reason = "local test configuration changed"
			return v, nil
		}
		return v, err
	}
	v.Eligible = true
	v.Endpoint = d.Binding.Endpoint
	v.Selection = selectionOf(d.Binding)
	return v, nil
}
func (e *Engine) localMCPCurrent(m domain.Mission) error {
	d := e.options.LocalMCPTest
	if e.closed || d == nil || d.Alive == nil || !d.Alive() || m.MCPBindings == nil || !reflect.DeepEqual(d.Binding, *m.MCPBindings) {
		return errors.New("controller-owned local test service unavailable or changed")
	}
	c, err := e.s.GetMCPConnection(d.Binding.ConnectionID)
	if err != nil {
		return err
	}
	b := d.Binding
	if !c.Enabled || c.Revision != b.ConnectionRevision || c.Endpoint != b.Endpoint || c.Protocol != b.Protocol || c.CredentialRef != b.CredentialRef || c.CredentialRevision != b.CredentialRevision {
		return store.ErrConflict
	}
	probe := m
	probe.WorkspaceMode = domain.WorkspaceModeControlledMCPReadV2
	probe.ToolMode = domain.ToolModeControlledMCPReadV2
	if probe.TaskBindings == nil {
		probe.TaskBindings = &domain.TaskBindings{}
	}
	for _, t := range b.Tools {
		if _, _, err = e.mcpTool(probe, t.Name); err != nil {
			return err
		}
	}
	return nil
}
func (e *Engine) resolveMCPSelection(r CreateRequest) (*domain.MCPTaskBindings, error) {
	if !domain.IsResearchFlowV2(r.WorkspaceMode, r.ToolMode) {
		if r.MCPSelection != nil || r.ToolMode == domain.ToolModeControlledMCPReadV2 || r.WorkspaceMode == domain.WorkspaceModeControlledMCPReadV2 {
			return nil, errors.New("exact v2 research mode pair required")
		}
		return nil, nil
	}
	d := e.options.LocalMCPTest
	if d == nil || r.MCPSelection == nil {
		return nil, errors.New("explicit owned local test selection required")
	}
	if !reflect.DeepEqual(r.MCPSelection, selectionOf(d.Binding)) {
		return nil, store.ErrConflict
	}
	if err := e.localMCPCurrent(domain.Mission{MCPBindings: &d.Binding}); err != nil {
		return nil, err
	}
	b := cloneMCPBinding(d.Binding)
	return &b, nil
}
