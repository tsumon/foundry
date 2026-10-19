package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"foundry/internal/domain"
)

func catalogProject(c *domain.CatalogSnapshot) ([]string, error) {
	c.Projects = append(c.Projects, domain.Project{CatalogMeta: domain.CatalogMeta{ID: "p1"}, Name: "A"})
	return []string{"p1"}, nil
}

func TestCatalogInvalidMutationDoesNotCommit(t *testing.T) {
	s := openTestStore(t)
	before, err := s.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateCatalog(0, "target.create", func(c *domain.CatalogSnapshot) ([]string, error) {
		c.Targets = append(c.Targets, domain.DeliveryTarget{ProjectID: "missing", Name: "broken"})
		return []string{"missing"}, nil
	})
	if err == nil || errors.Is(err, ErrCatalogCorrupt) {
		t.Fatal("invalid mutation", err)
	}
	after, err := s.Catalog()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("partial mutation", err)
	}
	events, err := s.CatalogEvents(0, 100)
	if err != nil || len(events) != 0 || events == nil {
		t.Fatal(events, err)
	}
}

func TestCatalogCASAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foundry.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.UpdateCatalog(0, "project.create", catalogProject)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal(successes, conflicts)
	}
	before, err := s.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, err := s.Catalog()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal(after, err)
	}
	if after.Projects[0].Revision != 1 || after.Projects[0].CreatedAt == "" || after.Projects[0].CreatedAt != after.Projects[0].UpdatedAt {
		t.Fatal(after)
	}
}

func TestCatalogMutationMetadataAndIDsAreGuarded(t *testing.T) {
	for _, test := range []struct {
		name, kind string
		mutate     func(*domain.CatalogSnapshot) ([]string, error)
	}{
		{"revision", "project.update", func(c *domain.CatalogSnapshot) ([]string, error) { c.Revision++; return nil, nil }},
		{"schema", "project.update", func(c *domain.CatalogSnapshot) ([]string, error) { c.SchemaVersion++; return nil, nil }},
		{"created", "project.update", func(c *domain.CatalogSnapshot) ([]string, error) {
			c.Projects[0].CreatedAt = "2026-01-01T00:00:00Z"
			return []string{"p1"}, nil
		}},
		{"entity revision", "project.update", func(c *domain.CatalogSnapshot) ([]string, error) {
			c.Projects[0].Revision++
			return []string{"p1"}, nil
		}},
		{"wrong IDs", "project.update", func(c *domain.CatalogSnapshot) ([]string, error) {
			c.Projects[0].Name = "B"
			return []string{"wrong"}, nil
		}},
		{"missing IDs", "project.update", func(c *domain.CatalogSnapshot) ([]string, error) { c.Projects[0].Name = "B"; return nil, nil }},
		{"noop IDs", "project.update", func(c *domain.CatalogSnapshot) ([]string, error) { return []string{"p1"}, nil }},
		{"new timestamp", "project.create", func(c *domain.CatalogSnapshot) ([]string, error) {
			c.Projects = append(c.Projects, domain.Project{CatalogMeta: domain.CatalogMeta{ID: "p2", CreatedAt: "2026-01-01T00:00:00Z"}, Name: "B"})
			return []string{"p2"}, nil
		}},
		{"linked_at", "mission.link", func(c *domain.CatalogSnapshot) ([]string, error) {
			c.MissionLinks = append(c.MissionLinks, domain.MissionProjectLink{MissionID: "m1", ProjectID: "p1", LinkedAt: "2026-01-01T00:00:00Z"})
			return []string{"m1"}, nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := openTestStore(t)
			before, err := s.UpdateCatalog(0, "project.create", catalogProject)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.UpdateCatalog(1, test.kind, test.mutate)
			if !errors.Is(err, domain.ErrInvalidCatalog) {
				t.Fatal(err)
			}
			after, err := s.Catalog()
			events, eventErr := s.CatalogEvents(0, 100)
			if err != nil || eventErr != nil || !reflect.DeepEqual(before, after) || len(events) != 1 {
				t.Fatal("rollback failed", err, eventErr)
			}
		})
	}
	s := openTestStore(t)
	_, err := s.UpdateCatalog(0, "project.create", func(c *domain.CatalogSnapshot) ([]string, error) {
		_, err := catalogProject(c)
		return []string{"p1", "p1"}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	events, _ := s.CatalogEvents(0, 100)
	if !reflect.DeepEqual(events[0].EntityIDs, []string{"p1"}) {
		t.Fatal(events)
	}
}

func TestCatalogCorruptionNeverReseedsOrBlocksMissions(t *testing.T) {
	cases := map[string]string{
		"empty both":             `DELETE FROM catalog; DELETE FROM catalog_events`,
		"missing row":            `DELETE FROM catalog`,
		"one table":              `DROP TABLE catalog_events`,
		"other table":            `DROP TABLE catalog`,
		"bad json":               `UPDATE catalog SET data='{'`,
		"bad revision":           `UPDATE catalog SET revision=7`,
		"future schema":          `UPDATE catalog SET data=json_set(data,'$.schema_version',2)`,
		"event gap":              `DELETE FROM catalog_events`,
		"bad event ids":          `UPDATE catalog_events SET entity_ids='null'`,
		"bad event time":         `UPDATE catalog_events SET created_at='yesterday'`,
		"bad event kind":         `UPDATE catalog_events SET kind='deploy'`,
		"unknown snapshot field": `UPDATE catalog SET data=json_set(data,'$.unexpected',1)`,
		"null description":       `UPDATE catalog SET data=json_set(data,'$.projects[0].description',null)`,
		"missing description":    `UPDATE catalog SET data=json_remove(data,'$.projects[0].description')`,
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "foundry.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.CreateMission(domain.Mission{ID: "legacy", State: "draft"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.UpdateCatalog(0, "project.create", catalogProject); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(corrupt); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatalf("corruption blocked old V1 startup: %v", err)
			}
			defer s.Close()
			if _, err = s.GetMission("legacy"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Catalog(); !errors.Is(err, ErrCatalogCorrupt) {
				t.Fatalf("read: %v", err)
			}
			called := false
			_, err = s.UpdateCatalog(1, "project.update", func(c *domain.CatalogSnapshot) ([]string, error) { called = true; return nil, nil })
			if !errors.Is(err, ErrCatalogCorrupt) || called {
				t.Fatalf("write: %v callback=%v", err, called)
			}
			if _, err = s.CatalogEvents(0, 100); !errors.Is(err, ErrCatalogCorrupt) {
				t.Fatalf("events: %v", err)
			}
		})
	}
}

func TestCatalogUpgradePreservesV1Bytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foundry.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateMission(domain.Mission{ID: "legacy", State: "draft", Title: "untouched"})
	if err != nil {
		t.Fatal(err)
	}
	var before []byte
	if err = s.db.QueryRow(`SELECT data FROM missions WHERE id='legacy'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	events, _ := s.Events(0, 100)
	if _, err = s.db.Exec(`DROP TABLE catalog; DROP TABLE catalog_events`); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := s.Catalog()
	if err != nil || !reflect.DeepEqual(c, domain.EmptyCatalog()) {
		t.Fatal(c, err)
	}
	var after []byte
	_ = s.db.QueryRow(`SELECT data FROM missions WHERE id='legacy'`).Scan(&after)
	eventsAfter, _ := s.Events(0, 100)
	if !bytes.Equal(before, after) || !reflect.DeepEqual(events, eventsAfter) {
		t.Fatal("V1 bytes rewritten")
	}
}

func TestCatalogFinalStampedSizeLimit(t *testing.T) {
	s := openTestStore(t)
	c := domain.EmptyCatalog()
	c.Revision = 1
	meta := func(id string) domain.CatalogMeta {
		return domain.CatalogMeta{ID: id, Revision: 1, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z"}
	}
	for i := 0; i < 50; i++ {
		c.Projects = append(c.Projects, domain.Project{CatalogMeta: meta(fmt.Sprintf("p%d", i)), Name: "A", Description: strings.Repeat("😀", 2000)})
	}
	for i := 0; i < 500; i++ {
		c.Resources = append(c.Resources, domain.Resource{CatalogMeta: meta(fmt.Sprintf("r%d", i)), Name: strings.Repeat("界", 120), Kind: "service", Provider: "aws", Account: strings.Repeat("a", 256), Region: strings.Repeat("b", 256), ExternalID: fmt.Sprintf("%0256d", i), Protected: true, Retention: "retain"})
	}
	raw, _ := json.Marshal(c)
	// Tune valid descriptions to leave room for the un-stamped link, but not its server timestamp.
	target := 1048576 - 70
	for len(raw) > target {
		n := (len(raw) - target + 3) / 4
		for i := range c.Projects {
			r := []rune(c.Projects[i].Description)
			if len(r) == 0 {
				continue
			}
			if n > len(r) {
				n = len(r)
			}
			c.Projects[i].Description = string(r[:len(r)-n])
			break
		}
		raw, _ = json.Marshal(c)
	}
	if err := domain.ValidateCatalog(c); err != nil {
		t.Fatal(err)
	}
	_, err := s.db.Exec(`UPDATE catalog SET revision=1,data=?; INSERT INTO catalog_events(catalog_revision,kind,entity_ids,created_at) VALUES(1,'project.create','["p0"]','2026-01-01T00:00:00Z')`, raw)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateMission(domain.Mission{ID: "m", State: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	proposal := c
	proposal.MissionLinks = []domain.MissionProjectLink{{MissionID: "m", ProjectID: "p0"}}
	proposalRaw, _ := json.Marshal(proposal)
	if len(proposalRaw) > 1048576 {
		t.Fatalf("proposal too large: %d", len(proposalRaw))
	}
	_, err = s.UpdateCatalog(1, "mission.link", func(c *domain.CatalogSnapshot) ([]string, error) {
		c.MissionLinks = proposal.MissionLinks
		return []string{"m"}, nil
	})
	if !errors.Is(err, domain.ErrInvalidCatalog) {
		t.Fatalf("stamp overflow accepted: %v (%d bytes)", err, len(proposalRaw))
	}
	after, err := s.Catalog()
	if err != nil || !reflect.DeepEqual(c, after) {
		t.Fatal("overflow did not roll back", err)
	}
}

func TestCatalogDeepCopyAndEventFailureRollback(t *testing.T) {
	s := openTestStore(t)
	c, err := s.UpdateCatalog(0, "project.create", catalogProject)
	if err != nil {
		t.Fatal(err)
	}
	owner := "p1"
	c, err = s.UpdateCatalog(c.Revision, "resource.create", func(c *domain.CatalogSnapshot) ([]string, error) {
		c.Resources = append(c.Resources, domain.Resource{CatalogMeta: domain.CatalogMeta{ID: "r1"}, Name: "R", Kind: "storage", OwnerProjectID: &owner, Provider: "manual", Protected: true, Retention: "retain"})
		return []string{"r1"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateCatalog(c.Revision, "resource.rename", func(c *domain.CatalogSnapshot) ([]string, error) {
		*c.Resources[0].OwnerProjectID = "missing"
		return []string{"r1"}, nil
	})
	if !errors.Is(err, domain.ErrInvalidCatalog) {
		t.Fatal("mutable pointer escaped before-state copy", err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_catalog_event BEFORE INSERT ON catalog_events BEGIN SELECT RAISE(ABORT,'fixture event failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateCatalog(c.Revision, "project.update", func(c *domain.CatalogSnapshot) ([]string, error) {
		c.Projects[0].Name = "B"
		return []string{"p1"}, nil
	})
	if err == nil {
		t.Fatal("event failure accepted")
	}
	after, err := s.Catalog()
	if err != nil || !reflect.DeepEqual(c, after) {
		t.Fatal("event failure partially committed", err)
	}
	events, err := s.CatalogEvents(0, 100)
	if err != nil || len(events) != 2 {
		t.Fatal(events, err)
	}
}

func TestCatalogDanglingMissionIsCorrupt(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateMission(domain.Mission{ID: "m1"}); err != nil {
		t.Fatal(err)
	}
	c, err := s.UpdateCatalog(0, "project.create", catalogProject)
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.UpdateCatalog(c.Revision, "mission.link", func(c *domain.CatalogSnapshot) ([]string, error) {
		c.MissionLinks = append(c.MissionLinks, domain.MissionProjectLink{MissionID: "m1", ProjectID: "p1"})
		return []string{"m1"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateCatalog(c.Revision, "mission.relink", func(c *domain.CatalogSnapshot) ([]string, error) {
		c.MissionLinks[0].LinkedAt = "2026-01-01T00:00:00Z"
		return []string{"m1"}, nil
	})
	if !errors.Is(err, domain.ErrInvalidCatalog) {
		t.Fatal("forged linked_at accepted", err)
	}
	if _, err = s.db.Exec(`DELETE FROM events WHERE mission_id='m1'; DELETE FROM missions WHERE id='m1'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Catalog(); !errors.Is(err, ErrCatalogCorrupt) {
		t.Fatal("dangling stored mission not corruption", err)
	}
}
