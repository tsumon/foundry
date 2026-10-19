package engine

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/gitinspect"
	"foundry/internal/skills"
	"foundry/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Local fake gateway/verifier; the repository and SQLite are real temporary fixtures.
func controlledRequest(t *testing.T, s *store.Store, old domain.Mission) CreateRequest {
	t.Helper()
	repo, err := s.SaveGitRepository(domain.GitRepository{ID: "source", Name: "fixture", Path: old.RepoPath}, 0)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := gitinspect.Inspect(context.Background(), repo.Path)
	if err != nil {
		t.Fatal(err)
	}
	observation.ID = "observed"
	observation.RepositoryID = repo.ID
	observation.RepositoryRevision = repo.Revision
	if _, err = s.SaveGitObservation(observation); err != nil {
		t.Fatal(err)
	}
	return CreateRequest{Title: old.Title, Goal: old.Goal, RepoPath: old.RepoPath, Spec: old.Spec, WorkspaceMode: domain.WorkspaceModeControlledReadV1, ToolMode: domain.ToolModeControlledReadV1, CapabilitySelection: &domain.CapabilitySelection{RepositoryID: repo.ID, RepositoryRevision: repo.Revision, ObservationID: observation.ID}}
}

func TestControlledCreationBindingAndExplicitConsentBeforeReset(t *testing.T) {
	e, s, g, _, old := setup(t)
	request := controlledRequest(t, s, old)
	m, err := e.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if m.TaskBindings == nil || m.TaskBindings.Git.BaseCommit != m.BaseCommit || m.CandidateGeneration == nil {
		t.Fatal("missing frozen source/generation")
	}
	before, _ := json.Marshal(m)
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("legacy plan authorized controlled read")
	}
	if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, false); err == nil {
		t.Fatal("missing explicit consent accepted")
	}
	after, _ := s.GetMission(m.ID)
	raw, _ := json.Marshal(after)
	if string(before) != string(raw) {
		t.Fatal("denied plan reset mission")
	}
	operations, _ := s.ListLocalOperations(m.ID)
	if len(operations) != 0 || len(g.roles) != 0 {
		t.Fatal("denied plan dispatched")
	}
}

func TestControlledCreationDisabledAndInvalidSelections(t *testing.T) {
	e, s, _, _, old := setup(t)
	request := controlledRequest(t, s, old)
	e.options.DisableControlledReadCreation = true
	request.RepoPath = "/does-not-exist"
	if _, err := e.Create(context.Background(), request); err == nil || e.ControlledReadCreationEnabled() {
		t.Fatal("creation gate absent")
	}
	e.options.DisableControlledReadCreation = false
	request.RepoPath = old.RepoPath
	for _, mutate := range []func(*CreateRequest){
		func(r *CreateRequest) { r.ToolMode = "future" },
		func(r *CreateRequest) { r.ToolMode = "" },
		func(r *CreateRequest) { r.Spec.Research = false },
		func(r *CreateRequest) { r.WorkspaceMode = domain.WorkspaceModeGenerationsV1 },
		func(r *CreateRequest) { r.CapabilitySelection = nil },
		func(r *CreateRequest) { r.CapabilitySelection.RepositoryRevision++ },
		func(r *CreateRequest) { r.CapabilitySelection.ObservationID = "missing" },
		func(r *CreateRequest) {
			r.CapabilitySelection.Skills = []domain.SkillBinding{{ID: "missing", Revision: 1, Role: domain.Researcher}}
		},
	} {
		copyRequest := request
		selection := *request.CapabilitySelection
		copyRequest.CapabilitySelection = &selection
		mutate(&copyRequest)
		if _, err := e.Create(context.Background(), copyRequest); err == nil {
			t.Fatal("invalid binding accepted")
		}
	}
}

func TestControlledCreationRejectsStaleObservationAndCombinedSkillLimit(t *testing.T) {
	e, s, _, _, old := setup(t)
	request := controlledRequest(t, s, old)
	observation, err := s.GetGitObservation("observed")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id     string
		mutate func(*domain.GitObservation)
	}{
		{"wrong-head", func(o *domain.GitObservation) { o.HEAD = strings.Repeat("a", 40) }},
		{"wrong-root", func(o *domain.GitObservation) { o.Root = "/tmp/not-source" }},
		{"unborn", func(o *domain.GitObservation) { o.Unborn = true }},
		{"failed", func(o *domain.GitObservation) { o.Status = "failed"; o.ErrorCode = "inspection_failed" }},
	} {
		copyObservation := observation
		copyObservation.ID = tc.id
		tc.mutate(&copyObservation)
		if _, err = s.SaveGitObservation(copyObservation); err != nil {
			t.Fatal(err)
		}
		request.CapabilitySelection.ObservationID = tc.id
		if _, err = e.Create(context.Background(), request); err == nil {
			t.Fatal("invalid observation accepted", tc.id)
		}
	}
	request.CapabilitySelection.ObservationID = observation.ID
	files := []domain.SkillFile{{Path: "SKILL.md", Content: "---\nname: large\ndescription: Large fake context\n---\n"}, {Path: "references/a.txt", Content: strings.Repeat("x", 60<<10)}, {Path: "references/b.txt", Content: strings.Repeat("x", 60<<10)}, {Path: "references/c.txt", Content: strings.Repeat("x", 20<<10)}}
	large, err := skills.Parse(files, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	large.ID = "large"
	large, err = s.SaveSkillRevision(large, 0)
	if err != nil {
		t.Fatal(err)
	}
	request.CapabilitySelection.Skills = []domain.SkillBinding{{ID: large.ID, Revision: 1, Role: domain.Main}, {ID: large.ID, Revision: 1, Role: domain.Researcher}}
	if _, err = e.Create(context.Background(), request); err == nil || !strings.Contains(err.Error(), "256 KiB") {
		t.Fatal("combined Skill disclosure limit", err)
	}
}

func TestControlledCreationGateDoesNotSnapshotOrBlockLegacyHistory(t *testing.T) {
	e, s, _, _, old := setup(t)
	request := controlledRequest(t, s, old)
	m, err := e.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(filepath.Join(e.options.DataDir, "missions"))
	if err != nil {
		t.Fatal(err)
	}
	e.options.DisableControlledReadCreation = true
	if _, err = e.Create(context.Background(), request); err == nil {
		t.Fatal("creation not disabled")
	}
	after, err := os.ReadDir(filepath.Join(e.options.DataDir, "missions"))
	if err != nil || len(after) != len(before) {
		t.Fatal("disabled creation wrote snapshot")
	}
	if _, err = e.Create(context.Background(), CreateRequest{Title: old.Title, Goal: old.Goal, RepoPath: old.RepoPath, Spec: old.Spec}); err != nil {
		t.Fatal("legacy creation disabled", err)
	}
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMission(m.ID)
	if err != nil || got.TaskBindings == nil {
		t.Fatal("gate hid history", err)
	}
}
