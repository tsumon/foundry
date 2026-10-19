package engine

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/mcp"
	"foundry/internal/store"
	"path/filepath"
)

// researchFlow runs durable segments. A tool proposal returns normally and
// releases the single owner; no goroutine waits for approval.
func (e *Engine) researchFlow(op *operation, reserved domain.Invocation) error {
	for {
		if reserved.ID == "" {
			e.mu.Lock()
			m, _, _, err := e.controlledAuthority(op, domain.Researcher)
			if err == nil {
				reserved, err = e.reserveResearch(m, nil, "", "")
			}
			e.mu.Unlock()
			if err != nil {
				return err
			}
		}
		e.mu.Lock()
		m, _, _, err := e.controlledAuthority(op, reserved.Role)
		if err == nil {
			reserved, err = e.s.ClaimInvocation(reserved.ID, reserved.Epoch)
		}
		var input controlledInput
		if err == nil {
			var raw []byte
			raw, err = e.evidenceStore().Read(reserved.Input)
			if err == nil {
				err = decodeControlled(string(raw), &input)
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		v, _, err := e.completeControlledModel(op, reserved, input)
		if err != nil {
			return err
		}
		e.mu.Lock()
		m, _, _, err = e.controlledAuthority(op, v.Role)
		if err == nil && v.Role == domain.Planner {
			err = e.applyResearchPlan(m, v)
		}
		var app domain.ResearchTurnApplication
		if err == nil && v.Role == domain.Researcher {
			m, app, err = e.applyResearchSource(m, v.ID)
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if v.Role == domain.Planner || app.Kind == "mcp" {
			return nil
		}
		var observation *domain.ResearchObservationRef
		final := ""
		if app.Kind == "repository" {
			call, err := e.s.GetInvocation(app.TargetID)
			if err != nil {
				return err
			}
			_, turn, err := e.researchSource(m, v.ID)
			if err != nil {
				return err
			}
			call, err = e.runReservedRepository(op, call, researchProposal{Tool: turn.Tool, Arguments: turn.Arguments})
			if err != nil {
				return err
			}
			observation = &domain.ResearchObservationRef{Kind: "repository", InvocationID: call.ID, Result: *call.Result}
		} else {
			final = v.ID
		}
		e.mu.Lock()
		m, _, _, err = e.controlledAuthority(op, domain.Researcher)
		if err == nil {
			reserved, err = e.reserveResearch(m, observation, final, "")
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
}

// Caller holds mu. This is the only producer of new Researcher/Planner intents.
func (e *Engine) reserveResearch(m domain.Mission, observation *domain.ResearchObservationRef, final, intent string) (domain.Invocation, error) {
	role := domain.Researcher
	if final != "" {
		role = domain.Planner
	}
	if err := e.preflightModelRoles(context.Background(), m, role); err != nil {
		return domain.Invocation{}, err
	}
	binding, err := controlledBindingDigest(m)
	if err != nil {
		return domain.Invocation{}, err
	}
	auth, err := authority(m, filepath.Join(e.dir(m.ID), "base"), false)
	if err != nil {
		return domain.Invocation{}, err
	}
	v, err := e.invocationIntent(m, domain.Researcher, "model", "model.complete", binding, auth)
	if err != nil {
		return v, err
	}
	session := v.SessionID
	key := session + ":initial"
	if observation != nil {
		key = observation.Kind + ":" + observation.InvocationID
	}
	if final != "" {
		key = "planner:" + final
	}
	v.ID = researchID(m.ID, "model", key)
	if intent != "" {
		v.ID = researchID(m.ID, "continue", intent)
	}
	v.Role = role
	if role == domain.Planner {
		v.GrantID = ""
	}
	observations, _, _, err := e.researchObservations(m, session, observation)
	if err != nil {
		return v, err
	}
	if role == domain.Planner {
		observations = []any{}
	} // Only the validated derived brief crosses this role boundary.
	sources, err := e.s.ListMCPProposalSources(m.ID)
	if err != nil {
		return v, err
	}
	turns := 0
	for _, s := range sources {
		if s.SessionID == session && s.Epoch == m.Epoch {
			turns++
		}
	}
	input, err := e.controlledModelInput(m, role, map[string]any{"observations": observations, "final_required": turns == 3, "continue_intent_id": intent, "continue_revision": m.Revision})
	if err != nil {
		return v, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return v, err
	}
	v.Input, err = e.seal(v, "model_input", raw, "application/json", false)
	if err != nil {
		return v, err
	}
	if final != "" {
		_, v, err = e.s.BeginResearchPlanner(v, final, m.Revision)
	} else {
		_, v, err = e.s.BeginResearchTurn(v, observation, m.Revision)
	}
	return v, err
}

func (e *Engine) applyResearchSource(m domain.Mission, id string) (domain.Mission, domain.ResearchTurnApplication, error) {
	v, p, err := e.researchSource(m, id)
	if err != nil {
		return m, domain.ResearchTurnApplication{}, err
	}
	if v.Epoch != m.Epoch || !hasSeal(m, "context", v.AuthorityDigest) {
		return m, domain.ResearchTurnApplication{}, store.ErrConflict
	}
	a := domain.ResearchTurnApplication{MissionID: m.ID, Epoch: v.Epoch, SessionID: v.SessionID, SourceInvocationID: v.ID, SourceResult: *v.Result, Kind: p.Kind}
	switch p.Kind {
	case "mcp":
		proposal, err := e.researchMCPSource(m, id)
		if err != nil {
			return m, a, err
		}
		m, _, err = e.s.CreateMCPProposal(proposal, m.Revision)
		if err != nil {
			return m, a, err
		}
		a, err = e.s.GetResearchTurnApplication(m.ID, id)
		return m, a, err
	case "repository":
		if err = validateToolProposal(researchProposal{Tool: p.Tool, Arguments: p.Arguments}, m.ContextManifest); err != nil {
			return m, a, err
		}
		target := domain.Invocation{ID: researchID(m.ID, "repository", id), MissionID: m.ID, Epoch: v.Epoch, SessionID: v.SessionID, Role: domain.Researcher, Kind: "repository", Name: p.Tool, GrantID: v.GrantID, BindingDigest: v.BindingDigest, AuthorityDigest: v.AuthorityDigest}
		target.Input, err = e.seal(target, "model_input", p.Arguments, "application/json", false)
		if err != nil {
			return m, a, err
		}
		a.TargetID = target.ID
		return e.s.ApplyResearchTurn(a, &target, m.Revision)
	case "final":
		if err = validateResearchV2(p.Research, m.ContextManifest); err != nil {
			return m, a, err
		}
		_, paths, refs, err := e.researchObservations(m, v.SessionID, nil)
		if err != nil {
			return m, a, err
		}
		for _, f := range p.Research.Findings {
			for _, path := range f.Paths {
				if !paths[path] {
					return m, a, errors.New("unobserved repository citation")
				}
			}
			for _, ref := range f.Sources {
				if ref.Kind != "mcp" || refs[ref.InvocationID] == "" || refs[ref.InvocationID] != ref.Digest {
					return m, a, errors.New("unobserved successful MCP citation")
				}
			}
		}
		a.Research = &p.Research
		return e.s.ApplyResearchTurn(a, nil, m.Revision)
	}
	return m, a, errors.New("invalid research turn")
}

func (e *Engine) researchObservations(m domain.Mission, session string, additional *domain.ResearchObservationRef) ([]any, map[string]bool, map[string]string, error) {
	consumptions, err := e.s.ListResearchConsumptions(m.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	observations := []domain.ResearchObservationRef{}
	seen := map[string]bool{}
	for _, c := range consumptions {
		if c.SessionID == session && c.Epoch == m.Epoch {
			observations = append(observations, c.Observation)
			seen[c.Observation.InvocationID] = true
		}
	}
	if additional != nil && !seen[additional.InvocationID] {
		observations = append(observations, *additional)
	}
	out := []any{}
	paths := map[string]bool{}
	refs := map[string]string{}
	for _, o := range observations {
		raw, err := e.evidenceStore().Read(o.Result)
		if err != nil {
			return nil, nil, nil, err
		}
		if o.Kind == "repository" {
			v, err := e.s.GetInvocation(o.InvocationID)
			if err != nil {
				return nil, nil, nil, err
			}
			if v.MissionID != m.ID || v.Epoch != m.Epoch || v.SessionID != session || v.Status != "succeeded" || v.Result == nil || *v.Result != o.Result {
				return nil, nil, nil, errors.New("repository observation mismatch")
			}
			grant, err := e.s.GetReadGrant(v.GrantID)
			if err != nil {
				return nil, nil, nil, err
			}
			if grant.MissionID != m.ID || grant.Epoch != v.Epoch || grant.SessionID != session || grant.Role != domain.Researcher || grant.BindingDigest != v.BindingDigest || grant.AuthorityDigest != v.AuthorityDigest {
				return nil, nil, nil, errors.New("historical repository grant mismatch")
			}
			observed, err := repositoryCitationPaths(v.Name, raw)
			if err != nil {
				return nil, nil, nil, err
			}
			for p := range observed {
				paths[p] = true
			}
		} else {
			v, err := e.s.GetMCPInvocation(m.ID, o.InvocationID)
			if err != nil {
				return nil, nil, nil, err
			}
			if v.Epoch != m.Epoch || v.SessionID != session || v.Result == nil || *v.Result != o.Result || (v.Status != "succeeded" && !(v.Status == "failed" && v.ErrorCode == "tool_error")) {
				return nil, nil, nil, errors.New("MCP observation mismatch")
			}
			p, err := e.mcpVerifiedProposal(m, v.ProposalID)
			if err != nil {
				return nil, nil, nil, err
			}
			tool, _, err := e.mcpTool(m, p.ToolID)
			if err != nil {
				return nil, nil, nil, err
			}
			result, err := mcp.ValidateToolResult(tool, raw)
			if err != nil {
				return nil, nil, nil, err
			}
			_ = result
			if v.Status == "succeeded" {
				refs[v.ID] = o.Result.Digest
			}
		}
		out = append(out, map[string]any{"kind": o.Kind, "invocation_id": o.InvocationID, "digest": o.Result.Digest, "body": json.RawMessage(raw)})
	}
	return out, paths, refs, nil
}

func (e *Engine) applyResearchPlan(m domain.Mission, v domain.Invocation) error {
	if v.Result == nil || v.Status != "succeeded" {
		return store.ErrConflict
	}
	raw, err := e.evidenceStore().Read(*v.Result)
	if err != nil {
		return err
	}
	var output planOutput
	if err = decodeControlled(string(raw), &output); err != nil {
		return err
	}
	plan := output.pendingPlan()
	if err = validateExecutablePlan(plan, m.Spec); err != nil {
		return err
	}
	plan.Revision = m.Revision + 1
	m.Plan = &plan
	digest, err := authority(m, filepath.Join(e.dir(m.ID), "base"), true)
	if err != nil {
		return err
	}
	_, _, err = e.s.ApplyResearchPlan(domain.ResearchPlanApplication{MissionID: m.ID, Epoch: v.Epoch, SessionID: v.SessionID, PlannerInvocationID: v.ID, PlannerResult: *v.Result, Plan: plan, Proposal: domain.Approval{Kind: "plan_proposal", Digest: digest, Revision: plan.Revision, PlanRevision: plan.Revision, CreatedAt: now()}}, m.Revision)
	return err
}
