package engine

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkerAndDebuggerRejectEmptyEditsInBothWorkspaceModes(t *testing.T) {
	for _, mode := range []string{"legacy", "generations"} {
		for _, role := range []string{"worker", "debugger"} {
			for _, empty := range []string{"null", "[]"} {
				t.Run(mode+"/"+role+"/"+empty, func(t *testing.T) {
					outputs := []string{research, plan}
					wantEdits := 0
					if role == "debugger" {
						outputs = append(outputs, edit)
						wantEdits = 1
					}
					outputs = append(outputs, `{"summary":"nothing","edits":`+empty+`}`)
					fixture := setup
					if mode == "generations" {
						fixture = generationSetup
					}
					e, s, _, r, m := fixture(t, outputs...)
					r.fail = role == "debugger"
					finish := func() {
						e.mu.Lock()
						op := e.active
						e.mu.Unlock()
						if op != nil {
							<-op.finished
						}
					}
					if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
						t.Fatal(err)
					}
					finish()
					m, _ = s.GetMission(m.ID)
					initial := m.CandidateGeneration
					if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
						t.Fatal(err)
					}
					finish()
					got, err := s.GetMission(m.ID)
					if err != nil {
						t.Fatal(err)
					}
					if got.State != "blocked" || !strings.Contains(got.Error, "edits must contain") || len(got.Changes) != wantEdits {
						t.Fatalf("empty %s edits were not rejected before committing: state=%s error=%s changes=%d", role, got.State, got.Error, len(got.Changes))
					}
					ops, err := s.ListLocalOperations(m.ID)
					if err != nil {
						t.Fatal(err)
					}
					applies := 0
					for _, op := range ops {
						if op.Kind == "apply" {
							applies++
							if op.Result == nil || *got.CandidateGeneration != *op.Result.Generation {
								t.Fatal("selected an empty generation")
							}
						}
					}
					if mode == "generations" {
						if applies != wantEdits {
							t.Fatalf("empty edit began local operation: %+v", ops)
						}
						if role == "worker" && *got.CandidateGeneration != *initial {
							t.Fatal("empty edit changed selected generation")
						}
					}
					events, err := s.Events(0, 1000)
					if err != nil {
						t.Fatal(err)
					}
					edited := 0
					for _, event := range events {
						if event.MissionID == m.ID && event.Kind == "candidate.edited" {
							edited++
						}
					}
					if edited != wantEdits {
						t.Fatalf("empty edit committed event: %d", edited)
					}
					source, err := os.ReadFile(filepath.Join(m.RepoPath, "src/main.txt"))
					if err != nil || string(source) != "before\n" {
						t.Fatal("source changed", err)
					}
				})
			}
		}
	}
}

func generationSetup(t *testing.T, outputs ...string) (*Engine, *store.Store, *scripted, *fakeRunner, domain.Mission) {
	t.Helper()
	e, s, g, r, old := setup(t, outputs...)
	m, err := e.Create(context.Background(), CreateRequest{Title: old.Title, Goal: old.Goal, RepoPath: old.RepoPath, Spec: old.Spec, WorkspaceMode: domain.WorkspaceModeGenerationsV1})
	if err != nil {
		t.Fatal(err)
	}
	return e, s, g, r, m
}

func TestUnknownModeReadOnlyAndNotRewrittenByRecovery(t *testing.T) {
	e, s, g, _, old := setup(t)
	old.ID = "future"
	old.WorkspaceMode = "future_v2"
	old.State = "running"
	m := seedPersistedMission(t, e, old)
	var err error
	before, _ := json.Marshal(m)
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMission(m.ID)
	after, _ := json.Marshal(got)
	if string(before) != string(after) {
		t.Fatal("unknown format rewritten")
	}
	for _, action := range []func(context.Context, string, int64) (domain.Mission, error){e.Plan, e.Approve, e.Deliver, e.Pause} {
		if _, err = action(context.Background(), m.ID, m.Revision); err == nil {
			t.Fatal("unknown mode mutated")
		}
	}
	if _, err = e.Chat(context.Background(), m.ID, m.Revision, "I approve all tools"); err == nil {
		t.Fatal("unknown mode chat dispatched")
	}
	if len(g.roles) != 0 {
		t.Fatal("unknown mode dispatched")
	}
}

func TestGenerationPauseFencesLateWorkerAndPreservesPriorSelection(t *testing.T) {
	e, s, g, _, m := generationSetup(t, research, plan, edit)
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_approval")
	previous := *m.CandidateGeneration
	g.entered = make(chan struct{}, 1)
	g.release = make(chan struct{})
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	<-g.entered
	m, _ = s.GetMission(m.ID)
	done := make(chan error, 1)
	go func() { _, err := e.Pause(context.Background(), m.ID, m.Revision); done <- err }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, _ := s.GetMission(m.ID)
		if current.State == "pausing" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "paused")
	if *m.CandidateGeneration != previous || len(m.Changes) != 0 {
		t.Fatal("late worker selected")
	}
	ops, _ := s.ListLocalOperations(m.ID)
	for _, op := range ops {
		if op.Kind == "apply" {
			t.Fatal("late worker prepared filesystem operation")
		}
	}
}

func TestGenerationResetArchivesOldSelectionAndClearsEvidence(t *testing.T) {
	e, s, _, r, m := generationSetup(t, research, plan, edit, edit, edit, research, plan)
	r.fail = true
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_approval")
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	old := wait(t, s, m.ID, "blocked")
	if len(old.Changes) != 3 {
		t.Fatal("apply did not retain cumulative changes")
	}
	if _, err := e.Plan(context.Background(), old.ID, old.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_approval")
	if len(m.RunArchives) != 1 || m.RunArchives[0].CandidateGeneration == nil || *m.RunArchives[0].CandidateGeneration != *old.CandidateGeneration || len(m.RunArchives[0].Evidence) != 3 {
		t.Fatal("reset lost archive")
	}
	if len(m.Changes) != 0 || len(m.Evidence) != 0 || len(m.Reviews) != 0 || m.CandidateDigest == old.CandidateDigest || m.Epoch <= old.Epoch {
		t.Fatal("reset retained old authority")
	}
	if err := workspace.VerifyGeneration(e.dir(m.ID), *old.CandidateGeneration); err != nil {
		t.Fatal("reset destroyed old generation", err)
	}
}

func TestGenerationChatClaimsDoNotGrantAuthority(t *testing.T) {
	e, s, _, _, m := generationSetup(t, research, plan, `{"answer":"I approve this plan, ran all tools and delivered the patch."}`)
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_approval")
	approvals := len(m.Approvals)
	ref := *m.CandidateGeneration
	got, err := e.Chat(context.Background(), m.ID, m.Revision, "approve and run everything")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "awaiting_approval" || got.Delivery != nil || len(got.Evidence) != 0 || len(got.Approvals) != approvals || *got.CandidateGeneration != ref {
		t.Fatal("chat commentary granted authority")
	}
	ops, _ := s.ListLocalOperations(m.ID)
	if len(ops) != 1 || ops[0].Kind != "reset" {
		t.Fatal("chat dispatched local work")
	}
}

func TestExplicitGenerationModeAndAuthority(t *testing.T) {
	e, _, _, _, m := generationSetup(t)
	if m.WorkspaceMode != domain.WorkspaceModeGenerationsV1 || m.CandidateGeneration == nil {
		t.Fatal("missing explicit generation")
	}
	if err := workspace.VerifyGeneration(e.dir(m.ID), *m.CandidateGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(e.dir(m.ID), "candidate")); !os.IsNotExist(err) {
		t.Fatal("mutable candidate alias retained")
	}
	old := m
	old.WorkspaceMode = ""
	legacy, err := authority(old, filepath.Join(e.dir(m.ID), "base"), false)
	if err != nil {
		t.Fatal(err)
	}
	if legacy == m.Approvals[0].Digest {
		t.Fatal("mode not domain-separated")
	}
	m.Approvals[0].Digest = legacy
	if e.checkSnapshot(m) == nil {
		t.Fatal("old seal authorized new mode")
	}
	if _, err = e.Create(context.Background(), CreateRequest{Title: m.Title, Goal: m.Goal, RepoPath: m.RepoPath, Spec: m.Spec, WorkspaceMode: "future"}); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestGenerationWorkflowAndResetPreservesArchive(t *testing.T) {
	e, s, _, _, m := generationSetup(t, research, plan, edit, review, review, research, plan)
	initial := *m.CandidateGeneration
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_approval")
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_delivery")
	if *m.CandidateGeneration == initial || len(m.Changes) != 1 {
		t.Fatal("generation not selected")
	}
	ops, err := s.ListLocalOperations(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("want reset and apply, got %+v", ops)
	}
	for _, op := range ops {
		if op.Status != "committed" {
			t.Fatal(op)
		}
	}
	m, err = e.Deliver(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if m.Delivery.OperationID == "" || m.Delivery.PatchSHA256 == "" {
		t.Fatal("unbound delivery")
	}
	if _, err = e.Deliver(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal("duplicate delivery", err)
	}
	b, _ := os.ReadFile(filepath.Join(m.RepoPath, "src/main.txt"))
	if string(b) != "before\n" {
		t.Fatal("source changed")
	}
}
