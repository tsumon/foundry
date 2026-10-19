package catalog

import (
	"crypto/rand"
	"encoding/hex"

	"foundry/internal/domain"
	"foundry/internal/store"
)

type Service struct{ store *store.Store }

func New(s *store.Store) *Service                            { return &Service{store: s} }
func (s *Service) Snapshot() (domain.CatalogSnapshot, error) { return s.store.Catalog() }
func (s *Service) Events(after int64, limit int) ([]domain.CatalogEvent, error) {
	return s.store.CatalogEvents(after, limit)
}

type mutation func(*domain.CatalogSnapshot) ([]string, error)

func (s *Service) Execute(cmd Command) (domain.CatalogSnapshot, error) {
	if cmd.ExpectedRevision < 0 {
		return domain.CatalogSnapshot{}, invalidInput()
	}
	mutate, err := commandMutation(cmd)
	if err != nil {
		return domain.CatalogSnapshot{}, err
	}
	return s.store.UpdateCatalog(cmd.ExpectedRevision, cmd.Kind, mutate)
}

func commandMutation(cmd Command) (mutation, error) {
	switch cmd.Kind {
	case "project.create":
		var input projectCreateInput
		if err := decodeInput(cmd.Input, &input); err != nil {
			return nil, err
		}
		return func(c *domain.CatalogSnapshot) ([]string, error) {
			meta, err := newMeta()
			if err != nil {
				return nil, err
			}
			c.Projects = append(c.Projects, domain.Project{CatalogMeta: meta, Name: input.Name, Description: input.Description})
			return []string{meta.ID}, nil
		}, nil
	case "project.update":
		var input projectUpdateInput
		if err := decodeInput(cmd.Input, &input); err != nil {
			return nil, err
		}
		return func(c *domain.CatalogSnapshot) ([]string, error) {
			for i := range c.Projects {
				p := &c.Projects[i]
				if p.ID == input.ID {
					if p.Name == input.Name && p.Description == input.Description {
						return nil, nil
					}
					p.Name = input.Name
					p.Description = input.Description
					return []string{p.ID}, nil
				}
			}
			return nil, store.ErrNotFound
		}, nil
	case "target.create":
		var input targetCreateInput
		if err := decodeInput(cmd.Input, &input); err != nil {
			return nil, err
		}
		return func(c *domain.CatalogSnapshot) ([]string, error) {
			if !hasProject(c, input.ProjectID) {
				return nil, store.ErrNotFound
			}
			meta, err := newMeta()
			if err != nil {
				return nil, err
			}
			c.Targets = append(c.Targets, domain.DeliveryTarget{CatalogMeta: meta, ProjectID: input.ProjectID, Name: input.Name, Platform: input.Platform, Format: input.Format, Architecture: input.Architecture, Distribution: input.Distribution})
			return []string{meta.ID}, nil
		}, nil
	case "target.rename", "environment.rename", "resource.rename":
		var input renameInput
		if err := decodeInput(cmd.Input, &input); err != nil {
			return nil, err
		}
		return func(c *domain.CatalogSnapshot) ([]string, error) {
			var name *string
			switch cmd.Kind {
			case "target.rename":
				for i := range c.Targets {
					if c.Targets[i].ID == input.ID {
						name = &c.Targets[i].Name
						break
					}
				}
			case "environment.rename":
				for i := range c.Environments {
					if c.Environments[i].ID == input.ID {
						name = &c.Environments[i].Name
						break
					}
				}
			case "resource.rename":
				for i := range c.Resources {
					if c.Resources[i].ID == input.ID {
						name = &c.Resources[i].Name
						break
					}
				}
			}
			if name == nil {
				return nil, store.ErrNotFound
			}
			if *name == input.Name {
				return nil, nil
			}
			*name = input.Name
			return []string{input.ID}, nil
		}, nil
	case "environment.create":
		var input environmentCreateInput
		if err := decodeInput(cmd.Input, &input); err != nil {
			return nil, err
		}
		return func(c *domain.CatalogSnapshot) ([]string, error) {
			if !hasProject(c, input.ProjectID) {
				return nil, store.ErrNotFound
			}
			meta, err := newMeta()
			if err != nil {
				return nil, err
			}
			c.Environments = append(c.Environments, domain.Environment{CatalogMeta: meta, ProjectID: input.ProjectID, Name: input.Name, Tier: input.Tier})
			return []string{meta.ID}, nil
		}, nil
	case "resource.create":
		var input resourceCreateInput
		if err := decodeInput(cmd.Input, &input); err != nil {
			return nil, err
		}
		return func(c *domain.CatalogSnapshot) ([]string, error) {
			if input.OwnerProjectID != nil && !hasProject(c, *input.OwnerProjectID) {
				return nil, store.ErrNotFound
			}
			if input.Provider != "manual" && input.Account != "" && input.Region != "" && input.ExternalID != "" {
				for _, r := range c.Resources {
					if r.Provider == input.Provider && r.Account == input.Account && r.Region == input.Region && r.ExternalID == input.ExternalID {
						return nil, store.ErrConflict
					}
				}
			}
			meta, err := newMeta()
			if err != nil {
				return nil, err
			}
			c.Resources = append(c.Resources, domain.Resource{CatalogMeta: meta, Name: input.Name, Kind: input.Kind, OwnerProjectID: input.OwnerProjectID, Provider: input.Provider, Account: input.Account, Region: input.Region, ExternalID: input.ExternalID, Protected: true, Retention: "retain"})
			return []string{meta.ID}, nil
		}, nil
	case "resource.bind":
		var input resourceBindInput
		if err := decodeInput(cmd.Input, &input); err != nil {
			return nil, err
		}
		return func(c *domain.CatalogSnapshot) ([]string, error) {
			if !hasProject(c, input.ProjectID) {
				return nil, store.ErrNotFound
			}
			environmentFound, resourceFound := false, false
			for _, e := range c.Environments {
				if e.ID == input.EnvironmentID {
					environmentFound = true
					if e.ProjectID != input.ProjectID {
						return nil, invalidInput()
					}
				}
			}
			for _, r := range c.Resources {
				if r.ID == input.ResourceID {
					resourceFound = true
				}
			}
			if !environmentFound || !resourceFound {
				return nil, store.ErrNotFound
			}
			for _, b := range c.Bindings {
				if b.ProjectID == input.ProjectID && b.EnvironmentID == input.EnvironmentID && b.Purpose == input.Purpose {
					if b.ResourceID == input.ResourceID {
						return nil, nil
					}
					return nil, store.ErrConflict
				}
			}
			meta, err := newMeta()
			if err != nil {
				return nil, err
			}
			c.Bindings = append(c.Bindings, domain.ResourceBinding{CatalogMeta: meta, ProjectID: input.ProjectID, EnvironmentID: input.EnvironmentID, ResourceID: input.ResourceID, Purpose: input.Purpose})
			return []string{meta.ID}, nil
		}, nil
	case "resource.unbind":
		var input resourceUnbindInput
		if err := decodeInput(cmd.Input, &input); err != nil {
			return nil, err
		}
		return func(c *domain.CatalogSnapshot) ([]string, error) {
			for i, b := range c.Bindings {
				if b.ID == input.ID {
					c.Bindings = append(c.Bindings[:i], c.Bindings[i+1:]...)
					return []string{b.ID}, nil
				}
			}
			return nil, store.ErrNotFound
		}, nil
	case "mission.link", "mission.unlink":
		var input missionLinkInput
		if err := decodeInput(cmd.Input, &input); err != nil {
			return nil, err
		}
		return func(c *domain.CatalogSnapshot) ([]string, error) {
			if cmd.Kind == "mission.link" && !hasProject(c, input.ProjectID) {
				return nil, store.ErrNotFound
			}
			for i, link := range c.MissionLinks {
				if link.MissionID == input.MissionID {
					if link.ProjectID != input.ProjectID {
						return nil, store.ErrConflict
					}
					if cmd.Kind == "mission.link" {
						return nil, nil
					}
					c.MissionLinks = append(c.MissionLinks[:i], c.MissionLinks[i+1:]...)
					return []string{input.MissionID}, nil
				}
			}
			if cmd.Kind == "mission.unlink" {
				return nil, store.ErrNotFound
			}
			c.MissionLinks = append(c.MissionLinks, domain.MissionProjectLink{MissionID: input.MissionID, ProjectID: input.ProjectID})
			return []string{input.MissionID}, nil
		}, nil
	case "mission.relink":
		var input missionRelinkInput
		if err := decodeInput(cmd.Input, &input); err != nil {
			return nil, err
		}
		return func(c *domain.CatalogSnapshot) ([]string, error) {
			if !hasProject(c, input.ToProjectID) {
				return nil, store.ErrNotFound
			}
			for i := range c.MissionLinks {
				link := &c.MissionLinks[i]
				if link.MissionID == input.MissionID {
					if link.ProjectID != input.FromProjectID {
						return nil, store.ErrConflict
					}
					if link.ProjectID == input.ToProjectID {
						return nil, nil
					}
					link.ProjectID = input.ToProjectID
					link.LinkedAt = ""
					return []string{link.MissionID}, nil
				}
			}
			return nil, store.ErrNotFound
		}, nil
	default:
		return nil, ErrUnknownCommand
	}
}

func newMeta() (domain.CatalogMeta, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return domain.CatalogMeta{}, err
	}
	return domain.CatalogMeta{ID: hex.EncodeToString(id[:])}, nil
}
func hasProject(c *domain.CatalogSnapshot, id string) bool {
	for _, p := range c.Projects {
		if p.ID == id {
			return true
		}
	}
	return false
}
