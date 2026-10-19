package engine

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/gitinspect"
	"foundry/internal/skills"
	"path/filepath"
)

func (e *Engine) ControlledReadCreationEnabled() bool {
	return !e.options.DisableControlledReadCreation
}

func (e *Engine) resolveControlledBindings(ctx context.Context, r CreateRequest) (*domain.TaskBindings, error) {
	if r.ToolMode == "" {
		if r.CapabilitySelection != nil || r.WorkspaceMode == domain.WorkspaceModeControlledReadV1 {
			return nil, errors.New("capability selection requires explicit controlled read mode")
		}
		return nil, nil
	}
	if !e.ControlledReadCreationEnabled() {
		return nil, errors.New("controlled read creation disabled")
	}
	if (!(r.ToolMode == domain.ToolModeControlledReadV1 && r.WorkspaceMode == domain.WorkspaceModeControlledReadV1) && !domain.IsResearchFlowV2(r.WorkspaceMode, r.ToolMode)) || !r.Spec.Research || r.CapabilitySelection == nil {
		return nil, errors.New("controlled read requires research, fenced generations and explicit selection")
	}
	selection := r.CapabilitySelection
	repo, err := e.s.GetGitRepository(selection.RepositoryID)
	if err != nil {
		return nil, err
	}
	if repo.Revision != selection.RepositoryRevision {
		return nil, errors.New("registered repository revision changed")
	}
	observation, err := e.s.GetGitObservation(selection.ObservationID)
	if err != nil {
		return nil, err
	}
	if observation.RepositoryID != repo.ID || observation.RepositoryRevision != repo.Revision || observation.Status != "succeeded" || observation.Unborn || observation.HEAD == "" || observation.WorkingTreeStatus != "not_checked" {
		return nil, errors.New("successful committed source observation required")
	}
	actual, err := gitinspect.Inspect(ctx, repo.Path)
	if err != nil {
		return nil, err
	}
	requested, err := filepath.EvalSymlinks(r.RepoPath)
	if err != nil {
		return nil, err
	}
	if actual.Root != observation.Root || actual.HEAD != observation.HEAD || requested != actual.Root {
		return nil, errors.New("registered source root or HEAD changed")
	}
	bindings := &domain.TaskBindings{Git: domain.GitSourceBinding{RepositoryID: repo.ID, RepositoryRevision: repo.Revision, ObservationID: observation.ID, Root: actual.Root, BaseCommit: actual.HEAD}, Skills: []domain.SkillBinding{}, PolicyVersion: "repo_read_v1", Tools: []string{"repo.list", "repo.read", "repo.search"}, Budget: domain.FixedReadBudget()}
	if len(selection.Skills) > 3 {
		return nil, errors.New("at most three Skill bindings")
	}
	for _, selected := range selection.Skills {
		skill, err := e.s.GetSkillRevision(selected.ID, selected.Revision)
		if err != nil {
			return nil, err
		}
		if err = skills.Verify(skill); err != nil {
			return nil, err
		}
		bindings.Skills = append(bindings.Skills, domain.SkillBinding{ID: skill.ID, Revision: skill.Revision, Name: skill.Name, Digest: skill.Digest, Role: selected.Role})
	}
	if _, err = domain.TaskBindingDigest(*bindings); err != nil {
		return nil, err
	}
	if _, err = e.boundSkills(bindings, ""); err != nil {
		return nil, err
	}
	return bindings, nil
}

func (e *Engine) boundSkills(bindings *domain.TaskBindings, role domain.Role) ([]domain.SkillRevision, error) {
	// Preserve the existing R3 error contract. R4 uses the same validation with
	// the direct Store reader so recovery can distinguish SQL infrastructure.
	return validateBoundSkills(bindings, role, func(id string, revision int64) (domain.SkillRevision, error) {
		skill, err := e.s.GetSkillRevision(id, revision)
		if err != nil {
			return domain.SkillRevision{}, errors.New("bound Skill exact version unavailable")
		}
		return skill, nil
	})
}

func validateBoundSkills(bindings *domain.TaskBindings, role domain.Role, read func(string, int64) (domain.SkillRevision, error)) ([]domain.SkillRevision, error) {
	if bindings == nil {
		return nil, errors.New("missing task bindings")
	}
	out := []domain.SkillRevision{}
	total := 0
	for _, binding := range bindings.Skills {
		skill, err := read(binding.ID, binding.Revision)
		if err != nil {
			return nil, err
		}
		if skill.ID != binding.ID || skill.Revision != binding.Revision || skill.Digest != binding.Digest || skill.Name != binding.Name || skills.Verify(skill) != nil {
			return nil, errors.New("bound Skill integrity changed")
		}
		for _, file := range skill.Files {
			total += len(file.Content)
		}
		if total > 256<<10 {
			return nil, errors.New("combined Skill content exceeds 256 KiB")
		}
		if role == "" || role == binding.Role {
			out = append(out, skill)
		}
	}
	return out, nil
}

// Called under Engine.mu after generation reset and context approval commit.
func (e *Engine) grantPlanningRead(m domain.Mission) (domain.Mission, error) {
	binding, err := controlledBindingDigest(m)
	if err != nil {
		return m, err
	}
	auth, err := authority(m, filepath.Join(e.dir(m.ID), "base"), false)
	if err != nil {
		return m, err
	}
	id, err := opaque()
	if err != nil {
		return m, err
	}
	session, err := opaque()
	if err != nil {
		return m, err
	}
	updated, _, err := e.s.CreateReadGrant(domain.ReadGrant{ID: id, MissionID: m.ID, Epoch: m.Epoch, SessionID: session, Role: domain.Researcher, RequiredState: "planning", BindingDigest: binding, AuthorityDigest: auth}, m.Revision)
	if err != nil {
		blocked, saveErr := e.s.UpdateMission(m.ID, m.Revision, "read_grant.failed", func(current *domain.Mission) error {
			current.State = "blocked"
			current.Error = "read grant persistence failed; explicitly replan"
			return nil
		})
		if saveErr == nil {
			m = blocked
		}
		return m, errors.New("read grant persistence failed; no dispatch started")
	}
	return updated, nil
}
