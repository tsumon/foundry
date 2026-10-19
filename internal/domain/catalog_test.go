package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const catalogTestTime = "2026-10-18T00:00:00Z"

func testCatalogMeta(id string) CatalogMeta {
	return CatalogMeta{
		ID:        id,
		Revision:  1,
		CreatedAt: catalogTestTime,
		UpdatedAt: catalogTestTime,
	}
}

func testCatalog() CatalogSnapshot {
	c := EmptyCatalog()
	c.Revision = 1
	c.Projects = append(c.Projects,
		Project{CatalogMeta: testCatalogMeta("p1"), Name: "Product One", Description: ""},
		Project{CatalogMeta: testCatalogMeta("p2"), Name: "Product Two", Description: ""},
	)
	c.Targets = append(c.Targets, DeliveryTarget{
		CatalogMeta: testCatalogMeta("t1"), ProjectID: "p1", Name: "iOS",
		Platform: "ios", Format: "ipa", Architecture: "arm64", Distribution: "testflight",
	})
	c.Environments = append(c.Environments,
		Environment{CatalogMeta: testCatalogMeta("e1"), ProjectID: "p1", Name: "Production", Tier: "production"},
		Environment{CatalogMeta: testCatalogMeta("e2"), ProjectID: "p2", Name: "Development", Tier: "dev"},
	)
	owner := "p1"
	c.Resources = append(c.Resources, Resource{
		CatalogMeta: testCatalogMeta("r1"), Name: "Primary database", Kind: "database",
		OwnerProjectID: &owner, Provider: "aws", Account: "account-1", Region: "us-east-1",
		ExternalID: "database/main", Protected: true, Retention: "retain",
	})
	c.Bindings = append(c.Bindings, ResourceBinding{
		CatalogMeta: testCatalogMeta("b1"), ProjectID: "p1", EnvironmentID: "e1",
		ResourceID: "r1", Purpose: "primary-db",
	})
	c.MissionLinks = append(c.MissionLinks, MissionProjectLink{
		MissionID: "m1", ProjectID: "p1", LinkedAt: catalogTestTime,
	})
	return c
}

func cloneCatalog(t *testing.T, c CatalogSnapshot) CatalogSnapshot {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var clone CatalogSnapshot
	if err := json.Unmarshal(b, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func requireInvalidCatalog(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("catalog accepted")
	}
	if !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("error %v does not wrap ErrInvalidCatalog", err)
	}
}

func TestCatalogIPAIsNotIPK(t *testing.T) {
	c := EmptyCatalog()
	c.Revision = 1
	c.Projects = append(c.Projects, Project{
		CatalogMeta: testCatalogMeta("p1"), Name: "产品", Description: "",
	})
	c.Targets = append(c.Targets, DeliveryTarget{
		CatalogMeta: testCatalogMeta("t1"), ProjectID: "p1",
		Name: "iOS", Platform: "ios", Format: "ipa",
		Architecture: "arm64", Distribution: "testflight",
	})
	if err := ValidateCatalog(c); err != nil {
		t.Fatal(err)
	}
	c.Targets[0].Format = "ipk"
	requireInvalidCatalog(t, ValidateCatalog(c))
}

func TestCatalogEmptyCollectionsEncodeAsArrays(t *testing.T) {
	b, err := json.Marshal(EmptyCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("null")) {
		t.Fatal(string(b))
	}
	want := `{"schema_version":1,"revision":0,"projects":[],"targets":[],"environments":[],"resources":[],"bindings":[],"mission_links":[]}`
	if string(b) != want {
		t.Fatalf("unexpected empty catalog JSON:\n got %s\nwant %s", b, want)
	}
}

func TestCatalogJSONContract(t *testing.T) {
	owner := "p1"
	values := []struct {
		name string
		got  any
		want string
	}{
		{"meta", testCatalogMeta("x"), `{"id":"x","revision":1,"created_at":"2026-10-18T00:00:00Z","updated_at":"2026-10-18T00:00:00Z"}`},
		{"project", Project{CatalogMeta: testCatalogMeta("p1"), Name: "P", Description: "D"}, `{"id":"p1","revision":1,"created_at":"2026-10-18T00:00:00Z","updated_at":"2026-10-18T00:00:00Z","name":"P","description":"D"}`},
		{"target", DeliveryTarget{CatalogMeta: testCatalogMeta("t1"), ProjectID: "p1", Name: "T", Platform: "ios", Format: "ipa", Architecture: "arm64", Distribution: "development"}, `{"id":"t1","revision":1,"created_at":"2026-10-18T00:00:00Z","updated_at":"2026-10-18T00:00:00Z","project_id":"p1","name":"T","platform":"ios","format":"ipa","architecture":"arm64","distribution":"development"}`},
		{"environment", Environment{CatalogMeta: testCatalogMeta("e1"), ProjectID: "p1", Name: "E", Tier: "dev"}, `{"id":"e1","revision":1,"created_at":"2026-10-18T00:00:00Z","updated_at":"2026-10-18T00:00:00Z","project_id":"p1","name":"E","tier":"dev"}`},
		{"resource", Resource{CatalogMeta: testCatalogMeta("r1"), Name: "R", Kind: "service", OwnerProjectID: &owner, Provider: "aws", Account: "a", Region: "r", ExternalID: "x", Protected: true, Retention: "retain"}, `{"id":"r1","revision":1,"created_at":"2026-10-18T00:00:00Z","updated_at":"2026-10-18T00:00:00Z","name":"R","kind":"service","owner_project_id":"p1","provider":"aws","account":"a","region":"r","external_id":"x","protected":true,"retention":"retain"}`},
		{"binding", ResourceBinding{CatalogMeta: testCatalogMeta("b1"), ProjectID: "p1", EnvironmentID: "e1", ResourceID: "r1", Purpose: "app"}, `{"id":"b1","revision":1,"created_at":"2026-10-18T00:00:00Z","updated_at":"2026-10-18T00:00:00Z","project_id":"p1","environment_id":"e1","resource_id":"r1","purpose":"app"}`},
		{"mission link", MissionProjectLink{MissionID: "m1", ProjectID: "p1", LinkedAt: catalogTestTime}, `{"mission_id":"m1","project_id":"p1","linked_at":"2026-10-18T00:00:00Z"}`},
		{"event", CatalogEvent{Seq: 2, CatalogRevision: 3, Kind: "project.create", EntityIDs: []string{"p1"}, CreatedAt: catalogTestTime}, `{"seq":2,"catalog_revision":3,"kind":"project.create","entity_ids":["p1"],"created_at":"2026-10-18T00:00:00Z"}`},
	}
	for _, tt := range values {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.got)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("got %s\nwant %s", got, tt.want)
			}
		})
	}

	nullOwner, err := json.Marshal(Resource{OwnerProjectID: nil})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(nullOwner, []byte(`"owner_project_id":null`)) {
		t.Fatalf("nil owner did not encode as JSON null: %s", nullOwner)
	}
}

func TestCatalogTargetMatrix(t *testing.T) {
	tests := []struct {
		platform     string
		format       string
		architecture string
		distribution string
	}{
		{"windows", "exe", "x64", "direct"},
		{"windows", "exe", "arm64", "direct"},
		{"macos", "dmg", "x64", "direct"},
		{"macos", "dmg", "arm64", "direct"},
		{"macos", "dmg", "universal", "direct"},
		{"android", "apk", "arm64", "direct"},
		{"android", "apk", "x86_64", "direct"},
		{"android", "apk", "universal", "direct"},
		{"ios", "ipa", "arm64", "development"},
		{"ios", "ipa", "arm64", "ad_hoc"},
		{"ios", "ipa", "arm64", "testflight"},
		{"ios", "ipa", "arm64", "app_store"},
	}
	for _, tt := range tests {
		name := strings.Join([]string{tt.platform, tt.format, tt.architecture, tt.distribution}, "/")
		t.Run(name, func(t *testing.T) {
			c := EmptyCatalog()
			c.Projects = append(c.Projects, Project{CatalogMeta: testCatalogMeta("p1"), Name: "P"})
			c.Targets = append(c.Targets, DeliveryTarget{
				CatalogMeta: testCatalogMeta("t1"), ProjectID: "p1", Name: "Target",
				Platform: tt.platform, Format: tt.format, Architecture: tt.architecture, Distribution: tt.distribution,
			})
			if err := ValidateCatalog(c); err != nil {
				t.Fatal(err)
			}
		})
	}

	invalid := []struct {
		name         string
		format       string
		architecture string
	}{
		{"ios apk", "apk", "arm64"},
		{"unknown architecture", "ipa", "x86_64"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			c := EmptyCatalog()
			c.Projects = append(c.Projects, Project{CatalogMeta: testCatalogMeta("p1"), Name: "P"})
			c.Targets = append(c.Targets, DeliveryTarget{
				CatalogMeta: testCatalogMeta("t1"), ProjectID: "p1", Name: "Target",
				Platform: "ios", Format: tt.format, Architecture: tt.architecture, Distribution: "development",
			})
			requireInvalidCatalog(t, ValidateCatalog(c))
		})
	}
}

func TestCatalogTextBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CatalogSnapshot)
	}{
		{"empty name", func(c *CatalogSnapshot) { c.Projects[0].Name = "  \t " }},
		{"long name", func(c *CatalogSnapshot) { c.Projects[0].Name = strings.Repeat("界", 121) }},
		{"name control", func(c *CatalogSnapshot) { c.Projects[0].Name = "bad\nname" }},
		{"name invalid utf8", func(c *CatalogSnapshot) { c.Projects[0].Name = string([]byte{0xff}) }},
		{"long description", func(c *CatalogSnapshot) { c.Projects[0].Description = strings.Repeat("界", 2001) }},
		{"description control", func(c *CatalogSnapshot) { c.Projects[0].Description = "bad\tdescription" }},
		{"description invalid utf8", func(c *CatalogSnapshot) { c.Projects[0].Description = string([]byte{0xff}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testCatalog()
			tt.mutate(&c)
			requireInvalidCatalog(t, ValidateCatalog(c))
		})
	}
	c := testCatalog()
	c.Projects[0].Name = strings.Repeat("界", 120)
	c.Projects[0].Description = strings.Repeat("界", 1999) + "\n"
	if err := ValidateCatalog(c); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogMetadataAndIdentifiers(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CatalogSnapshot)
	}{
		{"schema", func(c *CatalogSnapshot) { c.SchemaVersion = 2 }},
		{"negative catalog revision", func(c *CatalogSnapshot) { c.Revision = -1 }},
		{"zero entity revision", func(c *CatalogSnapshot) { c.Projects[0].Revision = 0 }},
		{"bad created time", func(c *CatalogSnapshot) { c.Projects[0].CreatedAt = "yesterday" }},
		{"non UTC updated time", func(c *CatalogSnapshot) { c.Projects[0].UpdatedAt = "2026-10-18T08:00:00+08:00" }},
		{"empty id", func(c *CatalogSnapshot) { c.Projects[0].ID = "" }},
		{"long id", func(c *CatalogSnapshot) { c.Projects[0].ID = strings.Repeat("a", 65) }},
		{"non ASCII id", func(c *CatalogSnapshot) { c.Projects[0].ID = "项目" }},
		{"punctuated id", func(c *CatalogSnapshot) { c.Projects[0].ID = "project.1" }},
		{"duplicate global id", func(c *CatalogSnapshot) { c.Resources[0].ID = c.Projects[0].ID }},
		{"nil projects", func(c *CatalogSnapshot) { c.Projects = nil }},
		{"nil targets", func(c *CatalogSnapshot) { c.Targets = nil }},
		{"nil environments", func(c *CatalogSnapshot) { c.Environments = nil }},
		{"nil resources", func(c *CatalogSnapshot) { c.Resources = nil }},
		{"nil bindings", func(c *CatalogSnapshot) { c.Bindings = nil }},
		{"nil mission links", func(c *CatalogSnapshot) { c.MissionLinks = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testCatalog()
			tt.mutate(&c)
			requireInvalidCatalog(t, ValidateCatalog(c))
		})
	}
}

func TestCatalogResourceRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Resource)
	}{
		{"unknown kind", func(r *Resource) { r.Kind = "queue" }},
		{"unknown provider", func(r *Resource) { r.Provider = "digitalocean" }},
		{"missing account", func(r *Resource) { r.Account = "" }},
		{"long identity field", func(r *Resource) { r.Region = strings.Repeat("x", 257) }},
		{"identity whitespace", func(r *Resource) { r.ExternalID = "database main" }},
		{"identity URL", func(r *Resource) { r.ExternalID = "https://example.test" }},
		{"identity user info", func(r *Resource) { r.Account = "user@example" }},
		{"identity query", func(r *Resource) { r.ExternalID = "database?secret=x" }},
		{"not protected", func(r *Resource) { r.Protected = false }},
		{"wrong retention", func(r *Resource) { r.Retention = "delete" }},
		{"manual account", func(r *Resource) { r.Provider = "manual" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testCatalog()
			tt.mutate(&c.Resources[0])
			requireInvalidCatalog(t, ValidateCatalog(c))
		})
	}

	c := testCatalog()
	c.Resources[0].Provider = "manual"
	c.Resources[0].Account = ""
	c.Resources[0].Region = ""
	c.Resources[0].ExternalID = ""
	if err := ValidateCatalog(c); err != nil {
		t.Fatal(err)
	}

	duplicate := testCatalog()
	second := duplicate.Resources[0]
	second.ID = "r2"
	second.Name = "Same external resource"
	duplicate.Resources = append(duplicate.Resources, second)
	requireInvalidCatalog(t, ValidateCatalog(duplicate))
}

func TestCatalogRelationshipRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CatalogSnapshot)
	}{
		{"target project dangling", func(c *CatalogSnapshot) { c.Targets[0].ProjectID = "missing" }},
		{"environment project dangling", func(c *CatalogSnapshot) { c.Environments[0].ProjectID = "missing" }},
		{"resource owner dangling", func(c *CatalogSnapshot) { missing := "missing"; c.Resources[0].OwnerProjectID = &missing }},
		{"binding project dangling", func(c *CatalogSnapshot) { c.Bindings[0].ProjectID = "missing" }},
		{"binding environment dangling", func(c *CatalogSnapshot) { c.Bindings[0].EnvironmentID = "missing" }},
		{"binding resource dangling", func(c *CatalogSnapshot) { c.Bindings[0].ResourceID = "missing" }},
		{"binding cross project environment", func(c *CatalogSnapshot) { c.Bindings[0].EnvironmentID = "e2" }},
		{"mission project dangling", func(c *CatalogSnapshot) { c.MissionLinks[0].ProjectID = "missing" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testCatalog()
			tt.mutate(&c)
			requireInvalidCatalog(t, ValidateCatalog(c))
		})
	}

	t.Run("duplicate purpose slot", func(t *testing.T) {
		c := testCatalog()
		second := c.Bindings[0]
		second.ID = "b2"
		c.Bindings = append(c.Bindings, second)
		requireInvalidCatalog(t, ValidateCatalog(c))
	})
	t.Run("duplicate mission link", func(t *testing.T) {
		c := testCatalog()
		c.MissionLinks = append(c.MissionLinks, MissionProjectLink{MissionID: "m1", ProjectID: "p2", LinkedAt: catalogTestTime})
		requireInvalidCatalog(t, ValidateCatalog(c))
	})
	t.Run("purpose syntax", func(t *testing.T) {
		c := testCatalog()
		c.Bindings[0].Purpose = "Primary DB"
		requireInvalidCatalog(t, ValidateCatalog(c))
	})
	t.Run("resource may be shared across projects", func(t *testing.T) {
		c := testCatalog()
		c.Bindings = append(c.Bindings, ResourceBinding{
			CatalogMeta: testCatalogMeta("b2"), ProjectID: "p2", EnvironmentID: "e2",
			ResourceID: "r1", Purpose: "shared-db",
		})
		if err := ValidateCatalog(c); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCatalogCollectionCountBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		make  func(int) CatalogSnapshot
	}{
		{"projects", 50, catalogWithProjects},
		{"targets", 250, catalogWithTargets},
		{"environments", 150, catalogWithEnvironments},
		{"resources", 500, catalogWithResources},
		{"bindings", 1000, catalogWithBindings},
		{"mission links", 2000, catalogWithMissionLinks},
	}
	for _, tt := range tests {
		t.Run(tt.name+" at limit", func(t *testing.T) {
			if err := ValidateCatalog(tt.make(tt.limit)); err != nil {
				t.Fatal(err)
			}
		})
		t.Run(tt.name+" over limit", func(t *testing.T) {
			requireInvalidCatalog(t, ValidateCatalog(tt.make(tt.limit+1)))
		})
	}
}

func TestCatalogEncodedSizeLimit(t *testing.T) {
	c := catalogWithResources(500)
	for i := range c.Resources {
		c.Resources[i].Provider = "aws"
		c.Resources[i].Account = fmt.Sprintf("%0250d", i)
		c.Resources[i].Region = strings.Repeat("r", 256)
		c.Resources[i].ExternalID = fmt.Sprintf("%0250d", i)
		c.Resources[i].Name = strings.Repeat("R", 120)
	}
	projects := catalogWithProjects(50)
	for i := range projects.Projects {
		projects.Projects[i].Name = strings.Repeat("P", 120)
		projects.Projects[i].Description = strings.Repeat("D", 2000)
	}
	c.Projects = projects.Projects
	c.Targets = catalogWithTargets(250).Targets
	for i := range c.Targets {
		c.Targets[i].Name = strings.Repeat("T", 120)
	}
	c.Environments = catalogWithEnvironments(150).Environments
	for i := range c.Environments {
		c.Environments[i].Name = strings.Repeat("E", 120)
	}
	c.Bindings = catalogWithBindings(1000).Bindings
	for i := range c.Bindings {
		c.Bindings[i].Purpose = fmt.Sprintf("p%063d", i)
	}
	c.MissionLinks = catalogWithMissionLinks(2000).MissionLinks
	for i := range c.MissionLinks {
		c.MissionLinks[i].MissionID = fmt.Sprintf("m%063d", i)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) <= 1048576 {
		t.Fatalf("oversize fixture is only %d bytes", len(b))
	}
	requireInvalidCatalog(t, ValidateCatalog(c))
}

func catalogWithProjects(n int) CatalogSnapshot {
	c := EmptyCatalog()
	for i := 0; i < n; i++ {
		c.Projects = append(c.Projects, Project{CatalogMeta: testCatalogMeta(fmt.Sprintf("p%d", i)), Name: "P"})
	}
	return c
}

func catalogWithTargets(n int) CatalogSnapshot {
	c := catalogWithProjects(1)
	for i := 0; i < n; i++ {
		c.Targets = append(c.Targets, DeliveryTarget{
			CatalogMeta: testCatalogMeta(fmt.Sprintf("t%d", i)), ProjectID: "p0", Name: "T",
			Platform: "ios", Format: "ipa", Architecture: "arm64", Distribution: "development",
		})
	}
	return c
}

func catalogWithEnvironments(n int) CatalogSnapshot {
	c := catalogWithProjects(1)
	for i := 0; i < n; i++ {
		c.Environments = append(c.Environments, Environment{
			CatalogMeta: testCatalogMeta(fmt.Sprintf("e%d", i)), ProjectID: "p0", Name: "E", Tier: "dev",
		})
	}
	return c
}

func catalogWithResources(n int) CatalogSnapshot {
	c := EmptyCatalog()
	for i := 0; i < n; i++ {
		c.Resources = append(c.Resources, Resource{
			CatalogMeta: testCatalogMeta(fmt.Sprintf("r%d", i)), Name: "R", Kind: "service",
			Provider: "manual", Protected: true, Retention: "retain",
		})
	}
	return c
}

func catalogWithBindings(n int) CatalogSnapshot {
	c := catalogWithProjects(1)
	c.Environments = append(c.Environments, Environment{CatalogMeta: testCatalogMeta("e0"), ProjectID: "p0", Name: "E", Tier: "dev"})
	c.Resources = append(c.Resources, Resource{CatalogMeta: testCatalogMeta("r0"), Name: "R", Kind: "service", Provider: "manual", Protected: true, Retention: "retain"})
	for i := 0; i < n; i++ {
		c.Bindings = append(c.Bindings, ResourceBinding{
			CatalogMeta: testCatalogMeta(fmt.Sprintf("b%d", i)), ProjectID: "p0", EnvironmentID: "e0",
			ResourceID: "r0", Purpose: fmt.Sprintf("purpose-%d", i),
		})
	}
	return c
}

func catalogWithMissionLinks(n int) CatalogSnapshot {
	c := catalogWithProjects(1)
	for i := 0; i < n; i++ {
		c.MissionLinks = append(c.MissionLinks, MissionProjectLink{
			MissionID: fmt.Sprintf("m%d", i), ProjectID: "p0", LinkedAt: catalogTestTime,
		})
	}
	return c
}

func TestCatalogChangeAllowsOnlyKindSpecificMutation(t *testing.T) {
	tests := []struct {
		kind   string
		mutate func(*CatalogSnapshot)
	}{
		{"project.create", func(c *CatalogSnapshot) {
			c.Projects = append(c.Projects, Project{CatalogMeta: CatalogMeta{ID: "p3"}, Name: "P3"})
		}},
		{"project.update", func(c *CatalogSnapshot) { c.Projects[0].Name = "Renamed"; c.Projects[0].Description = "Updated" }},
		{"target.create", func(c *CatalogSnapshot) {
			c.Targets = append(c.Targets, DeliveryTarget{CatalogMeta: CatalogMeta{ID: "t2"}, ProjectID: "p1", Name: "Mac", Platform: "macos", Format: "dmg", Architecture: "universal", Distribution: "direct"})
		}},
		{"target.rename", func(c *CatalogSnapshot) { c.Targets[0].Name = "Renamed" }},
		{"environment.create", func(c *CatalogSnapshot) {
			c.Environments = append(c.Environments, Environment{CatalogMeta: CatalogMeta{ID: "e3"}, ProjectID: "p1", Name: "Local", Tier: "local"})
		}},
		{"environment.rename", func(c *CatalogSnapshot) { c.Environments[0].Name = "Renamed" }},
		{"resource.create", func(c *CatalogSnapshot) {
			c.Resources = append(c.Resources, Resource{CatalogMeta: CatalogMeta{ID: "r2"}, Name: "Manual", Kind: "service", Provider: "manual", Protected: true, Retention: "retain"})
		}},
		{"resource.rename", func(c *CatalogSnapshot) { c.Resources[0].Name = "Renamed" }},
		{"resource.bind", func(c *CatalogSnapshot) {
			c.Bindings = append(c.Bindings, ResourceBinding{CatalogMeta: CatalogMeta{ID: "b2"}, ProjectID: "p2", EnvironmentID: "e2", ResourceID: "r1", Purpose: "shared"})
		}},
		{"resource.unbind", func(c *CatalogSnapshot) { c.Bindings = c.Bindings[:0] }},
		{"mission.link", func(c *CatalogSnapshot) {
			c.MissionLinks = append(c.MissionLinks, MissionProjectLink{MissionID: "m2", ProjectID: "p2"})
		}},
		{"mission.relink", func(c *CatalogSnapshot) { c.MissionLinks[0].ProjectID = "p2"; c.MissionLinks[0].LinkedAt = "" }},
		{"mission.unlink", func(c *CatalogSnapshot) { c.MissionLinks = c.MissionLinks[:0] }},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			before := testCatalog()
			after := cloneCatalog(t, before)
			tt.mutate(&after)
			if err := ValidateCatalogChange(before, after, tt.kind); err != nil {
				t.Fatal(err)
			}
		})
		t.Run(tt.kind+" no-op", func(t *testing.T) {
			before := testCatalog()
			if err := ValidateCatalogChange(before, cloneCatalog(t, before), tt.kind); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCatalogChangeRejectsSemanticSideEffects(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		mutate func(*CatalogSnapshot)
	}{
		{"environment rename changes tier", "environment.rename", func(c *CatalogSnapshot) { c.Environments[0].Name = "Renamed"; c.Environments[0].Tier = "staging" }},
		{"resource rename changes external id", "resource.rename", func(c *CatalogSnapshot) {
			c.Resources[0].Name = "Renamed"
			c.Resources[0].ExternalID = "database/other"
		}},
		{"project update deletes target", "project.update", func(c *CatalogSnapshot) { c.Projects[0].Name = "Renamed"; c.Targets = c.Targets[:0] }},
		{"resource unbind deletes resource", "resource.unbind", func(c *CatalogSnapshot) { c.Bindings = c.Bindings[:0]; c.Resources = c.Resources[:0] }},
		{"target rename changes project", "target.rename", func(c *CatalogSnapshot) { c.Targets[0].Name = "Renamed"; c.Targets[0].ProjectID = "p2" }},
		{"mission link edits existing link", "mission.link", func(c *CatalogSnapshot) { c.MissionLinks[0].ProjectID = "p2"; c.MissionLinks[0].LinkedAt = "" }},
		{"mission unlink removes wrong collection", "mission.unlink", func(c *CatalogSnapshot) { c.Bindings = c.Bindings[:0] }},
		{"snapshot revision", "project.update", func(c *CatalogSnapshot) { c.Revision++ }},
		{"snapshot schema", "project.update", func(c *CatalogSnapshot) { c.SchemaVersion++ }},
		{"existing metadata", "project.update", func(c *CatalogSnapshot) { c.Projects[0].Revision++ }},
		{"new metadata stamped", "project.create", func(c *CatalogSnapshot) {
			c.Projects = append(c.Projects, Project{CatalogMeta: testCatalogMeta("p3"), Name: "P3"})
		}},
		{"new id invalid", "project.create", func(c *CatalogSnapshot) {
			c.Projects = append(c.Projects, Project{CatalogMeta: CatalogMeta{ID: "bad.id"}, Name: "P3"})
		}},
		{"multiple creates", "project.create", func(c *CatalogSnapshot) {
			c.Projects = append(c.Projects, Project{CatalogMeta: CatalogMeta{ID: "p3"}, Name: "P3"}, Project{CatalogMeta: CatalogMeta{ID: "p4"}, Name: "P4"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := testCatalog()
			after := cloneCatalog(t, before)
			tt.mutate(&after)
			requireInvalidCatalog(t, ValidateCatalogChange(before, after, tt.kind))
		})
	}
}

func TestCatalogChangeRejectsUnknownKind(t *testing.T) {
	before := testCatalog()
	requireInvalidCatalog(t, ValidateCatalogChange(before, cloneCatalog(t, before), "project.delete"))
}
