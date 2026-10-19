package catalog_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"foundry/internal/catalog"
	"foundry/internal/domain"
	"foundry/internal/store"
)

func newService(t *testing.T) (*catalog.Service, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return catalog.New(s), s
}

func execute(t *testing.T, svc *catalog.Service, revision int64, kind string, input any) domain.CatalogSnapshot {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Execute(catalog.Command{ExpectedRevision: revision, Kind: kind, Input: raw})
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	return got
}

func TestProjectRenameCASAndNoop(t *testing.T) {
	svc, _ := newService(t)
	first := execute(t, svc, 0, "project.create", map[string]string{"name": "工作台", "description": ""})
	if first.Revision != 1 || len(first.Projects) != 1 {
		t.Fatal(first)
	}
	body := map[string]string{"id": first.Projects[0].ID, "name": " 工作台 ", "description": ""}
	same := execute(t, svc, 1, "project.update", body)
	if !reflect.DeepEqual(first, same) {
		t.Fatal("normalized no-op changed state")
	}
	raw, _ := json.Marshal(body)
	_, err := svc.Execute(catalog.Command{ExpectedRevision: 0, Kind: "project.update", Input: raw})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatal(err)
	}
	events, err := svc.Events(0, 100)
	if err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
}

func TestCatalogCommandsAndEventIDs(t *testing.T) {
	svc, s := newService(t)
	mission, err := s.CreateMission(domain.Mission{ID: "m1", Title: "untouched", State: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	missionBefore, _ := json.Marshal(mission)
	eventsBefore, _ := s.Events(0, 100)
	c := execute(t, svc, 0, "project.create", map[string]string{"name": "A", "description": ""})
	p := c.Projects[0].ID
	c = execute(t, svc, c.Revision, "project.create", map[string]string{"name": "B", "description": ""})
	p2 := c.Projects[1].ID
	checks := []struct{ kind, id string }{{"project.create", p}, {"project.create", p2}}
	run := func(kind string, input any, id func(domain.CatalogSnapshot) string) {
		t.Helper()
		c = execute(t, svc, c.Revision, kind, input)
		checks = append(checks, struct{ kind, id string }{kind, id(c)})
	}
	fixed := func(id string) func(domain.CatalogSnapshot) string {
		return func(domain.CatalogSnapshot) string { return id }
	}
	run("project.update", map[string]string{"id": p, "name": "A2", "description": "detail"}, fixed(p))
	run("target.create", map[string]string{"project_id": p, "name": "ios", "platform": "ios", "format": "ipa", "architecture": "arm64", "distribution": "testflight"}, func(c domain.CatalogSnapshot) string { return c.Targets[0].ID })
	target := c.Targets[0].ID
	run("target.rename", map[string]string{"id": target, "name": "phone"}, fixed(target))
	run("environment.create", map[string]string{"project_id": p, "name": "dev", "tier": "dev"}, func(c domain.CatalogSnapshot) string { return c.Environments[0].ID })
	env := c.Environments[0].ID
	run("environment.rename", map[string]string{"id": env, "name": "test"}, fixed(env))
	resourceInput := map[string]any{"name": "shared", "kind": "database", "owner_project_id": p2, "provider": "aws", "account": "acct", "region": "east", "external_id": "db-1"}
	run("resource.create", resourceInput, func(c domain.CatalogSnapshot) string { return c.Resources[0].ID })
	resource := c.Resources[0].ID
	if !c.Resources[0].Protected || c.Resources[0].Retention != "retain" || len(resource) != 32 {
		t.Fatal(c.Resources)
	}
	raw, _ := json.Marshal(resourceInput)
	_, err = svc.Execute(catalog.Command{ExpectedRevision: c.Revision, Kind: "resource.create", Input: raw})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatal("identity duplicate", err)
	}
	run("resource.rename", map[string]string{"id": resource, "name": "renamed"}, fixed(resource))
	bind := map[string]string{"project_id": p, "environment_id": env, "resource_id": resource, "purpose": "db"}
	run("resource.bind", bind, func(c domain.CatalogSnapshot) string { return c.Bindings[0].ID })
	binding := c.Bindings[0].ID
	same := execute(t, svc, c.Revision, "resource.bind", bind)
	if !reflect.DeepEqual(c, same) {
		t.Fatal("bind no-op changed state")
	}
	run("mission.link", map[string]string{"mission_id": "m1", "project_id": p}, fixed("m1"))
	same = execute(t, svc, c.Revision, "mission.link", map[string]string{"mission_id": "m1", "project_id": p})
	if !reflect.DeepEqual(c, same) {
		t.Fatal("link no-op")
	}
	for _, test := range []struct {
		kind  string
		input map[string]string
		want  error
	}{
		{"mission.link", map[string]string{"mission_id": "m1", "project_id": p2}, store.ErrConflict},
		{"mission.relink", map[string]string{"mission_id": "m1", "from_project_id": p2, "to_project_id": p}, store.ErrConflict},
		{"mission.unlink", map[string]string{"mission_id": "m1", "project_id": p2}, store.ErrConflict},
		{"mission.link", map[string]string{"mission_id": "missing", "project_id": p}, store.ErrNotFound},
	} {
		raw, _ := json.Marshal(test.input)
		_, err := svc.Execute(catalog.Command{ExpectedRevision: c.Revision, Kind: test.kind, Input: raw})
		if !errors.Is(err, test.want) {
			t.Fatalf("%s: %v", test.kind, err)
		}
	}
	same = execute(t, svc, c.Revision, "mission.relink", map[string]string{"mission_id": "m1", "from_project_id": p, "to_project_id": p})
	if !reflect.DeepEqual(c, same) {
		t.Fatal("relink no-op")
	}
	run("mission.relink", map[string]string{"mission_id": "m1", "from_project_id": p, "to_project_id": p2}, fixed("m1"))
	run("mission.unlink", map[string]string{"mission_id": "m1", "project_id": p2}, fixed("m1"))
	run("resource.unbind", map[string]string{"id": binding}, fixed(binding))
	events, err := svc.Events(0, 100)
	if err != nil || len(events) != len(checks) {
		t.Fatal(events, err)
	}
	for i, want := range checks {
		e := events[i]
		if e.CatalogRevision != int64(i+1) || e.Kind != want.kind || !reflect.DeepEqual(e.EntityIDs, []string{want.id}) {
			t.Fatalf("event %d: %+v want %+v", i, e, want)
		}
	}
	got, _ := s.GetMission("m1")
	missionAfter, _ := json.Marshal(got)
	eventsAfter, _ := s.Events(0, 100)
	if string(missionBefore) != string(missionAfter) || !reflect.DeepEqual(eventsBefore, eventsAfter) {
		t.Fatal("catalog modified mission history")
	}
}

func TestCatalogStrictCommandDecoding(t *testing.T) {
	valid := `{"expected_revision":0,"kind":"project.create","input":{"name":"A","description":""}}`
	if _, err := catalog.DecodeCommand([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{}`, `null`, `[]`, valid + ` {}`, `{"expected_revision":null,"kind":"project.create","input":{}}`,
		`{"expected_revision":-1,"kind":"project.create","input":{}}`, `{"expected_revision":0.0,"kind":"project.create","input":{}}`,
		`{"expected_revision":0,"kind":"project.create","input":{},"extra":1}`,
		`{"expected_revision":0,"kind":"project.create","kind":"project.create","input":{}}`,
		`{"expected_revision":0,"kind":"project.create","input":{"x":{"y":1,"y":2}}}`,
		`{"Expected_Revision":0,"kind":"project.create","input":{}}`,
		`{"expected_revision":0,"kind":null,"input":{}}`,
		`{"expected_revision":0,"kind":"project.create","input":null}`,
		"{\"expected_revision\":0,\"kind\":\"project.create\",\"input\":{\"name\":\"\xff\",\"description\":\"\"}}",
	} {
		if _, err := catalog.DecodeCommand([]byte(raw)); err == nil {
			t.Errorf("accepted invalid envelope %q", raw)
		}
	}
	inputs := map[string]string{
		"project.create": `{"name":"A","description":""}`, "project.update": `{"id":"missing","name":"A","description":""}`,
		"target.create": `{"project_id":"missing","name":"A","platform":"ios","format":"ipa","architecture":"arm64","distribution":"testflight"}`, "target.rename": `{"id":"missing","name":"A"}`,
		"environment.create": `{"project_id":"missing","name":"A","tier":"dev"}`, "environment.rename": `{"id":"missing","name":"A"}`,
		"resource.create": `{"name":"A","kind":"storage","owner_project_id":null,"provider":"manual","account":"","region":"","external_id":""}`, "resource.rename": `{"id":"missing","name":"A"}`,
		"resource.bind": `{"project_id":"missing","environment_id":"missing","resource_id":"missing","purpose":"db"}`, "resource.unbind": `{"id":"missing"}`,
		"mission.link": `{"mission_id":"missing","project_id":"missing"}`, "mission.relink": `{"mission_id":"missing","from_project_id":"missing","to_project_id":"missing"}`, "mission.unlink": `{"mission_id":"missing","project_id":"missing"}`,
	}
	svc, _ := newService(t)
	for kind, raw := range inputs {
		t.Run(kind, func(t *testing.T) {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal([]byte(raw), &fields)
			for key := range fields {
				copyFields := map[string]json.RawMessage{}
				for k, v := range fields {
					copyFields[k] = v
				}
				delete(copyFields, key)
				body, _ := json.Marshal(copyFields)
				_, err := svc.Execute(catalog.Command{Kind: kind, Input: body})
				if !errors.Is(err, domain.ErrInvalidCatalog) {
					t.Errorf("missing %s: %v", key, err)
				}
				if key != "owner_project_id" {
					copyFields[key] = json.RawMessage(`null`)
					body, _ = json.Marshal(copyFields)
					_, err = svc.Execute(catalog.Command{Kind: kind, Input: body})
					if !errors.Is(err, domain.ErrInvalidCatalog) {
						t.Errorf("null %s: %v", key, err)
					}
				}
			}
			for _, key := range []string{"unexpected", "id", "revision", "protected", "status"} {
				body := raw[:len(raw)-1] + `,"` + key + `":true}`
				_, err := svc.Execute(catalog.Command{Kind: kind, Input: json.RawMessage(body)})
				if !errors.Is(err, domain.ErrInvalidCatalog) {
					t.Errorf("injected %s: %v", key, err)
				}
			}
			_, err := svc.Execute(catalog.Command{Kind: kind, Input: json.RawMessage(raw[:len(raw)-1] + `,"name":"duplicate","name":"duplicate"}`)})
			if !errors.Is(err, domain.ErrInvalidCatalog) {
				t.Fatal("duplicates", err)
			}
			for _, bad := range []string{raw + ` {}`, `null`, `[]`, "{\"name\":\"\xff\"}"} {
				_, err := svc.Execute(catalog.Command{Kind: kind, Input: json.RawMessage(bad)})
				if !errors.Is(err, domain.ErrInvalidCatalog) {
					t.Fatal("malformed input", err)
				}
			}
		})
	}
	_, err := svc.Execute(catalog.Command{Kind: "deploy", Input: json.RawMessage(`{}`)})
	if !errors.Is(err, catalog.ErrUnknownCommand) {
		t.Fatal(err)
	}
}

func TestCatalogRejectsMalformedRelationshipIDs(t *testing.T) {
	svc, _ := newService(t)
	for _, test := range []struct{ kind, raw string }{
		{"project.update", `{"id":"","name":"A","description":""}`},
		{"target.rename", `{"id":"bad/id","name":"A"}`},
		{"environment.create", `{"project_id":"bad id","name":"A","tier":"dev"}`},
		{"resource.create", `{"name":"A","kind":"service","owner_project_id":"","provider":"manual","account":"","region":"","external_id":""}`},
		{"mission.link", `{"mission_id":"bad/id","project_id":"p1"}`},
		{"mission.relink", `{"mission_id":"m1","from_project_id":"","to_project_id":"p1"}`},
	} {
		_, err := svc.Execute(catalog.Command{Kind: test.kind, Input: json.RawMessage(test.raw)})
		if !errors.Is(err, domain.ErrInvalidCatalog) {
			t.Errorf("%s invalid ID returned %v", test.kind, err)
		}
	}
}

func TestCatalogUnlinkGuardsUnknownSourceProject(t *testing.T) {
	svc, s := newService(t)
	if _, err := s.CreateMission(domain.Mission{ID: "m1"}); err != nil {
		t.Fatal(err)
	}
	c := execute(t, svc, 0, "project.create", map[string]string{"name": "A", "description": ""})
	c = execute(t, svc, c.Revision, "mission.link", map[string]string{"mission_id": "m1", "project_id": c.Projects[0].ID})
	_, err := svc.Execute(catalog.Command{ExpectedRevision: c.Revision, Kind: "mission.unlink", Input: json.RawMessage(`{"mission_id":"m1","project_id":"wrong-source"}`)})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("wrong source must conflict: %v", err)
	}
}

func TestCatalogSharedBindingsConflictAndRenameNoops(t *testing.T) {
	svc, _ := newService(t)
	c := execute(t, svc, 0, "project.create", map[string]string{"name": "A", "description": ""})
	p1 := c.Projects[0].ID
	c = execute(t, svc, c.Revision, "project.create", map[string]string{"name": "B", "description": ""})
	p2 := c.Projects[1].ID
	c = execute(t, svc, c.Revision, "environment.create", map[string]string{"project_id": p1, "name": "dev", "tier": "dev"})
	e1 := c.Environments[0].ID
	c = execute(t, svc, c.Revision, "environment.create", map[string]string{"project_id": p2, "name": "dev", "tier": "dev"})
	e2 := c.Environments[1].ID
	input := map[string]any{"name": "shared", "kind": "database", "owner_project_id": nil, "provider": "manual", "account": "", "region": "", "external_id": ""}
	c = execute(t, svc, c.Revision, "resource.create", input)
	r1 := c.Resources[0].ID
	c = execute(t, svc, c.Revision, "resource.create", input)
	r2 := c.Resources[1].ID
	for _, pair := range [][2]string{{p1, e1}, {p2, e2}} {
		c = execute(t, svc, c.Revision, "resource.bind", map[string]string{"project_id": pair[0], "environment_id": pair[1], "resource_id": r1, "purpose": "db"})
	}
	if len(c.Bindings) != 2 || c.Resources[0].OwnerProjectID != nil {
		t.Fatal(c)
	}
	badBinding, _ := json.Marshal(map[string]string{"project_id": p1, "environment_id": e1, "resource_id": r2, "purpose": "db"})
	_, err := svc.Execute(catalog.Command{ExpectedRevision: c.Revision, Kind: "resource.bind", Input: badBinding})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatal("occupied slot", err)
	}
	c = execute(t, svc, c.Revision, "target.create", map[string]string{"project_id": p1, "name": "desktop", "platform": "macos", "format": "dmg", "architecture": "universal", "distribution": "direct"})
	for _, test := range []struct{ kind, id, name string }{{"target.rename", c.Targets[0].ID, "desktop"}, {"environment.rename", e1, "dev"}, {"resource.rename", r1, "shared"}} {
		same := execute(t, svc, c.Revision, test.kind, map[string]string{"id": test.id, "name": test.name})
		if !reflect.DeepEqual(same, c) {
			t.Fatalf("%s no-op changed timestamps/revisions", test.kind)
		}
	}
	before := c
	c = execute(t, svc, c.Revision, "resource.rename", map[string]string{"id": r1, "name": "new"})
	if c.Resources[0].Revision != 2 || c.Resources[0].CreatedAt != before.Resources[0].CreatedAt || c.Resources[0].UpdatedAt == before.Resources[0].UpdatedAt || !reflect.DeepEqual(c.Projects, before.Projects) || !reflect.DeepEqual(c.Bindings, before.Bindings) {
		t.Fatal("incorrect entity timestamp/revision stamping")
	}
	for _, test := range []struct{ kind, raw string }{{"resource.unbind", `{"id":"absent"}`}, {"mission.unlink", `{"mission_id":"absent","project_id":"absent"}`}, {"mission.relink", `{"mission_id":"absent","from_project_id":"absent","to_project_id":"` + p1 + `"}`}} {
		_, err := svc.Execute(catalog.Command{ExpectedRevision: c.Revision, Kind: test.kind, Input: json.RawMessage(test.raw)})
		if !errors.Is(err, store.ErrNotFound) {
			t.Fatal(test.kind, err)
		}
	}
	events, err := svc.Events(2, 2)
	if err != nil || len(events) != 2 || events[0].Seq != 3 || events[1].Seq != 4 {
		t.Fatal(events, err)
	}
	for _, params := range [][2]int64{{-1, 100}, {0, 0}, {0, 101}} {
		if _, err = svc.Events(params[0], int(params[1])); !errors.Is(err, domain.ErrInvalidCatalog) {
			t.Fatal("pagination", err)
		}
	}
}
