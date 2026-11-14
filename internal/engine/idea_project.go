package engine

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/platformfs"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type IdeaCreateRequest struct {
	Title      string   `json:"title"`
	Goal       string   `json:"goal"`
	Acceptance []string `json:"acceptance"`
	ProfileID  string   `json:"profile_id"`
	MaxCalls   int      `json:"max_calls"`
}

func (e *Engine) IdeaAvailable() (bool, string) {
	for _, p := range e.CodingProfilesV2() {
		if p.ProfileID == "c4_node_model_v2" && p.Available {
			return true, ""
		}
	}
	return false, "embedded_vm_v2_node_unavailable"
}

// CreateIdea prepares controller-owned bytes only. Inspection/admission do not
// execute project scripts, consult credentials, invoke providers or start VMs.
func (e *Engine) CreateIdea(ctx context.Context, q IdeaCreateRequest) (domain.Mission, error) {
	zero := domain.Mission{}
	if boundedPlanText("title", q.Title, 200) != nil || boundedPlanText("goal", q.Goal, 16000) != nil || strings.TrimSpace(q.ProfileID) == "" || q.MaxCalls < 1 || q.MaxCalls > 16 || len(q.Acceptance) < 1 || len(q.Acceptance) > 24 {
		return zero, domain.ImportInvalidRequest
	}
	for _, a := range q.Acceptance {
		if strings.TrimSpace(a) == "" || len(a) > 2000 || !utf8.ValidString(a) {
			return zero, domain.ImportInvalidRequest
		}
	}
	if ok, _ := e.IdeaAvailable(); !ok {
		return zero, ErrC4Unavailable
	}
	// Reject invalid selections before leaving a persistent seed behind.
	if _, _, _, _, err := e.s.ResolveMissionModelSelection(q.ProfileID); err != nil {
		return zero, err
	}
	catalog, err := e.s.Catalog()
	if err != nil {
		return zero, err
	}
	if len(catalog.Projects) >= 50 {
		return zero, domain.ImportInvalidRequest
	}
	id, err := opaque()
	if err != nil {
		return zero, err
	}
	// Imports deliberately forbid nesting source and workspace data. This private
	// sibling is controller-owned and is never a caller-selected repository.
	projects := e.c4Root + "-projects"
	if err = platformfs.PrivateDirectory(projects); err != nil {
		return zero, err
	}
	canonical, err := filepath.EvalSymlinks(projects)
	if err != nil || canonical != projects {
		return zero, domain.ImportLocation
	}
	repo := filepath.Join(projects, id)
	if err = os.Mkdir(repo, 0700); err != nil {
		return zero, err
	}
	keep := false
	inspectionID := ""
	defer func() {
		if !keep && inspectionID != "" {
			_, opErr := e.s.GetRepositoryImportByInspection(inspectionID)
			keep = !errors.Is(opErr, store.ErrNotFound)
		}
		if !keep {
			_ = os.RemoveAll(repo)
		}
	}()
	origin := &domain.IdeaProject{SchemaVersion: 1, Template: "browser_app_v1", Acceptance: append([]string{}, q.Acceptance...)}
	files := browserAppSeed(origin)
	for p, b := range files {
		name := filepath.Join(repo, p)
		if err = os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return zero, err
		}
		if err = os.WriteFile(name, []byte(b), 0644); err != nil {
			return zero, err
		}
	}
	if err = workspace.InitializeManagedAppRepository(ctx, repo); err != nil {
		return zero, domain.ImportPrepareFailed
	}

	var target CodingProfileTemplateV2
	for _, p := range e.CodingProfilesV2() {
		if p.ProfileID == "c4_node_model_v2" {
			target = p
		}
	}
	if target.EmbeddedVM == nil {
		return zero, ErrC4Unavailable
	}
	input := filepath.Join(repo, ".empty-dependencies")
	if err = os.Mkdir(input, 0700); err != nil {
		return zero, err
	}
	deps, err := e.ImportCodingDependencies(ctx, CodingDependencyImportRequest{RepoPath: repo, ProfileID: target.ProfileID, EmbeddedVM: *target.EmbeddedVM, RuntimeInventoryDigest: target.RuntimeInventoryDigest, InputDirectory: input, Consent: true})
	if err != nil {
		return zero, err
	}
	if err = os.Remove(input); err != nil {
		return zero, err
	}
	spec := domain.RepositoryImportSpec{Scope: []string{"src/", "APP_CONTRACT.md", "idea-project.json"}, Protected: []string{"APP_CONTRACT.md", "idea-project.json"}, Acceptance: []string{target.Verifier}, VerificationV2: &domain.C4ImportSelectionV2{SchemaVersion: 2, ProfileID: target.ProfileID, EmbeddedVM: *target.EmbeddedVM, RuntimeInventoryDigest: target.RuntimeInventoryDigest, AdmissionDigest: deps.AdmissionDigest, Dependencies: deps.Dependencies, SourceRoot: ".", TestRoot: "tests", TestSupportPrefixes: []string{}}}
	inspection, err := e.InspectRepository(ctx, domain.RepositoryInspectionRequest{RepoPath: repo, Spec: spec, Consent: true})
	if err != nil {
		return zero, err
	}
	inspectionID = inspection.ID
	if inspection.Status != "ready" {
		return zero, domain.ImportInputs
	}
	m, err := e.Create(ctx, CreateRequest{ideaProject: origin, ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: inspection.ID, Digest: inspection.Digest}, CodingVersion: 1, WorkspaceMode: "generations_v1", Title: q.Title, Goal: q.Goal, RepoPath: repo, Spec: domain.Spec{Scope: spec.Scope, Protected: spec.Protected, Acceptance: spec.Acceptance, ProfileID: q.ProfileID, MaxCalls: q.MaxCalls, Research: true, C4: &domain.C4Profile{SchemaVersion: 2, ID: target.ProfileID, Mode: "embedded_vm"}}})
	if err != nil {
		return zero, err
	}
	keep = true
	if err = e.ensureIdeaCatalog(m); err != nil {
		return m, err
	}
	return m, nil
}

func browserAppSeed(origin *domain.IdeaProject) map[string]string {
	raw, _ := json.MarshalIndent(origin, "", "  ")
	return map[string]string{
		"idea-project.json": string(raw) + "\n",
		"package.json":      `{"name":"foundry-browser-app","version":"1.0.0"}` + "\n",
		"package-lock.json": `{"name":"foundry-browser-app","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"foundry-browser-app","version":"1.0.0"}}}` + "\n",
		"APP_CONTRACT.md": `# Managed browser application contract
Open src/index.html directly in a browser. Build the requested application in src/index.html, src/style.css and src/app.js. Additional local .html/.css/.js text assets under src/ are supported. src/checks.js is the mutable candidate-authored requirement test module, loaded by the fixed Node harness only; do not include it in index.html. Use classic scripts (optional type="text/javascript" and defer) and relative stylesheet/script URLs. Inline SVG and data:image assets are supported; no external images. No npm dependencies, external URLs, imports, CSS url(), servers or build steps. Keep a complete HTML document with html/head/body, a title and main element. Do not modify tests, package metadata, this contract or idea-project.json.
The protected Node harness verifies document/source structure and classic JavaScript syntax, then loads src/checks.js inside the isolated VM. Implement business logic as browser-compatible CommonJS exports in app.js; guard DOM initialization with typeof document !== "undefined" so Node can require logic without a DOM. Replace the baseline smoke tests in src/checks.js with node:test and node:assert/strict assertions covering each user acceptance requirement, including edge cases. Failed assertions enter the existing test/debugger repair path. These candidate-authored checks are not independent validation: QA/Auditor must separately review their requirements coverage and behavior. User acceptance requirements are frozen in idea-project.json and the mission goal.
`,
		"src/index.html": `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>My application</title><link rel="stylesheet" href="style.css"></head><body><main><h1>My application</h1><p id="status">Your workspace is ready.</p><button id="action" type="button">Try it</button></main><script src="app.js"></script></body></html>`,
		"src/style.css":  `body{font:18px system-ui,sans-serif;margin:0;background:#f4f5f7;color:#17202a}main{max-width:800px;margin:8vh auto;padding:32px}button{font:inherit;padding:10px 20px;cursor:pointer}`,
		"src/app.js":     `function createCounter(){let value=0;return {get value(){return value;},increment(){return ++value;},reset(){value=0;return value;}};}if(typeof module!=="undefined")module.exports={createCounter};if(typeof document!=="undefined"){document.getElementById('action').addEventListener('click',()=>{document.getElementById('status').textContent='Ready to build your idea.';});}`,
		"src/checks.js": `// Candidate-authored baseline smoke; replace with assertions for user requirements.
const {test}=require('node:test');const assert=require('node:assert/strict');const {createCounter}=require('./app.js');test('candidate-authored baseline smoke',()=>{assert.equal(typeof createCounter,'function');});`,
		"tests/app.test.cjs": `const {test}=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const path=require('node:path');const vm=require('node:vm');const root=path.join(__dirname,'..');require('../src/checks.js');test('managed document and classic source contract',()=>{const html=fs.readFileSync(path.join(root,'src/index.html'),'utf8');for(const tag of ['html','head','title','body','main'])assert.match(html,new RegExp('<'+tag+'(?:\\s|>)','i'));function visit(dir){for(const item of fs.readdirSync(dir,{withFileTypes:true})){const p=path.join(dir,item.name);if(item.isDirectory())visit(p);else{assert.ok(/\.(html|css|js)$/.test(item.name),'unsupported application asset');const source=fs.readFileSync(p,'utf8');if(p.endsWith('.js'))new vm.Script(source,{filename:p});if(p.endsWith('.css'))assert.ok(!/(?:url\s*\(|@import)/i.test(source),'CSS assets unsupported');}}}visit(path.join(root,'src'));});`,
	}
}

func (e *Engine) validateIdeaOrigin(m domain.Mission) error {
	if m.IdeaProject == nil {
		return domain.ImportInvalidBinding
	}
	if m.IdeaProject.SchemaVersion != 1 || m.IdeaProject.Template != "browser_app_v1" || m.RepositoryImport == nil || m.Spec.C4 == nil || m.Spec.C4.ID != "c4_node_model_v2" {
		return domain.ImportInvalidBinding
	}
	if err := e.ValidateRepositoryImport(m); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Join(e.dir(m.ID), "base"))
	if err != nil {
		return domain.ImportInvalidBinding
	}
	defer root.Close()
	f, err := root.Open("idea-project.json")
	if err != nil {
		return domain.ImportInvalidBinding
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		return domain.ImportInvalidBinding
	}
	var frozen domain.IdeaProject
	if json.Unmarshal(raw, &frozen) != nil {
		return domain.ImportInvalidBinding
	}
	a, _ := json.Marshal(frozen)
	b, _ := json.Marshal(m.IdeaProject)
	if string(a) != string(b) {
		return domain.ImportInvalidBinding
	}
	return nil
}

// Deterministic project identity lets recovery finish a catalog link interrupted
// after mission commit without duplicating projects or re-running admission.
func (e *Engine) ensureIdeaCatalog(m domain.Mission) error {
	if m.IdeaProject == nil {
		return nil
	}
	projectID := "idea-" + m.ID
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, m.Title)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	description := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return ' '
		}
		return r
	}, m.Goal)
	if r := []rune(description); len(r) > 2000 {
		description = string(r[:2000])
	}
	for attempt := 0; attempt < 4; attempt++ {
		c, err := e.s.Catalog()
		if err != nil {
			return err
		}
		exists := false
		for _, p := range c.Projects {
			if p.ID == projectID {
				exists = true
			}
		}
		if !exists {
			_, err = e.s.UpdateCatalog(c.Revision, "project.create", func(c *domain.CatalogSnapshot) ([]string, error) {
				c.Projects = append(c.Projects, domain.Project{CatalogMeta: domain.CatalogMeta{ID: projectID}, Name: name, Description: description})
				return []string{projectID}, nil
			})
			if errors.Is(err, store.ErrConflict) {
				continue
			}
			if err != nil {
				return err
			}
			continue
		}
		for _, link := range c.MissionLinks {
			if link.MissionID == m.ID {
				return nil
			}
		}
		_, err = e.s.UpdateCatalog(c.Revision, "mission.link", func(c *domain.CatalogSnapshot) ([]string, error) {
			c.MissionLinks = append(c.MissionLinks, domain.MissionProjectLink{MissionID: m.ID, ProjectID: projectID})
			return []string{m.ID}, nil
		})
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		return err
	}
	return store.ErrConflict
}
