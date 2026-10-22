package store

import (
	"foundry/internal/domain"
	"path/filepath"
	"testing"
)

func TestWindowsNativeSQLitePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native.sqlite")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.CreateMission(domain.Mission{ID: "native", Title: "persisted", State: "draft"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	got, e := s.GetMission(m.ID)
	if e != nil || got.Title != "persisted" || got.Revision != m.Revision {
		t.Fatal("SQLite persistence failed", got, e)
	}
}
