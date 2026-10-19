package engine

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/store"
)

var errNoResearchCheckpoint = errors.New("no unconsumed verified checkpoint")

func (e *Engine) DecideResearchMCP(ctx context.Context, id string, revision int64, proposalID, digest, requestID string, approve bool) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, replay, err := e.researchMCPDecisionReceipt(id, revision, proposalID, digest, requestID, approve)
	if err != nil {
		return m, err
	}
	if replay {
		return m, nil
	}
	if op := e.active; op != nil {
		if op.id != id || op.epoch != m.Epoch || op.pausing || op.done == nil || m.State != "planning" || m.Stage != "awaiting_tool_approval" {
			return m, errors.New("engine busy")
		}
		e.mu.Unlock()
		select {
		case <-op.done:
		case <-ctx.Done():
			e.mu.Lock()
			return m, ctx.Err()
		}
		e.mu.Lock()
		m, replay, err = e.researchMCPDecisionReceipt(id, revision, proposalID, digest, requestID, approve)
		if err != nil || replay {
			return m, err
		}
		if e.active != nil {
			return m, errors.New("engine busy")
		}
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	if m.Revision != revision || m.State != "planning" || m.Stage != "awaiting_tool_approval" {
		return m, store.ErrConflict
	}
	if err = e.mcpCurrent(m); err != nil {
		return m, err
	}
	p, err := e.mcpVerifiedProposal(m, proposalID)
	if err != nil {
		return m, err
	}
	if p.Epoch != m.Epoch || !hasSeal(m, "context", p.AuthorityDigest) {
		return m, store.ErrConflict
	}
	var v domain.MCPInvocation
	m, _, v, err = e.s.DecideMCPProposal(id, proposalID, digest, requestID, revision, approve)
	if err != nil || !approve {
		return m, err
	}
	e.start(m, func(op *operation) error {
		call, err := e.dispatchResearchMCP(op, v.ID)
		if err != nil && !(call.Status == "failed" && call.ErrorCode == "tool_error" && call.Result != nil) {
			return err
		}
		if call.Result == nil {
			return errors.New("verified MCP observation missing")
		}
		e.mu.Lock()
		current, _, _, err := e.controlledAuthority(op, domain.Researcher)
		var next domain.Invocation
		if err == nil {
			next, err = e.reserveResearch(current, &domain.ResearchObservationRef{Kind: "mcp", InvocationID: call.ID, Result: *call.Result}, "", "")
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		return e.researchFlow(op, next)
	})
	return m, nil
}

func (e *Engine) researchMCPDecisionReceipt(id string, revision int64, proposalID, digest, requestID string, approve bool) (domain.Mission, bool, error) {
	m, err := e.s.GetMission(id)
	if err != nil {
		return m, false, err
	}
	if !domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return m, false, store.ErrConflict
	}
	prior, err := e.s.GetMCPDecision(id, requestID)
	if err == nil {
		if prior.ProposalID != proposalID || prior.ProposalDigest != digest || prior.ExpectedRevision != revision || prior.Approved != approve {
			return m, false, store.ErrConflict
		}
		return m, true, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return m, false, err
	}
	return m, false, nil
}

func (e *Engine) dispatchResearchMCP(op *operation, id string) (domain.MCPInvocation, error) {
	e.mu.Lock()
	m, _, _, err := e.controlledAuthority(op, domain.Researcher)
	var v domain.MCPInvocation
	var p domain.MCPProposal
	var tool domain.MCPTool
	var bound domain.MCPToolBinding
	if err == nil {
		v, err = e.s.GetMCPInvocation(m.ID, id)
	}
	if err == nil {
		p, err = e.mcpVerifiedProposal(m, v.ProposalID)
	}
	if err == nil {
		tool, bound, err = e.mcpTool(m, p.ToolID)
	}
	if err == nil {
		v, err = e.s.ClaimMCPInvocation(m.ID, id, op.epoch)
	}
	if err != nil {
		e.mu.Unlock()
		return v, err
	}
	credential, credentialErr := e.mcpCredential(m)
	connection, connectionErr := e.s.GetMCPConnection(m.MCPBindings.ConnectionID)
	e.mu.Unlock()
	return e.runMCPTransport(op, m, v, p, tool, bound, credential, credentialErr, connection, connectionErr)
}

// Continue reserves before returning or releasing mu. A historical intent is
// only a receipt, never permission to claim a reserved/dispatched call again.
func (e *Engine) ContinueResearch(ctx context.Context, id string, revision int64, intentID string) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.s.GetMission(id)
	if err != nil {
		return m, err
	}
	if !domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) || boundedPlanText("intent", intentID, 256) != nil {
		return m, store.ErrConflict
	}
	existing, err := e.s.GetInvocation(researchID(id, "continue", intentID))
	if err == nil {
		if existing.MissionID != id {
			return m, store.ErrConflict
		}
		raw, err := e.evidenceStore().Read(existing.Input)
		if err != nil {
			return m, err
		}
		var input controlledInput
		if err = decodeControlled(string(raw), &input); err != nil {
			return m, err
		}
		var payload struct {
			Extra struct {
				Intent   string `json:"continue_intent_id"`
				Revision int64  `json:"continue_revision"`
			} `json:"extra"`
		}
		if json.Unmarshal([]byte(input.Prompt), &payload) != nil || payload.Extra.Intent != intentID || payload.Extra.Revision != revision {
			return m, store.ErrConflict
		}
		return m, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return m, err
	}
	if m.Revision != revision || m.State != "planning" {
		return m, store.ErrConflict
	}
	if e.active != nil {
		return m, errors.New("engine busy")
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	if err = e.mcpCurrent(m); err != nil {
		return m, err
	}
	if err = e.verifyResearchHistory(m); err != nil {
		return m, err
	}
	observation, final, err := e.researchResumable(m)
	if err != nil {
		return m, err
	}
	next, err := e.reserveResearch(m, observation, final, intentID)
	if err != nil {
		return m, err
	}
	m, err = e.s.GetMission(id)
	if err != nil {
		return m, err
	}
	e.start(m, func(op *operation) error { return e.researchFlow(op, next) })
	return m, nil
}

func (e *Engine) researchResumable(m domain.Mission) (*domain.ResearchObservationRef, string, error) {
	apps, err := e.s.ListResearchTurnApplications(m.ID)
	if err != nil {
		return nil, "", err
	}
	consumed, err := e.s.ListResearchConsumptions(m.ID)
	if err != nil {
		return nil, "", err
	}
	used := map[string]bool{}
	for _, c := range consumed {
		used[c.Observation.InvocationID] = true
	}
	for _, a := range apps {
		if a.Epoch != m.Epoch {
			continue
		}
		if a.Kind == "final" {
			if _, err := e.s.GetResearchPlannerCheckpoint(m.ID, a.SourceInvocationID); errors.Is(err, store.ErrNotFound) {
				return nil, a.SourceInvocationID, nil
			} else if err != nil {
				return nil, "", err
			}
			continue
		}
		if a.Kind == "repository" {
			v, err := e.s.GetInvocation(a.TargetID)
			if err != nil {
				return nil, "", err
			}
			if !used[v.ID] && v.Status == "succeeded" && v.Result != nil {
				return &domain.ResearchObservationRef{Kind: "repository", InvocationID: v.ID, Result: *v.Result}, "", nil
			}
		}
		if a.Kind == "mcp" {
			calls, err := e.s.ListMCPInvocations(m.ID)
			if err != nil {
				return nil, "", err
			}
			for _, v := range calls {
				if v.ProposalID == a.TargetID && !used[v.ID] && v.Result != nil && (v.Status == "succeeded" || (v.Status == "failed" && v.ErrorCode == "tool_error")) {
					return &domain.ResearchObservationRef{Kind: "mcp", InvocationID: v.ID, Result: *v.Result}, "", nil
				}
			}
		}
	}
	return nil, "", errNoResearchCheckpoint
}
