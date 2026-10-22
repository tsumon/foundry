package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/repositorytask"
	"foundry/internal/testutil"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRepositoryTaskIgnoresWorkingTreeAndFilters(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "owned-source")
	if err := seedRepositoryTask(repo, "labels"); err != nil {
		t.Fatal(err)
	}
	cmd := testutil.Git(t, "config", "filter.fixture.process", "/synthetic/never-execute")
	cmd.Dir = repo
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(b))
	}
	// Select the nonexistent filter for the dirty source, not merely configure
	// an unused handler. Committed-object import must never invoke it.
	if err := os.WriteFile(filepath.Join(repo, ".git/info/attributes"), []byte("internal/label/label.go filter=fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal/label/label.go"), []byte("dirty working bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := repositorySourceDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	r, err := startRepositoryTasksAcceptance(repositoryWeb(t), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m, _ := r.store.GetMission(r.scenarios["repository_imported"])
	frozen, err := readRepositoryTaskTree(filepath.Join(r.Root, "managed", "missions", m.ID, "base"))
	if err != nil || frozen["internal/label/label.go"] != "package label\n\nfunc Normalize(s string) string { return s }\n" {
		t.Fatal("working bytes imported", err)
	}
	after, err := repositorySourceDigest(repo)
	if err != nil || before != after {
		t.Fatal("source/index modified", err)
	}
}

func repositorySourceDigest(path string) (string, error) {
	// Only freshly owned test fixture roots enter this helper. Hash raw source
	// and Git metadata (including index/HEAD); never invoke status or filters.
	h := sha256.New()
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		raw, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(path, p)
		if e != nil {
			return e
		}
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(raw))
		_, e = h.Write(raw)
		return e
	})
	return fmt.Sprintf("%x", h.Sum(nil)), err
}

func TestRepositoryTaskExhaustionAndExplicitReplan(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "owned-source")
	if err := seedRepositoryTask(repo, "labels"); err != nil {
		t.Fatal(err)
	}
	raw, err := readRepositoryDescriptor(repo)
	if err != nil {
		t.Fatal(err)
	}
	d, err := repositorytask.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	first := "package label\n\nfunc Normalize(s string) string { return \"first-wrong\" }\n"
	second := "package label\n\nfunc Normalize(s string) string { return \"second-wrong\" }\n"
	d.Repairs = [][]repositorytask.Edit{{{Path: "internal/label/label.go", Content: &first}}, {{Path: "internal/label/label.go", Content: &second}}}
	raw, _ = json.Marshal(d)
	if err := os.WriteFile(filepath.Join(repo, repositorytask.Descriptor), raw, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := testutil.Git(t, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-qam", "scripted exhaustion")
	cmd.Dir = repo
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(b))
	}
	r, err := startRepositoryTasksAcceptance(repositoryWeb(t), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m, _ := r.store.GetMission(r.scenarios["repository_imported"])
	c := newAcceptanceAPIClient(t, r.URL)
	base := "/api/missions/" + m.ID
	var res struct {
		Mission domain.Mission `json:"mission"`
	}
	o := m.C4Operations[0]
	for _, action := range []string{"approve", "execute"} {
		c.json("POST", base+"/c4", map[string]any{"action": action, "revision": m.Revision, "operation_id": o.ID, "material_digest": o.MaterialDigest}, 200, &res)
		m = res.Mission
	}
	c.json("POST", base+"/plan", map[string]any{"revision": m.Revision}, 200, &res)
	m, err = waitSyntheticMission(r.store, m.ID, "awaiting_approval", "blocked")
	if err != nil || m.State != "awaiting_approval" {
		t.Fatal(err, m.Error)
	}
	c.json("POST", base+"/approve", map[string]any{"revision": m.Revision}, 200, &res)
	m, err = waitSyntheticMission(r.store, m.ID, "blocked", "awaiting_delivery")
	if err != nil || m.State != "blocked" {
		t.Fatal(err, m.State)
	}
	if len(m.Evidence) != 3 || len(m.C4Operations) != 4 || len(m.Reviews) != 0 || !strings.Contains(m.Error, "maximum two repair rounds") {
		t.Fatal("did not exhaust exactly two repairs", m.Error, m.CodingAttempts)
	}
	seen := map[string]bool{}
	for _, v := range m.Evidence {
		if v.Passed || seen[v.CandidateDigest] {
			t.Fatal("unchanged/repeated/passing repair")
		}
		seen[v.CandidateDigest] = true
	}
	original := append([]domain.Verification{}, m.Evidence...)
	old := m.Revision
	c.json("POST", base+"/deliver", map[string]any{"revision": m.Revision}, 409, &res)
	c.json("POST", base+"/plan", map[string]any{"revision": m.Revision}, 200, &res)
	m, err = waitSyntheticMission(r.store, m.ID, "awaiting_approval", "blocked")
	if err != nil || m.State != "awaiting_approval" {
		t.Fatal("replan", err, m.Error)
	}
	if m.ReplanContext == nil || len(m.Plan.Adjustments) != len(m.ReplanContext.Failures) || len(m.RunArchives) != 1 || !reflect.DeepEqual(original, m.RunArchives[0].Evidence) || len(m.Evidence) != 0 {
		t.Fatal("replan lost frozen failure evidence")
	}
	ids := map[string]bool{}
	for _, f := range m.ReplanContext.Failures {
		ids[f.ID] = true
	}
	for _, a := range m.Plan.Adjustments {
		if !ids[a.FailureID] || len(a.StepIDs) != 1 {
			t.Fatal("unbound failure mapping", a)
		}
		delete(ids, a.FailureID)
	}
	if len(ids) != 0 {
		t.Fatal("unmapped failure")
	}
	c.json("POST", base+"/approve", map[string]any{"revision": old}, 409, &res)
}

func TestRepositoryTaskProtectedDriftBlocksDispatch(t *testing.T) {
	r, err := startRepositoryTasksAcceptance(repositoryWeb(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m, _ := r.store.GetMission(r.scenarios["repository_labels"])
	ctx := context.Background()
	o := m.C4Operations[0]
	m, err = r.engine.ApproveC4Baseline(ctx, m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	m, err = r.engine.ExecuteC4Baseline(ctx, m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.engine.Plan(ctx, m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m, err = waitSyntheticMission(r.store, m.ID, "awaiting_approval", "blocked")
	if err != nil || m.State != "awaiting_approval" {
		t.Fatal(err, m.Error)
	}
	protected := filepath.Join(r.Root, "managed", "missions", m.ID, "generations", m.CandidateGeneration.ID, "tree", "go.mod")
	if err := os.WriteFile(protected, []byte("module tampered\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := newAcceptanceAPIClient(t, r.URL)
	// Existing controller maps on-disk integrity failure to a generic 500;
	// it must still reject before reserving or dispatching any work.
	c.json("POST", "/api/missions/"+m.ID+"/approve", map[string]any{"revision": m.Revision}, 500, nil)
	after, _ := r.store.GetMission(m.ID)
	if len(after.C4Operations) != 1 || len(after.CodingAttempts) != 0 || after.Budget.UsedCalls != m.Budget.UsedCalls {
		t.Fatal("drift dispatched")
	}
}

func TestRepositoryTaskUnsupportedPlanLeavesUnknownReplanUnresolved(t *testing.T) {
	r, err := startRepositoryTasksAcceptance(repositoryWeb(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m, _ := r.store.GetMission(r.scenarios["repository_labels"])
	ctx := context.Background()
	o := m.C4Operations[0]
	m, err = r.engine.ApproveC4Baseline(ctx, m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	m, err = r.engine.ExecuteC4Baseline(ctx, m.ID, m.Revision, o.ID, o.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.engine.Plan(ctx, m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m, err = waitSyntheticMission(r.store, m.ID, "awaiting_approval", "blocked")
	if err != nil || m.State != "awaiting_approval" {
		t.Fatal(err, m.Error)
	}
	request := engine.PlanRevisionRequest{ExpectedMissionRevision: m.Revision, ExpectedPlanRevision: m.Plan.Revision, Reason: "Unsupported descriptor path must stop", Plan: engine.EditablePlan{Summary: "Unsupported proposal", Steps: []engine.EditableStep{{ID: "new", Title: "New", Description: "Not supplied by descriptor", Files: []string{"internal/label/undeclared.go"}}}}}
	m, _, err = r.engine.RevisePlan(ctx, m.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	c := newAcceptanceAPIClient(t, r.URL)
	base := "/api/missions/" + m.ID
	var res struct {
		Mission domain.Mission `json:"mission"`
	}
	c.json("POST", base+"/approve", map[string]any{"revision": m.Revision}, 200, &res)
	m, err = waitSyntheticMission(r.store, m.ID, "blocked", "awaiting_delivery")
	if err != nil || m.State != "blocked" || len(m.Evidence) != 0 || len(m.CodingAttempts) != 1 || m.CodingAttempts[0].Status != "outcome_unknown" {
		t.Fatal("unsupported proposal did not stop", err, m.Error, m.CodingAttempts)
	}
	c.json("POST", base+"/plan", map[string]any{"revision": m.Revision}, 200, &res)
	m, err = waitSyntheticMission(r.store, m.ID, "awaiting_approval", "blocked")
	if err != nil || m.State != "awaiting_approval" {
		t.Fatal("replan", err, m.Error)
	}
	unresolved := false
	for _, a := range m.Plan.Adjustments {
		for _, f := range m.ReplanContext.Failures {
			if a.FailureID == f.ID && f.Code == "outcome_unknown" {
				unresolved = a.Unresolved
			}
		}
	}
	if !unresolved {
		t.Fatal("unknown outcome presented as resolved", m.Plan.Adjustments)
	}
	c.json("POST", base+"/deliver", map[string]any{"revision": m.Revision}, 409, nil)
}

func TestRepositoryTaskRuntimeRejectsRealAndControlledProfiles(t *testing.T) {
	r, err := startRepositoryTasksAcceptance(repositoryWeb(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m, _ := r.store.GetMission(r.scenarios["repository_labels"])
	for _, mutate := range []func(*domain.C4Profile){func(p *domain.C4Profile) { p.Mode = "docker" }, func(p *domain.C4Profile) { p.ControlVersion = 1 }} {
		spec := m.Spec
		p := *spec.C4
		mutate(&p)
		spec.C4 = &p
		if _, err := r.engine.Create(context.Background(), engine.CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: m.RepositoryImport.InspectionID, Digest: m.RepositoryImport.InspectionDigest}, CodingVersion: 1, WorkspaceMode: m.WorkspaceMode, RepoPath: m.RepoPath, Title: "forbidden", Goal: m.Goal, Spec: spec}); err == nil {
			t.Fatal("real/controlled repository task accepted")
		}
	}
	after, _ := r.store.GetMission(m.ID)
	if !reflect.DeepEqual(m, after) {
		t.Fatal("rejected task changed original")
	}
}

func TestRepositoryTaskBoundedReadersRejectSymlinksAndOversize(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, repositorytask.Descriptor)); err != nil {
		t.Fatal(err)
	}
	if _, err := readRepositoryDescriptor(root); err == nil {
		t.Fatal("symlink descriptor read")
	}
	if _, err := readRepositoryTaskTree(root); err == nil {
		t.Fatal("symlink tree read")
	}
	large := t.TempDir()
	if err := os.WriteFile(filepath.Join(large, repositorytask.Descriptor), []byte(strings.Repeat("x", repositorytask.MaxDescriptorBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRepositoryDescriptor(large); err == nil {
		t.Fatal("oversized descriptor read")
	}
}

func repositoryWeb(t *testing.T) string {
	t.Helper()
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return web
}

func TestRepositoryTaskFlagsRejectAmbiguity(t *testing.T) {
	for _, v := range []struct {
		controls, repository bool
		path                 string
	}{{true, true, ""}, {false, false, "/tmp/source"}, {true, false, "/tmp/source"}} {
		if validateRepositoryTaskFlags(v.controls, v.repository, v.path) == nil {
			t.Fatal("ambiguous fixture flags accepted", v)
		}
	}
	for _, v := range []struct {
		controls, repository bool
		path                 string
	}{{true, false, ""}, {false, true, ""}, {false, true, "/tmp/source"}, {false, false, ""}} {
		if err := validateRepositoryTaskFlags(v.controls, v.repository, v.path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRepositoryTaskHTTPApprovalRepairDelivery(t *testing.T) {
	r, err := startRepositoryTasksAcceptance(repositoryWeb(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.router != nil || r.siwc != nil || r.mcp != nil || r.protocol != nil || r.gates != nil || len(r.scenarios) != 2 {
		t.Fatal("not isolated", r.scenarios)
	}
	if !strings.Contains(r.manifest().Label, "REPOSITORY TASK") || strings.Contains(r.manifest().Label, "SIWC") {
		t.Fatal(r.manifest())
	}
	c := newAcceptanceAPIClient(t, r.URL)
	for _, scenario := range []string{"repository_labels", "repository_pricing"} {
		t.Run(scenario, func(t *testing.T) {
			m, err := r.store.GetMission(r.scenarios[scenario])
			if err != nil {
				t.Fatal(err)
			}
			if len(m.C4Operations) != 1 || m.C4Operations[0].Approval != nil || len(m.Approvals) != 1 || m.Approvals[0].Kind != "snapshot" || m.Plan != nil || m.Budget.UsedCalls != 0 || m.Spec.C4.ControlVersion != 0 {
				t.Fatal("startup granted authority")
			}
			before, err := readRepositoryTaskTree(filepath.Join(r.Root, "managed", "missions", m.ID, "base"))
			if err != nil {
				t.Fatal(err)
			}
			sourceBefore, err := repositorySourceDigest(m.RepoPath)
			if err != nil {
				t.Fatal(err)
			}
			base := "/api/missions/" + m.ID
			var res struct {
				Mission domain.Mission `json:"mission"`
			}
			action := func(name string, revision int64, want int) {
				t.Helper()
				c.json("POST", base+"/"+name, map[string]any{"revision": revision}, want, &res)
				if want == 200 {
					m = res.Mission
				}
			}
			baseline := func(action string, want int) {
				t.Helper()
				o := m.C4Operations[0]
				c.json("POST", base+"/c4", map[string]any{"action": action, "revision": m.Revision, "operation_id": o.ID, "material_digest": o.MaterialDigest}, want, &res)
				if want == 200 {
					m = res.Mission
				}
			}
			action("plan", m.Revision, 409)
			action("approve", m.Revision, 409)
			action("deliver", m.Revision, 409)
			baseline("execute", 409)
			baseline("approve", 200)
			if m.C4Operations[0].Dispatch != nil {
				t.Fatal("approval dispatched")
			}
			baseline("execute", 200)
			if !m.C4Operations[0].Passed() || len(m.Evidence) != 0 {
				t.Fatal("baseline not separate")
			}
			action("plan", m.Revision, 200)
			m, err = waitSyntheticMission(r.store, m.ID, "awaiting_approval", "blocked")
			if err != nil || m.State != "awaiting_approval" {
				t.Fatal("plan", err, m.Error)
			}
			oldRevision := m.Revision
			steps := []engine.EditableStep{}
			for _, s := range m.Plan.Steps {
				steps = append(steps, engine.EditableStep{ID: s.ID, Title: "Reviewed: " + s.Title, Description: s.Description, Files: s.Files, DependsOn: s.DependsOn})
			}
			request := engine.PlanRevisionRequest{ExpectedMissionRevision: m.Revision, ExpectedPlanRevision: m.Plan.Revision, Reason: "Explicit human review of scripted proposal", Plan: engine.EditablePlan{Summary: "Human reviewed local simulation", Steps: steps}}
			c.json("POST", base+"/plan-revisions", request, 200, &res)
			m = res.Mission
			action("approve", oldRevision, 409)
			action("deliver", m.Revision, 409)
			action("approve", m.Revision, 200)
			m, err = waitSyntheticMission(r.store, m.ID, "awaiting_delivery", "blocked")
			if err != nil || m.State != "awaiting_delivery" {
				t.Fatal("execute", err, m.Error, m.CodingAttempts)
			}
			if len(m.C4Operations) != 3 || m.C4Operations[1].Result.Class != "test_failed" || !m.C4Operations[2].Passed() || m.C4Operations[1].CandidateDigest == m.C4Operations[2].CandidateDigest || len(m.Evidence) != 2 || len(m.Reviews) != 2 || m.Delivery != nil {
				t.Fatal("missing repair/review evidence", m)
			}
			for _, v := range m.Evidence {
				if v.SourceKind != "synthetic_stub" {
					t.Fatal(v)
				}
			}
			for _, review := range m.Reviews {
				if !review.Passed || review.BindingDigest == "" || len(review.Checks) == 0 {
					t.Fatal("unbound review", review)
				}
			}
			action("deliver", m.Revision-1, 409)
			action("deliver", m.Revision, 200)
			patch, err := os.ReadFile(m.Delivery.PatchPath)
			if err != nil || m.State != "delivered" {
				t.Fatal(err, m)
			}
			if scenario == "repository_pricing" && (!strings.Contains(string(patch), "internal/pricing/price.go") || !strings.Contains(string(patch), "internal/pricing/format.go") || !strings.Contains(string(patch), "new file mode")) {
				t.Fatal("missing multifile patch", string(patch))
			}
			sourceAfter, err := repositorySourceDigest(m.RepoPath)
			if err != nil || sourceBefore != sourceAfter {
				t.Fatal("source Git changed", err)
			}
			after, err := readRepositoryTaskTree(filepath.Join(r.Root, "managed", "missions", m.ID, "base"))
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("frozen base changed", err)
			}
		})
	}
}

func TestRepositoryTaskExistingSourceReadOnly(t *testing.T) {
	r, err := startRepositoryTasksAcceptance(repositoryWeb(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m, _ := r.store.GetMission(r.scenarios["repository_pricing"])
	before, err := repositorySourceDigest(m.RepoPath)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := startRepositoryTasksAcceptance(repositoryWeb(t), m.RepoPath)
	if err != nil {
		t.Fatal(err)
	}
	defer imported.Close()
	if len(imported.scenarios) != 1 {
		t.Fatal(imported.scenarios)
	}
	task, _ := imported.store.GetMission(imported.scenarios["repository_imported"])
	if task.Goal != m.Goal || task.RepositoryImport.BaseDigest != m.RepositoryImport.BaseDigest || task.C4Operations[0].Approval != nil {
		t.Fatal("different import")
	}
	after, err := repositorySourceDigest(m.RepoPath)
	if err != nil || before != after {
		t.Fatal("source changed", err)
	}
	if err := os.WriteFile(filepath.Join(m.RepoPath, "untracked.txt"), []byte("dirty"), 0600); err != nil {
		t.Fatal(err)
	}
	other, err := startRepositoryTasksAcceptance(repositoryWeb(t), m.RepoPath)
	if err != nil {
		t.Fatal("committed-only import consulted untracked bytes", err)
	}
	other.Close()
	if err := os.WriteFile(filepath.Join(m.RepoPath, repositorytask.Descriptor), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if rejected, err := startRepositoryTasksAcceptance(repositoryWeb(t), m.RepoPath); err == nil {
		rejected.Close()
		t.Fatal("uncommitted descriptor accepted")
	}
}

func TestRepositoryTaskRestartDoesNotReplay(t *testing.T) {
	r, err := startRepositoryTasksAcceptance(repositoryWeb(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m, _ := r.store.GetMission(r.scenarios["repository_labels"])
	before := m
	if err := r.engine.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ = r.store.GetMission(m.ID)
	if m.State != "blocked" || m.Epoch != before.Epoch+1 || m.C4Operations[0].Result == nil || m.C4Operations[0].Result.Class != "outcome_unknown" || m.Budget.UsedCalls != 0 || !reflect.DeepEqual(before.Approvals, m.Approvals) || len(m.Evidence) != 0 || m.C4Operations[0].Dispatch != nil {
		t.Fatal("recovery granted/replayed work")
	}
}
