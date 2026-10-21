package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/store"
	"os"
	"path/filepath"
	"testing"
)

// This runner inspects the actual selected candidate, never executes commands.
type codingContentRunner struct {
	*fakeRunner
	calls int
}

func TestCodingFailuresAreTypedAndNeverInferFromProse(t *testing.T) {
	for _, tc := range []struct {
		name, output, code string
		infra              bool
	}{
		{"invalid", `{"summary":"bad","edits":[]}`, "invalid_model_output", false},
		{"denied", `{"summary":"bad","edits":[{"path":"outside.txt","content":"x"}]}`, "edit_denied", false},
		{"infrastructure", edit, "infrastructure_failed", true},
		{"rejected", edit, "review_rejected", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, g, r, m := codingSetup(t, plan, tc.output, `{"passed":false,"summary":"not ready","findings":[]}`)
			if tc.infra {
				e.runner = r.fakeRunner
				r.runErr = errors.New("verification_failed invalid_model_output arbitrary prose")
			}
			_, err := e.Plan(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			_, err = e.Approve(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			if m.State != "blocked" || len(m.CodingAttempts) == 0 {
				t.Fatalf("missing blocked trace: %+v", m)
			}
			last := m.CodingAttempts[len(m.CodingAttempts)-1]
			if last.FailureCode != tc.code || last.Status != "failed" {
				t.Fatalf("wrong failure: %+v", last)
			}
			for _, role := range g.roles {
				if role == domain.Debugger {
					t.Fatal("retried non-validation failure")
				}
			}
			if tc.name == "invalid" || tc.name == "denied" {
				path, err := e.selectedCandidate(m)
				if err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(filepath.Join(path, "src/main.txt"))
				if err != nil || string(b) != "before\n" || len(m.Changes) != 0 {
					t.Fatal("denied edit selected", err)
				}
			}
		})
	}
}

func TestCodingBadContentRemainsFailedAfterTwoRepairs(t *testing.T) {
	bad := `{"summary":"unchanged","edits":[{"path":"src/main.txt","content":"before\n"}]}`
	e, s, g, r, m := codingSetup(t, plan, bad, bad, bad)
	_, err := e.Plan(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	_, err = e.Approve(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if m.State != "blocked" || r.calls != 3 || len(g.roles) != 4 || len(m.CodingAttempts) != 6 || len(m.Evidence) != 3 {
		t.Fatalf("repair budget/lifecycle: %+v calls=%d", m, r.calls)
	}
	for _, v := range m.Evidence {
		if v.Passed {
			t.Fatal("unchanged bad candidate passed")
		}
	}
	for _, a := range m.CodingAttempts {
		if a.Phase == "verification" && (a.Status != "failed" || a.FailureCode != "verification_failed" || a.VerificationID == "") {
			t.Fatal("verification loss", a)
		}
	}
}

func TestCodingPersistenceStopsDispatchAndRecoversWithoutReplay(t *testing.T) {
	for _, tc := range []struct {
		name, condition string
		calls, attempts int
	}{
		{"before_dispatch", `NEW.kind='coding.started'`, 1, 0},
		{"reservation", `NEW.kind='call.reserved' AND (SELECT json_array_length(json_extract(data,'$.coding_attempts')) FROM missions WHERE id=NEW.mission_id)>0`, 1, 1},
		{"call_result", `NEW.kind='call.finished'`, 2, 1},
		{"after_selection", `NEW.kind='coding.finished'`, 2, 1},
		{"only_success_finish", `NEW.kind='coding.finished' AND (SELECT json_extract(data,'$.coding_attempts[0].status') FROM missions WHERE id=NEW.mission_id)='succeeded'`, 2, 1},
		{"candidate_commit", `NEW.kind='candidate.edited'`, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, g, r, m := codingSetup(t, plan, edit, review, review)
			_, err := e.Plan(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			initial := *m.CandidateGeneration
			db := failureDB(t, e)
			if _, err = db.Exec(`CREATE TRIGGER coding_failure BEFORE INSERT ON events WHEN ` + tc.condition + ` BEGIN SELECT RAISE(ABORT,'fixture storage failure'); END`); err != nil {
				t.Fatal(err)
			}
			_, err = e.Approve(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			if len(g.roles) != tc.calls || r.calls != 0 || len(m.CodingAttempts) != tc.attempts || m.State != "blocked" {
				t.Fatalf("continued after storage failure %+v calls=%d", m, len(g.roles))
			}
			if tc.name == "before_dispatch" || tc.name == "candidate_commit" || tc.name == "reservation" || tc.name == "call_result" {
				if *m.CandidateGeneration != initial {
					t.Fatal("uncommitted candidate selected")
				}
			}
			if (tc.name == "only_success_finish" || tc.name == "reservation" || tc.name == "call_result") && m.CodingAttempts[0].FailureCode != "persistence_failed" {
				t.Fatal("failed terminal persistence was not recorded", m.CodingAttempts)
			}
			if _, err = db.Exec(`DROP TRIGGER coding_failure`); err != nil {
				t.Fatal(err)
			}
			restart := New(s, g, Options{DataDir: e.options.DataDir, Runner: r})
			for i := 0; i < 2; i++ {
				if err = restart.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			got, _ := s.GetMission(m.ID)
			if unfinishedCoding(got) || len(g.roles) != tc.calls || r.calls != 0 {
				t.Fatal("recovery replayed or left pending", got.CodingAttempts)
			}
			if tc.name == "after_selection" && got.CodingAttempts[0].Status != "interrupted" {
				t.Fatal("unknown completion guessed", got.CodingAttempts)
			}
		})
	}
}

func TestCodingTraceLimitStopsBeforeModelDispatch(t *testing.T) {
	e, s, g, r, m := codingSetup(t, plan, edit)
	_, err := e.Plan(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	// Simulate a durable history; no permission is conferred by these records.
	for i := 0; i < 256; i++ {
		m.CodingAttempts = append(m.CodingAttempts, domain.CodingAttempt{ID: fmt.Sprint(i), Epoch: m.Epoch, PlanRevision: m.Plan.Revision, Phase: "worker", Role: domain.Worker, StepID: "one", Status: "succeeded", Before: m.CandidateGeneration, After: m.CandidateGeneration, StartedAt: now(), FinishedAt: now()})
	}
	raw, _ := json.Marshal(m)
	db := failureDB(t, e)
	if _, err = db.Exec(`UPDATE missions SET data=? WHERE id=?`, raw, m.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.Approve(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if m.State != "blocked" || len(g.roles) != 1 || r.calls != 0 || len(m.CodingAttempts) != 256 {
		t.Fatal("overflow dispatched")
	}
}

func TestCodingPauseFencesLateWorkerAndDoesNotReplay(t *testing.T) {
	e, s, g, r, m := codingSetup(t, plan, edit)
	_, err := e.Plan(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	initial := *m.CandidateGeneration
	g.entered = make(chan struct{}, 1)
	g.release = make(chan struct{})
	_, err = e.Approve(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	<-g.entered
	m, _ = s.GetMission(m.ID)
	paused := make(chan error, 1)
	go func() { _, err := e.Pause(context.Background(), m.ID, m.Revision); paused <- err }()
	pausing := wait(t, s, m.ID, "pausing")
	trace, _ := json.Marshal(pausing.CodingAttempts)
	close(g.release)
	if err = <-paused; err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMission(m.ID)
	after, _ := json.Marshal(got.CodingAttempts)
	if string(after) != string(trace) || len(got.CodingAttempts) != 1 || got.CodingAttempts[0].Status != "interrupted" || *got.CandidateGeneration != initial || r.calls != 0 {
		t.Fatal("late result altered trace/selection", got)
	}
	restart := New(s, g, Options{DataDir: e.options.DataDir, Runner: r})
	for i := 0; i < 2; i++ {
		if err = restart.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(g.roles) != 2 || r.calls != 0 {
		t.Fatal("restart replayed")
	}
	path, err := e.selectedCandidate(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(path, "src/main.txt"))
	if err != nil || string(b) != "before\n" {
		t.Fatal("late worker changed selected files")
	}
}

func TestCodingUnsupportedCreationAndRecoveryAreReadOnly(t *testing.T) {
	e, s, g, r, m := codingSetup(t)
	for _, tc := range []struct {
		v          int
		mode, tool string
	}{{9, domain.WorkspaceModeGenerationsV1, ""}, {1, "", ""}, {1, domain.WorkspaceModeControlledReadV1, domain.ToolModeControlledReadV1}, {1, domain.WorkspaceModeControlledMCPReadV2, domain.ToolModeControlledMCPReadV2}} {
		if _, err := e.Create(context.Background(), CreateRequest{CodingVersion: tc.v, WorkspaceMode: tc.mode, ToolMode: tc.tool}); err == nil {
			t.Fatal("unsupported creation accepted", tc)
		}
	}
	db := failureDB(t, e)
	for _, bad := range []string{"future", "corrupt"} {
		raw, _ := json.Marshal(m)
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		if bad == "future" {
			fields["coding_version"] = 9
		} else {
			fields["coding_attempts"] = []any{map[string]any{"unknown_future_field": "retain"}}
		}
		raw, _ = json.Marshal(fields)
		if _, err := db.Exec(`UPDATE missions SET data=? WHERE id=?`, raw, m.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Plan(context.Background(), m.ID, m.Revision); err == nil {
			t.Fatal("unsupported execution allowed")
		}
		for i := 0; i < 2; i++ {
			if err := e.Recover(context.Background()); err == nil {
				t.Fatal("unsupported recovery accepted")
			}
		}
		var after []byte
		_ = db.QueryRow(`SELECT data FROM missions WHERE id=?`, m.ID).Scan(&after)
		if string(raw) != string(after) || len(g.roles) != 0 || r.calls != 0 {
			t.Fatal("unsupported record rewritten or dispatched")
		}
		if _, err := s.GetMission(m.ID); err != nil {
			t.Fatal("unsupported record not inspectable", err)
		}
	}
}

func TestCodingAuthorityVersionAndLegacyBytes(t *testing.T) {
	e, _, _, _, m := codingSetup(t)
	base := filepath.Join(e.dir(m.ID), "base")
	versioned, err := authority(m, base, false)
	if err != nil {
		t.Fatal(err)
	}
	m.CodingVersion = 0
	old, err := authority(m, base, false)
	if err != nil {
		t.Fatal(err)
	}
	historical, err := modelAuthority(m, base, false)
	if err != nil {
		t.Fatal(err)
	}
	if versioned == old || historical != old {
		t.Fatal("coding version is not bound or legacy authority changed")
	}
}

func TestCodingOrdinaryCallClearsStaleInvocationCorrelation(t *testing.T) {
	e, s, _, _, m := codingSetup(t, plan, edit)
	_, err := e.Plan(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	// The actual ordinary call cannot turn another dispatch's ID into a receipt.
	op := &operation{id: m.ID, epoch: m.Epoch, ctx: context.Background(), modelInvocationID: "unrelated-previous-call"}
	var out editOutput
	if err = e.call(op, domain.Worker, nil, &out); err == nil {
		t.Fatal("unapproved worker accepted")
	}
	if op.modelInvocationID != "" {
		t.Fatal("stale invocation correlation retained")
	}
	got, _ := s.GetMission(m.ID)
	if got.Budget.UsedCalls != m.Budget.UsedCalls || len(got.CodingAttempts) != 0 {
		t.Fatal("unapproved call had side effects")
	}
}

func (r *codingContentRunner) VerificationSource(string) string { return "synthetic_content_check" }
func (r *codingContentRunner) Run(_ context.Context, _, candidate, _, command string) (domain.CommandResult, error) {
	r.calls++
	b, err := os.ReadFile(filepath.Join(candidate, "src/main.txt"))
	code := 1
	if string(b) == "after\n" {
		code = 0
	}
	return domain.CommandResult{Command: command, ExitCode: code, Output: "local content check"}, err
}
func codingSetup(t *testing.T, outputs ...string) (*Engine, *store.Store, *scripted, *codingContentRunner, domain.Mission) {
	t.Helper()
	e, s, g, r, old := setup(t, outputs...)
	spec := old.Spec
	spec.Research = false
	// Decode the public request so baseline RED is behavioral, not a missing symbol.
	raw, _ := json.Marshal(CreateRequest{Title: old.Title, Goal: old.Goal, RepoPath: old.RepoPath, Spec: spec, WorkspaceMode: domain.WorkspaceModeGenerationsV1})
	var request map[string]any
	_ = json.Unmarshal(raw, &request)
	request["coding_version"] = 1
	raw, _ = json.Marshal(request)
	var req CreateRequest
	_ = json.Unmarshal(raw, &req)
	m, err := e.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	runner := &codingContentRunner{fakeRunner: r}
	e.runner = runner
	return e, s, g, runner, m
}
func finishCoding(e *Engine) {
	e.mu.Lock()
	op := e.active
	e.mu.Unlock()
	if op != nil {
		<-op.finished
	}
}
func TestCodingExecutionRequiresApprovalAndPersistsCandidateTrace(t *testing.T) {
	e, s, g, r, m := codingSetup(t, plan, edit, review, review)
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	var before struct {
		Attempts []any `json:"coding_attempts"`
	}
	raw, _ := json.Marshal(m)
	_ = json.Unmarshal(raw, &before)
	if len(before.Attempts) != 0 || len(g.roles) != 1 || r.calls != 0 {
		t.Fatal("execution before explicit approval")
	}
	initial := *m.CandidateGeneration
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	got, _ := s.GetMission(m.ID)
	if got.CandidateGeneration == nil || len(got.Evidence) == 0 {
		t.Fatal("fixture did not exercise candidate/evidence")
	}
	var view struct {
		Attempts []struct {
			ID, Phase, Status string
			Before, After     *domain.GenerationRef
			VerificationID    string `json:"verification_id"`
		} `json:"coding_attempts"`
	}
	raw, _ = json.Marshal(got)
	_ = json.Unmarshal(raw, &view)
	if len(view.Attempts) == 0 {
		t.Fatal("controller did not persist execution attempts")
	}
	if len(view.Attempts) != 4 || view.Attempts[0].Phase != "worker" || view.Attempts[1].Phase != "verification" || view.Attempts[2].Phase != "qa" || view.Attempts[3].Phase != "auditor" {
		t.Fatalf("trace order: %+v", view)
	}
	seen := map[string]bool{}
	for _, a := range view.Attempts {
		if a.ID == "" || seen[a.ID] || a.Status != "succeeded" {
			t.Fatalf("invalid attempt %+v", a)
		}
		seen[a.ID] = true
	}
	if *view.Attempts[0].Before != initial || *view.Attempts[0].After != *got.CandidateGeneration || *view.Attempts[1].Before != *got.CandidateGeneration || view.Attempts[1].VerificationID != got.Evidence[0].ID {
		t.Fatal("trace not bound to selected candidate/evidence")
	}
	if view.Attempts[2].VerificationID != got.Evidence[0].ID || view.Attempts[3].VerificationID != got.Evidence[0].ID {
		t.Fatal("review trace omitted known verification")
	}
	if got.State != "awaiting_delivery" || r.calls != 1 {
		t.Fatalf("bad lifecycle: %+v", got)
	}
	b, _ := os.ReadFile(filepath.Join(m.RepoPath, "src/main.txt"))
	if string(b) != "before\n" {
		t.Fatal("source changed")
	}
}

func TestCodingVerificationCorrelationUsesChronologicalTime(t *testing.T) {
	for _, tc := range []struct {
		name, started, created string
		passed, linked         bool
	}{
		{"successful_later_fraction", "2020-01-01T00:00:00.12Z", "2020-01-01T00:00:00.123Z", true, true},
		{"failed_later_fraction", "2020-01-01T00:00:00.12Z", "2020-01-01T00:00:00.123Z", false, true},
		{"equal_different_precision", "2020-01-01T00:00:00.12Z", "2020-01-01T00:00:00.120Z", true, true},
		{"reject_older_fraction", "2020-01-01T00:00:00.123Z", "2020-01-01T00:00:00.12Z", false, false},
		{"reject_older_offset", "2020-01-01T00:00:00.12Z", "2020-01-01T01:00:00.119+01:00", false, false},
		{"reject_invalid_timestamp", "2020-01-01T00:00:00.12Z", "invalid", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, g, r, m := codingSetup(t, plan)
			if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, err := s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			// Persist the boundary state directly so timestamps are deterministic;
			// completion still uses the real controller and immutable store path.
			m, err = s.UpdateMission(m.ID, m.Revision, "fixture.verification", func(m *domain.Mission) error {
				m.CodingAttempts = append(m.CodingAttempts, domain.CodingAttempt{ID: "attempt", Epoch: m.Epoch, PlanRevision: m.Plan.Revision, Phase: "verification", Status: "running", Before: m.CandidateGeneration, StartedAt: tc.started})
				m.Evidence = append(m.Evidence, domain.Verification{ID: "verification", CandidateDigest: m.CandidateDigest, AcceptanceRevision: m.Plan.Revision, CreatedAt: tc.created, Passed: tc.passed})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			var cause error
			wantStatus, wantCode := "succeeded", ""
			if !tc.passed {
				cause = codingError("verification_failed", errors.New("candidate verification failed"))
				wantStatus, wantCode = "failed", "verification_failed"
			}
			op := &operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}
			if err = e.finishCodingAttempt(op, "attempt", "", cause); err != cause {
				t.Errorf("completion error = %v, want original %v", err, cause)
			}
			got, err := s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			a := got.CodingAttempts[0]
			wantID := ""
			if tc.linked {
				wantID = "verification"
			}
			if a.VerificationID != wantID || a.Status != wantStatus || a.FailureCode != wantCode {
				t.Fatalf("persisted correlation: id=%q status=%q code=%q; want %q/%q/%q", a.VerificationID, a.Status, a.FailureCode, wantID, wantStatus, wantCode)
			}
			if len(g.roles) != 1 || r.calls != 0 || got.Budget != m.Budget || *got.CandidateGeneration != *m.CandidateGeneration {
				t.Fatal("completion dispatched or changed candidate/budget")
			}
		})
	}
}
