package store

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/knowledge"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestKnowledgePersistenceAtomicReplacement(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graph.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	m, err := s.CreateMission(domain.Mission{ID: "old", Title: "before", State: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateCatalog(0, "project.create", catalogProject); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncKnowledge(ctx, false); err != nil {
		t.Fatal(err)
	}
	g, err := s.QueryKnowledge(ctx, knowledge.Query{})
	if err != nil || len(g.Nodes) < 2 {
		t.Fatal(g, err)
	}
	before, _ := s.GetMission(m.ID)
	events, _ := s.Events(0, 100)
	cat, _ := s.Catalog()
	if err = s.SyncKnowledge(ctx, true); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetMission(m.ID)
	afterEvents, _ := s.Events(0, 100)
	afterCat, _ := s.Catalog()
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(events, afterEvents) || !reflect.DeepEqual(cat, afterCat) {
		t.Fatal("rebuild modified business records")
	}
	g, err = s.QueryKnowledge(ctx, knowledge.Query{})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := s.QueryKnowledge(ctx, knowledge.Query{})
	if err != nil || !reflect.DeepEqual(g, reopened) {
		t.Fatal("reopen", err)
	}
	// Forced write failure must preserve the previous complete graph.
	if _, err = s.db.Exec(`CREATE TRIGGER fail_knowledge BEFORE INSERT ON knowledge_nodes BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncKnowledge(ctx, true); !errors.Is(err, ErrKnowledge) {
		t.Fatal(err)
	}
	recovered, err := s.QueryKnowledge(ctx, knowledge.Query{})
	if err != nil || !reflect.DeepEqual(reopened, recovered) {
		t.Fatal("partial replacement", err)
	}
	s.db.Exec(`DROP TRIGGER fail_knowledge`)
	if _, err = s.UpdateMission(m.ID, m.Revision, "mission.updated", func(m *domain.Mission) error { m.Title = "after"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncKnowledge(ctx, false); err != nil {
		t.Fatal(err)
	}
	updated, err := s.QueryKnowledge(ctx, knowledge.Query{Search: "after"})
	if err != nil || len(updated.Nodes) != 1 {
		t.Fatal(updated, err)
	}
	// Source deletion removes derived records without touching remaining old records.
	if _, err = s.db.Exec(`DELETE FROM events WHERE mission_id='old'; DELETE FROM missions WHERE id='old'`); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncKnowledge(ctx, false); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.QueryKnowledge(ctx, knowledge.Query{MissionID: "old"})
	if err != nil || len(deleted.Nodes) != 0 {
		t.Fatal(deleted, err)
	}
}

func TestKnowledgeTraversalBoundsCorruptionAndRecovery(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if _, err := s.CreateMission(domain.Mission{ID: "m1", Title: "task", State: "draft", Plan: &domain.Plan{}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncKnowledge(ctx, false); err != nil {
		t.Fatal(err)
	}
	full, err := s.QueryKnowledge(ctx, knowledge.Query{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.QueryKnowledge(ctx, knowledge.Query{Root: "task:m1", Mode: "impact", Depth: 4, Limit: 1})
	if err != nil || len(out.Nodes) != 1 || !out.Truncated {
		t.Fatal(out, err)
	}
	incoming, err := s.QueryKnowledge(ctx, knowledge.Query{Root: "task:m1", Mode: "lineage"})
	if err != nil || len(incoming.Nodes) != 1 {
		t.Fatal(incoming, err)
	}
	for _, q := range []knowledge.Query{{Depth: 5}, {Depth: -1}, {Limit: 201}, {Mode: "sql"}} {
		if _, err = s.QueryKnowledge(ctx, q); !errors.Is(err, ErrKnowledgeQuery) {
			t.Fatal(q, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.QueryKnowledge(canceled, knowledge.Query{}); err == nil {
		t.Fatal("cancellation ignored")
	}
	var raw []byte
	s.db.QueryRow(`SELECT data FROM knowledge_nodes LIMIT 1`).Scan(&raw)
	var n knowledge.Node
	json.Unmarshal(raw, &n)
	n.Label = "corrupt"
	raw, _ = json.Marshal(n)
	s.db.Exec(`UPDATE knowledge_nodes SET data=? WHERE id=?`, raw, n.ID)
	if _, err = s.QueryKnowledge(ctx, knowledge.Query{}); !errors.Is(err, ErrKnowledge) {
		t.Fatal("corruption hidden", err)
	}
	if err = s.SyncKnowledge(ctx, false); !errors.Is(err, ErrKnowledge) {
		t.Fatal("normal sync silently repaired", err)
	}
	if err = s.SyncKnowledge(ctx, true); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := s.QueryKnowledge(ctx, knowledge.Query{})
	if err != nil || full.SourceDigest != rebuilt.SourceDigest {
		t.Fatal(rebuilt, err)
	}
}

func TestKnowledgeProjectScopeSharedResourcesAndCycles(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	plan := &domain.Plan{Revision: 2, Steps: []domain.Step{{ID: "a", Title: "A", DependsOn: []string{"b"}}, {ID: "b", Title: "B", DependsOn: []string{"a"}}}}
	if _, err := s.CreateMission(domain.Mission{ID: "scope", Title: "scoped", State: "draft", Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMission(domain.Mission{ID: "other", Title: "secret other task", State: "draft"}); err != nil {
		t.Fatal(err)
	}
	update := func(kind string, mutate func(*domain.CatalogSnapshot) ([]string, error)) {
		t.Helper()
		c, e := s.Catalog()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.UpdateCatalog(c.Revision, kind, mutate); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []string{"p1", "p2"} {
		update("project.create", func(c *domain.CatalogSnapshot) ([]string, error) {
			c.Projects = append(c.Projects, domain.Project{CatalogMeta: domain.CatalogMeta{ID: id}, Name: id})
			return []string{id}, nil
		})
	}
	update("resource.create", func(c *domain.CatalogSnapshot) ([]string, error) {
		c.Resources = append(c.Resources, domain.Resource{CatalogMeta: domain.CatalogMeta{ID: "shared"}, Name: "Shared", Kind: "database", Provider: "aws", Region: "us-east-1", Protected: true, Retention: "retain", Account: "ACCOUNT-MUST-NOT-LEAK", ExternalID: "EXTERNAL-MUST-NOT-LEAK"})
		return []string{"shared"}, nil
	})
	update("environment.create", func(c *domain.CatalogSnapshot) ([]string, error) {
		c.Environments = append(c.Environments, domain.Environment{CatalogMeta: domain.CatalogMeta{ID: "env"}, ProjectID: "p1", Name: "Dev", Tier: "dev"})
		return []string{"env"}, nil
	})
	update("resource.bind", func(c *domain.CatalogSnapshot) ([]string, error) {
		c.Bindings = append(c.Bindings, domain.ResourceBinding{CatalogMeta: domain.CatalogMeta{ID: "binding"}, ProjectID: "p1", EnvironmentID: "env", Purpose: "database", ResourceID: "shared"})
		return []string{"binding"}, nil
	})
	for _, link := range []domain.MissionProjectLink{{MissionID: "scope", ProjectID: "p1"}, {MissionID: "other", ProjectID: "p2"}} {
		update("mission.link", func(c *domain.CatalogSnapshot) ([]string, error) {
			c.MissionLinks = append(c.MissionLinks, link)
			return []string{link.MissionID}, nil
		})
	}

	revision := domain.PlanRevision{MissionID: "scope", PlanRevision: 1, MissionRevision: 1, Plan: domain.Plan{Revision: 1, Steps: []domain.Step{{ID: "old", Title: "Old step"}}}}
	raw, _ := json.Marshal(revision)
	if _, err := s.db.Exec(`INSERT INTO plan_revisions VALUES(?,?,?)`, "scope", 1, raw); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncKnowledge(ctx, false); err != nil {
		t.Fatal(err)
	}
	scope, err := s.QueryKnowledge(ctx, knowledge.Query{ProjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	foundResource, foundTask, foundHistory := false, false, false
	for _, n := range scope.Nodes {
		if n.MissionID == "other" {
			t.Fatal("cross-project leak")
		}
		foundResource = foundResource || n.Kind == "resource"
		foundTask = foundTask || n.Kind == "task"
		foundHistory = foundHistory || n.Historical && n.Kind == "plan"
	}
	if !foundResource || !foundTask || !foundHistory {
		t.Fatal("project membership/history missing", scope)
	}
	scopedTraversal, err := s.QueryKnowledge(ctx, knowledge.Query{Root: "project:p1", ProjectID: "p1", Depth: 4})
	if err != nil {
		t.Fatal(err)
	}
	foundResource = false
	for _, n := range scopedTraversal.Nodes {
		foundResource = foundResource || n.Kind == "resource"
	}
	if !foundResource {
		t.Fatal("traversal excluded bound shared resource")
	}
	var root string
	for _, n := range scope.Nodes {
		if n.Kind == "step" && !n.Historical {
			root = n.ID
			break
		}
	}
	cycle, err := s.QueryKnowledge(ctx, knowledge.Query{Root: root, Mode: "impact", Depth: 4})
	if err != nil || len(cycle.Nodes) != 2 {
		t.Fatal("cycle traversal", cycle, err)
	}
	all, err := s.QueryKnowledge(ctx, knowledge.Query{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(all)
	if strings.Contains(string(raw), "MUST-NOT-LEAK") {
		t.Fatal("resource secret leaked")
	}
	var stored []byte
	s.db.QueryRow(`SELECT data FROM plan_revisions WHERE mission_id='scope'`).Scan(&stored)
	expected, _ := json.Marshal(revision)
	if string(stored) != string(expected) {
		t.Fatal("plan revision changed")
	}
}

func TestKnowledgeScopedIssuesExcludeOtherMissions(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, m := range []domain.Mission{
		{ID: "clean", Title: "Clean", State: "draft"},
		{ID: "unrelated/fixture", Title: "Legacy fixture", State: "draft", Evidence: []domain.Verification{{Passed: true}}},
	} {
		if _, err := s.CreateMission(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SyncKnowledge(ctx, false); err != nil {
		t.Fatal(err)
	}
	all, err := s.QueryKnowledge(ctx, knowledge.Query{})
	if err != nil || len(all.Issues) == 0 {
		t.Fatal("fixture issue missing", all, err)
	}
	for _, q := range []knowledge.Query{{MissionID: "clean"}, {Root: "task:clean"}, {Search: "Clean"}} {
		graph, err := s.QueryKnowledge(ctx, q)
		if err != nil || len(graph.Nodes) != 1 || len(graph.Issues) != 0 {
			t.Fatal("unrelated issue leaked into scoped graph", q, graph, err)
		}
	}
	related, err := s.QueryKnowledge(ctx, knowledge.Query{MissionID: "unrelated/fixture"})
	if err != nil || len(related.Issues) == 0 {
		t.Fatal("related issue was lost", related, err)
	}
}
