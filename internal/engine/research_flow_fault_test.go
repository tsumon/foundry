package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/store"
	"os"
	"path/filepath"
	"testing"
)

// Drive the real durable boundary and stop before its next segment, as a
// process crash would. No model or Store transition is replaced by a mock.
func prepareResearchCheckpoint(t *testing.T, e *Engine, m domain.Mission, output string) (domain.Mission, domain.Invocation) {
	t.Helper()
	e.gateway = &scripted{outputs: []string{output}}
	var err error
	m, err = e.resetGeneration(m)
	if err != nil {
		t.Fatal(err)
	}
	m, err = e.grantPlanningRead(m)
	if err != nil {
		t.Fatal(err)
	}
	m, err = e.s.UpdateMission(m.ID, m.Revision, "fixture.image_resolved", func(m *domain.Mission) error { m.Spec.ImageID = "sha256:fixture"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.reserveResearch(m, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	v, err = e.s.ClaimInvocation(v.ID, v.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := e.evidenceStore().Read(v.Input)
	if err != nil {
		t.Fatal(err)
	}
	var input controlledInput
	if err = decodeControlled(string(raw), &input); err != nil {
		t.Fatal(err)
	}
	v, _, err = e.completeControlledModel(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, v, input)
	if err != nil {
		t.Fatal(err)
	}
	m, err = e.s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m, v
}

func TestResearchFlowRecoveryFutureVersionAndInfrastructureAreUntouched(t *testing.T) {
	for _, future := range []bool{false, true} {
		t.Run(map[bool]string{false: "sql", true: "future"}[future], func(t *testing.T) {
			e, _, m := researchFixture(t)
			m, source := prepareResearchCheckpoint(t, e, m, `{"kind":"final","research":{"summary":"No claims","findings":[],"unknowns":[]}}`)
			var err error
			m, _, err = e.applyResearchSource(m, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(m)
			db, err := sql.Open("sqlite3", filepath.Join(e.options.DataDir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if future {
				if _, err = db.Exec(`DROP TRIGGER research_turn_applications_no_update`); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`UPDATE research_turn_applications SET version=99 WHERE mission_id=?`, m.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err = db.Exec(`DROP TABLE research_turn_applications`); err != nil {
					t.Fatal(err)
				}
			}
			err = e.Recover(context.Background())
			if future && err != nil {
				t.Fatal(err)
			}
			if !future && err == nil {
				t.Fatal("SQL infrastructure error swallowed")
			}
			after, err := e.s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(after)
			if string(raw) != string(before) {
				t.Fatal("future version/infrastructure failure rewrote healthy mission")
			}
		})
	}
}

func TestResearchFlowDecisionRejectsConnectionProfileAndDescriptorDrift(t *testing.T) {
	for _, kind := range []string{"connection", "profile", "descriptor"} {
		t.Run(kind, func(t *testing.T) {
			e, s, m := researchFixture(t)
			if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
				t.Fatal(err)
			}
			m = researchIdle(t, e, m.ID)
			p, _ := s.ListMCPProposals(m.ID)
			switch kind {
			case "connection":
				c, err := s.GetMCPConnection(m.MCPBindings.ConnectionID)
				if err != nil {
					t.Fatal(err)
				}
				c.Enabled = false
				if _, err = s.SaveMCPConnection(c, c.Revision); err != nil {
					t.Fatal(err)
				}
			case "profile":
				var err error
				m, err = s.UpdateMission(m.ID, m.Revision, "fixture.profile_drift", func(m *domain.Mission) error {
					p := m.Profiles[domain.Researcher]
					p.Model = "different"
					m.Profiles[domain.Researcher] = p
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			case "descriptor":
				e.options.LocalMCPTest.Binding.Tools[0].Policy = []byte(`{"reviewed":false}`)
			}
			if _, err := e.DecideResearchMCP(context.Background(), m.ID, m.Revision, p[0].ID, p[0].Digest, "drift", true); err == nil {
				t.Fatal("drift authorized")
			}
			calls, err := s.ListMCPInvocations(m.ID)
			if err != nil || len(calls) != 0 {
				t.Fatal("drift reserved call", err)
			}
		})
	}
}

func TestResearchFlowRecoveryNeverReissuesReservedOrDispatchedModels(t *testing.T) {
	for _, planner := range []bool{false, true} {
		for _, claimed := range []bool{false, true} {
			name := fmt.Sprintf("planner=%v/claimed=%v", planner, claimed)
			t.Run(name, func(t *testing.T) {
				e, _, m := researchFixture(t)
				var err error
				var v domain.Invocation
				if planner {
					var source domain.Invocation
					m, source = prepareResearchCheckpoint(t, e, m, `{"kind":"final","research":{"summary":"No claims","findings":[],"unknowns":[]}}`)
					m, _, err = e.applyResearchSource(m, source.ID)
					if err != nil {
						t.Fatal(err)
					}
					v, err = e.reserveResearch(m, nil, source.ID, "")
				} else {
					m, err = e.resetGeneration(m)
					if err != nil {
						t.Fatal(err)
					}
					m, err = e.grantPlanningRead(m)
					if err != nil {
						t.Fatal(err)
					}
					v, err = e.reserveResearch(m, nil, "", "")
				}
				if err != nil {
					t.Fatal(err)
				}
				if claimed {
					v, err = e.s.ClaimInvocation(v.ID, v.Epoch)
					if err != nil {
						t.Fatal(err)
					}
				}
				e = reopenResearch(t, e)
				if err = e.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
				got, err := e.s.GetInvocation(v.ID)
				if err != nil {
					t.Fatal(err)
				}
				want := "interrupted_before_dispatch"
				if claimed {
					want = "outcome_unknown"
				}
				if got.Status != want {
					t.Fatalf("status %s want %s", got.Status, want)
				}
				m, _ = e.s.GetMission(m.ID)
				if m.State != "blocked" {
					t.Fatal("unfinished model not blocked")
				}
				if _, err = e.ContinueResearch(context.Background(), m.ID, m.Revision, "never-repeat"); err == nil {
					t.Fatal("unfinished model repeated")
				}
			})
		}
	}
}

func TestResearchFlowRecoveryFencesUnreservedSegmentGaps(t *testing.T) {
	for _, execution := range []bool{false, true} {
		t.Run(fmt.Sprint(execution), func(t *testing.T) {
			e, _, m := researchFixture(t)
			var err error
			if execution {
				m, source := prepareResearchCheckpoint(t, e, m, `{"kind":"final","research":{"summary":"No claims","findings":[],"unknowns":[]}}`)
				m, _, err = e.applyResearchSource(m, source.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = e.s.UpdateMission(m.ID, m.Revision, "fixture.execution_gap", func(m *domain.Mission) error { m.State = "running"; m.Stage = "build"; m.Epoch++; return nil }); err != nil {
					t.Fatal(err)
				}
			} else {
				m, err = e.resetGeneration(m)
				if err != nil {
					t.Fatal(err)
				}
				m, err = e.grantPlanningRead(m)
				if err != nil {
					t.Fatal(err)
				}
			}
			e = reopenResearch(t, e)
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			m, err = e.s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			if m.State != "blocked" || m.Stage != "mcp_interrupted" {
				t.Fatalf("unreserved gap stayed active: %s/%s", m.State, m.Stage)
			}
		})
	}
}

func reopenResearch(t *testing.T, e *Engine) *Engine {
	t.Helper()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(e.options.DataDir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	fresh := New(s, &scripted{}, e.options)
	t.Cleanup(func() { fresh.Close(); s.Close() })
	return fresh
}

func TestResearchFlowRecoverySourceApplicationGaps(t *testing.T) {
	for _, kind := range []string{"mcp", "repository", "final"} {
		t.Run(kind, func(t *testing.T) {
			e, _, m := researchFixture(t)
			output := map[string]string{"mcp": `{"kind":"mcp","tool_id":"reference.lookup","arguments":{"topic":"mcp-workflow"},"reason":"synthetic check"}`, "repository": `{"kind":"repository","tool":"repo.list","arguments":{}}`, "final": `{"kind":"final","research":{"summary":"No factual claims","findings":[],"unknowns":["not inspected"]}}`}[kind]
			m, v := prepareResearchCheckpoint(t, e, m, output)
			e = reopenResearch(t, e)
			if err := e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			m, err := e.s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			a, err := e.s.GetResearchTurnApplication(m.ID, v.ID)
			if err != nil || a.Kind != kind {
				t.Fatalf("application: %+v %v", a, err)
			}
			if m.Budget.UsedCalls != 1 {
				t.Fatal("recovery purchased model")
			}
			switch kind {
			case "mcp":
				if m.State != "planning" || m.Stage != "awaiting_tool_approval" {
					t.Fatalf("proposal not retained: %s/%s", m.State, m.Stage)
				}
			case "repository":
				call, err := e.s.GetInvocation(a.TargetID)
				if err != nil || call.Status != "interrupted_before_dispatch" || m.State != "blocked" {
					t.Fatalf("reserved repo reissued %+v %s %v", call, m.State, err)
				}
			case "final":
				if m.Stage != "planner_ready" {
					t.Fatal("final not ready", m.Stage)
				}
				e.gateway = &scripted{outputs: []string{plan}}
				rev := m.Revision
				if _, err = e.ContinueResearch(context.Background(), m.ID, rev, "resume-plan"); err != nil {
					t.Fatal(err)
				}
				m = researchIdle(t, e, m.ID)
				if m.State != "awaiting_approval" || m.Budget.UsedCalls != 2 {
					t.Fatalf("plan not applied %s %s", m.State, m.Error)
				}
				if _, err = e.ContinueResearch(context.Background(), m.ID, rev, "resume-plan"); err != nil {
					t.Fatal("exact continue replay", err)
				}
				if _, err = e.ContinueResearch(context.Background(), m.ID, m.Revision, "resume-plan"); !errors.Is(err, store.ErrConflict) {
					t.Fatal("changed continue intent accepted", err)
				}
			}
		})
	}
}

func TestResearchFlowRecoveryPlannerApplicationGap(t *testing.T) {
	e, _, m := researchFixture(t)
	m, source := prepareResearchCheckpoint(t, e, m, `{"kind":"final","research":{"summary":"No factual claims","findings":[],"unknowns":[]}}`)
	var err error
	m, _, err = e.applyResearchSource(m, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.reserveResearch(m, nil, source.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	v, err = e.s.ClaimInvocation(v.ID, v.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := e.evidenceStore().Read(v.Input)
	var input controlledInput
	if err = decodeControlled(string(raw), &input); err != nil {
		t.Fatal(err)
	}
	e.gateway = &scripted{outputs: []string{plan}}
	v, _, err = e.completeControlledModel(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, v, input)
	if err != nil {
		t.Fatal(err)
	}
	e = reopenResearch(t, e)
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err = e.s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "awaiting_approval" || m.Plan == nil || m.Budget.UsedCalls != 2 {
		t.Fatalf("planner recovery %s %s", m.State, m.Error)
	}
	if _, err = e.s.GetResearchPlanApplication(m.ID, v.ID); err != nil {
		t.Fatal(err)
	}
}

func TestResearchFlowRecoveryObservationGapAndConsumerNoReplay(t *testing.T) {
	for _, reserve := range []bool{false, true} {
		t.Run(map[bool]string{false: "observation", true: "reserved-consumer"}[reserve], func(t *testing.T) {
			e, _, m := researchFixture(t)
			if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
				t.Fatal(err)
			}
			m = researchIdle(t, e, m.ID)
			proposals, _ := e.s.ListMCPProposals(m.ID)
			p := proposals[0]
			m, _, call, err := e.s.DecideMCPProposal(m.ID, p.ID, p.Digest, "local-crash-gap", m.Revision, true)
			if err != nil {
				t.Fatal(err)
			}
			op := &operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}
			e.active = op
			call, err = e.dispatchResearchMCP(op, call.ID)
			e.active = nil
			if err != nil {
				t.Fatal(err)
			}
			m, _ = e.s.GetMission(m.ID)
			if reserve {
				if _, err = e.reserveResearch(m, &domain.ResearchObservationRef{Kind: "mcp", InvocationID: call.ID, Result: *call.Result}, "", ""); err != nil {
					t.Fatal(err)
				}
			}
			e = reopenResearch(t, e)
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			m, _ = e.s.GetMission(m.ID)
			if reserve {
				if m.State != "blocked" {
					t.Fatal("pending consumer not interrupted")
				}
				if _, err = e.ContinueResearch(context.Background(), m.ID, m.Revision, "no-retry"); err == nil {
					t.Fatal("interrupted consumer replayed")
				}
				return
			}
			if m.Stage != "mcp_observation_ready" {
				t.Fatalf("observation not resumable %s %s", m.Stage, m.Error)
			}
			e.gateway = &researchGateway{s: e.s}
			if _, err = e.ContinueResearch(context.Background(), m.ID, m.Revision, "resume-observation"); err != nil {
				t.Fatal(err)
			}
			m = researchIdle(t, e, m.ID)
			calls, err := e.s.ListMCPInvocations(m.ID)
			if err != nil || len(calls) != 1 || m.State != "awaiting_approval" {
				t.Fatalf("resumed call duplicated or blocked %s %s %v", m.State, m.Error, err)
			}
		})
	}
}

func TestResearchFlowRecoveryCorruptEvidenceAndSQLFailure(t *testing.T) {
	e, _, m := researchFixture(t)
	m, source := prepareResearchCheckpoint(t, e, m, `{"kind":"final","research":{"summary":"No claims","findings":[],"unknowns":[]}}`)
	// Locate the already sealed blob through the evidence implementation's digest path.
	root := filepath.Join(e.options.DataDir, "evidence")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(raw) == `{"kind":"final","research":{"summary":"No claims","findings":[],"unknowns":[]}}` {
			return os.WriteFile(path, []byte("corrupt"), 0600)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.evidenceStore().Read(*source.Result); err == nil {
		t.Fatal("fixture did not corrupt evidence")
	}
	e = reopenResearch(t, e)
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ = e.s.GetMission(m.ID)
	if m.State != "blocked" || m.Stage != "mcp_corrupt" {
		t.Fatal("corruption not isolated")
	}
	e.s.Close()
	if err = e.Recover(context.Background()); err == nil {
		t.Fatal("closed SQL failure swallowed")
	}
}
