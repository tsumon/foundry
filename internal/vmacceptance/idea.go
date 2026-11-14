package vmacceptance

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

// All code in this case is a repository-owned protocol fixture. Only the actual
// installed guest executes the candidate assertions and Linux isolation checks.
func ideaCounterSource(repaired bool) string {
	increment := "--value"
	if repaired {
		increment = "++value"
	}
	return `function createCounter(){let value=0;return {get value(){return value;},increment(){return ` + increment + `;},reset(){value=0;return value;}};}
if(typeof module!=="undefined")module.exports={createCounter};
if(typeof document!=="undefined"){const c=createCounter(),out=document.getElementById('status'),add=document.getElementById('action');out.textContent='0';add.textContent='Increment';add.onclick=()=>out.textContent=String(c.increment());const reset=document.createElement('button');reset.textContent='Reset';reset.onclick=()=>out.textContent=String(c.reset());document.querySelector('main').append(reset);}
`
}

const ideaRequirementChecks = `const {test}=require('node:test');const assert=require('node:assert/strict');const {createCounter}=require('./app.js');
test('candidate-authored: increment adds one',()=>{const c=createCounter();assert.equal(c.increment(),1);assert.equal(c.increment(),2);});
test('candidate-authored: reset returns zero',()=>{const c=createCounter();c.increment();assert.equal(c.reset(),0);assert.equal(c.value,0);});
test('native guest isolation',()=>{const module={exports:{}};
` + nodeGuardsV2 + `
module.exports();});
`

func (f fixtureSpec) testsFor(kind string) int {
	if f.caseID == "idea-app" && kind == "candidate" {
		return 4
	}
	return f.expectedTests
}

func createIdeaAcceptance(ctx context.Context, controller *engine.Engine, s *store.Store, f *fixtureSpec, summary *Summary) (domain.Mission, error) {
	m, err := controller.CreateIdea(ctx, engine.IdeaCreateRequest{Title: "SYNTHETIC model / native VM idea-app", Goal: "Build a browser counter with Increment and Reset controls.", Acceptance: []string{"Increment adds one on every click", "Reset returns the displayed count to zero"}, ProfileID: "native-fixture-profile", MaxCalls: 6})
	if err != nil {
		return m, err
	}
	if m.IdeaProject == nil || m.RepositoryImport == nil || m.Budget.UsedCalls != 0 || len(m.C4Operations) != 0 {
		return m, errors.New("idea_creation_not_managed")
	}
	receipt, err := s.GetRepositoryInspection(m.RepositoryImport.InspectionID)
	if err != nil || receipt.VerificationV2 == nil {
		return m, errors.New("idea_input_contract_missing")
	}
	summary.V2.Inputs = receipt.VerificationV2
	summary.V2.RuntimeInventoryDigest = m.Spec.C4.V2.RuntimeInventoryDigest
	summary.V2.AdmissionDigest = receipt.VerificationV2.AdmissionDigest
	if receipt.VerificationV2.Dependencies.FileCount != 0 || receipt.VerificationV2.Dependencies.TotalBytes != 0 {
		return m, errors.New("idea_dependencies_not_empty")
	}
	for _, p := range []string{"src/index.html", "src/style.css", "src/app.js", "src/checks.js", "tests/app.test.cjs", "package.json", "package-lock.json", "APP_CONTRACT.md", "idea-project.json"} {
		b, e := os.ReadFile(filepath.Join(m.RepoPath, filepath.FromSlash(p)))
		if e != nil {
			return m, e
		}
		f.files[p] = string(b)
	}
	f.initial = f.files[f.path]
	summary.V2.ExpectedFiles = f.fileDigests()
	summary.SourceDigest, err = sourceTreeDigest(m.RepoPath)
	if err != nil {
		return m, err
	}
	summary.ProtectedDigests, err = f.protectedDigests(m.RepoPath)
	if err != nil {
		return m, err
	}
	catalog, err := s.Catalog()
	if err != nil {
		return m, err
	}
	linked := false
	for _, link := range catalog.MissionLinks {
		if link.MissionID == m.ID && link.ProjectID == "idea-"+m.ID {
			linked = true
		}
	}
	if !linked {
		return m, errors.New("idea_catalog_link_missing")
	}
	return m, nil
}

func fixtureRepairTrace(m domain.Mission, f fixtureSpec) bool {
	if f.caseID != "idea-app" {
		return len(m.Changes) == 2 && m.Changes[0].Before == f.initial && m.Changes[0].After == f.worker && m.Changes[1].After == f.repaired
	}
	if len(m.Changes) != 3 {
		return false
	}
	app := []domain.Change{}
	checks := false
	for _, change := range m.Changes {
		if change.Path == f.path {
			app = append(app, change)
		}
		if change.Path == "src/checks.js" && change.Before == f.files["src/checks.js"] && change.After == ideaRequirementChecks {
			checks = true
		}
	}
	return checks && len(app) == 2 && app[0].Before == f.initial && app[0].After == f.worker && app[1].Before == f.worker && app[1].After == f.repaired
}

type IdeaEvidence struct {
	CandidateDigest        string `json:"candidate_digest"`
	PreviewDigest          string `json:"preview_digest"`
	SourceZIPDigest        string `json:"source_zip_digest"`
	Files                  int    `json:"files"`
	UnapprovedSourceDenied bool   `json:"unapproved_source_denied"`
}

func verifyIdeaDelivery(controller *engine.Engine, m domain.Mission, tree string, inputs domain.C4InputContractV2) (*IdeaEvidence, error) {
	preview, err := controller.AppPreview(m.ID)
	if err != nil || preview.CandidateDigest != m.CandidateDigest {
		return nil, errors.New("idea_preview_not_current")
	}
	source, err := controller.AppSource(m.ID)
	if err != nil {
		return nil, err
	}
	archive, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, item := range archive.File {
		reader, e := item.Open()
		if e != nil {
			return nil, e
		}
		b, e := io.ReadAll(io.LimitReader(reader, workspace.MaxSnapshotBytes+1))
		reader.Close()
		if e != nil {
			return nil, e
		}
		files[item.Name] = b
	}
	actual, err := workspace.ReadAppFiles(tree, m.CandidateDigest, inputs)
	if err != nil || !reflect.DeepEqual(files, actual) {
		return nil, errors.New("idea_zip_candidate_mismatch")
	}
	html, err := workspace.BundleBrowserApp(files)
	if err != nil || html != preview.HTML {
		return nil, errors.New("idea_zip_preview_mismatch")
	}
	again, err := controller.AppSource(m.ID)
	if err != nil || !bytes.Equal(source, again) {
		return nil, errors.New("idea_zip_changed")
	}
	return &IdeaEvidence{CandidateDigest: m.CandidateDigest, PreviewDigest: digestBytes([]byte(html)), SourceZIPDigest: digestBytes(source), Files: len(files), UnapprovedSourceDenied: true}, nil
}
