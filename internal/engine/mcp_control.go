package engine

import (
	"context"
	"errors"
	"fmt"

	"foundry/internal/domain"
	"foundry/internal/mcp"
	"foundry/internal/store"
)

// All four methods are Go-only application services. No route or production
// creation path binds them; every active operation requires the explicit option.
func (e *Engine) mcpEnabled() error {
	if !e.options.EnableLocalMCP {
		return errors.New("local MCP service disabled")
	}
	if e.closed {
		return errors.New("engine closed")
	}
	return nil
}
func (e *Engine) mcpChecked(id string, revision int64) (domain.Mission, error) {
	if err := e.mcpEnabled(); err != nil {
		return domain.Mission{}, err
	}
	m, err := e.s.GetMission(id)
	if err != nil {
		return m, err
	}
	if m.Revision != revision || !isLocalMCP(m) {
		return m, store.ErrConflict
	}
	if e.active != nil {
		return m, errors.New("engine busy")
	}
	return m, e.mcpCurrent(m)
}

func (e *Engine) MaterializeMCPProposal(ctx context.Context, id string, revision int64, sourceID string) (domain.Mission, domain.MCPProposal, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.mcpChecked(id, revision)
	if err != nil {
		return m, domain.MCPProposal{}, err
	}
	if err = ctx.Err(); err != nil {
		return m, domain.MCPProposal{}, err
	}
	p, err := e.mcpSource(m, sourceID)
	if err != nil {
		return m, p, err
	}
	if p.Epoch != m.Epoch || !hasSeal(m, "context", p.AuthorityDigest) {
		return m, p, store.ErrConflict
	}
	credential, err := e.mcpCredential(m)
	if err != nil {
		return m, p, err
	}
	if mcpReflects(p.Arguments, credential) || mcpReflects([]byte(fmt.Sprintf("%q", p.ToolID)), credential) {
		return m, domain.MCPProposal{}, errors.New("MCP source credential reflection")
	}
	// A deterministic identity makes materialization crash-gap replay local and
	// stable; no caller controls the proposal identity or replacement arguments.
	p.ID = mcpHash([]byte("FOUNDRY MCP proposal v1\x00" + id + "\x00" + sourceID))
	return e.s.CreateMCPProposal(p, revision)
}

func (e *Engine) DecideMCPProposal(ctx context.Context, id string, revision int64, proposalID, digest, requestID string, approve bool) (domain.Mission, domain.MCPCallGrant, domain.MCPInvocation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var m domain.Mission
	var g domain.MCPCallGrant
	var v domain.MCPInvocation
	if err := e.mcpEnabled(); err != nil {
		return m, g, v, err
	}
	if e.active != nil {
		return m, g, v, errors.New("engine busy")
	}
	m, err := e.s.GetMission(id)
	if err != nil {
		return m, g, v, err
	}
	if err = e.mcpCurrent(m); err != nil {
		return m, g, v, err
	}
	if err = ctx.Err(); err != nil {
		return m, g, v, err
	}
	p, err := e.mcpVerifiedProposal(m, proposalID)
	if err != nil {
		return m, g, v, err
	}
	if p.Epoch != m.Epoch || !hasSeal(m, "context", p.AuthorityDigest) {
		return m, g, v, store.ErrConflict
	}
	credential, err := e.mcpCredential(m)
	if err != nil {
		return m, g, v, err
	}
	if mcpReflects(p.Arguments, credential) {
		return m, g, v, errors.New("MCP proposal credential reflection")
	}
	// Store performs exact stable replay before checking the current revision.
	return e.s.DecideMCPProposal(id, proposalID, digest, requestID, revision, approve)
}

func (e *Engine) DispatchMCPInvocation(ctx context.Context, id string, revision int64, invocationID string) (domain.MCPInvocation, error) {
	e.mu.Lock()
	m, err := e.mcpChecked(id, revision)
	if err != nil {
		e.mu.Unlock()
		return domain.MCPInvocation{}, err
	}
	v, err := e.s.GetMCPInvocation(id, invocationID)
	if err != nil {
		e.mu.Unlock()
		return v, err
	}
	p, err := e.mcpVerifiedProposal(m, v.ProposalID)
	if err != nil {
		e.mu.Unlock()
		return v, err
	}
	tool, bound, err := e.mcpTool(m, p.ToolID)
	if err != nil {
		e.mu.Unlock()
		return v, err
	}
	if err = ctx.Err(); err != nil {
		e.mu.Unlock()
		return v, err
	}
	v, err = e.s.ClaimMCPInvocation(id, invocationID, m.Epoch)
	if err != nil {
		e.mu.Unlock()
		return v, err
	}
	opCtx, cancel := context.WithCancel(ctx)
	op := &operation{id: id, epoch: m.Epoch, ctx: opCtx, cancel: cancel, done: make(chan struct{}), finished: make(chan struct{})}
	e.active = op
	credential, credentialErr := e.mcpCredential(m)
	connection, connectionErr := e.s.GetMCPConnection(m.MCPBindings.ConnectionID)
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		cancel()
		if e.active == op {
			e.active = nil
		}
		close(op.done)
		close(op.finished)
	}()
	return e.runMCPTransport(op, m, v, p, tool, bound, credential, credentialErr, connection, connectionErr)
}

// Shared bounded transport; ownership and claim belong to the caller. This
// avoids installing a second owner during an already-owned research segment.
func (e *Engine) runMCPTransport(op *operation, m domain.Mission, v domain.MCPInvocation, p domain.MCPProposal, tool domain.MCPTool, bound domain.MCPToolBinding, credential string, credentialErr error, connection domain.MCPConnection, connectionErr error) (domain.MCPInvocation, error) {
	id := m.ID
	var result mcp.CallResult
	callErr := credentialErr
	if callErr == nil {
		callErr = connectionErr
	}
	if callErr == nil && mcpReflects(p.Arguments, credential) {
		callErr = errors.New("credential reflection")
	}
	if callErr == nil {
		result, callErr = mcp.Call(op.ctx, connection, tool, bound.Policy, p.Arguments, credential, func() error {
			e.mu.Lock()
			defer e.mu.Unlock()
			current, err := e.s.GetMission(id)
			if err != nil {
				return err
			}
			if op.ctx.Err() != nil || current.Epoch != op.epoch || e.active != op {
				return context.Canceled
			}
			if err = e.mcpCurrent(current); err != nil {
				return err
			}
			if _, err = e.mcpVerifiedProposal(current, p.ID); err != nil {
				return err
			}
			_, err = e.s.MarkMCPCallStarted(id, v.ID, v.Epoch)
			return err
		})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	outcome := domain.MCPCallOutcome{Status: "failed", ErrorCode: "preflight_failed", Requests: result.Requests, RequestBytes: result.RequestBytes, ResponseBytes: result.ResponseBytes, DurationMillis: result.Duration.Milliseconds()}
	current, currentErr := e.s.GetMission(id)
	stale := currentErr != nil || op.ctx.Err() != nil || current.Epoch != op.epoch
	if !stale {
		stale = e.mcpCurrent(current) != nil
	}
	if !stale {
		_, checkErr := e.mcpVerifiedProposal(current, p.ID)
		stale = checkErr != nil
	}
	if stale {
		if result.Dispatched {
			outcome.Status = "outcome_unknown"
		}
		outcome.ErrorCode = "dispatch_interrupted"
	} else if callErr != nil {
		var protocolError *mcp.Error
		if errors.As(callErr, &protocolError) {
			outcome.ErrorCode = protocolError.Code
			if protocolError.Unknown && result.Dispatched {
				outcome.Status = "outcome_unknown"
			}
		}
	} else {
		outcome.Status = "succeeded"
		outcome.ErrorCode = ""
		if result.RemoteToolError {
			outcome.Status = "failed"
			outcome.ErrorCode = "tool_error"
		}
		envelope, sealErr := e.evidenceStore().Put(domain.EvidenceEnvelope{Kind: "mcp_result", InvocationID: v.ID, MissionID: id, Epoch: v.Epoch, Producer: "mcp." + p.ToolID, ContentType: "application/json", BindingDigest: v.BindingDigest}, result.Result)
		if sealErr == nil {
			_, sealErr = e.evidenceStore().Read(envelope)
		}
		if sealErr != nil {
			outcome.Status = "outcome_unknown"
			outcome.ErrorCode = "result_seal_failed"
		} else {
			outcome.Result = &envelope
		}
	}
	finished, finishErr := e.s.FinishMCPInvocation(id, v.ID, v.Epoch, outcome)
	if finishErr != nil {
		// An orphan blob never proves completion. Retry only a local unknown-state
		// commit; never retry protocol traffic. A persistent DB failure is surfaced.
		if result.Dispatched {
			outcome.Status = "outcome_unknown"
			outcome.Result = nil
			outcome.ErrorCode = "result_commit_failed"
			if unknown, err := e.s.FinishMCPInvocation(id, v.ID, v.Epoch, outcome); err == nil {
				return unknown, errors.New("MCP result commit failed; outcome unknown")
			}
		}
		return v, errors.New("MCP ledger finalization failed; local recovery required")
	}
	if finished.Status != "succeeded" {
		return finished, fmt.Errorf("MCP invocation ended: %s", finished.ErrorCode)
	}
	return finished, nil
}

// Pause fences idle waiting checkpoints as well as an owned active transport.
// The epoch changes before cancellation, so a late response cannot advance it.
func (e *Engine) PauseMCP(ctx context.Context, id string, revision int64) (domain.Mission, error) {
	if m, err := e.s.GetMission(id); err == nil && domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return e.Pause(ctx, id, revision)
	}
	e.mu.Lock()
	if err := e.mcpEnabled(); err != nil {
		e.mu.Unlock()
		return domain.Mission{}, err
	}
	m, err := e.s.GetMission(id)
	if err != nil {
		e.mu.Unlock()
		return m, err
	}
	if !isLocalMCP(m) || m.Revision != revision || m.State != "planning" {
		e.mu.Unlock()
		return m, store.ErrConflict
	}
	if err = e.s.CheckMCPLedgerVersion(id); err != nil {
		e.mu.Unlock()
		return m, err
	}
	op := e.active
	if op != nil && (op.id != id || op.pausing) {
		e.mu.Unlock()
		return m, errors.New("engine busy")
	}
	if err = ctx.Err(); err != nil {
		e.mu.Unlock()
		return m, err
	}
	state := "paused"
	if op != nil {
		state = "pausing"
	}
	m, err = e.s.UpdateMission(id, revision, "mcp.pausing", func(m *domain.Mission) error { m.Epoch++; m.State = state; m.Stage = "mcp_paused"; return nil })
	if err != nil || op == nil {
		e.mu.Unlock()
		return m, err
	}
	op.pausing = true
	op.cancel()
	e.mu.Unlock()
	<-op.finished
	e.mu.Lock()
	defer e.mu.Unlock()
	current, err := e.s.GetMission(id)
	if err != nil {
		return current, err
	}
	if current.Epoch != m.Epoch || current.State != "pausing" {
		return current, store.ErrConflict
	}
	return e.s.UpdateMission(id, current.Revision, "mcp.paused", func(m *domain.Mission) error { m.State = "paused"; return nil })
}
