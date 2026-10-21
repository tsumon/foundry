package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func TestRepositoryImportScenariosAndDiagnosticPurity(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	for _, key := range []string{"repository_import_draft", "repository_import_corrupt_evidence", "repository_import_interrupted_operation", "repository_import_binary_repository", "repository_import_project"} {
		if r.scenarios[key] == "" {
			t.Fatalf("missing C3 scenario %s", key)
		}
	}
	c := newAcceptanceAPIClient(t, r.URL)
	var draft struct {
		Mission domain.Mission `json:"mission"`
	}
	c.json("GET", "/api/missions/"+r.scenarios["repository_import_draft"], nil, 200, &draft)
	if draft.Mission.ImportVersion != 1 || draft.Mission.State != "draft" || draft.Mission.Budget.UsedCalls != 0 {
		t.Fatal("import draft was preapproved")
	}
	for _, a := range draft.Mission.Approvals {
		if a.Kind != "snapshot" {
			t.Fatal("import draft carries non-snapshot approval")
		}
	}
	var inspections []domain.RepositoryInspection
	c.json("GET", "/api/repository-imports/inspections", nil, 200, &inspections)
	for _, row := range inspections {
		if row.RepositoryID == r.scenarios["repository_import_binary_repository"] {
			t.Fatal("binary inspected before explicit user action")
		}
	}
	registry, err := r.store.Capabilities()
	if err != nil {
		t.Fatal(err)
	}
	var binary domain.GitRepository
	for _, repo := range registry.Repositories {
		if repo.ID == r.scenarios["repository_import_binary_repository"] {
			binary = repo
		}
	}
	var blocked domain.RepositoryInspection
	spec := domain.RepositoryImportSpec{Scope: []string{"src/"}, Protected: []string{}, Acceptance: []string{codingLoopCommand}, Image: codingLoopImage}
	c.json("POST", "/api/repository-imports/inspections", domain.RepositoryInspectionRequest{RepositoryID: binary.ID, RepositoryRevision: binary.Revision, Spec: spec, Consent: true}, 200, &blocked)
	if blocked.Status != "blocked" || blocked.Code != domain.ImportBinary {
		t.Fatalf("not binary blocker: %+v", blocked)
	}
	op, err := r.store.GetRepositoryImportOperation(r.scenarios["repository_import_interrupted_operation"])
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != "pending" {
		t.Fatal("interruption falsely completed")
	}
	if _, err := r.store.GetMission(op.MissionID); err == nil {
		t.Fatal("interrupted import invented mission")
	}
	for _, key := range []string{"repository_import_draft", "repository_import_corrupt_evidence"} {
		id := r.scenarios[key]
		before := snapshotDiagnosticState(t, r)
		var result struct {
			Diagnostic domain.RecoveryDiagnostic `json:"diagnostic"`
		}
		c.json("GET", "/api/missions/"+id+"/recovery-diagnostic", nil, 200, &result)
		if result.Diagnostic.MissionID != id || result.Diagnostic.Stale || len(result.Diagnostic.Checks) != 10 {
			t.Fatal("bad diagnostic envelope", result)
		}
		if key == "repository_import_corrupt_evidence" {
			found := false
			for _, check := range result.Diagnostic.Checks {
				if (check.ID == "verification" || check.ID == "reviews") && check.Status == domain.RecoveryBlocked {
					found = true
				}
			}
			if !found {
				t.Fatal("tampered evidence not blocked", result)
			}
		}
		if !reflect.DeepEqual(before, snapshotDiagnosticState(t, r)) {
			t.Fatal("diagnosis mutated records/budgets/events")
		}
	}
	manifest, _ := json.Marshal(r.manifest())
	if !strings.Contains(string(manifest), "not_checked") {
		t.Fatal("C4 readiness must remain not_checked")
	}
}

func snapshotDiagnosticState(t *testing.T, r *acceptanceRuntime) []byte {
	t.Helper()
	missions, err := r.store.ListMissions()
	if err != nil {
		t.Fatal(err)
	}
	events, err := r.store.Events(0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	imports, err := r.store.ListRepositoryImportOperations()
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]any{}
	invocations := map[string]any{}
	for _, m := range missions {
		rows, err := r.store.ListLocalOperations(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		operations[m.ID] = rows
		calls, err := r.store.ListInvocations(m.ID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		invocations[m.ID] = calls
	}
	raw, _ := json.Marshal([]any{missions, events, imports, operations, invocations})
	return raw
}

func TestRepositoryImportHTTPReplanDelivery(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	c := newAcceptanceAPIClient(t, r.URL)
	var profiles struct {
		Settings domain.Settings `json:"settings"`
	}
	c.json("GET", "/api/profiles", nil, 200, &profiles)
	if profiles.Settings.DefaultProfileID != "synthetic-stub" {
		t.Fatal("new C3 task does not default to local stub")
	}
	for _, id := range profiles.Settings.RoleBindings {
		if id != "synthetic-stub" {
			t.Fatal("new C3 role overrides local stub")
		}
	}
	req := codingReplanCreateRequest(r.Repo)
	req.Title = "SYNTHETIC C3 HTTP import and C2 replan"
	var receipt domain.RepositoryInspection
	registered, err := r.store.GetGitRepository(r.scenarios["repository_import_repository"])
	if err != nil {
		t.Fatal(err)
	}
	if registered.ProjectID != r.scenarios["repository_import_project"] {
		t.Fatal("fixture project entry not linked")
	}
	c.json("POST", "/api/repository-imports/inspections", domain.RepositoryInspectionRequest{RepositoryID: registered.ID, RepositoryRevision: registered.Revision, Consent: true, Spec: domain.RepositoryImportSpec{Scope: req.Spec.Scope, Protected: []string{}, Acceptance: req.Spec.Acceptance, Image: req.Spec.Image}}, 200, &receipt)
	if receipt.Status != "ready" {
		t.Fatal("not importable", receipt)
	}
	req.ImportVersion, req.ImportSelection = 1, &domain.ImportSelection{InspectionID: receipt.ID, Digest: receipt.Digest}
	var result struct {
		Mission domain.Mission `json:"mission"`
	}
	c.json("POST", "/api/missions", req, 200, &result)
	if result.Mission.RepositoryImport == nil || result.Mission.RepositoryImport.InspectionID != receipt.ID {
		t.Fatal("missing exact binding")
	}
	runCodingReplanWorkflow(t, r, result.Mission.ID)
	m, err := r.store.GetMission(result.Mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(m.Delivery.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	section := strings.Split(string(raw), "## C3 · Frozen repository import")
	if len(section) != 2 {
		t.Fatal("report missing source section")
	}
	blocks := strings.Split(section[1], "```json\n")
	if len(blocks) != 2 {
		t.Fatal("report missing source JSON")
	}
	var bound domain.RepositoryImportBinding
	if err := json.Unmarshal([]byte(strings.Split(blocks[1], "\n```")[0]), &bound); err != nil {
		t.Fatal(err)
	}
	if bound != *m.RepositoryImport {
		t.Fatal("report lacks exact source binding")
	}
	if _, err := os.Stat(filepath.Join(r.Repo, "src/main.txt")); err != nil {
		t.Fatal(err)
	}
	before := snapshotDiagnosticState(t, r)
	var diagnosis struct {
		Diagnostic domain.RecoveryDiagnostic `json:"diagnostic"`
	}
	c.json("GET", "/api/missions/"+m.ID+"/recovery-diagnostic", nil, 200, &diagnosis)
	for _, check := range diagnosis.Diagnostic.Checks {
		if check.Status == domain.RecoveryBlocked {
			t.Fatalf("completed task blocked: %+v", check)
		}
	}
	if !reflect.DeepEqual(before, snapshotDiagnosticState(t, r)) {
		t.Fatal("delivery diagnosis mutated state")
	}
}
