package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Catches silently treating a versioned request as a historical creation.
func TestCreateRepositoryImportRequiresSelection(t *testing.T) {
	e, s, repo, data := importFixture(t)
	if err := s.PutProfile(domain.Profile{ID: "test", Name: "Test", Protocol: "chat_completions", BaseURL: "http://127.0.0.1", Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2, -1} {
		r := CreateRequest{Title: "import", Goal: "change", RepoPath: repo, Spec: domain.Spec{Scope: []string{"file.txt"}, Acceptance: []string{"test -f file.txt"}, Image: "local", ProfileID: "test"}, CodingVersion: 1, WorkspaceMode: "generations_v1"}
		raw, _ := json.Marshal(r)
		var fields map[string]any
		json.Unmarshal(raw, &fields)
		fields["import_version"] = version
		raw, _ = json.Marshal(fields)
		json.Unmarshal(raw, &r)
		if m, err := e.Create(context.Background(), r); err == nil {
			t.Fatalf("version %d without selection created %s", version, m.ID)
		}
	}
	missions, err := s.ListMissions()
	if err != nil || len(missions) != 0 {
		t.Fatal(missions, err)
	}
	entries, err := os.ReadDir(data)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}

func createImportRequest(t *testing.T, e *Engine, repo string) CreateRequest {
	t.Helper()
	if err := e.s.PutProfile(domain.Profile{ID: "test", Name: "Test", Protocol: "chat_completions", BaseURL: "http://127.0.0.1", Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	v, err := e.InspectRepository(context.Background(), importRequest(repo))
	if err != nil || v.Status != "ready" {
		t.Fatal(v, err)
	}
	return CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: v.ID, Digest: v.Digest}, CodingVersion: 1, WorkspaceMode: "generations_v1", Title: "import", Goal: "change", RepoPath: repo, Spec: domain.Spec{Scope: v.Spec.Scope, Protected: v.Spec.Protected, Acceptance: v.Spec.Acceptance, Image: v.Spec.Image, ProfileID: "test"}}
}

// Catches HEAD/worktree substitution, missing binding, implicit approvals,
// repeated work on duplicate intent, and a mutable downgrade to old authority.
func TestCreateRepositoryImportFrozenAndConsumed(t *testing.T) {
	e, s, repo, _ := importFixture(t)
	r := createImportRequest(t, e, repo)
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("uncommitted\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := e.Create(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "draft" || len(m.Approvals) != 1 || m.Approvals[0].Kind != "snapshot" || m.CandidateGeneration == nil {
		t.Fatalf("unexpected authority %+v", m)
	}
	b, err := os.ReadFile(filepath.Join(e.dir(m.ID), "base/file.txt"))
	if err != nil || string(b) != "before\n" {
		t.Fatal(string(b), err)
	}
	raw, _ := json.Marshal(m)
	if !strings.Contains(string(raw), `"repository_import":`) || !strings.Contains(string(raw), `"import_version":1`) {
		t.Fatalf("missing provenance %s", raw)
	}
	if err = e.checkSnapshot(m); err != nil {
		t.Fatal(err)
	}
	// Settings and source are no longer dependencies of completed intent or authority.
	if err = os.Rename(repo, repo+"-moved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Rename(repo+"-moved", repo) })
	if err = s.PutProfile(domain.Profile{ID: "test", Name: "changed", Protocol: "chat_completions", BaseURL: "http://127.0.0.2", Model: "changed"}); err != nil {
		t.Fatal(err)
	}
	again, err := e.Create(context.Background(), r)
	if err != nil || again.ID != m.ID {
		t.Fatal(again.ID, err)
	}
	if err = e.checkSnapshot(m); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*CreateRequest){func(r *CreateRequest) { r.Title += "x" }, func(r *CreateRequest) { r.Goal += "x" }, func(r *CreateRequest) { r.Spec.MaxCalls = 15 }, func(r *CreateRequest) {
		sel := *r.ImportSelection
		sel.Digest = strings.Repeat("0", 64)
		r.ImportSelection = &sel
	}} {
		changed := r
		mutate(&changed)
		if _, err = e.Create(context.Background(), changed); err != domain.ImportIntentConflict {
			t.Fatal("consumed selection changed", err)
		}
	}
	if _, err = s.UpdateMission(m.ID, m.Revision, "test", func(x *domain.Mission) error {
		raw, _ := json.Marshal(x)
		var f map[string]any
		json.Unmarshal(raw, &f)
		delete(f, "import_version")
		delete(f, "repository_import")
		raw, _ = json.Marshal(f)
		*x = domain.Mission{}
		return json.Unmarshal(raw, x)
	}); err == nil {
		t.Fatal("binding stripped")
	}
	if err = os.WriteFile(filepath.Join(e.dir(m.ID), "base/file.txt"), []byte("tampered\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if e.checkSnapshot(m) == nil {
		t.Fatal("tampered base accepted")
	}
	if !strings.Contains(marshalReport(m), `"inspection_id"`) {
		t.Fatal("report lacks provenance")
	}
}

// Real SQLite faults bracket the ledger and publication. Removing rollback,
// failure journaling, or the no-replay gate exposes a mission or rewrites files.
func TestCreateRepositoryImportStorageFaultsAndRestart(t *testing.T) {
	for _, kind := range []string{"begin", "prepare", "mission_commit", "operation_commit", "journal_pending", "journal_prepared"} {
		t.Run(kind, func(t *testing.T) {
			_, _, repo, _ := importFixture(t)
			dbPath := filepath.Join(t.TempDir(), "db")
			s, err := store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			data := t.TempDir()
			e := New(s, &scripted{}, Options{DataDir: data, Runner: &fakeRunner{err: errors.New("runner forbidden")}})
			r := createImportRequest(t, e, repo)
			fault, err := sql.Open("sqlite3", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer fault.Close()
			statement := ""
			switch kind {
			case "begin":
				statement = `CREATE TRIGGER fail_import BEFORE INSERT ON repository_import_operations BEGIN SELECT RAISE(ABORT,'PRIVATE_SQL_ERROR'); END`
			case "prepare":
				statement = `CREATE TRIGGER fail_import BEFORE UPDATE ON repository_import_operations WHEN NEW.status='prepared' BEGIN SELECT RAISE(ABORT,'PRIVATE_SQL_ERROR'); END`
			case "mission_commit":
				statement = `CREATE TRIGGER fail_import BEFORE INSERT ON missions BEGIN SELECT RAISE(ABORT,'PRIVATE_SQL_ERROR'); END`
			case "operation_commit":
				statement = `CREATE TRIGGER fail_import BEFORE UPDATE ON repository_import_operations WHEN NEW.status='committed' BEGIN SELECT RAISE(ABORT,'PRIVATE_SQL_ERROR'); END`
			case "journal_pending":
				statement = `CREATE TRIGGER fail_import BEFORE UPDATE ON repository_import_operations BEGIN SELECT RAISE(ABORT,'PRIVATE_SQL_ERROR'); END`
			case "journal_prepared":
				statement = `CREATE TRIGGER fail_import BEFORE UPDATE ON repository_import_operations WHEN NEW.status IN ('committed','failed') BEGIN SELECT RAISE(ABORT,'PRIVATE_SQL_ERROR'); END`
			}
			if _, err = fault.Exec(statement); err != nil {
				t.Fatal(err)
			}
			before := importSourceDigest(t, repo)
			m, err := e.Create(context.Background(), r)
			if err == nil || m.ID != "" || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal(m, err)
			}
			if kind == "begin" && err != domain.ImportStorageFailed {
				t.Fatal("ledger storage fault mislabeled as consumed selection", err)
			}
			if before != importSourceDigest(t, repo) {
				t.Fatal("source changed")
			}
			missions, err := s.ListMissions()
			if err != nil || len(missions) != 0 {
				t.Fatal(missions, err)
			}
			ops, err := s.ListRepositoryImportOperations()
			if err != nil {
				t.Fatal(err)
			}
			if kind == "begin" {
				if len(ops) != 0 {
					t.Fatal(ops)
				}
				entries, _ := os.ReadDir(data)
				if len(entries) != 0 {
					t.Fatal("write before journal", entries)
				}
				s.Close()
				return
			}
			if len(ops) != 1 {
				t.Fatal(ops)
			}
			op := ops[0]
			want := "failed"
			if kind == "journal_pending" {
				want = "pending"
			}
			if kind == "journal_prepared" {
				want = "prepared"
			}
			if op.Status != want {
				t.Fatal(op)
			}
			published := kind == "mission_commit" || kind == "operation_commit" || kind == "journal_prepared"
			_, statErr := os.Stat(e.dir(op.MissionID))
			if published && statErr != nil || !published && !os.IsNotExist(statErr) {
				t.Fatal("publication state", statErr)
			}
			// Remove failure source, restart, and explicitly repeat: no adoption/retry.
			if _, err = fault.Exec(`DROP TRIGGER fail_import`); err != nil {
				t.Fatal(err)
			}
			artifacts := importSourceDigest(t, data)
			if err = e.Close(); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			e = New(s, &scripted{}, Options{DataDir: data, Runner: &fakeRunner{}})
			defer e.Close()
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err = e.Create(context.Background(), r); err != domain.ImportIncomplete {
				t.Fatal("replayed unfinished import", err)
			}
			if artifacts != importSourceDigest(t, data) {
				t.Fatal("restart changed artifacts")
			}
			after, err := s.GetRepositoryImportOperation(op.ID)
			if err != nil || after.Revision != op.Revision || after.Status != op.Status {
				t.Fatal(after, err)
			}
		})
	}
}

func TestCreateRepositoryImportRegistryChangesAndConcurrentIntent(t *testing.T) {
	e, s, repo, data := importFixture(t)
	registration, err := s.SaveGitRepository(domain.GitRepository{ID: "repo", Name: "fixture", Path: repo}, 0)
	if err != nil {
		t.Fatal(err)
	}
	request := importRequest("")
	request.RepositoryID = registration.ID
	request.RepositoryRevision = registration.Revision
	v, err := e.InspectRepository(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	r := createImportRequest(t, e, repo)
	r.ImportSelection = &domain.ImportSelection{InspectionID: v.ID, Digest: v.Digest}
	registration.Name = "new revision"
	if _, err = s.SaveGitRepository(registration, registration.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Create(context.Background(), r); err != domain.ImportSourceChanged {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(data)
	if len(entries) != 0 {
		t.Fatal(entries)
	}
	r = createImportRequest(t, e, repo)
	other := New(s, &scripted{}, Options{DataDir: data, Runner: &fakeRunner{}})
	defer other.Close()
	type result struct {
		m   domain.Mission
		err error
	}
	results := make(chan result, 2)
	for _, engine := range []*Engine{e, other} {
		go func(e *Engine) { m, err := e.Create(context.Background(), r); results <- result{m, err} }(engine)
	}
	successes := 0
	for range 2 {
		got := <-results
		if got.err == nil {
			successes++
		} else if got.err != domain.ImportIncomplete {
			t.Fatal(got.err)
		}
	}
	if successes < 1 {
		t.Fatal("no creation")
	}
	missions, err := s.ListMissions()
	if err != nil || len(missions) != 1 {
		t.Fatal(missions, err)
	}
	ops, err := s.ListRepositoryImportOperations()
	if err != nil || len(ops) != 1 || ops[0].Status != "committed" {
		t.Fatal(ops, err)
	}
}

func TestCreateRepositoryImportRejectsRawProvenanceTamper(t *testing.T) {
	for _, kind := range []string{"strip_both", "strip_version", "strip_binding", "foreign_binding", "unknown_binding", "unknown_version", "wrong_version_type"} {
		t.Run(kind, func(t *testing.T) {
			_, _, repo, _ := importFixture(t)
			path := filepath.Join(t.TempDir(), "db")
			s, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			e := New(s, &scripted{}, Options{DataDir: t.TempDir(), Runner: &fakeRunner{}})
			defer e.Close()
			r := createImportRequest(t, e, repo)
			m, err := e.Create(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(m)
			var f map[string]any
			if err = json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "strip_both":
				delete(f, "import_version")
				delete(f, "repository_import")
			case "strip_version":
				delete(f, "import_version")
			case "strip_binding":
				delete(f, "repository_import")
			case "foreign_binding":
				f["repository_import"].(map[string]any)["import_operation_id"] = "foreign"
			case "unknown_binding":
				f["repository_import"].(map[string]any)["future_authority"] = true
			case "unknown_version":
				f["import_version"] = 2
			case "wrong_version_type":
				f["import_version"] = "1"
			}
			raw, _ = json.Marshal(f)
			damage, err := sql.Open("sqlite3", path)
			if err != nil {
				t.Fatal(err)
			}
			defer damage.Close()
			if _, err = damage.Exec(`UPDATE missions SET data=? WHERE id=?`, raw, m.ID); err != nil {
				t.Fatal(err)
			}
			loaded, err := s.GetMission(m.ID)
			if err != nil {
				t.Fatal("must remain inspectable", err)
			}
			if err = e.checkSnapshot(loaded); err == nil {
				t.Fatal("tampered provenance accepted")
			}
			if _, err = e.Plan(context.Background(), loaded.ID, loaded.Revision); err == nil {
				t.Fatal("tampered provenance planned")
			}
			if _, err = s.UpdateMission(m.ID, m.Revision, "repair", func(m *domain.Mission) error { return nil }); err == nil {
				t.Fatal("tampered provenance reserialized")
			}
		})
	}
}

func TestCreateRepositoryImportIndependentApprovalsAndDelivery(t *testing.T) {
	e, s, repo, _ := importFixture(t)
	r := createImportRequest(t, e, repo)
	script := &scripted{outputs: []string{`{"summary":"change","steps":[{"id":"one","title":"change","description":"update","depends_on":[],"files":["file.txt"]}]}`, `{"summary":"changed","edits":[{"path":"file.txt","content":"after\n"}]}`, codingPass, codingPass}}
	e.gateway = &codingReviewGateway{script: script}
	e.runner = &fakeRunner{image: "sha256:fixture"}
	m, err := e.Create(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(script.roles) != 0 || m.Budget.UsedCalls != 0 {
		t.Fatal("creation called model")
	}
	if _, err = e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("import allowed delivery")
	}
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, err = s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "awaiting_approval" {
		t.Fatal(m.State, m.Error)
	}
	// Live HEAD changing now must not participate in any later approval or export.
	writeImportFile(t, repo, "file.txt", "different live source\n")
	if _, err = e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, err = s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "awaiting_delivery" {
		t.Fatal(m.State, m.Error)
	}
	m, err = e.Deliver(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	report, err := e.ReadDelivery(m.ID, "report")
	if err != nil || !strings.Contains(string(report), m.RepositoryImport.InspectionDigest) {
		t.Fatal(string(report), err)
	}
	if err = os.WriteFile(filepath.Join(e.dir(m.ID), "base/file.txt"), []byte("tamper\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"patch", "report"} {
		if _, err = e.ReadDelivery(m.ID, kind); err == nil {
			t.Fatal("tampered base downloaded", kind)
		}
	}
}

func TestCreateRepositoryImportRejectsDrift(t *testing.T) {
	for _, kind := range []string{"head", "spec", "root", "mode", "coding", "tool"} {
		t.Run(kind, func(t *testing.T) {
			e, s, repo, data := importFixture(t)
			r := createImportRequest(t, e, repo)
			switch kind {
			case "head":
				writeImportFile(t, repo, "file.txt", "new\n")
			case "spec":
				r.Spec.Image = "other"
			case "root":
				r.RepoPath = t.TempDir()
			case "mode":
				r.WorkspaceMode = ""
			case "coding":
				r.CodingVersion = 0
			case "tool":
				r.ToolMode = "controlled_read_v1"
			}
			if _, err := e.Create(context.Background(), r); err == nil {
				t.Fatal("stale/unsupported import accepted")
			}
			ms, err := s.ListMissions()
			if err != nil || len(ms) != 0 {
				t.Fatal(ms, err)
			}
			entries, err := os.ReadDir(data)
			if err != nil || len(entries) != 0 {
				t.Fatal(entries, err)
			}
		})
	}
}
