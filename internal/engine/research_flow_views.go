package engine

import (
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/store"
)

type ResearchRecipient struct {
	Role     domain.Role `json:"role"`
	Name     string      `json:"name"`
	Protocol string      `json:"protocol"`
	Model    string      `json:"model"`
}
type ResearchMCPDecisionView struct {
	ResearchStatus
	ProposalID         string              `json:"proposal_id"`
	ProposalDigest     string              `json:"proposal_digest"`
	Revision           int64               `json:"revision"`
	Purpose            string              `json:"purpose"`
	Endpoint           string              `json:"endpoint"`
	Tool               string              `json:"tool"`
	Arguments          json.RawMessage     `json:"arguments"`
	Policy             json.RawMessage     `json:"policy"`
	SourceInvocationID string              `json:"source_invocation_id"`
	SourceDigest       string              `json:"source_digest"`
	TestOnly           bool                `json:"test_only"`
	UsedCalls          int                 `json:"used_calls"`
	RemainingCalls     int                 `json:"remaining_calls"`
	Budget             domain.MCPBudget    `json:"budget"`
	Recipients         []ResearchRecipient `json:"recipients"`
}
type ResearchMCPInvocationView struct {
	ID             string          `json:"id"`
	ProposalID     string          `json:"proposal_id"`
	Status         string          `json:"status"`
	Phase          string          `json:"phase"`
	ErrorCode      string          `json:"error_code,omitempty"`
	Tool           string          `json:"tool"`
	Arguments      json.RawMessage `json:"arguments"`
	ResultDigest   string          `json:"result_digest,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	Requests       int             `json:"requests"`
	ResponseBytes  int64           `json:"response_bytes"`
	DurationMillis int64           `json:"duration_millis"`
	CreatedAt      string          `json:"created_at"`
	FinishedAt     string          `json:"finished_at,omitempty"`
	TestOnly       bool            `json:"test_only"`
}

type ResearchMCPHistoryView struct {
	Invocations []ResearchMCPInvocationView `json:"invocations"`
	Decisions   []store.MCPDecision         `json:"decisions"`
}

// ResearchMCPHistory includes rejected and approved decisions across sessions.
// It verifies source evidence without consulting present execution authority.
func (e *Engine) ResearchMCPHistory(id string) (ResearchMCPHistoryView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	calls, err := e.researchMCPInvocations(id, false)
	if err != nil {
		return ResearchMCPHistoryView{}, err
	}
	decisions, err := e.s.ListMCPDecisions(id)
	if err != nil {
		return ResearchMCPHistoryView{}, err
	}
	m, err := e.s.GetMission(id)
	if err != nil {
		return ResearchMCPHistoryView{}, err
	}
	for _, d := range decisions {
		if _, err = e.mcpVerifiedProposal(m, d.ProposalID); err != nil {
			return ResearchMCPHistoryView{}, err
		}
	}
	return ResearchMCPHistoryView{Invocations: calls, Decisions: decisions}, nil
}

// History reads verify sealed content but do not require an enabled fixture.
// Neither complete grants nor raw model prompts enter these projections.
func (e *Engine) ResearchMCPDecision(id string) (*ResearchMCPDecisionView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.s.GetMission(id)
	if err != nil {
		return nil, err
	}
	if !domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return nil, store.ErrConflict
	}
	if err = e.verifyResearchHistory(m); err != nil {
		return nil, err
	}
	if m.State != "planning" || m.Stage != "awaiting_tool_approval" {
		return nil, nil
	}
	proposals, err := e.s.ListMCPProposals(id)
	if err != nil {
		return nil, err
	}
	for i := len(proposals) - 1; i >= 0; i-- {
		p := proposals[i]
		if p.Epoch != m.Epoch {
			continue
		}
		p, err = e.mcpVerifiedProposal(m, p.ID)
		if err != nil {
			return nil, err
		}
		_, turn, err := e.researchSource(m, p.SourceInvocationID)
		if err != nil {
			return nil, err
		}
		_, tool, err := e.mcpTool(m, p.ToolID)
		if err != nil {
			return nil, err
		}
		calls, err := e.s.ListMCPInvocations(id)
		if err != nil {
			return nil, err
		}
		generic, err := e.verifiedInvocations(id)
		if err != nil {
			return nil, err
		}
		used := len(calls)
		for _, v := range generic {
			if v.Kind == "repository" {
				used++
			}
		}
		view := &ResearchMCPDecisionView{ProposalID: p.ID, ProposalDigest: p.Digest, Revision: m.Revision, Purpose: turn.Reason, Endpoint: m.MCPBindings.Endpoint, Tool: p.ToolID, Arguments: p.Arguments, Policy: tool.Policy, SourceInvocationID: p.SourceInvocationID, SourceDigest: p.SourceResult.Digest, TestOnly: true, UsedCalls: used, RemainingCalls: 3 - used, Budget: m.MCPBindings.Budget, Recipients: []ResearchRecipient{}}
		view.ResearchStatus, err = e.researchStatus(m)
		if err != nil {
			return nil, err
		}
		for _, role := range []domain.Role{domain.Researcher, domain.Planner, domain.Main, domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
			profile := m.Profiles[role]
			view.Recipients = append(view.Recipients, ResearchRecipient{role, profile.Name, profile.Protocol, profile.Model})
		}
		return view, nil
	}
	return nil, errors.New("waiting proposal missing")
}
func (e *Engine) ResearchMCPDecisionReceipt(id, requestID string) (store.MCPDecision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, err := e.s.GetMCPDecision(id, requestID)
	if err != nil {
		return d, err
	}
	m, err := e.s.GetMission(id)
	if err != nil {
		return d, err
	}
	if !domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return d, store.ErrConflict
	}
	_, err = e.mcpVerifiedProposal(m, d.ProposalID)
	return d, err
}
func (e *Engine) researchMCPInvocations(id string, detail bool) ([]ResearchMCPInvocationView, error) {
	m, err := e.s.GetMission(id)
	if err != nil {
		return nil, err
	}
	if !domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return nil, store.ErrConflict
	}
	if err = e.verifyResearchHistory(m); err != nil {
		return nil, err
	}
	calls, err := e.s.ListMCPInvocations(id)
	if err != nil {
		return nil, err
	}
	out := []ResearchMCPInvocationView{}
	for _, v := range calls {
		p, err := e.mcpVerifiedProposal(m, v.ProposalID)
		if err != nil {
			return nil, err
		}
		view := ResearchMCPInvocationView{ID: v.ID, ProposalID: p.ID, Status: v.Status, Phase: v.Phase, ErrorCode: v.ErrorCode, Tool: p.ToolID, Arguments: p.Arguments, Requests: v.Requests, ResponseBytes: v.ResponseBytes, DurationMillis: v.DurationMillis, CreatedAt: v.CreatedAt, FinishedAt: v.FinishedAt, TestOnly: true}
		if v.Result != nil {
			raw, err := e.evidenceStore().Read(*v.Result)
			if err != nil {
				return nil, err
			}
			view.ResultDigest = v.Result.Digest
			if detail {
				view.Result = raw
			}
		}
		out = append(out, view)
	}
	return out, nil
}
func (e *Engine) ResearchMCPInvocations(id string) ([]ResearchMCPInvocationView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.researchMCPInvocations(id, false)
}
func (e *Engine) ResearchMCPInvocation(id, invocationID string) (ResearchMCPInvocationView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	all, err := e.researchMCPInvocations(id, true)
	if err != nil {
		return ResearchMCPInvocationView{}, err
	}
	for _, v := range all {
		if v.ID == invocationID {
			return v, nil
		}
	}
	return ResearchMCPInvocationView{}, store.ErrNotFound
}
