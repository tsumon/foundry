package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func capabilityConnection(t *testing.T, s *Store) domain.MCPConnection {
	t.Helper()
	c, err := s.SaveMCPConnection(domain.MCPConnection{ID: "conn", Name: "Fixture", Endpoint: "https://example.com/mcp", Protocol: domain.MCPProtocol, Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Removing additive initialization or array initialization breaks old/empty DB reads.
func TestCapabilitiesEmptyAndOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE profiles (id TEXT PRIMARY KEY,data BLOB NOT NULL); INSERT INTO profiles VALUES('old','{"id":"old","name":"Before"}')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := s.Capabilities()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(c)
	for _, key := range []string{"connections", "snapshots", "discoveries", "skills", "repositories", "git_observations"} {
		if !strings.Contains(string(raw), `"`+key+`":[]`) {
			t.Fatalf("not an empty array: %s", raw)
		}
	}
	if c.ToolExecution || c.SkillExecution || c.GitWrite {
		t.Fatal("execution enabled")
	}
	callable := map[string]bool{}
	for _, item := range c.BuiltinCapabilities {
		callable[item.ID] = item.AgentCallable
	}
	for _, id := range []string{"repo.list", "repo.read", "repo.search"} {
		if !callable[id] {
			t.Errorf("controlled Researcher built-in %q is not truthfully callable", id)
		}
	}
	for _, id := range []string{"snapshot", "candidate_patch", "docker_verification", "local_export"} {
		if callable[id] {
			t.Errorf("controller-only built-in %q became agent callable", id)
		}
	}
	p, err := s.GetProfile("old")
	if err != nil || p.Name != "Before" {
		t.Fatalf("old data changed: %+v %v", p, err)
	}
}

// Removing CAS or pending-operation guards lets configuration race dispatch.
func TestMCPConnectionCASAndValidation(t *testing.T) {
	s := openTestStore(t)
	c := capabilityConnection(t, s)
	if c.Revision != 1 || c.CreatedAt == "" {
		t.Fatalf("missing metadata: %+v", c)
	}
	c.Name = "Changed"
	if _, err := s.SaveMCPConnection(c, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	changed, err := s.SaveMCPConnection(c, 1)
	if err != nil || changed.Revision != 2 || changed.CreatedAt != c.CreatedAt {
		t.Fatalf("update: %+v %v", changed, err)
	}
	for _, endpoint := range []string{"https://user:secret@example.com", "https://example.com?secret=key", "https://example.com#secret", "http://example.com", "http://localhost/mcp"} {
		bad := changed
		bad.Endpoint = endpoint
		if _, err := s.SaveMCPConnection(bad, 2); !errors.Is(err, domain.ErrInvalidCapability) {
			t.Fatalf("accepted unsafe endpoint %s: %v", endpoint, err)
		}
	}
	if _, err := s.GetMCPConnection("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

// Overwriting immutable rows or accepting stale revisions loses original text.
func TestSkillRevisionHistoryAndCAS(t *testing.T) {
	s := openTestStore(t)
	first, err := s.SaveSkillRevision(domain.SkillRevision{ID: "skill", Name: "review", Description: "Review text", Source: "manual", Digest: strings.Repeat("a", 64), Files: []domain.SkillFile{{Path: "SKILL.md", Content: "first"}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	next := first
	next.Files = []domain.SkillFile{{Path: "SKILL.md", Content: "second"}}
	next.Digest = strings.Repeat("b", 64)
	if _, err = s.SaveSkillRevision(next, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	second, err := s.SaveSkillRevision(next, 1)
	if err != nil || second.Revision != 2 {
		t.Fatalf("append: %+v %v", second, err)
	}
	old, err := s.GetSkillRevision("skill", 1)
	if err != nil || old.Files[0].Content != "first" {
		t.Fatalf("history: %+v %v", old, err)
	}
	duplicate := first
	duplicate.ID = "other"
	if _, err = s.SaveSkillRevision(duplicate, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("same name: %v", err)
	}
	if _, err = s.db.Exec(`UPDATE skill_revisions SET data='{}' WHERE id='skill'`); err == nil {
		t.Fatal("SQL overwrote history")
	}
	if _, err = s.db.Exec(`DELETE FROM skill_revisions WHERE id='skill'`); err == nil {
		t.Fatal("SQL deleted history")
	}
}

// Accepting an observation for an old config falsely reports the new path checked.
func TestGitObservationsBoundToRevision(t *testing.T) {
	s := openTestStore(t)
	repo, err := s.SaveGitRepository(domain.GitRepository{ID: "repo", Name: "Fixture", Path: "/tmp/fixture"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	obs := domain.GitObservation{ID: "obs", RepositoryID: repo.ID, RepositoryRevision: 1, Root: "/tmp/fixture", Branch: "main", HEAD: strings.Repeat("a", 40), Status: "succeeded", WorkingTreeStatus: "not_checked"}
	saved, err := s.SaveGitObservation(obs)
	if err != nil || saved.CheckedAt == "" {
		t.Fatalf("observe: %+v %v", saved, err)
	}
	exact, err := s.GetGitObservation(obs.ID)
	if err != nil || exact.ID != obs.ID || exact.RepositoryID != repo.ID || exact.RepositoryRevision != repo.Revision {
		t.Fatalf("exact observation: %+v %v", exact, err)
	}
	if _, err := s.GetGitObservation("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing observation error = %v, want ErrNotFound", err)
	}
	repo.Path = "/tmp/other"
	if _, err = s.SaveGitRepository(repo, 1); err != nil {
		t.Fatal(err)
	}
	obs.ID = "stale"
	if _, err = s.SaveGitObservation(obs); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale accepted: %v", err)
	}
	obs.RepositoryRevision = 2
	obs.WorkingTreeStatus = "clean"
	if _, err = s.SaveGitObservation(obs); !errors.Is(err, domain.ErrInvalidCapability) {
		t.Fatalf("fabricated clean: %v", err)
	}
	repo.ProjectID = "missing"
	if _, err = s.SaveGitRepository(repo, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project accepted: %v", err)
	}
	all, err := s.Capabilities()
	if err != nil || len(all.GitObservations) != 1 || all.GitObservations[0].RepositoryRevision != 1 {
		t.Fatalf("history missing: %+v %v", all, err)
	}
}

func TestCapabilityInvalidWritesDoNotCreateRows(t *testing.T) {
	s := openTestStore(t)
	for _, files := range [][]domain.SkillFile{
		{{Path: "../SKILL.md", Content: "text"}},
		{{Path: "SKILL.md", Content: "text"}, {Path: "scripts/run.sh", Content: "text"}},
		{{Path: "SKILL.md", Content: "text"}, {Path: "skill.md", Content: "other"}},
		{{Path: "SKILL.md", Content: string([]byte{0xff})}},
	} {
		_, err := s.SaveSkillRevision(domain.SkillRevision{ID: "bad", Name: "bad", Description: "text", Files: files, Digest: strings.Repeat("a", 64)}, 0)
		if !errors.Is(err, domain.ErrInvalidCapability) {
			t.Fatalf("unsafe files accepted: %v", err)
		}
	}
	if _, err := s.SaveGitRepository(domain.GitRepository{ID: "repo", Name: "Relative", Path: "relative"}, 0); !errors.Is(err, domain.ErrInvalidCapability) {
		t.Fatalf("relative registration: %v", err)
	}
	c, err := s.Capabilities()
	if err != nil || len(c.Skills) != 0 || len(c.Repositories) != 0 {
		t.Fatalf("invalid mutation persisted: %+v %v", c, err)
	}
}
