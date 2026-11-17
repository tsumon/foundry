package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdeaProjectCreatesWithoutRepositoryOrCalls(t *testing.T) {
	e, original, _, instances := codingV2Import(t)
	q := IdeaCreateRequest{Title: "Counter", Goal: "Build a counter", Acceptance: []string{"Click increments the count"}, ProfileID: "synthetic", MaxCalls: 12}
	// Use the fixture's configured profile, without executing a model or VM.
	q.ProfileID = original.Spec.ProfileID
	m, err := e.CreateIdea(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if m.IdeaProject == nil || m.IdeaProject.Template != "browser_app_v1" || m.Budget.UsedCalls != 0 {
		t.Fatalf("unexpected creation %+v", m)
	}
	if m.RepositoryImport == nil || m.Spec.C4 == nil || m.Spec.C4.SchemaVersion != 2 {
		t.Fatal("missing existing engine binding")
	}
	raw, _ := json.Marshal(m)
	var restored domain.Mission
	if json.Unmarshal(raw, &restored) != nil || restored.IdeaProject == nil {
		t.Fatal("origin did not persist")
	}
	entries, _ := os.ReadDir(instances)
	if len(entries) != 0 {
		t.Fatal("VM started")
	}
	root, err := e.selectedCandidate(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "src/index.html")); err != nil {
		t.Fatal(err)
	}
	if err = workspace.ValidateEditPath("tests/app.test.cjs", m.Spec.Scope, m.Spec.Protected); err == nil {
		t.Fatal("test editable")
	}
	preview, err := e.AppPreview(m.ID)
	if err != nil || preview.CandidateDigest != m.CandidateDigest {
		t.Fatalf("preview %v", err)
	}
	if _, err = e.AppSource(m.ID); err == nil {
		t.Fatal("undelivered source exported")
	}
	os.WriteFile(filepath.Join(root, "src/app.js"), []byte("tampered"), 0644)
	if _, err = e.AppPreview(m.ID); err == nil {
		t.Fatal("tampered preview accepted")
	}
}

func TestCodingRecoveryPreservesOrdinaryHistory(t *testing.T) {
	old, s, _, _, m := setup(t)
	before, _ := json.Marshal(m)
	configured, _, _, _ := codingV2Import(t)
	old.options.C4Controlled = configured.options.C4Controlled
	old.options.CodingWorkbench = true
	if err := CheckCodingRecoveryModelSnapshots(s); err != nil {
		t.Fatal(err)
	}
	if err := old.CheckC4ControlledRecovery(); err != nil {
		t.Fatal(err)
	}
	if err := old.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(got)
	if string(before) != string(after) {
		t.Fatal("historical task rewritten")
	}
	if _, err = old.Plan(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("ordinary runtime execution admitted")
	}
}

func TestIdeaProjectSyntheticRepairDeliveryAndArchive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "persistent.db")
	persistent, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { persistent.Close() })
	e, q, _, _ := codingV2ImportProfile(t, "c4_node_model_v2", persistent)
	outputs := []string{`{"summary":"synthetic counter observations","findings":[],"unknowns":[]}`, `{"summary":"Build requested counter","steps":[{"id":"counter","title":"Counter","description":"Add incrementing counter with reset","depends_on":[],"files":["src/index.html","src/app.js","src/checks.js"]}]}`, `{"summary":"synthetic failing counter","edits":[{"path":"src/index.html","content":"<!doctype html><html lang=\"en\"><head><title>Counter</title><link rel=\"stylesheet\" href=\"style.css\"></head><body><main><h1>Counter</h1><output id=\"count\">0</output><button id=\"add\">Add</button><button id=\"reset\">Reset</button></main><script src=\"app.js\"></script></body></html>"},{"path":"src/app.js","content":"// broken synthetic candidate\nfunction createCounter(){let value=0;return {get value(){return value;},increment(){return --value;},reset(){value=0;return value;}};}if(typeof module!=='undefined')module.exports={createCounter};if(typeof document!=='undefined'){const counter=createCounter();document.getElementById('add').onclick=()=>{document.getElementById('count').textContent=String(counter.increment());};document.getElementById('reset').onclick=()=>{document.getElementById('count').textContent=String(counter.reset());};}"},{"path":"src/checks.js","content":"const {test}=require('node:test');const assert=require('node:assert/strict');const {createCounter}=require('./app.js');test('candidate-authored: Add increments by one',()=>{const c=createCounter();assert.equal(c.increment(),1);assert.equal(c.increment(),2);});test('candidate-authored: Reset sets zero',()=>{const c=createCounter();c.increment();assert.equal(c.reset(),0);assert.equal(c.value,0);});"}]}`, `{"summary":"synthetic counter repair","edits":[{"path":"src/app.js","content":"function createCounter(){let value=0;return {get value(){return value;},increment(){return ++value;},reset(){value=0;return value;}};}if(typeof module!=='undefined')module.exports={createCounter};if(typeof document!=='undefined'){const counter=createCounter();const output=document.getElementById('count');document.getElementById('add').onclick=()=>{output.textContent=String(counter.increment());};document.getElementById('reset').onclick=()=>{output.textContent=String(counter.reset());};}"}]}`, codingPass, codingPass}
	gateway := &backendV2SyntheticModels{e: e, script: &codingReviewGateway{script: &scripted{outputs: outputs}}}
	e.gateway = gateway
	e.options.C4Controlled.models = gateway
	m, err := e.CreateIdea(context.Background(), IdeaCreateRequest{Title: "Counter", Goal: "Create an incrementing counter and reset", Acceptance: []string{"Add increments by one", "Reset sets count to zero"}, ProfileID: q.Spec.ProfileID, MaxCalls: 16})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err == nil {
		t.Fatal("readiness/baseline bypass")
	}
	if len(gateway.requests) != 0 {
		t.Fatal("unauthorized model call")
	}
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("context bypass")
	}
	if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = e.s.GetMission(m.ID)
	if m.State != "awaiting_approval" {
		t.Fatal(m.State, m.Error)
	}
	if _, err = e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = e.s.GetMission(m.ID)
	if m.State != "awaiting_delivery" || len(m.C4Operations) != 3 || m.C4Operations[1].Result.Class != "test_failed" || !m.C4Operations[2].Passed() || len(gateway.requests) != 6 {
		t.Fatalf("bad repair lifecycle %s %s %+v", m.State, m.Error, m.C4Operations)
	}
	if _, err = e.AppSource(m.ID); err == nil {
		t.Fatal("unapproved export")
	}
	preview, err := e.AppPreview(m.ID)
	if err != nil || !strings.Contains(preview.HTML, "String(counter.increment())") {
		t.Fatal("current preview missing repair", err)
	}
	m, err = e.Deliver(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := e.AppSource(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	extracted := map[string][]byte{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		extracted[f.Name] = b
	}
	if len(extracted) != 9 || !strings.Contains(string(extracted["src/app.js"]), "String(counter.increment())") || len(extracted["tests/app.test.cjs"]) == 0 || len(extracted["package-lock.json"]) == 0 {
		t.Fatal("incomplete archive", extracted)
	}
	html, err := workspace.BundleBrowserApp(extracted)
	if err != nil || html != preview.HTML {
		t.Fatal("restored archive differs", err)
	}
	catalog, err := e.s.Catalog()
	if err != nil || len(catalog.MissionLinks) != 1 || catalog.MissionLinks[0].MissionID != m.ID {
		t.Fatal("project association missing", err)
	}
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, _ := e.s.GetMission(m.ID)
	if restored.IdeaProject == nil {
		t.Fatal("origin lost")
	}
	again, err := e.AppSource(m.ID)
	if err != nil || !bytes.Equal(archive, again) {
		t.Fatal("recovery changed archive", err)
	}
	e.Close()
	persistent.Close()
	reopened, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := New(reopened, e.gateway, e.options)
	defer restarted.Close()
	if err = CheckCodingRecoveryModelSnapshots(reopened); err != nil {
		t.Fatal(err)
	}
	if err = restarted.CheckC4ControlledRecovery(); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	restartedMission, err := reopened.GetMission(m.ID)
	if err != nil || restartedMission.IdeaProject == nil || len(restartedMission.IdeaProject.Acceptance) != 2 {
		t.Fatal("restart origin", err)
	}
	restartedArchive, err := restarted.AppSource(m.ID)
	if err != nil || !bytes.Equal(restartedArchive, archive) {
		t.Fatal("restart export", err)
	}
	root, _ := restarted.selectedCandidate(m)
	os.WriteFile(filepath.Join(root, "src/app.js"), []byte("tampered"), 0644)
	if _, err = restarted.AppSource(m.ID); err == nil {
		t.Fatal("tampered archive accepted")
	}
}

// Only controller-owned fixture bytes execute here. Production dispatch still
// goes exclusively through the separately approved VM verification path.
func TestBrowserSeedOwnedNodeContractAndRequirementAssertions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable for controller-owned fixture")
	}
	dir := t.TempDir()
	seed := browserAppSeed(&domain.IdeaProject{SchemaVersion: 1, Template: "browser_app_v1", Acceptance: []string{"counter increments"}})
	for p, b := range seed {
		name := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(name), 0700)
		os.WriteFile(name, []byte(b), 0644)
	}
	run := func() ([]byte, error) {
		cmd := exec.Command(node, "--test", filepath.Join(dir, "tests/app.test.cjs"))
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}
	if out, err := run(); err != nil {
		t.Fatalf("owned baseline smoke: %v\n%s", err, out)
	}
	checks := `const {test}=require('node:test');const assert=require('node:assert/strict');const {createCounter}=require('./app.js');test('candidate-authored acceptance: increment',()=>{const c=createCounter();assert.equal(c.increment(),1);assert.equal(c.increment(),2);});test('candidate-authored acceptance: reset',()=>{const c=createCounter();c.increment();assert.equal(c.reset(),0);assert.equal(c.value,0);});`
	os.WriteFile(filepath.Join(dir, "src/checks.js"), []byte(checks), 0644)
	good := seed["src/app.js"] + "\n// Normal comment with example URL https://example.invalid shown as text\n"
	bad := strings.Replace(good, "return ++value", "return --value", 1)
	os.WriteFile(filepath.Join(dir, "src/app.js"), []byte(bad), 0644)
	if out, err := run(); err == nil || !strings.Contains(string(out), "increment") {
		t.Fatalf("actual assertion did not fail: %v\n%s", err, out)
	}
	os.WriteFile(filepath.Join(dir, "src/app.js"), []byte(good), 0644)
	if out, err := run(); err != nil {
		t.Fatalf("actual repaired assertions failed: %v\n%s", err, out)
	}
	// Verify localStorage + DOMContentLoaded smoke succeeds.
	domApp := `function createCounter(){let value=0;return {get value(){return value;},increment(){return ++value;},reset(){value=0;return value;}};}
if(typeof module!=="undefined")module.exports={createCounter};
if(typeof document!=="undefined"){document.addEventListener('DOMContentLoaded',()=>{localStorage.setItem('k','1');document.getElementById('action').addEventListener('click',()=>{document.getElementById('status').textContent=localStorage.getItem('k');});});}`
	os.WriteFile(filepath.Join(dir, "src/app.js"), []byte(domApp), 0644)
	if out, err := run(); err != nil {
		t.Fatalf("DOMContentLoaded + localStorage smoke failed: %v\n%s", err, out)
	}
	// Verify missing DOM element ID fails in VM smoke before delivery.
	typoApp := strings.Replace(domApp, "getElementById('action')", "getElementById('missing-typo')", 1)
	os.WriteFile(filepath.Join(dir, "src/app.js"), []byte(typoApp), 0644)
	if out, err := run(); err == nil {
		t.Fatalf("missing DOM id passed smoke: %s", out)
	}
	os.WriteFile(filepath.Join(dir, "src/app.js"), []byte(domApp), 0644)
	// Verify disallowed HTML/module/missing-CSS fails in VM contract before delivery.
	origHTML := seed["src/index.html"]
	for _, badHTML := range []string{
		strings.Replace(origHTML, "</main>", `<iframe src="about:blank"></iframe></main>`, 1),
		strings.Replace(origHTML, `<script src="app.js"></script>`, `<script type="module" src="app.js"></script>`, 1),
		strings.Replace(origHTML, `href="style.css"`, `href="missing.css"`, 1),
	} {
		os.WriteFile(filepath.Join(dir, "src/index.html"), []byte(badHTML), 0644)
		if out, err := run(); err == nil {
			t.Fatalf("disallowed HTML passed VM contract: %s\n%s", badHTML, out)
		}
	}
	os.WriteFile(filepath.Join(dir, "src/index.html"), []byte(origHTML), 0644)
}

func TestIdeaProjectRejectsOriginAndProtectedTampering(t *testing.T) {
	e, q, _, _ := codingV2Import(t)
	m, err := e.CreateIdea(context.Background(), IdeaCreateRequest{Title: "App", Goal: "Build", Acceptance: []string{"offline"}, ProfileID: q.Spec.ProfileID, MaxCalls: 12})
	if err != nil {
		t.Fatal(err)
	}
	forged := m
	origin := *m.IdeaProject
	origin.Acceptance = []string{"forged"}
	forged.IdeaProject = &origin
	if err = e.validateIdeaOrigin(forged); err == nil {
		t.Fatal("forged origin accepted")
	}
	root, err := e.selectedCandidate(m)
	if err != nil {
		t.Fatal(err)
	}
	contract, _, _, err := e.resolveV2Material(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "tests/app.test.cjs"), []byte("// modified fixed harness"), 0644)
	digest, err := workspace.Digest(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workspace.ReadAppFiles(root, digest, contract); err == nil {
		t.Fatal("modified fixed test accepted")
	}
	if err = workspace.ValidateEditPath("outside.js", m.Spec.Scope, m.Spec.Protected); err == nil {
		t.Fatal("out-of-scope path accepted")
	}
}
func TestIdeaProjectInvalidSelectionLeavesNoSeed(t *testing.T) {
	e, _, _, _ := codingV2Import(t)
	_, err := e.CreateIdea(context.Background(), IdeaCreateRequest{Title: "App", Goal: "Build", Acceptance: []string{"offline"}, ProfileID: "absent-profile", MaxCalls: 12})
	if err == nil {
		t.Fatal("unknown profile accepted")
	}
	entries, err := os.ReadDir(e.c4Root + "-projects")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("invalid selection left projects")
	}
}

func TestIdeaProjectIgnoresPollutedGitEnvironment(t *testing.T) {
	e, q, _, _ := codingV2Import(t)
	malicious := t.TempDir()
	sentinel := filepath.Join(malicious, "hook-ran")
	os.WriteFile(filepath.Join(malicious, "pre-commit"), []byte("#!/bin/sh\nexit 92\n"), 0755)
	t.Setenv("PATH", malicious)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", malicious)
	t.Setenv("GIT_DIR", filepath.Join(malicious, "not-a-repository"))
	m, err := e.CreateIdea(context.Background(), IdeaCreateRequest{Title: "Owned app", Goal: "Build offline", Acceptance: []string{"works"}, ProfileID: q.Spec.ProfileID, MaxCalls: 12})
	if err != nil || m.IdeaProject == nil {
		t.Fatal("ambient git environment affected creation", err)
	}
	if _, err = os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("ambient hook executed")
	}
}

func TestIdeaProjectStandaloneHTMLAndContinueIteration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "persistent.db")
	persistent, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { persistent.Close() })
	e, q, _, _ := codingV2ImportProfile(t, "c4_node_model_v2", persistent)
	outputs := []string{
		`{"summary":"obs","findings":[],"unknowns":[]}`,
		`{"summary":"Counter v1","steps":[{"id":"counter","title":"Counter","description":"Build counter","depends_on":[],"files":["src/app.js","src/checks.js"]}]}`,
		`{"summary":"working counter","edits":[{"path":"src/app.js","content":"function createCounter(){let value=0;return {get value(){return value;},increment(){return ++value;},reset(){value=0;return value;}};}if(typeof module!=='undefined')module.exports={createCounter};if(typeof document!=='undefined'){document.getElementById('action').addEventListener('click',()=>{localStorage.setItem('count',String(createCounter().increment()));});}"},{"path":"src/checks.js","content":"const {test}=require('node:test');const assert=require('node:assert/strict');const {createCounter}=require('./app.js');test('v1 counter',()=>{const c=createCounter();assert.equal(c.increment(),1);assert.equal(c.reset(),0);});"}]}`,
		codingPass, codingPass,
	}
	gateway := &backendV2SyntheticModels{e: e, script: &codingReviewGateway{script: &scripted{outputs: outputs}}}
	e.gateway = gateway
	e.options.C4Controlled.models = gateway
	v1, err := e.CreateIdea(context.Background(), IdeaCreateRequest{Title: "Counter App", Goal: "Build counter", Acceptance: []string{"Increment works"}, ProfileID: q.Spec.ProfileID, MaxCalls: 16})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.AppStandaloneHTML(v1.ID); err == nil {
		t.Fatal("undelivered standalone html exported")
	}
	v1 = controlReady(t, e, v1)
	v1 = c4Baseline(t, e, v1)
	if _, err = e.PlanControlled(context.Background(), v1.ID, v1.Revision, true); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	v1, _ = e.s.GetMission(v1.ID)
	if _, err = e.Approve(context.Background(), v1.ID, v1.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	v1, _ = e.s.GetMission(v1.ID)
	v1, err = e.Deliver(context.Background(), v1.ID, v1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	standalone, err := e.AppStandaloneHTML(v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := e.AppPreview(v1.ID)
	if err != nil || string(standalone) != preview.HTML || !strings.Contains(string(standalone), "localStorage.setItem") {
		t.Fatalf("standalone HTML mismatch: %v", err)
	}
	v1Before, _ := json.Marshal(v1)
	v1ArchiveBefore, err := e.AppSource(v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	callsBefore := len(gateway.requests)
	v2, err := e.ContinueIdea(context.Background(), v1.ID, IdeaCreateRequest{Title: "Counter App v2", Goal: "Add step size selector", Acceptance: []string{"Increment works", "Custom step size works"}, ProfileID: q.Spec.ProfileID, MaxCalls: 12})
	if err != nil {
		t.Fatal(err)
	}
	if len(gateway.requests) != callsBefore {
		t.Fatal("ContinueIdea invoked model")
	}
	if v2.IdeaProject == nil || v2.IdeaProject.SchemaVersion != 2 || v2.IdeaProject.ParentMissionID != v1.ID || v2.Budget.UsedCalls != 0 {
		t.Fatalf("unexpected continuation mission: %+v", v2.IdeaProject)
	}
	v2Root, err := e.selectedCandidate(v2)
	if err != nil {
		t.Fatal(err)
	}
	inheritedJS, err := os.ReadFile(filepath.Join(v2Root, "src/app.js"))
	if err != nil || !strings.Contains(string(inheritedJS), "localStorage.setItem") {
		t.Fatalf("v2 candidate did not inherit v1 src/app.js: %v %s", err, string(inheritedJS))
	}
	catalog, err := e.s.Catalog()
	if err != nil || len(catalog.Projects) != 1 || len(catalog.MissionLinks) != 2 || catalog.MissionLinks[0].ProjectID != catalog.MissionLinks[1].ProjectID {
		t.Fatalf("v2 should share v1 catalog project: %+v", catalog)
	}
	v1AfterMission, _ := e.s.GetMission(v1.ID)
	v1After, _ := json.Marshal(v1AfterMission)
	if string(v1Before) != string(v1After) {
		t.Fatal("ContinueIdea mutated parent mission")
	}
	v1ArchiveAfter, err := e.AppSource(v1.ID)
	if err != nil || !bytes.Equal(v1ArchiveBefore, v1ArchiveAfter) {
		t.Fatal("ContinueIdea mutated parent export")
	}
	nonIdea, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.ContinueIdea(context.Background(), nonIdea.ID, IdeaCreateRequest{Title: "Bad", Goal: "Non-idea", Acceptance: []string{"x"}, ProfileID: q.Spec.ProfileID, MaxCalls: 12}); err == nil {
		t.Fatal("ContinueIdea accepted non-idea mission")
	}
	if _, err = e.ContinueIdea(context.Background(), "00000000000000000000000000000000", IdeaCreateRequest{Title: "Missing", Goal: "Missing", Acceptance: []string{"x"}, ProfileID: q.Spec.ProfileID, MaxCalls: 12}); err == nil {
		t.Fatal("ContinueIdea accepted missing mission")
	}
}
