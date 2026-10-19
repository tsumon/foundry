package capability

import (
	"errors"
	"foundry/internal/domain"
	"foundry/internal/skills"
)

type SkillInput struct {
	ID               string             `json:"id"`
	ExpectedRevision int64              `json:"expected_revision"`
	Source           string             `json:"source"`
	Files            []domain.SkillFile `json:"files"`
}

func (s *Service) SaveSkill(in SkillInput) (domain.SkillRevision, error) {
	if in.ExpectedRevision < 0 || (in.ID == "" && in.ExpectedRevision != 0) {
		return domain.SkillRevision{}, domain.ErrInvalidCapability
	}
	revision, err := skills.Parse(in.Files, in.Source)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	revision.ID = in.ID
	if revision.ID == "" {
		revision.ID, err = newID()
		if err != nil {
			return domain.SkillRevision{}, err
		}
	}
	return s.store.SaveSkillRevision(revision, in.ExpectedRevision)
}

func verifySkill(v domain.SkillRevision) error {
	if skills.Verify(v) != nil {
		return errors.New("stored skill integrity check failed")
	}
	return nil
}

func (s *Service) SkillRevision(id string, revision int64) (domain.SkillRevision, error) {
	if revision < 1 {
		return domain.SkillRevision{}, domain.ErrInvalidCapability
	}
	v, err := s.store.GetSkillRevision(id, revision)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	if err = verifySkill(v); err != nil {
		return domain.SkillRevision{}, err
	}
	return v, nil
}
