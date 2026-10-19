package engine

import (
	"errors"
	"foundry/internal/domain"
	"foundry/internal/mcp"
	"foundry/internal/store"
	"github.com/mattn/go-sqlite3"
)

// Called under the engine mutex. No credentials, transport, model, Git or
// sandbox are involved in this local-only audit.
func (e *Engine) recoverLocalMCP(id string) error {
	if err := e.s.CheckMCPLedgerVersion(id); err != nil {
		if errors.Is(err, store.ErrMCPFutureVersion) {
			return nil
		}
		return err
	}
	if err := e.s.RecoverMCPInvocations(id); err != nil {
		return err
	}
	m, err := e.s.GetMission(id)
	if err != nil {
		return err
	}
	if m.State == "blocked" && m.Stage == "mcp_corrupt" {
		return nil
	}
	problem := e.verifyMCPLocalHistory(m)
	if errors.Is(problem, store.ErrMCPFutureVersion) {
		return nil
	}
	if problem == nil && m.State == "planning" {
		problem = e.mcpCurrent(m)
	}
	if problem == nil {
		return nil
	}
	var databaseError sqlite3.Error
	if errors.As(problem, &databaseError) {
		return problem
	}
	// The Engine's additional snapshot/profile reads can also fail at the SQL
	// layer. Only non-infrastructure integrity failures isolate this owner.
	_, err = e.s.UpdateMission(id, m.Revision, "mcp.evidence_integrity_failed", func(m *domain.Mission) error {
		m.Epoch++
		m.State = "blocked"
		m.Stage = "mcp_corrupt"
		m.Error = "MCP local evidence or authority integrity failed"
		return nil
	})
	return err
}

func (e *Engine) verifyMCPLocalHistory(m domain.Mission) error {
	if !isLocalMCP(m) {
		return errors.New("invalid MCP mission")
	}
	sources, err := e.s.ListMCPProposalSources(m.ID)
	if err != nil {
		return err
	}
	proposals, err := e.s.ListMCPProposals(m.ID)
	if err != nil {
		return err
	}
	grants, err := e.s.ListMCPCallGrants(m.ID)
	if err != nil {
		return err
	}
	calls, err := e.s.ListMCPInvocations(m.ID)
	if err != nil {
		return err
	}
	if _, err = e.verifiedInvocations(m.ID); err != nil {
		return err
	}
	for _, source := range sources {
		v, err := e.s.GetInvocation(source.ModelInvocationID)
		if err != nil {
			return err
		}
		if v.Status == "succeeded" {
			if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
				if _, _, err = e.researchSourceEvidence(m, v.ID); err != nil {
					return err
				}
				continue
			}
			p, err := e.mcpSource(m, v.ID)
			if err != nil {
				return err
			}
			if p.Epoch == m.Epoch && m.State == "planning" && !hasSeal(m, "context", p.AuthorityDigest) {
				return errors.New("source context changed")
			}
		}
	}
	byProposal := map[string]domain.MCPProposal{}
	for _, p := range proposals {
		verified, err := e.mcpVerifiedProposal(m, p.ID)
		if err != nil {
			return err
		}
		byProposal[p.ID] = verified
	}
	byGrant := map[string]domain.MCPCallGrant{}
	for _, g := range grants {
		p, ok := byProposal[g.ProposalID]
		if !ok || g.MissionID != m.ID || g.ProposalDigest != p.Digest || g.Epoch != p.Epoch || g.SessionID != p.SessionID || g.BindingDigest != p.BindingDigest || g.AuthorityDigest != p.AuthorityDigest {
			return errors.New("MCP grant history mismatch")
		}
		byGrant[g.ID] = g
	}
	for _, v := range calls {
		g, ok := byGrant[v.GrantID]
		if !ok || v.ProposalID != g.ProposalID || v.Epoch != g.Epoch || v.SessionID != g.SessionID || v.BindingDigest != g.BindingDigest || v.AuthorityDigest != g.AuthorityDigest {
			return errors.New("MCP invocation history mismatch")
		}
		if v.Result != nil {
			raw, err := e.evidenceStore().Read(*v.Result)
			if err != nil {
				return err
			}
			p := byProposal[v.ProposalID]
			tool, _, err := e.mcpTool(m, p.ToolID)
			if err != nil {
				return err
			}
			if _, err = mcp.ValidateToolResult(tool, raw); err != nil {
				return err
			}
		}
	}
	return nil
}
