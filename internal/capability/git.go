package capability

import (
	"context"
	"foundry/internal/domain"
	"foundry/internal/gitinspect"
	"foundry/internal/store"
	"strings"
	"unicode"
)

type GitInput struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Name             string `json:"name"`
	Path             string `json:"path"`
	ProjectID        string `json:"project_id"`
}

// SaveGit registers metadata only: no stat, config read, or subprocess.
func (s *Service) SaveGit(in GitInput) (domain.GitRepository, error) {
	if in.ExpectedRevision < 0 || (in.ID == "" && in.ExpectedRevision != 0) || strings.ContainsFunc(in.Path, unicode.IsControl) {
		return domain.GitRepository{}, domain.ErrInvalidCapability
	}
	var err error
	if in.ID == "" {
		in.ID, err = newID()
		if err != nil {
			return domain.GitRepository{}, err
		}
	}
	return s.store.SaveGitRepository(domain.GitRepository{ID: in.ID, Name: in.Name, Path: in.Path, ProjectID: in.ProjectID}, in.ExpectedRevision)
}

func (s *Service) InspectGit(ctx context.Context, id string, expectedRevision int64, consent bool) (domain.GitObservation, error) {
	if !consent || expectedRevision < 1 {
		return domain.GitObservation{}, domain.ErrInvalidCapability
	}
	repository, err := s.store.GetGitRepository(id)
	if err != nil {
		return domain.GitObservation{}, err
	}
	if repository.Revision != expectedRevision {
		return domain.GitObservation{}, store.ErrConflict
	}
	observationID, err := newID()
	if err != nil {
		return domain.GitObservation{}, err
	}
	// Failed inspections are observations with fixed safe categories, not raw
	// process errors. Persistence checks the repository revision again after I/O.
	observation, _ := gitinspect.Inspect(ctx, repository.Path)
	observation.ID = observationID
	observation.RepositoryID = id
	observation.RepositoryRevision = expectedRevision
	return s.store.SaveGitObservation(observation)
}
