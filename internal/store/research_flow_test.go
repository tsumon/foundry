package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"foundry/internal/domain"
)

func researchFixture(t *testing.T) (*Store, domain.Mission, domain.ReadGrant) {
	t.Helper()
	s := openTestStore(t)
	base, old := mcpStoreFixture(t, s, "v1-fixture")
	base.ID = "research-v2"
	base.ToolMode = domain.ToolModeControlledMCPReadV2
	base.WorkspaceMode = domain.WorkspaceModeControlledMCPReadV2
	m, err := s.CreateMission(base)
	if err != nil {
		t.Fatal(err)
	}
	g := old
	g.ID, g.MissionID, g.CreatedAt = "v2-grant", m.ID, ""
	m, g, err = s.CreateReadGrant(g, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return s, m, g
}

func researchInput(id string, m domain.Mission, g domain.ReadGrant) domain.Invocation {
	v := modelInvocation(id, m, g, g.BindingDigest)
	v.SessionID = g.SessionID
	return v
}

func researchFinish(t *testing.T, s *Store, v domain.Invocation) (domain.Mission, domain.Invocation) {
	t.Helper()
	if _, err := s.ClaimInvocation(v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	kind := "model_output"
	if v.Kind == "repository" {
		kind = "tool_observation"
	}
	e := controlledEnvelope(kind, v.ID, v.MissionID, v.BindingDigest, `{}`)
	v, err := s.FinishInvocation(v.ID, v.Epoch, "succeeded", &e, "", domain.CallUsage{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.GetMission(v.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	return m, v
}

func researchObservation(t *testing.T, s *Store, m domain.Mission, g domain.ReadGrant) (domain.Mission, domain.ResearchObservationRef) {
	t.Helper()
	m, source, err := s.BeginResearchTurn(researchInput("turn-one", m, g), nil, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m, source = researchFinish(t, s, source)
	v := repositoryInvocation("repo-one", m, g, g.BindingDigest)
	v.SessionID = g.SessionID
	a := domain.ResearchTurnApplication{MissionID: m.ID, Epoch: m.Epoch, SessionID: g.SessionID, SourceInvocationID: source.ID, SourceResult: *source.Result, Kind: "repository", TargetID: v.ID}
	m, _, err = s.ApplyResearchTurn(a, &v, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m, v = researchFinish(t, s, v)
	return m, domain.ResearchObservationRef{Kind: "repository", InvocationID: v.ID, Result: *v.Result}
}

// Missing atomic source registration would let a reserved turn lack its owner checkpoint.
func TestResearchFlowFirstTurnSourceAndReplay(t *testing.T) {
	s, m, g := researchFixture(t)
	v := researchInput("first", m, g)
	first, saved, err := s.BeginResearchTurn(v, nil, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	again, replay, err := s.BeginResearchTurn(v, nil, m.Revision)
	if err != nil || replay.ID != saved.ID || again.Revision != first.Revision || again.Budget.UsedCalls != 1 {
		t.Fatalf("unstable turn: %+v %v", again, err)
	}
	source, err := s.GetMCPProposalSource(m.ID, v.ID)
	if err != nil || source.Purpose != "research_turn_v2" {
		t.Fatalf("missing v2 source: %+v %v", source, err)
	}
	if _, err := s.db.Exec(`UPDATE mcp_proposal_sources SET session_id='other' WHERE model_invocation_id=?`, v.ID); err == nil {
		t.Fatal("source mutable")
	}
	if _, err := s.ClaimInvocation(saved.ID, saved.Epoch); err != nil {
		t.Fatal(err)
	}
}

// Missing observation uniqueness or replay comparison would double-charge paid calls.
func TestResearchFlowConsumptionConcurrentReplayAndConflict(t *testing.T) {
	s, m, g := researchFixture(t)
	m, observation := researchObservation(t, s, m, g)
	v := researchInput("consumer-one", m, g)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, got, err := s.BeginResearchTurn(v, &observation, m.Revision)
			if err == nil && got.ID != v.ID {
				err = errors.New("different consumer")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	current, _ := s.GetMission(m.ID)
	if current.Budget.UsedCalls != 2 {
		t.Fatalf("model reservations=%d want 2", current.Budget.UsedCalls)
	}
	changed := observation
	changed.Result.Digest = strings.Repeat("f", 64)
	if _, _, err := s.BeginResearchTurn(v, &changed, current.Revision); err == nil {
		t.Fatal("changed evidence accepted")
	}
	other := researchInput("consumer-two", m, g)
	if _, _, err := s.BeginResearchTurn(other, &observation, current.Revision); err == nil {
		t.Fatal("second consumer accepted")
	}
	consumptions, err := s.ListResearchConsumptions(m.ID)
	if err != nil || len(consumptions) != 1 || consumptions[0].ConsumerInvocationID != v.ID {
		t.Fatalf("consumptions=%+v err=%v", consumptions, err)
	}
}

// A non-atomic final transition or missing planner identity allows duplicate planning calls.
func TestResearchFlowFinalPlannerCheckpoint(t *testing.T) {
	s, m, g := researchFixture(t)
	m, source, err := s.BeginResearchTurn(researchInput("final-source", m, g), nil, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m, source = researchFinish(t, s, source)
	a := domain.ResearchTurnApplication{MissionID: m.ID, Epoch: m.Epoch, SessionID: g.SessionID, SourceInvocationID: source.ID, SourceResult: *source.Result, Kind: "final", Research: &domain.Research{Summary: "verified brief"}}
	expected := m.Revision
	m, saved, err := s.ApplyResearchTurn(a, nil, expected)
	if err != nil || m.Stage != "planner_ready" || m.Research == nil || m.Research.Summary != "verified brief" {
		t.Fatalf("final=%+v %v", m, err)
	}
	again, _, err := s.ApplyResearchTurn(a, nil, expected)
	if err != nil || again.Revision != m.Revision {
		t.Fatalf("final replay: %v", err)
	}
	v := researchInput("planner-one", m, g)
	v.Role, v.GrantID = domain.Planner, ""
	m, planner, err := s.BeginResearchPlanner(v, saved.SourceInvocationID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_, replay, err := s.BeginResearchPlanner(v, saved.SourceInvocationID, expected)
	if err != nil || planner.ID != replay.ID {
		t.Fatalf("planner replay: %v", err)
	}
	checkpoint, err := s.GetResearchPlannerCheckpoint(m.ID, saved.SourceInvocationID)
	if err != nil || checkpoint.PlannerInvocationID != planner.ID {
		t.Fatalf("planner checkpoint: %+v %v", checkpoint, err)
	}
	if _, err := s.ClaimInvocation(planner.ID, planner.Epoch); err != nil {
		t.Fatal(err)
	}
}

// Context authorization must not authorize a Worker, even with identical bindings.
func TestMCPV2RoleAuthorityAndMixedModes(t *testing.T) {
	s, m, g := researchFixture(t)
	if domain.IsControlledMCPRead(domain.WorkspaceModeControlledMCPReadV1, domain.ToolModeControlledMCPReadV2) {
		t.Fatal("mixed pair accepted")
	}
	m, err := s.UpdateMission(m.ID, m.Revision, "fixture.running", func(m *domain.Mission) error { m.State = "running"; m.Stage = "work"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	v := researchInput("worker", m, g)
	v.Role, v.GrantID = domain.Worker, ""
	if _, _, err := s.BeginInvocation(v, m.Revision); err == nil {
		t.Fatal("context authorized Worker")
	}
	m, err = s.UpdateMission(m.ID, m.Revision, "fixture.execute", func(m *domain.Mission) error {
		m.Plan = &domain.Plan{Revision: 1}
		m.Approvals = append(m.Approvals, domain.Approval{Kind: "execute", Digest: strings.Repeat("c", 64), PlanRevision: 1})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	v.AuthorityDigest = strings.Repeat("c", 64)
	_, v, err = s.BeginInvocation(v, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimInvocation(v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
}

// A successful Planner output must be applied once without another paid call.
func TestResearchFlowPlannerResultApplication(t *testing.T) {
	s, m, g := researchFixture(t)
	m, source, err := s.BeginResearchTurn(researchInput("final-source", m, g), nil, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m, source = researchFinish(t, s, source)
	m, _, err = s.ApplyResearchTurn(domain.ResearchTurnApplication{MissionID: m.ID, Epoch: m.Epoch, SessionID: g.SessionID, SourceInvocationID: source.ID, SourceResult: *source.Result, Kind: "final", Research: &domain.Research{Summary: "brief"}}, nil, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	v := researchInput("planner", m, g)
	v.Role, v.GrantID = domain.Planner, ""
	m, v, err = s.BeginResearchPlanner(v, source.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m, v = researchFinish(t, s, v)
	a := domain.ResearchPlanApplication{MissionID: m.ID, Epoch: m.Epoch, SessionID: g.SessionID, PlannerInvocationID: v.ID, PlannerResult: *v.Result, Plan: domain.Plan{Revision: 1, Summary: "plan", Steps: []domain.Step{{ID: "one", Status: "pending"}}}, Proposal: domain.Approval{Kind: "plan_proposal", Digest: strings.Repeat("c", 64), Revision: 1, PlanRevision: 1}}
	expected := m.Revision
	m, saved, err := s.ApplyResearchPlan(a, expected)
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "awaiting_approval" || m.Stage != "awaiting_approval" || m.Plan == nil || m.Plan.Summary != "plan" || m.Budget.UsedCalls != 2 {
		t.Fatalf("wrong planned state: %+v", m)
	}
	again, replay, err := s.ApplyResearchPlan(a, expected)
	if err != nil || !sameJSON(replay, saved) || again.Revision != m.Revision {
		t.Fatalf("unstable plan replay: %v", err)
	}
	a.Plan.Summary = "changed"
	if _, _, err := s.ApplyResearchPlan(a, m.Revision); err == nil {
		t.Fatal("changed plan accepted")
	}
	history, err := s.ListPlanRevisions(m.ID)
	if err != nil || len(history) != 1 || history[0].Source != "planner" {
		t.Fatalf("plan history=%+v %v", history, err)
	}
	if _, err := s.GetResearchPlanApplication(m.ID, v.ID); err != nil {
		t.Fatal(err)
	}
}

// Removing the transaction would leave a paid reservation or source after failure.
func TestResearchFlowReservationRollback(t *testing.T) {
	for _, boundary := range []string{"source", "consumption", "event", "commit"} {
		t.Run(boundary, func(t *testing.T) {
			s, m, g := researchFixture(t)
			m, o := researchObservation(t, s, m, g)
			switch boundary {
			case "source":
				_, err := s.db.Exec(`CREATE TRIGGER fail_source BEFORE INSERT ON mcp_proposal_sources BEGIN SELECT RAISE(ABORT,'fixture'); END`)
				if err != nil {
					t.Fatal(err)
				}
			case "consumption":
				_, err := s.db.Exec(`CREATE TRIGGER fail_consumption BEFORE INSERT ON research_consumptions BEGIN SELECT RAISE(ABORT,'fixture'); END`)
				if err != nil {
					t.Fatal(err)
				}
			case "event":
				_, err := s.db.Exec(`CREATE TRIGGER fail_event BEFORE INSERT ON events WHEN NEW.kind='research.turn_reserved' BEGIN SELECT RAISE(ABORT,'fixture'); END`)
				if err != nil {
					t.Fatal(err)
				}
			case "commit":
				installDeferredFailure(t, s, "research_consumptions")
			}
			if _, _, err := s.BeginResearchTurn(researchInput("rollback", m, g), &o, m.Revision); err == nil {
				t.Fatal("injected failure accepted")
			}
			got, err := s.GetMission(m.ID)
			if err != nil || !sameJSON(got, m) {
				t.Fatalf("mission changed: %v", err)
			}
			if _, err := s.GetInvocation("rollback"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("reservation leaked: %v", err)
			}
			if _, err := s.GetMCPProposalSource(m.ID, "rollback"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("source leaked: %v", err)
			}
		})
	}
}

// SQL ownership and immutable checksums must be checked even on historical reads.
func TestResearchFlowHistoryCorruptionAndFutureVersion(t *testing.T) {
	for _, mutation := range []string{"owner", "result", "future", "infrastructure"} {
		t.Run(mutation, func(t *testing.T) {
			s, m, g := researchFixture(t)
			m, o := researchObservation(t, s, m, g)
			_, v, err := s.BeginResearchTurn(researchInput("consumer", m, g), &o, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "infrastructure" {
				if _, err := s.db.Exec(`ALTER TABLE research_consumptions RENAME TO missing_consumptions`); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.db.Exec(`DROP TRIGGER research_consumptions_no_update`); err != nil {
					t.Fatal(err)
				}
				switch mutation {
				case "owner":
					_, err = s.db.Exec(`UPDATE research_consumptions SET session_id='wrong'`)
				case "future":
					_, err = s.db.Exec(`UPDATE research_consumptions SET version=3`)
				case "result":
					c, err := s.GetResearchConsumption(m.ID, v.ID)
					if err != nil {
						t.Fatal(err)
					}
					c.Observation.Result.Digest = strings.Repeat("f", 64)
					raw, _ := json.Marshal(c)
					_, err = s.db.Exec(`UPDATE research_consumptions SET data=?,digest=?`, raw, mcpHash(raw))
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			rows, err := s.ListResearchConsumptions(m.ID)
			if err == nil || rows != nil {
				t.Fatalf("partial/corrupt history: %+v %v", rows, err)
			}
			if mutation == "future" && !errors.Is(err, ErrMCPFutureVersion) {
				t.Fatalf("future error=%v", err)
			}
		})
	}
}

// Direct stage mutation must not mint continuation permission.
func TestResearchFlowObservationCannotUnblockByStageWrite(t *testing.T) {
	s, m, _ := researchFixture(t)
	m, err := s.UpdateMission(m.ID, m.Revision, "fixture.ready", func(m *domain.Mission) error { m.Stage = "mcp_observation_ready"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateMission(m.ID, m.Revision, "forged.resume", func(m *domain.Mission) error { m.Stage = "research_turn"; return nil }); err == nil {
		t.Fatal("stage-only resume accepted")
	}
}

func researchMCPObservation(t *testing.T, s *Store, m domain.Mission, g domain.ReadGrant, id string, observation *domain.ResearchObservationRef, toolError bool) (domain.Mission, domain.ResearchObservationRef) {
	t.Helper()
	m, v, err := s.BeginResearchTurn(researchInput("source-"+id, m, g), observation, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m, v = researchFinish(t, s, v)
	m, p, err := s.CreateMCPProposal(domain.MCPProposal{ID: "proposal-" + id, MissionID: m.ID, SourceInvocationID: v.ID, SourceResult: *v.Result, ToolID: "lookup", Arguments: json.RawMessage(`{}`)}, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	application, err := s.GetResearchTurnApplication(m.ID, v.ID)
	if err != nil || application.Kind != "mcp" || application.TargetID != p.ID {
		t.Fatalf("MCP application %+v %v", application, err)
	}
	m, _, call, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request-"+id, m.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimMCPInvocation(m.ID, call.ID, m.Epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkMCPCallStarted(m.ID, call.ID, m.Epoch); err != nil {
		t.Fatal(err)
	}
	kind, status, code := "mcp_result", "succeeded", ""
	if toolError {
		status, code = "failed", "tool_error"
	}
	e := controlledEnvelope(kind, call.ID, m.ID, g.BindingDigest, `{}`)
	call, err = s.FinishMCPInvocation(m.ID, call.ID, m.Epoch, domain.MCPCallOutcome{Status: status, Result: &e, ErrorCode: code})
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m, domain.ResearchObservationRef{Kind: "mcp", InvocationID: call.ID, Result: *call.Result}
}

// Recovery must not reinterpret tagged v2 sources as ABv1 bare MCP proposals.
func TestResearchFlowMCPObservationRecoveryAndConsumption(t *testing.T) {
	for _, toolError := range []bool{false, true} {
		t.Run(fmt.Sprint(toolError), func(t *testing.T) {
			s, m, g := researchFixture(t)
			m, o := researchMCPObservation(t, s, m, g, "first", nil, toolError)
			if m.Stage != "mcp_observation_ready" {
				t.Fatalf("stage=%s", m.Stage)
			}
			if err := s.RecoverMCPInvocations(m.ID); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetMission(m.ID)
			if err != nil || !sameJSON(got, m) {
				t.Fatalf("verified observation changed during recovery: %+v %v", got, err)
			}
			m, v, err := s.BeginResearchTurn(researchInput("consumer", m, g), &o, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if m.Stage != "research_turn" || m.Budget.UsedCalls != 2 {
				t.Fatalf("continuation=%+v", m)
			}
			if _, err := s.ClaimInvocation(v.ID, v.Epoch); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Lifetime quotas must count both ledgers, including across new authorized epochs.
func TestMCPV2SharedQuotaBothDirections(t *testing.T) {
	for _, last := range []string{"repository", "mcp"} {
		t.Run(last, func(t *testing.T) {
			s, m, g := researchFixture(t)
			m, o := researchMCPObservation(t, s, m, g, "first", nil, false)
			// A new explicitly authorized epoch shares the lifetime quota, not turns.
			for i := 0; i < 2; i++ {
				var source domain.Invocation
				var err error
				m, source, err = s.BeginResearchTurn(researchInput(fmt.Sprintf("repo-source-%d", i), m, g), &o, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				m, source = researchFinish(t, s, source)
				v := repositoryInvocation(fmt.Sprintf("repo-%d", i), m, g, g.BindingDigest)
				v.SessionID = g.SessionID
				a := domain.ResearchTurnApplication{MissionID: m.ID, Epoch: m.Epoch, SessionID: g.SessionID, SourceInvocationID: source.ID, SourceResult: *source.Result, Kind: "repository", TargetID: v.ID}
				m, _, err = s.ApplyResearchTurn(a, &v, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				m, v = researchFinish(t, s, v)
				o = domain.ResearchObservationRef{Kind: "repository", InvocationID: v.ID, Result: *v.Result}
			}
			m, err := s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			m, err = s.UpdateMission(m.ID, m.Revision, "fixture.new_authorized_epoch", func(m *domain.Mission) error { m.Epoch++; m.Stage = "plan"; return nil })
			if err != nil {
				t.Fatal(err)
			}
			g.ID, g.Epoch, g.SessionID, g.CreatedAt = "epoch-two-grant", m.Epoch, "epoch-two-session", ""
			m, g, err = s.CreateReadGrant(g, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			v := researchInput("new-epoch-source", m, g)
			v.Input.Epoch = m.Epoch
			m, v, err = s.BeginResearchTurn(v, nil, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
				t.Fatal(err)
			}
			e := controlledEnvelope("model_output", v.ID, m.ID, g.BindingDigest, `{}`)
			e.Epoch = m.Epoch
			v, err = s.FinishInvocation(v.ID, v.Epoch, "succeeded", &e, "", domain.CallUsage{})
			if err != nil {
				t.Fatal(err)
			}
			m, err = s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			if last == "repository" {
				repo := repositoryInvocation("fourth", m, g, g.BindingDigest)
				repo.SessionID = g.SessionID
				repo.Input.Epoch = m.Epoch
				a := domain.ResearchTurnApplication{MissionID: m.ID, Epoch: m.Epoch, SessionID: g.SessionID, SourceInvocationID: v.ID, SourceResult: *v.Result, Kind: "repository", TargetID: repo.ID}
				if _, _, err := s.ApplyResearchTurn(a, &repo, m.Revision); !errors.Is(err, ErrMCPBudget) {
					t.Fatalf("quota error=%v", err)
				}
				_, err := s.db.Exec(`INSERT INTO invocations(id,mission_id,epoch,session_id,ordinal,kind,role,name,binding_digest,authority_digest,grant_id,intent,status,reserved_result_bytes,created_at) SELECT 'sql-fourth',mission_id,epoch,session_id,ordinal+100,kind,role,name,binding_digest,authority_digest,grant_id,intent,'authorized_pending',reserved_result_bytes,created_at FROM invocations WHERE id='repo-1'`)
				if err == nil || !strings.Contains(err.Error(), "shared tool quota") {
					t.Fatalf("SQL quota error=%v", err)
				}
			} else {
				var p domain.MCPProposal
				m, p, err = s.CreateMCPProposal(domain.MCPProposal{ID: "fourth", MissionID: m.ID, SourceInvocationID: v.ID, SourceResult: *v.Result, ToolID: "lookup", Arguments: json.RawMessage(`{}`)}, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, _, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "fourth", m.Revision, true); !errors.Is(err, ErrMCPBudget) {
					t.Fatalf("quota error=%v", err)
				}
			}
		})
	}
}

// Latest execute authority must still bind the current plan at claim time.
func TestMCPV2ClaimRejectsSupersededPlan(t *testing.T) {
	s, m, g := researchFixture(t)
	m, err := s.UpdateMission(m.ID, m.Revision, "fixture.execution", func(m *domain.Mission) error {
		m.State = "running"
		m.Stage = "work"
		m.Plan = &domain.Plan{Revision: 1}
		m.Approvals = append(m.Approvals, domain.Approval{Kind: "execute", Digest: strings.Repeat("d", 64), PlanRevision: 1})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	v := researchInput("worker", m, g)
	v.Role, v.GrantID, v.AuthorityDigest = domain.Worker, "", strings.Repeat("d", 64)
	m, v, err = s.BeginInvocation(v, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateMission(m.ID, m.Revision, "fixture.new_plan", func(m *domain.Mission) error { m.Plan.Revision++; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(v.ID, v.Epoch); err == nil {
		t.Fatal("stale execution approval claimed")
	}
}

// A final-write failure cannot leak Research or planner_ready before commit.
func TestResearchFlowFinalApplicationRollback(t *testing.T) {
	s, m, g := researchFixture(t)
	m, v, err := s.BeginResearchTurn(researchInput("final", m, g), nil, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m, v = researchFinish(t, s, v)
	installDeferredFailure(t, s, "research_turn_applications")
	a := domain.ResearchTurnApplication{MissionID: m.ID, Epoch: m.Epoch, SessionID: g.SessionID, SourceInvocationID: v.ID, SourceResult: *v.Result, Kind: "final", Research: &domain.Research{Summary: "brief"}}
	if _, _, err := s.ApplyResearchTurn(a, nil, m.Revision); err == nil {
		t.Fatal("commit failure accepted")
	}
	got, err := s.GetMission(m.ID)
	if err != nil || !sameJSON(got, m) {
		t.Fatalf("final changed before commit: %v", err)
	}
	if _, err := s.GetResearchTurnApplication(m.ID, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("application leaked: %v", err)
	}
}

// New source purpose is mandatory at both the SQL dispatch and history boundaries.
func TestMCPV2SourcePurposeAndFutureRecovery(t *testing.T) {
	for _, mode := range []string{"purpose", "future"} {
		t.Run(mode, func(t *testing.T) {
			s, m, g := researchFixture(t)
			m, v, err := s.BeginResearchTurn(researchInput("first", m, g), nil, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`DROP TRIGGER mcp_proposal_sources_no_update`); err != nil {
				t.Fatal(err)
			}
			if mode == "purpose" {
				source, err := s.GetMCPProposalSource(m.ID, v.ID)
				if err != nil {
					t.Fatal(err)
				}
				source.Purpose = "research_turn"
				raw, _ := json.Marshal(source)
				if _, err := s.db.Exec(`UPDATE mcp_proposal_sources SET data=?,digest=? WHERE model_invocation_id=?`, raw, mcpHash(raw), v.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ClaimInvocation(v.ID, v.Epoch); err == nil {
					t.Fatal("ABv1 purpose claimed in v2")
				}
				if _, err := s.db.Exec(`UPDATE invocations SET status='dispatching',dispatched_at=? WHERE id=?`, timestamp(), v.ID); err == nil {
					t.Fatal("SQL bypassed purpose fence")
				}
			} else {
				if _, err := s.db.Exec(`UPDATE mcp_proposal_sources SET version=3 WHERE model_invocation_id=?`, v.ID); err != nil {
					t.Fatal(err)
				}
				if err := s.RecoverResearchFlow(m.ID); err != nil {
					t.Fatal(err)
				}
				got, err := s.GetMission(m.ID)
				if err != nil || !sameJSON(got, m) {
					t.Fatalf("future mission rewritten: %v", err)
				}
				gotV, err := s.GetInvocation(v.ID)
				if err != nil || gotV.Status != "authorized_pending" {
					t.Fatalf("future reservation rewritten: %v", err)
				}
			}
		})
	}
}

// A new observation never bypasses the shared paid-model budget.
func TestResearchFlowModelBudgetRollback(t *testing.T) {
	s, m, g := researchFixture(t)
	m, err := s.UpdateMission(m.ID, m.Revision, "fixture.budget", func(m *domain.Mission) error { m.Budget.MaxCalls = 1; return nil })
	if err != nil {
		t.Fatal(err)
	}
	m, o := researchObservation(t, s, m, g)
	if _, _, err := s.BeginResearchTurn(researchInput("over-budget", m, g), &o, m.Revision); err == nil {
		t.Fatal("model budget bypassed")
	}
	got, err := s.GetMission(m.ID)
	if err != nil || !sameJSON(got, m) {
		t.Fatalf("budget denial changed checkpoint: %v", err)
	}
	cs, err := s.ListResearchConsumptions(m.ID)
	if err != nil || len(cs) != 0 {
		t.Fatalf("budget denial consumed observation: %+v %v", cs, err)
	}
}

// The fourth and last allowed Researcher call can finish, but cannot propose a tool.
func TestResearchFlowLastTurnReservedForFinal(t *testing.T) {
	s, m, g := researchFixture(t)
	var observation *domain.ResearchObservationRef
	for i := 0; i < 3; i++ {
		var o domain.ResearchObservationRef
		m, o = researchMCPObservation(t, s, m, g, fmt.Sprint(i), observation, false)
		observation = &o
	}
	m, v, err := s.BeginResearchTurn(researchInput("fourth", m, g), observation, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m, v = researchFinish(t, s, v)
	if _, _, err := s.CreateMCPProposal(domain.MCPProposal{ID: "last-tool", MissionID: m.ID, SourceInvocationID: v.ID, SourceResult: *v.Result, ToolID: "lookup", Arguments: json.RawMessage(`{}`)}, m.Revision); err == nil {
		t.Fatal("fourth turn proposed a tool")
	}
	m, _, err = s.ApplyResearchTurn(domain.ResearchTurnApplication{MissionID: m.ID, Epoch: m.Epoch, SessionID: g.SessionID, SourceInvocationID: v.ID, SourceResult: *v.Result, Kind: "final", Research: &domain.Research{Summary: "done"}}, nil, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if m.Budget.UsedCalls != 4 || m.Stage != "planner_ready" {
		t.Fatalf("last final=%+v", m)
	}
	if _, _, err := s.BeginResearchTurn(researchInput("fifth", m, g), observation, m.Revision); err == nil {
		t.Fatal("fifth Researcher turn accepted")
	}
}

// A valid checksum cannot hide a call whose authority differs from its grant.
func TestResearchFlowMCPConsumptionRejectsCorruptCallAuthority(t *testing.T) {
	s, m, g := researchFixture(t)
	m, o := researchMCPObservation(t, s, m, g, "first", nil, false)
	if _, _, err := s.BeginResearchTurn(researchInput("consumer", m, g), &o, m.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER mcp_invocation_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER mcp_invocation_transition`); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := s.db.QueryRow(`SELECT intent FROM mcp_invocations WHERE id=?`, o.InvocationID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var v domain.MCPInvocation
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	v.AuthorityDigest = strings.Repeat("f", 64)
	raw, _ = json.Marshal(v)
	if _, err := s.db.Exec(`UPDATE mcp_invocations SET intent=?,digest=? WHERE id=?`, raw, mcpHash(raw), o.InvocationID); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ListResearchConsumptions(m.ID); err == nil || rows != nil {
		t.Fatalf("corrupt authority accepted: %+v %v", rows, err)
	}
}

// Claim must verify the owning checkpoint, not merely its presence in SQL.
func TestResearchFlowClaimRejectsCorruptApplication(t *testing.T) {
	for _, kind := range []string{"repository", "planner", "consumer"} {
		t.Run(kind, func(t *testing.T) {
			s, m, g := researchFixture(t)
			if kind == "consumer" {
				m, o := researchObservation(t, s, m, g)
				_, v, err := s.BeginResearchTurn(researchInput("consumer", m, g), &o, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`DROP TRIGGER research_consumptions_no_update`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`UPDATE research_consumptions SET digest='corrupt'`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ClaimInvocation(v.ID, v.Epoch); err == nil {
					t.Fatal("corrupt consumption claimed")
				}
				return
			}
			m, source, err := s.BeginResearchTurn(researchInput("source", m, g), nil, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			m, source = researchFinish(t, s, source)
			v := repositoryInvocation("repository", m, g, g.BindingDigest)
			v.SessionID = g.SessionID
			if kind == "planner" {
				m, _, err = s.ApplyResearchTurn(domain.ResearchTurnApplication{MissionID: m.ID, Epoch: m.Epoch, SessionID: g.SessionID, SourceInvocationID: source.ID, SourceResult: *source.Result, Kind: "final", Research: &domain.Research{Summary: "brief"}}, nil, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				v = researchInput("planner", m, g)
				v.Role, v.GrantID = domain.Planner, ""
				_, v, err = s.BeginResearchPlanner(v, source.ID, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`DROP TRIGGER research_planner_checkpoints_no_update`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`UPDATE research_planner_checkpoints SET digest='corrupt'`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ClaimInvocation(v.ID, v.Epoch); err == nil {
					t.Fatal("corrupt planner checkpoint claimed")
				}
				return
			}
			m, _, err = s.ApplyResearchTurn(domain.ResearchTurnApplication{MissionID: m.ID, Epoch: m.Epoch, SessionID: g.SessionID, SourceInvocationID: source.ID, SourceResult: *source.Result, Kind: "repository", TargetID: v.ID}, &v, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`DROP TRIGGER research_turn_applications_no_update`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE research_turn_applications SET digest='corrupt'`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ClaimInvocation(v.ID, v.Epoch); err == nil {
				t.Fatal("corrupt application claimed")
			}
		})
	}
}

// Omitting the v2 application check at either MCP dispatch boundary would let
// an approved proposal execute after its durable application was corrupted.
func TestResearchFlowMCPDispatchRejectsCorruptApplication(t *testing.T) {
	for _, boundary := range []string{"claim", "call_started"} {
		for _, corruption := range []string{"digest", "owner"} {
			t.Run(boundary+"/"+corruption, func(t *testing.T) {
				s, m, g := researchFixture(t)
				m, source, err := s.BeginResearchTurn(researchInput("source", m, g), nil, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				m, source = researchFinish(t, s, source)
				m, p, err := s.CreateMCPProposal(domain.MCPProposal{ID: "proposal", MissionID: m.ID, SourceInvocationID: source.ID, SourceResult: *source.Result, ToolID: "lookup", Arguments: json.RawMessage(`{}`)}, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				m, _, call, err := s.DecideMCPProposal(m.ID, p.ID, p.Digest, "request", m.Revision, true)
				if err != nil {
					t.Fatal(err)
				}
				if boundary == "call_started" {
					call, err = s.ClaimMCPInvocation(m.ID, call.ID, m.Epoch)
					if err != nil {
						t.Fatal(err)
					}
				}
				beforeEvents, err := s.Events(0, 100)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`DROP TRIGGER research_turn_applications_no_update`); err != nil {
					t.Fatal(err)
				}
				query := `UPDATE research_turn_applications SET digest='corrupt' WHERE source_invocation_id=?`
				if corruption == "owner" {
					query = `UPDATE research_turn_applications SET session_id='wrong-session' WHERE source_invocation_id=?`
				}
				if _, err := s.db.Exec(query, source.ID); err != nil {
					t.Fatal(err)
				}
				if boundary == "claim" {
					_, err = s.ClaimMCPInvocation(m.ID, call.ID, m.Epoch)
				} else {
					_, err = s.MarkMCPCallStarted(m.ID, call.ID, m.Epoch)
				}
				if err == nil || errors.Is(err, ErrConflict) {
					t.Fatalf("application corruption not propagated: %v", err)
				}
				after, err := s.GetMCPInvocation(m.ID, call.ID)
				if err != nil || !sameJSON(after, call) {
					t.Fatalf("rejected dispatch changed call state/phase: before=%+v after=%+v err=%v", call, after, err)
				}
				afterMission, err := s.GetMission(m.ID)
				if err != nil || !sameJSON(afterMission, m) {
					t.Fatalf("rejected dispatch changed mission: %v", err)
				}
				afterEvents, err := s.Events(0, 100)
				if err != nil || len(afterEvents) != len(beforeEvents) {
					t.Fatalf("rejected dispatch appended events: %v", err)
				}
			})
		}
	}
}
