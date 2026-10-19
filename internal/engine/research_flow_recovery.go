package engine

import (
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/store"
	"github.com/mattn/go-sqlite3"
	"reflect"
)

func (e *Engine) verifyResearchHistory(m domain.Mission) error {
	if err := e.s.CheckMCPLedgerVersion(m.ID); err != nil {
		return err
	}
	if err := e.checkSnapshot(m); err != nil {
		return err
	}
	if err := e.verifyMCPLocalHistory(m); err != nil {
		return err
	}
	applications, err := e.s.ListResearchTurnApplications(m.ID)
	if err != nil {
		return err
	}
	for _, a := range applications {
		v, p, err := e.researchSource(m, a.SourceInvocationID)
		if err != nil {
			return err
		}
		if p.Kind != a.Kind {
			return errors.New("research application kind differs from sealed source")
		}
		switch p.Kind {
		case "repository":
			if err = validateToolProposal(researchProposal{Tool: p.Tool, Arguments: p.Arguments}, m.ContextManifest); err != nil {
				return err
			}
			target, err := e.s.GetInvocation(a.TargetID)
			if err != nil {
				return err
			}
			raw, err := e.evidenceStore().Read(target.Input)
			if err != nil {
				return err
			}
			if target.Name != p.Tool || string(raw) != string(p.Arguments) {
				return errors.New("repository target differs from source")
			}
		case "final":
			if !reflect.DeepEqual(a.Research, &p.Research) {
				return errors.New("final research differs from source")
			}
			if err = validateResearchV2(p.Research, m.ContextManifest); err != nil {
				return err
			}
			// Historical sessions retain their own epoch for evidence checks.
			historical := m
			historical.Epoch = v.Epoch
			_, paths, refs, err := e.researchObservations(historical, v.SessionID, nil)
			if err != nil {
				return err
			}
			for _, f := range p.Research.Findings {
				for _, path := range f.Paths {
					if !paths[path] {
						return errors.New("unobserved historical path")
					}
				}
				for _, ref := range f.Sources {
					if ref.Kind != "mcp" || refs[ref.InvocationID] == "" || refs[ref.InvocationID] != ref.Digest {
						return errors.New("invalid historical MCP citation")
					}
				}
			}
		}
	}
	consumptions, err := e.s.ListResearchConsumptions(m.ID)
	if err != nil {
		return err
	}
	for _, c := range consumptions {
		if _, err = e.evidenceStore().Read(c.Observation.Result); err != nil {
			return err
		}
	}
	if _, err = e.s.ListResearchPlannerCheckpoints(m.ID); err != nil {
		return err
	}
	plans, err := e.s.ListResearchPlanApplications(m.ID)
	if err != nil {
		return err
	}
	for _, a := range plans {
		raw, err := e.evidenceStore().Read(a.PlannerResult)
		if err != nil {
			return err
		}
		var p planOutput
		if err = decodeControlled(string(raw), &p); err != nil {
			return err
		}
		plan := p.pendingPlan()
		plan.Revision = a.Plan.Revision
		left, _ := json.Marshal(plan)
		right, _ := json.Marshal(a.Plan)
		if string(left) != string(right) {
			return errors.New("plan application differs from source")
		}
		if err = validateExecutablePlan(plan, m.Spec); err != nil {
			return err
		}
	}
	return nil
}

// Local reconciliation can apply already sealed output, never execute a model,
// MCP, repository tool or sandbox. New reservations created by reconciliation
// are interrupted in the second Store pass, not implicitly dispatched.
func (e *Engine) recoverResearchFlow(id string) error {
	if err := e.s.CheckMCPLedgerVersion(id); err != nil {
		if errors.Is(err, store.ErrMCPFutureVersion) {
			return nil
		}
		return err
	}
	if err := e.s.RecoverResearchFlow(id); err != nil {
		return err
	}
	m, err := e.s.GetMission(id)
	if err != nil {
		return err
	}
	if m.Stage == "mcp_corrupt" {
		return nil
	}
	problem := e.verifyResearchHistory(m)
	if problem == nil && m.State == "planning" {
		problem = e.mcpCurrent(m)
	}
	if problem == nil && m.State == "planning" {
		sources, err := e.s.ListMCPProposalSources(id)
		if err != nil {
			return err
		}
		for _, source := range sources {
			if source.Epoch != m.Epoch {
				continue
			}
			v, err := e.s.GetInvocation(source.ModelInvocationID)
			if err != nil {
				return err
			}
			if v.Status != "succeeded" {
				continue
			}
			if _, err = e.s.GetResearchTurnApplication(id, v.ID); errors.Is(err, store.ErrNotFound) {
				m, _, problem = e.applyResearchSource(m, v.ID)
				if problem != nil {
					break
				}
			} else if err != nil {
				problem = err
				break
			}
		}
		if problem == nil {
			checkpoints, err := e.s.ListResearchPlannerCheckpoints(id)
			if err != nil {
				return err
			}
			for _, c := range checkpoints {
				if c.Epoch != m.Epoch {
					continue
				}
				v, err := e.s.GetInvocation(c.PlannerInvocationID)
				if err != nil {
					return err
				}
				if v.Status != "succeeded" {
					continue
				}
				if _, err = e.s.GetResearchPlanApplication(id, v.ID); errors.Is(err, store.ErrNotFound) {
					problem = e.applyResearchPlan(m, v)
					if problem != nil {
						break
					}
				} else if err != nil {
					problem = err
					break
				}
			}
		}
	}
	if problem == nil {
		if err = e.s.RecoverResearchFlow(id); err != nil {
			return err
		}
		m, err = e.s.GetMission(id)
		if err != nil {
			return err
		}
		interrupted := m.State == "running" || m.State == "verifying" || m.State == "pausing"
		if m.State == "planning" && m.Stage != "awaiting_tool_approval" {
			_, _, resumeErr := e.researchResumable(m)
			if resumeErr != nil && !errors.Is(resumeErr, errNoResearchCheckpoint) {
				return resumeErr
			}
			interrupted = errors.Is(resumeErr, errNoResearchCheckpoint)
		}
		if !interrupted {
			return nil
		}
		_, err = e.s.UpdateMission(id, m.Revision, "research.interrupted", func(m *domain.Mission) error {
			m.Epoch++
			m.State = "blocked"
			m.Stage = "mcp_interrupted"
			m.Error = "interrupted research segment; explicitly replan, no calls were replayed"
			return nil
		})
		return err
	}
	if errors.Is(problem, store.ErrMCPFutureVersion) {
		return nil
	}
	var sqlError sqlite3.Error
	if errors.As(problem, &sqlError) {
		return problem
	}
	m, err = e.s.GetMission(id)
	if err != nil {
		return err
	}
	_, err = e.s.UpdateMission(id, m.Revision, "research.integrity_failed", func(m *domain.Mission) error {
		m.Epoch++
		m.State = "blocked"
		m.Stage = "mcp_corrupt"
		m.Error = "research evidence or authority integrity failed"
		return nil
	})
	return err
}
