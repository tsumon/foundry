package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"foundry/internal/domain"
)

func routeStoreFixture(t *testing.T, s *Store) domain.ModelRoute {
	t.Helper()
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "route-account", Name: "Synthetic", Provider: "openai", AuthMethod: "api_key", BillingMode: "api_credits", BaseURL: "http://127.0.0.1:1234/v1", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateModelRoute(domain.ModelRoute{ID: "route", AccountID: a.ID, Adapter: "openai_api_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "explicit_api", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutProfile(domain.Profile{ID: "routed", AccountID: a.ID, RouteID: r.ID, Protocol: r.Protocol, BaseURL: r.BaseURL, Model: "fake"}); err != nil {
		t.Fatal(err)
	}
	if err = s.PutSettings(domain.Settings{DefaultProfileID: "routed"}); err != nil {
		t.Fatal(err)
	}
	return r
}

func routedMission(t *testing.T, s *Store, id string) domain.Mission {
	t.Helper()
	def, p, snapshot, err := s.ResolveMissionModels("")
	if err != nil {
		t.Fatal(err)
	}
	return domain.Mission{ID: id, WorkspaceMode: "account_snapshot_v2", Spec: domain.Spec{ProfileID: def}, Profiles: p, ModelSnapshot: snapshot, State: "draft", Budget: domain.Budget{MaxCalls: 16}}
}

func TestR5BRoutesAreImmutableAndResolveAllAccountRoles(t *testing.T) {
	s := openTestStore(t)
	r := routeStoreFixture(t, s)
	got, err := s.GetModelRoute(r.ID)
	if err != nil || got != r || got.CreatedAt == "" {
		t.Fatalf("route=%+v %v", got, err)
	}
	list, err := s.ListModelRoutes()
	if err != nil || len(list) != 1 || list[0] != r {
		t.Fatal(list, err)
	}
	if _, err = s.CreateModelRoute(r); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate=%v", err)
	}
	for _, query := range []string{`UPDATE model_routes SET adapter='kimi_api_chat' WHERE id='route'`, `DELETE FROM model_routes WHERE id='route'`} {
		if _, err = s.db.Exec(query); err == nil {
			t.Fatal("route history modified")
		}
	}
	bad := r
	bad.ID = "bad"
	bad.AccountID = "missing"
	if _, err = s.CreateModelRoute(bad); err == nil {
		t.Fatal("missing account accepted")
	}
	m := routedMission(t, s, "routed")
	if m.ModelSnapshot.SchemaVersion != 2 || m.ModelSnapshot.Roles[domain.Worker].Route.ID != r.ID {
		t.Fatal("missing frozen route")
	}
	m, err = s.CreateMission(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateMission(m.ID, m.Revision, "tamper", func(m *domain.Mission) error {
		m.ModelSnapshot.Roles[domain.Worker].Route.ExtraUsage = "unknown"
		m.ModelSnapshot.Digest, _ = domain.ModelSnapshotDigest(*m.ModelSnapshot)
		return nil
	}); err == nil {
		t.Fatal("snapshot route mutated")
	}
	p, _ := s.GetProfile("routed")
	p.ID = "implicit"
	p.RouteID = ""
	if err = s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if err = s.PutSettings(domain.Settings{DefaultProfileID: "routed", RoleBindings: map[domain.Role]string{domain.Worker: "implicit"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.ResolveMissionModels(""); err == nil {
		t.Fatal("mixed explicit/implicit account routes accepted")
	}
	p.AccountID = ""
	if err = s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if _, _, snapshot, err := s.ResolveMissionModels(""); err != nil || snapshot.SchemaVersion != 2 || snapshot.Roles[domain.Worker].Account != nil {
		t.Fatal("legacy mixed role rejected", err)
	}
	old, err := s.GetMission(m.ID)
	if err != nil || !reflect.DeepEqual(old.ModelSnapshot, m.ModelSnapshot) {
		t.Fatal("historical routes followed settings", err)
	}
}

func TestR5BRouteCreationRechecksConfigurationDrift(t *testing.T) {
	s := openTestStore(t)
	r := routeStoreFixture(t, s)
	m := routedMission(t, s, "stale-route")
	r.ID = "route-new"
	if _, err := s.CreateModelRoute(r); err != nil {
		t.Fatal(err)
	}
	p, _ := s.GetProfile("routed")
	p.RouteID = r.ID
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMission(m); !errors.Is(err, ErrConflict) {
		t.Fatal("stale route snapshot committed", err)
	}
	events, _ := s.Events(0, 100)
	if len(events) != 0 {
		t.Fatal("stale creation left event")
	}
}

func TestR5BRouteCreationRequiresExactAccountRevisionAtomically(t *testing.T) {
	s := openTestStore(t)
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "route-revision-account", Name: "Synthetic", Provider: "openai", AuthMethod: "api_key", BillingMode: "api_credits", BaseURL: "https://api.openai.com/v1", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	route := domain.ModelRoute{ID: "route-revision", AccountID: a.ID, Adapter: "openai_api_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "explicit_api", ExtraUsage: "off"}
	if _, err = s.CreateModelRouteAtAccountRevision(route, a.Revision+1); !errors.Is(err, ErrConflict) {
		t.Fatalf("future revision accepted: %v", err)
	}
	if _, err = s.GetModelRoute(route.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale create left a route: %v", err)
	}
	a.Name = "Changed"
	a, err = s.SaveModelAccount(a, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateModelRouteAtAccountRevision(route, a.Revision-1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision accepted: %v", err)
	}
	if _, err = s.CreateModelRouteAtAccountRevision(route, a.Revision); err != nil {
		t.Fatalf("exact revision rejected: %v", err)
	}
}

func TestR5BOAuthSnapshotWithoutBindingIsReadableButNotRunnable(t *testing.T) {
	s := openTestStore(t)
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "oauth", Name: "Synthetic OAuth", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", BaseURL: "http://127.0.0.1:1234/v1", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateModelRoute(domain.ModelRoute{ID: "oauth-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutProfile(domain.Profile{ID: "oauth-profile", AccountID: a.ID, RouteID: r.ID, Protocol: r.Protocol, BaseURL: r.BaseURL, Model: "fake"}); err != nil {
		t.Fatal(err)
	}
	if err = s.PutSettings(domain.Settings{DefaultProfileID: "oauth-profile"}); err != nil {
		t.Fatal(err)
	}
	m := routedMission(t, s, "oauth-mission")
	if m.Profiles[domain.Worker].CredentialVersion != 0 || m.ModelSnapshot.Roles[domain.Worker].Authorization != nil {
		t.Fatal("missing authorization invented")
	}
	m, err = s.CreateMission(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckModelCall(m.ID, domain.Worker); err == nil {
		t.Fatal("missing OAuth authorization runnable")
	}
}

// Rebuild from R5A's real schema with rows, credential foreign keys, and custom
// triggers. Losing rows, trigger restoration, or FK enforcement must fail here.
func TestR5BAccountAuthCheckMigrationPreservesHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := os.ReadFile("testdata/r5a_model_accounts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(legacy)); err != nil {
		t.Fatal(err)
	}
	oldStore := &Store{db: db}
	a, err := oldStore.SaveModelAccount(domain.ModelAccount{ID: "old", Name: "Old", Provider: "openai", AuthMethod: "api_key", BillingMode: "api_credits", BaseURL: "https://api.openai.com/v1", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	a = snapshotVersion(t, oldStore, a, "old-op")
	if _, err = db.Exec(`CREATE TRIGGER retained_custom BEFORE DELETE ON model_accounts BEGIN SELECT RAISE(ABORT,'preserved'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE UNIQUE INDEX retained_name_constraint ON model_accounts(name)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetModelAccount(a.ID)
	if err != nil || got != a {
		t.Fatal("old account changed", got, err)
	}
	v, err := s.GetModelCredential(a.ID, 1)
	if err != nil || v.State != "available" || v.SecretRef != "foundry-model-account-fake-old-op" {
		t.Fatal("credential changed", v, err)
	}
	_, err = s.SaveModelAccount(domain.ModelAccount{ID: "oauth-new", Name: "New", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", BaseURL: "https://api.openai.com/v1"}, 0)
	if err != nil {
		t.Fatal("migration did not permit OAuth", err)
	}
	for _, q := range []string{`UPDATE model_accounts SET auth_method='openai_siwc' WHERE id='old'`, `DELETE FROM model_accounts WHERE id='old'`, `DELETE FROM model_credential_versions WHERE account_id='old'`, `INSERT INTO model_accounts VALUES('bad',1,'Bad','kimi','https://example.com','openai_siwc','subscription',1,0,'then','then')`} {
		if _, err = s.db.Exec(q); err == nil {
			t.Fatal("lost constraint", q)
		}
	}
	if _, err = s.db.Exec(`INSERT INTO model_accounts VALUES('duplicate',1,'Old','openai','https://api.openai.com/v1','api_key','api_credits',1,0,'then','then')`); err == nil {
		t.Fatal("lost unique index constraint")
	}
	var enabled int
	if err = s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatal("foreign keys disabled", err)
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign key violation")
	}
	rows.Close()
	var integrity string
	if err = s.db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
}

func TestR5BMigrationFailureRollsBackAndRestoresForeignKeys(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "invalid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	legacy, err := os.ReadFile("testdata/r5a_model_accounts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(legacy)); err != nil {
		t.Fatal(err)
	}
	// Corrupt historical input is diagnosed, never committed as a repaired DB.
	if _, err = db.Exec(`INSERT INTO model_credential_versions VALUES('missing',1,'bad-op','pending','fake-ref',0,'then','then')`); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err = db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='model_accounts'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = (&Store{db: db}).initializeModelAccounts(); err == nil {
		t.Fatal("invalid foreign key migration committed")
	}
	if err = db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='model_accounts'`).Scan(&after); err != nil || before != after {
		t.Fatal("failed migration changed original schema", err)
	}
	var enabled int
	if err = db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatal("failed migration disabled enforcement", err)
	}
}
