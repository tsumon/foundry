package domain

type CatalogMeta struct {
	ID        string `json:"id"`
	Revision  int64  `json:"revision"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type Project struct {
	CatalogMeta
	Name        string `json:"name"`
	Description string `json:"description"`
}

type DeliveryTarget struct {
	CatalogMeta
	ProjectID    string `json:"project_id"`
	Name         string `json:"name"`
	Platform     string `json:"platform"`
	Format       string `json:"format"`
	Architecture string `json:"architecture"`
	Distribution string `json:"distribution"`
}

type Environment struct {
	CatalogMeta
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Tier      string `json:"tier"`
}

type Resource struct {
	CatalogMeta
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	OwnerProjectID *string `json:"owner_project_id"`
	Provider       string  `json:"provider"`
	Account        string  `json:"account"`
	Region         string  `json:"region"`
	ExternalID     string  `json:"external_id"`
	Protected      bool    `json:"protected"`
	Retention      string  `json:"retention"`
}

type ResourceBinding struct {
	CatalogMeta
	ProjectID     string `json:"project_id"`
	EnvironmentID string `json:"environment_id"`
	ResourceID    string `json:"resource_id"`
	Purpose       string `json:"purpose"`
}

type MissionProjectLink struct {
	MissionID string `json:"mission_id"`
	ProjectID string `json:"project_id"`
	LinkedAt  string `json:"linked_at"`
}

type CatalogSnapshot struct {
	SchemaVersion int64                `json:"schema_version"`
	Revision      int64                `json:"revision"`
	Projects      []Project            `json:"projects"`
	Targets       []DeliveryTarget     `json:"targets"`
	Environments  []Environment        `json:"environments"`
	Resources     []Resource           `json:"resources"`
	Bindings      []ResourceBinding    `json:"bindings"`
	MissionLinks  []MissionProjectLink `json:"mission_links"`
}

type CatalogEvent struct {
	Seq             int64    `json:"seq"`
	CatalogRevision int64    `json:"catalog_revision"`
	Kind            string   `json:"kind"`
	EntityIDs       []string `json:"entity_ids"`
	CreatedAt       string   `json:"created_at"`
}

func EmptyCatalog() CatalogSnapshot {
	return CatalogSnapshot{
		SchemaVersion: 1,
		Revision:      0,
		Projects:      []Project{},
		Targets:       []DeliveryTarget{},
		Environments:  []Environment{},
		Resources:     []Resource{},
		Bindings:      []ResourceBinding{},
		MissionLinks:  []MissionProjectLink{},
	}
}
