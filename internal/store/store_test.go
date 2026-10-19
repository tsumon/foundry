package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func TestUpdateMissionConflictDoesNotCreateEvent(t *testing.T) {
	s := openTestStore(t)
	created, err := s.CreateMission(domain.Mission{ID: "m1", Title: "before", State: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Events(0, 100)
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.UpdateMission(created.ID, created.Revision+1, "mission.updated", func(m *domain.Mission) error {
		m.Title = "after"
		return nil
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	after, err := s.Events(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("event count changed from %d to %d", len(before), len(after))
	}
	stored, err := s.GetMission(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Title != "before" {
		t.Fatalf("stale mutation persisted title %q", stored.Title)
	}
}

func TestUpdateMissionMutationErrorIsAtomic(t *testing.T) {
	s := openTestStore(t)
	created, err := s.CreateMission(domain.Mission{ID: "m1", Title: "before", State: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("stop")
	_, err = s.UpdateMission(created.ID, created.Revision, "mission.updated", func(m *domain.Mission) error {
		m.Title = "after"
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want mutation error", err)
	}
	events, err := s.Events(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want only create event", len(events))
	}
	stored, err := s.GetMission(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Title != "before" || stored.Revision != created.Revision {
		t.Fatalf("mutation leaked into stored mission: %+v", stored)
	}
}

func TestCreateAndUpdateMissionPersistRevisionAndEvents(t *testing.T) {
	s := openTestStore(t)
	created, err := s.CreateMission(domain.Mission{ID: "m1", Title: "before", State: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatalf("create revision = %d, want 1", created.Revision)
	}
	updated, err := s.UpdateMission("m1", 1, "mission.planning", func(m *domain.Mission) error {
		m.State = "planning"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.State != "planning" {
		t.Fatalf("updated mission = %+v", updated)
	}
	events, err := s.Events(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Revision != 1 || events[1].Revision != 2 || events[1].Kind != "mission.planning" {
		t.Fatalf("events = %+v", events)
	}
}

func TestProfilePersistenceNeverContainsSecret(t *testing.T) {
	const secret = "fixture-provider-key-never-persist"
	dir := t.TempDir()
	path := filepath.Join(dir, "foundry.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := domain.Profile{ID: "p1", Name: "Direct API", Protocol: "responses", BaseURL: "https://example.com/v1", Model: "model", HasKey: true}
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := readProfileData(path, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), secret) || strings.Contains(string(b), "api_key") || strings.Contains(string(b), "Bearer") {
		t.Fatalf("profile JSON contains credential material: %s", b)
	}
	if !strings.Contains(string(b), `"has_key":true`) {
		t.Fatalf("profile JSON does not retain key-presence flag: %s", b)
	}
	databaseBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(databaseBytes), secret) {
		t.Fatal("SQLite file contains the fixture provider key")
	}
}

func TestProfilesAndSettingsRoundTrip(t *testing.T) {
	s := openTestStore(t)
	p := domain.Profile{ID: "p1", Name: "Local", Kind: "magpie", Protocol: "chat_completions", BaseURL: "http://localhost:3425/v1", Model: "configured", HasKey: false}
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetProfile("p1")
	if err != nil || got != p {
		t.Fatalf("GetProfile() = %+v, %v", got, err)
	}
	profiles, err := s.ListProfiles()
	if err != nil || len(profiles) != 1 || profiles[0] != p {
		t.Fatalf("ListProfiles() = %+v, %v", profiles, err)
	}
	want := domain.Settings{DefaultProfileID: "p1", RoleBindings: map[domain.Role]string{domain.Worker: "p1"}}
	if err := s.PutSettings(want); err != nil {
		t.Fatal(err)
	}
	settings, err := s.GetSettings()
	if err != nil || settings.DefaultProfileID != "p1" || settings.RoleBindings[domain.Worker] != "p1" {
		t.Fatalf("GetSettings() = %+v, %v", settings, err)
	}
}

func TestPutProfileRejectsCredentialBearingURLWithoutPersistingIt(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		baseURL string
	}{
		{name: "userinfo", secret: "fixture-userinfo-secret", baseURL: "https://user:fixture-userinfo-secret@example.com/v1"},
		{name: "query", secret: "fixture-query-secret", baseURL: "https://example.com/v1?api_key=fixture-query-secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "foundry.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			baseline := domain.Profile{ID: "p1", Name: "Safe", Protocol: "responses", BaseURL: "https://example.com/v1", Model: "m"}
			if err := s.PutProfile(baseline); err != nil {
				t.Fatal(err)
			}
			malicious := baseline
			malicious.BaseURL = tt.baseURL
			if err := s.PutProfile(malicious); err == nil {
				t.Error("credential-bearing provider URL was accepted")
			}
			stored, err := s.GetProfile("p1")
			if err != nil {
				t.Fatal(err)
			}
			if stored != baseline {
				t.Error("rejected profile changed stored value")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(path + "*")
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range files {
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), tt.secret) {
					t.Errorf("SQLite persistence contains rejected credential marker in %s", filepath.Base(file))
				}
			}
		})
	}
}

func TestMissingRecordsReturnNotFound(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.GetMission("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMission error = %v, want ErrNotFound", err)
	}
	if _, err := s.GetProfile("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProfile error = %v, want ErrNotFound", err)
	}
}

func TestEmptyListsAreJSONArrays(t *testing.T) {
	s := openTestStore(t)
	missions, err := s.ListMissions()
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := s.ListProfiles()
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if missions == nil || profiles == nil || events == nil {
		t.Fatalf("empty lists must be non-nil: missions=%v profiles=%v events=%v", missions, profiles, events)
	}
}

func TestUpdateMissionRejectsIdentityChange(t *testing.T) {
	s := openTestStore(t)
	created, err := s.CreateMission(domain.Mission{ID: "m1", State: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateMission("m1", created.Revision, "mission.updated", func(m *domain.Mission) error {
		m.ID = "m2"
		return nil
	})
	if err == nil {
		t.Fatal("mission identity change was accepted")
	}
	stored, err := s.GetMission("m1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != "m1" || stored.Revision != created.Revision {
		t.Fatalf("identity mutation changed stored mission: %+v", stored)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func readProfileData(path, id string) ([]byte, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var data []byte
	err = db.QueryRow(`SELECT data FROM profiles WHERE id = ?`, id).Scan(&data)
	return data, err
}
